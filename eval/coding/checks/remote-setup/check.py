"""校验远程主机的设置：连不上时按种类说明（auth、hostkey、unreachable、no-tern），/hosts 回 502 JSON；
proxy 命令优先用 ~/.tern/bin/tern；initialize 带 providers；新路由。用假的 ssh，不需要真的远端。"""
import http.cookiejar, json, os, re, shutil, socket, stat, subprocess, sys, tempfile, time, urllib.request
sys.path.insert(0, sys.argv[3])
os.environ["NO_PROXY"] = os.environ["no_proxy"] = "127.0.0.1,localhost"
from client import AppServer

tern, here = sys.argv[1], sys.argv[3]
problems = []
web_src = open("internal/web/server.go", encoding="utf-8").read()
for route in (r"POST /hosts/\{alias\}/install", r"GET /hosts/\{alias\}/hostkey", r"POST /hosts/\{alias\}/hostkey", r"POST /hosts/\{alias\}/keys"):
    if not re.search(route, web_src):
        problems.append(f"internal/web/server.go 应有路由 {route.replace(chr(92), '')}")

# initialize 的 providers：配了 DEEPSEEK_API_KEY 的这台应列出 deepseek
app = AppServer(tern, int(sys.argv[2]))
info = app.info if isinstance(app.info, dict) else {}
providers = (info.get("server") or info).get("providers")
if not providers or "deepseek" not in providers:
    problems.append(f"initialize 的 ServerInfo 应带 providers（配好密钥的供应商）：{app.info}")
home = app.home
app.close()

# 假 ssh 放在 PATH 最前面，起网页端
bindir = tempfile.mkdtemp(prefix="tern-eval-bin-")
shutil.copy(os.path.join(here, "fakessh"), os.path.join(bindir, "ssh"))
os.chmod(os.path.join(bindir, "ssh"), 0o755)
log = os.path.join(bindir, "ssh.log")
env = dict(os.environ, TERN_HOME=home, HOME=home, PATH=bindir + ":" + os.environ["PATH"], FAKE_SSH_LOG=log, DEEPSEEK_API_KEY="eval-not-a-key")
r = subprocess.run([tern, "web", "-add-user", "eval"], input="eval-password\n", text=True, env=env, capture_output=True)
if r.returncode:
    sys.exit(f"web -add-user 失败：{r.stderr}")
s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
web = subprocess.Popen([tern, "web", "-addr", f"127.0.0.1:{port}"], env=env, stdout=subprocess.DEVNULL, stderr=open(os.path.join(home, "web.log"), "w"), cwd=home)
base = f"http://127.0.0.1:{port}"
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), urllib.request.ProxyHandler({}))
def call(method, path, body=None):
    req = urllib.request.Request(base + path, method=method, data=json.dumps(body).encode() if body is not None else None, headers={"Content-Type": "application/json"})
    try:
        with opener.open(req, timeout=60) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()
for _ in range(100):
    try:
        if call("POST", "/login", {"name": "eval", "password": "eval-password"})[0] < 400:
            break
    except Exception:
        pass
    time.sleep(0.2)
else:
    web.kill(); sys.exit("网页端起不来或登录不了")

for alias, kind in (("evalauth", "auth"), ("evalhostkey", "hostkey"), ("evalunreach", "unreachable"), ("evalnotern", "no-tern")):
    code, text = call("POST", "/hosts", {"alias": alias})
    try:
        body = json.loads(text)
    except ValueError:
        body = {}
    if code != 502 or body.get("kind") != kind or not body.get("hint") or "error" not in body or not body.get("detail"):
        problems.append(f"{alias} 连不上时 /hosts 应回 502 和 JSON（kind={kind}，带 error、hint、detail）：{code} {text[:300]}")
web.kill()

calls = open(log).read() if os.path.exists(log) else ""
if ".tern/bin/tern" not in calls:
    problems.append(f"proxy 命令应优先用远端的 $HOME/.tern/bin/tern：{calls[-400:]}")
if problems:
    sys.exit("\n".join(problems))
print("ok")
