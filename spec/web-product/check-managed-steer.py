"""使用隔离真实服务和真实模型验证 managed 补充；不模拟模型或工具输出。"""
import argparse
import concurrent.futures
import hashlib
import http.cookiejar
import json
import os
import signal
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--home',required=True);p.add_argument('--scenario',choices=['legacy','consumed','stop','immediate','stopping','unknown','natural','hook','storage','ui'],required=True);p.add_argument('--resume-unknown',action='store_true');a=p.parse_args()
home=Path(a.home).resolve();assert home.name.startswith('.tern-web-product-qa')
cfg=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
def client():
    opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def call(method,path,body=None):
        request=urllib.request.Request('http://127.0.0.1:8095'+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
        try:response=opener.open(request,timeout=30)
        except urllib.error.HTTPError as error:response=error
        text=response.read().decode()
        try:value=json.loads(text)
        except json.JSONDecodeError:value=text
        return response.status,value
    assert call('POST','/login',{'name':cfg.get('OBSERVER_TEST_USER','qa'),'password':cfg['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0]==200
    return call
call,other=client(),client()
report={'scenario':a.scenario};report_path=home/f'managed-steer-{a.scenario}.json'
def state(task):
    code,rows=call('GET','/runs')
    return next((r for r in rows if r['id']==task),None) if code==200 else None
def events(task):return [json.loads(line) for line in (home/'.data/runs'/f'{task}.jsonl').read_bytes().split(b'\n')[:-1] if line]
def meta(task):return json.loads((home/'.data/runs'/f'{task}.meta.json').read_text())
def wait(task,predicate):
    until=time.monotonic()+150
    while time.monotonic()<until:
        current=state(task)
        if current and predicate(current):return current
        time.sleep(.2)
    raise AssertionError('真实执行未到预期状态: '+task)
def create(query=None):
    code,result=call('POST','/runs',{'provider':'deepseek','workspace':'','request_id':str(uuid.uuid4()),'query':query or '补充验收：先用 ask_user 问“是否继续验证”，选项“继续”和“等待”。等待真实回答；回答后优先遵循最新的用户补充，没有补充则只回复 HOLD_DONE。不调用其他工具。'})
    assert code==200,result
    task=result['id'];report['task']=task;report_path.write_text(json.dumps(report))
    if query is None:wait(task,lambda r:r.get('waiting')=='question')
    return task

def send(task,text,duplicate=False):
    current=state(task);id=str(uuid.uuid4());body={'text':text,'managed':True,'client_id':id,'request_id':id,'turn_seq':current['steer_turn_seq']}
    wrong=str(uuid.uuid4())
    assert call('POST',f'/runs/{task}/steer',{**body,'client_id':wrong,'request_id':wrong,'turn_seq':current['steer_turn_seq']+1})[0]==409
    if duplicate:
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            pending=[pool.submit(c,'POST',f'/runs/{task}/steer',body) for c in (call,other)]
            responses=[job.result() for job in pending]
        assert all(code in (200,409) for code,_ in responses),responses
    else:
        code,result=call('POST',f'/runs/{task}/steer',body);assert code==200,result
    assert call('GET','/requests/'+id)[1]['state']=='accepted'
    assert call('POST',f'/runs/{task}/steer',{**body,'text':'不同内容'})[0]==409
    report.setdefault('inputs',[]).append(body);report_path.write_text(json.dumps(report,ensure_ascii=False,indent=2))
    return id

def pending_state(task,id,desired):return wait(task,lambda r:any(item['id']==id and item['state']==desired for item in r.get('steer_pending',[])))
def answer(task):
    req=next(r for r in state(task)['waiting_requests'] if r['kind']=='question')
    assert call('POST',f'/runs/{task}/answer',{'call_id':req['call_id'],'request_id':req['id'],'answer':'继续'})[0]==204

def restart():
    pids=[]
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():continue
        try:args=(proc/'cmdline').read_bytes().split(b'\0')
        except OSError:continue
        if len(args)>1 and args[0].startswith(str(home).encode()) and args[1]==b'app-server':pids.append(int(proc.name))
    assert len(pids)==1
    os.kill(pids[0],signal.SIGTERM)
    until=time.monotonic()+15
    while Path(f'/proc/{pids[0]}').exists() and time.monotonic()<until:time.sleep(.2)
    assert not Path(f'/proc/{pids[0]}').exists()
def checkpoint(task):
    id=next(e['body']['turn_id'] for e in reversed(events(task)) if e.get('method')=='turn/started')
    return json.loads((home/'.data/checkpoints'/f'{id}.json').read_text())
def continued(task):return [e for e in events(task) if e['kind']=='continue']

def verify_consumed(task,id,text,blocked=False):
    wait(task,lambda r:r['done'])
    data=meta(task);record=next(i for i in data['managed_steers'] if i['id']==id)
    expected='blocked' if blocked else 'consumed'
    assert record['state']==expected and not record.get('text'),record
    receipts=[e['body']['item']['data'] for e in events(task) if e.get('method')=='item/completed' and e['body'].get('item',{}).get('type')=='userMessage' and e['body']['item'].get('data',{}).get('client_id')==id]
    assert len(receipts)==1 and receipts[0]['state']==expected,receipts
    cp=checkpoint(task);assert cp['version']==3 and cp['state']['managed_steers'][id]['state']==expected
    assert cp['state']['managed_steers'][id]['hash']==hashlib.sha256(text.encode()).hexdigest()
    found=[m for m in cp['messages'] if m.get('role')=='user' and m.get('content')==text]
    assert len(found)==(0 if blocked else 1)
    assert not continued(task)

original_platform=None
storage_block=None
suspended_pid=None
try:
    if a.scenario=='hook':
        original_platform=call('GET','/settings')[1]['platform'] or {}
        hook=home/'managed-steer-hook.py'
        hook.write_text('import json,sys\np=json.load(sys.stdin)\nprint(json.dumps({"decision":"block","reason":"测试提交规则拦截"} if "STEER_BLOCK_FIXTURE" in p.get("prompt","") else {}))\n')
        settings={**original_platform,'hooks':json.dumps({'hooks':{'UserPromptSubmit':[{'hooks':[{'type':'command','command':'/usr/bin/python3 '+str(hook),'timeout':5}]}]}})}
        assert call('PUT','/settings/platform',settings)[0]==200
    if a.scenario=='legacy':
        task=json.loads((home/'steer-legacy.json').read_text())['task'];report['task']=task
        current=state(task);assert current['steer_managed'] and not current['done']
        id=send(task,'只回复 MANAGED_LEGACY_OK，不调用工具。')
        pending_state(task,id,'deferred')
        assert call('POST',f'/runs/{task}/stop',{'steer_ids':[id],'steer_turn':current['steer_turn_seq']})[0]==204
        wait(task,lambda r:r['done'] and len(continued(task))==1)
        assert len(continued(task))==1 and 'MANAGED_LEGACY_OK' in checkpoint(task)['answer']
        report['old_agent_deferred']=True
    elif a.scenario=='ui':
        task=create();print('READY UI:',task);raise SystemExit
    elif a.scenario=='natural':
        task=create('请写一篇约 800 字的中文短文，主题是春天的公园，不调用工具。')
        wait(task,lambda r:any(e['kind']=='request' for e in events(task)))
        id=send(task,'不要调用工具，最新要求是只回复 NATURAL_NEXT_OK。')
        wait(task,lambda r:r['done'] and len(continued(task))==1)
        assert len(continued(task))==1 and 'NATURAL_NEXT_OK' in checkpoint(task)['answer']
        report['natural_fallback_once']=True
    else:
        text='STEER_BLOCK_FIXTURE 这段测试文字不可进入模型。' if a.scenario=='hook' else '最新补充：不要调用其他工具，只回复 STEER_ONE。'
        if a.resume_unknown:
            assert a.scenario=='unknown'
            report=json.loads(report_path.read_text());task=report['task'];id=report['inputs'][-1]['client_id']
        else:
            task=create();id=send(task,text,duplicate=True)
        pending_state(task,id,'accepted')
        if a.scenario=='storage':
            ordinary=call('POST',f'/runs/{task}/queue',{'text':'只回复 STORAGE_QUEUE_OK，不调用工具。','request_id':str(uuid.uuid4())})[1]
            storage_block=home/'.data/runs'/f'{task}.meta.json.tmp'
            storage_block.mkdir()  # 只阻止本验收任务的原子写入，不改变其他任务或存储权限。
            answer(task)
            current=wait(task,lambda r:r['done'] and bool(r.get('steer_error')))
            assert not continued(task) and current['queue'][0]['id']==ordinary['id']
            assert next(i for i in meta(task)['managed_steers'] if i['id']==id)['state']=='accepted'
            assert checkpoint(task)['state']['managed_steers'][id]['state']=='consumed'
            storage_block.rmdir();storage_block=None
            assert call('POST',f'/runs/{task}/steer/refresh',{})[0]==200
            current=wait(task,lambda r:r['done'] and len(continued(task))==1)
            assert not current.get('steer_error') and not current.get('steer_pending')
            assert 'STORAGE_QUEUE_OK' in checkpoint(task)['answer']
            assert len([e for e in events(task) if e.get('method')=='item/completed' and e['body'].get('item',{}).get('data',{}).get('client_id')==id])==1
            report['storage_failure_preserves_and_recovers']=True
        elif a.scenario in ('consumed','hook'):
            if a.scenario=='consumed':
                restart();pending_state(task,id,'accepted');report['restart_verified']=True
            answer(task);verify_consumed(task,id,text,blocked=a.scenario=='hook')
            if a.scenario=='hook':
                assert 'STEER_BLOCK_FIXTURE' not in (home/'.data/runs'/f'{task}.jsonl').read_text()
                assert 'STEER_BLOCK_FIXTURE' not in (home/'.data/runs'/f'{task}.meta.json').read_text()
                assert call('POST',f'/runs/{task}/steer/resolve',{'id':id,'action':'queue'})[0]==409
                report['blocked_text_removed']=True
        elif a.scenario in ('unknown','stopping'):
            info=json.loads((home/'.data/live'/f'{task}.json').read_text());pid=info['pid']
            proc=Path('/proc')/str(pid);assert str(home) in os.readlink(proc/'exe') and b'-agent-server' in (proc/'cmdline').read_bytes().split(b'\0')
            if a.scenario=='stopping':
                os.kill(pid,signal.SIGSTOP);suspended_pid=pid
                assert call('POST',f'/runs/{task}/stop')[0]==204
                assert call('POST',f'/runs/{task}/stop',{'steer_ids':[id],'steer_turn':state(task)['steer_turn_seq']})[0]==204
                assert proc.exists() and (proc/'cmdline').read_bytes()
                assert not any(e['kind']=='stop' and 'SIGKILL' in e.get('text','') for e in events(task))
                os.kill(pid,signal.SIGCONT);suspended_pid=None
                wait(task,lambda r:r['done'] and len(continued(task))==1)
                assert 'STEER_ONE' in checkpoint(task)['answer']
                report['immediate_during_stop_never_forces']=True
                report['verified']=True;report_path.write_text(json.dumps(report,ensure_ascii=False,indent=2));print('PASS:',a.scenario,task)
                raise SystemExit
            os.kill(pid,signal.SIGKILL)
            wait(task,lambda r:r['done'] and any(i['id']==id and i['state']=='unknown' for i in r.get('steer_pending',[])))
            assert not continued(task) and not state(task).get('queue')
            assert call('POST',f'/runs/{task}/steer/resolve',{'id':id,'action':'queue'})[0]==200
            current=state(task);assert current['queue_paused'] and current['queue'][0]['steer_ids']==[id]
            assert not continued(task)
            assert call('DELETE',f'/runs/{task}/queue/{current["queue"][0]["id"]}',{'version':current['queue_version']})[0]==204
            report['unknown_no_automatic_retry']=True
        elif a.scenario=='stop':
            assert call('POST',f'/runs/{task}/stop')[0]==204
            current=wait(task,lambda r:r['done'] and bool(r.get('queue')))
            assert current['queue_paused'] and current['queue'][0]['steer_ids']==[id] and not continued(task)
            body={'id':current['queue'][0]['id'],'version':current['queue_version'],'all':False,'request_id':str(uuid.uuid4())}
            assert call('POST',f'/runs/{task}/queue/send',body)[0]==200
            wait(task,lambda r:r['done'] and len(continued(task))==1)
            assert 'STEER_ONE' in checkpoint(task)['answer'];report['stop_preserves_without_send']=True
        elif a.scenario=='immediate':
            ordinary=call('POST',f'/runs/{task}/queue',{'text':'只回复 ORDINARY_QUEUE_HOLD，不调用工具。','request_id':str(uuid.uuid4())})[1]
            current=state(task);body={'steer_ids':[id],'steer_turn':current['steer_turn_seq']}
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                responses=[f.result() for f in [pool.submit(c,'POST',f'/runs/{task}/stop',body) for c in (call,other)]]
            assert all(code in (204,409) for code,_ in responses),responses
            current=wait(task,lambda r:r['done'] and len(continued(task))==1)
            assert current['queue_paused'] and [q['id'] for q in current['queue']]==[ordinary['id']]
            assert 'STEER_ONE' in checkpoint(task)['answer']
            assert not any(e['kind']=='stop' and 'SIGKILL' in e.get('text','') for e in events(task))
            assert call('DELETE',f'/runs/{task}/queue/{ordinary["id"]}',{'version':current['queue_version']})[0]==204
            report['only_unconsumed_group_sent']=True
    report['verified']=True
    report_path.write_text(json.dumps(report,ensure_ascii=False,indent=2))
    print('PASS:',a.scenario,task)
finally:
    if suspended_pid is not None:
        os.kill(suspended_pid,signal.SIGCONT)
    if storage_block is not None:
        storage_block.rmdir()
    if original_platform is not None:
        assert call('PUT','/settings/platform',original_platform)[0]==200,'restore QA hook settings failed'
