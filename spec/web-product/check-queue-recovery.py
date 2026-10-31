"""在已停止的隔离实例中构造崩溃窗口存储状态，验证真实服务恢复；不模拟模型。"""
import argparse
import hashlib
import http.cookiejar
import json
import os
import signal
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095
p=argparse.ArgumentParser();p.add_argument('--home',required=True);a=p.parse_args()
home=Path(a.home).resolve();assert home.name.startswith(('.tern-web-product-qa','tern-qa-'))
web_pid=(home/'web.pid').read_text().strip()
binary=Path(Path('/proc',web_pid,'cmdline').read_bytes().split(b'\0')[0].decode()).resolve();assert binary.parent==home
report=json.loads((home/'queue-results.json').read_text());task=report['task'];assert report['verified']
config=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
user=config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa'))
def client():
    end=time.monotonic()+20
    o=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def call(method,path,body=None):
        req=urllib.request.Request(QA_BASE+path,method=method,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json'})
        try:r=o.open(req,timeout=5)
        except urllib.error.HTTPError as error:r=error
        text=r.read().decode()
        try:data=json.loads(text)
        except json.JSONDecodeError:data=text
        return r.status,data
    while True:
        try:
            if call('POST','/login',{'name':user,'password':(config.get('TERN_QA_PASSWORD') or config['OBSERVER_TEST_PASSWORD']).strip('\"\'')})[0]==200:return call
        except urllib.error.URLError:pass
        assert time.monotonic()<end,'QA login unavailable';time.sleep(.2)
def state(call):
    code,rows=call('GET','/runs');assert code==200
    return next(r for r in rows if r['id']==task)
def stop_pid(pid,expected):
    proc=Path(f'/proc/{pid}/cmdline')
    if not proc.exists() or (proc.parent/'stat').read_text().split(') ')[1].split()[0]=='Z':return
    cmd=proc.read_bytes().split(b'\0');assert cmd[0].startswith(str(home).encode()) and expected in cmd
    os.kill(pid,signal.SIGTERM)
    end=time.monotonic()+10
    def running():return proc.exists() and (proc.parent/'stat').read_text().split(') ')[1].split()[0]!='Z'
    while running() and time.monotonic()<end:time.sleep(.1)
    assert not running()
    try:os.waitpid(pid,os.WNOHANG)
    except ChildProcessError:pass
def stop():
    stop_pid(int((home/'web.pid').read_text()),b'web')
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():continue
        try:cmd=(proc/'cmdline').read_bytes().split(b'\0')
        except OSError:continue
        if len(cmd)>1 and cmd[0].startswith(str(home).encode()) and cmd[1]==b'app-server':stop_pid(int(proc.name),b'app-server')
def start():
    with (home/'web.log').open('ab') as log:
        proc=subprocess.Popen([str(binary),'web','-addr',QA_BASE.split('//', 1)[1]],env={**os.environ,'TERN_HOME':str(home)},stdin=subprocess.DEVNULL,stdout=log,stderr=log,start_new_session=True)
    (home/'web.pid').write_text(str(proc.pid));return client()
def history():return [json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
call=client();rows=call('GET','/runs')[1];assert all(r['done'] for r in rows),'QA has running tasks; do not stop it'
assert not state(call).get('queue')
meta_path=home/'.data/runs'/f'{task}.meta.json';original=json.loads(meta_path.read_text());(home/'queue-recovery-original-meta.json').write_text(json.dumps(original,indent=2))
baseline=len(history());started=next(e for e in history() if e.get('queue_id') and e['kind']=='continue')
# 已有 durable 启动事件，但旧元数据仍含待发项/claim：必须去重而不是再执行。
stop();meta=dict(original);item={'id':started['queue_id'],'text':started['text'],'at':started['time'],'user':user}
meta.update({'queue':[item],'queue_claim':item,'queue_version':meta.get('queue_version',0)+1});meta_path.write_text(json.dumps(meta));call=start()
r=state(call);assert not r.get('queue') and not r.get('queue_claim') and r['done'];assert len(history())==baseline
# 领取已持久化，但没有启动事件；同时有待确认的发送回执。
stop();meta=json.loads(meta_path.read_text());dispatch=str(uuid.uuid4());qid=str(uuid.uuid4());version=meta.get('queue_version',0)
item={'id':qid,'text':'RECOVERY_FIXTURE_DO_NOT_EXECUTE','at':'2026-10-25T00:00:00Z','user':user,'dispatch_request_id':dispatch,'dispatch_user':user}
params={'threadId':task,'id':qid,'version':version,'all':False,'request_id':dispatch,'user':user}
digest=hashlib.sha256(('thread/queue/send\0'+json.dumps(params,sort_keys=True,separators=(',',':'))).encode()).hexdigest()
receipt_path=home/'.data/requests'/(hashlib.sha256((user+'\0'+dispatch).encode()).hexdigest()+'.json')
receipt_path.write_text(json.dumps({'state':'pending','hash':digest}));os.chmod(receipt_path,0o600)
meta.update({'queue':[],'queue_claim':item,'queue_version':version+1});meta_path.write_text(json.dumps(meta));call=start()
r=state(call);assert r['queue_paused'] and r['queue'][0]['id']==qid and not r.get('queue_claim') and r['done']
assert len(history())==baseline
code,receipt=call('GET','/requests/'+dispatch);assert code==200 and receipt['state']=='rejected',receipt
assert call('DELETE',f'/runs/{task}/queue/{qid}',{'version':r['queue_version']})[0]==204
assert not state(call).get('queue') and not state(call).get('queue_error')
(home/'queue-recovery-results.json').write_text(json.dumps({'task':task,'storage_fixture':True,'existing_start_deduplicated':True,'unstarted_claim_paused':True,'pending_receipt_resolved':True,'no_new_events':True,'fixture_removed':True},indent=2))
print('PASS: storage crash-window fixtures recovered by real service; started item not replayed; unstarted claim restored paused; pending receipt resolved; no new model execution')
