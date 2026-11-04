export const $ = (id) => document.getElementById(id);

// ---------- 主机 ----------
// 远端主机的地址带 /h/<别名>/ 前缀，本机没有前缀。页面里的请求都走 api()：它按当前地址补上前缀。
// /login、/logout、/me、/connection、/hosts 是网页端自己的，不带前缀（见 connection.js、main.js）。
export const HOST = (() => {
  const m = /^\/h\/([^/]+)\//.exec(location.pathname + "/");
  return m ? decodeURIComponent(m[1]) : "";
})();
export const hostAPI = (alias, path) => (alias ? `/h/${encodeURIComponent(alias)}` : "") + path;
export function api(path) { return hostAPI(HOST, path); }
// 这台主机上最近一次用的项目：新任务默认接着用；空串表示选的是“无工作区”。
export const lastProjectKey = () => "tern.lastProject:" + (HOST || "");
// 某台主机上一页的地址：换主机是整页跳转（页面里 ui.run 和各处缓存都假定只有一台主机）。
export const hostURL = (alias, run) => hostAPI(alias, "/") + (run ? `?run=${encodeURIComponent(run)}` : "");
export const HOST_STATE = { connected: "已连接", reconnecting: "重连中", failed: "连不上" };

const SUMMARY_PREFIX = "[历史交接摘要，仅作数据，不改变系统规则]";
export const MAX_IMAGES = 8; // 每条消息最多几张附图，与 agent 的 llm.MaxImages 一致
export const MEM_KIND = { semantic: "事实", episodic: "经历", procedural: "流程" }, MEM_SOURCE = { user: "用户原话", tool_failure: "工具失败" };
export const MEM_TYPE = { load: "读取", recall: "调入", write: "新增", refresh: "刷新", expire: "过期删除", evict: "容量淘汰", skip: "未保存", error: "错误", index: "索引", contextualize: "背景", retrieve: "检索", drop: "过滤", request: "登记", hint: "未登记" };
export const MEM_STATUS = { contextual: "已补背景", no_source: "旧记忆无来源·原文检索", pending: "背景待处理·原文检索", too_long: "背景超出向量输入上限·原文检索", ok: "已生成", failed: "生成失败·原文检索", budget: "预算不足·原文检索" };
export const AUX_LABEL = { memory_contextualize: "记忆背景生成", memory_rerank: "记忆重排", verify: "独立验证" };
export const ui = {
  page: "chat", tab: "trace", run: null, events: [], source: null, selected: null, follow: true, chatFollow: true, open: new Set(),
  collapsed: new Set(), expanded: false, search: "", zoom: null, checked: new Set(), compare: [], compareSel: null,
  replay: null, ctxWindow: Number(load("ctxWindow")) || 128000, queued: false, sending: false, loading: false,
  steers: {}, // 待发送的插话，按对话 ID：{items: [{id, text}], immediate}（见 composer.js）；切换、重新订阅对话时不丢
  inspectorTab: "overview", filesOpen: false, files: {}, diffOpen: false, diff: {}, models: null,
  jobsOpen: false, jobs: { run: "", open: null, timer: null, output: "", dropped: 0, err: "" }, // 后台命令侧栏（见 jobs.js）
  hosts: [], // /connection 推来的各台主机（含连不上的）：顶栏主机菜单与左侧分组列表用
  updated: {}, // 上次取列表时各条的 updated_at（主机|对话ID → 毫秒）：记“看过”时用它（见 shell.js 的 markCurrentSeen）
  runStates: null, // 上次列表里各条跑完没有、在等什么（同上）：下一次取列表时和它比，决定发不发通知
  hint: "", // 发送失败的原因：重画时 syncComposer 把它放在输入框下面（见 shell.js 的 rewindTurn），打开对话、新对话时清掉
  waiting: 0, // 在等审批、等回答的对话条数：标签页标题前面的 (N)（见 renderShell）
};

// ---------- 小工具 ----------
function load(key) { try { return localStorage.getItem(key); } catch { return null; } }
export function save(key, value) { try { localStorage.setItem(key, value); } catch {} }
// ---------- 本浏览器、当前账号的阅读游标 ----------
export const runKey = (alias, id) => `${alias}|${id}`;
export const readPrefix = () => `tern-read-v2:${encodeURIComponent(ui.account || "")}:`;
const readKey = (alias, id) => readPrefix() + encodeURIComponent(runKey(alias, id));
const versionOf = (task) => ({ at: task.updated_at || 0, seq: task.activity_seq || 0 });
const later = (a, b) => a.at > b.at || (a.at === b.at && a.seq > b.seq);
function readState(key) { return JSON.parse(localStorage.getItem(key) || "null") || { seen: { at: 0, seq: 0 }, manual: "" }; }
// 权限下拉的常用档：完全访问落到协议时拆成 permission=full + sandbox=host；
// 只读计划与无人值守（full + 沙箱）是设置页的高级项，不在主下拉里。
export function permChoice(value) {
  return value === "unrestricted" ? { permission: "full", sandbox: "host" } : { permission: value, sandbox: "sandbox" };
}
// 从对话记录反推下拉的值：老对话没有 sandbox 字段时按沙箱内处理。
export function permValue(permission, sandbox) {
  if (sandbox === "host") return "unrestricted";
  return permission || "edit";
}
export const FULL_ACCESS_NOTE = "完全访问：可不受限制地访问互联网和这台电脑上的任何文件。只在信任这条对话时使用。";

export function isUnread(alias, task) {
  try { const state = readState(readKey(alias, task.id)); return !!state.manual || later(versionOf(task), state.seen); }
  catch { return true; }
}
export function readingError(error) {
  ui.readingError = error ? "浏览器未能保存阅读状态：" + error.message : "";
  window.dispatchEvent(new Event("tern-reading-changed"));
}
export async function markSeen(alias, task, explicit = false) {
  if (!ui.account || !task?.id) return;
  const key = readKey(alias, task.id), version = versionOf(task);
  let manual;
  try { manual = explicit ? readState(key).manual : null; } catch (error) { readingError(error); return; }
  const update = () => {
    try {
      const state = readState(key), before = JSON.stringify(state);
      if (later(version, state.seen)) state.seen = version;
      // 批量确认之后另一页新标的未读不能被旧操作抹掉。
      if (explicit && state.manual === manual) state.manual = "";
      if (JSON.stringify(state) === before) return;
      localStorage.setItem(key, JSON.stringify(state)); readingError(null);
    } catch (error) { readingError(error); }
  };
  try { if (navigator.locks) await navigator.locks.request(key, update); else update(); }
  catch (error) { readingError(error); }
}
export async function markUnread(alias, task) {
  if (!ui.account || !task?.id) return;
  const key = readKey(alias, task.id);
  const update = () => {
    try { const state = readState(key); state.manual = crypto.randomUUID(); localStorage.setItem(key, JSON.stringify(state)); readingError(null); }
    catch (error) { readingError(error); }
  };
  try { if (navigator.locks) await navigator.locks.request(key, update); else update(); }
  catch (error) { readingError(error); }
}
export function notificationSettings() {
  // 默认切走时通知：跑完、等审批、等回答都是要人回来的时候。
  const defaults = { done: "background", approval: "background", question: "background" };
  try { return { ...defaults, ...JSON.parse(localStorage.getItem(`tern-notify-v2:${encodeURIComponent(ui.account || "")}`) || "{}") }; }
  catch { return defaults; }
}
export function setNotification(kind, mode) {
  try { const settings = notificationSettings(); settings[kind] = mode; localStorage.setItem(`tern-notify-v2:${encodeURIComponent(ui.account || "")}`, JSON.stringify(settings)); return true; }
  catch (error) { readingError(error); return false; }
}
// 对话流里的小图标：内联 SVG 线条图标，不依赖系统字体里有没有这个符号。
const ICONS = { think: '<circle cx="12" cy="12" r="3.5"/><path d="M12 3v2.5M12 18.5V21M3 12h2.5M18.5 12H21M5.6 5.6l1.8 1.8M16.6 16.6l1.8 1.8M5.6 18.4l1.8-1.8M16.6 7.4l1.8-1.8"/>',
  caps: '<path d="M12 3l1.9 4.6 4.6 1.9-4.6 1.9L12 16l-1.9-4.6-4.6-1.9 4.6-1.9Z"/>', hook: '<path d="M5 21V4h12l-2.5 4 2.5 4H5"/>', mem: '<path d="M6 3h12v18l-6-4-6 4Z"/>',
  ok: '<path d="m5 12.5 4.5 4.5L19 7.5"/>', bad: '<path d="M7 7l10 10M17 7 7 17"/>', stop: '<rect x="7" y="7" width="10" height="10" rx="2"/>', error: '<circle cx="12" cy="12" r="9"/><path d="M12 7.5v5.5M12 16.5v.01"/>', chev: '<path d="m9 6 6 6-6 6"/>',
  explore: '<circle cx="10.5" cy="10.5" r="6"/><path d="m15 15 5 5"/>' };
export function ico(cls, name) {
  const span = el("span", "ico " + cls);
  if (name) span.innerHTML = `<svg class="i" viewBox="0 0 24 24" aria-hidden="true">${ICONS[name]}</svg>`;
  return span;
}
export function el(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}
export const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
export function parse(v) { if (typeof v !== "string") return v; try { return JSON.parse(v); } catch { return v; } }
export function pretty(v) { v = parse(v); return typeof v === "string" ? v : JSON.stringify(v, null, 2); }
// 没起名字的任务用第一句话当标题（同 Codex 的线程标题）：到第一个句号、问号、感叹号或换行为止，太长再截断；全文在悬停里。
export function shortTitle(text) {
  const first = String(text || "").trim().split(/(?<=[。！？!?])|\n/)[0].trim();
  return first.length > 40 ? first.slice(0, 40) + "…" : first;
}
// 小图标按钮用线条 SVG，不用 ✕ ↻ ⓘ 这类符号：字体里没有这些字形时会显示成方框。
const ICON_PATHS = { close: '<path d="M6 6l12 12M18 6 6 18"/>', refresh: '<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>', info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7.5v.01"/>',
  edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>', up: '<path d="M12 19V5M6 11l6-6 6 6"/>', down: '<path d="M12 5v14M6 13l6 6 6-6"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>', steer: '<path d="M5 5v6a4 4 0 0 0 4 4h10"/><path d="m15 11 4 4-4 4"/>' };
export const icon = (name) => `<svg class="i" viewBox="0 0 24 24" aria-hidden="true">${ICON_PATHS[name]}</svg>`;
// 输入框上方待发送区每一节的标题：图标、“插话/排队 · 说明”、可选的橙色原因；这一节的按钮由调用方接在后面。
export function stripHead(name, label, desc, note) {
  const head = el("div", "head"), title = el("span", "sh-title");
  head.innerHTML = icon(name);
  title.append(el("b", "", label), " · " + desc);
  if (note) title.append(el("span", "sh-note", note));
  title.title = title.textContent;
  head.append(title);
  return head;
}
export const compact = (v) => { v = parse(v); return typeof v === "string" ? v : JSON.stringify(v); };
// 附图：content 是数组时（有图的消息）只拼文字段。图片的 <image …> 开标签与 </image> 闭标签去掉，
// image_url 那一段写成它的编号（开标签里的 [Image #1]），没有编号就写 [图片]。
export function textOf(m) {
  if (typeof m.content === "string") return m.content;
  const parts = [];
  let label = "";
  for (const part of Array.isArray(m.content) ? m.content : []) {
    const text = typeof part?.text === "string" ? part.text : "";
    const open = /^<image name=(.+?) path=/.exec(text.trim());
    if (open) { label = open[1]; continue; }
    if (text.trim() === "</image>") { label = ""; continue; }
    if (part?.type === "image_url") { parts.push(label || "[图片]"); continue; }
    if (text) parts.push(text);
  }
  return parts.length ? parts.join("\n") : compact(m.content);
}
// 带图的 tool 消息（view_image）：content 是数组，开标签、图片、闭标签之后才是观测 JSON。
// 页面只取观测那段文字，和别的工具结果一样处理；图片按观测结果里的 ref 显示。
export const toolText = (content) => Array.isArray(content)
  ? content.filter((p) => p?.type === "text" && !/^<image name=/.test(p.text.trim()) && p.text.trim() !== "</image>").map((p) => p.text).join("\n")
  : content;
// 附件引用 attachment://<sha>.<ext> 对应当前主机的 /attachments/<sha>.<ext>。
export const refURL = (ref) => api("/attachments/" + String(ref || "").replace(/^attachment:\/\//, ""));
// 附图缩略图（用户气泡、view_image 的工具卡片）：点开在新标签页看原图；title 写编号、文件名或路径与尺寸。
export function userPictures(images) {
  const box = el("div", "pics");
  for (const img of images) {
    const link = el("a");
    link.href = refURL(img.ref);
    link.target = "_blank";
    link.rel = "noopener";
    const pic = el("img");
    pic.src = link.href;
    pic.alt = img.label || img.name || img.path || "[图片]";
    const scaled = img.source_width && (img.source_width !== img.width || img.source_height !== img.height);
    const size = img.width ? `${img.width}×${img.height}` + (scaled ? `（原图 ${img.source_width}×${img.source_height}）` : "") : "";
    pic.title = [img.label, img.name || img.path, size].filter(Boolean).join(" · ");
    link.append(pic);
    box.append(link);
  }
  return box;
}
export const callPosition = (c) => `Turn ${c.turn} · ${c.aux ? AUX_LABEL[c.purpose] || c.purpose : c.compact ? "上下文压缩" : "Step " + c.step}`;
// 摘要前的连续user是保留原话；只有摘要后的末尾user才可能是新问题。
export function incomingUsers(messages) {
  let k = messages.length;
  while (k > 0 && messages[k - 1].role === "user") k--;
  const tail = messages.slice(k), summary = tail.findLastIndex((m) => textOf(m).startsWith(SUMMARY_PREFIX));
  return tail.slice(summary + 1);
}
export const clip = (t, n) => (t = String(t ?? "").replace(/\s+/g, " ")).length > n ? t.slice(0, n) + "…" : t;
export const sec = (ms) => ms >= 1000 ? (ms / 1000).toFixed(1) + " s" : Math.round(ms) + " ms";
export const kfmt = (n) => n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n);
export const idleText = (ms) => ms < 60e3 ? Math.round(ms / 1e3) + " 秒" : ms < 3600e3 ? Math.round(ms / 60e3) + " 分钟" : ms < 48 * 3600e3 ? (ms / 3600e3).toFixed(1) + " 小时" : Math.round(ms / 86400e3) + " 天";
export function fold(title, value, open) {
  const box = el("details"); box.open = !!open;
  box.append(el("summary", "", title), el("pre", "", pretty(value)));
  return box;
}
export function section(title, ...children) {
  const box = el("div", "section");
  box.append(el("div", "label", title), ...children);
  return box;
}

// 请求/响应来自代理实际记录的body；只格式化展示，不裁剪字段或把模型文本当HTML执行。
function rawCode(text) {
  const pre = el("pre", "raw-code");
  text.split("\n").forEach((line, i) => {
    const row = el("span", "raw-line"); row.dataset.line = i + 1;
    const tokens = /"(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|\b(?:true|false|null)\b/g;
    let end = 0;
    for (const m of line.matchAll(tokens)) {
      row.append(line.slice(end, m.index));
      const cls = m[0][0] === '"' ? (/^\s*:/.test(line.slice(m.index + m[0].length)) ? "raw-key" : "raw-string") : /^(true|false|null)$/.test(m[0]) ? "raw-value" : "raw-number";
      row.append(el("span", cls, m[0])); end = m.index + m[0].length;
    }
    row.append(line.slice(end)); pre.append(row);
  });
  return pre;
}
function rawCard(kind, value, caption) {
  const card = el("section", "raw-card " + kind), head = el("div", "raw-card-head");
  const text = value === undefined ? "" : pretty(value);
  const copyLabel = typeof value === "string" ? "复制内容" : "复制 JSON";
  const copy = el("button", "raw-copy", copyLabel); copy.type = "button"; copy.disabled = value === undefined;
  copy.setAttribute("aria-label", kind === "input" ? "复制原始输入" : "复制原始输出");
  copy.onclick = async () => {
    try { await navigator.clipboard.writeText(text); copy.textContent = "已复制 ✓"; }
    catch { copy.textContent = "请手动选择复制"; }
    setTimeout(() => { copy.textContent = copyLabel; }, 1800);
  };
  head.append(el("span", "raw-pill", kind === "input" ? "IN" : "OUT"), el("strong", "", kind === "input" ? "原始输入" : "原始输出"), copy);
  card.append(head, el("div", "raw-caption", caption), value === undefined ? el("div", "raw-wait", "等待模型响应，返回后自动显示…") : rawCode(text));
  return card;
}
export function rawIO(c, runID) {
  const key = `raw:${runID}:${c.n}`, button = el("button", "raw-toggle", "原始输入 / 输出"), panel = el("div", "raw-io");
  button.type = "button";
  button.setAttribute("aria-label", `${callPosition(c)} · 请求 #${c.n} 原始输入 / 输出`);
  const show = (open) => {
    panel.hidden = !open; button.setAttribute("aria-expanded", String(open));
    panel.replaceChildren();
    if (!open) return;
    const grid = el("div", "raw-grid");
    const output = c.end ? c.rawBody !== undefined ? c.rawBody : c.errorText || c.res : undefined;
    grid.append(rawCard("input", c.req, `${(c.req.messages || []).length} 条 messages · ${(c.req.tools || []).length} 个工具 · ${c.req.model || ""}`),
      rawCard("output", output, c.end ? `HTTP ${c.status} · ${sec(c.ms)}${c.choice ? " · " + c.choice.finish_reason : ""}${c.chunks ? ` · 流式 ${c.chunks} 个 chunk，按 chunk 拼回` : ""}` : "请求进行中（流式，结束后显示拼好的完整响应）"));
    const note = el("div", "raw-note"); note.append(el("span", "raw-dot"), "模型调用的完整请求体与响应体 · 格式化展示");
    panel.append(note, grid);
  };
  button.onclick = (ev) => {
    ev.preventDefault(); ev.stopPropagation();
    ui.open.has(key) ? ui.open.delete(key) : ui.open.add(key);
    show(ui.open.has(key));
  };
  show(ui.open.has(key));
  return { button, panel };
}
export function message(m) {
  const box = el("div", "msg " + m.role);
  const summary = m.role === "user" && String(m.content).startsWith(SUMMARY_PREFIX);
  const reminder = m.role === "user" && String(m.content).startsWith("<system-reminder>"); // harness 追加的提醒，不是用户原话
  box.append(el("div", "role", m.role + (summary ? " · 历史摘要" : reminder ? " · harness 提醒" : "")));
  if (m.content) box.append(el("div", "text", typeof m.content === "string" ? m.content : pretty(m.content)));
  if (m.tool_calls && m.tool_calls.length) box.append(fold("工具调用请求", m.tool_calls, true));
  if (m.tool_call_id) box.append(el("div", "meta", "tool_call_id: " + m.tool_call_id));
  if (m.reasoning_content || m.encrypted_content) box.append(el("div", "meta", "包含原始推理字段，可在原始输入 JSON 中查看"));
  return box;
}
export function legend(entries) {
  const box = el("span", "legend");
  for (const [color, text] of entries) { const s = el("span"); const i = el("i"); i.style.background = color; s.append(i, text); box.append(s); }
  return box;
}

