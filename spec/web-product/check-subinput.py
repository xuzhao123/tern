"""隔离服务中真实子 agent 提问验收，start 后可在网页回答，再 verify。"""
import argparse
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

p = argparse.ArgumentParser()
p.add_argument('--home', required=True)
p.add_argument('--mode', choices=['start', 'verify', 'cancel', 'restart'], required=True)
args = p.parse_args()
home = Path(args.home).resolve()
assert home.name.startswith('.tern-web-product-qa')
config = dict(x.split('=', 1) for x in (home / '.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method, path, body=None):
    req = urllib.request.Request('http://127.0.0.1:8095' + path, method=method, data=json.dumps(body).encode() if body is not None else None, headers={'Content-Type': 'application/json'})
    try: response = opener.open(req, timeout=30)
    except urllib.error.HTTPError as error: response = error
    text = response.read().decode()
    try: data = json.loads(text)
    except json.JSONDecodeError: data = text
    return response.status, data
assert call('POST', '/login', {'name': config.get('OBSERVER_TEST_USER', 'qa'), 'password': config['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0] == 200
record = home / 'subinput-results.json'
def get(task):
    code, rows = call('GET', '/runs')
    if code != 200: return None
    return next((r for r in rows if r['id'] == task), None)
def wait(task, condition):
    end = time.monotonic() + 150
    while time.monotonic() < end:
        state = get(task)
        if state and condition(state): return state
        time.sleep(.5)
    raise AssertionError('真实子任务没有到达预期状态: ' + task)
def read_events(task):
    # 活动中的 SSE 不会结束；直接读隔离实例自己的真实存档来验收，不代替应用接口。
    return [json.loads(line) for line in (home / '.data/runs' / (task + '.jsonl')).read_text().splitlines()]

if args.mode == 'start':
    body = {'provider': 'deepseek', 'workspace': '', 'request_id': str(uuid.uuid4()), 'query': '这是子 agent 提问的产品验收。请先用 spawn_agent 派两个 background=true 的独立子任务，两个都启动后再用 agent_wait 等待并汇总。ALPHA 子任务说明：用 ask_user 问“ALPHA 选择输出格式”，选项“JSON”和“文本”；等用户真实回答后只输出 ALPHA_RESULT 和所选答案。BETA 子任务说明：用 ask_user 问“BETA 选择显示主题”，选项“浅色”和“深色”；等用户真实回答后只输出 BETA_RESULT 和所选答案。父任务自己不要提问，不要代替用户回答，不要停止子任务；两个结果都回收后再结束。不要修改文件或访问网络。'}
    code, created = call('POST', '/runs', body)
    assert code == 200, created
    task = created['id']
    record.write_text(json.dumps({'task': task}, indent=2))
    state = wait(task, lambda r: len(r.get('waiting_requests', [])) == 2)
    requests = state['waiting_requests']
    assert not state['done'] and len({r['task_id'] for r in requests}) == 2
    assert len({r['call_id'] for r in requests}) == 2
    events = read_events(task)
    for request in requests:
        event = next(e for e in events if e.get('kind') == 'approval' and e.get('body', {}).get('call_id') == request['call_id'])
        body = event['body']
        assert body['original_call_id'] and body['task_id'] == request['task_id']
        expected = 'sub-' + hashlib.sha256((body['task_id'] + '\0' + body['original_call_id']).encode()).hexdigest()[:32]
        assert body['call_id'] == expected
        assert request['id'] == 'event:' + str(event['seq'])
        assert request['task_title']
    record.write_text(json.dumps({'task': task, 'requests': requests}, ensure_ascii=False, indent=2))
    print('READY:', task, 'two independently identified child questions')
    raise SystemExit
report = json.loads(record.read_text()); task = report['task']
if args.mode == 'restart':
    before = get(task)
    assert len(before['waiting_requests']) == 2 and not before['done']
    pids = []
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit(): continue
        try: cmd = (proc / 'cmdline').read_bytes().split(b'\0')
        except OSError: continue
        if len(cmd) > 1 and cmd[0].startswith(str(home).encode()) and cmd[1] == b'app-server': pids.append(int(proc.name))
    assert len(pids) == 1, pids
    os.kill(pids[0], signal.SIGTERM)
    state = wait(task, lambda r: len(r.get('waiting_requests', [])) == 2)
    # wait 还可能先看到旧进程的响应；确认实际旧句柄已消失再核验。
    until = time.monotonic() + 15
    while Path(f'/proc/{pids[0]}').exists() and time.monotonic() < until: time.sleep(.2)
    assert not Path(f'/proc/{pids[0]}').exists()
    state = wait(task, lambda r: len(r.get('waiting_requests', [])) == 2)
    assert {(r['id'], r['call_id'], r['task_id']) for r in before['waiting_requests']} == {(r['id'], r['call_id'], r['task_id']) for r in state['waiting_requests']}
    report['restart_verified'] = True
    print('PASS: pending child questions adopted with stable identity')
elif args.mode == 'cancel':
    state = get(task); assert len(state['waiting_requests']) == 2
    target = next(r for r in state['waiting_requests'] if 'ALPHA' in r['task_title'])
    sibling = next(r for r in state['waiting_requests'] if r['call_id'] != target['call_id'])
    assert call('POST', f'/runs/{task}/answer', {'call_id': target['call_id'], 'request_id': sibling['id'], 'answer': '错误请求版本'})[0] == 409
    assert len(get(task)['waiting_requests']) == 2
    report['version_rejected'] = True
    pids = []
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit(): continue
        try: cmd = (proc / 'cmdline').read_bytes().split(b'\0')
        except OSError: continue
        if target['task_id'].encode() in cmd and b'-agent-server' in cmd and cmd[0].startswith(str(home).encode()): pids.append(int(proc.name))
    assert len(pids) == 1, pids
    os.kill(pids[0], signal.SIGINT)
    state = wait(task, lambda r: all(w['call_id'] != target['call_id'] for w in r.get('waiting_requests', [])))
    assert not state['done'] and len(state['waiting_requests']) == 1, state
    assert call('POST', f'/runs/{task}/answer', {'call_id': target['call_id'], 'answer': '迟到回答'})[0] == 409
    original = next(e for e in read_events(task) if e.get('kind') == 'approval' and e.get('body', {}).get('call_id') == target['call_id'])
    assert any(e['kind'] == 'input_closed' and e['rpc'] == original['rpc'] for e in read_events(task))
    report['cancelled'] = target['call_id']
    print('PASS: child cancellation clears only its request; sibling remains waiting; late answer rejected')
else:
    state = wait(task, lambda r: r['done'])
    assert not state['waiting_requests']
    history = read_events(task)
    answers = [e for e in history if e['kind'] == 'answer']
    expected = {r['call_id'] for r in report['requests']} - {report.get('cancelled')}
    assert {e['text'] for e in answers} == expected and len(answers) == len(expected)
    report['answers'] = {e['text']: e['body']['answer'] for e in answers}
    for request in report['requests']:
        if request['call_id'] not in expected: continue
        assert any(e.get('method') == 'subagent/completed' and e['body'].get('task_id') == request['task_id'] and e['body'].get('state') == 'done' and report['answers'][request['call_id']] in e['body'].get('answer', '') for e in history)
    report['verified'] = True
    print('PASS: each answer returned to its own child result; no duplicate answers; final waiting set empty')
record.write_text(json.dumps(report, ensure_ascii=False, indent=2))
