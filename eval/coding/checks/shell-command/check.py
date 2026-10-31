"""校验 !命令：thread/shellCommand 记 shell 事件与审计、返回结果、下一轮告诉模型；空命令拒绝；网页路由与脚本。
评测的校验本身跑在沙箱里，里面再起不了一层沙箱，所以不要求命令真的跑成功，只看结果的形状和记录。"""
import json, os, re, subprocess, sys
sys.path.insert(0, sys.argv[3])
from client import AppServer

problems = []
if not re.search(r'/runs/\{id\}/shell', open("internal/web/server.go", encoding="utf-8").read()):
    problems.append("internal/web/server.go 应有 POST /runs/{id}/shell 路由")
for js in ("composer.js", "chat.js", "runs.js"):
    r = subprocess.run(["node", "--check", "internal/web/page/" + js], capture_output=True, text=True)
    if r.returncode:
        problems.append(f"node --check {js} 未通过：{r.stderr[-300:]}")

app = AppServer(sys.argv[1], int(sys.argv[2]))
thread = app.must("thread/start", {"query": "只回复 OK", "workspace": ""})["id"]
app.wait(thread, lambda es: sum(e.get("kind") == "exit" for e in es) >= 1)

result, err = app.call("thread/shellCommand", {"threadId": thread, "command": "echo EVAL_SHELL_$((40+2)); exit 3"})
if err:
    problems.append(f"thread/shellCommand 出错：{err}")
else:
    if not isinstance(result.get("exit_code"), int) or "output" not in result:
        problems.append(f"返回应有 exit_code 与 output：{result}")

_, err = app.call("thread/shellCommand", {"threadId": thread, "command": "   "})
if not err:
    problems.append("空命令应报错")

shells = [e for e in app.events(thread) if e.get("kind") == "shell"]
if len(shells) != 1 or "EVAL_SHELL_" not in (shells[0].get("text") or "") or not isinstance((shells[0].get("body") or {}).get("exit_code"), int):
    problems.append(f"存档里应有一条 shell 事件，text 是命令、body 有 exit_code：{shells}")

audit = os.path.join(app.home, ".data", "audit", "audit.jsonl")
if not os.path.exists(audit) or "user_shell_command" not in open(audit).read():
    problems.append("跑命令前应写审计 user_shell_command")

app.must("turn/start", {"threadId": thread, "query": "继续"})
events = app.wait(thread, lambda es: sum(e.get("kind") == "exit" for e in es) >= 2)
exits = [e["seq"] for e in events if e.get("kind") == "exit"]
second = " ".join(json.dumps(e.get("body"), ensure_ascii=False) for e in events if e.get("kind") == "request" and exits[0] < e["seq"] <= exits[1])
if "EVAL_SHELL_$((40+2))" not in second:
    problems.append("下一轮的模型请求里应告诉模型用户跑过的命令")
app.close()
if problems:
    sys.exit("\n".join(problems))
print("ok")
