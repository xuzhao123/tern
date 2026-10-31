"""假的要求 OAuth 的远程 MCP server：受保护资源与授权服务器元数据、动态注册、换令牌；没带令牌的 /mcp 回 401。"""
import json, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs

class Fake:
    def __init__(self):
        self.tokens = []      # 收到的换令牌请求（表单）
        self.registered = 0
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, body, headers=None):
                data = json.dumps(body).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                for k, v in (headers or {}).items():
                    self.send_header(k, v)
                self.end_headers()
                self.wfile.write(data)

            def do_GET(self):
                base = fake.base
                if self.path.startswith("/.well-known/oauth-protected-resource"):
                    return self.send(200, {"resource": base + "/mcp", "authorization_servers": [base]})
                if self.path.startswith("/.well-known/oauth-authorization-server") or self.path.startswith("/.well-known/openid-configuration"):
                    return self.send(200, {"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token",
                                           "registration_endpoint": base + "/register", "response_types_supported": ["code"],
                                           "grant_types_supported": ["authorization_code", "refresh_token"], "code_challenge_methods_supported": ["S256"],
                                           "token_endpoint_auth_methods_supported": ["client_secret_post", "client_secret_basic", "none"]})
                if self.path.startswith("/mcp"):
                    return self.unauthorized()
                self.send(404, {"error": "not_found"})

            def unauthorized(self):
                self.send(401, {"error": "unauthorized"}, {"WWW-Authenticate": f'Bearer resource_metadata="{fake.base}/.well-known/oauth-protected-resource"'})

            def do_POST(self):
                length = int(self.headers.get("Content-Length") or 0)
                raw = self.rfile.read(length).decode()
                if self.path.startswith("/register"):
                    fake.registered += 1
                    body = json.loads(raw or "{}")
                    return self.send(201, {"client_id": "eval-client", "client_secret": "EVAL_CLIENT_SECRET", "redirect_uris": body.get("redirect_uris", []),
                                           "client_id_issued_at": 1, "client_secret_expires_at": 0})
                if self.path.startswith("/token"):
                    form = {k: v[0] for k, v in parse_qs(raw).items()}
                    fake.tokens.append(form)
                    if form.get("code") != "EVAL_CODE":
                        return self.send(400, {"error": "invalid_grant"})
                    return self.send(200, {"access_token": "EVAL_ACCESS_TOKEN", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "EVAL_REFRESH"})
                if self.path.startswith("/mcp"):
                    if self.headers.get("Authorization") != "Bearer EVAL_ACCESS_TOKEN":
                        return self.unauthorized()
                    msg = json.loads(raw or "{}")
                    if msg.get("method") == "initialize":
                        return self.send(200, {"jsonrpc": "2.0", "id": msg.get("id"), "result": {"protocolVersion": "2025-03-26", "capabilities": {"tools": {}}, "serverInfo": {"name": "fake", "version": "1"}}})
                    if "id" not in msg:
                        self.send_response(202); self.end_headers(); return
                    return self.send(200, {"jsonrpc": "2.0", "id": msg.get("id"), "result": {"tools": []}})
                self.send(404, {"error": "not_found"})

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.base = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
