#!/usr/bin/env python3
"""Isolated API -> JetStream -> worker -> CPU AI acceptance environment.

Each invocation owns uniquely named PostgreSQL/NATS containers and processes.
Start/run/stop state lives outside the repository; production data is untouched.
Use --start then --run while performing browser acceptance, then --stop.
"""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
STATE = Path('/tmp/probara-local-ai-acceptance.json')
TENANT = '00000000-0000-0000-0000-000000000001'


def command(args, **kw):
    return subprocess.run(args, check=True, capture_output=True, text=True, **kw).stdout.strip()


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0)); return sock.getsockname()[1]


def sql(state, query):
    return command(['docker', 'exec', '-i', state['postgres'], 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'probara', '-d', 'probara', '-At'], input=query)


def client(state):
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def request(method, path, body=None):
        req = urllib.request.Request(state['api_url']+path, data=json.dumps(body).encode() if body is not None else None,
                                     method=method, headers={'Content-Type': 'application/json'})
        with opener.open(req, timeout=100) as response:
            data=response.read();return json.loads(data) if data else None
    return request


def wait(fn, limit=60):
    deadline=time.monotonic()+limit
    while True:
        try:
            value=fn()
            if value:return value
        except Exception:
            if time.monotonic()>=deadline:raise
        if time.monotonic()>=deadline:raise TimeoutError('Acceptance readiness deadline exceeded')
        time.sleep(.2)


def start(args):
    if STATE.exists():raise RuntimeError('Existing acceptance state; run --stop first')
    suffix=uuid.uuid4().hex[:10]
    state={'owner':suffix,'postgres':'probara-ai-pg-'+suffix,'nats':'probara-ai-nats-'+suffix,'pids':[], 'tenant':TENANT}
    STATE.write_text(json.dumps(state))
    try:
        for key,image,extra in (('postgres','postgres:16-alpine',['-e','POSTGRES_USER=probara','-e','POSTGRES_PASSWORD=acceptance-only','-e','POSTGRES_DB=probara','-p','127.0.0.1::5432']),('nats','nats:2.10-alpine',['-p','127.0.0.1::4222'])):
            command(['docker','run','-d','--name',state[key],'--label','probara.ai-acceptance='+suffix,*extra,image,*(['-js'] if key=='nats' else ['-c','synchronous_commit=off','-c','full_page_writes=off'])])
        pg_port=command(['docker','port',state['postgres'],'5432/tcp']).rsplit(':',1)[1]
        nats_port=command(['docker','port',state['nats'],'4222/tcp']).rsplit(':',1)[1]
        wait(lambda:command(['docker','exec',state['postgres'],'pg_isready','-U','probara']))
        env=dict(os.environ,POSTGRES_URL=f'postgres://probara:acceptance-only@127.0.0.1:{pg_port}/probara?sslmode=disable',
                 NATS_URL=f'nats://127.0.0.1:{nats_port}',LOCAL_AI_URL=args.model_url,LOCAL_AI_TOKEN=args.token,
                 LOCAL_AI_DISABLED='false',ADMIN_JWT_SECRET='isolated-acceptance-jwt-key',ADMIN_BCRYPT_COST='4',LOG_LEVEL='warn',GOTOOLCHAIN='go1.26.7')
        state['api_port']=port();state['api_url']=f"http://127.0.0.1:{state['api_port']}";state['ui_port']=port()
        for service in ('api','worker'):
            binary=f'/tmp/probara-ai-acceptance-{suffix}-{service}'
            command(['go','build','-o',binary,f'./{service}/cmd/{service}'],cwd=ROOT,env=env)
            service_env=dict(env,HTTP_PORT=str(state['api_port'] if service=='api' else port()),METRICS_PORT=str(port()))
            log=open(f'/tmp/probara-ai-acceptance-{service}.log','w')
            process=subprocess.Popen([binary],cwd=ROOT,env=service_env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
            state['pids'].append({'pid':process.pid,'binary':binary});STATE.write_text(json.dumps(state))
        request=client(state);wait(lambda:request('GET','/api/v1/users/bootstrap/status'))
        request('POST','/api/v1/users/bootstrap/first',{'username':'ai-acceptance','password':'Acceptance-fixture-2026!'})
        # Seed a 20-monitor DNS incident, an unsupported incident, and repeated
        # co-firing history with known dependency direction.
        incident=str(uuid.uuid4());unknown=str(uuid.uuid4());a=str(uuid.uuid4());b=str(uuid.uuid4())
        state.update(incident=incident,unknown_incident=unknown,upstream=a,dependent=b)
        queries=[f"INSERT INTO incidents(id,tenant_id,title,created_at) VALUES('{incident}','{TENANT}','CPU DNS acceptance',NOW()-INTERVAL '10 minutes'),('{unknown}','{TENANT}','Unsupported evidence acceptance',NOW()-INTERVAL '10 minutes');"]
        for i in range(21):
            mid=str(uuid.uuid4());name=f'rca-fixture-{i}';inc=incident if i<20 else unknown
            queries.append(f"INSERT INTO monitors(id,tenant_id,name,type,config,interval_seconds,timeout_seconds,enabled) VALUES('{mid}','{TENANT}','{name}','http','{{\"url\":\"https://example.invalid\"}}',60,10,TRUE);INSERT INTO incident_monitors(incident_id,monitor_id) VALUES('{inc}','{mid}');")
            if i<20:
                queries.append(f"INSERT INTO check_results(monitor_id,tenant_id,job_id,status,error_message,latency_ms,created_at,started_at,completed_at) SELECT '{mid}','{TENANT}',uuid_generate_v4(),CASE WHEN j<4 THEN 'failure' ELSE 'success' END,CASE WHEN j<4 THEN 'dns lookup: no such host' ELSE '' END,100,NOW()-INTERVAL '10 minutes'+j*INTERVAL '1 minute',NOW()-INTERVAL '10 minutes'+j*INTERVAL '1 minute',NOW()-INTERVAL '10 minutes'+j*INTERVAL '1 minute' FROM generate_series(0,8) j;")
        for mid,name in ((a,'checkout-database'),(b,'checkout-frontend')):
            queries.append(f"INSERT INTO monitors(id,tenant_id,name,type,config,interval_seconds,timeout_seconds,enabled,tags) VALUES('{mid}','{TENANT}','{name}','http','{{\"url\":\"https://example.invalid\"}}',60,10,TRUE,ARRAY['service:checkout']);")
        queries.append(f"INSERT INTO alerts(id,tenant_id,monitor_id,triggered_at,resolved_at,failure_count,status,kind) SELECT uuid_generate_v4(),'{TENANT}',m, date_trunc('hour',NOW())-INTERVAL '1 day'+j*INTERVAL '10 minutes'+CASE WHEN m='{b}'::uuid THEN INTERVAL '30 seconds' ELSE INTERVAL '0 seconds' END,date_trunc('hour',NOW())-INTERVAL '1 day'+j*INTERVAL '10 minutes'+INTERVAL '2 minutes',1,'resolved','availability' FROM generate_series(0,6) j CROSS JOIN unnest(ARRAY['{a}'::uuid,'{b}'::uuid]) m;")
        sql(state,'\n'.join(queries));STATE.write_text(json.dumps(state))
        print(json.dumps({'api_url':state['api_url'],'ui_port':state['ui_port'],'incident':incident,'unknown_incident':unknown,'state':str(STATE)}))
    except Exception:
        stop();raise


def run():
    state=json.loads(STATE.read_text());request=client(state)
    request('POST','/api/v1/auth/login',{'username':'ai-acceptance','password':'Acceptance-fixture-2026!'})
    scoped=lambda path:path+'?tenant_id='+TENANT
    settings=request('GET',scoped('/api/v1/ai-settings'));assert settings['enabled'] and settings['available'],settings
    report={'settings':settings,'analyses':[]}
    for incident,hypothesis in ((state['incident'],'dns_failure'),(state['unknown_incident'],'insufficient_evidence')):
        before=time.monotonic();pending=request('POST',scoped(f'/api/v1/incidents/{incident}/ai-analysis'),{})
        assert pending['status']=='pending'
        def completed():
            row=request('GET',scoped(f'/api/v1/incidents/{incident}/ai-analysis'))
            return row if row and row['status']!='pending' else None
        result=wait(completed,100)
        assert result['status']=='ready' and result['metadata']['hypothesis']==hypothesis,result
        assert result['metadata']['engine']=='laya-cpu'
        report['analyses'].append({'hypothesis':hypothesis,'seconds':time.monotonic()-before,'evidence_references':len(result['metadata']['evidence_references'])})
    suggestions=request('POST',scoped('/api/v1/monitors/dependency-suggestions'),{})
    proposal=next((s for s in suggestions['suggestions'] if s['monitor_id']==state['dependent'] and s['depends_on_id']==state['upstream']),None)
    assert proposal,suggestions
    request('POST',scoped(f"/api/v1/monitors/{state['dependent']}/dependencies"),{'depends_on_id':state['upstream']})
    try:request('POST',scoped(f"/api/v1/monitors/{state['upstream']}/dependencies"),{'depends_on_id':state['dependent']});raise AssertionError('Cycle accepted')
    except urllib.error.HTTPError as error:assert error.code==409,error.code  # dependency_cycle
    report['suggestions']={'analyzed_pairs':suggestions['analyzed_pairs'],'proposals':len(suggestions['suggestions']),'accepted':True,'cycle_rejected':True}
    request('PUT',scoped('/api/v1/ai-settings'),{'enabled':False})
    try:request('POST',scoped(f"/api/v1/incidents/{state['incident']}/ai-analysis"),{});raise AssertionError('Disabled workspace analyzed')
    except urllib.error.HTTPError as error:assert error.code==503
    request('PUT',scoped('/api/v1/ai-settings'),{'enabled':True})
    try:request('PUT',scoped('/api/v1/ai-settings'),{'enabled':True,'base_url':'https://example.invalid'});raise AssertionError('Legacy destination accepted')
    except urllib.error.HTTPError as error:assert error.code==400
    report['workspace_disable_and_legacy_validation']=True
    Path('ai-service/artifacts/acceptance.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))


def stop():
    if not STATE.exists():return
    state=json.loads(STATE.read_text())
    for item in state['pids']:
        observed=command(['ps','-p',str(item['pid']),'-o','comm=']) if subprocess.run(['kill','-0',str(item['pid'])],capture_output=True).returncode==0 else ''
        if observed.strip()==item['binary']:
            os.killpg(item['pid'],signal.SIGTERM)
    for key in ('postgres','nats'):
        result=subprocess.run(['docker','inspect','--format','{{index .Config.Labels "probara.ai-acceptance"}}',state[key]],capture_output=True,text=True)
        if result.returncode==0 and result.stdout.strip()==state['owner']:
            command(['docker','rm','-f','-v',state[key]])
    STATE.unlink()


if __name__=='__main__':
    p=argparse.ArgumentParser();g=p.add_mutually_exclusive_group(required=True);g.add_argument('--start',action='store_true');g.add_argument('--run',action='store_true');g.add_argument('--stop',action='store_true')
    p.add_argument('--model-url',default='http://127.0.0.1:18090');p.add_argument('--token',default='isolated-local-ai-acceptance-token')
    args=p.parse_args()
    if args.start:start(args)
    elif args.run:run()
    else:stop()
