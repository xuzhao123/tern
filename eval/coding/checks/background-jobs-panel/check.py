"""校验后台命令面板：app-server 有 thread/job/output、thread/job/kill，这一轮结束后回 409；网页路由、#jobs-btn、jobs.js。
真正的后台命令要模型调 bash，校验里起不了模型和内层沙箱，这部分不查。"""
import os, re, subprocess, sys
sys.path.insert(0, sys.argv[3])
from client import AppServer

problems = []
web = open("internal/web/server.go", encoding="utf-8").read()
if not re.search(r'GET /runs/\{id\}/jobs/\{jid\}/output', web) or not re.search(r'POST /runs/\{id\}/jobs/\{jid\}/kill', web):
    problems.append("internal/web/server.go 应有 GET /runs/{id}/jobs/{jid}/output 与 POST /runs/{id}/jobs/{jid}/kill")
html = open("internal/web/page/index.html", encoding="utf-8").read()
if not re.search(r'id=["\']jobs-btn["\']', html):
    problems.append("index.html 应有 id 为 jobs-btn 的按钮")
if not os.path.exists("internal/web/page/jobs.js"):
    problems.append("应新建 internal/web/page/jobs.js")
for js in ("jobs.js", "runs.js", "agents.js", "shell.js"):
    path = "internal/web/page/" + js
    if os.path.exists(path):
        r = subprocess.run(["node", "--check", path], capture_output=True, text=True)
        if r.returncode:
            problems.append(f"node --check {js} 未通过：{r.stderr[-300:]}")
jobs = open("internal/sandbox/jobs.go", encoding="utf-8").read()
if "job/updated" not in jobs:
    problems.append("internal/sandbox/jobs.go 应发 job/updated 通知")
agent_main = open("main.go", encoding="utf-8").read()
if '"job/output"' not in agent_main or '"job/kill"' not in agent_main:
    problems.append("main.go 的 agent 协议处理应有 job/output 与 job/kill")

app = AppServer(sys.argv[1], int(sys.argv[2]))
thread = app.must("thread/start", {"query": "只回复 OK", "workspace": ""})["id"]
app.wait(thread, lambda es: any(e.get("kind") == "exit" for e in es))
for method in ("thread/job/output", "thread/job/kill"):
    _, err = app.call(method, {"threadId": thread, "jobId": "job-1"})
    if not err or err.get("code") == -32601:
        problems.append(f"{method} 应存在，这一轮结束后报错（409）：{err}")
    elif "结束" not in (err.get("message") or ""):
        problems.append(f"{method} 在这一轮结束后应说明“这一轮已经结束，后台命令也一起结束了”：{err}")
app.close()
if problems:
    sys.exit("\n".join(problems))
print("ok")
