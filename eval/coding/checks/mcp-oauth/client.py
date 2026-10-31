"""校验用的最小 app-server 客户端：临时 TERN_HOME 里起 app-server，经 control.sock 发 JSON-RPC。"""
import json, os, socket, subprocess, sys, tempfile, time


class AppServer:
    def __init__(self, tern, protocol):
        self.home = tempfile.mkdtemp(prefix="tern-eval-home-")
        env = dict(os.environ, TERN_HOME=self.home, HOME=self.home, DEEPSEEK_API_KEY="eval-not-a-key", ARK_API_KEY="eval-not-a-key")
        self.proc = subprocess.Popen([tern, "app-server"], env=env, stdout=subprocess.DEVNULL, stderr=open(os.path.join(self.home, "server.log"), "w"), cwd=self.home)
        path = os.path.join(self.home, ".data", "app-server", "control.sock")
        for _ in range(100):
            if os.path.exists(path):
                break
            time.sleep(0.1)
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.connect(path)
        self.reader = self.sock.makefile("r")
        self.next = 0
        self.must("initialize", {"client": {"name": "eval", "version": "1"}, "protocol": protocol})

    def call(self, method, params):
        self.next += 1
        self.sock.sendall((json.dumps({"jsonrpc": "2.0", "id": self.next, "method": method, "params": params}) + "\n").encode())
        while True:
            message = json.loads(self.reader.readline())
            if message.get("id") == self.next:
                return message.get("result"), message.get("error")

    def must(self, method, params):
        result, err = self.call(method, params)
        if err:
            self.fail(f"{method} 出错：{err}")
        return result

    def events(self, thread):
        path = os.path.join(self.home, ".data", "runs", f"{thread}.jsonl")
        return [json.loads(line) for line in open(path)] if os.path.exists(path) else []

    def wait(self, thread, cond, seconds=60):
        for _ in range(seconds * 10):
            events = self.events(thread)
            if cond(events):
                return events
            time.sleep(0.1)
        self.fail(f"等不到预期的事件：{[e.get('kind') for e in self.events(thread)]}")

    def summary(self, thread):
        listed = self.must("thread/list", {})
        items = listed if isinstance(listed, list) else listed.get("threads") or listed.get("items") or []
        for item in items:
            if item.get("id") == thread:
                return item
        self.fail(f"thread/list 里没有 {thread}")

    def fail(self, text):
        self.proc.kill()
        sys.exit(text)

    def close(self):
        self.proc.kill()
