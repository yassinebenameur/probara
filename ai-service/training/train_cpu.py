"""Adapt a trained head to the exact deployed, frozen CPU feature extractor.

Uses the pinned upstream soft-CE trainer and its option-order augmentation.
Only training rows are used; export stays blocked until separate CPU validation.
The predecessor (--resume) is a head-only artifact composed with the pinned
base into <work>/<predecessor version>; the candidate is written head-only to
artifacts/<version> and its composition recorded in <work>/<version>.
Per-epoch recovery heads go to <work>/<version>.epochs/, never the artifact.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import time
from types import SimpleNamespace

os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", USE_TF="0")
import torch
from safetensors.torch import save_file
from laya.train import TrainConfig, load_checkpoint, items_from_rows, train_model
from probara_ai.contract import STATE_FORMAT, VERSION
from probara_ai.cpu import CPU_FORMAT, prepare_cpu
from train import read, MAX_LEN, HEAD_LEN
from upstream import verify_base, package_notices
from compose_artifact import (CONFIG, check_version, compose, load_manifest, prepare_output,
                              record_composed, save_head, save_json, write_manifest)
from partitions import verify_partitions, verify_rendering


class FrozenCPUEncoder(torch.nn.Module):
    """Keep inference-format features on CPU while accelerating head training."""
    def __init__(self, encoder):
        super().__init__()
        self.inner, self.config = encoder, encoder.config

    def _apply(self, fn, recurse=True):
        # Upstream train_model moves the complete model to its training device.
        # Quantized kernels must stay on CPU; only the head follows that move.
        return self

    def forward(self, **kwargs):
        destination = kwargs['input_ids'].device
        with torch.no_grad():
            output = self.inner(**{key: value.cpu() if isinstance(value, torch.Tensor) else value
                                   for key, value in kwargs.items()})
        return SimpleNamespace(last_hidden_state=output.last_hidden_state.to(destination))


def run(args):
    check_version(args.version)
    if args.output.resolve() == args.resume.resolve():
        raise ValueError("Export must use a separate candidate directory")
    # Every refusal before anything is deleted or trained: record_composed
    # needs a fresh <work>/<version>, and --replace-unapproved deletes the old
    # candidate only once base and corpus are known good.
    if (args.work / args.version).exists():
        raise ValueError(f"Compose directory {args.work / args.version} already exists; remove it first")
    verify_base(args.base)
    # A corrected corpus may differ from the predecessor's training corpus.
    # Verify its own provenance and independent groups before using train rows;
    # the predecessor's immutable files are verified separately below.
    verify_partitions(args.data, args.data, current_generator=True)
    # The candidate learns the current rendering, whatever its predecessor used.
    verify_rendering(args.data, STATE_FORMAT, parts=('train',))
    predecessor_data = args.predecessor_data or args.data
    verify_partitions(predecessor_data, args.resume)
    def group_roles(data):
        return {part: {row['group'] for task in ('rca', 'dependency')
                       for row in read(data / f'{task}-{part}.jsonl')}
                for part in ('train', 'calibration', 'test')}
    before, after = group_roles(predecessor_data), group_roles(args.data)
    if before['train'] & (after['calibration'] | after['test']) or after['train'] & (before['calibration'] | before['test']):
        raise ValueError('Corpus revision leaks predecessor evaluation groups into training or vice versa')
    prepare_output(args.output, args.version, args.replace_unapproved)
    # Composition verifies every predecessor checksum and rebuilds it on the
    # pinned frozen encoder, which reproducible CPU preparation requires.
    parent = load_manifest(args.resume)
    predecessor = args.work / args.resume.name
    compose(args.resume, args.base, predecessor)
    torch.set_num_threads(4)
    torch.set_num_interop_threads(1)
    torch.manual_seed(20261011)
    model, tok, cfg = load_checkpoint(str(predecessor))
    model = prepare_cpu(model)
    device = torch.device(args.head_device if args.head_device != 'auto' else
                          'cuda' if torch.cuda.is_available() else
                          'mps' if torch.backends.mps.is_available() else 'cpu')
    model.encoder = FrozenCPUEncoder(model.encoder)
    rows = sum((read(args.data / f'{task}-train.jsonl') for task in ('rca', 'dependency')), [])
    items, skipped = items_from_rows(tok, rows, MAX_LEN, HEAD_LEN)
    if skipped or len(items) != 6000:
        raise ValueError('Incomplete training partition')
    config = TrainConfig(epochs=args.epochs, micro_batch=8, grad_accum=4, head_lr=3e-4,
                         freeze_encoder=True, loss='soft-ce', shuffle_options=('choice',),
                         seed=20261011, max_len=MAX_LEN, head_max_len=HEAD_LEN,
                         log_every=25, amp=False, gradient_checkpointing=False)
    recovery = args.work / f'{args.version}.epochs'
    recovery.mkdir(parents=True, exist_ok=True)
    started = time.perf_counter()

    def epoch_checkpoint(epoch, loss):
        # Head-only recovery files are never deployable artifacts.
        state = {key: value.detach().cpu().contiguous() for key, value in model.state_dict().items()
                 if not key.startswith('encoder.')}
        save_file(state, str(recovery / f'head-epoch-{epoch+1}.safetensors'))
        print(f'CPU epoch {epoch+1}: loss={loss:.6f}; elapsed={time.perf_counter()-started:.1f}s', flush=True)

    print(f'Training frozen deployment-format CPU features; head device={device}', flush=True)
    history = train_model(model, tok, items, config, device, MAX_LEN, HEAD_LEN,
                          on_epoch_end=epoch_checkpoint)
    # Only the head is exported: composition restores the pinned frozen
    # encoder, and deployment quantizes it exactly as during this training.
    cfg['predecessor_training'] = cfg.get('training', {})
    cfg.update(max_len=MAX_LEN, head_max_len=HEAD_LEN, model_name=args.version, catalogue=VERSION,
               state_format=STATE_FORMAT, training={'fine_tuned_from_checkpoint': True, 'epochs_completed': args.epochs,
                         'encoder_device': 'cpu', 'head_device': str(device), 'seed': config.seed,
                         'loss': config.loss, 'freeze_encoder': True, 'train_items': len(items)})
    save_head(model, args.output)
    save_json(args.output / CONFIG, cfg)
    package_notices(args.base, args.output)
    shutil.copy(args.data / 'provenance.json', args.output / 'provenance.json')
    predecessor_report = args.resume / 'cpu-evaluation-initial.json'
    if not predecessor_report.exists():
        predecessor_report = args.resume / 'cpu-evaluation.json'
    evaluation = {'release_approved': False, 'scope': 'Controlled scenarios only; no real-world accuracy claim.',
                  'gate_failure': 'Separate CPU calibration and held-out validation required.',
                  'loss_history': history, 'training_seconds': time.perf_counter()-started,
                  'predecessor_version': parent['version'],
                  'predecessor_model_sha256': parent['composed']['model.safetensors'],
                  'predecessor_evaluation': json.loads(predecessor_report.read_text())}
    (args.output / 'evaluation.json').write_text(json.dumps(evaluation, indent=2)+'\n')
    (args.output / 'training-config.json').write_text(json.dumps({**vars(config), 'head_device': str(device), 'encoder_device': 'cpu',
        'frozen_encoder_cpu_format': CPU_FORMAT, 'state_format': STATE_FORMAT, 'predecessor_model_sha256': parent['composed']['model.safetensors'],
        'predecessor_partition_sha256': json.loads((args.resume / 'provenance.json').read_text())['partition_sha256']}, indent=2)+'\n')
    # No calibration settings or CPU release report are inherited.
    write_manifest(args.output, args.version, STATE_FORMAT, release_approved=False)
    record_composed(args.output, args.base, args.work / args.version)
    print(json.dumps(evaluation, indent=2), flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--version', required=True, help='candidate name, e.g. probara-laya-v3; also the output directory name')
    parser.add_argument('--base', type=Path, required=True, help='verified pinned base, e.g. ai-service/.cache/base')
    parser.add_argument('--data', type=Path, required=True, help='controlled corpus, e.g. ai-service/artifacts/dataset')
    parser.add_argument('--resume', type=Path, required=True, help='predecessor head-only artifact, e.g. ai-service/artifacts/<predecessor version>')
    parser.add_argument('--output', type=Path, required=True, help='ai-service/artifacts/<version>')
    parser.add_argument('--work', type=Path, required=True, help='compose root, e.g. ai-service/.cache/composed')
    parser.add_argument('--replace-unapproved', action='store_true', help='replace a non-empty, unapproved candidate directory')
    parser.add_argument('--epochs', type=int, default=3)
    parser.add_argument('--head-device', choices=('auto', 'cpu', 'mps', 'cuda'), default='auto')
    parser.add_argument('--predecessor-data', type=Path)
    run(parser.parse_args())
