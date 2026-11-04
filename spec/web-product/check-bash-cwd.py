"""真实模型验证 bash 的工作目录跨轮保留（spec/harness-basics.md 第 2 节）：沙箱与宿主机各跑一遍。
第一轮只让模型 cd 到子目录；第二轮让它不带 cd 跑 pwd。核对检查点 state.cwd、第二轮的 pwd 输出、
第二轮开头的环境信息里“bash 当前目录”。不修改日常数据（只认 tern-qa- 运行目录），结束时恢复 qa 的个人设置。"""
import argparse, http.cookiejar, json, subprocess, time, urllib.request, urllib.error, uuid, os
from pathlib import Path

QA_BASE = os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')
p = argparse.ArgumentParser(); p.add_argument('--home', required=True); a = p.parse_args()
home = Path(a.home).resolve(); assert home.name.startswith(('.tern-web-product-qa', 'tern-qa-'))
config = dict(x.split('=', 1) for x in (home / '.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), urllib.request.ProxyHandler({}))


def call(method, path, body=None):
    req = urllib.request.Request(QA_BASE + path, method=method, data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
    try:
        res = opener.open(req, timeout=60)
    except urllib.error.HTTPError as error:
        res = error
    data = res.read().decode()
    try:
        data = json.loads(data)
    except json.JSONDecodeError:
        pass
    return res.status, data


def events(task): return [json.loads(x) for x in (home / '.data/runs' / f'{task}.jsonl').read_text().splitlines()]


def wait(task, turns):
    """等这条对话结束到第 turns 轮（每轮一个 turn/completed）。"""
    end = time.monotonic() + 240
    while time.monotonic() < end:
        code, runs = call('GET', '/runs')
        row = next((r for r in runs if r['id'] == task), None) if code == 200 else None
        if row and row['done'] and len([e for e in events(task) if e.get('method') == 'turn/completed']) >= turns:
            return
        time.sleep(.5)
    raise AssertionError('先查看原任务 ' + task)


def checkpoint(task):
    turn = next(e['body']['turn_id'] for e in reversed(events(task)) if e.get('method') == 'turn/started')
    return json.loads((home / '.data/checkpoints' / f'{turn}.json').read_text())


def bash_calls(task, turn_index):
    """第 turn_index 轮（从 0 数）里的 bash 调用：(命令, 观测)。"""
    out, turn = [], -1
    for e in events(task):
        if e.get('method') == 'turn/started':
            turn += 1
        item = e.get('body', {}).get('item', {}) if e.get('method') == 'item/completed' else {}
        if turn == turn_index and item.get('type') == 'toolCall' and item.get('data', {}).get('tool') == 'bash':
            d = item['data']
            out.append((json.loads(d.get('arguments') or '{}').get('command', ''), d.get('observation', {})))
    return out


assert call('POST', '/login', {'name': config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa')), 'password': (config.get('TERN_QA_PASSWORD') or config['OBSERVER_TEST_PASSWORD']).strip('"\'')})[0] == 200
code, settings = call('GET', '/settings'); assert code == 200; original = settings.get('user') or {}
repo = home / 'cwd-fixtures' / uuid.uuid4().hex[:8] / 'cwd-repo'
(repo / 'sub' / 'deep').mkdir(parents=True)
(repo / 'sub' / 'deep' / 'marker.txt').write_text('CWD_MARKER\n')
subprocess.run(['git', 'init', '-q', str(repo)], check=True)
subprocess.run(['git', '-C', str(repo), 'add', '.'], check=True)
subprocess.run(['git', '-C', str(repo), '-c', 'user.name=QA', '-c', 'user.email=qa@example.invalid', 'commit', '-qm', 'cwd fixture'], check=True)
report = {'checks': []}; out = home / 'bash-cwd-results.json'
def save(): out.write_text(json.dumps(report, ensure_ascii=False, indent=2))
try:
    assert call('PUT', '/settings', {**original, 'bash': 'on'})[0] == 200
    code, project = call('POST', '/settings/workspaces', {'path': str(repo)}); assert code == 200, project
    real = os.path.realpath(repo)
    for permission, sandbox, expect in (('edit', 'sandbox', '/workspace/sub/deep'), ('full', 'host', real + '/sub/deep')):
        code, row = call('POST', '/runs', {'provider': 'deepseek', 'workspace': project['name'], 'permission': permission, 'sandbox': sandbox, 'request_id': str(uuid.uuid4()),
                                           'query': '验收：请调用一次 bash，命令只写 cd sub/deep（不要加任何别的命令），然后只回复 CD_DONE。不要读写文件、不要写长期记忆。'})
        assert code == 200, row; task = row['id']; report[sandbox] = {'task': task}; save()
        wait(task, 1)
        first = bash_calls(task, 0); assert first and first[0][1].get('status') == 'ok', first
        state = checkpoint(task)['state']
        assert state.get('cwd') == expect and bool(state.get('cwd_host')) == (sandbox == 'host'), state
        report['checks'].append(f'{sandbox}: cd 之后检查点 state.cwd = {expect}'); save()
        code, row = call('POST', f'/runs/{task}/messages', {'permission': permission, 'sandbox': sandbox, 'request_id': str(uuid.uuid4()),
                                                            'query': '验收：请调用一次 bash，命令只写 pwd（不要 cd、不要加别的命令），把输出原样告诉我。'})
        assert code == 200, row
        wait(task, 2)
        second = bash_calls(task, 1); assert second, '第二轮没有调用 bash'
        command, obs = second[0]
        assert 'cd' not in command.split(), command
        assert expect in json.dumps(obs, ensure_ascii=False), obs
        report['checks'].append(f'{sandbox}: 下一轮不带 cd 的 pwd 在 {expect}'); save()
        # 第二轮开头的环境信息告诉模型 bash 在哪：找这一轮第一次请求里的提醒。
        requests = [e for e in events(task) if e['kind'] == 'request']
        assert any('bash 当前目录' in json.dumps(r.get('body', {}), ensure_ascii=False) and expect in json.dumps(r.get('body', {}), ensure_ascii=False) for r in requests), '环境信息里没有 bash 当前目录'
        report['checks'].append(f'{sandbox}: 新一轮的环境信息带 bash 当前目录'); save()
    report['verified'] = True; save(); print(json.dumps(report, ensure_ascii=False, indent=2))
finally:
    assert call('PUT', '/settings', original)[0] == 200, 'QA 个人设置恢复失败，请先处理'
