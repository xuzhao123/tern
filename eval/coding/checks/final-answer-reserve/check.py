"""校验最后一次请求留给最终回答：设了 -max-steps 时最后一次请求 tool_choice=none、带工具定义、带 [最后一次请求] 提醒，
日志有 Final request，检查点 forced_final；75% 那档改成 [收尾检查]；评测汇总有“被逼收尾”。用本地的假模型，不需要外网。"""
import glob, json, os, subprocess, sys, tempfile
sys.path.insert(0, sys.argv[2])
os.environ["NO_PROXY"] = os.environ["no_proxy"] = "127.0.0.1,localhost"
from fake_model import Fake

tern = sys.argv[1]
problems = []

def run(steps):
    fake = Fake()
    home = tempfile.mkdtemp(prefix="tern-eval-home-")
    env = dict(os.environ, TERN_HOME=home, HOME=home, DEEPSEEK_API_KEY="eval-not-a-key", DEEPSEEK_API_URL=fake.base + "/chat/completions")
    r = subprocess.run([tern, "-provider", "deepseek", "-workspace", os.getcwd(), "-max-steps", str(steps), "-question", "逐个读这个仓库的文件，说明每个文件的作用。"],
                       env=env, capture_output=True, text=True, timeout=240, cwd=home)
    cps = [json.load(open(p)) for p in glob.glob(os.path.join(home, ".data", "checkpoints", "*.json"))]
    return fake.requests, r.stdout + r.stderr, cps

reqs, out, cps = run(3)
if len(reqs) != 3:
    problems.append(f"-max-steps 3 应正好请求 3 次：{len(reqs)} 次；输出末尾：{out[-600:]}")
else:
    last = reqs[-1]
    if last.get("tool_choice") != "none":
        problems.append(f"最后一次请求应 tool_choice=none：{last.get('tool_choice')}")
    if not last.get("tools"):
        problems.append("最后一次请求仍应带工具定义（Messages 协议的历史里有 tool_use 时必须带）")
    if "[最后一次请求]" not in json.dumps(last.get("messages"), ensure_ascii=False):
        problems.append("最后一次请求应带 [最后一次请求] 提醒")
    if any(r.get("tool_choice") == "none" for r in reqs[:-1]):
        problems.append("只有最后一次请求禁止调用工具")
if "Final request: tools disabled" not in out:
    problems.append("终端日志应有 Final request: tools disabled")
if "Termination: no_tool_calls" not in out:
    problems.append(f"结局应是 no_tool_calls：{out[-400:]}")
main = [c for c in cps if c.get("answer")]
if not main or not main[0].get("forced_final") or "EVAL_FINAL_ANSWER" not in main[0].get("answer", ""):
    problems.append(f"检查点应有最终回答和 forced_final: true：{[{k: c.get(k) for k in ('reason', 'forced_final')} for c in cps]}")

reqs, out, _ = run(8)
texts = [json.dumps(r.get("messages"), ensure_ascii=False) for r in reqs]
first = next((i for i, t in enumerate(texts) if "[收尾检查]" in t), None)
if first is None:
    problems.append("用到 75% 时应插 [收尾检查] 提醒")
elif "[请求预算]" not in texts[-1]:
    problems.append("50% 那档的 [请求预算] 提醒应保留")

src = open("internal/eval/cli.go", encoding="utf-8").read()
if "被逼收尾" not in src:
    problems.append("编码题汇总应列出“被逼收尾”的次数（internal/eval/cli.go）")
if problems:
    sys.exit("\n".join(problems))
print("ok")
