"""校验副本对话：thread/start copy=true 建副本并带上未提交改动、快照指向副本；workspace/applyCopy 应用、无改动与冲突的处理。"""
import json, os, subprocess, sys
sys.path.insert(0, sys.argv[3])
from client import AppServer

app = AppServer(sys.argv[1], int(sys.argv[2]))
proj = os.path.join(app.home, "proj")
git = lambda *args, cwd=proj: subprocess.run(["git", "-c", "user.name=eval", "-c", "user.email=eval@localhost", *args], cwd=cwd, check=True, capture_output=True, text=True).stdout
os.makedirs(proj)
git("init", "-q")
open(os.path.join(proj, "a.txt"), "w").write("one\n")
open(os.path.join(proj, "b.txt"), "w").write("keep\n")
git("add", "-A"); git("commit", "-qm", "init")
open(os.path.join(proj, "a.txt"), "w").write("one\nuncommitted\n")
open(os.path.join(proj, "new.txt"), "w").write("untracked\n")
problems = []
name = app.must("workspace/add", {"path": proj})["name"]

thread = app.must("thread/start", {"query": "只回复 OK", "workspace": name, "copy": True})["id"]
events = app.wait(thread, lambda es: any(e.get("kind") == "start" for e in es))
start = next(e for e in events if e.get("kind") == "start")
copy = start.get("workspace_dir", "")
if not copy or os.path.realpath(copy) == os.path.realpath(proj) or not os.path.isdir(copy):
    app.fail(f"快照的 workspace_dir 应是副本目录：{copy}")
if os.path.realpath(start.get("copy_of", "")) != os.path.realpath(proj) or not start.get("copy_base"):
    problems.append(f"start 事件应记 copy_of（原目录）与 copy_base（基线）：{start.get('copy_of')} {start.get('copy_base')}")
if open(os.path.join(copy, "a.txt")).read() != "one\nuncommitted\n" or not os.path.exists(os.path.join(copy, "new.txt")):
    problems.append("副本应带上原目录未提交的改动和未跟踪文件")
if not app.summary(thread).get("copy"):
    problems.append("summary 应带 copy: true")
app.wait(thread, lambda es: any(e.get("kind") == "exit" for e in es))

_, err = app.call("workspace/applyCopy", {"threadId": thread})
if not err:
    problems.append("副本没有新改动时 applyCopy 应报错")

open(os.path.join(copy, "b.txt"), "w").write("changed in copy\n")
open(os.path.join(copy, "made.txt"), "w").write("made in copy\n")
result, err = app.call("workspace/applyCopy", {"threadId": thread})
if err:
    problems.append(f"applyCopy 失败：{err}")
elif open(os.path.join(proj, "b.txt")).read() != "changed in copy\n" or not os.path.exists(os.path.join(proj, "made.txt")):
    problems.append("applyCopy 后原目录应有副本里的改动")
elif "b.txt" not in json.dumps(result) or "made.txt" not in json.dumps(result):
    problems.append(f"applyCopy 应返回改了哪些文件：{result}")
if git("status", "--porcelain").count("A ") or git("diff", "--cached", "--name-only").strip():
    problems.append("应用到原目录不应暂存（不 --index）")
if git("log", "--oneline").count("\n") != 1:
    problems.append("应用到原目录不应提交")
if "copy_applied" not in json.dumps(app.events(thread), ensure_ascii=False):
    problems.append("存档里应有 copy_applied 事件")

thread2 = app.must("thread/start", {"query": "只回复 OK", "workspace": name, "copy": True})["id"]
start2 = next(e for e in app.wait(thread2, lambda es: any(e.get("kind") == "exit" for e in es)) if e.get("kind") == "start")
copy2 = start2["workspace_dir"]
open(os.path.join(copy2, "b.txt"), "w").write("conflicting copy edit\n")
open(os.path.join(proj, "b.txt"), "w").write("original moved on\n")
_, err = app.call("workspace/applyCopy", {"threadId": thread2})
if not err:
    problems.append("原目录同一处已改时 applyCopy 应报冲突")
elif open(os.path.join(proj, "b.txt")).read() != "original moved on\n":
    problems.append("冲突时原目录不应被改动")
app.close()
if problems:
    sys.exit("\n".join(problems))
print("ok")
