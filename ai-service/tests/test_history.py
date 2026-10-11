"""Frozen history: v1's corpus is reproducible from committed code."""
import json
from pathlib import Path
import runpy

ROOT = Path(__file__).resolve().parents[1]


def test_generate_v1_regenerates_v1_provenance(tmp_path):
    generate = runpy.run_path(str(ROOT / "training/history/generate_v1.py"))["generate"]
    generate(tmp_path)
    expected = json.loads((ROOT / "artifacts/probara-laya-v1/provenance.json").read_text())
    assert json.loads((tmp_path / "provenance.json").read_text()) == expected
