"""隔离真实服务：等待身份、并发答复及完成摘要。不使用脚本模型。"""
import argparse
import concurrent.futures
import http.cookiejar
import json
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--home', required=True)
args = parser.parse_args()
home = Path(args.home).resolve()
assert home.name.startswith('.tern-web-product-qa')
cfg = dict(line.split('=', 1) for line in (home / '.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
def client():
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def request(method, path, body=None):
        req = urllib.request.Request('http://127.0.0.1:8095' + path, method=method, data=json.dumps(body).encode() if body is not None else None, headers={'Content-Type': 'application/json'})
        try:
            res = opener.open(req, timeout=30)
        except urllib.error.HTTPError as error:
            res = error
        text = res.read().decode()
        try:
            data = json.loads(text)
        except json.JSONDecodeError:
            data = text
        return res.status, data
    assert request('POST', '/login', {'name': cfg.get('OBSERVER_TEST_USER', 'qa'), 'password': cfg['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0] == 200
    return request
call, other = client(), client()
def task(task_id):
    code, runs = call('GET', '/runs')
    assert code == 200
    return next(r for r in runs if r['id'] == task_id)
def wait(task_id, predicate):
    end = time.monotonic() + 120
    while time.monotonic() < end:
        state = task(task_id)
        if predicate(state): return state
        time.sleep(.5)
    raise AssertionError('真实模型未到预期状态: ' + task_id)
def events(task_id):
    code, stream = call('GET', f'/runs/{task_id}/events')
    assert code == 200
    return [v for v in (json.loads(line[6:]) for line in stream.splitlines() if line.startswith('data: ')) if 'kind' in v]
# 用户界面已真实回答的任务：验证不是只隐藏了按钮。
ui_task = json.loads((home / 'activity-test.json').read_text())['task']
state = wait(ui_task, lambda r: r['done'])
assert state['waiting_requests'] == [] and 'waiting' not in state
history = events(ui_task)
answers = [e for e in history if e['kind'] == 'answer']
assert len(answers) == 1 and answers[0]['body']['answer'] == '网页验收'
assert state['completion_seq'] == next(e['seq'] for e in reversed(history) if e['kind'] == 'exit')
assert state['activity_seq'] == history[-1]['seq']
code, created = call('POST', '/runs', {'provider': 'deepseek', 'workspace': '', 'request_id': str(uuid.uuid4()), 'query': '用 ask_user 问我“选择并发验收标记”，选项为“甲”和“乙”。等我回答后只回复 ACTIVITY_RACE_OK 和我选的标记，不调用其他工具。'})
assert code == 200, created
task_id = created['id']
state = wait(task_id, lambda r: r.get('waiting') == 'question')
assert not state['done'] and len(state['waiting_requests']) == 1
request = state['waiting_requests'][0]
assert request['id'].startswith('event:') and request['kind'] == 'question' and request['call_id']
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    jobs = [pool.submit(c, 'POST', f'/runs/{task_id}/answer', {'call_id': request['call_id'], 'answer': answer}) for c, answer in [(call, '甲'), (other, '乙')]]
    replies = [job.result() for job in jobs]
assert sorted(code for code, _ in replies) == [204, 409], replies
state = wait(task_id, lambda r: r['done'])
assert not state['waiting_requests'] and 'waiting' not in state
history = events(task_id)
assert len([e for e in history if e['kind'] == 'answer']) == 1
approval = next(e for e in history if e['kind'] == 'approval' and e['body'].get('call_id') == request['call_id'])
assert request['id'] == 'event:' + str(approval['seq'])
assert call('POST', f'/runs/{task_id}/answer', {'call_id': request['call_id'], 'answer': '再答一次'})[0] == 409
(home / 'activity-results.json').write_text(json.dumps({'ui_task': ui_task, 'race_task': task_id, 'request': request, 'answer_statuses': [code for code, _ in replies], 'passed': True}, ensure_ascii=False, indent=2))
print('PASS: real UI answer persisted; stable waiting identity; concurrent answers exactly one accepted; completed/expired requests reject further answers')
