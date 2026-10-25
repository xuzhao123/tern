import { show } from "./follow.js";
import { openSettings } from "./pages.js";
import { autosize, modelVision, syncComposer } from "./shell.js";
import { $, MAX_IMAGES, api, el, refURL, ui } from "./util.js";
export async function start(url, body) {
  ui.sending = true; syncComposer();
  try {
    const res = await fetch(api(url), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (!res.ok) {
      // 失败（比如服务端说这个模型不能看图）时保留已附的图：换一下模型就能再发。
      const text = await res.text();
      if (attached.length) { ui.attachError = text; renderAttached(); }
      return alert(text);
    }
    $("query").value = "";
    attached = []; ui.attachError = ""; renderAttached();
    ui.page = "chat";
    show((await res.json()).id);
  } catch (err) {
    alert("启动失败：" + err.message);
  } finally {
    ui.sending = false; syncComposer(); autosize();
  }
}
// ---------- 附图 ----------
// 图先 POST /attachments 换成附件引用，发送时只带引用；缩略图显示 /attachments/<名字> 的原图，缩放由 agent 做。
let attached = [];
function renderAttached() {
  const strip = $("attach-strip"), pics = attached.map((img) => {
    const wrap = el("div", "pic"), thumb = el("img");
    thumb.src = refURL(img.ref);
    thumb.alt = img.name;
    thumb.title = img.name;
    const del = el("button", "del", "×");
    del.type = "button";
    del.title = "不附这张图";
    del.onclick = () => { attached = attached.filter((x) => x !== img); renderAttached(); };
    wrap.append(thumb, del);
    return wrap;
  });
  if (ui.attachError) pics.push(el("div", "err", ui.attachError));
  strip.replaceChildren(...pics);
  strip.hidden = !pics.length;
  syncComposer();
}
// 上传失败的图不进列表，服务端说的话（只支持 PNG…、超过 20 MB…）原样显示在输入框里。
async function addImages(files) {
  ui.attachError = "";
  for (const file of files) {
    if (attached.length >= MAX_IMAGES) { ui.attachError = `每条消息最多 ${MAX_IMAGES} 张图`; break; }
    try {
      const res = await fetch(api("/attachments?name=" + encodeURIComponent(file.name || "image")), { method: "POST", body: file });
      if (!res.ok) { ui.attachError = await res.text(); continue; }
      attached.push(await res.json());
    } catch (err) { ui.attachError = "上传失败：" + err.message; }
  }
  renderAttached();
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
  const query = $("query").value.trim();
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
  const body = ui.run ? { query, permission: $("perm").value, effort: $("effort").value } : { query, provider: $("provider").value, model: $("model").hidden ? "" : $("model").value, workspace: $("workspace").disabled ? "" : $("workspace").value, permission: $("perm").value, effort: $("effort").value };
  start(ui.run ? `/runs/${ui.run.id}/messages` : "/runs", images.length ? { ...body, images } : body);
};
async function steer(text) {
  const r = ui.run, item = { id: crypto.randomUUID(), text };
  const q = (ui.steers[r.id] ??= { items: [], immediate: false });
  q.items.push(item);
  $("query").value = "";
  autosize(); syncComposer();
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/steer`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text, client_id: item.id }) });
    // 409：这一轮刚好结束，留在待发送区，等这一轮结束（exit）后按顺序接着发；已经结束了就直接发。
    if (res.status === 409) { if (ui.run === r && r.exit) settleSteers(r); return; }
    if (!res.ok) throw new Error(await res.text());
  } catch (err) {
    q.items = q.items.filter((x) => x !== item);
    backToInput(text);
    $("hint").textContent = "没发出去：" + err.message;
  }
}
// ---------- 待发送 ----------
// 运行中发出去的话先排在这里，不要等 agent 取走（下一次请求模型时才取，中间可能正跑着一条 bash）。
// agent 取走时发回带同一个 client_id 的 userMessage，runs.js 据此把它从这里挪进对话；
// 没赶上这一轮的，这一轮结束（exit）时由 runs.js 交给 settleSteers 接着发。
// 另一节是服务端队列（thread/queue/add，Tab 或“排队”）：排到这一轮结束后由 app-server 接着发，所有页面看得到。
export function renderSteers() {
  const strip = $("steer-strip"), items = (ui.run && ui.steers[ui.run.id]?.items) || [];
  const queue = (ui.run && ui.run.queue) || [];
  // 流式渲染会一遍遍调到这里：内容没变就不重建，免得正要点“立即发送”时按钮被换掉。
  // 停下与否也进签名：这一轮结束时队列那一节要从“这一轮结束后发”换成“已停止…”。
  const sig = [ui.run?.id, ui.run?.exit ? "stop" : "", items.map((it) => it.id + "\n" + it.text).join("\u0000"), queue.map((q) => q.id + "\n" + q.text).join("\u0000")].join("\u0000");
  if (strip.dataset.sig === sig) return;
  strip.dataset.sig = sig;
  const nodes = [];
  if (items.length) {
    const head = el("div", "head"), now = el("button", "", "立即发送");
    now.type = "button";
    now.title = "停下正在做的这一步，马上发出去";
    now.onclick = () => sendSteersNow();
    head.append(el("span", "", "做完这一步后发给 agent"), now);
    nodes.push(head, ...items.map((it) => el("div", "item", it.text)));
  }
  if (queue.length) nodes.push(...queueSection(queue));
  strip.hidden = !nodes.length;
  strip.replaceChildren(...nodes);
}
// 排队的一节：运行中是“这一轮结束后发（N）”；这条对话停下、队列还有东西时（人点了停止）换成
// “已停止，排着的 N 条没有发”，旁边一个“发第一条”按普通续聊发出去，并从队列里删掉。
function queueSection(queue) {
  const head = el("div", "head qhead");
  if (ui.run.exit) {
    const first = el("button", "", "发第一条");
    first.type = "button";
    first.title = "按普通续聊发出队列里的第一条，并从队列里删掉";
    first.onclick = () => sendFirstQueued();
    head.append(el("span", "", `已停止，排着的 ${queue.length} 条没有发`), first);
  } else {
    head.append(el("span", "", `这一轮结束后发（${queue.length}）`));
  }
  return [head, ...queue.map((q) => {
    const row = el("div", "qitem"), del = el("button", "del", "×");
    del.type = "button";
    del.title = "不排这一条";
    del.onclick = () => dropQueued(q);
    row.append(el("span", "", q.text), del);
    return row;
  })];
}
// 排队：把输入框里的话排到这一轮结束之后，由 app-server 接着发（Tab 或“排队”按钮），不打断现在这一步。
// 成功就清空输入框；失败时文字留在输入框，原因写在输入框下面。
async function queueInput() {
  const r = ui.run, text = $("query").value.trim();
  if (!r || !text) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/queue`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text }) });
    if (!res.ok) { $("hint").textContent = "没排上队：" + (await res.text()).trim(); return; }
    // 服务端回排上的这一项，先画出来；别的页面靠 thread/status/changed 刷新列表，从 summary.queue 看到。
    (r.queue ??= []).push(await res.json());
    if (ui.run === r) { $("query").value = ""; autosize(); syncComposer(); }
  } catch (err) { $("hint").textContent = "没排上队：" + err.message; }
}
// 删掉排着的一项：队列在服务端，所有页面一起变（别的页面下次刷新列表时看到）。
async function dropQueued(item) {
  const r = ui.run;
  if (!r) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/queue/${encodeURIComponent(item.id)}`), { method: "DELETE" });
    if (!res.ok) { $("hint").textContent = "没删掉：" + (await res.text()).trim(); return; }
    if (ui.run === r) { r.queue = (r.queue || []).filter((q) => q.id !== item.id); renderSteers(); }
  } catch (err) { $("hint").textContent = "没删掉：" + err.message; }
}
// 停下以后“发第一条”：按普通续聊发出去，再从队列里删掉（发出去了才删，删不掉也不丢话）。
async function sendFirstQueued() {
  const r = ui.run, item = r?.queue?.[0];
  if (!item) return;
  ui.chatFollow = true;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/messages`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query: item.text, permission: $("perm").value, effort: $("effort").value }) });
    if (!res.ok) { $("hint").textContent = "没发出去：" + (await res.text()).trim(); return; }
    r.queue = r.queue.slice(1);
    renderSteers();
    await fetch(api(`/runs/${encodeURIComponent(r.id)}/queue/${encodeURIComponent(item.id)}`), { method: "DELETE" }).catch(() => {});
    ui.page = "chat";
    show((await res.json()).id);
  } catch (err) { $("hint").textContent = "没发出去：" + err.message; }
}
// 没送到、或者人点了停止时没发出去的，按顺序放回输入框，人自己决定发不发。
export function backToInput(text) {
  const box = $("query");
  box.value = box.value.trim() ? box.value.replace(/\s+$/, "") + "\n\n" + text : text;
  autosize(); syncComposer();
}
async function stopRun() {
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
  renderSteers();
  await stopRun();
}
// 这一轮结束时（runs.js 收到 exit）处理剩下的：按顺序用空行拼成一条接着发；
// 人点了停止按钮（不是“立即发送”）的，放回输入框由人决定。
export function settleSteers(r) {
  const q = ui.steers[r.id];
  if (!q?.items.length) return;
  const text = q.items.map((it) => it.text).join("\n\n"), now = q.immediate;
  q.items = []; q.immediate = false;
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
$("workspace").onchange = () => { ui.picked.workspace = true; syncComposer(); };
$("perm").onchange = () => { ui.picked.permission = true; syncComposer(); };
$("effort").onchange = () => { ui.picked.effort = true; syncComposer(); };
$("settings").onclick = () => openSettings();
// 输入法组合输入时的 Enter 用于选词，不能发送；@ 菜单开着时回车是插入，也不发送。
// 运行中 Tab 是排队（这一轮结束后再发）；斜杠菜单、@ 菜单开着时 Tab 归菜单。
$("query").onkeydown = (ev) => {
  if (atKey(ev)) { ev.preventDefault(); return; }
  if (ev.key === "Tab" && !ev.shiftKey && !ev.isComposing && $("slash").hidden && $("send").classList.contains("stop")) {
    if (!$("query").value.trim()) return; // 输入框里没字：Tab 照常换焦点
    ev.preventDefault();
    queueInput();
    return;
  }
  if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) { ev.preventDefault(); $("composer").requestSubmit(); }
};
$("queue").onclick = () => queueInput();
$("query").oninput = () => { autosize(); syncComposer(); if (!syncAt()) syncSlash(); };
// 斜杠命令。输入框里只有 /前缀时列出命令（编进程序的内置命令、个人级 commands/、所选工作区的 .agents/commands/）；展开由 agent 做。
async function loadCommands() {
  const ws = ui.run ? ui.run.workspace || "" : $("workspace").value;
  if (ui.commands && ui.commandsFor === ws) return;
  try { const res = await fetch(api("/commands?workspace=" + encodeURIComponent(ws))); ui.commands = res.ok ? await res.json() : []; } catch { ui.commands = []; }
  ui.commandsFor = ws;
}
async function syncSlash() {
  const box = $("slash"), m = /^\/([a-z0-9_-]*)$/.exec($("query").value);
  if (!m) { box.hidden = true; return; }
  await loadCommands();
  if (atMatch) { box.hidden = true; return; } // 等命令列表时 @ 菜单开了：这个框归它
  const list = ui.commands.filter((c) => c.name.startsWith(m[1]));
  const scope = { builtin: "内置", user: "个人", project: "项目" };
  box.replaceChildren(...list.map((c) => {
    const b = el("button", "slash-item");
    b.type = "button";
    b.append(el("span", "slash-name", "/" + c.name + (c.argument_hint ? " " + c.argument_hint : "")), el("span", "meta", (scope[c.scope] ? scope[c.scope] + " · " : "") + (c.description || "")));
    b.onmousedown = (ev) => ev.preventDefault();
    b.onclick = () => { $("query").value = "/" + c.name + " "; box.hidden = true; $("query").focus(); syncComposer(); };
    return b;
  }));
  if (!list.length) box.append(el("div", "meta", ui.commands.length ? "没有匹配的命令" : "没有可用的命令：放在 TERN_HOME 的 commands/ 或工作区的 .agents/commands/"));
  box.hidden = false;
}
$("query").onblur = () => { atClose(); $("slash").hidden = true; };
$("workspace").addEventListener("change", () => { ui.commands = null; atClose(); });
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
}
