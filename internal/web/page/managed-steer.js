import { $, api, el, stripHead, ui } from "./util.js";
import { loadRuns } from "./shell.js";

const labels = { received: "发送中", sending: "发送中", accepted: "做完这一步就会看到", deferred: "下一轮处理", unconsumed: "这一轮没用上，转到下一轮", unknown: "不确定有没有送达", blocked: "已被提交规则拦下" };
function action(label, fn, disabled, primary = false) { const b = el("button", primary ? "sa primary" : "sa", label); b.type = "button"; b.disabled = disabled; b.onclick = fn; return b; }
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
  const head = stripHead("steer", "插话", `${items.length} 条`);
  // 只有状态真的不确定时才要人核对。
  if (r.steerError || items.some((item) => item.state === "unknown")) head.append(action("核对状态", () => call(r, "/steer/refresh", {}), disabled));
  const pending = items.filter((item) => item.turn === r.steerTurnSeq && item.state !== "blocked");
  if (!r.exit && pending.length) head.append(action("停下并立即发送", () => call(r, "/stop", { steer_ids: pending.map((item) => item.id), steer_turn: r.steerTurnSeq }), disabled, true));
  const nodes = [head];
  if (r.steerError) nodes.push(el("div", "meta err", r.steerError));
  for (const item of items) {
    // 一行：原文在前（太长省略、悬停看全文），状态说明跟在后面；需要人处理的说明和按钮放在下面。
    const row = el("div", "managed-steer"), line = el("div", "msline"), text = el("span", "mstext", item.text || "");
    row.dataset.steerId = item.id; text.title = item.text || "";
    line.append(text, el("span", "meta", labels[item.state] || item.state));
    row.append(line);
    if (item.error) row.append(el("div", "meta", item.error));
    const acts = el("div", "msacts");
    if (item.state === "blocked") {
      row.append(el("div", "meta", "原文已从服务端待发区清除，不会自动重发。"));
      acts.append(action("知道了", () => call(r, "/steer/resolve", { id: item.id, action: "discard" }), disabled));
    } else if (item.state === "unknown" && (r.exit || item.turn !== r.steerTurnSeq)) {
      row.append(el("div", "meta", "请先核对对话记录；再次发送可能重复此前的操作。"));
      acts.append(action("已核对，转入暂停队列", () => call(r, "/steer/resolve", { id: item.id, action: "queue" }), disabled),
        action("移除这条待确认补充", () => call(r, "/steer/resolve", { id: item.id, action: "discard" }), disabled));
    }
    if (acts.children.length) row.append(acts);
    nodes.push(row);
  }
  return nodes;
}
