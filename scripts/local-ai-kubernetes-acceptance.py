#!/usr/bin/env python3
"""Verify only local AI resources in an owned, disposable namespace."""
import argparse
import json
from pathlib import Path
import subprocess
import time
import uuid
import yaml

ROOT = Path(__file__).resolve().parent.parent


def command(args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True, **kwargs).stdout


def run(args):
    owner = uuid.uuid4().hex
    namespace = 'probara-ai-validation-' + owner[:10]
    kubectl = ['kubectl', '--context', args.context, '-n', namespace]
    report = {'context': args.context, 'namespace': namespace, 'image': args.image,
              'production_deployment': False, 'passed': False}
    created = False

    def apply(obj):
        command(kubectl + ['apply', '-f', '-'], input=yaml.safe_dump(obj))

    try:
        # Create atomically; never adopt or relabel an existing namespace.
        command(kubectl + ['create', '-f', '-'], input=yaml.safe_dump({
            'apiVersion': 'v1', 'kind': 'Namespace',
            'metadata': {'name': namespace, 'labels': {'probara.ai-validation': owner}}}))
        created = True
        repository, tag = args.image.rsplit(':', 1)
        rendered = list(yaml.safe_load_all(command([
            'helm', 'template', 'ai-validation', str(ROOT / 'helm/monitoring-platform'),
            '--namespace', namespace, '--set', 'localAI.image.repository=' + repository,
            '--set', 'localAI.image.tag=' + tag, '--set', 'localAI.image.pullPolicy=Never'])))
        selected = [v for v in rendered if v and v['metadata']['name'].endswith('-local-ai')]
        deployment = next(v for v in selected if v['kind'] == 'Deployment')
        container = deployment['spec']['template']['spec']['containers'][0]
        secret = next(v for v in container['env'] if v['name'] == 'LOCAL_AI_TOKEN')['valueFrom']['secretKeyRef']
        token = 'disposable-kubernetes-acceptance-token'
        apply({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': secret['name']},
               'stringData': {secret['key']: token}})
        for obj in selected:
            apply(obj)
        command(kubectl + ['rollout', 'status', 'deployment/' + deployment['metadata']['name'], '--timeout=240s'])
        labels = dict(deployment['spec']['selector']['matchLabels'])
        labels['app.kubernetes.io/component'] = 'api'
        code = f'''import json,urllib.request,urllib.error
from probara_ai.contract import options
base="http://{deployment['metadata']['name']}:8090"
def call(path,body=None,auth=True):
    request=urllib.request.Request(base+path,data=json.dumps(body).encode() if body else None,
        headers={{"Content-Type":"application/json",**({{"Authorization":"Bearer {token}"}} if auth else {{}})}})
    with urllib.request.urlopen(request,timeout=90) as response:return json.load(response)
try:call('/readyz',auth=False);raise AssertionError('unauthenticated request accepted')
except urllib.error.HTTPError as error:assert error.code==401
identity=call('/readyz')['model']
batch={{"decisions":[{{"id":str(i),"task":"rca","evidence":{{"checks":12+i,"failures":4,"dns_failures":4}},"choices":options('rca')}} for i in range(20)]}}
response=call('/v1/decisions',batch)
assert len(response['results'])==20 and response['model']==identity
assert all(abs(sum(row['probabilities'].values())-1)<1e-5 for row in response['results'])
print(json.dumps({{"model":identity,"cpu_batch_decisions":20,"unauthenticated_status":401}}))
'''
        apply({'apiVersion': 'v1', 'kind': 'Pod',
               'metadata': {'name': 'authorized-client', 'labels': labels},
               'spec': {'restartPolicy': 'Never', 'automountServiceAccountToken': False,
                        'containers': [{'name': 'client', 'image': args.image, 'imagePullPolicy': 'Never',
                                        'command': ['python', '-u', '-c', code],
                                        'resources': {'requests': {'cpu': '100m', 'memory': '128Mi'},
                                                      'limits': {'cpu': '1', 'memory': '512Mi'}}}]}})
        deadline = time.monotonic() + 180
        phase = ''
        while time.monotonic() < deadline:
            phase = command(kubectl + ['get', 'pod', 'authorized-client', '-o', 'jsonpath={.status.phase}'])
            if phase in ('Succeeded', 'Failed'):
                break
            time.sleep(2)
        report['client_phase'] = phase
        report['client_logs'] = command(kubectl + ['logs', 'authorized-client'])
        if phase != 'Succeeded':
            raise RuntimeError('Kubernetes CPU acceptance client did not succeed')
        report['client_result'] = json.loads(report.pop('client_logs'))
        report['deployment_security'] = container['securityContext']
        report['resources'] = container['resources']
        daemonsets = json.loads(command(['kubectl', '--context', args.context, '-n', 'kube-system', 'get', 'daemonsets', '-o', 'json']))
        report['network_plugins'] = [v['metadata']['name'] for v in daemonsets['items']]
        report['network_policy_enforcement_verified'] = False
        report['network_policy_limitation'] = 'Policy applied; authentication and inference verified. CNI enforcement requires separate validation.'
        report['passed'] = True
    except Exception as exc:
        report['error'] = str(exc)
        raise
    finally:
        try:
            if created:
                observed = json.loads(command(kubectl + ['get', 'namespace', namespace, '-o', 'json']))
                if observed['metadata'].get('labels', {}).get('probara.ai-validation') != owner:
                    raise RuntimeError('Namespace ownership changed; refusing cleanup')
                command(kubectl + ['delete', 'namespace', namespace, '--wait=false'])
                report['namespace_cleanup_requested'] = True
        except Exception as exc:
            report['cleanup_error'] = str(exc)
            report['passed'] = False
            raise
        finally:
            args.output.write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--context', required=True)
    parser.add_argument('--image', required=True)
    parser.add_argument('--output', type=Path, required=True)
    run(parser.parse_args())
