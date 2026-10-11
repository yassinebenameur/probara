#!/usr/bin/env python3
"""Check the rendered Helm chart and Compose file for the local AI workload.

Usage (from anywhere; paths resolve from this file):
    python3 -I scripts/check-local-ai-packaging.py [--skip-compose]

Needs helm on PATH, PyYAML, and (unless --skip-compose) docker compose.
Asserts the service boundary (only api + platform worker get LOCAL_AI_*,
the local AI pod gets only its token), the token convention (stable derived
default, literal, per-field and chart-wide existing Secret), the external URL
mode, scheduling/image/extraEnv knobs (top-level extraEnv never reaches the
local AI pod), the optional client token reference, the NetworkPolicy toggle,
every fail-fast case (including the threads/queueCapacity/timeoutSeconds
ranges), and the Compose/Makefile/start-script wiring (a failed local-ai build
never blocks the platform; CPU sizing fits small Docker hosts).
"""
import argparse
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys

import yaml

ROOT = Path(__file__).resolve().parent.parent
CHART = str(ROOT / 'helm' / 'monitoring-platform')
RELEASE = 'ai-validation'
FULL = RELEASE + '-probara'
CHART_SECRET = FULL + '-secret'
LOCATION = ['--set', 'worker.locations[0].name=eu', '--set', 'worker.locations[0].locationId=00000000-0000-0000-0000-0000000000aa',
            '--set', 'worker.locations[0].credential=loc-credential', '--set', 'worker.locations[0].natsUrl=nats://loc:pw@nats:4222']
OPERATOR_TOKEN = 'operator-supplied-token-0123456789'


def helm(*args):
    return subprocess.run(['helm', 'template', RELEASE, CHART, *args], capture_output=True, text=True)


def render(*args):
    result = helm(*args)
    if result.returncode != 0:
        raise AssertionError(f'helm template {args} failed: {result.stderr}')
    return [v for v in yaml.safe_load_all(result.stdout) if v], result.stdout


def find(objects, kind, name):
    matches = [v for v in objects if v['kind'] == kind and v['metadata']['name'] == name]
    assert len(matches) == 1, f'expected one {kind} {name}, found {len(matches)}'
    return matches[0]


def pod(objects, kind, name):
    return find(objects, kind, name)['spec']['template']['spec']


def env_of(objects, kind, name):
    return {v['name']: v for v in pod(objects, kind, name)['containers'][0]['env']}


def local_ai_objects(objects):
    return [v for v in objects if v['metadata']['name'] == FULL + '-local-ai']


def secret_data(objects):
    secrets = [v for v in objects if v['kind'] == 'Secret' and v['metadata']['name'] == CHART_SECRET]
    return secrets[0].get('stringData', {}) if secrets else None


def token_ref(env):
    return env['LOCAL_AI_TOKEN']['valueFrom']['secretKeyRef']


def assert_clients(objects, url, ref):
    """api and platform worker carry the client env; nothing else does."""
    for name in (FULL + '-api', FULL + '-worker'):
        env = env_of(objects, 'Deployment', name)
        if url:
            assert env['LOCAL_AI_DISABLED']['value'] == 'false', name
            assert env['LOCAL_AI_URL']['value'] == url, (name, env['LOCAL_AI_URL'])
            assert env['LOCAL_AI_TIMEOUT_SECONDS']['value'] == '90', name
            # Optional on the clients only: an existing Secret without the key
            # must leave api/worker running with AI unavailable, not blocked.
            assert token_ref(env) == {**ref, 'optional': True}, (name, token_ref(env))
        else:
            assert env['LOCAL_AI_DISABLED']['value'] == 'true', name
            assert not {'LOCAL_AI_URL', 'LOCAL_AI_TOKEN', 'LOCAL_AI_TIMEOUT_SECONDS'} & env.keys(), name
    others = [(k, n) for k, n in (('Deployment', FULL + '-scheduler'), ('Deployment', FULL + '-alerter'),
                                  ('Deployment', FULL + '-status-page'), ('Deployment', FULL + '-frontend'),
                                  ('Deployment', FULL + '-worker-eu'), ('Job', FULL + '-migrations'))]
    for kind, name in others:
        leaked = [k for k in env_of(objects, kind, name) if k.startswith('LOCAL_AI_')]
        assert not leaked, f'{name} must not receive local AI env: {leaked}'


def check_default():
    objects, text = render(*LOCATION)
    _, again = render(*LOCATION)
    assert text == again, 'default render is not stable: the derived local_ai_token must not change between renders'
    spec = pod(objects, 'Deployment', FULL + '-local-ai')
    container = spec['containers'][0]
    assert container['image'] == 'ghcr.io/yassinebenameur/probara-local-ai:latest', container['image']
    assert container['imagePullPolicy'] == 'IfNotPresent'
    assert container['resources'] == {'requests': {'cpu': '1', 'memory': '2Gi'}, 'limits': {'cpu': '4', 'memory': '4Gi'}}
    assert spec['automountServiceAccountToken'] is False
    assert 'serviceAccountName' not in spec
    assert spec['securityContext']['runAsNonRoot'] is True
    assert spec['securityContext']['runAsUser'] == 1000 and spec['securityContext']['fsGroup'] == 1000
    assert container['securityContext']['readOnlyRootFilesystem'] is True
    assert container['securityContext']['allowPrivilegeEscalation'] is False
    names = [v['name'] for v in container['env']]
    assert names == ['LOCAL_AI_TOKEN', 'LOCAL_AI_THREADS', 'LOCAL_AI_QUEUE_CAPACITY'], names
    assert token_ref({v['name']: v for v in container['env']}) == {'name': CHART_SECRET, 'key': 'local_ai_token'}
    # The global podAnnotations advertise a 9090 scrape port; the service is on 8090.
    assert 'annotations' not in find(objects, 'Deployment', FULL + '-local-ai')['spec']['template']['metadata']
    assert not any(k in spec for k in ('nodeSelector', 'tolerations', 'affinity'))
    service = find(objects, 'Service', FULL + '-local-ai')['spec']
    assert service['type'] == 'ClusterIP' and service['ports'][0]['port'] == 8090
    policy = find(objects, 'NetworkPolicy', FULL + '-local-ai')['spec']
    assert policy['egress'] == [] and policy['policyTypes'] == ['Ingress', 'Egress']
    assert len(policy['ingress']) == 1
    sources = {p['podSelector']['matchLabels']['app.kubernetes.io/component'] for p in policy['ingress'][0]['from']}
    assert sources == {'api', 'worker'}, sources
    assert policy['ingress'][0]['ports'] == [{'port': 8090, 'protocol': 'TCP'}]
    token = secret_data(objects)['local_ai_token']
    assert len(token) == 64 and token != 'change-me-in-production-jwt-secret-minimum-32-chars'
    rotated, _ = render('--set', 'secrets.adminJwtSecret=another-admin-jwt-secret-0123456789')
    assert secret_data(rotated)['local_ai_token'] not in (token, ''), 'derived token must follow adminJwtSecret'
    assert_clients(objects, f'http://{FULL}-local-ai:8090', {'name': CHART_SECRET, 'key': 'local_ai_token'})


def check_disabled():
    # Range checks apply only to what is in use: no pod, no URL, nothing to check.
    objects, _ = render('--set', 'localAI.enabled=false', '--set', 'localAI.threads=8',
                        '--set', 'localAI.timeoutSeconds=0', *LOCATION)
    assert not local_ai_objects(objects)
    assert 'local_ai_token' not in secret_data(objects)
    assert_clients(objects, None, None)


def check_external():
    objects, _ = render('--set', 'localAI.enabled=false', '--set', 'localAI.externalUrl=http://probara-ai.ai.svc:8090',
                        '--set', 'secrets.localAiToken=' + OPERATOR_TOKEN, *LOCATION)
    assert not local_ai_objects(objects)
    assert secret_data(objects)['local_ai_token'] == OPERATOR_TOKEN
    assert_clients(objects, 'http://probara-ai.ai.svc:8090', {'name': CHART_SECRET, 'key': 'local_ai_token'})
    objects, _ = render('--set', 'localAI.enabled=false', '--set', 'localAI.externalUrl=https://ai.example.internal',
                        '--set', 'secrets.localAiTokenExistingSecret=ai-token', '--set', 'secrets.localAiTokenExistingSecretKey=token', *LOCATION)
    assert 'local_ai_token' not in secret_data(objects)
    assert_clients(objects, 'https://ai.example.internal', {'name': 'ai-token', 'key': 'token'})


def check_existing_secrets():
    objects, _ = render('--set', 'secrets.localAiTokenExistingSecret=internal-ai-token',
                        '--set', 'secrets.localAiTokenExistingSecretKey=token', *LOCATION)
    ref = {'name': 'internal-ai-token', 'key': 'token'}
    assert token_ref(env_of(objects, 'Deployment', FULL + '-local-ai')) == ref
    assert 'local_ai_token' not in secret_data(objects)
    assert_clients(objects, f'http://{FULL}-local-ai:8090', ref)
    # Chart-wide Secret: canonical key, and the chart renders no Secret at all.
    objects, _ = render('--set', 'secrets.existingSecret=probara-ext', *LOCATION)
    ref = {'name': 'probara-ext', 'key': 'local_ai_token'}
    assert secret_data(objects) is None
    assert token_ref(env_of(objects, 'Deployment', FULL + '-local-ai')) == ref
    assert_clients(objects, f'http://{FULL}-local-ai:8090', ref)
    # A literal token stays usable when the JWT secret is external.
    objects, _ = render('--set', 'secrets.adminJwtSecretExistingSecret=jwt', '--set', 'secrets.localAiToken=' + OPERATOR_TOKEN)
    assert secret_data(objects)['local_ai_token'] == OPERATOR_TOKEN


def check_knobs():
    objects, _ = render('--set', 'localAI.nodeSelector.pool=ai', '--set', 'localAI.tolerations[0].key=ai',
                        '--set', 'localAI.tolerations[0].operator=Exists',
                        '--set', 'localAI.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key=pool',
                        '--set', 'localAI.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator=Exists',
                        '--set-string', 'localAI.podAnnotations.prometheus\\.io/port=8090',
                        '--set', 'localAI.image.repository=registry.example/ai', '--set', 'localAI.image.tag=v9',
                        '--set', 'localAI.image.pullPolicy=Always', '--set', 'localAI.podSecurityContext.runAsUser=2000',
                        '--set', 'extraEnv[0].name=GLOBAL_X', '--set-string', 'extraEnv[0].value=1',
                        '--set', 'localAI.extraEnv[0].name=LOCAL_X', '--set-string', 'localAI.extraEnv[0].value=2',
                        '--set', 'localAI.networkPolicy.enabled=false')
    deployment = find(objects, 'Deployment', FULL + '-local-ai')['spec']['template']
    spec, container = deployment['spec'], deployment['spec']['containers'][0]
    assert spec['nodeSelector'] == {'pool': 'ai'}
    assert spec['tolerations'] == [{'key': 'ai', 'operator': 'Exists'}]
    assert 'nodeAffinity' in spec['affinity']
    assert deployment['metadata']['annotations'] == {'prometheus.io/port': '8090'}
    assert container['image'] == 'registry.example/ai:v9' and container['imagePullPolicy'] == 'Always'
    assert spec['securityContext']['runAsUser'] == 2000
    names = [v['name'] for v in container['env']]
    # Top-level extraEnv carries shared credentials (PROBARA_SECRETS_KEY_V2);
    # the credential-free inference pod must never receive it.
    assert 'GLOBAL_X' not in names and names[-1] == 'LOCAL_X', names
    assert env_of(objects, 'Deployment', FULL + '-api')['GLOBAL_X']['value'] == '1'
    assert not [v for v in objects if v['kind'] == 'NetworkPolicy']
    objects, _ = render('--set', 'localAI.image.tag=', '--set', 'image.tag=1.2.3')
    assert pod(objects, 'Deployment', FULL + '-local-ai')['containers'][0]['image'] == 'ghcr.io/yassinebenameur/probara-local-ai:1.2.3'
    objects, _ = render('--set', 'localAI.networkPolicy.extraIngress[0].from[0].namespaceSelector.matchLabels.team=monitoring')
    ingress = find(objects, 'NetworkPolicy', FULL + '-local-ai')['spec']['ingress']
    assert len(ingress) == 2 and ingress[1]['from'][0]['namespaceSelector']['matchLabels'] == {'team': 'monitoring'}


FAILURES = [
    (['--set', 'localAI.externalUrl=http://x:8090'], 'mutually exclusive'),
    (['--set', 'localAI.enabled=false', '--set', 'localAI.externalUrl=http://x:8090'], 'required with localAI.externalUrl'),
    (['--set', 'localAI.enabled=false', '--set', 'localAI.externalUrl=http://x:8090/v1', '--set', 'secrets.localAiToken=' + OPERATOR_TOKEN], 'localAI.externalUrl must be'),
    (['--set', 'localAI.enabled=false', '--set', 'localAI.externalUrl=http://u:p@x:8090', '--set', 'secrets.localAiToken=' + OPERATOR_TOKEN], 'localAI.externalUrl must be'),
    (['--set', 'localAI.enabled=false', '--set', 'localAI.externalUrl=x:8090', '--set', 'secrets.localAiToken=' + OPERATOR_TOKEN], 'localAI.externalUrl must be'),
    (['--set', 'secrets.localAiToken=short'], 'at least 16 characters'),
    (['--set', 'secrets.adminJwtSecretExistingSecret=jwt'], 'cannot read'),
    (['--set', 'secrets.adminJwtSecret='], 'cannot read'),
    (['--set', 'localAI.threads=8'], 'localAI.threads must be a whole number from 1 to 4'),
    (['--set', 'localAI.threads=0'], 'localAI.threads must be a whole number from 1 to 4'),
    (['--set', 'localAI.threads=2.5'], 'localAI.threads must be a whole number from 1 to 4'),
    (['--set', 'localAI.queueCapacity=0'], 'localAI.queueCapacity must be a whole number from 1 to 32'),
    (['--set', 'localAI.queueCapacity=33'], 'localAI.queueCapacity must be a whole number from 1 to 32'),
    (['--set', 'localAI.timeoutSeconds=0'], 'localAI.timeoutSeconds must be a whole number from 1 to 300'),
    (['--set', 'localAI.timeoutSeconds=600'], 'localAI.timeoutSeconds must be a whole number from 1 to 300'),
    (['--set', 'localAI.enabled=false', '--set', 'localAI.externalUrl=http://x:8090', '--set', 'secrets.localAiToken=' + OPERATOR_TOKEN,
      '--set', 'localAI.timeoutSeconds=301'], 'localAI.timeoutSeconds must be a whole number from 1 to 300'),
]


def check_failures():
    for args, message in FAILURES:
        result = helm(*args)
        assert result.returncode != 0, f'expected failure for {args}'
        assert message in result.stderr, (args, result.stderr)


def check_compose():
    compose = json.loads(subprocess.check_output(['docker', 'compose', 'config', '--format', 'json'], text=True, cwd=ROOT))
    services = compose['services']
    ai = services['local-ai']
    assert not ai.get('profiles'), 'local-ai must be default-on (no profile)'
    assert ai['ports'] and all(v['host_ip'] == '127.0.0.1' for v in ai['ports'])
    assert not any('POSTGRES' in k or 'NATS' in k or 'SECRETS_KEY' in k or 'JWT' in k for k in ai['environment'])
    assert ai['healthcheck']['test'] and 'healthz' in ' '.join(ai['healthcheck']['test'])
    assert int(ai['mem_limit']) == 4 * 1024 ** 3 and float(ai['cpus']) == 4
    # Pulled when missing (no build needed once an approved image exists); the
    # build stays for local work. The CPU limit must be settable below 4.
    assert ai['image'].startswith('ghcr.io/yassinebenameur/probara-local-ai:') and ai['pull_policy'] == 'missing', ai['image']
    assert ai['build']['dockerfile'] == 'ai-service/Dockerfile'
    small = json.loads(subprocess.check_output(['docker', 'compose', 'config', '--format', 'json'], text=True, cwd=ROOT,
                                               env={**os.environ, 'LOCAL_AI_CPUS': '2', 'LOCAL_AI_THREADS': '2'}))
    assert float(small['services']['local-ai']['cpus']) == 2
    assert small['services']['local-ai']['environment']['LOCAL_AI_THREADS'] == '2'
    reservations = ai['deploy']['resources']['reservations']
    assert float(reservations['cpus']) == 1 and int(reservations['memory']) == 2 * 1024 ** 3
    for name in ('api', 'worker'):
        env = services[name]['environment']
        assert env['LOCAL_AI_URL'] == 'http://local-ai:8090', name
        assert env['LOCAL_AI_TOKEN'] == ai['environment']['LOCAL_AI_TOKEN'], name
        assert env['LOCAL_AI_DISABLED'] == ai['environment']['LOCAL_AI_DISABLED'], name
    for name in ('scheduler', 'alerter', 'status-page'):
        leaked = [k for k in services[name].get('environment', {}) if k.startswith('LOCAL_AI_')]
        assert not leaked, f'compose {name} must not receive local AI env: {leaked}'


def check_dev_wiring():
    makefile = (ROOT / 'Makefile').read_text()
    for target in ('dev-stop:', 'stop-all-local:'):
        body = makefile.split(target, 1)[1].split('\n\n', 1)[0]
        assert 'local-ai' in body, f'{target} must stop local-ai'
    # A failed local-ai build must never abort the platform: every compose
    # up/build a target runs either names platform services (never local-ai,
    # never "all services") or is the guarded, tolerant local-ai command.
    for target in ('up', 'build', 'rebuild', 'scale-workers', 'start-all'):
        dry = subprocess.run(['make', '-n', '-C', str(ROOT), target, 'N=2'], capture_output=True, text=True, check=True).stdout
        commands = [line for line in dry.splitlines() if re.search(r'docker compose (up|build)\b', line)]
        assert commands, f'make {target} runs no compose up/build'
        assert any('local-ai' in line for line in commands), f'make {target} must also start local-ai'
        for line in commands:
            if line.rstrip().endswith('fi'):
                assert '"${LOCAL_AI_DISABLED:-false}" != "true"' in line and 'local-ai ||' in line and 'WARNING' in line, (target, line)
                continue
            tail = re.search(r'docker compose (?:up|build)\b(.*)', line).group(1)
            words = shlex.split(re.split(r'&&|\|\||;', tail)[0])
            named = [w for i, w in enumerate(words) if not w.startswith('-') and not (i and words[i - 1] == '--scale')]
            assert named and 'local-ai' not in named, f'make {target}: {line!r} must name platform services only'
            if '$services' in named:
                assert 'grep -vx local-ai' in line, (target, line)
    start = (ROOT / 'scripts' / 'start-local-services.sh').read_text()
    assert '.dev-local-ai-token' in start and 'docker compose up -d local-ai' in start
    assert 'LOCAL_AI_CPUS=' in start and "docker info --format '{{.NCPU}}'" in start
    assert '.dev-local-ai-token' in (ROOT / '.gitignore').read_text().split()


def main():
    parser = argparse.ArgumentParser(description=__doc__.split('\n', 1)[0])
    parser.add_argument('--skip-compose', action='store_true', help='skip the docker compose config checks')
    args = parser.parse_args()
    checks = [check_default, check_disabled, check_external, check_existing_secrets, check_knobs, check_failures, check_dev_wiring]
    if not args.skip_compose:
        checks.append(check_compose)
    for check in checks:
        check()
        print('ok', check.__name__)
    print('Local AI packaging checks passed')


if __name__ == '__main__':
    sys.exit(main())
