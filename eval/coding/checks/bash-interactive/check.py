"""校验 bash_input：发给模型的工具定义里有 bash_input（job_id、text、close），bash 有 tty 参数；
bash_input 和 bash_kill 一样按顺序执行；README 写了。真正往后台命令写输入要内层沙箱，校验里起不了，不查。"""
import json, re, sys
sys.path.insert(0, sys.argv[3])
from client import AppServer

problems = []
app = AppServer(sys.argv[1], int(sys.argv[2]))
thread = app.must("thread/start", {"query": "只回复 OK", "workspace": ""})["id"]
events = app.wait(thread, lambda es: any(e.get("kind") == "exit" for e in es))
tools = []
for e in events:
    if e.get("kind") == "request" and isinstance(e.get("body"), dict) and e["body"].get("tools"):
        tools = e["body"]["tools"]
        break
app.close()

def find(name):
    for t in tools:
        f = t.get("function") or t
        if f.get("name") == name:
            return f
    return None

if not tools:
    problems.append("找不到发给模型的请求里的工具定义（bash 没开？）")
else:
    bi = find("bash_input")
    if not bi:
        problems.append("工具定义里应有 bash_input")
    else:
        props = (bi.get("parameters") or {}).get("properties") or {}
        for p in ("job_id", "text", "close"):
            if p not in props:
                problems.append(f"bash_input 应有参数 {p}：{list(props)}")
    bash = find("bash")
    if not bash or "tty" not in ((bash.get("parameters") or {}).get("properties") or {}):
        problems.append("bash 的参数应有 tty")

spec = open("internal/agent/toolspec.go", encoding="utf-8").read()
line = next((l for l in spec.splitlines() if '"bash_input"' in l), "")
kill = next((l for l in spec.splitlines() if '"bash_kill"' in l), "")
if not line:
    problems.append("internal/agent/toolspec.go 应有 bash_input 的特性")
elif "Serial" in kill and "Serial" not in line:
    problems.append(f"bash_input 应和 bash_kill 一样按顺序执行（Serial）：{line.strip()}")
readme = open("README.md", encoding="utf-8").read()
if "bash_input" not in readme or "tty" not in readme:
    problems.append("README 应写 bash_input 与 tty=true")
if problems:
    sys.exit("\n".join(problems))
print("ok")
