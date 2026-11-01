import { $, HOST, clip, el, hostURL, isUnread, markSeen, markUnread, notificationSettings, readPrefix, runKey, ui } from "./util.js";
import { show } from "./follow.js";
import { approvalCard, askCard, pendingNetCard } from "./sandbox.js";

const dialog = $("activity-dialog"), list = $("activity-list"), pending = $("pending-dialog");
let catalog = [], selected = "waiting", priorFocus, openedRequest = null, signature = "";
let priorStates = new Map();
const projectKey = ({ host, task }) => runKey(host.alias, task.workspace_id || (task.workspace ? `history:${task.workspace}` : "none"));
const projectName = ({ host, task }) => ui.projects?.[host.alias]?.find((p) => p.id === task.workspace_id)?.name || task.workspace || "无项目";
const requests = (task) => task.waiting_requests || (task.waiting ? [{ id: "legacy", kind: task.waiting }] : []);
const title = (task) => task.name || task.query || "未命名任务";
function button(text, action) { const b = el("button", "ghost", text); b.type = "button"; b.onclick = action; return b; }
function fresh(entry) { return !entry.stale && !ui.connectionLost && entry.host.state === "connected"; }
function matches(entry, filter) {
  if (filter === "waiting") return requests(entry.task).length > 0;
  if (filter === "running") return !entry.task.done;
  if (filter === "unread") return !!entry.task.done && isUnread(entry.host.alias, entry.task);
  return true;
}
function scoped() {
  return catalog.filter((e) => ($("activity-host").value === "all" || e.host.alias === $("activity-host").value) &&
    (!$("activity-project").value || projectKey(e) === $("activity-project").value));
}
function options(select, entries) {
  const old = select.value, sig = JSON.stringify(entries);
  if (select.dataset.sig === sig) return;
  select.replaceChildren(...entries.map(([key, text]) => new Option(text, key)));
  if (entries.some(([key]) => key === old)) select.value = old;
  select.dataset.sig = sig;
}
function go(entry, request) {
  const { host, task } = entry;
  dialog.close();
  document.body.classList.remove("nav-open");
  if (host.alias !== HOST) {
    location.href = hostURL(host.alias, task.id) + (request ? `&attention=${encodeURIComponent(request.id)}` : "");
    return;
  }
  ui.page = "chat";
  ui.pendingTarget = request ? { run: task.id, request } : null;
  show(task.id);
}
export function invalidateActivity() { priorStates = new Map(); renderActivity(); renderPending(ui.run); }
export function updateActivity(entries) {
  catalog = [...entries].sort((a, b) => (b.task.updated_at || 0) - (a.task.updated_at || 0));
  const states = new Map();
  for (const entry of entries) {
    const { host, task } = entry, key = runKey(host.alias, task.id), previous = priorStates.get(key);
    const state = { done: !!task.done, completion: task.completion_seq, requests: requests(task), fresh: fresh(entry) };
    states.set(key, state);
    if (!previous?.fresh || !state.fresh) continue;
    for (const request of state.requests) {
      if (!previous.requests.some((p) => p.id === request.id)) notify(entry, request.kind, request.id, request);
    }
    if (state.done && (!previous.done || state.completion !== previous.completion)) notify(entry, "done", String(state.completion || task.updated_at));
  }
  priorStates = states;
  renderActivity(); renderPending(ui.run);
}
export function renderActivity() {
  const count = catalog.filter((e) => requests(e.task).length).length;
  const activityLabel = `活动${count ? ` · 待处理 ${count}` : ""}`;
  ($("activity-label") || $("activity-open")).textContent = activityLabel;
  $("activity-open").title = activityLabel;
  $("activity-open").classList.toggle("has-activity", count > 0);
  if (!dialog.open) return;
  const hosts = ui.hosts.length ? ui.hosts : [...new Map(catalog.map((e) => [e.host.alias, e.host])).values()];
  options($("activity-host"), [["all", "全部主机"], ...hosts.map((h) => [h.alias, h.alias || h.host || "当前主机"])]);
  const projects = [...new Map(catalog.filter((e) => $("activity-host").value === "all" || e.host.alias === $("activity-host").value).map((e) => [projectKey(e), `${projectName(e)} · ${e.host.alias || e.host.host || "当前主机"}`])).entries()];
  options($("activity-project"), [["", "全部项目"], ...projects]);
  const base = scoped(), entries = base.filter((e) => matches(e, selected));
  for (const b of dialog.querySelectorAll("[data-activity-filter]")) {
    const key = b.dataset.activityFilter;
    b.textContent = `${{ waiting: "待处理", running: "运行中", unread: "完成未读", all: "全部" }[key]} ${base.filter((e) => matches(e, key)).length}`;
    b.setAttribute("aria-pressed", String(selected === key));
  }
  $("activity-error").textContent = ui.readingError || ui.notificationError || (ui.connectionLost || hosts.some((h) => h.state !== "connected") || catalog.some((e) => !fresh(e)) ? "部分主机连接中断或列表读取失败，显示最后一次状态，可能已过期。" : "");
  const sig = JSON.stringify(entries.map((e) => [e.host.alias, e.task, projectName(e), fresh(e), isUnread(e.host.alias, e.task)]));
  if (signature !== sig) {
    signature = sig;
    const top = list.scrollTop, focus = document.activeElement?.dataset.activityAction;
    list.replaceChildren();
    for (const entry of entries) {
      const { host, task } = entry, waiting = requests(task), unread = isUnread(host.alias, task);
      const row = el("article", "activity-row"), open = button(title(task), () => go(entry));
      row.dataset.taskId = task.id; open.className = "activity-title";
      open.dataset.activityAction = `open:${runKey(host.alias, task.id)}`;
      row.append(open, el("div", "meta", `${host.alias || host.host || "当前主机"} · ${projectName(entry)}${task.archived ? " · 已归档" : ""}`));
      const state = [!task.done ? "运行中" : "本轮已结束", waiting.length ? `待处理 ${waiting.length}` : "", unread ? "未读" : "", !fresh(entry) ? "状态可能过期" : ""].filter(Boolean).join(" · ");
      row.append(el("div", "activity-status", state));
      const actions = el("div", "activity-actions");
      const read = button(unread ? "标为已读" : "标为未读", () => unread ? markSeen(host.alias, task, true) : markUnread(host.alias, task));
      read.dataset.activityAction = `read:${runKey(host.alias, task.id)}`; actions.append(read);
      for (const request of waiting) {
        // 主任务自己的请求不写来源；子任务的标出是哪个。
        const source = request.task_id ? ` · 子任务 ${request.task_title ? clip(request.task_title, 85) : request.task_id}` : "";
        const act = button(`${request.kind === "question" ? "回答问题" : "处理审批"}${source}${request.domain ? ` · ${request.domain}` : ""}`, () => go(entry, request));
        act.title = [request.task_title, request.task_id].filter(Boolean).join("\n");
        act.disabled = !fresh(entry); act.dataset.activityAction = `request:${runKey(host.alias, task.id)}:${request.id}`;
        actions.append(act);
      }
      row.append(actions); list.append(row);
    }
    if (!entries.length) list.append(el("p", "empty", catalog.length ? "当前筛选没有任务" : "还没有可显示的任务；连接恢复后会自动更新"));
    list.scrollTop = top;
    if (focus) [...list.querySelectorAll("[data-activity-action]")].find((b) => b.dataset.activityAction === focus)?.focus({ preventScroll: true });
  }
  $("activity-read-all").disabled = !entries.some((e) => isUnread(e.host.alias, e.task));
  $("activity-read-all").onclick = () => {
    // 此闭包固定本次渲染的任务版本，新事件不能被同一次批量确认覆盖。
    const snapshot = entries.map((e) => ({ alias: e.host.alias, task: { ...e.task } }));
    Promise.all(snapshot.map((e) => markSeen(e.alias, e.task, true)));
  };
}
export function openActivity(filter = "waiting") {
  priorFocus = document.activeElement; selected = filter; signature = "";
  if (!dialog.open) dialog.showModal(); renderActivity(); $("activity-close").focus();
}
$("activity-open").onclick = () => openActivity();
$("activity-close").onclick = () => dialog.close();
dialog.addEventListener("close", () => priorFocus?.focus?.());
for (const b of dialog.querySelectorAll("[data-activity-filter]")) b.onclick = () => { selected = b.dataset.activityFilter; renderActivity(); };
$("activity-host").onchange = $("activity-project").onchange = renderActivity;
window.addEventListener("tern-reading-changed", renderActivity);
window.addEventListener("storage", (event) => { if (event.key?.startsWith(readPrefix())) window.dispatchEvent(new Event("tern-reading-changed")); });

// 展示使用当前事件流还原的真实请求，处理仍走原有审批/回答 API。
export function renderPending(root) {
  const target = ui.pendingTarget;
  const count = !root?.exit ? Object.values(root?.approvals || {}).filter((a) => a.state === "waiting").length + Object.values(root?.asks || {}).filter((a) => a.state === "waiting").length + Object.keys(root?.netPending || {}).length : 0;
  $("attention-open").hidden = !count;
  $("attention-open").textContent = `待处理 ${count}`;
  if (!root || (openedRequest && openedRequest.run !== root.id)) { if (pending.open) pending.close(); }
  if (!root || ui.historyReplaying) return;
  if (target?.run === root.id) {
    const event = ui.events.find((e) => e.kind === "approval" && `event:${e.seq}` === target.request.id);
    const resolved = catalog.find((e) => e.host.alias === HOST && e.task.id === root.id)?.task.waiting_requests?.find((p) => p.id === target.request.id);
    openedRequest = { ...target, request: resolved || (event ? { ...event.body, id: target.request.id } : target.request) }; ui.pendingTarget = null;
    if (!pending.open) pending.showModal();
  }
  if (!pending.open || openedRequest?.run !== root.id) return;
  const request = openedRequest.request, body = $("pending-body");
  const oldInput = body.querySelector("input"), focus = oldInput === document.activeElement;
  body.replaceChildren();
  if (ui.streamDisconnected || ui.connectionLost) { body.append(el("p", "", "连接中断，恢复状态后再处理。")); return; }
  if (root.exit || (request.id !== "legacy" && !request.call_id && !request.domain)) { body.append(el("p", "", "该请求已结束或无法定位，请核对对话记录。")); return; }
  if (request.domain) body.append(el("p", "meta", request.task_id ? `来源：子任务 ${request.task_title ? clip(request.task_title, 85) : request.task_id}` : "来源：主任务"));
  let rendered = false;
  const add = (item) => { body.append(item); rendered = true; };
  for (const [id, a] of Object.entries(root.approvals || {})) if ((!request.call_id || request.call_id === id) && !request.domain && a.state === "waiting") add(approvalCard(root, id));
  for (const [id, a] of Object.entries(root.asks || {})) if ((!request.call_id || request.call_id === id) && !request.domain && a.state === "waiting") add(askCard(root, { id, args: a }));
  for (const domain of Object.keys(root.netPending || {})) if ((!request.domain || request.domain === domain) && (!request.call_id || request.domain)) add(pendingNetCard(domain));
  if (!rendered) {
    const input = root.asks?.[request.call_id] || root.approvals?.[request.call_id];
    const text = input?.state === "answered" ? `已回答：${input.answer || ""}`
      : input?.state === "expired" ? "请求已取消、超时或子任务已结束，不能再答复。"
      : input?.state === "deny" ? "已拒绝这项操作。"
      : ["allow", "allow_session"].includes(input?.state) ? "已允许这项操作，请在任务中查看后续结果。"
      : "该请求已处理、已过期或尚未同步。请核对对话记录；这里不会重新执行。";
    body.append(el("p", "", text));
  }
  if (focus) body.querySelector("input")?.focus();
}
$("attention-open").onclick = () => {
  if (!ui.run) return;
  ui.pendingTarget = { run: ui.run.id, request: { id: "legacy" } }; renderPending(ui.run);
};
$("pending-close").onclick = () => pending.close();
pending.addEventListener("close", () => { openedRequest = null; });

async function notify(entry, kind, id, request) {
  const mode = notificationSettings()[kind] || "off";
  if (mode === "off" || (mode !== "always" && document.visibilityState === "visible") || typeof Notification === "undefined" || Notification.permission !== "granted") return;
  if (!navigator.locks) { ui.notificationError = "浏览器不支持多页面通知去重，系统通知暂停；请使用活动中心。"; renderActivity(); return; }
  const account = ui.account, { host, task } = entry;
  const key = `tern-notice-v2:${encodeURIComponent(account)}:${encodeURIComponent(runKey(host.alias, task.id))}:${kind}`;
  try {
    await navigator.locks.request(key, () => {
      if (account !== ui.account) return;
      const delivered = JSON.parse(localStorage.getItem(key) || "[]");
      if (delivered.includes(id)) return;
      localStorage.setItem(key, JSON.stringify([...delivered, id]));
      const notice = new Notification(`${{ done: "本轮已结束", approval: "等你审批", question: "等你回答" }[kind]}：${title(task).slice(0, 40)}`, { tag: key });
      notice.onclick = () => { notice.close(); if (ui.account !== account) return; window.focus(); go(entry, request); };
    });
  } catch (error) { ui.notificationError = "系统通知未能发送：" + error.message; renderActivity(); }
}
