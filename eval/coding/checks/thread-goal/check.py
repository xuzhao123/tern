"""校验对话目标：thread/start 带 goal、thread/goal/set 改与清除、summary 的 goal、下一轮请求里告诉模型、审计。"""
import json, os, sys
sys.path.insert(0, sys.argv[3])
from client import AppServer

app = AppServer(sys.argv[1], int(sys.argv[2]))
problems = []

def goal_of(thread):
    goal = app.summary(thread).get("goal")
    return goal.get("text") if isinstance(goal, dict) else goal

def turn_requests(thread, after):
    events = app.wait(thread, lambda es: sum(e.get("kind") == "exit" for e in es) >= after)
    exits = [e["seq"] for e in events if e.get("kind") == "exit"]
    low = exits[after - 2] if after >= 2 else 0
    return " ".join(json.dumps(e.get("body"), ensure_ascii=False) for e in events if e.get("kind") == "request" and low < e["seq"] <= exits[after - 1])

thread = app.must("thread/start", {"query": "只回复 OK", "workspace": "", "goal": "EVAL_GOAL_ONE"})["id"]
if "EVAL_GOAL_ONE" not in turn_requests(thread, 1):
    problems.append("thread/start 带的 goal 应在第一轮告诉模型")
if goal_of(thread) != "EVAL_GOAL_ONE":
    problems.append(f"summary 的 goal 应是 EVAL_GOAL_ONE：{app.summary(thread)}")

app.must("thread/goal/set", {"threadId": thread, "text": "  EVAL_GOAL_TWO  "})
if goal_of(thread) != "EVAL_GOAL_TWO":
    problems.append(f"thread/goal/set 后 summary 的 goal 应是去掉首尾空白的 EVAL_GOAL_TWO：{app.summary(thread)}")
app.must("turn/start", {"threadId": thread, "query": "继续"})
if "EVAL_GOAL_TWO" not in turn_requests(thread, 2):
    problems.append("改了目标后，下一轮的模型请求里应告诉模型新目标")

app.must("thread/goal/set", {"threadId": thread, "text": ""})
if goal_of(thread):
    problems.append(f"空串应清除目标：{app.summary(thread)}")
app.must("turn/start", {"threadId": thread, "query": "继续"})
if "清除" not in turn_requests(thread, 3):
    problems.append("清除目标后，下一轮应告诉模型目标已清除")

audit = os.path.join(app.home, ".data", "audit", "audit.jsonl")
if not os.path.exists(audit) or "user_thread_goal" not in open(audit).read():
    problems.append("改目标应写审计 user_thread_goal")
app.close()
if problems:
    sys.exit("\n".join(problems))
print("ok")
