import { patchLive } from "./chat.js";
import { openEvals } from "./eval.js";
import { render, schedule } from "./render.js";
import { apply, newRun } from "./runs.js";
import { loadRuns, newChat, syncComposer } from "./shell.js";
import { $, api, ui } from "./util.js";
import { leaveDraft, readPendingSteers, switchDraft } from "./drafts.js";
import { settleSteers } from "./composer.js";
import { leaveWorkPanel } from "./workpanel.js";
// ---------- 数据加载、实时跟随与回放 ----------
export function fetchEvents(id) {
  return new Promise((resolve) => {
    const list = [], es = new EventSource(api(`/runs/${id}/events`));
    es.onmessage = (m) => list.push(JSON.parse(m.data));
    es.addEventListener("end", () => { es.close(); resolve(list); });
    es.onerror = () => { es.close(); resolve(list); };
  });
}
// URL 复用后端的对话 ID；切换对话才新增历史，续聊仍使用同一个链接。
export function syncRunURL(id) {
  const url = new URL(location.href);
  if (id) url.searchParams.set("run", id); else url.searchParams.delete("run");
  if (id) url.searchParams.delete("eval");
  url.searchParams.delete("attention");
  if (url.href !== location.href) history.pushState(null, "", url);
}
export function restoreRunURL() {
  const params = new URL(location.href).searchParams, id = params.get("run");
  if (id) {
    if (params.get("attention")) ui.pendingTarget = { run: id, request: { id: params.get("attention") } };
    show(id, false);
  } else if (params.get("eval") !== null) openEvals(params.get("eval"), false); else newChat(false);
}
export function show(id, updateURL = true) {
  if (!leaveDraft()) return;
  leaveWorkPanel();
  if (updateURL || ui.run?.id !== id) ui.readExplicit = id;
  stopReplay();
  if (ui.source) ui.source.close();
  if (ui.tab === "compare") ui.tab = "trace";
  Object.assign(ui, { run: newRun(id), events: [], follow: true, zoom: null, selected: null, expanded: false, loading: true, streamDisconnected: false, search: "", open: new Set(), subOpen: null, webOpen: null, webClosed: false, chatFollow: true, unseen: false, hint: "" });
  switchDraft(id, true);
  ui.steers[id] ??= readPendingSteers(id);
  ui.historyReplaying = true;
  if (updateURL) syncRunURL(id);
  $("search").value = "";
  ui.collapsed.clear();
  let sequence = 0, delay = 1000, lastEnd = -1, knownDone = false;
  const connect = () => {
    const es = ui.source = new EventSource(api(`/runs/${encodeURIComponent(id)}/events?after=${sequence}`));
    let snapshot = Infinity;
    es.addEventListener("snapshot", (message) => {
      if (ui.source !== es) return;
      snapshot = JSON.parse(message.data).through;
      ui.historyReplaying = sequence < snapshot;
      syncComposer();
    });
    es.onopen = () => {
      if (ui.source !== es) return;
      ui.streamDisconnected = false;
      if (ui.hint.startsWith("连接中断")) ui.hint = "";
      syncComposer(); render();
    };
    es.onmessage = (m) => {
      if (ui.source !== es) return;
      const e = JSON.parse(m.data);
      if (Number.isSafeInteger(e.seq) && e.seq <= sequence) return;
      if (Number.isSafeInteger(e.seq)) sequence = e.seq;
      delay = 1000;
      ui.historyReplaying = e.seq <= snapshot;
      ui.events.push(e); apply(ui.run, e);
      if (sequence >= snapshot) ui.historyReplaying = false;
      if (!(e.kind === "delta" && !e.sub && patchLive(ui.run))) schedule();
    };
    const verify = async () => {
      try {
        const response = await fetch(api("/runs"));
        if (ui.source !== es) return false;
        if (response.ok) {
          const task = (await response.json()).find((r) => r.id === id);
          if (!task) { es.close(); ui.hint = "任务不存在，已停止重连。"; syncComposer(); return false; }
          knownDone = !!task.done;
        }
      } catch { /* 失联不代表任务已结束。 */ }
      return true;
    };
    es.addEventListener("end", async () => {
      es.close();
      if (ui.source !== es) return;
      if (ui.run.exit) { ui.loading = false; ui.historyReplaying = false; render(); settleSteers(ui.run); loadRuns(); return; }
      ui.streamDisconnected = true; ui.hint = "连接中断，正在恢复事件流；任务可能仍在执行，草稿已保留。"; syncComposer();
      if (await verify()) {
        if (knownDone && lastEnd === sequence) {
          ui.run.exit = "本轮已结束，但缺少完整退出记录"; ui.loading = false; render(); return;
        }
        lastEnd = sequence;
        setTimeout(() => { if (ui.source === es) connect(); }, delay);
        delay = Math.min(delay * 2, 30000);
      }
    });
    es.onerror = () => {
      if (ui.source !== es) return;
      ui.streamDisconnected = true;
      ui.hint = "连接中断，正在恢复事件流；任务可能仍在执行，草稿已保留。";
      syncComposer(); verify();
      // 原生 EventSource 重连携带 Last-Event-ID；服务端按同一游标补齐。
    };
  };
  connect();
  render();
  loadRuns();
}
export function stopReplay() {
  if (!ui.replay) return;
  clearTimeout(ui.replay.timer);
  ui.replay = null;
}
// 按真实时间间隔重放已结束的运行；折叠空闲时，单个间隔最多按 0.4 秒播放。
export function startReplay() {
  if (!ui.run || !ui.run.exit || ui.tab === "compare") return;
  const events = ui.events.slice(), speed = Number($("speed").value), foldIdle = $("fold").checked;
  Object.assign(ui, { run: newRun(ui.run.id), follow: true, zoom: null, replay: { i: 0 }, chatFollow: true });
  const step = () => {
    if (!ui.replay) return;
    const e = events[ui.replay.i++];
    apply(ui.run, e);
    if (ui.replay.i >= events.length) ui.replay = null;
    render();
    if (!ui.replay) return;
    let gap = Date.parse(events[ui.replay.i].time) - Date.parse(e.time);
    if (foldIdle) gap = Math.min(gap, 400 * speed);
    ui.replay.timer = setTimeout(step, Math.max(0, gap / speed));
  };
  step();
}

