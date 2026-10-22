"""Build-time download, verification and attribution for the pinned base only.

`files` in base-lock.json are the bytes Hugging Face serves at the pinned
revision. `normalized` are the bytes after the one rewrite laya's
load_checkpoint applies in place (extra_special_tokens list -> mapping).
`fetch` applies that rewrite itself right after download so later loads never
write to the base directory, and verify_base accepts either form because a
base that a laya load has already touched is still the pinned revision.
"""
import argparse
import hashlib
import json
from pathlib import Path
import shutil

LOCK = json.loads(Path(__file__).with_name("base-lock.json").read_text())


def sha256(path):
    with open(path, "rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def verify_base(base, lock=LOCK):
    base = Path(base)
    for name, digest in lock["files"].items():
        path = base / name
        accepted = {digest, lock.get("normalized", {}).get(name)}
        if path.is_symlink() or not path.is_file() or sha256(path) not in accepted:
            raise ValueError(f"Base file does not match pinned revision: {name}")


def normalize_base(base, lock=LOCK):
    """Apply laya's tokenizer_config rewrite deterministically, then verify it."""
    path = Path(base) / "tokenizer/tokenizer_config.json"
    config = json.loads(path.read_text())
    extra = config.get("extra_special_tokens")
    if isinstance(extra, list):
        config["extra_special_tokens"] = {"extra_%d" % i: token for i, token in enumerate(extra)}
        # Byte-identical to laya.agent._fix_tokenizer_config (indent=2, no newline).
        path.write_text(json.dumps(config, indent=2))
    expected = lock.get("normalized", {}).get("tokenizer/tokenizer_config.json")
    if expected and sha256(path) != expected:
        raise ValueError("Normalized tokenizer config does not match the pinned revision")


def package_notices(base, output):
    shutil.copy(Path(__file__).parents[1] / "UPSTREAM-LICENSE", output / "LICENSE")
    # mmBERT (MIT) encoder and Gemma 2 tokenizer origin, which the base redistributes.
    shutil.copy(Path(__file__).parents[1] / "THIRD-PARTY-NOTICES", output / "THIRD-PARTY-NOTICES")
    shutil.copy(base / "README.md", output / "base-model-card.md")
    notice = {"repo": LOCK["repo"], "revision": LOCK["revision"],
              "weight_sha256": LOCK["files"]["model.safetensors"],
              "training_interface_commit": LOCK["upstream_commit"], "license": "Apache-2.0",
              "modified": "Probara domain decision head fine-tuning and CPU encoder quantization; original encoder weights frozen."}
    (output / "upstream.json").write_text(json.dumps(notice, indent=2) + "\n")


def fetch(output, repo=None, revision=None, lock=LOCK):
    """Download the locked files and verify each one's sha256.

    repo/revision default to the lock; a mirror (another repo id, or another
    endpoint through HF_ENDPOINT) is accepted only if it serves the same bytes.
    Attribution (package_notices) always names the locked upstream.
    """
    output = Path(output)
    if output.exists() and any(output.iterdir()):
        raise ValueError("Base download requires an empty output directory")
    # Network access is confined to this explicit build-time command. Neither
    # the inference process nor the runtime image contains this downloader.
    from huggingface_hub import snapshot_download
    snapshot_download(repo or lock["repo"], revision=revision or lock["revision"], local_dir=output,
                      allow_patterns=list(lock["files"]))
    shutil.rmtree(output / ".cache", ignore_errors=True)
    for name, digest in lock["files"].items():
        path = output / name
        if not path.is_file() or sha256(path) != digest:
            raise ValueError(f"Downloaded base file does not match pinned revision: {name}")
    normalize_base(output, lock)
    verify_base(output, lock)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--repo", default="", help="mirror repo id (empty = base-lock.json)")
    parser.add_argument("--revision", default="", help="mirror revision (empty = base-lock.json)")
    args = parser.parse_args()
    fetch(args.output, args.repo or None, args.revision or None)
