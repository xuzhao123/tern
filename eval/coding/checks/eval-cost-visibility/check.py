"""校验评测的成本：逐题平均请求、tokens、未命中、耗时；和基线配对的成本差（含基线平均）；整体每试次请求；
命令行报告；eval/get 的 baseline 参数（空、指定、none、不存在）。用两份手写的评测记录，不需要模型。"""
import json, os, shutil, subprocess, sys, tempfile
sys.path.insert(0, sys.argv[3])
import client
from client import AppServer

tern = sys.argv[1]
problems = []
home = tempfile.mkdtemp(prefix="tern-eval-home-")
os.makedirs(os.path.join(home, ".data", "eval"))
shutil.copytree("eval", os.path.join(home, "eval"))

def trial(case, n, calls, tokens, cached, seconds):
    return {"case": case, "arm": "with", "n": n, "task_id": f"t-{case}-{n}-{calls}", "status": "done", "outcome": "no_tool_calls", "answer": "x",
            "tools": [], "started": "2026-01-01T00:00:00Z", "seconds": seconds, "model_calls": calls, "tokens": tokens, "cached_tokens": cached,
            "checks": [{"name": "x", "pass": True}], "score": 1, "pass": True}

def record(rid, trials):
    return {"id": rid, "status": "done", "started": "2026-01-01T00:00:00Z", "commit": "eval", "cases_file": "eval/cases.jsonl", "cases_hash": "",
            "set": "dev", "only": ["weekday", "no-tool"], "k": 2, "ablation": False, "args": [], "model": "fake", "judge": "none",
            "judge_usage": {"calls": 0, "input_tokens": 0, "output_tokens": 0}, "trials": trials}

base_id, cur_id = "e20990101-000001", "e20990101-000002"
base = [trial("weekday", 1, 10, 1000, 500, 10), trial("weekday", 2, 20, 3000, 1500, 30), trial("no-tool", 1, 2, 100, 0, 1), trial("no-tool", 2, 2, 100, 0, 1)]
cur = [trial("weekday", 1, 4, 2000, 0, 5), trial("weekday", 2, 6, 2000, 1000, 15), trial("no-tool", 1, 4, 300, 0, 3), trial("no-tool", 2, 4, 300, 0, 3)]
json.dump(record(base_id, base), open(os.path.join(home, ".data", "eval", base_id + ".json"), "w"))
json.dump(record(cur_id, cur), open(os.path.join(home, ".data", "eval", cur_id + ".json"), "w"))

# 期望：逐题平均 weekday 5 次、2000、1500、10s；no-tool 4、300、300、3s。
# 成本差（对题取平均）：请求 -4、tokens +100、未命中 +350、耗时 -4；基线平均 8.5、1050、550、10.5。
def near(a, b):
    return isinstance(a, (int, float)) and abs(a - b) < 1e-6

env = dict(os.environ, TERN_HOME=home, HOME=home)
r = subprocess.run([tern, "eval", "report", cur_id, "-baseline", base_id], env=env, capture_output=True, text=True, timeout=120, cwd=home)
out = r.stdout + r.stderr
for want in ("-47%", "+64%", "-38%", "次请求"):
    if want not in out:
        problems.append(f"命令行报告应有 {want}（成本差的百分比、逐题请求次数）：\n{out[-1200:]}")
        break

client.tempfile.mkdtemp = lambda prefix="": home
app = AppServer(tern, int(sys.argv[2]))
res, err = app.call("eval/get", {"id": cur_id, "baseline": base_id})
if err:
    problems.append(f"eval/get 带 baseline 出错：{err}")
else:
    s = res.get("summary", {})
    cases = {c["id"]: c for c in s.get("cases", [])}
    wk = cases.get("weekday", {})
    for key, want in (("calls_per_trial", 5), ("tokens_per_trial", 2000), ("uncached_per_trial", 1500), ("seconds_per_trial", 10)):
        if not near(wk.get(key), want):
            problems.append(f"逐题 weekday 的 {key} 应是 {want}：{wk.get(key)}")
    if not near((s.get("main") or {}).get("calls_per_trial"), 4.5):
        problems.append(f"整体的 calls_per_trial 应是 4.5：{(s.get('main') or {}).get('calls_per_trial')}")
    cost = ((s.get("compare") or {}).get("cost")) or {}
    for key, want in (("calls", -4), ("tokens", 100), ("uncached", 350), ("seconds", -4)):
        if not near(cost.get(key), want):
            problems.append(f"成本差 {key} 应是 {want}：{cost}")
            break
    b = cost.get("base") or {}
    for key, want in (("calls", 8.5), ("tokens", 1050), ("uncached", 550), ("seconds", 10.5)):
        if not near(b.get(key), want):
            problems.append(f"成本差的基线平均 {key} 应是 {want}：{b}")
            break
    if res.get("baseline_id") != base_id:
        problems.append(f"eval/get 应返回实际用的 baseline_id：{res.get('baseline_id')}")
res, err = app.call("eval/get", {"id": cur_id, "baseline": "none"})
if err or (res.get("summary") or {}).get("compare"):
    problems.append(f"baseline=none 应明确不对比：{err or 'compare 仍在'}")
_, err = app.call("eval/get", {"id": cur_id, "baseline": "e20990101-000009"})
if not err:
    problems.append("不存在的基线应报错")
app.close()

if "baseline" not in open("internal/web/server.go", encoding="utf-8").read().split("func (s *server) getEval", 1)[-1][:400]:
    problems.append("网页端取评测详情时应把查询参数 baseline 转给 eval/get")
r = subprocess.run(["node", "--check", "internal/web/page/eval.js"], capture_output=True, text=True)
if r.returncode:
    problems.append(f"node --check eval.js 未通过：{r.stderr[-300:]}")
if problems:
    sys.exit("\n".join(problems))
print("ok")
