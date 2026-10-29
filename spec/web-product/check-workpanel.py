"""准备真实模型生成的工作面板验收资源；浏览器操作另留截图和回执。"""
import argparse
import http.cookiejar
import json
import subprocess
import time
import urllib.request
import urllib.error
import uuid
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095

p = argparse.ArgumentParser()
p.add_argument('--home', required=True)
a = p.parse_args()
home = Path(a.home).resolve()
assert home.name.startswith(('.tern-web-product-qa','tern-qa-'))
config = dict(line.split('=', 1) for line in (home / '.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
deadline = time.monotonic() + 15
while True:
    try:
        with urllib.request.urlopen(QA_BASE+'/login', timeout=2) as response:
            assert response.status == 200
        break
    except urllib.error.URLError:
        if time.monotonic() >= deadline:
            raise
        time.sleep(.2)

def call(method, path, body=None):
    req = urllib.request.Request(QA_BASE + path, method=method,
                                data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
    with opener.open(req, timeout=30) as res:
        data = res.read().decode()
        return json.loads(data) if data else None

call('POST', '/login', {'name': config.get('OBSERVER_TEST_USER', 'qa'), 'password': config['OBSERVER_TEST_PASSWORD'].strip('"\'')})
repo = home / 'panel-fixtures' / str(uuid.uuid4()) / 'panel-repo'
repo.mkdir(parents=True)
subprocess.run(['git', 'init', '-q', str(repo)], check=True)
(repo / 'readme.txt').write_text('\n'.join(f'BASE_{i:03}' for i in range(1, 241)) + '\n')
subprocess.run(['git', '-C', str(repo), 'add', 'readme.txt'], check=True)
subprocess.run(['git', '-C', str(repo), '-c', 'user.name=QA', '-c', 'user.email=qa@example.invalid', 'commit', '-qm', 'QA panel baseline'], check=True)
(repo / 'readme.txt').write_text('\n'.join(f'CHANGED_{i:03}' for i in range(1, 241)) + '\n')
project = call('POST', '/settings/workspaces', {'path': str(repo)})
task = call('POST', '/runs', {'provider': 'deepseek', 'workspace': project['name'], 'request_id': str(uuid.uuid4()),
                            'query': '工作面板验收：请使用 bash，在本任务的临时目录（按实际工具说明定位，不是项目目录）创建 panel-a.txt 和 panel-b.txt 两个文件，各 180 行，内容分别为 PANEL_A_001 到 PANEL_A_180、PANEL_B_001 到 PANEL_B_180。不要修改项目目录。实际检查两个文件的行数，成功后只回复 PANEL_FILES_READY。'})
report = {'task': task['id'], 'project': project['name'], 'path': str(repo)}
report_file = home / 'workpanel-results.json'
report_file.write_text(json.dumps(report, indent=2))
deadline = time.monotonic() + 180
while True:
    state = next(t for t in call('GET', '/runs') if t['id'] == task['id'])
    if state['done']:
        break
    assert time.monotonic() < deadline, '实际任务尚未结束，请检查原任务而非重复创建'
    time.sleep(.5)
files = call('GET', f'/runs/{task["id"]}/files')['files']
assert {'panel-a.txt', 'panel-b.txt'} <= {f['path'] for f in files}, files
for name, prefix in [('panel-a.txt', 'PANEL_A'), ('panel-b.txt', 'PANEL_B')]:
    with opener.open(f'{QA_BASE}/runs/{task["id"]}/files/{name}') as res:
        text = res.read().decode()
    assert len(text.splitlines()) == 180 and prefix + '_001' in text and prefix + '_180' in text
report['real_files_verified'] = True
report_file.write_text(json.dumps(report, indent=2))
print('READY work panel:', task['id'])
