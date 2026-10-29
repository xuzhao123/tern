"""发送去重和 SSE 游标的真实服务验收；只访问指定 QA 实例。"""
import argparse
import concurrent.futures
import http.cookiejar
import json
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095

p = argparse.ArgumentParser()
p.add_argument("--home", required=True)
p.add_argument("--url", default=QA_BASE)
p.add_argument("--verify-existing", action="store_true")
args = p.parse_args()
home = Path(args.home).resolve()
assert home.name.startswith((".tern-web-product-qa","tern-qa-"))
config = dict(x.split("=", 1) for x in (home / ".env").read_text().splitlines() if "=" in x and not x.startswith("#"))
deadline = time.monotonic() + 15
while True:
    try:
        urllib.request.urlopen(args.url + "/login", timeout=2).close()
        break
    except urllib.error.URLError:
        if time.monotonic() >= deadline:
            raise
        time.sleep(.2)


def client(user):
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(method, path, body=None, headers=None):
        req = urllib.request.Request(args.url + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                     headers={"Content-Type": "application/json", **(headers or {})})
        try:
            res = opener.open(req, timeout=45)
        except urllib.error.HTTPError as e:
            res = e
        text = res.read().decode()
        try:
            value = json.loads(text)
        except json.JSONDecodeError:
            value = text
        return res.status, value
    assert request("POST", "/login", {"name": user, "password": config["OBSERVER_TEST_PASSWORD"].strip("\"'")})[0] == 200
    return request


call = client(config.get("OBSERVER_TEST_USER", "qa"))
other = client("qa-secondary")
report_path = home / "delivery-results.json"
if args.verify_existing:
    report = json.loads(report_path.read_text())
    for key in ("create", "continue"):
        payload, route = report[key]["body"], report[key]["url"]
        status, result = call("POST", route, payload)
        assert status == 200 and result["id"] == report["task"]
        assert call("GET", "/requests/" + payload["request_id"])[1]["state"] == "accepted"
    report["restart_verified"] = True
    report_path.write_text(json.dumps(report, indent=2))
    print("PASS: persisted create/continue receipts replay without starting additional turns")
    raise SystemExit(0)

body = {"query": "只回复 DELIVERY_OK，不调用工具。", "provider": "deepseek", "workspace": "", "request_id": str(uuid.uuid4())}
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    responses = list(pool.map(lambda _: call("POST", "/runs", body), range(2)))
assert all(status in (200, 409) for status, _ in responses), responses
status, receipt = call("GET", "/requests/" + body["request_id"])
assert status == 200 and receipt["state"] == "accepted", receipt
task_id = receipt["response"]["id"]
assert call("POST", "/runs", {**body, "query": "different"})[0] == 409
assert other("GET", "/requests/" + body["request_id"])[1]["state"] == "not_found"
stale = {**body, "request_id": str(uuid.uuid4())}
assert other("POST", "/runs", stale, {"X-Tern-Account": config.get("OBSERVER_TEST_USER", "qa")})[0] == 409
assert other("GET", "/requests/" + stale["request_id"])[1]["state"] == "not_found"
bad = {"query": "", "request_id": str(uuid.uuid4())}
assert call("POST", "/runs", bad)[0] == 400
assert call("GET", "/requests/" + bad["request_id"])[1]["state"] == "rejected"


def finished_events():
    until = time.monotonic() + 120
    while not next(r for r in call("GET", "/runs")[1] if r["id"] == task_id)["done"]:
        assert time.monotonic() < until, "真实模型任务超时"
        time.sleep(1)
    code, stream = call("GET", f"/runs/{task_id}/events")
    assert code == 200
    return [e for e in (json.loads(line[6:]) for line in stream.splitlines() if line.startswith("data: ")) if "kind" in e]


events = finished_events()
assert sum(e.get("request_id") == body["request_id"] for e in events) == 1
turn = {"query": "继续只回复 DELIVERY_CONTINUED，不调用工具。", "request_id": str(uuid.uuid4())}
route = f"/runs/{task_id}/messages"
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    responses = list(pool.map(lambda _: call("POST", route, turn), range(2)))
assert all(status in (200, 409) for status, _ in responses), responses
events = finished_events()
assert sum(e.get("request_id") == turn["request_id"] for e in events) == 1
assert sum(e["kind"] in ("start", "continue") for e in events) == 2
cursor = events[len(events) // 2]["seq"]
status, stream = call("GET", f"/runs/{task_id}/events", headers={"Last-Event-ID": str(cursor)})
assert status == 200
recovered = [e for e in (json.loads(line[6:]) for line in stream.splitlines() if line.startswith("data: ")) if "kind" in e]
assert [e["seq"] for e in recovered] == [e["seq"] for e in events if e["seq"] > cursor]
assert "id: " in stream
assert call("GET", f"/runs/{task_id}/events?after=-1")[0] == 400
report = {"task": task_id, "create": {"url": "/runs", "body": body}, "continue": {"url": route, "body": turn}, "events": len(events),
          "checks": ["concurrent create once", "concurrent continue once", "payload conflict", "account isolation", "stale account header rejected", "rejected receipt", "SSE cursor exact replay", "negative cursor rejection"], "restart_verified": False}
report_path.write_text(json.dumps(report, indent=2))
print(json.dumps(report))
