import json
import hashlib
from pathlib import Path
import pytest
from probara_ai.contract import options, state_text, validate_evidence
from probara_ai.runtime import HEAD, REQUIRED_COMPOSED, REQUIRED_FILES, verify_artifact
from probara_ai.cpu import CPU_FORMAT
from probara_ai.contract import VERSION
from approval import passing_tasks, write_approval


def test_allowlist_and_budgets():
    with pytest.raises(ValueError): validate_evidence("rca", {"checks": 1, "failures": 1, "password": "secret"})
    with pytest.raises(ValueError): validate_evidence("rca", {"checks": 1, "failures": 2})
    with pytest.raises(ValueError): validate_evidence("dependency", {"co_fires": float("nan")})
    with pytest.raises(ValueError): validate_evidence("dependency", {"shared_service": "false"})
    assert "B precedes A" in state_text("dependency", {"co_fires": 5, "a_leads_seconds": -20}, 1)
    assert "B-before-A" in state_text("dependency", {"co_fires": 5, "a_leads_seconds": -20})
    assert len(options("rca")) == 9


def test_failing_checkpoint_blocks_startup(tmp_path):
    (tmp_path / "manifest.json").write_text(json.dumps({"release_approved": False}))
    with pytest.raises(ValueError, match="release gates"): verify_artifact(tmp_path)


def test_artifact_integrity_and_cpu_approval(tmp_path):
    for name in REQUIRED_FILES - {HEAD} | REQUIRED_COMPOSED:
        path = tmp_path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('{}')
    (tmp_path / 'rl_agent_config.json').write_text(json.dumps({'model_name': 'probara-laya-fixture'}))
    digest = lambda name: hashlib.sha256((tmp_path / name).read_bytes()).hexdigest()
    composed = {name: digest(name) for name in REQUIRED_COMPOSED}
    report = tmp_path / 'cpu-evaluation.json'
    write_approval(tmp_path, 'probara-laya-fixture', composed['model.safetensors'], CPU_FORMAT)
    files = {name: digest(name) for name in REQUIRED_FILES - {HEAD}}
    files[HEAD] = 'a' * 64  # bound through the composed checksum; not shipped
    manifest = {'version': 'probara-laya-fixture', 'release_approved': True, 'catalogue': VERSION, 'cpu_format': CPU_FORMAT,
                'head_sha256': 'a' * 64, 'files': files, 'composed': composed}
    def save():
        (tmp_path / 'manifest.json').write_text(json.dumps(manifest))
    save()
    verify_artifact(tmp_path)
    report.write_text(json.dumps({'release_approved': False, 'device': 'cpu', 'cpu_format': CPU_FORMAT,
                                  'version': 'probara-laya-fixture', 'tasks': passing_tasks(),
                                  'model_sha256': composed['model.safetensors']}))
    with pytest.raises(ValueError, match='checksum mismatch'):
        verify_artifact(tmp_path)
    manifest['files']['cpu-evaluation.json'] = digest('cpu-evaluation.json')
    save()
    with pytest.raises(ValueError, match='validation did not pass'):
        verify_artifact(tmp_path)
    manifest['composed'] = {k: v for k, v in composed.items() if k != 'model.safetensors'}
    save()
    with pytest.raises(ValueError, match='Incomplete'):
        verify_artifact(tmp_path)


def approved_fixture(path, state_format=None):
    """A composed directory verify_artifact accepts; returns a re-sealing helper."""
    for name in REQUIRED_FILES - {HEAD} | REQUIRED_COMPOSED:
        (path / name).parent.mkdir(parents=True, exist_ok=True)
        (path / name).write_text('{}')
    config = {'model_name': 'probara-laya-fixture'}
    if state_format is not None:
        config['state_format'] = state_format
    (path / 'rl_agent_config.json').write_text(json.dumps(config))
    digest = lambda name: hashlib.sha256((path / name).read_bytes()).hexdigest()  # noqa: E731
    composed = {name: digest(name) for name in REQUIRED_COMPOSED}
    write_approval(path, 'probara-laya-fixture', composed['model.safetensors'], CPU_FORMAT, state_format)
    manifest = {'version': 'probara-laya-fixture', 'release_approved': True, 'catalogue': VERSION,
                'cpu_format': CPU_FORMAT, 'head_sha256': 'a' * 64, 'composed': composed}
    if state_format is not None:
        manifest['state_format'] = state_format

    def seal(report=None):
        if report is not None:
            (path / 'cpu-evaluation.json').write_text(json.dumps(report))
        files = {name: digest(name) for name in REQUIRED_FILES - {HEAD}}
        files[HEAD] = 'a' * 64
        (path / 'manifest.json').write_text(json.dumps({**manifest, 'files': files}))
    seal()
    return seal


def report_of(path):
    return json.loads((path / 'cpu-evaluation.json').read_text())


@pytest.mark.parametrize('tamper, message', [
    # A consistently re-sealed report whose metrics fail the gate is refused,
    # whatever its release_approved flag says.
    ('coverage_below_rules', 'does not meet the release gate'),
    ('precision_below_floor', 'does not meet the release gate'),
    ('missing_task', 'does not meet the release gate'),
    ('no_metrics', 'does not meet the release gate'),
    ('other_version', 'different artifact version'),
])
def test_release_gate_is_recomputed_from_the_report(tmp_path, tamper, message):
    seal = approved_fixture(tmp_path)
    verify_artifact(tmp_path)
    report = report_of(tmp_path)
    if tamper == 'coverage_below_rules':
        report['tasks']['dependency']['held_out']['coverage'] = 0.7
    elif tamper == 'precision_below_floor':
        report['tasks']['rca']['held_out']['precision'] = 0.94
        report['tasks']['rca']['conservative_rules']['precision'] = 0.5
    elif tamper == 'missing_task':
        del report['tasks']['dependency']
    elif tamper == 'no_metrics':
        del report['tasks']
    else:
        report['version'] = 'probara-laya-other'
    seal(report)
    with pytest.raises(ValueError, match=message):
        verify_artifact(tmp_path)


def test_calibration_must_be_the_validated_one(tmp_path):
    seal = approved_fixture(tmp_path)
    (tmp_path / 'calibration.json').write_text(json.dumps({'temperatures': {'rca': 1.5, 'dependency': 1.5},
                                                           'thresholds': {'rca': 0.2, 'dependency': 0.5}}))
    seal()
    with pytest.raises(ValueError, match='Calibration settings differ'):
        verify_artifact(tmp_path)


def test_format_two_report_must_record_its_format(tmp_path):
    seal = approved_fixture(tmp_path, state_format=2)
    assert verify_artifact(tmp_path)['state_format'] == 2
    report = report_of(tmp_path)
    del report['state_format']
    seal(report)
    with pytest.raises(ValueError, match='different evidence state format'):
        verify_artifact(tmp_path)


@pytest.mark.parametrize('extra', ['tokenizer/added_tokens.json', 'encoder/extra.json', 'notes.txt'])
def test_unlisted_checkpoint_files_are_refused(tmp_path, extra):
    approved_fixture(tmp_path)
    (tmp_path / extra).write_text('{}')
    with pytest.raises(ValueError, match='differ from the manifest: ' + extra):
        verify_artifact(tmp_path)


def test_gate_predicate():
    from probara_ai.gate import release_gate
    tasks = passing_tasks()
    assert release_gate(tasks) is True
    tasks['rca']['held_out']['decision_accuracy'] = 0.84
    assert release_gate(tasks) is False
    tasks = passing_tasks()
    tasks['rca']['held_out']['coverage'] = 0.49
    tasks['rca']['conservative_rules']['coverage'] = 0.1
    assert release_gate(tasks) is False
    tasks = passing_tasks()
    tasks['dependency']['held_out']['unsupported_abstention'] = True
    assert release_gate(tasks) is False
    assert release_gate({**passing_tasks(), 'extra': passing_tasks()['rca']}) is False
    assert release_gate(None) is False


def test_grouped_dataset_partition():
    root = Path(__file__).resolve().parents[1] / "artifacts/dataset"
    if not root.exists(): pytest.skip("Regenerate controlled dataset first")
    for task in ("rca", "dependency"):
        groups = {}
        count = 0
        for split, expected in (("train", 3000), ("calibration", 1000), ("test", 1000)):
            rows = [json.loads(line) for line in (root / f"{task}-{split}.jsonl").read_text().splitlines()]
            assert len(rows) == expected
            groups[split] = {row["group"] for row in rows}
            count += len(rows)
            for row in rows:
                assert row["expected"][task] in row["questions"][task]["criteria"]
                assert row["state"] == state_text(task, row["evidence"])
            if task == "dependency":
                supported = [row['evidence'] for row in rows
                             if row['expected'][task] in ('a_depends_b', 'b_depends_a')]
                # Real installations can have tags without groups, or groups
                # without tags. Both require training and independent audits.
                assert any(e['shared_service'] and not e['shared_group'] for e in supported)
                assert any(e['shared_group'] and not e['shared_service'] for e in supported)
        assert count == 5000
        assert not groups["train"] & groups["test"]
        assert not groups["train"] & groups["calibration"]
        assert not groups["test"] & groups["calibration"]
