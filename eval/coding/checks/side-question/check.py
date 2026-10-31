"""校验旁支提问：thread/fork 带 side 时新对话记 side、已归档、名字“旁支”，重启后仍在；不带 side 的分叉不变；页面有 #side-btn。"""
import json, os, re, subprocess, sys, time
sys.path.insert(0, sys.argv[3])
from client import AppServer

problems = []
html = open("internal/web/page/index.html", encoding="utf-8").read()
if not re.search(r'id=["\']side-btn["\']', html):
    problems.append("index.html 应有 id 为 side-btn 的按钮")
for js in ("internal/web/page/shell.js", "internal/web/page/chat.js"):
    r = subprocess.run(["node", "--check", js], capture_output=True, text=True)
    if r.returncode:
        problems.append(f"node --check {js} 未通过：{r.stderr[-300:]}")

app = AppServer(sys.argv[1], int(sys.argv[2]))
thread = app.must("thread/start", {"query": "只回复 OK", "workspace": ""})["id"]
events = app.wait(thread, lambda es: any(e.get("kind") == "exit" for e in es))
turn = next((e.get("task") or (e.get("body") or {}).get("task") for e in events if e.get("kind") == "start"), None)
if not turn:
    turn = next((e.get("task") for e in events if e.get("task")), None)
if not turn:
    app.fail(f"找不到第一轮的检查点ID：{[e.get('kind') for e in events]}")

side = app.must("thread/fork", {"threadId": thread, "turn": turn, "side": True})
plain = app.must("thread/fork", {"threadId": thread, "turn": turn})

def check(summary, label):
    if summary.get("side") is not True:
        problems.append(f"{label}：带 side 分出来的对话 summary 应有 side: true：{summary}")
    if summary.get("archived") is not True:
        problems.append(f"{label}：旁支应已归档：{summary}")
    if summary.get("name") != "旁支":
        problems.append(f"{label}：旁支的名字在发问题前应是“旁支”：{summary.get('name')}")

check(side, "thread/fork 的返回")
if plain.get("side") or plain.get("archived"):
    problems.append(f"不带 side 的分叉不应记 side 或归档：{plain}")
app.close()

# 重启后从 .meta.json 读回来
home = app.home
app2 = AppServer.__new__(AppServer)
env = dict(os.environ, TERN_HOME=home, HOME=home, DEEPSEEK_API_KEY="eval-not-a-key", ARK_API_KEY="eval-not-a-key")
sock = os.path.join(home, ".data", "app-server", "control.sock")
if os.path.exists(sock):
    os.remove(sock)
proc = subprocess.Popen([sys.argv[1], "app-server"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, cwd=home)
import socket
for _ in range(100):
    if os.path.exists(sock):
        break
    time.sleep(0.1)
app2.home, app2.proc = home, proc
app2.sock = socket.socket(socket.AF_UNIX); app2.sock.connect(sock)
app2.reader = app2.sock.makefile("r"); app2.next = 0
app2.must("initialize", {"client": {"name": "eval", "version": "1"}, "protocol": int(sys.argv[2])})
check(app2.summary(side["id"]), "重启后")
app2.close()
if problems:
    sys.exit("\n".join(problems))
print("ok")
