import { analyze } from "./analysis.js";
import { renderWebPanel } from "./browser.js";
import { renderCall, renderChat, renderCompact } from "./chat.js";
import { renderDiffPanel } from "./diff.js";
import { renderFilesPanel } from "./files.js";
import { renderJobsPanel } from "./jobs.js";
import { md } from "./md.js";
import { $, clip, el, fold, parse, ui } from "./util.js";
import { subActivity, subContext, subState, subTasks } from "./substate.js";
import { openWorkTab, renderWorkPanel, workTab } from "./workpanel.js";
import { renderShell } from "./shell.js";
import { renderProjectFiles } from "./projectfiles.js";
import { renderGitDelivery } from "./git-delivery.js";
import { renderOperations } from "./operations.js";
import { renderContinuous } from "./continuous.js";
import { renderPlan } from "./plan.js";
import { renderPreview } from "./preview.js";
import { subInputActions } from "./subinput.js";
// ---------- 子 agent ----------
function subList(r) {
  return subTasks(r);
}
function avatar(id) {
  let hash = 2166136261;
  for (const char of id) hash = Math.imul(hash ^ char.charCodeAt(0), 16777619) >>> 0;
  const icon = el("span", "agent-avatar"), colors = ["#83bf70", "#ed7884", "#e5b844", "#789edb", "#b08ad2", "#61b4a7", "#d48d62", "#9292c7"];
  icon.style.setProperty("--agent-color", colors[hash % colors.length]);
  icon.setAttribute("aria-hidden", "true");
  const petal = ["M10.3 9.2 8.2 3.7 12 1.2 15.8 3.7 13.7 9.2Z", "M10.4 9.2C5 3 9-1 12 2c3-3 7 1 1.6 7.2Z", "M10.7 9.4 10 2a2 2 0 0 1 4 0l-.7 7.4Z"][(hash >>> 4) % 3];
  icon.innerHTML = '<svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">' + Array.from({ length: 8 }, (_, i) => `<path d="${petal}" transform="rotate(${i * 45} 12 12)" fill="currentColor" opacity="${i % 2 ? .82 : 1}"/>`).join("") + '</svg>';
  return icon;
}
function clockText(at, mode) {
  const seconds = Math.max(0, Math.floor((Date.now() - at) / 1000)), minutes = Math.floor(seconds / 60), hours = Math.floor(minutes / 60);
  if (mode === "elapsed") return hours ? `${hours}h ${minutes % 60}m` : minutes ? `${minutes}m ${seconds % 60}s` : `${seconds}s`;
  const ago = seconds < 60 ? "刚刚" : minutes < 60 ? `${minutes}分钟前` : hours < 24 ? `${hours}小时前` : `${Math.floor(hours / 24)}天前`;
  return ago + (mode === "updated" ? "更新" : "");
}
function agentClock(view) {
  if (!view.at) return null;
  const clock = el("time", "agent-clock", clockText(view.at, view.clock));
  clock.dateTime = new Date(view.at).toISOString(); clock.dataset.at = view.at; clock.dataset.clock = view.clock;
  const label = view.group === "active" ? "本次派发" : view.group === "done" ? "完成于" : view.group === "ended" ? "最近尝试结束于" : "最近记录于";
  clock.title = label + "：" + new Date(view.at).toLocaleString();
  return clock;
}
function agentRow(r, item, view = subActivity(r, item)) {
  const row = el("button", "agent-row " + (view.retryFailed ? "failed" : view.key)), text = el("span", "agent-row-text");
  row.type = "button"; row.dataset.subagent = item.id; row.title = item.title;
  text.append(el("span", "agent-name", item.title));
  if (view.subtitle) text.append(el("span", "agent-progress", view.subtitle));
  row.append(avatar(item.id), text);
  const clock = agentClock(view); if (clock) row.append(clock);
  row.onclick = () => openSub(item.id);
  return row;
}
function openAgents() { openWorkTab("agent", "", "子智能体"); }
export function agentCard(r, t) {
  const item = subList(r).find((x) => x.attempts.some((a) => a.t.id === t.id)), view = subActivity(r, item), card = agentRow(r, item, { ...view, subtitle: view.subtitle || view.text });
  card.classList.add("agent-card"); card.classList.toggle("on", ui.subOpen === item.id);
  return card;
}
function openSub(id) {
  const item = subList(ui.run).find((x) => x.id === id);
  openWorkTab("agent", id, item ? clip(item.title, 32) : "子智能体详情");
}
export function collaborationSummary(r) {
  const list = subList(r);
  if (!list.length) return null;
  const box = el("section", "collaboration-summary");
  box.setAttribute("aria-label", "子任务协作");
  const heading = el("button", "agent-summary-link", `子智能体 · ${list.length}`);
  heading.type = "button"; heading.title = "在右侧查看全部子智能体"; heading.onclick = openAgents;
  box.append(heading);
  for (const item of list) {
    const state = subState(r, item), row = el("button", "collaboration-row");
    row.type = "button";
    row.append(el("span", "q", clip(item.title, 70)), el("span", "sub-st " + state.key, state.text));
    row.onclick = () => openSub(item.id);
    box.append(row);
  }
  const wait = r.calls.flatMap((c) => c.tools).findLast((t) => t.name === "agent_wait" && !t.done);
  if (wait && !r.exit) {
    const id = parse(wait.args)?.task_id, target = list.find((x) => x.id === id);
    box.append(el("div", "meta", id ? `主任务正在等待：${target ? clip(target.title, 60) : id}` : "主任务正在等待任一子任务的结果"));
  }
  return box;
}
export function renderSubPanel() {
  const params=new URLSearchParams(location.search), sub=params.get("sub"), link=ui.run?.id+":"+sub;
  if(sub && params.get("run")===ui.run?.id && !ui.historyReplaying && ui.subLinkApplied!==link && subList(ui.run).some((item)=>item.id===sub)) {ui.subLinkApplied=link;openSub(sub);}
  renderWorkPanel({ git:renderGitDelivery, operations:renderOperations, goal:renderContinuous, plan:renderPlan, files: renderFilesPanel, file: renderFilesPanel, "project-files": renderProjectFiles, "project-file": renderProjectFiles, diff: renderDiffPanel, jobs: renderJobsPanel, browser: renderWebPanel, preview: renderPreview, agent: renderAgentPanel });
}
function renderAgentOverview(panel, r, list) {
  const tab = workTab(), views = list.map((item) => ({ item, view: subActivity(r, item) }));
  const signature = JSON.stringify(views.map(({ item, view }) => [item.id, item.title, view]));
  panel.hidden = false; panel.dataset.agent = "";
  if (panel.querySelector(".agents-overview") && tab.state.overviewSignature === signature) return;
  const focused = panel.contains(document.activeElement) ? document.activeElement.dataset.subagent : null;
  const body = el("div", "sp-body agents-overview");
  for (const [key, label] of [["active", "已开启"], ["done", "完成"], ["ended", "已结束"], ["unknown", "状态待确认"]]) {
    const entries = views.filter(({ view }) => view.group === key);
    if (!entries.length) continue;
    const group = el("section", "agents-group"), items = el("ul", "agents-items");
    group.append(el("h3", "agents-group-title", `${label} · ${entries.length}`));
    for (const { item, view } of entries) { const li = el("li"); li.append(agentRow(r, item, view)); items.append(li); }
    group.append(items); body.append(group);
  }
  if (!list.length) body.append(el("div", "empty", ui.loading ? "正在恢复子智能体…" : "当前任务还没有子智能体。"));
  panel.replaceChildren(body); tab.state.overviewSignature = signature;
  if (focused) [...body.querySelectorAll(".agent-row")].find((row) => row.dataset.subagent === focused)?.focus({ preventScroll: true });
}
function renderAgentPanel() {
  const panel = $("subpanel"), r = ui.run, list = r && ui.page === "chat" ? subList(r) : [];
  if (workTab()?.resource === "") return renderAgentOverview(panel, r, list);
  const item = list.find((x) => x.id === ui.subOpen || x.attempts.some((a) => a.t.id === ui.subOpen));
  if (!item) { panel.hidden = true; panel.replaceChildren(); return; }
  const oldBody = panel.querySelector(".sp-body");
  const stick = !oldBody || oldBody.scrollHeight - oldBody.scrollTop - oldBody.clientHeight < 80, top = oldBody?.scrollTop || 0, sameAgent = panel.dataset.agent === item.id;
  panel.hidden = false;
  panel.dataset.agent = item.id;
  // 关闭在上面的资源标签上，这一行只留返回、身份和标题。
  const head = el("div", "sp-head agent-detail-head"), back = el("button", "sp-close", "←");
  back.type = "button"; back.title = "返回子智能体列表"; back.setAttribute("aria-label", back.title); back.onclick = openAgents;
  head.append(back, avatar(item.id), el("strong", "", clip(item.title, 60)));
  const body = el("div", "sp-body"), st = subState(r, item), res = parse(item.t.result);
  body.append(el("div", "sub-st " + st.key, st.text), el("div", "label", "任务说明"), el("div", "sp-task", item.title));
  body.append(el("div", "meta", subContext(item)), el("div", "meta", `子任务 ID ${item.id} · ${item.attempts.length} 次尝试`));
  body.append(el("div", "meta", `执行目录：${r.workspaceDir || "无项目工作目录"}。与主任务共享文件，子任务不是隔离副本。`), subInputActions(r,item));
  if(item.attempts.length>1) {const history=el("details"),label=el("summary","",`尝试记录 · ${item.attempts.length}`);history.append(label);for(const [index,a] of item.attempts.entries())history.append(el("p","meta",`尝试 ${index+1} · ${a.t.id} · ${a.info?.state || a.res?.status || "状态未记录"} · ${a.info?.error || a.res?.error || ""}`));body.append(history);}
  if (item.args.background) body.append(el("div", "meta", "后台子任务随主任务本轮结束而停止，之后可由主任务续跑。"));
  const answer = item.info?.answer ?? res?.result?.answer ?? item.answer;
  body.append(el("div", "meta", item.collected ? "结果已返回主任务（不代表已验证或采用）" : st.key === "done" ? "已完成，尚未确认主任务回收结果" : "结果尚未返回主任务"));
  if (typeof answer === "string") body.append(el("div", "label", "结果"), md(answer));
  if (!item.info && item.lastInfo && res?.status === "error") body.append(el("div", "err", "最近一次续跑未启动：" + (res.error || "请求失败")));
  if (item.info?.error) body.append(el("div", "err", item.info.error));
  body.append(el("div", "label", "执行过程"));
  const sub = item.sub;
  if (sub) {
    // 子进程的结局来自父 agent 的起止记录；面板据此判断“没有结果”的工具是还在跑，还是进程已经结束。
    sub.exit = ["running", "starting", "waiting", "stopping"].includes(st.key) ? "" : st.key === "done" ? "exit 0" : st.text;
    analyze(sub);
    const flowBox = el("div", "a");
    sub.calls.forEach((c, i) => c.compact ? flowBox.append(renderCompact(c)) : renderCall(flowBox, sub, c, i === sub.calls.length - 1));
    body.append(flowBox);
    if (sub.termination) body.append(el("div", "stopcard err", "子 agent 停止：" + sub.termination));
    if (sub.logs.length) {
      const logs = fold(`子进程终端输出 · ${sub.logs.length} 行`, sub.logs.map((l) => l.text).join("\n"));
      body.append(logs);
    }
  }
  if (!sub?.calls.length && res?.result?.replayed) {
    body.append(el("div", "banner", "这个子任务之前已经完成，本次按子任务ID直接取回存档答案，没有发出新的模型请求。"), md(res.result.answer || ""));
  } else if (!sub?.calls.length) body.append(el("div", st.key === "running" ? "shimmer" : "meta", st.key === "running" ? "正在启动子 agent…" : "没有模型请求记录"));
  if (st.key === "running" && sub?.calls.length && sub.calls.at(-1).end && sub.calls.at(-1).tools.every((t) => t.done)) body.append(el("div", "shimmer", "思考中…"));
  panel.replaceChildren(head, body);
  body.scrollTop = sameAgent ? (stick ? body.scrollHeight : top) : 0;
}
document.addEventListener("tern:work-changed", () => { renderChat(); renderShell(); });
document.addEventListener("tern:open-agents", openAgents);
// 只更新时间文字，不重建列表或挪动用户正在操作的焦点。
setInterval(() => {
  if (document.hidden) return;
  for (const clock of document.querySelectorAll(".agent-clock")) clock.textContent = clockText(Number(clock.dataset.at), clock.dataset.clock);
}, 1000);
