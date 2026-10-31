"""假的 OpenAI 兼容模型（流式）：带工具且没禁用时，每次让模型读一个不同的文件；tool_choice 为 none 时给文字回答。
每次请求的 body 记在 requests 里。"""
import json, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

FILES = ["main.go", "go.mod", "README.md", "AGENTS.md", "internal/agent/react.go", "internal/llm/llm.go", "internal/agent/checkpoint.go", "internal/eval/cli.go", "internal/agent/context_manager.go", "internal/eval/summary.go"]

class Fake:
    def __init__(self):
        self.requests = []
        fake = self

        class H(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"
            def log_message(self, *a):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or "{}")
                fake.requests.append(body)
                n = len(fake.requests)
                if body.get("tools") and body.get("tool_choice") != "none":
                    path = FILES[(n - 1) % len(FILES)]
                    delta = {"role": "assistant", "content": "", "tool_calls": [{"index": 0, "id": f"call_{n}", "type": "function",
                             "function": {"name": "read_file", "arguments": json.dumps({"path": path})}}]}
                    finish = "tool_calls"
                else:
                    delta = {"role": "assistant", "content": "EVAL_FINAL_ANSWER：改了什么、验收结果、没做的项。"}
                    finish = "stop"
                chunks = [{"id": "x", "object": "chat.completion.chunk", "model": "fake", "choices": [{"index": 0, "delta": delta, "finish_reason": None}]},
                          {"id": "x", "object": "chat.completion.chunk", "model": "fake", "choices": [{"index": 0, "delta": {}, "finish_reason": finish}]},
                          {"id": "x", "object": "chat.completion.chunk", "model": "fake", "choices": [], "usage": {"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110}}]
                data = "".join(f"data: {json.dumps(c, ensure_ascii=False)}\n\n" for c in chunks) + "data: [DONE]\n\n"
                raw = data.encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.base = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
