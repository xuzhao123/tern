"""独立真实服务回归：活跃队列接管、补充转交规则与重启去重。

例：python3 spec/web-product/check-managed-recovery.py \
    --binary /path/to/tern --home /home/dev/.tern-web-product-qa-review \
    --port 8096 --setup-from /home/dev/.tern-web-product-qa

只支持 Linux 隔离 QA 目录；运行前须无该目录的活跃进程、端口空闲。
使用真实模型，不伪造模型输出；只复制必要模型/QA 认证配置，不输出凭据。
脚本管理自己的 web/app-server，结束时清理进程并保留验收存档。
"""

import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--home', type=Path, required=True)
    parser.add_argument('--port', type=int, default=8096)
    parser.add_argument('--setup-from', type=Path)
    parser.add_argument('--provider', default='deepseek')
    parser.add_argument('--timeout', type=int, default=180)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    binary, home = args.binary.resolve(), args.home.resolve()
    assert Path('/proc').is_dir(), '本脚本依赖 Linux /proc 核验自有进程'
    assert binary.is_file(), 'binary 必须是已编译程序'
    assert home.name.startswith(('.tern-web-product-qa','tern-qa-')), '只允许隔离 QA 运行目录'
    assert args.port not in (8090, 8094, 8095), '不要占用日常或共享验收端口'
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', args.port))
    home.mkdir(mode=0o700, parents=True, exist_ok=True)
    home.chmod(0o700)
    data_dir = home / '.data'
    data_dir.mkdir(mode=0o700, exist_ok=True)
    if args.setup_from and not (home / '.env').exists():
        source = args.setup_from.resolve()
        lines = [line for line in (source / '.env').read_text().splitlines()
                 if line.startswith(('DEEPSEEK_', 'TERN_QA_', 'OBSERVER_TEST_'))]
        (home / '.env').write_text('\n'.join(lines) + '\n')
        config = dict(line.split('=', 1) for line in lines)
        name = config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa')).strip('\"\'')
        users = json.loads((source / '.data/users.json').read_text())
        (data_dir / 'users.json').write_text(json.dumps({
            'users': [user for user in users['users'] if user['name'] == name]}))
    (home / '.env').chmod(0o600)
    (data_dir / 'users.json').chmod(0o600)
    config = dict(line.split('=', 1) for line in (home / '.env').read_text().splitlines()
                  if '=' in line and not line.startswith('#'))
    account = config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa')).strip('\"\'')

    def owned(pid):
        try:
            return Path(os.readlink(f'/proc/{pid}/cwd')) == home
        except OSError:
            return False

    def own_pids():
        return [int(path.name) for path in Path('/proc').iterdir()
                if path.name.isdigit() and owned(int(path.name))]

    assert not own_pids(), '此 QA 目录已有活跃进程，不接管或重启它们'
    report_path = (args.report or home / 'managed-recovery-results.json').resolve()
    report = {'binary': str(binary), 'home': str(home), 'port': args.port,
              'status': 'running', 'cases': {}}

    def save():
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')

    def wait(check, label, timeout=None):
        end = time.monotonic() + (timeout or args.timeout)
        while time.monotonic() < end:
            try:
                result = check()
                if result:
                    return result
            except (urllib.error.URLError, ConnectionError, TimeoutError,
                    FileNotFoundError, KeyError):
                pass
            time.sleep(.2)
        raise AssertionError('等待超时：' + label)

    opener = urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(method, path, body=None):
        request = urllib.request.Request(
            f'http://127.0.0.1:{args.port}' + path, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Content-Type': 'application/json'})
        try:
            response = opener.open(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        text = response.read().decode()
        try:
            value = json.loads(text)
        except json.JSONDecodeError:
            value = text
        return response.status, value

    def state(task):
        code, rows = call('GET', '/runs')
        return next((row for row in rows if row['id'] == task), None) if code == 200 else None

    def events(task):
        path = data_dir / 'runs' / f'{task}.jsonl'
        return [json.loads(line) for line in path.read_bytes().split(b'\n')[:-1] if line]

    def meta(task):
        return json.loads((data_dir / 'runs' / f'{task}.meta.json').read_text())

    def continues(task):
        return [event for event in events(task) if event['kind'] == 'continue']

    def checkpoint(task):
        cpid = next(event['body']['turn_id'] for event in reversed(events(task))
                    if event.get('method') == 'turn/started')
        return json.loads((data_dir / 'checkpoints' / f'{cpid}.json').read_text())

    def create(query):
        code, result = call('POST', '/runs', {'provider': args.provider, 'workspace': '',
                           'request_id': str(uuid.uuid4()), 'query': query})
        assert code == 200, '创建真实模型任务失败'
        return result['id']

    def restart():
        pid_file = data_dir / 'app-server/app-server.pid'
        before = int(pid_file.read_text())
        assert owned(before), 'app-server 不属于本验收目录'
        os.kill(before, signal.SIGTERM)
        wait(lambda: not owned(before), '旧 app-server 退出', 20)
        wait(lambda: call('GET', '/runs')[0] == 200
             and int(pid_file.read_text()) != before, 'web 自动重连新 app-server', 30)

    env = {**os.environ, 'TERN_HOME': str(home)}
    # 隔离验收不把数据发送到调用者可能配置的远程 trace 收集器。
    for key in list(env):
        if key.startswith('OTEL_EXPORTER_'):
            env.pop(key)
    web = None
    original_platform = None
    save()
    try:
        with (home / 'managed-recovery-web.log').open('ab') as log:
            web = subprocess.Popen([str(binary), 'web', '-addr', f'127.0.0.1:{args.port}'],
                                   cwd=home, env=env, stdin=subprocess.DEVNULL,
                                   stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        wait(lambda: call('POST', '/login', {'name': account,
             'password': (config.get('TERN_QA_PASSWORD') or config['OBSERVER_TEST_PASSWORD']).strip('\"\'')})[0] == 200,
             'QA 登录', 20)
        original_platform = call('GET', '/settings')[1]['platform'] or {}
        hook = home / 'managed-recovery-hook.py'
        hook.write_text('import json,sys,time\np=json.load(sys.stdin)\n'
                        'if "RECOVERY_SUBMIT_WAIT_FIXTURE" in p.get("prompt",""): time.sleep(3)\n'
                        'print(json.dumps({"decision":"block","reason":"recovery fixture blocked"}'
                        ' if "RECOVERY_BLOCK_FIXTURE" in p.get("prompt","") else {}))\n')
        hook.chmod(0o600)
        hooks = {'hooks': {'UserPromptSubmit': [{'hooks': [{'type': 'command',
                  'command': '/usr/bin/python3 ' + str(hook), 'timeout': 5}]}]}}
        assert call('PUT', '/settings/platform',
                    {**original_platform, 'hooks': json.dumps(hooks)})[0] == 200

        task = create('接管验收：先使用 ask_user 问“是否继续”，选项“继续”和“等待”。'
                      '必须等待真实回答。收到回答才只回复 RECOVERY_FIRST_OK，不调用其他工具。')
        wait(lambda: (state(task) or {}).get('waiting') == 'question', '真实 ask_user 等待')
        code, queued = call('POST', f'/runs/{task}/queue', {
            'text': '只回复 RECOVERY_QUEUE_OK，不调用工具。', 'request_id': str(uuid.uuid4())})
        assert code == 200
        before = state(task)
        assert not before['done'] and not before.get('queue_paused')
        waiting_ids = [request['id'] for request in before['waiting_requests']]
        restart()
        after = wait(lambda: state(task) if (state(task) or {}).get('waiting') == 'question'
                     else None, '原问题被接管')
        assert not after['done'] and not after.get('queue_paused'), '活跃接管误暂停队列'
        assert [request['id'] for request in after['waiting_requests']] == waiting_ids
        assert any(event['kind'] == 'stdout' and '重新接管' in event.get('text', '')
                   for event in events(task)), '缺少真实接管通知'
        request = next(item for item in after['waiting_requests'] if item['kind'] == 'question')
        assert call('POST', f'/runs/{task}/answer', {'call_id': request['call_id'],
                    'request_id': request['id'], 'answer': '继续'})[0] == 204
        wait(lambda: state(task)['done'] and len(continues(task)) == 1, '普通队列自动续发')
        assert continues(task)[0]['queue_id'] == queued['id']
        assert not state(task).get('queue') and 'RECOVERY_QUEUE_OK' in checkpoint(task)['answer']
        report['cases']['active_restart'] = {'task': task, 'passed': True,
            'queue_paused_after_adoption': after.get('queue_paused', False),
            'waiting_ids_preserved': True, 'queue_id': queued['id'], 'continue_count': 1}
        save()
        print('PASS active_restart', task, flush=True)

        bodies = {}
        for case, blocked, immediate in [('fallback_blocked', True, False),
                                          ('immediate_blocked', True, True),
                                          ('manual_blocked', True, True),
                                          ('handoff_active', False, True),
                                          ('fallback_allowed', False, False)]:
            prefix = 'RECOVERY_BLOCK_FIXTURE_' if blocked else 'RECOVERY_ALLOWED_'
            if case == 'handoff_active':
                prefix = 'RECOVERY_SUBMIT_WAIT_FIXTURE_'
            marker = prefix + uuid.uuid4().hex
            text = marker + ' 最新要求：不调用任何工具，只回复 RECOVERY_FALLBACK_OK。'
            if immediate:
                task = create('先使用 ask_user 问“是否继续”，选项“继续”和“等待”。'
                              '必须等待真实回答，不调用其他工具。')
                wait(lambda: (state(task) or {}).get('waiting') == 'question', '立即发送前的真实问题')
            else:
                task = create('请写一篇约 800 字的中文短文，主题是春天的公园，不调用任何工具。')
                wait(lambda: any(event['kind'] == 'request' for event in events(task)), '真实模型请求开始')
            current = state(task)
            sid = str(uuid.uuid4())
            body = {'text': text, 'managed': True, 'client_id': sid,
                    'request_id': sid, 'turn_seq': current['steer_turn_seq']}
            assert call('POST', f'/runs/{task}/steer', body)[0] == 200
            bodies[task] = body
            if immediate:
                wait(lambda: any(item['id'] == sid and item['state'] == 'accepted'
                     for item in state(task).get('steer_pending', [])), '补充已送达')
                if case == 'manual_blocked':
                    assert call('POST', f'/runs/{task}/stop')[0] == 204
                    wait(lambda: state(task)['done'] and bool(state(task).get('queue')), '补充进入暂停队列')
                    current = state(task)
                    code, received = call('POST', f'/runs/{task}/queue/send', {
                        'id': current['queue'][0]['id'], 'version': current['queue_version'],
                        'all': False, 'request_id': str(uuid.uuid4())})
                    assert code == 200 and set(received) == {'id', 'queue_id'}, '发送回执复制了未确认正文'
                else:
                    assert call('POST', f'/runs/{task}/stop', {
                        'steer_ids': [sid], 'steer_turn': current['steer_turn_seq']})[0] == 204
                if case == 'handoff_active':
                    wait(lambda: not state(task)['done'] and bool(state(task).get('queue_claim')),
                         '提交规则处理期间的领取项')
                    claimed = state(task)
                    assert call('POST', f'/runs/{task}/queue/recover', {
                        'action': 'release', 'id': claimed['queue_claim']['id'],
                        'version': claimed['queue_version']})[0] == 409, '运行中的领取项被释放'
            desired = 'blocked' if blocked else 'delivered'
            wait(lambda: state(task)['done'] and len(continues(task)) == 1
                 and any(item['id'] == sid and item['state'] == desired
                         for item in meta(task)['managed_steers']), '补充转交结局 ' + case)
            ev, cp = events(task), checkpoint(task)
            handoff = continues(task)[0]
            assert handoff['steer_ids'] == [sid] and marker not in handoff['text']
            source_record = next(item for item in meta(task)['managed_steers'] if item['id'] == sid)
            assert not source_record.get('text')
            archive_matches = [event['seq'] for event in ev if marker in json.dumps(event, ensure_ascii=False)]
            item_messages = [event['body']['item']['data'] for event in ev
                             if event.get('method') == 'item/completed'
                             and event['body'].get('item', {}).get('type') == 'userMessage'
                             and not event['body']['item'].get('data', {}).get('steer')]
            if blocked:
                assert cp['reason'] == 'hook_blocked' and not archive_matches
                assert marker not in json.dumps(cp, ensure_ascii=False)
                assert marker not in json.dumps(meta(task), ensure_ascii=False), '拦截原文仍留在领取项'
                assert not any(marker in json.dumps(message) for message in item_messages)
                assert call('POST', f'/runs/{task}/steer/resolve', {'id': sid, 'action': 'queue'})[0] == 409
                root_id = next(event['body']['turn_id'] for event in ev if event.get('method') == 'turn/started')
                journal = data_dir / 'sessions' / f'{root_id}.jsonl'
                assert marker not in journal.read_text(), '阻止的补充仍留在协议会话记录'
                assert not any(marker in path.read_text() for path in (data_dir / 'requests').glob('*.json')), \
                    '阻止的补充仍留在发送回执'
            else:
                assert any(message.get('text') == text for message in item_messages), '通过规则后缺少可读用户消息'
                assert cp['question'] == text and 'RECOVERY_FALLBACK_OK' in cp['answer']
                assert any(event['kind'] == 'request' and any(message.get('role') == 'user'
                           and message.get('content') == text for message in event['body'].get('messages', []))
                           for event in ev), '通过规则的原文没有进入真实模型请求'
            report['cases'][case] = {'task': task, 'steer_id': sid, 'passed': True,
                'state': source_record['state'], 'continue_count': 1,
                'continue_seq': handoff['seq'], 'checkpoint': cp['id'],
                'checkpoint_reason': cp.get('reason'), 'archive_marker_events': archive_matches,
                'checkpoint_contains_marker': marker in json.dumps(cp, ensure_ascii=False),
                'readable_user_message': any(message.get('text') == text for message in item_messages)}
            save()
            print('PASS', case, task, flush=True)

        task = create('先使用 ask_user 问“是否继续”，选项“继续”和“等待”。必须等待真实回答，不调用其他工具。')
        wait(lambda: state(task).get('waiting') == 'question', '提交前失败的真实问题')
        sid, marker = str(uuid.uuid4()), 'RECOVERY_PREFLIGHT_' + uuid.uuid4().hex
        text = '/recovery_missing_command ' + marker + ' 这段原始输入需要能够找回。'
        turn = state(task)['steer_turn_seq']
        body = {'text': text, 'managed': True, 'client_id': sid, 'request_id': sid, 'turn_seq': turn}
        assert call('POST', f'/runs/{task}/steer', body)[0] == 200
        wait(lambda: any(item['id'] == sid and item['state'] == 'accepted'
             for item in state(task).get('steer_pending', [])), '失败补充已送达')
        code, ordinary = call('POST', f'/runs/{task}/queue', {
            'text': '只回复 PREFLIGHT_ORDINARY_HOLD。', 'request_id': str(uuid.uuid4())})
        assert code == 200
        assert call('POST', f'/runs/{task}/stop', {'steer_ids': [sid], 'steer_turn': turn})[0] == 204
        wait(lambda: state(task)['done'] and len(continues(task)) == 1, '转交的命令解析失败')
        current = state(task)
        assert current['queue_claim']['text'] == text and current['queue_paused']
        assert next(item['state'] for item in meta(task)['managed_steers'] if item['id'] == sid) == 'queued'
        assert marker not in json.dumps(events(task), ensure_ascii=False)
        restart()
        current = state(task)
        assert current['queue_claim']['text'] == text and len(continues(task)) == 1
        assert call('POST', f'/runs/{task}/queue/recover', {})[0] == 200
        current = state(task)
        assert current['queue_claim']['text'] == text
        release = {'action': 'release', 'id': current['queue_claim']['id'], 'version': current['queue_version']}
        assert call('POST', f'/runs/{task}/queue/recover', {**release, 'version': 0})[0] == 409
        assert call('POST', f'/runs/{task}/queue/recover', {**release, 'id': 'wrong-claim'})[0] == 409
        assert call('POST', f'/runs/{task}/queue/recover', release)[0] == 200
        current = state(task)
        assert not current.get('queue_claim') and current['queue_paused'] and len(continues(task)) == 1
        assert [item['id'] for item in current['queue']] == [ordinary['id']]
        assert next(item['state'] for item in meta(task)['managed_steers'] if item['id'] == sid) == 'released'
        assert call('POST', f'/runs/{task}/messages', {'query': '已核对并修正输入，只回复 RECOVERY_NEW_SEND_OK。',
                    'request_id': str(uuid.uuid4())})[0] == 200
        wait(lambda: state(task)['done'] and len(continues(task)) == 2, '释放后以新请求继续')
        assert 'RECOVERY_NEW_SEND_OK' in checkpoint(task)['answer']
        current = state(task)
        assert current['queue_paused'] and [item['id'] for item in current['queue']] == [ordinary['id']]
        assert call('DELETE', f'/runs/{task}/queue/{ordinary["id"]}', {'version': current['queue_version']})[0] == 204
        report['cases']['preflight_failure'] = {'task': task, 'steer_id': sid, 'passed': True,
            'state': 'released', 'continue_count': 2, 'retained_until_explicit_release': True,
            'restart_preserved_original': True, 'incorrect_release_rejected': True,
            'new_request_after_release': True}
        save()
        print('PASS preflight_failure', task, flush=True)

        restart()
        for case, row in report['cases'].items():
            task = row['task']
            current = state(task)
            assert current['done'] and len(continues(task)) == row['continue_count']
            assert not current.get('queue') and not current.get('queue_claim')
            if task in bodies:
                assert call('POST', f'/runs/{task}/steer', bodies[task])[0] == 200
                assert next(item['state'] for item in meta(task)['managed_steers']
                            if item['id'] == row['steer_id']) == row['state']
                assert len(continues(task)) == 1
            row['restart_preserves_state_and_no_reexecution'] = True
        report['status'] = 'passed'
    except Exception as error:
        report['status'] = 'failed'
        report['error'] = type(error).__name__ + ': ' + str(error)
        raise
    finally:
        if original_platform is not None:
            try:
                assert call('PUT', '/settings/platform', original_platform)[0] == 200
            except Exception:
                report['settings_restore_failed'] = True
        for path in (data_dir / 'live').glob('*.json'):
            pid = json.loads(path.read_text()).get('pid')
            if pid and owned(pid):
                os.kill(pid, signal.SIGINT)
        if web is not None:
            web.terminate()
            try:
                web.wait(timeout=8)
            except subprocess.TimeoutExpired:
                web.kill()
                web.wait()
        pid_file = data_dir / 'app-server/app-server.pid'
        if pid_file.exists():
            pid = int(pid_file.read_text())
            if owned(pid):
                os.kill(pid, signal.SIGTERM)
        end = time.monotonic() + 10
        while own_pids() and time.monotonic() < end:
            time.sleep(.2)
        report['remaining_owned_pids'] = own_pids()
        save()
    if report['remaining_owned_pids'] or report.get('settings_restore_failed'):
        report['status'] = 'failed'
        save()
        raise AssertionError('独立验收进程或设置未完整清理，请检查报告')
    print('PASS managed recovery:', report_path)


if __name__ == '__main__':
    main()
