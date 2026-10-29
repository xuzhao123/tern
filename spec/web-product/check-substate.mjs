// 纯展示状态回归，不替代真实模型和页面验收。
import assert from "node:assert/strict";
import { subContext, subState, subTasks } from "../../internal/web/page/substate.js";

const call = { id: "dispatch-a", name: "spawn_agent", args: JSON.stringify({ task: "检查权限", background: true }), done: 1,
  result: JSON.stringify({ status: "ok", result: { task_id: "child-a", status: "running" } }) };
const run = { calls: [{ tools: [call] }], subCalls: {}, subs: {}, exit: "" };
const item = () => subTasks(run)[0];
assert.equal(subState(run, item()).key, "running", "后台启动成功仍在运行");
run.subCalls[call.id] = { task: "child-a", text: "检查权限", state: "running", contextMode: "inherited" };
assert.match(subContext(item()), /继承/);
run.subs["child-a"] = { calls: [], approvals: { approval: { state: "waiting" } } };
assert.equal(subState(run, item()).key, "waiting");
run.subCalls[call.id].state = "done";
assert.equal(subState(run, item()).key, "done", "完成事件优先于旧 running 回执");
run.subCalls[call.id].state = "failed";
assert.equal(subState(run, item()).key, "failed", "后台失败不能被外层 ok 覆盖");
run.subCalls[call.id].state = "stopped";
assert.equal(subState(run, item()).key, "stopped");
run.subCalls[call.id].state = "running";
run.exit = "exit 0";
assert.equal(subState(run, item()).key, "unknown", "父轮结束不能猜子任务成功或停止");
run.exit = "";
run.calls.push({ tools: [{ id: "dispatch-b", name: "spawn_agent", args: JSON.stringify({ task_id: "child-a" }) }] });
assert.equal(subTasks(run).length, 1, "续跑按 task_id 聚合");
assert.equal(item().attempts.length, 2);
assert.equal(item().title, "检查权限");
assert.match(subContext(item()), /未记录/);
run.calls[1].tools[0].result = JSON.stringify({ status: "ok", result: { task_id: "child-a", answer: "通过", replayed: true } });
assert.equal(subState(run, item()).text, "取回已有结果");
assert.equal(item().collected, true);
assert.equal(item().answer, "通过");
// 同 task_id 的续跑请求未启动，不能覆盖上一实例已经停止的事实。
run.calls[1].tools[0].result = JSON.stringify({ status: "error", error: "启动被拒绝" });
run.subCalls[call.id].state = "stopped";
assert.equal(subState(run, item()).key, "stopped");
console.log("PASS: background lifecycle, waiting, stale receipt, failure, missing end, resume identity, context and replay");
