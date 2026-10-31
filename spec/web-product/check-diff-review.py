"""真实 Git 与 diff API 的独立 QA 验收，不启动/重启服务。

在新 binary 就绪后运行：
  python3 spec/web-product/check-diff-review.py --home /home/dev/.tern-web-product-qa --port 8095

只在 QA home 的独立 fixture 里改 Git，并注册一个真实只读模型任务。
先验证 unborn，再在同一路径提交与分支；不伪造任务存档、模型或检查点。
报告、原始 API 响应和 Git 对照结果保留在 fixture 目录，便于继续 UI 验收。
"""

import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import shlex
import stat
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


FILE_LIMIT = 500
FILE_BYTES = 256 << 10
TOTAL_BYTES = 1 << 20


def digest(data):
    return hashlib.sha256(data).hexdigest()


def patch_body(text):
    lines = text.splitlines(keepends=True)
    return ''.join(lines[1:] if lines and lines[0].startswith('diff --git ') else lines)


def additions(text):
    return [line[1:] for line in text.splitlines() if line.startswith('+') and not line.startswith('+++')]


def recreated_patch(text):
    # no-index 对照的临时文件名不同，只比较实际模式变化与 hunk，保留全部代码行。
    lines = patch_body(text).splitlines(keepends=True)
    at = next((i for i, line in enumerate(lines) if line.startswith('@@ ')), len(lines))
    modes = [line for line in lines[:at] if line.startswith(('old mode ', 'new mode '))]
    return ''.join(modes + lines[at:])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--home', type=Path, required=True)
    parser.add_argument('--port', type=int, default=8095)
    parser.add_argument('--provider', default='deepseek')
    parser.add_argument('--timeout', type=int, default=180)
    parser.add_argument('--report', type=Path)
    parser.add_argument('--resume', type=Path, help='沿既有报告/任务和保存的阶段继续，不创建新任务')
    args = parser.parse_args()
    home = args.home.resolve()
    assert home.name.startswith(('.tern-web-product-qa','tern-qa-')), '只允许专用 QA home'
    assert args.port not in (8090, 8094), '不得访问日常或 OAuth 验收端口'
    assert 1 <= args.port <= 65535
    config = dict(line.split('=', 1) for line in (home / '.env').read_text().splitlines()
                  if '=' in line and not line.startswith('#'))
    account = config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa')).strip('\"\'')
    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    url = f'http://127.0.0.1:{args.port}'

    def call(method, path, body=None):
        request = urllib.request.Request(url + path, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Content-Type': 'application/json'})
        try:
            response = opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        text = response.read().decode()
        try:
            value = json.loads(text)
        except json.JSONDecodeError:
            value = text
        return response.status, value

    assert call('GET', '/login')[0] == 200, '请先由主任务确认 QA 服务与新 binary 就绪'
    assert call('POST', '/login', {'name': account,
        'password': (config.get('TERN_QA_PASSWORD') or config['OBSERVER_TEST_PASSWORD']).strip('\"\'')})[0] == 200, 'QA 登录失败'
    if args.resume:
        report_path = args.resume.resolve()
        report = json.loads(report_path.read_text())
        repo, evidence = Path(report['fixture']), Path(report['evidence'])
        root, stamp = repo.parent, repo.parent.name
        assert root.parent == home / 'diff-review-fixtures' and report['home'] == str(home)
        assert report['port'] == args.port and report['task']
        if report.get('error'):
            report.setdefault('previous_errors', []).append(report.pop('error'))
        report['checks'] = list({check['name']: check for check in report['checks']}.values())
        report['status'] = 'running'
    else:
        stamp = uuid.uuid4().hex[:12]
        root = home / 'diff-review-fixtures' / stamp
        repo, evidence = root / ('diff-review-' + stamp), root / 'evidence'
        repo.mkdir(parents=True, mode=0o700)
        root.chmod(0o700)
        evidence.mkdir(mode=0o700)
        report_path = (args.report or root / 'report.json').resolve()
        report = {'status': 'running', 'fixture': str(repo), 'evidence': str(evidence),
                  'home': str(home), 'port': args.port, 'checks': [], 'model_tasks_created': 0,
                  'next_stage': 'initial_setup'}
    git_env = {**os.environ, 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': os.devnull,
               'GIT_OPTIONAL_LOCKS': '0'}

    def save():
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')

    def stage(name):
        report['next_stage'] = name
        save()

    def git(*arguments, stdin=None, allowed=(0,), directory=None):
        result = subprocess.run(['git', '--literal-pathspecs', *arguments], cwd=directory or repo,
            env=git_env, input=stdin, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=40)
        assert result.returncode in allowed, 'Git fixture 命令失败：' + ' '.join(arguments[:3])
        return result.stdout

    def oid(ref):
        return git('rev-parse', '--verify', ref).decode().strip()

    def write(path, content):
        file = repo / path
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_bytes(content if isinstance(content, bytes) else content.encode())

    def commit(message):
        git('add', '-A', '--', '.')
        git('commit', '-q', '-m', message)
        return oid('HEAD')

    def snapshot():
        files = []
        for directory, dirs, names in os.walk(repo, followlinks=False):
            dirs[:] = sorted(name for name in dirs if name != '.git')
            for name in sorted(names):
                path = Path(directory) / name
                info = path.lstat()
                if stat.S_ISLNK(info.st_mode):
                    value = 'link:' + os.readlink(path)
                elif stat.S_ISREG(info.st_mode):
                    value = digest(path.read_bytes())
                else:
                    value = 'special:' + str(stat.S_IFMT(info.st_mode))
                files.append((str(path.relative_to(repo)), stat.S_IMODE(info.st_mode), value))
        index = repo / '.git/index'
        return {'files': sorted(files), 'index': digest(index.read_bytes()) if index.exists() else '',
                'objects': sorted((str(path.relative_to(repo / '.git/objects')), digest(path.read_bytes()))
                                  for path in (repo / '.git/objects').rglob('*') if path.is_file()),
                'head': (repo / '.git/HEAD').read_text(),
                'refs': git('show-ref', allowed=(0, 1)).decode(),
                'status': git('status', '--porcelain=v1', '-z', '--untracked-files=all').decode()}

    task = report.get('task')

    def run_events():
        path = home / '.data/runs' / f'{task}.jsonl'
        return [json.loads(line) for line in path.read_bytes().split(b'\n')[:-1] if line]

    def current():
        code, rows = call('GET', '/runs')
        assert code == 200
        return next(row for row in rows if row['id'] == task)

    def wait_done():
        until = time.monotonic() + args.timeout
        while time.monotonic() < until:
            if current()['done']:
                return
            time.sleep(.3)
        raise AssertionError('真实只读任务仍未结束；保留原任务，不重发或重启服务')

    def reference(scope, base=None):
        head = git('rev-parse', '--verify', 'HEAD', allowed=(0, 128)).decode().strip()
        empty = git('hash-object', '-t', 'tree', '--stdin', stdin=b'').decode().strip()
        branch = git('symbolic-ref', '--quiet', '--short', 'HEAD', allowed=(0, 1)).decode().strip()
        tip = ''
        if scope == 'branch':
            tip = oid(base + '^{commit}')
            left = git('merge-base', tip, head).decode().strip()
            ranges = [left, head]
            base_ref, target, target_oid = base, 'HEAD', head
        elif scope == 'unstaged':
            left, ranges, base_ref, target, target_oid = '', [], 'INDEX', 'worktree', ''
        else:
            left = head or empty
            ranges = (['--cached'] if scope == 'staged' else []) + [left]
            base_ref, target, target_oid = ('HEAD' if head else 'EMPTY_TREE'), ('index' if scope == 'staged' else 'worktree'), ''
        flags = ['diff', '--no-color', '--no-ext-diff', '--no-textconv', '--find-renames', *ranges]
        names = git(*flags, '--name-status', '-z').decode().split('\0')
        tracked = {}
        at = 0
        while at < len(names) and names[at]:
            status_code, path = names[at], names[at + 1]
            at += 2
            old_path = ''
            if status_code[0] in ('R', 'C'):
                old_path, path = path, names[at]
                at += 1
            tracked[path] = {'path': path, 'status': status_code[0], 'old_path': old_path}
        stats = git(*flags, '--numstat', '-z').decode().split('\0')
        at = 0
        while at < len(stats) and stats[at]:
            plus, minus, path = stats[at].split('\t', 2)
            at += 1
            if not path:
                path = stats[at + 1]
                at += 2
            tracked[path].update({'binary': plus == '-', 'added': 0 if plus == '-' else int(plus),
                                  'deleted': 0 if minus == '-' else int(minus)})
        untracked = []
        if scope in ('workspace', 'unstaged'):
            untracked = [path for path in git('ls-files', '--others', '--exclude-standard', '-z').decode().split('\0') if path]
        # 暂存删除后重建的文件，git diff HEAD 仍只见 index 的删除；工作区范围要直接比较
        # 基准 blob 和当前普通文件，不能把该删除与 ?? 拼成两项。对照使用真实 no-index diff。
        if scope == 'workspace':
            for path in list(untracked):
                if path not in tracked or tracked[path]['status'] != 'D':
                    continue
                info = (repo / path).lstat()
                if not stat.S_ISREG(info.st_mode):
                    continue
                tree = git('ls-tree', '-z', left, '--', path).split(b'\t', 1)[0].split()
                assert tree and tree[1] == b'blob'
                attributes = git('check-attr', '-z', 'filter', '--', path).decode().split('\0')
                if attributes[2] not in ('unspecified', 'unset'):
                    tracked[path]['unverifiable_filter'] = True
                    untracked.remove(path)
                    continue
                normalized = git('hash-object', '--path=' + path, '--stdin',
                                 stdin=(repo / path).read_bytes()).decode().strip()
                trust_mode = git('config', '--bool', '--get', 'core.filemode', allowed=(0, 1)).decode().strip() != 'false'
                same_mode = not trust_mode or (tree[0] == b'100755') == bool(info.st_mode & stat.S_IXUSR)
                if normalized == tree[2].decode() and same_mode:
                    del tracked[path]
                    untracked.remove(path)
                    continue
                baseline = root / 'oracle-blobs' / (digest(path.encode()) + '.left')
                baseline.parent.mkdir(exist_ok=True)
                baseline.write_bytes(git('cat-file', 'blob', tree[2].decode()))
                baseline.chmod(0o755 if tree[0] == b'100755' else 0o644)
                patch = git('diff', '--no-index', '--no-color', '--no-ext-diff', '--no-textconv',
                            '--', str(baseline), str(repo / path), allowed=(0, 1)).decode()
                untracked.remove(path)
                if not patch:
                    del tracked[path]
                    continue
                counts = git('diff', '--no-index', '--no-ext-diff', '--no-textconv', '--numstat',
                             '--', str(baseline), str(repo / path), allowed=(0, 1)).decode().split('\t', 2)
                tracked[path] = {'path': path, 'status': 'M', 'old_path': '',
                    'binary': counts[0] == '-', 'added': 0 if counts[0] == '-' else int(counts[0]),
                    'deleted': 0 if counts[1] == '-' else int(counts[1]),
                    'recreated_patch': recreated_patch(patch)}
        conflicts = sorted({record.split('\t', 1)[1] for record in
                            git('ls-files', '--unmerged', '-z').decode().split('\0') if record})
        return {'scope': scope, 'base_ref': base_ref, 'base_oid': left,
                'base_tip_oid': tip, 'target': target, 'target_oid': target_oid,
                'head_oid': head, 'branch': branch, 'tracked': tracked,
                'untracked': untracked, 'conflicts': conflicts, 'flags': flags}

    outside_marker = 'OUTSIDE_DIFF_FIXTURE_' + stamp
    external_marker = root / 'external-diff-ran'

    def inspect(label, scope='workspace', base=None, default=False, limited=False, conflict=False):
        expected = reference(scope, base)
        assert not external_marker.exists(), 'fixture/Git 对照已触发探针，尚未归因到 API'
        before = snapshot()
        assert not external_marker.exists(), 'Git 状态读取触发探针，尚未归因到 API'
        query = {} if default else {'scope': scope}
        if base is not None:
            query['base'] = base
        path = f'/runs/{task}/diff' + ('?' + urllib.parse.urlencode(query) if query else '')
        code, actual = call('GET', path)
        report['active_check'] = {'name': label, 'scope': scope, 'http_status': code}
        (evidence / (label + '-api.json')).write_text(json.dumps(actual, ensure_ascii=False, indent=2))
        (evidence / (label + '-git.json')).write_text(json.dumps(expected, ensure_ascii=False, indent=2))
        assert code == 200 and isinstance(actual, dict), 'diff API 没有返回成功对象：' + label
        assert snapshot() == before, '只读 diff 查询改变了 Git/工作目录：' + label
        assert not external_marker.exists(), '查询运行了 external diff/textconv'
        for key in ('scope', 'base_ref', 'base_oid', 'target', 'target_oid', 'head_oid', 'branch'):
            assert actual.get(key) == expected[key], f'{label} 的 {key} 与 Git 不同'
        if scope == 'branch':
            assert actual.get('base_tip_oid') == expected['base_tip_oid']
        assert str(Path(actual['dir']).resolve()) == str(repo)
        assert isinstance(actual.get('head'), str)
        assert len(actual.get('view_version', '')) == 64
        files = actual['files']
        paths = [file['path'] for file in files]
        wanted = set(expected['tracked']) | set(expected['untracked'])
        assert len(paths) == len(set(paths)), '响应有重复文件路径'
        assert set(paths) <= wanted, '响应混入比较范围以外的文件'
        assert actual.get('total_files') == len(wanted)
        assert actual.get('file_limit') == FILE_LIMIT
        if len(wanted) <= FILE_LIMIT:
            assert set(paths) == wanted, f'{label} 缺少 Git 已报告的文件'
        else:
            assert len(paths) == FILE_LIMIT and actual['truncated']
        assert outside_marker not in json.dumps(actual, ensure_ascii=False), '读取了链接外部目标正文'
        for file in files:
            path = file['path']
            assert isinstance(file.get('binary'), bool)
            assert isinstance(file.get('truncated'), bool)
            assert isinstance(file.get('notice'), str)
            assert isinstance(file.get('old_path'), str)
            assert len(file.get('version', '')) == 64
            is_conflicted = scope != 'branch' and path in expected['conflicts']
            assert bool(file.get('conflicted')) == is_conflicted, f'{label}/{path} 的冲突标识与真实 index stages 不同'
            if file['truncated']:
                assert actual['truncated'] and file['notice'] and not file['diff'], '截断必须有明确说明且不能返回半份 patch'
            if path in expected['tracked']:
                wanted_file = expected['tracked'][path]
                if wanted_file.get('unverifiable_filter'):
                    assert file['notice'] and not file['diff'], '自定义 filter 的重建文件没有明确无法核对说明'
                    continue
                # unmerged 的 staged/unstaged Git 输出可同时含 U 与 M；冲突事实以真实
                # index stages 对照，不把混合 patch 当作已合并的普通文本强行匹配。
                if conflict and is_conflicted:
                    continue
                for key in ('status', 'old_path', 'binary', 'added', 'deleted'):
                    assert file[key] == wanted_file[key], f'{label}/{path} 的 {key} 与 Git 不同'
                if not limited and not file['truncated'] and not file['binary']:
                    if 'recreated_patch' in wanted_file:
                        assert recreated_patch(file['diff']) == wanted_file['recreated_patch'], f'{label}/{path} 重建文件与真实 no-index diff 不同'
                    else:
                        pathspecs = [wanted_file['old_path'], path] if wanted_file['old_path'] else [path]
                        patch = git(*expected['flags'], '--', *pathspecs).decode()
                        assert patch_body(file['diff']) == patch_body(patch), f'{label}/{path} patch 与真实 Git 不同'
            else:
                assert file['status'] == '??' and not file['old_path']
                info = (repo / path).lstat()
                if not stat.S_ISREG(info.st_mode):
                    assert file['notice'] and not file['diff'], '未跟踪链接/特殊文件不能跟随或阻塞读取'
                elif not limited and not file['truncated']:
                    raw = (repo / path).read_bytes()
                    binary = b'\0' in raw
                    assert file['binary'] == binary
                    if not binary:
                        # 未跟踪预览展示原始文件；在仓库外跑 no-index，避免对照自身触发项目 clean filter。
                        patch = git('diff', '--no-index', '--no-color', '--no-ext-diff', '--no-textconv',
                                    '--', os.devnull, str(repo / path), allowed=(0, 1), directory=root).decode()
                        assert additions(file['diff']) == additions(patch), f'{label}/{path} 新文件正文不同'
                        assert file['added'] == len(raw.splitlines()) and file['deleted'] == 0
        assert sum(len(file['diff'].encode()) for file in files) <= TOTAL_BYTES
        assert not external_marker.exists(), '逐文件 Git 对照触发了探针，不能算 API 通过或失败'
        report['checks'] = [check for check in report['checks'] if check['name'] != label]
        report['checks'].append({'name': label, 'scope': scope, 'files': len(files),
                                 'total_files': len(wanted), 'truncated': actual['truncated'],
                                 'view_version': actual['view_version'], 'read_only': True})
        save()
        print('PASS', label, flush=True)
        return actual

    def rejected(label, parameters, codes=(400,)):
        before = snapshot()
        code, body = call('GET', f'/runs/{task}/diff?' + urllib.parse.urlencode(parameters))
        assert code in codes and body, '非法比较范围/基准没有明确报错：' + label
        assert snapshot() == before and not external_marker.exists()
        report['checks'] = [check for check in report['checks'] if check['name'] != label]
        report['checks'].append({'name': label, 'status': code, 'read_only': True})
        save()

    phase = report.get('next_stage', 'combined') if args.resume else 'initial'
    assert phase in ('initial', 'combined', 'branch', 'conflict', 'single-limit', 'total-limit', 'count-limit'), 'fixture 构造尚未完成，请先检查现状，不能盲重做'
    if args.resume:
        assert current()['done'], '原任务仍在运行，等待原任务，不创建或接着执行 fixture'
        assert run_events()[-1]['kind'] == 'exit' and run_events()[-1]['text'] == 'exit 0'
    model_event_count = report.get('model_event_count', len(run_events()) if task else 0)
    attributes = 'external-probe.txt diff=review_probe\n"recreated crlf.txt" text eol=crlf\n'
    save()
    try:
        if phase == 'initial':
            git('init', '-q', '-b', 'main')
            for key, value in {'user.name': 'Tern diff QA', 'user.email': 'qa@example.invalid',
                               'commit.gpgSign': 'false', 'core.autocrlf': 'false',
                               'core.quotePath': 'false', 'core.hooksPath': str(root / 'empty-hooks'),
                               'diff.renames': 'true', 'diff.algorithm': 'myers',
                               'diff.mnemonicPrefix': 'false', 'diff.noprefix': 'false'}.items():
                git('config', key, value)
            (root / 'empty-hooks').mkdir()
            write('README.md', 'Diff review fixture; only this temporary repository is modified.\n')
            write('both.txt', 'unborn staged\n')
            git('add', '--', 'README.md', 'both.txt')
            write('both.txt', 'unborn worktree\n')
            write('unborn new.txt', 'unborn untracked\n')
            code, project = call('POST', '/settings/workspaces', {'path': str(repo)})
            assert code == 200
            report.update({'workspace_id': project['id'], 'workspace_name': project['name']})
            before = snapshot()
            request_id = str(uuid.uuid4())
            code, created = call('POST', '/runs', {'workspace': project['name'], 'provider': args.provider,
                'permission': 'plan', 'request_id': request_id,
                'query': '这是 diff API 的只读验收任务，请简短确认工作区已经就绪。不要调用工具，不要修改任何文件。'})
            assert code == 200, '真实任务创建失败；不重发创建请求'
            task = created['id']
            report.update({'task': task, 'model_tasks_created': 1, 'task_request_id': request_id})
            save()
            wait_done()
            assert snapshot() == before, '只读模型任务改变了 fixture'
            events = run_events()
            assert sum(event['kind'] == 'start' for event in events) == 1
            assert not any(event['kind'] == 'continue' for event in events)
            assert any(event['kind'] == 'request' and not event.get('sub') for event in events)
            assert events[-1]['kind'] == 'exit' and events[-1]['text'] == 'exit 0'
            assert not any(choice.get('message', {}).get('tool_calls') for event in events
                           if event['kind'] == 'response' for choice in event.get('body', {}).get('choices', []))
            model_event_count = len(events)
            report['model_event_count'] = model_event_count
            report['model_requests'] = sum(event['kind'] == 'request' and not event.get('sub') for event in events)

            for scope in ('workspace', 'staged', 'unstaged'):
                inspect('unborn-' + scope, scope)
            rejected('unborn-branch', {'scope': 'branch', 'base': 'HEAD'}, (400, 409))
            commit('Initial fixture')
            for path in ('staged.txt', 'unstaged.txt', 'rename old.txt', '中文 空格.txt',
                         'literal[1].txt', 'literal1.txt', ':(glob)victim*', 'victim-one.txt',
                         'external-probe.txt', 'tracked-big.txt', 'delete staged.txt', 'delete unstaged.txt',
                         'recreated changed.txt', 'recreated identical.txt', 'recreated executable.sh',
                         'recreated mode-change.sh', 'recreated others.sh', 'recreated filter.txt',
                         'rename reused old.txt', 'conflict.txt'):
                write(path, 'base ' + path + '\n')
            (repo / 'recreated executable.sh').chmod(0o755)
            (repo / 'recreated mode-change.sh').chmod(0o755)
            write('binary.dat', b'base\0binary\n')
            (root / 'outside-secret.txt').write_text(outside_marker)
            (root / 'outside-second.txt').write_text(outside_marker + '_SECOND')
            (repo / 'tracked-link').symlink_to(root / 'outside-secret.txt')
            attributes = 'external-probe.txt diff=review_probe\n"recreated crlf.txt" text eol=crlf\n'
            write('.gitattributes', attributes)
            write('recreated crlf.txt', b'CRLF baseline\n')
            commit('Baseline file kinds')
            hook = root / 'external-probe.sh'
            hook.write_text('#!/bin/sh\nprintf ran > ' + shlex.quote(str(external_marker)) + '\nexit 71\n')
            hook.chmod(0o700)
            git('config', 'diff.external', str(hook))
            git('config', 'diff.review_probe.textconv', str(hook))
            write('staged.txt', 'staged new\n')
            write('both.txt', 'both staged\n')
            git('mv', '--', 'rename old.txt', '重命名 文件.txt')
            git('mv', '--', 'rename reused old.txt', 'rename reused new.txt')
            write('rename reused old.txt', 'RENAME_REPLACEMENT_OLD_ONLY\n')
            git('add', '--', 'rename reused old.txt')
            git('rm', '-q', '--', 'delete staged.txt')
            (repo / 'delete unstaged.txt').unlink()
            for path in ('recreated changed.txt', 'recreated identical.txt', 'recreated executable.sh',
                         'recreated mode-change.sh', 'recreated others.sh', 'recreated filter.txt'):
                git('rm', '-q', '--', path)
                write(path, ('changed ' if path == 'recreated changed.txt' else 'base ') + path + '\n')
            (repo / 'recreated executable.sh').chmod(0o755)
            (repo / 'recreated others.sh').chmod(0o641)
            git('rm', '-q', '--', 'recreated crlf.txt')
            write('recreated crlf.txt', b'CRLF baseline\r\n')
            git('add', '--', 'staged.txt', 'both.txt')
            write('both.txt', 'both worktree\n')
            write('unstaged.txt', 'unstaged new\n')
            for path in ('中文 空格.txt', 'literal[1].txt', 'literal1.txt', ':(glob)victim*',
                         'victim-one.txt', 'external-probe.txt'):
                write(path, 'changed only ' + path + '\n')
            write('binary.dat', b'changed\0binary\n')
            write('未跟踪 文件.txt', 'untracked UTF8\nsecond line\n')
            write('only-newline.txt', '\n')
            write('untracked-binary.bin', b'untracked\0binary\n')
            (repo / 'tracked-link').unlink()
            (repo / 'tracked-link').symlink_to(root / 'outside-second.txt')
            (repo / 'untracked-link').symlink_to(root / 'outside-secret.txt')
            os.mkfifo(repo / 'untracked-fifo')
            git('config', 'filter.review_guard.clean', str(hook))
            write('.gitattributes', attributes + '"recreated filter.txt" filter=review_guard\n')
            stage('combined')
        if phase in ('initial', 'combined'):
            git('config', 'core.filemode', 'true')
            write('未跟踪 文件.txt', 'untracked UTF8\nsecond line\n')
            default = inspect('default-workspace', default=True)
            workspace = inspect('combined-workspace')
            assert default['view_version'] == workspace['view_version']
            displayed = {file['path']: file for file in workspace['files']}
            assert 'recreated identical.txt' not in displayed and 'recreated executable.sh' not in displayed
            assert 'recreated crlf.txt' not in displayed and 'recreated others.sh' not in displayed
            assert displayed['recreated changed.txt']['status'] == 'M'
            assert displayed['recreated mode-change.sh']['status'] == 'M'
            assert displayed['recreated mode-change.sh']['added'] == displayed['recreated mode-change.sh']['deleted'] == 0
            assert displayed['only-newline.txt']['added'] == 1
            assert displayed['recreated filter.txt']['notice'] and not displayed['recreated filter.txt']['diff']
            assert 'RENAME_REPLACEMENT_OLD_ONLY' not in displayed['rename reused new.txt']['diff']
            for scope in ('staged', 'unstaged'):
                inspect('combined-' + scope, scope)
            git('config', 'core.filemode', 'false')
            no_mode = inspect('ignore-executable-bit')
            assert 'recreated mode-change.sh' not in {file['path'] for file in no_mode['files']}
            git('config', 'core.filemode', 'true')
            again = inspect('unchanged-workspace')
            assert again['view_version'] == workspace['view_version']
            old = next(file for file in again['files'] if file['path'] == '未跟踪 文件.txt')['version']
            write('未跟踪 文件.txt', 'untracked UTF8\nsecond line\nvisible new line\n')
            changed = inspect('changed-workspace')
            assert changed['view_version'] != again['view_version']
            assert next(file for file in changed['files'] if file['path'] == '未跟踪 文件.txt')['version'] != old

            stage('branch_setup')
            (repo / 'untracked-fifo').unlink()
            write('.gitattributes', attributes)
            git('config', '--unset', 'filter.review_guard.clean')
            common = commit('Committed scope and path fixtures')
            git('branch', 'review-feature')
            git('switch', '-q', '-c', 'review-base')
            write('base-only.txt', 'only on base tip\n')
            base_tip = commit('Base advances separately')
            git('switch', '-q', 'review-feature')
            write('branch-only.txt', 'committed on feature\n')
            target = commit('Feature commit')
            git('switch', '-q', '--orphan', 'review-unrelated')
            write('orphan.txt', 'unrelated root\n')
            commit('Unrelated history')
            git('switch', '-q', 'review-feature')
            write('branch-only.txt', 'dirty staged feature\n')
            git('add', '--', 'branch-only.txt')
            write('branch-only.txt', 'dirty unstaged feature\n')
            write('branch-untracked.txt', 'not part of branch comparison\n')
            report['branch_oids'] = {'common': common, 'base_tip': base_tip, 'target': target}
            stage('branch')
        if phase in ('initial', 'combined', 'branch'):
            common, base_tip, target = (report['branch_oids'][key] for key in ('common', 'base_tip', 'target'))
            branch = inspect('branch-merge-base', 'branch', 'review-base')
            assert branch['base_oid'] == common and branch['base_tip_oid'] == base_tip
            assert branch['base_oid'] != branch['base_tip_oid'] and branch['target_oid'] == target
            assert [file['path'] for file in branch['files']] == ['branch-only.txt']
            rejected('invalid-scope', {'scope': 'recent-turn'})
            rejected('missing-base', {'scope': 'branch'})
            rejected('unknown-base', {'scope': 'branch', 'base': 'refs/heads/not-present'}, (400, 409))
            rejected('option-like-base', {'scope': 'branch', 'base': '--help'}, (400, 409))
            rejected('unrelated-base', {'scope': 'branch', 'base': 'review-unrelated'}, (400, 409))
            git('switch', '-q', '--detach', 'HEAD')
            detached = inspect('branch-detached', 'branch', 'review-base')
            assert detached['branch'] == ''
            git('switch', '-q', 'review-feature')

            stage('conflict_setup')
            conflict_base = commit('Keep local fixture changes before conflict checks')
            git('switch', '-q', '-c', 'review-conflict-left')
            write('conflict.txt', 'left side\n')
            commit('Left conflict change')
            git('switch', '-q', '-c', 'review-conflict-right', conflict_base)
            write('conflict.txt', 'right side\n')
            commit('Right conflict change')
            git('merge', '--no-edit', 'review-conflict-left', allowed=(1,))
            assert git('ls-files', '--unmerged', '-z'), '没有产生真实 Git 合并冲突'
            report['conflict_base'] = conflict_base
            stage('conflict')
        if phase in ('initial', 'combined', 'branch', 'conflict'):
            for scope in ('workspace', 'staged', 'unstaged'):
                result = inspect('conflicted-' + scope, scope, conflict=True)
                assert next(file for file in result['files'] if file['path'] == 'conflict.txt')['conflicted'] is True
            committed_conflict = inspect('branch-ignores-unmerged-index', 'branch', report['conflict_base'])
            assert not any(file.get('conflicted') for file in committed_conflict['files'])
            git('merge', '--abort')
            git('switch', '-q', 'review-feature')

            stage('single-limit')
        if phase in ('initial', 'combined', 'branch', 'conflict', 'single-limit'):
            backup = root / 'tracked-big-before.bin'
            if not backup.exists():
                backup.write_bytes((repo / 'tracked-big.txt').read_bytes())
            old_big = backup.read_bytes()
            write('tracked-big.txt', 'x' * (FILE_BYTES + 1024) + '\n')
            write('large-untracked.txt', 'y' * (FILE_BYTES + 1024) + '\n')
            large = inspect('single-file-truncation', limited=True)
            for path in ('tracked-big.txt', 'large-untracked.txt'):
                row = next(file for file in large['files'] if file['path'] == path)
                assert row['truncated'] and row['notice'] and not row['diff']
            write('tracked-big.txt', old_big)
            (repo / 'large-untracked.txt').unlink()
            stage('total-limit')
        if phase in ('initial', 'combined', 'branch', 'conflict', 'single-limit', 'total-limit'):
            for index in range(12):
                write(f'volume/{index:02}.txt', ('abcdefghij' * 40 + '\n') * 320)
            total = inspect('total-body-truncation', limited=True)
            assert total['truncated'] and any(file['truncated'] and file['notice'] for file in total['files'])
            for path in (repo / 'volume').iterdir():
                path.unlink()
            (repo / 'volume').rmdir()
            stage('count-limit')
        for index in range(FILE_LIMIT + 1):
            write(f'file-count/{index:04}.txt', 'count fixture\n')
        count = inspect('file-count-truncation', limited=True)
        assert count['total_files'] > count['file_limit'] and len(count['files']) == FILE_LIMIT
        for path in (repo / 'file-count').iterdir():
            path.unlink()
        (repo / 'file-count').rmdir()
        assert len(run_events()) == model_event_count, 'diff 查询引发了额外任务/模型事件'
        assert not external_marker.exists()
        report.update({'status': 'passed', 'next_stage': 'complete', 'ui_branch_base': 'review-base',
                       'query_did_not_start_models': True, 'service_restarted': False,
                       'fixture_retained_for_ui': True})
    except Exception as error:
        report.update({'status': 'failed', 'error': type(error).__name__ + ': ' + str(error)})
        raise
    finally:
        save()
    print('PASS diff API review:', report_path)


if __name__ == '__main__':
    main()
