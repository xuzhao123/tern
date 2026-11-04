// 子任务的执行结果与 spawn 工具的回执不是同一个状态。这里不依赖 DOM，供卡片、总览和详情共用。
const value = (v) => { if (typeof v !== "string") return v; try { return JSON.parse(v); } catch { return null; } };

export function subTasks(r) {
  const tasks = new Map();
  for (const c of r.calls) for (const t of c.tools || []) {
    if (t.name !== "spawn_agent") continue;
    const args = value(t.args) || {}, res = value(t.result), info = r.subCalls[t.id];
    const id = info?.task || res?.result?.task_id || args.task_id || t.id;
    const attempt = { t, args, res, info, sub: r.subs[id] || null };
    const previous = tasks.get(id);
    tasks.set(id, { ...attempt, lastInfo: info || previous?.lastInfo || previous?.info, id, title: info?.text || args.task || previous?.title || "子任务", attempts: [...(previous?.attempts || []), attempt] });
  }
  const latest = new Map([...tasks.values()].map((task) => [task.t.id, task]));
  for (const c of r.calls) for (const t of c.tools || []) {
    if (t.name !== "agent_wait" && t.name !== "spawn_agent") continue;
    const current = latest.get(t.id);
    if (current) { delete current.collected; delete current.answer; }
    const res = value(t.result), out = res?.result;
    if (res?.status !== "ok" || !out?.task_id || out.status === "running") continue;
    const task = tasks.get(out.task_id);
    if (task && (typeof out.answer === "string" || out.status === "failed")) {
      task.collected = true;
      // 旧生命周期事件没有 answer 时，也能从真实工具回执找回结果。
      if (typeof out.answer === "string") task.answer = out.answer;
    }
  }
  return [...tasks.values()];
}

export function subState(r, item) {
  const { t, sub, res } = item, info = item.info || (res?.status === "error" ? item.lastInfo : null), out = res?.result;
  const calls = info?.calls ?? out?.model_calls ?? sub?.calls.length ?? 0;
  const state = (key, text) => ({ key, text });
  // 后到的生命周期事件比工具回执更可靠，尤其后台 spawn 只是立即返回 running。
  if (info?.state === "done") return state("done", info.replayed ? "取回已有结果" : "已完成");
  if (info?.state === "failed") return state("failed", "失败");
  if (info?.state === "stopped") return state("stopped", "已停止");
  if (res?.status === "ok" && (out?.status === "done" || typeof out?.answer === "string")) return state("done", out.replayed ? "取回已有结果" : "已完成");
  if (res?.status === "error" && !item.info && item.lastInfo?.state === "running") return state("unknown", "状态待确认");
  if (res?.status === "error") return state("failed", "失败");
  if (res?.status === "unknown" || res?.status === "not_run") return state("unknown", "状态待确认");
  if (r.exit) return state("unknown", "状态待确认");
  if (info?.state === "stopping") return state("stopping", "正在停止");
  if (Object.keys(sub?.netPending || {}).length) return state("waiting", "等你审批");
  if (Object.values(sub?.approvals || {}).some((a) => a.state === "waiting")) return state("waiting", "等你审批");
  if (Object.values(sub?.asks || {}).some((a) => a.state === "waiting")) return state("waiting", "等你回答");
  if (info?.state === "running" || out?.status === "running") return state("running", calls ? `运行中 · ${calls} 次请求` : "运行中");
  return t.done ? state("unknown", "状态待确认") : state("starting", "启动中");
}

// 总览只使用已记录的活动，不从耗时或 spawn 的外层 ok 推断进度。
export function subActivity(r, item) {
  const state = subState(r, item), info = item.info || (item.res?.status === "error" ? item.lastInfo : null), out = item.res?.result;
  const retryFailed = !item.info && !!item.lastInfo && item.res?.status === "error";
  const group = state.key === "unknown" ? "unknown" : retryFailed ? "ended" : state.key === "done" ? "done" : ["running", "starting", "waiting", "stopping"].includes(state.key) ? "active" : "ended";
  const time = (value) => Number.isFinite(value) && value > 0 ? value : 0;
  const started = time(item.info?.startedAt) || time(item.t.start);
  let subtitle = state.text, at = 0, clock = "relative";
  if (retryFailed) subtitle = "最近续跑未启动 · " + (state.key === "unknown" ? state.text : "上次" + state.text);
  else if (state.key === "done") subtitle = info?.replayed || out?.replayed ? "取回已有结果" : "";
  else if (state.key === "running") {
    const call = item.sub?.calls.findLast((c) => !c.compact && (!started || c.start >= started));
    const tool = call?.tools.find((t) => !t.done), message = call?.phase === "commentary" && call.choice?.message?.content;
    const labels = { read_file: "读取文件", view_image: "查看图片", read_output: "读取输出", grep: "搜索代码", glob: "查找文件", bash: "执行命令", write_file: "写入文件", edit_file: "修改文件", apply_patch: "应用补丁", agent_wait: "等待子任务", spawn_agent: "派发子任务" };
    subtitle = tool ? labels[tool.name] || "调用 " + tool.name : typeof message === "string" && message.trim() ? message.replace(/\s+/g, " ").trim() : "处理中";
  }
  if (group === "active") { at = started; clock = "elapsed"; }
  else if (group === "done") {
    if (info?.replayed || out?.replayed) {
      // 取回存档的时间不是原任务的完成时间；没有旧完成记录就不显示时间。
      const completed = item.attempts.findLast((attempt) => attempt.info?.state === "done" && !attempt.info.replayed);
      at = time(completed?.info?.finishedAt);
    } else at = time(info?.finishedAt) || time(item.t.done);
  } else if (group === "ended") at = retryFailed ? time(item.t.done) : time(info?.finishedAt) || time(item.t.done);
  else { at = Math.max(time(item.sub?.last), time(info?.startedAt), time(info?.finishedAt), time(item.t.done), time(item.t.start)); clock = "updated"; }
  return { ...state, group, subtitle, at, clock, retryFailed };
}

export function subContext(item) {
  const mode = item.info?.contextMode;
  if (mode === "inherited") return "继承派发时的父对话快照";
  if (mode === "independent") return "独立任务说明，不包含父对话历史";
  if (mode === "checkpoint") return "沿用这个子任务的检查点上下文";
  if (item.args.task_id) return "续跑已有子任务；原始上下文模式未记录";
  if (item.args.fork === true) return "请求继承父对话；实际模式未记录";
  return "上下文模式未记录";
}
