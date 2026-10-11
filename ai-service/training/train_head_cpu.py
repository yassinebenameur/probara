"""Train the decision head from the untuned base on the deployed CPU features.

The encoder stays the pinned base encoder, prepared exactly as production
prepares it (int8 dynamic linear layers); only the head is trained, on
training rows only, from the base's own head (no predecessor). The candidate
is written head-only (compose_artifact.py) to artifacts/<version> and its
composition recorded in <work>/<version>; per-epoch recovery heads go to
<work>/<version>.epochs/, never the artifact. Export is never releasable
here: CPU calibration and the held-out gate run separately in validate_cpu.py.

tools/convert_candidate.py turns the full-checkpoint layout an earlier copy of
this script wrote (model.safetensors + configs) into this same layout.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import time

os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", USE_TF="0")
import torch
from safetensors.torch import save_file
from laya.train import TrainConfig, load_checkpoint, items_from_rows, train_model
from probara_ai.contract import STATE_FORMAT, VERSION
from probara_ai.cpu import CPU_FORMAT, prepare_cpu
from train import read, MAX_LEN, HEAD_LEN
from train_cpu import FrozenCPUEncoder
from upstream import verify_base, package_notices
from compose_artifact import CONFIG, check_version, prepare_output, record_composed, save_head, save_json, write_manifest
from partitions import verify_partitions, verify_rendering


def run(args):
    check_version(args.version)
    if args.output.exists() and any(args.output.iterdir()):
        raise ValueError("Output directory must be new or empty; train a new --version")
    composed = args.work / args.version
    if composed.exists():
        raise ValueError(f"Compose directory {composed} already exists; remove it first")
    verify_base(args.base)
    verify_partitions(args.data, args.data, current_generator=True)
    verify_rendering(args.data, STATE_FORMAT, parts=("train",))
    prepare_output(args.output, args.version)
    torch.set_num_threads(args.threads)
    torch.set_num_interop_threads(1)
    torch.manual_seed(args.seed)
    model, tok, cfg = load_checkpoint(str(args.base))
    model = prepare_cpu(model)
    device = torch.device(args.head_device if args.head_device != "auto" else
                          "cuda" if torch.cuda.is_available() else
                          "mps" if torch.backends.mps.is_available() else "cpu")
    model.encoder = FrozenCPUEncoder(model.encoder)
    rows = sum((read(args.data / f"{task}-train.jsonl") for task in ("rca", "dependency")), [])
    for row in rows:
        if len(tok.encode(row["state"], add_special_tokens=False)) > MAX_LEN - HEAD_LEN:
            raise ValueError("state exceeds token budget")
    items, skipped = items_from_rows(tok, rows, MAX_LEN, HEAD_LEN)
    if skipped or len(items) != 6000:
        raise ValueError("Incomplete training partition")
    config = TrainConfig(epochs=args.epochs, micro_batch=8, grad_accum=4, head_lr=args.lr,
                         freeze_encoder=True, loss="soft-ce", shuffle_options=("choice",),
                         seed=args.seed, max_len=MAX_LEN, head_max_len=HEAD_LEN,
                         log_every=25, amp=False, gradient_checkpointing=False)
    recovery = args.work / f"{args.version}.epochs"
    recovery.mkdir(parents=True, exist_ok=True)
    started = time.perf_counter()

    def epoch_checkpoint(epoch, loss):
        # Head-only recovery files are never deployable artifacts.
        state = {k: v.detach().cpu().contiguous() for k, v in model.state_dict().items() if not k.startswith("encoder.")}
        save_file(state, str(recovery / f"head-epoch-{epoch+1}.safetensors"))
        print(f"CPU epoch {epoch+1}: loss={loss:.6f}; elapsed={time.perf_counter()-started:.1f}s", flush=True)

    print(f"Training head from base on frozen CPU-format features; head device={device}", flush=True)
    history = train_model(model, tok, items, config, device, MAX_LEN, HEAD_LEN, on_epoch_end=epoch_checkpoint)
    # Only the head is exported; composition restores the pinned frozen encoder.
    # Move to CPU float32 first so the fp16 export rounds exactly as
    # laya's save_checkpoint does for a full checkpoint.
    model.encoder = None
    model.float().cpu().eval()
    cfg.update(max_len=MAX_LEN, head_max_len=HEAD_LEN, model_name=args.version, catalogue=VERSION,
               state_format=STATE_FORMAT,
               training={"fine_tuned_from_checkpoint": False, "initialised_from": "base head",
                         "epochs_completed": args.epochs, "encoder_device": "cpu", "head_device": str(device),
                         "seed": config.seed, "loss": config.loss, "freeze_encoder": True, "train_items": len(items)})
    save_head(model, args.output)
    save_json(args.output / CONFIG, cfg)
    package_notices(args.base, args.output)
    shutil.copy(args.data / "provenance.json", args.output / "provenance.json")
    evaluation = {"release_approved": False, "scope": "Controlled scenarios only; no real-world accuracy claim.",
                  "gate_failure": "Separate CPU calibration and held-out validation required.",
                  "loss_history": history, "training_seconds": time.perf_counter() - started}
    (args.output / "evaluation.json").write_text(json.dumps(evaluation, indent=2) + "\n")
    (args.output / "training-config.json").write_text(json.dumps({**vars(config), "head_device": str(device),
        "encoder_device": "cpu", "frozen_encoder_cpu_format": CPU_FORMAT, "state_format": STATE_FORMAT,
        "initialised_from": "base head"}, indent=2) + "\n")
    # No calibration settings or CPU release report exist yet.
    write_manifest(args.output, args.version, STATE_FORMAT, release_approved=False, cpu_format=CPU_FORMAT)
    record_composed(args.output, args.base, composed)
    print(json.dumps(evaluation, indent=2), flush=True)


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--version", required=True, help="candidate name, e.g. probara-laya-v4; also the output directory name")
    p.add_argument("--base", type=Path, required=True, help="verified pinned base, e.g. ai-service/.cache/base")
    p.add_argument("--data", type=Path, required=True, help="controlled corpus, e.g. ai-service/artifacts/dataset")
    p.add_argument("--output", type=Path, required=True, help="new, empty ai-service/artifacts/<version>")
    p.add_argument("--work", type=Path, required=True, help="compose root, e.g. ai-service/.cache/composed")
    p.add_argument("--epochs", type=int, default=3)
    p.add_argument("--lr", type=float, default=3e-4)
    p.add_argument("--seed", type=int, default=20261011)
    p.add_argument("--threads", type=int, default=4)
    p.add_argument("--head-device", choices=("auto", "cpu", "mps", "cuda"), default="auto")
    run(p.parse_args())
