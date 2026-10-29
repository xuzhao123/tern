"""真实隔离服务的队列并发、停止/重启、原子领取与逐轮执行验收。"""
import argparse
import concurrent.futures
import http.cookiejar
import json
import os
import signal
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095
p = argparse.ArgumentParser(); p.add_argument('--home', required=True); p.add_argument('--mode', choices=['start','finish'], required=True); a = p.parse_args()
home = Path(a.home).resolve(); assert home.name.startswith(('.tern-web-product-qa','tern-qa-'))
cfg=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
def client():
    o=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def call(method,path,body=None):
        req=urllib.request.Request(QA_BASE+path,method=method,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json'})
        try: r=o.open(req,timeout=30)
        except urllib.error.HTTPError as error:r=error
        text=r.read().decode()
        try: value=json.loads(text)
        except json.JSONDecodeError:value=text
        return r.status,value
    assert call('POST','/login',{'name':cfg.get('OBSERVER_TEST_USER','qa'),'password':cfg['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0]==200
    return call
call,other=client(),client(); record=home/'queue-results.json'
def task(id):
    code,rows=call('GET','/runs')
    return next(r for r in rows if r['id']==id) if code==200 else None
def wait(id,predicate):
    until=time.monotonic()+150
    while time.monotonic()<until:
        r=task(id)
        if r and predicate(r):return r
        time.sleep(.3)
    raise AssertionError('真实队列任务未到预期状态: '+id)
def parallel(jobs):
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(jobs)) as pool:
        fs=[pool.submit(fn,*args) for fn,args in jobs]
        return [f.result() for f in fs]
def restart():
    pids=[]
    for path in Path('/proc').iterdir():
        if not path.name.isdigit():continue
        try:args=(path/'cmdline').read_bytes().split(b'\0')
        except OSError:continue
        if len(args)>1 and args[0].startswith(str(home).encode()) and args[1]==b'app-server':pids.append(int(path.name))
    assert len(pids)==1
    os.kill(pids[0],signal.SIGTERM)
    until=time.monotonic()+15
    while Path(f'/proc/{pids[0]}').exists() and time.monotonic()<until:time.sleep(.2)
    assert not Path(f'/proc/{pids[0]}').exists()
def events(id):return [json.loads(l) for l in (home/'.data/runs'/f'{id}.jsonl').read_text().splitlines()]
if a.mode=='start':
    code,res=call('POST','/runs',{'provider':'deepseek','workspace':'','request_id':str(uuid.uuid4()),'query':'队列验收：请用 ask_user 问“是否开始后续队列验收”，选项“继续”和“暂缓”。等待用户真实回答，不调用其他工具。'})
    assert code==200,res
    id=res['id']; record.write_text(json.dumps({'task':id}))
    wait(id,lambda r:r.get('waiting')=='question')
    route=f'/runs/{id}/queue'
    bodies=[{'text':f'只回复 QUEUE_{i}，不调用工具。','request_id':str(uuid.uuid4())} for i in range(1,4)]
    result=parallel([(call,('POST',route,bodies[0])),(other,('POST',route,bodies[1]))]);assert all(code==200 for code,_ in result),result
    result=parallel([(call,('POST',route,bodies[2])),(other,('POST',route,bodies[2]))]);assert all(code in (200,409) for code,_ in result),result
    assert call('GET','/requests/'+bodies[2]['request_id'])[1]['state']=='accepted'
    state=task(id);assert len(state['queue'])==3
    initial_ids={q['id'] for q in state['queue']};assert len(initial_ids)==3
    version=state['queue_version'];qid=state['queue'][0]['id']
    result=parallel([(call,('PUT',route+'/'+qid,{'text':'只回复 QUEUE_EDIT_A，不调用工具。','version':version})),(other,('PUT',route+'/'+qid,{'text':'只回复 QUEUE_EDIT_B，不调用工具。','version':version}))]);assert sorted(code for code,_ in result)==[200,409],result
    state=task(id);version=state['queue_version'];ids=[q['id'] for q in state['queue']]
    assert call('PUT',route,{'ids':[ids[0]]*len(ids),'version':version})[0]==400
    result=parallel([(call,('PUT',route,{'ids':list(reversed(ids)),'version':version})),(other,('PUT',route,{'ids':ids[1:]+ids[:1],'version':version}))]);assert sorted(code for code,_ in result)==[200,409],result
    # 元数据事务不能因改名/编辑并发丢掉另一种字段。
    state=task(id);qid=state['queue'][0]['id']
    result=parallel([(call,('PUT',f'/runs/{id}/name',{'name':'队列并发验收'})),(other,('PUT',route+'/'+qid,{'text':'只回复 QUEUE_FINAL，不调用工具。','version':state['queue_version']}))]);assert all(code==200 for code,_ in result),result
    state=task(id);assert state['name']=='队列并发验收' and len(state['queue'])==3 and state['queue'][0]['text'].startswith('只回复 QUEUE_FINAL')
    record.write_text(json.dumps({'task':id,'ids':list(initial_ids),'concurrency_passed':True},indent=2));print('READY:',id,'three real queued items; add dedup, edit/reorder CAS and metadata concurrency passed')
else:
    report=json.loads(record.read_text());id=report['task'];route=f'/runs/{id}/queue'
    baseline=[e['queue_id'] for e in events(id) if e.get('queue_id') and e['kind']=='continue']
    if not baseline and not task(id)['done']: assert call('POST',f'/runs/{id}/stop')[0]==204
    state=wait(id,lambda r:r['done']);assert state['queue_paused'] and len(state['queue'])>=3
    expected=[q['id'] for q in state['queue']];version=state['queue_version']
    restart();state=wait(id,lambda r:r['done'] and r.get('queue_paused'))
    assert [q['id'] for q in state['queue']]==expected and state['queue_version']==version
    body={'id':expected[0],'version':version,'all':False,'request_id':str(uuid.uuid4())}
    result=parallel([(call,('POST',route+'/send',body)),(other,('POST',route+'/send',body))]);assert all(code in (200,409) for code,_ in result),result
    state=wait(id,lambda r:r['done'] and not r.get('queue_claim'))
    assert state['queue_paused'] and [q['id'] for q in state['queue']]==expected[1:],state
    dispatched=[e['queue_id'] for e in events(id) if e.get('queue_id') and e['kind']=='continue'][len(baseline):];assert dispatched==expected[:1],dispatched
    assert call('PUT',route+'/'+expected[0],{'text':'不能改已发项','version':state['queue_version']})[0] in (404,409)
    restart();state=wait(id,lambda r:r['done'] and r.get('queue_paused'))
    body={'id':state['queue'][0]['id'],'version':state['queue_version'],'all':True,'request_id':str(uuid.uuid4())}
    assert call('POST',route+'/send',body)[0]==200
    state=wait(id,lambda r:r['done'] and not r.get('queue') and not r.get('queue_claim'))
    dispatched=[e['queue_id'] for e in events(id) if e.get('queue_id') and e['kind']=='continue'][len(baseline):];assert dispatched==expected,dispatched
    assert len(set(dispatched))==len(expected)
    assert call('POST',route,{'text':'已停止时不能入队'})[0]==409
    report.update({'verified':True,'dispatched':baseline+dispatched,'ui_first':baseline,'restart_verified':True,'paused_single_all_verified':True});record.write_text(json.dumps(report,indent=2))
    print('PASS: stop/restart retain pause; send-first consumes once; continue-all consumes exact order; no duplicate queue turns')
