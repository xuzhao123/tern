"""一条命令跑完网页产品的验收脚本（check-*）：建临时 QA 运行目录、qa 账号、起网页端，按顺序跑，最后汇总。

    DEEPSEEK_API_KEY=... python3 spec/web-product/run-checks.py --binary /path/to/tern [--only a,b] [--keep]

- 运行目录是 /tmp/tern-qa-<时间>（脚本自己的保险只认 tern-qa- 开头），.env 只放 DEEPSEEK_API_KEY 和随机生成的 qa 密码。
- 被测程序用 --binary 指定（通常是临时 clone 编出的），拷进运行目录再用：脚本按"程序路径在运行目录下"认自己的 app-server 和 agent 进程。不碰 ~/.tern。
- 除了 qa 再建一个 qa-secondary（跨账号隔离的检查要用），两个账号同一个随机密码。
- 需要人在浏览器里操作、或要先构造崩溃现场的脚本列在 MANUAL 里，不在这里跑，汇总里照样列出。
- 各脚本的证据（*-results.json 等）留在运行目录；--keep 时不删。
"""
import argparse, json, os, secrets, shutil, socket, subprocess, sys, tempfile, time, urllib.request, http.cookiejar
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent

# 每一步：(名字, 脚本, 参数, 跑之前要不要重启服务)。{home}、{binary} 运行时替换。
# 顺序有依赖：workpanel 建的 panel-fixtures 和 workpanel-results.json 给 projectfiles、closeout、operations 用；closeout 建的 closeout-fixture 给 operations 用。
STEPS = [
    ("diff-lines", "check-diff-lines.mjs", [], False),
    ("highlight", "check-highlight.mjs", [], False),
    ("substate", "check-substate.mjs", [], False),
    ("permission-state", "check-permission-state.mjs", [], False),
    ("navigation", "check-navigation.py", ["--home", "{home}"], False),
    ("delivery", "check-delivery.py", ["--home", "{home}"], False),
    ("workpanel", "check-workpanel.py", ["--home", "{home}"], False),
    ("projectfiles", "check-projectfiles.py", ["--home", "{home}"], False),
    ("closeout", "check-closeout.py", ["--home", "{home}", "--model"], False),
    ("copy-delivery", "check-copy-delivery.py", ["--home", "{home}"], False),
    ("git-delivery", "check-git-delivery.py", ["--home", "{home}"], False),
    ("operations", "check-operations.py", ["--home", "{home}"], False),
    ("skill-refs", "check-skill-refs.py", ["--home", "{home}"], False),
    ("goal-arbitration", "check-goal-arbitration.py", ["--home", "{home}"], False),
    ("goal-plan plan", "check-goal-plan.py", ["--home", "{home}", "--mode", "plan"], False),
    ("goal-plan goal", "check-goal-plan.py", ["--home", "{home}", "--mode", "goal"], False),
    ("goal-plan budget", "check-goal-plan.py", ["--home", "{home}", "--mode", "budget"], False),
    ("goal-plan pause", "check-goal-plan.py", ["--home", "{home}", "--mode", "pause"], False),
    ("subcontrol", "check-subcontrol.py", ["--home", "{home}"], False),
    ("subresume", "check-subresume.py", ["--home", "{home}"], False),
    ("shell-access", "check-shell-access.py", ["--home", "{home}", "--binary", "{binary}"], False),
    ("queue start", "check-queue.py", ["--home", "{home}", "--mode", "start"], False),
    ("queue finish", "check-queue.py", ["--home", "{home}", "--mode", "finish"], False),  # 脚本自己会重启 app-server 核对队列
    ("closeout-lifecycle", "check-closeout-lifecycle.py", ["--home", "{home}", "--binary", "{binary}"], False),
] + [(f"managed-steer {s}", "check-managed-steer.py", ["--home", "{home}", "--scenario", s], False)
     for s in ("consumed", "stop", "immediate", "stopping", "unknown", "natural", "hook", "storage")]

# 不在这里跑的：要人在浏览器里答、或要在停掉的服务上构造现场、或自己另起一套服务。
MANUAL = {
    "check-activity.py": "先要人在网页上答一个等待中的请求（activity-test.json）",
    "check-subinput.py": "start 之后要人在网页上回答子 agent 的问题，再 verify",
    "check-queue-recovery.py": "要在停掉的服务上构造崩溃窗口",
    "check-managed-recovery.py": "自己另起一套服务（--port 8096、--setup-from）",
    "check-diff-review.py": "按 --port 起服务、带 --report/--resume 的长流程",
    "check-managed-steer.py --scenario ui": "要浏览器操作",
    "check-managed-steer.py --scenario legacy": "要旧版本程序留下的对话（steer-legacy.json）",
}


def free_port():
    s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
    return port


class Service:
    def __init__(self, binary, home, port):
        self.binary, self.home, self.port, self.web = binary, home, port, None
        self.env = dict(os.environ, TERN_HOME=str(home), TERN_QA_BASE=f"http://127.0.0.1:{port}")

    def start(self):
        log = open(self.home / "web.log", "a")
        self.web = subprocess.Popen([self.binary, "web", "-addr", f"127.0.0.1:{self.port}"], env=self.env, stdout=log, stderr=log, cwd=self.home)
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), urllib.request.ProxyHandler({}))
        for _ in range(150):
            try:
                opener.open(urllib.request.Request(f"http://127.0.0.1:{self.port}/login"), timeout=2)
                return
            except urllib.error.HTTPError:
                return
            except Exception:
                time.sleep(0.2)
        raise SystemExit("网页端起不来，见 " + str(self.home / "web.log"))

    def stop(self):
        if self.web:
            self.web.terminate()
            try:
                self.web.wait(10)
            except subprocess.TimeoutExpired:
                self.web.kill()
        subprocess.run([self.binary, "app-server", "stop"], env=self.env, capture_output=True, timeout=60)

    def restart(self):
        self.stop(); self.start()


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--binary", required=True, help="被测的 tern 程序")
    p.add_argument("--only", default="", help="只跑这几步（名字的前缀，逗号分隔）")
    p.add_argument("--keep", action="store_true", help="跑完保留运行目录")
    p.add_argument("--timeout", type=int, default=900, help="每一步的时限（秒）")
    a = p.parse_args()
    if not os.environ.get("DEEPSEEK_API_KEY"):
        raise SystemExit("要设 DEEPSEEK_API_KEY（只导出这一个）")
    home = Path(tempfile.mkdtemp(prefix="tern-qa-"))
    binary = str(home / "tern")
    shutil.copy2(Path(a.binary).resolve(), binary)
    password = secrets.token_urlsafe(18)
    (home / ".env").write_text(f"DEEPSEEK_API_KEY={os.environ['DEEPSEEK_API_KEY']}\nOBSERVER_TEST_USER=qa\nOBSERVER_TEST_PASSWORD={password}\n")
    os.chmod(home / ".env", 0o600)
    env = dict(os.environ, TERN_HOME=str(home))
    for user in ("qa", "qa-secondary"):
        r = subprocess.run([binary, "web", "-add-user", user], input=password + "\n", text=True, env=env, capture_output=True)
        if r.returncode:
            raise SystemExit(f"建 {user} 账号失败：" + r.stderr)
    svc = Service(binary, home, free_port())
    svc.start()
    only = [x.strip() for x in a.only.split(",") if x.strip()]
    results = []
    try:
        for name, script, args, restart in STEPS:
            if only and not any(name.startswith(o) for o in only):
                continue
            if restart:
                svc.restart()
            argv = [arg.replace("{home}", str(home)).replace("{binary}", binary) for arg in args]
            cmd = (["node"] if script.endswith(".mjs") else [sys.executable]) + [str(HERE / script)] + argv
            started = time.monotonic()
            try:
                run = subprocess.run(cmd, env=svc.env, cwd=ROOT, capture_output=True, text=True, timeout=a.timeout)
                ok, tail = run.returncode == 0, (run.stdout + run.stderr).strip()[-600:]
            except subprocess.TimeoutExpired:
                ok, tail = False, f"超时（{a.timeout}s）"
            seconds = time.monotonic() - started
            results.append({"step": name, "pass": ok, "seconds": round(seconds), "tail": tail})
            print(f"{'✓' if ok else '✗'} {name}（{seconds:.0f}s）", flush=True)
            if not ok:
                print("   " + tail.replace("\n", "\n   "), flush=True)
            if svc.web.poll() is not None:
                svc.start()  # 有的脚本自己停了服务
    finally:
        svc.stop()
        (home / "run-checks.json").write_text(json.dumps(results, ensure_ascii=False, indent=1))
    passed = sum(r["pass"] for r in results)
    print(f"\n通过 {passed}/{len(results)}；没在这里跑的 {len(MANUAL)} 项：")
    for k, v in MANUAL.items():
        print(f"  - {k}：{v}")
    if a.keep:
        print("运行目录：" + str(home))
    else:
        shutil.rmtree(home, ignore_errors=True)
    sys.exit(0 if passed == len(results) else 1)


if __name__ == "__main__":
    main()
