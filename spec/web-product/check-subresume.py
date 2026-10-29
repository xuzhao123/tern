"""真实子任务在同一父轮内取消、检查点续跑、旧请求版本拒绝与结果回收。"""
import argparse
import http.cookiejar
import json
import os
import signal
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
p = argparse.ArgumentParser(); p.add_argument('--home', required=True); p.add_argument('--verify', action='store_true'); args = p.parse_args()
home = Path(args.home).resolve(); assert home.name.startswith('.tern-web-product-qa')
cfg = dict(x.split('=', 1) for x in (home / '.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method, path, body=None):
    req = urllib.request.Request('http://127.0.0.1:8095' + path, method=method, data=json.dumps(body).encode() if body is not None else None, headers={'Content-Type': 'application/json'})
    try: res = opener.open(req, timeout=30)
    except urllib.error.HTTPError as error: res = error
    text = res.read().decode()
    try: value = json.loads(text)
    except json.JSONDecodeError: value = text
    return res.status, value
assert call('POST','/login',{'name':cfg.get('OBSERVER_TEST_USER','qa'),'password':cfg['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0] == 200
record = home / 'subresume-results.json'
def events(task): return [json.loads(line) for line in (home / '.data/runs' / (task + '.jsonl')).read_text().splitlines()]
def wait(task, predicate):
    until = time.monotonic() + 150
    while time.monotonic() < until:
        status, rows = call('GET','/runs'); assert status == 200
        state = next(r for r in rows if r['id'] == task)
        if predicate(state): return state
        assert not state['done'], '父任务提前结束，请检查真实事件'
        time.sleep(.5)
    raise AssertionError('等待真实模型超时，任务保留供检查')
if args.verify:
    report = json.loads(record.read_text()); task = report['task']; wait(task, lambda r:r['done'])
    history = events(task)
    starts = [e['body'] for e in history if e.get('method') == 'subagent/started']
    assert len(starts) == 2 and all(s['task_id'] == report['child'] for s in starts)
    assert starts[-1]['context_mode'] == 'checkpoint'
    completed = [e['body'] for e in history if e.get('method') == 'subagent/completed']
    assert completed[0]['state'] == 'stopped' and completed[-1]['state'] == 'done'
    assert '恢复确认' in completed[-1]['answer']
    assert len([e for e in history if e['kind'] == 'answer']) == 1
    assert history[-1]['kind'] == 'exit' and history[-1]['text'] == 'exit 0'
    report['verified'] = True; record.write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print('PASS: same child resumed within parent turn, checkpoint context, one accepted answer, parent completed normally')
    raise SystemExit
code, result = call('POST','/runs',{'provider':'deepseek','workspace':'','request_id':str(uuid.uuid4()),'query':'恢复验收：只派一个 background=true 子任务。子任务完整说明请原样转发：用 ask_user 问“恢复验收确认”，选项“恢复确认”和“取消”；等待真实用户回答，收到后只输出 RECOVERY_RESULT 和答案。如果从检查点恢复时先前提问被取消或结果未知，必须再次使用 ask_user 请求真实答案；不得把问题放进最终回答来代替交互，不得在没有真实答案时结束。父任务用 agent_wait 等待。如果子任务被外部中断，必须使用原 task_id 再调用一次 spawn_agent(background=true) 从检查点续跑，不改任务文字，不新建另一个子任务，最多恢复一次。不要由父任务提问或代答。恢复后的子任务完成后回收并汇总，不要提前结束。不要修改文件或访问网络。'})
assert code == 200, result
task = result['id']; report = {'task':task}; record.write_text(json.dumps(report))
state = wait(task, lambda r:len(r.get('waiting_requests',[])) == 1)
old = state['waiting_requests'][0]; child = old['task_id']; report.update({'child':child,'old':old}); record.write_text(json.dumps(report))
pids=[]
for proc in Path('/proc').iterdir():
    if not proc.name.isdigit(): continue
    try: cmd=(proc/'cmdline').read_bytes().split(b'\0')
    except OSError: continue
    if child.encode() in cmd and b'-agent-server' in cmd and cmd[0].startswith(str(home).encode()): pids.append(int(proc.name))
assert len(pids)==1,pids
os.kill(pids[0],signal.SIGINT)
state=wait(task,lambda r:any(w['task_id']==child and w['id']!=old['id'] for w in r.get('waiting_requests',[])))
new=next(w for w in state['waiting_requests'] if w['task_id']==child)
assert call('POST',f'/runs/{task}/answer',{'call_id':new['call_id'],'request_id':old['id'],'answer':'过期答案'})[0]==409
assert len(wait(task,lambda r:len(r.get('waiting_requests',[]))==1)['waiting_requests'])==1
report['new']=new;report['old_version_rejected']=True;record.write_text(json.dumps(report,ensure_ascii=False,indent=2))
print('READY:',task,'same child resumed; old request version rejected; answer current question in browser')
