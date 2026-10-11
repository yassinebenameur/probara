"""Verify controlled corpus provenance and separation without selecting a device."""
import hashlib
import json
from pathlib import Path


def read(path):
    return [json.loads(line) for line in path.read_text().splitlines()]


def verify_rendering(data, state_format, parts=('train', 'calibration', 'test')):
    """Every row's precomputed state must be the given format's rendering.

    Training and validation consume row["state"] as written by generate.py,
    while the runtime renders evidence live with the artifact's state_format.
    A corpus in another format would train or evaluate text production never
    sends, so it is refused rather than silently mixed.
    """
    from probara_ai.contract import state_text
    for task in ('rca', 'dependency'):
        for part in parts:
            for row in read(data / f'{task}-{part}.jsonl'):
                if row['state'] != state_text(task, row['evidence'], state_format):
                    raise ValueError(f'Corpus {data} is not rendered in evidence state format {state_format} '
                                     f'({task}-{part}.jsonl, group {row["group"]})')


HERE = Path(__file__).resolve().parent


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def verify_partitions(data, artifact, current_generator=False):
    """The corpus matches the artifact's provenance and keeps groups separate.

    current_generator=True (training a new candidate, validating a
    current-format one) also requires the corpus to come from the committed
    training/generate.py and checker-fixtures.json, so an approved artifact
    is reproducible from the repository and not from a local generator edit.
    """
    provenance = json.loads((artifact / 'provenance.json').read_text())
    if current_generator and (provenance.get('generator_sha256') != sha256(HERE / 'generate.py') or
                              provenance.get('checker_fixture_sha256') != sha256(HERE / 'checker-fixtures.json')):
        raise ValueError(f'Corpus {data} was not produced by the committed training/generate.py and '
                         'checker-fixtures.json; regenerate it')
    for task in ('rca', 'dependency'):
        groups = {}
        for part, count in (('train', 3000), ('calibration', 1000), ('test', 1000)):
            name = f'{task}-{part}.jsonl'
            path = data / name
            if hashlib.sha256(path.read_bytes()).hexdigest() != provenance['partition_sha256'][name]:
                raise ValueError(f'Dataset does not match checkpoint provenance: {name}')
            rows = read(path)
            if len(rows) != count or any(row['task'] != task for row in rows):
                raise ValueError(f'Invalid task partition: {name}')
            groups[part] = {row['group'] for row in rows}
        if groups['train'] & groups['calibration'] or groups['train'] & groups['test'] or groups['calibration'] & groups['test']:
            raise ValueError('Related scenario groups cross dataset partitions')
