"""校验 tern mcp-server：stdio 握手、三个工具及必填参数、tern_threads 经 app-server 调通（不需要模型）。"""
import json, os, subprocess, sys, tempfile

home = tempfile.mkdtemp(prefix="tern-mcp-home-")
env = dict(os.environ, TERN_HOME=home, HOME=home)
proc = subprocess.Popen([sys.argv[1], "mcp-server"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=open("/tmp/w/mcp-stderr.txt", "w"), env=env, text=True)

def send(message):
    proc.stdin.write(json.dumps(message) + "\n")
    proc.stdin.flush()

def call(id, method, params):
    send({"jsonrpc": "2.0", "id": id, "method": method, "params": params})
    while True:
        line = proc.stdout.readline()
        if not line:
            sys.exit("mcp-server 提前退出：" + open("/tmp/w/mcp-stderr.txt").read()[-800:])
        message = json.loads(line)
        if message.get("id") == id:
            if "error" in message:
                sys.exit(f"{method} 出错：{message['error']}")
            return message["result"]

def fail(text):
    proc.kill()
    sys.exit(text)

info = call(1, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "eval", "version": "1"}})
send({"jsonrpc": "2.0", "method": "notifications/initialized"})
if info.get("serverInfo", {}).get("name") != "tern":
    fail(f"server 名字应是 tern：{info.get('serverInfo')}")
tools = {t["name"]: t for t in call(2, "tools/list", {})["tools"]}
if sorted(tools) != ["tern_reply", "tern_start", "tern_threads"]:
    fail(f"工具应恰好是 tern_start、tern_reply、tern_threads：{sorted(tools)}")
required = lambda name: set(tools[name].get("inputSchema", {}).get("required") or [])
if "prompt" not in required("tern_start"):
    fail("tern_start 的 prompt 应是必填")
if not {"thread_id", "prompt"} <= required("tern_reply"):
    fail("tern_reply 的 thread_id、prompt 应是必填")
for name in ("workspace", "provider", "model", "permission"):
    if name not in tools["tern_start"].get("inputSchema", {}).get("properties", {}):
        fail(f"tern_start 缺少可选参数 {name}")
result = call(3, "tools/call", {"name": "tern_threads", "arguments": {"limit": 5}})
if result.get("isError"):
    fail(f"tern_threads 返回错误：{result}")
text = "".join(part.get("text", "") for part in result.get("content", []))
data = json.loads(text) if text.strip().startswith(("[", "{")) else None
if data is None and "structuredContent" in result:
    data = result["structuredContent"]
if data is None:
    fail(f"tern_threads 的结果不是 JSON：{result}")
proc.kill()
subprocess.run([sys.argv[1], "app-server", "stop"], env=env, capture_output=True)
print("ok")
