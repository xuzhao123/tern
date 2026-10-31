"""校验 MCP 的 OAuth 登录：动态注册、PKCE 授权地址、回调换令牌存 .data/mcp-tokens.json（0600，不进 hub.json）、
hub/list 的 auth 状态、审计、退出登录；网页端三条路由。用本地的假 OAuth server，不需要外网。"""
import base64, hashlib, json, os, re, stat, sys
from urllib.parse import urlparse, parse_qs
sys.path.insert(0, sys.argv[3])
os.environ["NO_PROXY"] = os.environ["no_proxy"] = "127.0.0.1,localhost"
from client import AppServer
from fake_oauth import Fake

problems = []
web = open("internal/web/server.go", encoding="utf-8").read()
for route in (r"POST /hub/mcp/\{name\}/login", r"POST /hub/mcp/\{name\}/logout", r"GET /mcp-oauth/callback"):
    if not re.search(route, web):
        problems.append(f"internal/web/server.go 应有路由 {route.replace(chr(92), '')}")

fake = Fake()
app = AppServer(sys.argv[1], int(sys.argv[2]))
app.must("hub/mcp/add", {"name": "evaloauth", "command": fake.base + "/mcp"})

def auth():
    listed = app.must("hub/list", {})
    item = next((m for m in listed.get("mcp_servers", []) if m.get("name") == "evaloauth"), {})
    return item.get("auth")

redirect = "http://127.0.0.1:9/mcp-oauth/callback"
result, err = app.call("hub/mcp/login/start", {"name": "evaloauth", "redirect": redirect})
if err:
    app.fail("\n".join(problems + [f"hub/mcp/login/start 出错：{err}"]))
q = {k: v[0] for k, v in parse_qs(urlparse(result.get("url", "")).query).items()}
if fake.registered != 1 or q.get("client_id") != "eval-client":
    problems.append(f"没有 client_id 时应先动态注册，授权地址带注册到的 client_id：registered={fake.registered} {q}")
if q.get("redirect_uri") != redirect or not q.get("state") or q.get("code_challenge_method") != "S256" or not q.get("code_challenge"):
    problems.append(f"授权地址应带 redirect_uri、state 与 S256 的 code_challenge：{q}")

_, err = app.call("hub/mcp/login/finish", {"state": "not-a-state", "code": "EVAL_CODE"})
if not err:
    problems.append("不认识的 state 应报错")
result, err = app.call("hub/mcp/login/finish", {"state": q.get("state", ""), "code": "EVAL_CODE"})
if err:
    problems.append(f"hub/mcp/login/finish 出错：{err}")
else:
    form = fake.tokens[-1] if fake.tokens else {}
    verifier = form.get("code_verifier", "")
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    if not verifier or challenge != q.get("code_challenge"):
        problems.append(f"换令牌时应带与授权地址对得上的 code_verifier：{form}")

tokens = os.path.join(app.home, ".data", "mcp-tokens.json")
if not os.path.exists(tokens):
    problems.append("令牌应存在 .data/mcp-tokens.json")
else:
    if stat.S_IMODE(os.stat(tokens).st_mode) != 0o600:
        problems.append(f".data/mcp-tokens.json 权限应是 0600：{oct(stat.S_IMODE(os.stat(tokens).st_mode))}")
    if "EVAL_ACCESS_TOKEN" not in open(tokens).read():
        problems.append(".data/mcp-tokens.json 里应有换到的令牌")
hub = os.path.join(app.home, ".data", "hub.json")
if os.path.exists(hub) and ("EVAL_CLIENT_SECRET" in open(hub).read() or "EVAL_ACCESS_TOKEN" in open(hub).read()):
    problems.append("client_secret 与令牌不能写进 hub.json")
if auth() != "logged_in":
    problems.append(f"登录后 hub/list 里这一项的 auth 应是 logged_in：{auth()}")
listed = json.dumps(app.must("hub/list", {}))
if "EVAL_ACCESS_TOKEN" in listed or "EVAL_CLIENT_SECRET" in listed:
    problems.append("令牌与 client_secret 不能经协议发给界面")

app.must("hub/mcp/logout", {"name": "evaloauth"})
if os.path.exists(tokens) and "EVAL_ACCESS_TOKEN" in open(tokens).read():
    problems.append("退出登录后令牌文件里不应再有这个 server 的令牌")
if auth() != "required":
    problems.append(f"退出登录后 auth 应是 required：{auth()}")
audit = os.path.join(app.home, ".data", "audit", "audit.jsonl")
text = open(audit).read() if os.path.exists(audit) else ""
for event in ("user_mcp_login", "user_mcp_logout"):
    if event not in text:
        problems.append(f"应写审计 {event}")
if "EVAL_ACCESS_TOKEN" in text:
    problems.append("审计里不能写令牌")
app.close()
if problems:
    sys.exit("\n".join(problems))
print("ok")
