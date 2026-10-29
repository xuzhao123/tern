import { $, api, el, ui } from "./util.js";
import { loadRuns } from "./shell.js";

const labels = { received: "服务端已接收，等待送达", sending: "正在送达", accepted: "已送达，等待采纳", deferred: "将安排到下一轮", unconsumed: "未被本轮采纳，正在转交", unknown: "结果待确认", blocked: "已被提交规则拦下" };
function action(label, fn, disabled) { const b = el("button", "ghost", label); b.type = "button"; b.disabled = disabled; b.onclick = fn; return b; }
function changed() { document.dispatchEvent(new Event("tern:managed-steer-changed")); }
async function call(r, path, body) {
  if (ui.managedSteerBusy) return;
  ui.managedSteerBusy = true; changed();
  try {
    const response = await fetch(api(`/runs/${encodeURIComponent(r.id)}${path}`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), signal: AbortSignal.timeout(15000) });
    if (!response.ok) throw new Error((await response.text()).trim());
    ui.hint = "";
  } catch (error) { ui.hint = "补充状态尚未确认：" + error.message + "。请核对状态，不会由网页重复发送。"; }
  finally { ui.managedSteerBusy = false; $("hint").textContent = ui.hint; await loadRuns(); changed(); }
}
export function managedSteerSection(r) {
  const items = r.managedSteers || [];
  if (!items.length && !r.steerError) return [];
  const disabled = !!ui.managedSteerBusy || !!ui.streamDisconnected || !!ui.connectionLost || !!ui.historyReplaying;
  const head = el("div", "head", `当前轮补充（${items.length}）`);
  head.append(action("核对状态", () => call(r, "/steer/refresh", {}), disabled));
  const pending = items.filter((item) => item.turn === r.steerTurnSeq && item.state !== "blocked");
  if (!r.exit && pending.length) head.append(action("停止并发送未采纳内容", () => call(r, "/stop", { steer_ids: pending.map((item) => item.id), steer_turn: r.steerTurnSeq }), disabled));
  const nodes = [head];
  if (r.steerError) nodes.push(el("div", "meta err", r.steerError));
  for (const item of items) {
    const row = el("div", "managed-steer"); row.dataset.steerId = item.id;
    row.append(el("div", "meta", labels[item.state] || item.state));
    if (item.text) row.append(el("div", "item", item.text));
    if (item.error) row.append(el("div", "meta", item.error));
    if (item.state === "blocked") {
      row.append(el("div", "meta", "原文已从服务端待发区清除，不会自动重发。"), action("知道了", () => call(r, "/steer/resolve", { id: item.id, action: "discard" }), disabled));
    } else if (item.state === "unknown" && (r.exit || item.turn !== r.steerTurnSeq)) {
      row.append(el("div", "meta", "请先核对对话记录；再次发送可能重复此前的操作。"),
        action("已核对，转入暂停队列", () => call(r, "/steer/resolve", { id: item.id, action: "queue" }), disabled),
        action("移除这条待确认补充", () => call(r, "/steer/resolve", { id: item.id, action: "discard" }), disabled));
    }
    nodes.push(row);
  }
  return nodes;
}
