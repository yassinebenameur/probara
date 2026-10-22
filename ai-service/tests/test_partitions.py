"""Corpus provenance and the refusals that run before any training work."""
import json
from pathlib import Path
import runpy
from types import SimpleNamespace
import pytest
from partitions import verify_partitions

ROOT = Path(__file__).resolve().parents[1]


@pytest.fixture(scope="module")
def corpus(tmp_path_factory):
    out = tmp_path_factory.mktemp("corpus")
    runpy.run_path(str(ROOT / "training/generate.py"))["generate"](out)
    return out


def test_current_corpus_must_come_from_the_committed_generator(corpus, tmp_path):
    verify_partitions(corpus, corpus, current_generator=True)
    for field in ("generator_sha256", "checker_fixture_sha256"):
        provenance = json.loads((corpus / "provenance.json").read_text())
        provenance[field] = "0" * 64
        (tmp_path / "provenance.json").write_text(json.dumps(provenance))
        verify_partitions(corpus, tmp_path)  # partitions still match their provenance
        with pytest.raises(ValueError, match="committed training/generate.py"):
            verify_partitions(corpus, tmp_path, current_generator=True)


@pytest.mark.parametrize("script", ["train", "train_cpu", "train_head_cpu"])
def test_trainers_refuse_a_used_compose_directory_before_deleting(tmp_path, script):
    pytest.importorskip("laya")
    module = __import__(script)
    version = "probara-laya-fixture"
    output = tmp_path / "artifacts" / version
    output.mkdir(parents=True)
    (output / "manifest.json").write_text(json.dumps({"release_approved": False}))
    (tmp_path / "work" / version).mkdir(parents=True)
    args = SimpleNamespace(version=version, output=output, work=tmp_path / "work", base=tmp_path / "base",
                           data=tmp_path / "data", replace_unapproved=True, resume=tmp_path / "other",
                           predecessor_data=None)
    with pytest.raises(ValueError, match="already exists|must be new or empty"):
        module.run(args)
    assert (output / "manifest.json").exists(), "the previous candidate must survive a refused run"


def test_score_cache_identity_covers_the_scoring_code():
    pytest.importorskip("laya")
    import validate_cpu
    assert {p.name for p in validate_cpu.SCORE_CODE} == {"validate_cpu.py", "train.py", "contract.py", "cpu.py"}
    assert all(p.is_file() for p in validate_cpu.SCORE_CODE)
