import { $, HOST, api, el, ui } from "./util.js";
import { loadRuns } from "./shell.js";

const dialog = $("queue-dialog"), field = $("queue-edit-text");
let editor = null, priorFocus;
// 每个标签页的编辑草稿独立；刷新沿用本页身份，不覆盖另一页尚未保存的修改。
const editorTab = (() => { try { let id = sessionStorage.getItem("tern-queue-editor-page"); if (!id) { id = crypto.randomUUID(); sessionStorage.setItem("tern-queue-editor-page", id); } return id; } catch { return crypto.randomUUID(); } })();
const prefix = () => `tern-queue-v1:${encodeURIComponent(ui.account || "")}:${encodeURIComponent(HOST)}:`;
const pendingKey = (id) => prefix() + id + ":pending";
const editKey = (run, item) => prefix() + run + ":edit:" + item + ":" + editorTab;
function pending(id) { try { return JSON.parse(localStorage.getItem(pendingKey(id)) || "null"); } catch { return null; } }
function changed() { document.dispatchEvent(new Event("tern:queue-changed")); }
function note(text) { ui.hint = text; $("hint").textContent = text; }
function action(text, fn, disabled = false) { const b = el("button", "", text); b.type = "button"; b.disabled = disabled; b.onclick = fn; return b; }
export const hasQueueStatus = (r) => !!r && !!(r.queue?.length || r.queueClaim || r.queueError || pending(r.id));

async function mutate(r, suffix, method, body) {
  if (ui.queueBusy) return false;
  ui.queueBusy = true; changed();
  try {
    const response = await fetch(api(`/runs/${encodeURIComponent(r.id)}/queue${suffix}`), { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), signal: AbortSignal.timeout(15000) });
    if (!response.ok) throw new Error((await response.text()).trim());
    note(""); return true;
  } catch (error) { note("队列操作未确认：" + error.message + "。请核对最新队列；编辑内容已保留。"); return false; }
  finally { ui.queueBusy = false; await loadRuns(); changed(); }
}
export function queueSection(r) {
  const nodes = [], queue = r.queue || [], waiting = pending(r.id);
  const disabled = !!ui.queueBusy || !!ui.streamDisconnected || !!ui.connectionLost || !!waiting;
  if (waiting) {
    const row = el("div", "head", "队列发送结果待确认");
    row.append(action("查询回执", () => checkSend(r.id), !!ui.queueBusy));
    if (waiting.notFound) row.append(action("用原请求重试", () => postSend(r.id, waiting), !!ui.queueBusy));
    nodes.push(row);
  }
  if (r.queueClaim) {
    const claim = r.queueClaim, managed = !!claim.steer_ids?.length;
    const row = el("div", "qitem", (managed ? "补充转交待确认：" : "已领取，正在核对启动结果：") + claim.text);
    row.append(action("核对领取状态", () => mutate(r, "/recover", "POST", {}), !!ui.queueBusy)); nodes.push(row);
    if (managed && r.exit) {
      const recovery = el("div", "qitem", "不会自动重发。请先恢复草稿并核对历史；释放领取项会移除这里的原文，队列仍保持暂停。");
      recovery.append(action("恢复到输入草稿", () => restoreClaim(r, claim), disabled),
        action("已核对，释放领取项", () => mutate(r, "/recover", "POST", { action: "release", id: claim.id, version: r.queueVersion }), disabled));
      nodes.push(recovery);
    }
  }
  if (r.queueError) nodes.push(el("div", "err", r.queueError));
  if (!queue.length) return nodes;
  const head = el("div", "head qhead", r.queuePaused ? `队列已暂停（${queue.length}），不会自动发送` : r.exit ? `等待发送（${queue.length}）` : `这一轮结束后发（${queue.length}）`);
  if (r.exit && !r.queueClaim) head.append(action("发第一条", () => send(r, false), disabled), action("继续全部", () => send(r, true), disabled));
  nodes.push(head);
  queue.forEach((item, index) => {
    const row = el("div", "qitem"), text = el("span", "qtext", (item.steer_ids?.length ? "补充转入下一轮：\n" : "") + item.text), buttons = el("div", "qactions");
    row.dataset.queueId = item.id;
    buttons.append(action("编辑", () => edit(r, item), disabled), action("上移", () => move(r, index, -1), disabled || index === 0), action("下移", () => move(r, index, 1), disabled || index === queue.length - 1), action("删除", () => mutate(r, `/${encodeURIComponent(item.id)}`, "DELETE", { version: r.queueVersion }), disabled));
    for (const b of buttons.children) b.setAttribute("aria-label", `${b.textContent}第 ${index+1} 条排队消息`);
    row.append(text, buttons); nodes.push(row);
  });
  return nodes;
}
function restoreClaim(r, claim) {
  if (ui.run?.id !== r.id || ui.loading || ui.historyReplaying) return;
  const input = $("query"), text = claim.text || "";
  if (!text) return;
  if (!input.value.includes(text)) input.value = input.value.trim() ? input.value + "\n\n" + text : text;
  input.dispatchEvent(new Event("input", { bubbles: true })); input.focus();
  note("原文已恢复到输入草稿，尚未发送。核对历史后可释放领取项，再决定是否用新请求发送。");
}
function move(r, index, delta) {
  const ids = r.queue.map((item) => item.id);
  [ids[index], ids[index+delta]] = [ids[index+delta], ids[index]];
  return mutate(r, "", "PUT", { ids, version: r.queueVersion });
}
function edit(r, item) {
  priorFocus = document.activeElement;
  let saved;
  try { saved = JSON.parse(localStorage.getItem(editKey(r.id, item.id)) || "null"); } catch {}
  editor = { run: r.id, id: item.id, version: saved?.version ?? r.queueVersion };
  field.value = saved?.text ?? item.text;
  $("queue-edit-note").textContent = "";
  dialog.showModal(); refreshQueueEditor(); field.focus();
}
field.oninput = () => {
  if (!editor) return;
  try { localStorage.setItem(editKey(editor.run, editor.id), JSON.stringify({ version: editor.version, text: field.value })); }
  catch { $("queue-edit-note").textContent = "编辑草稿未能保存到浏览器，离开前请复制；仍可尝试保存到队列。"; }
};
export function refreshQueueEditor() {
  if (!editor || !dialog.open) return;
  const r = ui.run?.id === editor.run ? ui.run : null, item = r?.queue?.find((q) => q.id === editor.id);
  $("queue-edit-save").disabled = !item || !!ui.queueBusy || !!ui.connectionLost || !!ui.streamDisconnected;
  const stale = !!item && r.queueVersion !== editor.version;
  $("queue-edit-rebase").hidden = !stale;
  $("queue-edit-rebase").disabled = !!ui.queueBusy;
  $("queue-edit-latest").hidden = !stale;
  if (!item) $("queue-edit-note").textContent = "这一项已不在待发队列中（可能被领取或删除），你的编辑仍保留。";
  else if (stale) {
    $("queue-edit-note").textContent = "队列已变化。下方是服务器当前文字；你的编辑不会被覆盖。核对后再使用最新版本保存。";
    $("queue-edit-latest").textContent = item.text;
  }
}
$("queue-edit-rebase").onclick = () => {
  if (!editor || ui.run?.id !== editor.run) return;
  editor.version = ui.run.queueVersion; field.oninput(); $("queue-edit-note").textContent = "已使用最新版本，点击保存才会修改队列。"; refreshQueueEditor();
};
$("queue-edit-save").onclick = async () => {
  const r = ui.run, current = editor;
  if (!current || r?.id !== current.run || !field.value.trim()) return;
  field.oninput();
  const text = field.value;
  if (await mutate(r, `/${encodeURIComponent(current.id)}`, "PUT", { text, version: current.version })) {
    if (editor !== current || field.value !== text) return;
    try { localStorage.removeItem(editKey(current.run, current.id)); } catch {}
    dialog.close();
  } else $("queue-edit-note").textContent = ui.hint;
};
$("queue-edit-close").onclick = () => { field.oninput(); dialog.close(); };
dialog.addEventListener("close", () => { editor = null; if (priorFocus?.isConnected) priorFocus.focus(); else $("query").focus(); });

function send(r, all) {
  if (pending(r.id)) return checkSend(r.id);
  const request = { id: r.queue[0].id, version: r.queueVersion, all, request_id: crypto.randomUUID() };
  try { localStorage.setItem(pendingKey(r.id), JSON.stringify(request)); }
  catch { note("无法保存队列发送回执，本次未发送。请检查浏览器存储。 "); return; }
  postSend(r.id, request);
}
async function postSend(id, request) {
  if (ui.queueBusy) return;
  ui.queueBusy = true; changed();
  try {
    const { notFound, ...body } = request; // 查询提示不是请求参数；重试必须保持原始负载。
    const response = await fetch(api(`/runs/${encodeURIComponent(id)}/queue/send`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), signal: AbortSignal.timeout(15000) });
    if (!response.ok) throw new Error((await response.text()).trim());
    localStorage.removeItem(pendingKey(id)); note("");
  } catch (error) { note("队列发送结果待确认：" + error.message); await checkSend(id); }
  finally { ui.queueBusy = false; await loadRuns(); changed(); }
}
async function checkSend(id) {
  const request = pending(id); if (!request) return;
  try {
    const response = await fetch(api(`/requests/${request.request_id}`), { signal: AbortSignal.timeout(8000) });
    if (!response.ok) throw new Error("回执查询失败");
    const receipt = await response.json();
    if (receipt.state === "accepted" || receipt.state === "rejected") {
      localStorage.removeItem(pendingKey(id)); note(receipt.state === "rejected" ? receipt.error?.message || "发送未接受，队列保持原处" : "队列发送已接收");
    } else { localStorage.setItem(pendingKey(id), JSON.stringify({ ...request, notFound: receipt.state === "not_found" })); note("尚未确认发送结果，不会自动重复发送。"); }
  } catch (error) { note("无法确认队列发送结果：" + error.message); }
  await loadRuns(); changed();
}
window.addEventListener("storage", (e) => { if (e.key?.startsWith(prefix()) && e.key.endsWith(":pending")) changed(); });
