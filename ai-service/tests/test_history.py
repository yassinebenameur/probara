"""Frozen history: every committed artifact's corpus is reproducible from committed code."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import pytest

ROOT = Path(__file__).resolve().parents[1]
HISTORY = ROOT / "training/history"


def run(script, *args):
    return runpy.run_path(str(HISTORY / script))["generate"](*args)


def test_generate_v1_regenerates_v1_provenance(tmp_path):
    run("generate_v1.py", tmp_path)
    expected = json.loads((ROOT / "artifacts/probara-laya-v1/provenance.json").read_text())
    assert json.loads((tmp_path / "provenance.json").read_text()) == expected


@pytest.mark.parametrize("version", ["probara-laya-v3", "probara-laya-v4"])
def test_generate_v3_regenerates_the_v3_and_v4_corpus(tmp_path, version):
    # probara-controlled-v3 in state format 2 with the probara-evidence-v1
    # option texts: the same provenance.json, partition hashes included. Compared
    # as JSON: the generator lists partitions in directory order, which is the
    # filesystem's, so the key order differs between platforms.
    run("generate_v3.py", tmp_path)
    provenance = json.loads((tmp_path / "provenance.json").read_text())
    assert provenance == json.loads((ROOT / "artifacts" / version / "provenance.json").read_text())
    for name, digest in provenance["partition_sha256"].items():
        assert hashlib.sha256((tmp_path / name).read_bytes()).hexdigest() == digest
    for row in map(json.loads, (tmp_path / "dependency-test.jsonl").read_text().splitlines()[:5]):
        assert len(row["evidence"]) == 7


def test_augment_v4_regenerates_the_v4_training_augmentation(tmp_path):
    meta = run("augment_v4.py", tmp_path, 400)
    recorded = json.loads((ROOT / "artifacts/probara-laya-v4/training-config.json").read_text())["extra_train"]
    assert meta["rows"] == 2000
    assert meta["sha256"] == recorded["dependency-train-augment.jsonl"]


def test_frozen_catalogue_is_the_v1_catalogue():
    frozen = HISTORY / "catalogue-probara-evidence-v1.json"
    catalogue = json.loads(frozen.read_text())
    assert catalogue["version"] == "probara-evidence-v1"
    # Byte-identical to shared/ai/catalogue.json at the commit that shipped v4.
    shipped = subprocess.run(["git", "-C", str(ROOT), "show", "a0422212:shared/ai/catalogue.json"],
                             capture_output=True, check=False)
    if shipped.returncode != 0:
        pytest.skip("commit a0422212 is not in this checkout")
    assert frozen.read_bytes() == shipped.stdout


def test_generate_v3_records_the_original_generator():
    # The committed generate.py at the commit that shipped v4 is the file v3's
    # and v4's provenance names; generate_v3.py is that file, path- and pin-adjusted.
    original = subprocess.run(["git", "-C", str(ROOT), "show", "a0422212:ai-service/training/generate.py"],
                              capture_output=True, check=False)
    if original.returncode != 0:
        pytest.skip("commit a0422212 is not in this checkout")
    recorded = json.loads((ROOT / "artifacts/probara-laya-v4/provenance.json").read_text())["generator_sha256"]
    assert hashlib.sha256(original.stdout).hexdigest() == recorded
    assert runpy.run_path(str(HISTORY / "generate_v3.py"))["ORIGINAL_GENERATOR_SHA256"] == recorded
