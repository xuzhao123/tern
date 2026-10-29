"""校验项目配置：经 app-server 协议登记带 .agents/tern.json 的工作区、开对话，检查 start 快照（不需要模型）。"""
import json, os, socket, subprocess, sys, tempfile, time

tern = sys.argv[1]
home = tempfile.mkdtemp(prefix="tern-pc-home-")
proj = os.path.join(home, "proj")
os.makedirs(os.path.join(proj, ".agents"))
subprocess.run(["git", "init", "-q", proj], check=True)
with open(os.path.join(proj, ".agents", "tern.json"), "w") as f:
    json.dump({"provider": "deepseek", "web_search": "off", "ds_search": "off", "hooks": "{\"x\":1}", "max_steps": "abc", "net_allow": "example.com"}, f)
env = dict(os.environ, TERN_HOME=home, HOME=home, DEEPSEEK_API_KEY="eval-not-a-key", ARK_API_KEY="eval-not-a-key")
server = subprocess.Popen([tern, "app-server"], env=env, stdout=subprocess.DEVNULL, stderr=open(os.path.join(home, "server.log"), "w"), cwd=home)
sock_path = os.path.join(home, ".data", "app-server", "control.sock")
for _ in range(100):
    if os.path.exists(sock_path):
        break
    time.sleep(0.1)
sock = socket.socket(socket.AF_UNIX)
sock.connect(sock_path)
reader = sock.makefile("r")
next_id = 0

def call(method, params):
    global next_id
    next_id += 1
    sock.sendall((json.dumps({"jsonrpc": "2.0", "id": next_id, "method": method, "params": params}) + "\n").encode())
    while True:
        message = json.loads(reader.readline())
        if message.get("id") == next_id:
            return message.get("result"), message.get("error")

def fail(text):
    server.kill()
    sys.exit(text)

_, err = call("initialize", {"client": {"name": "eval", "version": "1"}, "protocol": int(sys.argv[2])})
if err:
    fail(f"initialize：{err}")
_, err = call("settings/write", {"layer": "platform", "net_allow": "golang.org"})
if err:
    fail(f"settings/write：{err}")
added, err = call("workspace/add", {"path": proj})
if err:
    fail(f"workspace/add：{err}")
def start_event(params):
    started, err = call("thread/start", params)
    if err:
        fail(f"thread/start：{err}")
    thread = started.get("id") or started.get("threadId") or started.get("thread_id")
    path = os.path.join(home, ".data", "runs", f"{thread}.jsonl")
    for _ in range(100):
        if os.path.exists(path):
            for line in open(path):
                event = json.loads(line)
                if event.get("kind") == "start":
                    return event
        time.sleep(0.1)
    fail("没找到这条对话的 start 事件")

with_project = start_event({"query": "只回复 OK", "workspace": added["name"]})
without = start_event({"query": "只回复 OK", "workspace": ""})
server.kill()
problems = []
if with_project.get("provider") != "deepseek":
    problems.append("选了工作区：快照的 provider 应是项目配置里的 deepseek")
if with_project.get("ds_search") or not without.get("ds_search"):
    problems.append("项目配置关掉的 ds_search 在快照里应是关的，不选工作区的对话照常开着")
if not {"golang.org", "example.com"} <= set(with_project.get("net_allow") or []):
    problems.append("net_allow 应是平台与项目的并集（golang.org、example.com）")
if with_project.get("hooks"):
    problems.append("项目配置里的 hooks 不能生效")
if with_project.get("max_steps") == "abc":
    problems.append("不合规的 max_steps 应被忽略")
applied = with_project.get("project_config") or {}
if not {"provider", "ds_search"} <= set(applied):
    problems.append(f"start 事件的 project_config 应记下生效的键：{applied}")
if "hooks" in applied or "max_steps" in applied:
    problems.append(f"project_config 不应包含不生效的 hooks、max_steps：{applied}")
if without.get("provider") == "deepseek" or without.get("project_config"):
    problems.append("不选工作区的对话不应受项目配置影响")
if problems:
    print(json.dumps(with_project, ensure_ascii=False))
    sys.exit("\n".join(problems))
print("ok")
