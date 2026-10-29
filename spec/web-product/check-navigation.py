"""真实隔离服务验收。只在 --home 指定的 QA 运行目录里创建仓库与测试数据。"""
import argparse
import http.cookiejar
import json
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095

p = argparse.ArgumentParser()
p.add_argument("--home", required=True)
p.add_argument("--url", default=QA_BASE)
p.add_argument("--verify-existing", action="store_true")
args = p.parse_args()
home = Path(args.home).resolve()
assert home.name.startswith((".tern-web-product-qa","tern-qa-")), "只允许专用 QA 目录"
config = dict(line.split("=", 1) for line in (home / ".env").read_text().splitlines() if "=" in line and not line.startswith("#"))
password = config["OBSERVER_TEST_PASSWORD"].strip("\"'")

# 重启后先等只读登录页就绪，不重试创建任务等有副作用请求。
ready_deadline = time.monotonic() + 15
while True:
    try:
        with urllib.request.urlopen(args.url + "/login", timeout=2) as response:
            assert response.status == 200
        break
    except urllib.error.URLError:
        if time.monotonic() >= ready_deadline:
            raise
        time.sleep(.2)


def client(user):
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(method, path, body=None, status=200):
        req = urllib.request.Request(args.url + path, data=json.dumps(body).encode() if body is not None else None,
                                     method=method, headers={"Content-Type": "application/json"})
        try:
            res = opener.open(req, timeout=30)
        except urllib.error.HTTPError as error:
            res = error
        content = res.read().decode()
        assert res.status == status, (path, res.status, content[:300])
        try:
            return json.loads(content)
        except json.JSONDecodeError:
            return content
    call("POST", "/login", {"name": user, "password": password})
    return call


qa = client(config.get("OBSERVER_TEST_USER", "qa"))
other = client("qa-secondary")
if args.verify_existing:
    report = json.loads((home / "navigation-results.json").read_text())
    for call in (qa, other):
        task = next(t for t in call("GET", "/runs") if t["id"] == report["task"])
        assert task["pinned"] is True
        assert task["workspace_id"] == report["workspace_id"]
        assert task["workspace_dir"] == report["path"]
    project = next(w for w in qa("GET", "/workspaces") if w["id"] == report["workspace_id"])
    assert project["name"] == report["name"]
    second_path = str(Path(report["path"]).parent.parent / "two" / "repo")
    second = next(w for w in qa("GET", "/workspaces") if w["path"] == second_path)
    qa("DELETE", "/settings/workspaces/" + second["name"])
    assert Path(second_path).is_dir(), "移除登记不得删除目录"
    restored = qa("POST", "/settings/workspaces", {"path": second_path})
    assert restored["id"] == second["id"], "重新登记同一目录仍是同一项目身份"
    if "remove preserves directory and re-register identity" not in report["checks"]:
        report["checks"].append("remove preserves directory and re-register identity")
    report["restart_verified"] = True
    (home / "navigation-results.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print("PASS: project identity, rename and both accounts' pins survive process restart")
    raise SystemExit(0)
stamp = str(time.time_ns())
root = home / "navigation-fixtures" / stamp
projects = []
for parent in ("one", "two"):
    repo = root / parent / "repo"
    repo.mkdir(parents=True)
    subprocess.run(["git", "init", "-q", str(repo)], check=True)
    projects.append(qa("POST", "/settings/workspaces", {"path": str(repo)}))
assert projects[0]["id"] != projects[1]["id"]
assert projects[0]["name"] != projects[1]["name"]
qa("POST", "/settings/workspaces", {"path": projects[0]["path"]}, 409)
task = qa("POST", "/runs", {"query": "只回复 PROJECT_ID_OK，不调用工具，不修改文件。", "workspace": projects[0]["name"], "provider": "deepseek", "permission": "plan"})
task_id = task["id"]
current = lambda call: next(t for t in call("GET", "/runs") if t["id"] == task_id)
deadline = time.monotonic() + 120
while not current(qa)["done"]:
    assert time.monotonic() < deadline, "真实任务超时"
    time.sleep(1)
before = current(qa)
assert before["workspace_id"] == projects[0]["id"]
name = "nav-" + stamp[-12:]
renamed = qa("PUT", "/settings/workspaces/" + projects[0]["name"] + "/name", {"newName": name})
assert renamed["id"] == before["workspace_id"]
after = current(qa)
assert after["workspace_dir"] == before["workspace_dir"]
assert after["project_dir"] == projects[0]["path"]
assert after["workspace_id"] == before["workspace_id"]
assert next(w for w in qa("GET", "/workspaces") if w["id"] == before["workspace_id"])["name"] == name
qa("PUT", f"/runs/{task_id}/pin", {"pinned": True, "user": "qa-secondary"})
assert current(qa)["pinned"] is True, "web 必须用登录身份覆盖请求中的 user"
assert current(other)["pinned"] is False
qa("PUT", f"/runs/{task_id}/pin", {"pinned": None}, 400)
qa("PUT", f"/runs/{task_id}/pin", {"pinned": "true"}, 400)
other("PUT", f"/runs/{task_id}/pin", {"pinned": True})
qa("PUT", f"/runs/{task_id}/pin", {"pinned": False})
assert current(qa)["pinned"] is False and current(other)["pinned"] is True
qa("PUT", f"/runs/{task_id}/pin", {"pinned": True})
report = {"task": task_id, "workspace_id": before["workspace_id"], "name": name, "path": projects[0]["path"],
          "checks": ["distinct directories", "duplicate registration rejection", "real task", "rename stable identity", "unchanged execution directory", "account-isolated pins", "forged user ignored", "invalid pin rejected"],
          "restart_verified": False}
(home / "navigation-results.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
print(json.dumps(report, ensure_ascii=False))
