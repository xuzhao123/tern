import { selectedSkills, restoreSkills } from "./skillrefs.js";
import { renderSlash, runSlash, slashKey } from "./slash.js";
import { managedSteerSection } from "./managed-steer.js";
import { queueSection, hasQueueStatus, refreshQueueEditor } from "./queue.js";
import { show } from "./follow.js";
import { openSettings } from "./pages.js";
import { autosize, loadRuns, loadWsSettings, modelVision, syncComposer } from "./shell.js";
import { $, FULL_ACCESS_NOTE, HOST, MAX_IMAGES, api, el, permChoice, refURL, stripHead, ui } from "./util.js";
import { attachToDraft, captureDraft, currentDraftKey, finishDelivery, pendingDelivery, rememberDelivery, saveDraft, savePendingSteers } from "./drafts.js";
// 插话、排队、! 命令被服务端接受后显示在输入框下面的提示；runs.js 在这一轮结束时据此清掉已经过时的两条。
export const HINTS = { queue: "已排到下一轮", steer: "补充已由服务端接收，采纳结果见待发区", shell: "命令已执行，结果见命令记录" };
export async function start(url, body, kind = "message") {
  if (ui.sending) return;
  if (pendingDelivery()) { await checkDelivery(); return; }
  const token = captureDraft();
  if (!token) return;
  const id = crypto.randomUUID();
  const request = { url, kind, body: { ...body, request_id: id, ...(kind === "steer" ? { client_id: id } : {}) } };
  if (!rememberDelivery(token, request)) return;
  ui.sending = true; syncComposer();
  try {
    const res = await fetch(api(url), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(request.body), signal: AbortSignal.timeout(15000) });
    if (!res.ok) {
      // 失败（比如服务端说这个模型不能看图）时保留已附的图：换一下模型就能再发。
      const text = await res.text();
      if (currentDraftKey() === token.key && attached.length) { ui.attachError = text; renderAttached(); }
      await checkDelivery({ ...request, token }, text); return;
    }
    acceptDelivery(token, await res.json(), kind);
  } catch (err) {
    await checkDelivery({ ...request, token }, "连接中断：" + err.message);
  } finally {
    ui.sending = false; syncComposer(); autosize();
  }
}
function acceptDelivery(token, response, kind = "message") {
  const same = currentDraftKey() === token.key;
  finishDelivery(token, true);
  if (kind === "queue" || kind === "steer" || kind === "shell") { if (same) ui.hint = HINTS[kind]; loadRuns(); }
  else if (same) { ui.attachError = ""; ui.page = "chat"; show(response.id); }
  else loadRuns();
  renderDelivery();
}
async function checkDelivery(request = pendingDelivery(), failure = "") {
  if (!request) return;
  const same = () => currentDraftKey() === request.token.key;
  try {
    const response = await fetch(api(`/requests/${encodeURIComponent(request.body.request_id)}`), { signal: AbortSignal.timeout(8000) });
    if (!response.ok) throw new Error("回执查询不可用");
    const receipt = await response.json();
    if (receipt.state === "accepted") return acceptDelivery(request.token, receipt.response, request.kind);
    if (receipt.state === "rejected") {
      finishDelivery(request.token, false);
      if (same()) ui.hint = "未发送：" + (receipt.error?.message || failure);
    } else if (same()) ui.hint = receipt.state === "not_found" ? "尚未查到发送回执，草稿已保留。可使用原请求重试。" : "请求已接收，结果仍待确认；不会重复发送。";
    request.notFound = receipt.state === "not_found";
    rememberDelivery(request.token, request);
    if (receipt.state === "rejected") finishDelivery(request.token, false);
  } catch { if (same()) ui.hint = (failure ? failure + "。" : "") + "发送结果未知，草稿已保留，请查询回执。"; }
  if (same()) { renderDelivery(); syncComposer(); }
}
function renderDelivery() {
  const box = $("delivery-notice"), request = pendingDelivery();
  box.hidden = !request; box.replaceChildren();
  if (!request) return;
  box.append(document.createTextNode("有一条发送结果待确认。"));
  const check = el("button", "link", "查询回执"); check.type = "button"; check.onclick = () => checkDelivery(); box.append(check);
  if (request.notFound) {
    const retry = el("button", "link", "用原请求重试"); retry.type = "button";
    retry.onclick = async () => {
      if (ui.sending) return;
      ui.sending = true; syncComposer();
      try {
        const res = await fetch(api(request.url), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(request.body), signal: AbortSignal.timeout(15000) });
        if (res.ok) acceptDelivery(request.token, await res.json(), request.kind); else await checkDelivery(request);
      } catch { await checkDelivery(request); }
      finally { ui.sending = false; syncComposer(); }
    };
    box.append(retry);
  }
}
document.addEventListener("tern:draft-changed", renderDelivery);
// ---------- 附图 ----------
// 图先 POST /attachments 换成附件引用，发送时只带引用；缩略图显示 /attachments/<名字> 的原图，缩放由 agent 做。
let attached = [];
export function readDraftInput() {
  const picks = {};
  for (const [key, id] of [["provider", "provider"], ["model", "model"], ["permission", "perm"], ["effort", "effort"]]) if (ui.picked?.[key]) picks[key] = $(id).value;
  return { skillRefs: selectedSkills(), compact: !!ui.compactNext, text: $("query").value, images: attached.map(({ ref, name }) => ({ ref, name })), picks };
}
export function writeDraftInput(value) {
  ui.compactNext = value.compact === true;
  restoreSkills(value.skillRefs);
  $("query").value = typeof value.text === "string" ? value.text : "";
  attached = Array.isArray(value.images) ? value.images.filter((i) => typeof i?.ref === "string" && typeof i?.name === "string") : [];
  ui.picked = { workspace: ui.picked?.workspace };
  if (value.picks?.provider) { ui.picked.provider = true; $("provider").value = value.picks.provider; syncComposer(); }
  for (const [key, id] of [["provider", "provider"], ["model", "model"], ["permission", "perm"], ["effort", "effort"]]) if (value.picks?.[key]) { ui.picked[key] = true; $(id).value = value.picks[key]; }
  ui.attachError = ""; renderAttached(); autosize();
}
function renderAttached() {
  const strip = $("attach-strip"), pics = attached.map((img) => {
    const wrap = el("div", "pic"), thumb = el("img");
    thumb.src = refURL(img.ref);
    thumb.alt = img.name;
    thumb.title = img.name;
    thumb.onerror = () => { ui.attachError = `附件 ${img.name} 无法读取，请移除或重新上传`; renderAttachmentError(); };
    const del = el("button", "del", "×");
    del.type = "button";
    del.title = "不附这张图";
    del.onclick = () => { attached = attached.filter((x) => x !== img); ui.attachError = ""; renderAttached(); saveDraft(); };
    wrap.append(thumb, del);
    return wrap;
  });
  if (ui.attachError) pics.push(el("div", "err", ui.attachError));
  strip.replaceChildren(...pics);
  strip.hidden = !pics.length;
  syncComposer();
}
function renderAttachmentError() {
  let note = $("attach-strip").querySelector(".attachment-error");
  if (!note) { note = el("div", "err attachment-error"); $("attach-strip").append(note); }
  note.textContent = ui.attachError;
}
// 上传失败的图不进列表，服务端说的话（只支持 PNG…、超过 20 MB…）原样显示在输入框里。
async function addImages(files) {
  const token = captureDraft();
  if (!token) return;
  ui.attachError = "";
  for (const file of files) {
    if (attached.length >= MAX_IMAGES) { ui.attachError = `每条消息最多 ${MAX_IMAGES} 张图`; break; }
    try {
      const res = await fetch(api("/attachments?name=" + encodeURIComponent(file.name || "image")), { method: "POST", body: file });
      if (!res.ok) { ui.attachError = await res.text(); continue; }
      const image = await res.json();
      if (!attachToDraft(token, image)) attached.push(image);
    } catch (err) { ui.attachError = "上传失败：" + err.message; }
  }
  renderAttached();
  saveDraft();
}
$("attach").onclick = () => { if (!$("attach").disabled) $("attach-file").click(); };
$("attach-file").onchange = () => {
  const files = [...$("attach-file").files];
  $("attach-file").value = "";
  if (files.length) addImages(files);
};
// 粘贴：剪贴板里的图片文件（截图最常见）；没有图片时照常粘贴文字。
$("query").addEventListener("paste", (ev) => {
  const files = [...(ev.clipboardData?.files || [])].filter((f) => f.type.startsWith("image/"));
  if (!files.length) return;
  ev.preventDefault();
  addImages(files);
});
// 拖放：落在输入区就当附图，不让浏览器直接打开这个文件。
const dropZone = $("composer-wrap");
dropZone.addEventListener("dragover", (ev) => { if ([...(ev.dataTransfer?.types || [])].includes("Files")) { ev.preventDefault(); dropZone.classList.add("drag"); } });
dropZone.addEventListener("dragleave", () => dropZone.classList.remove("drag"));
dropZone.addEventListener("drop", (ev) => {
  const files = [...(ev.dataTransfer?.files || [])].filter((f) => f.type.startsWith("image/"));
  if (!files.length) return;
  ev.preventDefault();
  dropZone.classList.remove("drag");
  addImages(files);
});
$("composer").onsubmit = (ev) => {
  ev.preventDefault();
  if (ui.sending || ui.streamDisconnected || !ui.account) return;
  const query = $("query").value.trim();
  // “/名字 参数”是界面命令时在页面里执行，不发给模型（运行中也一样，能不能用由命令自己判断）。
  if (query.startsWith("/") && runSlash(query)) return;
  // 以 ! 开头是人在沙箱里跑一条命令（不进模型，见 runShell）：新对话要先开一个，运行中要等它结束。
  if (query.startsWith("!")) { runShell(query.slice(1).trim()); return; }
  // 运行中按回车是插话：排进 agent 的下一次模型请求，不打断正在进行的请求或工具。
  if ($("send").classList.contains("stop")) { if (query) steer(query); return; }
  if ($("send").disabled || !query) return;
  // 附了图又换到不能看图的模型：说明原因、保留图，换回能看图的模型就能发。
  const vis = ui.run ? modelVision(ui.run.provider || "ark", ui.run.model || "") : modelVision($("provider").value, $("model").value);
  if (attached.length && !vis.vision) {
    ui.attachError = `当前模型 ${vis.name} 不能看图，换一个能看图的模型再发送`;
    renderAttached();
    return;
  }
  ui.chatFollow = true;
  const images = attached.map(({ ref, name }) => ({ ref, name }));
  const perm = permChoice($("perm").value);
  const body = ui.run ? { query, compact: !!ui.compactNext, permission: perm.permission, sandbox: perm.sandbox, effort: $("effort").value } : { query, provider: $("provider").value, model: $("model").hidden ? "" : $("model").value, workspace: $("workspace").disabled ? "" : $("workspace").value, permission: perm.permission, sandbox: perm.sandbox, effort: $("effort").value };
  body.skill_refs = selectedSkills();
  start(ui.run ? `/runs/${ui.run.id}/messages` : "/runs", images.length ? { ...body, images } : body);
};
// ! 命令：在这条对话的沙箱里跑一条（POST /runs/<id>/shell），和模型的 bash 同一个沙箱；卡片出现在对话里，
// 下一轮的问题里也会带上命令与输出（app-server 的 notes）。这时没有 agent 在跑，联网只认这条对话白名单里的域名。
// 运行中不跑（也不插话）：这一轮的 agent 还在跑，跑的命令会和它抢沙箱、也没人当场批准联网。
async function runShell(command) {
  const r = ui.run, box = $("query");
  if (!r) { $("hint").textContent = "先发一句话开个对话，再用 ! 跑命令"; return; }
  if (!r.exit) { $("hint").textContent = "这一轮在跑，等它结束再跑命令"; return; }
  if (!command) { $("hint").textContent = "! 后面写要跑的命令"; return; }
  if (ui.shellBusy) return; // 上一条还没跑完：不排队，连按两次也不跑两遍
  if (attached.length || selectedSkills().length || ui.compactNext) { ui.hint="命令不发送附件、Skill或压缩选项。请保留这些内容在正常消息中发送。";syncComposer();return; }
  ui.shellBusy=true;
  try { await start(`/runs/${encodeURIComponent(r.id)}/shell`,{command,...permChoice($("perm").value)},"shell"); }
  finally { ui.shellBusy=false;syncComposer(); }

}
document.addEventListener("tern:enable-bash",async()=>{
 const r=ui.run;if(!r?.exit||ui.enablingBash)return;ui.enablingBash=true;syncComposer();
 try{const res=await fetch(api(`/runs/${encodeURIComponent(r.id)}/enable-bash`),{method:"POST"});if(!res.ok)throw new Error(await res.text());if(ui.run!==r)return;r.bashEnabled=true;ui.hint="本任务已启用命令；后续消息会带上命令工具。执行位置仍按权限选择。";await loadRuns();}
 catch(error){if(ui.run===r)ui.hint="命令未启用："+error.message;}
 finally{ui.enablingBash=false;syncComposer();}
});
async function steer(text) {
  if (selectedSkills().length) { ui.hint = "已选择 Skill，请等本轮结束后正常发送；引用仍在草稿中。"; $("hint").textContent = ui.hint; return; }
  if (ui.compactNext) { ui.hint = "已准备压缩，请等本轮结束后发送，或在上下文面板取消压缩。"; $("hint").textContent = ui.hint; return; }
  const current = ui.run;
  if (!current || ui.sending) return;
  if (attached.length) { ui.hint = "当前轮补充暂只支持文字，请保留附图在下一轮正常发送。"; syncComposer(); return; }
  if (current.steerManaged === undefined) { ui.hint = "正在确认服务端的补充能力，请稍后再发送。"; syncComposer(); return; }
  if (current.steerManaged) return start(`/runs/${encodeURIComponent(current.id)}/steer`, { text, managed: true, turn_seq: current.steerTurnSeq }, "steer");
  const r = ui.run, item = { id: crypto.randomUUID(), text };
  const q = (ui.steers[r.id] ??= { items: [], immediate: false });
  q.items.push(item);
  if (!savePendingSteers(r.id, q)) { q.items.pop(); return; }
  $("query").value = "";
  saveDraft();
  autosize(); syncComposer();
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/steer`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text, client_id: item.id }) });
    // 409：这一轮刚好结束，留在待发送区，等这一轮结束（exit）后按顺序接着发；已经结束了就直接发。
    if (res.status === 409) { if (ui.run === r && r.exit) settleSteers(r); return; }
    if (!res.ok) throw new Error(await res.text());
  } catch (err) {
    q.items = q.items.filter((x) => x !== item);
    savePendingSteers(r.id, q);
    backToInput(text);
    $("hint").textContent = "没发出去：" + err.message;
  }
}
// ---------- 待发送 ----------
// 运行中发出去的话先排在这里，不要等 agent 取走（下一次请求模型时才取，中间可能正跑着一条 bash）。
// agent 取走时发回带同一个 client_id 的 userMessage，runs.js 据此把它从这里挪进对话；
// 没赶上这一轮的，这一轮结束（exit）时由 runs.js 交给 settleSteers 接着发。
// 另一节是服务端队列（thread/queue/add，“排队”按钮）：排到这一轮结束后由 app-server 接着发，所有页面看得到。
// Tab 是焦点导航（补全菜单打开时选候选），不排队。
export function renderSteers() {
  const strip = $("steer-strip"), items = (ui.run && ui.steers[ui.run.id]?.items) || [];
  const queue = (ui.run && ui.run.queue) || [];
  refreshQueueEditor();
  // 流式渲染会一遍遍调到这里：内容没变就不重建，免得正要点“立即发送”时按钮被换掉。
  // 停下与否也进签名：这一轮结束时队列那一节要从“这一轮结束后发”换成“已停止…”。
  const restored = ui.run && ui.steers[ui.run.id]?.restored;
  const sig = [JSON.stringify(ui.run?.managedSteers || []), ui.run?.steerError, ui.managedSteerBusy, ui.run?.queueVersion, ui.run?.queuePaused, ui.run?.queueClaim?.id, ui.run?.queueError, ui.queueBusy, ui.queueRevision, ui.run?.id, ui.run?.exit ? "stop" : "", !!restored, ui.historyReplaying, ui.streamDisconnected, items.map((it) => it.id + "\n" + it.text).join("\u0000"), queue.map((q) => q.id + "\n" + q.text).join("\u0000")].join("\u0000");
  if (strip.dataset.sig === sig) return;
  strip.dataset.sig = sig;
  const nodes = [];
  if (ui.run?.steerManaged) nodes.push(...managedSteerSection(ui.run));
  // 插话每条一行，太长省略，悬停看全文。
  const line = (it) => { const d = el("div", "item", it.text); d.title = it.text; return d; };
  if (items.length && ui.run?.steerManaged) {
    const head = stripHead("steer", "插话", "旧版待确认补充，请先核对历史");
    const restore = el("button", "sa", "恢复为输入草稿"); restore.type = "button";
    restore.onclick = () => { const text = items.map((item) => item.text).join("\n\n"); ui.steers[ui.run.id] = { items: [], immediate: false }; savePendingSteers(ui.run.id, ui.steers[ui.run.id]); backToInput(text); renderSteers(); };
    head.append(restore); nodes.push(head, ...items.map(line));
  } else if (items.length) {
    const head = stripHead("steer", "插话", restored ? "上次的补充尚未确认采纳，请先核对对话记录" : "做完这一步后发给 agent"), now = el("button", "sa primary", "立即发送");
    now.type = "button";
    now.disabled = !!ui.historyReplaying || !!ui.streamDisconnected;
    if (restored) now.textContent = "核对后重新发送";
    now.title = "停下正在做的这一步，马上发出去";
    now.onclick = () => sendSteersNow();
    head.append(now);
    nodes.push(head, ...items.map(line));
  }
  if (hasQueueStatus(ui.run)) nodes.push(...queueSection(ui.run));
  strip.hidden = !nodes.length;
  strip.replaceChildren(...nodes);
}
async function queueInput() {
  if (selectedSkills().length) { ui.hint = "已选择 Skill，请等本轮结束后正常发送；引用仍在草稿中。"; $("hint").textContent = ui.hint; return; }
  if (ui.compactNext) { ui.hint = "已准备压缩，请等本轮结束后发送，或在上下文面板取消压缩。"; $("hint").textContent = ui.hint; return; }
  const r = ui.run, text = $("query").value.trim();
  if (!r || !text || ui.sending) return;
  if (attached.length) { ui.hint = "队列当前只支持文字，请先移除附图或在本轮结束后正常发送。"; syncComposer(); return; }
  await start(`/runs/${encodeURIComponent(r.id)}/queue`, { text }, "queue");
}
document.addEventListener("tern:managed-steer-changed", () => { $("steer-strip").dataset.sig = ""; renderSteers(); });
document.addEventListener("tern:queue-changed", () => { ui.queueRevision = (ui.queueRevision || 0) + 1; renderSteers(); });
// 没送到、或者人点了停止时没发出去的，按顺序放回输入框，人自己决定发不发。
export function backToInput(text) {
  const box = $("query");
  box.value = box.value.trim() ? box.value.replace(/\s+$/, "") + "\n\n" + text : text;
  autosize(); syncComposer(); saveDraft();
}
async function stopRun() {
  if (ui.streamDisconnected) return;
  const r = ui.run;
  if (!r) return;
  if (r.stopRequested && !confirm("agent 正在停止（回填结果、保存检查点）。确定强制结束？未保存的进度会丢失。")) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/stop`), { method: "POST" });
    if (!res.ok && res.status !== 409) alert(await res.text());
  } catch (err) { alert("停止失败：" + err.message); }
}
// 立即发送：和点停止一样先停下这一轮，等它结束（exit）时再作为下一条消息发（见 settleSteers）。
async function sendSteersNow() {
  const r = ui.run;
  if (!r || !ui.steers[r.id]?.items.length) return;
  ui.steers[r.id].immediate = true;
  ui.steers[r.id].restored = false;
  savePendingSteers(r.id, ui.steers[r.id]);
  renderSteers();
  if (r.exit) { settleSteers(r); return; }
  await stopRun();
}
// 这一轮结束时（runs.js 收到 exit）处理剩下的：按顺序用空行拼成一条接着发；
// 人点了停止按钮（不是“立即发送”）的，放回输入框由人决定。
export function settleSteers(r) {
  if (r.steerManaged !== false) return; // 新模式与能力尚未确认时都不由浏览器补发。
  const q = ui.steers[r.id];
  if (ui.historyReplaying || q?.restored) return;
  if (!q?.items.length) return;
  const text = q.items.map((it) => it.text).join("\n\n"), now = q.immediate;
  q.items = []; q.immediate = false;
  savePendingSteers(r.id, q);
  renderSteers();
  if (!now && r.stopRequested) return backToInput(text);
  sendMessage(r, text);
}
// 自动接着发：和人在输入框里发一样走 /runs/<id>/messages，成功时不动输入框里已经打好的字与附的图；
// 没发出去就把这句话放回输入框，别丢。
async function sendMessage(r, query) {
  ui.chatFollow = true;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/messages`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query, permission: $("perm").value, effort: $("effort").value }) });
    if (!res.ok) { backToInput(query); $("hint").textContent = "没发出去：" + await res.text(); return; }
    ui.page = "chat";
    show((await res.json()).id);
  } catch (err) { backToInput(query); $("hint").textContent = "没发出去：" + err.message; }
}
$("send").onclick = async (ev) => {
  if (!$("send").classList.contains("stop")) return; // 发送仍走表单提交
  ev.preventDefault();
  await stopRun();
};
$("provider").onchange = () => { ui.picked.provider = true; ui.picked.model = false; syncComposer(); };
$("model").onchange = () => { ui.picked.model = true; syncComposer(); };
$("workspace").onchange = () => { ui.picked.workspace = true; syncComposer(); loadWsSettings($("workspace").value, true); };
// 权限下拉：选「完全访问」要确认一次，取消就退回上一档；确认后每轮请求带 sandbox=host。
let permPrev = $("perm").value;
$("perm").onchange = () => {
  if ($("perm").value === "unrestricted" && !confirm(FULL_ACCESS_NOTE + (HOST ? `\n\n目标主机：${HOST}` : ""))) {
    $("perm").value = permPrev;
    return;
  }
  permPrev = $("perm").value;
  ui.picked.permission = true;
  syncComposer();
};
$("effort").onchange = () => { ui.picked.effort = true; syncComposer(); };
$("settings").onclick = () => openSettings();
// 输入法组合输入时的 Enter 用于选词，不能发送；@ 菜单开着时回车是插入，也不发送。
// Tab 保留焦点导航（补全菜单打开时可选候选）；排队使用显式按钮。
$("query").onkeydown = (ev) => {
  if (atKey(ev) || slashKey(ev)) { ev.preventDefault(); return; }
  if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) { ev.preventDefault(); $("composer").requestSubmit(); }
};
$("queue").onclick = () => queueInput();
$("query").oninput = () => { autosize(); syncComposer(); if (!syncAt()) renderSlash(); };
// 斜杠命令的菜单与执行在 slash.js：界面命令在页面里执行，提示词命令照旧整句发给 agent 展开。
$("query").onblur = () => { atClose(); $("slash").hidden = true; };
$("workspace").addEventListener("change", () => atClose());
// ---------- @ 提到文件 ----------
// 光标前是“行首或空白 + @ + 若干非空白字符”时，在斜杠命令那个框里列出工作区的文件（app-server 的 workspace/files）：
// 新对话按工作区下拉当前选的，已有对话按这条对话的（threadId，用它快照里的工作区目录）。没有工作区就不弹菜单，@ 是普通字符。
// 敲字时请求做 150 毫秒去抖，seq 保证只用最后一次的结果：先发后到的旧结果不覆盖新的。
let atMatch = null; // 光标前那段 @…（{start, q}）：有它就说明这个框归 @ 菜单
let atItems = [];   // 最后一次结果里的路径
let atPick = 0;     // 上下键选中的第几项
let atTimer = 0, atSeq = 0;

function syncAt() {
  const box = $("query"), m = /(^|\s)@(\S*)$/.exec(box.value.slice(0, box.selectionStart));
  const ws = ui.run ? ui.run.workspace || "" : $("workspace").value;
  if (!m || !ws) { atClose(); return false; }
  // 从斜杠菜单接管这个框：先藏起来，结果到了再显示（已经在用 @ 菜单时留着旧结果，别一闪一闪）。
  if (!atMatch) $("slash").hidden = true;
  const at = { start: box.selectionStart - m[2].length - 1, q: m[2] };
  atMatch = at;
  const seq = ++atSeq;
  clearTimeout(atTimer);
  atTimer = setTimeout(() => loadAt(at, ws, seq), 150);
  return true;
}

// 关掉 @ 菜单：框里的内容可能已经归斜杠命令，只有 @ 菜单开着时才把它藏起来。
function atClose() {
  if (atMatch) $("slash").hidden = true;
  atMatch = null; atItems = []; atPick = 0;
  clearTimeout(atTimer);
  atSeq++;
}

async function loadAt(at, ws, seq) {
  const qs = new URLSearchParams({ q: at.q });
  if (ui.run) qs.set("threadId", ui.run.id); else qs.set("workspace", ws);
  let files = [], note = "";
  try {
    const res = await fetch(api("/workspaces/files?" + qs));
    if (res.ok) files = (await res.json()).files || []; else note = await res.text();
  } catch (err) { note = err.message; }
  if (seq !== atSeq) return; // 后面又敲了字：这份结果不要了
  atItems = files; atPick = 0;
  renderAt(note);
}

// 每项显示文件名，后面淡色的目录；点一下插入。
function renderAt(note) {
  const box = $("slash");
  if (note || !atItems.length) {
    box.replaceChildren(el("div", "meta", note || "没有匹配的文件"));
    box.hidden = false;
    return;
  }
  box.replaceChildren(...atItems.map((path, i) => {
    const b = el("button", "slash-item"), cut = path.lastIndexOf("/");
    b.type = "button";
    const name = el("span", "slash-name", cut < 0 ? path : path.slice(cut + 1));
    if (cut >= 0) name.append(el("span", "dir", path.slice(0, cut)));
    b.append(name);
    b.classList.toggle("on", i === atPick);
    b.onmousedown = (ev) => ev.preventDefault();
    b.onclick = () => insertAt(path);
    return b;
  }));
  box.hidden = false;
}

// 上下键选，回车或 Tab 插入，Esc 关掉（@ 留在输入框里）。菜单开着时回车不发送消息：
// 结果还没回来（敲完马上按回车）就先关掉菜单，再按一次才是发送。
function atKey(ev) {
  if (ev.isComposing || !atMatch) return false;
  if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    if (!atItems.length) return false;
    atPick = (atPick + (ev.key === "ArrowDown" ? 1 : atItems.length - 1)) % atItems.length;
    for (const [i, b] of [...$("slash").querySelectorAll(".slash-item")].entries()) b.classList.toggle("on", i === atPick);
    $("slash").children[atPick]?.scrollIntoView({ block: "nearest" });
    return true;
  }
  if (ev.key === "Enter" || ev.key === "Tab") {
    if (atItems.length) insertAt(atItems[atPick]); else atClose();
    return true;
  }
  if (ev.key === "Escape") { atClose(); return true; }
  return false;
}

// Esc 打断（同 Codex）：这一轮正在跑、没有对话框和菜单挡着时，和点停止一样；
// 待发送区里有插话时和“立即发送”一样——停下这一轮，插话作为下一条消息发出去。
// 输入法组合中的 Esc 是选词；菜单或别的输入框自己处理过 Esc（已 preventDefault）的也不算。
document.addEventListener("keydown", (ev) => {
  if (ev.key !== "Escape" || ev.isComposing || ev.defaultPrevented) return;
  if (ui.page !== "chat" || !$("send").classList.contains("stop")) return;
  if (document.querySelector("dialog[open]") || !$("slash").hidden || atMatch) return;
  ev.preventDefault();
  const r = ui.run;
  if (r && ui.steers[r.id]?.items.length) sendSteersNow(); else stopRun();
});

// 插入：把 @ 加后面敲的字换成路径，后面跟一个空格；路径里有空格时用反引号括起来。
function insertAt(path) {
  const box = $("query"), at = atMatch;
  if (!at) return;
  const text = /\s/.test(path) ? "`" + path + "`" : path, caret = at.start + text.length + 1;
  box.value = box.value.slice(0, at.start) + text + " " + box.value.slice(box.selectionStart);
  atClose();
  box.focus();
  box.setSelectionRange(caret, caret);
  autosize(); syncComposer();
  saveDraft();
}
