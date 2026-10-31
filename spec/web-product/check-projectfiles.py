"""在专用 QA 项目验证只读文件接口；为真实浏览器准备文件链接。"""
import argparse
import base64
import hashlib
import http.cookiejar
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095

p = argparse.ArgumentParser(); p.add_argument('--home', required=True); p.add_argument('--links', action='store_true'); a = p.parse_args()
home = Path(a.home).resolve(); assert home.name.startswith(('.tern-web-product-qa','tern-qa-'))
fixture = json.loads((home / 'workpanel-results.json').read_text())
repo = Path(fixture['path']).resolve(); assert repo.is_relative_to(home / 'panel-fixtures')
task = fixture['task']
config = dict(x.split('=', 1) for x in (home / '.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method, path, body=None):
    req = urllib.request.Request(QA_BASE + path, method=method, data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
    try: res = opener.open(req, timeout=15)
    except urllib.error.HTTPError as error: res = error
    text = res.read().decode()
    try: data = json.loads(text)
    except json.JSONDecodeError: data = text
    return res.status, data
assert call('POST', '/login', {'name': config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa')), 'password': (config.get('TERN_QA_PASSWORD') or config['OBSERVER_TEST_PASSWORD']).strip('"\'')})[0] == 200
def read(path): return call('GET', f'/runs/{task}/workspace/file?' + urllib.parse.urlencode({'path': str(path)}))
def events(): return [json.loads(x) for x in (home / '.data/runs' / f'{task}.jsonl').read_bytes().split(b'\n')[:-1] if x]
before = len([e for e in events() if e['kind'] == 'request'])
folder = repo / 'preview-fixture'; folder.mkdir(exist_ok=True)
text = '\n'.join(f'中文预览第 {i:03} 行 <tag> & value' for i in range(1, 451)) + '\n'
file = folder / '中文 文件.txt'; file.write_text(text)
(folder / 'empty.txt').write_bytes(b'')
(folder / 'binary.dat').write_bytes(b'\x00\xff\x01')
(folder / 'large.txt').write_bytes(b'x' * (2 * 1024 * 1024 + 1))
(folder / 'unsafe.html').write_text('<script>window.TERN_PREVIEW_EXECUTED=true</script><h1>TEXT_ONLY_PREVIEW</h1>')
outside = home / 'preview-outside.txt'; outside.write_text('OUTSIDE_FIXTURE_ONLY')
for link, target in [('outside-link', outside), ('inside-link', file)]:
    path = folder / link
    if path.is_symlink(): path.unlink()
    path.symlink_to(target if link == 'outside-link' else file.name)
fifo = folder / 'pipe'
if not fifo.exists(): os.mkfifo(fifo)
checks = []
for path in ['preview-fixture/中文 文件.txt', file, './preview-fixture/中文 文件.txt', 'preview-fixture/inside-link']:
    code, data = read(path); assert code == 200, (path, code)
    assert base64.b64decode(data['data']) == text.encode()
    assert data['version'] == hashlib.sha256(text.encode()).hexdigest()
    assert data['directory'] == str(repo)
checks.append('relative/absolute/UTF-8/inside symlink use task snapshot and content hash')
for path in [outside, '../outside.txt', 'preview-fixture/outside-link', '/etc/passwd']:
    code, data = read(path); assert code in (403, 404), (str(path), code)
    assert 'OUTSIDE_FIXTURE_ONLY' not in str(data)
checks.append('outside path and symlink rejected')
assert read('preview-fixture/pipe')[0] == 400
assert read('preview-fixture')[0] == 400
assert read('preview-fixture/missing.txt')[0] == 404
checks.append('pipe/directory/missing return explicit failures without blocking')
code, data = read('preview-fixture/large.txt'); assert code == 200 and data['too_large'] and not data.get('data') and not data.get('version')
code, data = read('preview-fixture/empty.txt'); assert code == 200 and base64.b64decode(data['data']) == b''
code, data = read('preview-fixture/binary.dat'); assert code == 200 and base64.b64decode(data['data']) == b'\x00\xff\x01'
checks.append('large/empty/binary represented honestly')
old = read('preview-fixture/中文 文件.txt')[1]['version']; file.write_text(text + '新增版本行\n')
assert read('preview-fixture/中文 文件.txt')[1]['version'] != old
assert len([e for e in events() if e['kind'] == 'request']) == before
checks.append('version changes with content; reads create no model requests')
report = {'task': task, 'checks': checks, 'verified': True}
(home / 'projectfiles-results.json').write_text(json.dumps(report, ensure_ascii=False, indent=2))
print('PASS: project files', task)
if a.links:
    query = '仅回复以下三个 Markdown 链接，不调用工具、不改文件： [源码第24行](readme.txt:24) [中文文件第220行](<preview-fixture/中文 文件.txt#L220>) [HTML源码](preview-fixture/unsafe.html)。'
    code, data = call('POST', f'/runs/{task}/messages', {'query': query, 'request_id': str(uuid.uuid4())}); assert code == 200, data
    until = time.monotonic() + 120
    while time.monotonic() < until:
        rows = call('GET', '/runs')[1]; state = next(r for r in rows if r['id'] == task)
        if state['done']: break
        time.sleep(.3)
    else: raise AssertionError('链接任务仍运行，请观察原任务')
    report['links_requested'] = True
    (home / 'projectfiles-results.json').write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print('READY: real model file links')
