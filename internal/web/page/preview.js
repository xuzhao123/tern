import { $, api, el, HOST, icon, ui } from "./util.js";
import { openWorkTab, workTab } from "./workpanel.js";

// ---------- 网页预览 ----------
// 一个标签对应一条对话在 app-server 里的一个预览会话：一个授权的本机开发服务、一个独立的 Chrome。
// 画面是一张 <img>，循环长轮询 frame?after= 换图；输入只在画面聚焦时转发，全部是受限动作
// （navigate、reload、back、forward、resize、mouse、key、text），没有任意 CDP、任意 JavaScript。

const DEFAULT_URL = "http://localhost:";
const MIN_W = 320, MAX_W = 2560, MIN_H = 240, MAX_H = 1600;
const STATE_TEXT = { opening: "正在打开", ready: "就绪", failed: "失败", closed: "已关闭" };

// 这个标签的视图状态：结构只建一次，之后只改状态行与图，免得动到已经聚焦的画面。
const view = {
  run: "", tab: null, nodes: null, status: null, after: 0, client: "", seq: 0, viewport: 1,
  blob: "", timer: 0, scheduled: 0, note: "", composing: false, lastMove: 0, polls: 0, statusAt: 0,
};

// 每次建视图换一个随机 client：seq 只在同一个 client 里从 1 递增，服务端按 client 各记各的号，
// 所以刷新页面、第二个标签页、重建视图都不会跟会话里已收的号撞上（见 spec 2.1）。
const newClient = () => crypto.randomUUID();

// 动作一条一条发：一次只有一个 preview/action 请求在路上，下一个等上一个回来（成功或失败）再发。
// 并发的请求到达顺序不保证：pointerdown 会晚于它后面的 pointermove 落到服务端，被当成过期丢掉。
const pending = [];
let sending = false;

// 这台主机上用不了预览时 initialize 报的原因；有原因就只显示它，不给地址栏。
function previewReason() {
  return (ui.hosts || []).find((h) => (h.alias || "") === HOST)?.preview || "";
}

// 标签不可见（面板收起、切到别的标签、换了对话）时停止轮询。
function active(tab) {
  return !!tab && view.tab === tab && workTab() === tab && ui.page === "chat" && ui.run?.id === view.run &&
    document.visibilityState === "visible" && !$("workpanel").hidden;
}

function reset() {
  stopPolling();
  view.nodes?.observer?.disconnect();
  if (view.blob) URL.revokeObjectURL(view.blob);
  pending.length = 0; // 排着还没发的动作属于旧视图，不再发（在路上的那个回来时自己会收尾）
  // 重建视图就是这一页的一份新身份：新的 client、seq 从 1 重新算。
  Object.assign(view, { nodes: null, status: null, after: 0, client: newClient(), seq: 0, viewport: 1, blob: "", note: "", composing: false, polls: 0, statusAt: 0 });
}

function stopPolling() {
  clearTimeout(view.timer);
  clearTimeout(view.scheduled);
  view.timer = view.scheduled = 0;
}

// 视口跟着画面区域的大小：开预览与改尺寸都用它，鼠标坐标也按它换算。
function shotSize() {
  const rect = view.nodes?.shot?.getBoundingClientRect();
  const width = Math.round(rect?.width || 0) || 1280, height = Math.round(rect?.height || 0) || 800;
  return { width: Math.min(MAX_W, Math.max(MIN_W, width)), height: Math.min(MAX_H, Math.max(MIN_H, height)) };
}

function setNote(text) {
  view.note = text;
  paint();
}

async function errorText(res, what) {
  const text = await res.text().catch(() => "");
  return (text.trim() || `${what}（${res.status}）`).slice(0, 300);
}

// 取一次会话描述：状态、origin、denied 这类数字都靠它，帧序号另有帧接口。
async function refreshStatus() {
  const run = view.run;
  if (!run) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(run)}/preview`), { cache: "no-store" });
    if (view.run !== run) return; // 中途换了对话：旧结果不回填
    view.statusAt = Date.now();
    if (res.status === 404) {
      view.status = null;
      paint();
      return;
    }
    if (!res.ok) return;
    view.status = await res.json();
    view.viewport = view.status.viewport || view.viewport;
    paint();
  } catch {}
}

async function openSession(raw) {
  const url = (raw || "").trim();
  if (!url) {
    setNote("先写地址，例如 " + DEFAULT_URL + "3000");
    return;
  }
  const size = shotSize();
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(view.run)}/preview`), {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url, width: size.width, height: size.height }),
    });
    if (!res.ok) {
      setNote(await errorText(res, "打不开预览"));
      return;
    }
    view.status = await res.json();
    view.after = 0;
    view.viewport = view.status.viewport || 1;
    view.note = "";
    paint();
    startPolling();
  } catch {
    setNote("开预览的请求没发出去，检查与主机的连接");
  }
}

async function closeSession() {
  const run = view.run;
  stopPolling();
  pending.length = 0; // 会话关掉了：排着的动作没有意义
  try {
    if (run) await fetch(api(`/runs/${encodeURIComponent(run)}/preview`), { method: "DELETE" });
  } catch {}
  if (view.blob) URL.revokeObjectURL(view.blob);
  Object.assign(view, { blob: "", status: null, after: 0, note: "" });
  const img = view.nodes?.img;
  if (img) {
    img.removeAttribute("src");
    img.hidden = true;
  }
  paint();
}

// 排一个受限动作：client 是这个视图的随机标识、seq 在它里面按发出的顺序递增；
// viewport 在入队时就定下来——坐标和它是一起的，之后尺寸变了服务端会回 409，不拿旧坐标套新尺寸。
function send(action) {
  if (!view.run) return;
  const item = { ...action, viewport: view.viewport };
  const last = pending.at(-1);
  // 排队里连续的 move 只留最后一个：中间那些位置没人看得到。
  if (item.action === "mouse" && item.type === "move" && last?.action === "mouse" && last.type === "move") pending[pending.length - 1] = item;
  else pending.push(item);
  pump();
}

async function pump() {
  if (sending || !pending.length) return;
  const run = view.run;
  if (!run) {
    pending.length = 0;
    return;
  }
  if (!view.client) view.client = newClient(); // 理论上建视图时就给了，兜底
  const item = pending.shift();
  sending = true;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(run)}/preview/action`), {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...item, client: view.client, seq: ++view.seq }),
    });
    if (res.ok) {
      if (item.action === "resize") refreshStatus(); // 新的视口版本在会话描述里
    } else if (view.run === run) {
      if (res.status === 409) refreshStatus(); // 过期或旧坐标：换到最新状态，不重放
      else if (res.status === 429) setNote("上一步动作还在排队，稍等再试");
      else setNote(await errorText(res, "动作被拒绝"));
    }
  } catch {
    setNote("预览动作没发出去，检查与主机的连接");
  }
  sending = false;
  if (view.run !== run) {
    pending.length = 0; // 换了对话：排队的不再发
    return;
  }
  pump();
}

function act(action) {
  if (!view.status || view.status.state !== "ready") {
    setNote("先打开一个地址");
    return;
  }
  // 停在空白页时刷新要回到授权地址，不是刷新空白页。
  if (action === "reload" && view.status.left_origin) {
    openSession(view.status.origin + "/");
    return;
  }
  send({ action });
}

// 坐标按 <img> 的显示尺寸和视口尺寸换算成视口 CSS 像素。
function point(event) {
  const rect = view.nodes.img.getBoundingClientRect();
  const width = view.status?.width || Math.round(rect.width), height = view.status?.height || Math.round(rect.height);
  return {
    x: (event.clientX - rect.left) * width / Math.max(1, rect.width),
    y: (event.clientY - rect.top) * height / Math.max(1, rect.height),
  };
}

function mods(event) {
  const list = [];
  if (event.altKey) list.push("alt");
  if (event.ctrlKey) list.push("ctrl");
  if (event.metaKey) list.push("meta");
  if (event.shiftKey) list.push("shift");
  return list;
}

function buttonOf(event) {
  return event.button === 2 ? "right" : event.button === 1 ? "middle" : "left";
}

function mouse(event, type) {
  const at = point(event);
  if (type === "wheel") {
    send({ action: "mouse", type, x: at.x, y: at.y, dx: event.deltaX, dy: event.deltaY });
    return;
  }
  send({ action: "mouse", type, x: at.x, y: at.y, button: buttonOf(event), clicks: 1, modifiers: mods(event) });
}

// 输入：只在画面聚焦时转发。普通可打印字符只发 key（带 text），组合输入提交时发 text。
function key(event, type) {
  const text = type === "down" && [...event.key].length === 1 ? event.key : "";
  if (!event.ctrlKey && !event.metaKey) event.preventDefault();
  send({ action: "key", type, key: event.key, code: event.code, text, modifiers: mods(event) });
}

function bindInput(nodes) {
  const img = nodes.img;
  img.onpointerdown = (event) => {
    img.focus();
    event.preventDefault();
    mouse(event, "down");
  };
  img.onpointerup = (event) => mouse(event, "up");
  img.onpointermove = (event) => {
    if (document.activeElement !== img) return;
    const now = Date.now();
    if (now - view.lastMove < 40) return; // 移动事件不淹没队列
    view.lastMove = now;
    mouse(event, "move");
  };
  img.onwheel = (event) => {
    if (document.activeElement !== img) return;
    event.preventDefault();
    mouse(event, "wheel");
  };
  img.onkeydown = (event) => {
    if (view.composing || event.isComposing || event.key === "Process") return; // 组合过程中不发按键
    key(event, "down");
  };
  img.onkeyup = (event) => {
    if (view.composing) return;
    key(event, "up");
  };
  img.addEventListener("compositionstart", () => { view.composing = true; });
  img.addEventListener("compositionend", (event) => {
    view.composing = false;
    if (event.data) send({ action: "text", text: event.data }); // 中文输入法的提交走这里
  });
}

// 画面区域大小变了（面板宽度、全宽切换、窗口缩放）：去抖 300 毫秒再发 resize，之后的鼠标动作带新视口。
function watchSize(nodes) {
  let last = "";
  nodes.observer = new ResizeObserver(() => {
    clearTimeout(view.scheduled);
    view.scheduled = setTimeout(() => {
      if (!active(view.tab) || view.status?.state !== "ready") return;
      const size = shotSize(), id = size.width + "x" + size.height;
      if (id === last) return;
      last = id;
      send({ action: "resize", width: size.width, height: size.height });
    }, 300);
  });
  nodes.observer.observe(nodes.shot);
}

function showFrame(blob) {
  const url = URL.createObjectURL(blob);
  if (view.blob) URL.revokeObjectURL(view.blob);
  view.blob = url;
  const img = view.nodes?.img;
  if (img) {
    img.src = url;
    img.hidden = false;
  }
}

// 循环长轮询下一帧：回来就换图，隔几次顺带取一次状态，让“拦下几个请求”这类数字跟上。
async function poll() {
  view.timer = 0;
  const tab = view.tab, run = view.run;
  if (!active(tab) || !run) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(run)}/preview/frame?after=${view.after}&wait=5000`), { cache: "no-store" });
    if (view.run !== run || view.tab !== tab) return;
    if (res.status === 200) {
      const frame = Number(res.headers.get("X-Preview-Frame")) || 0;
      const blob = await res.blob();
      if (view.run !== run) return;
      showFrame(blob);
      view.after = Math.max(view.after, frame);
    } else if (res.status === 404 || res.status === 409) {
      view.status = null; // 会话没了：停下，等用户重新打开
      paint();
      return;
    }
    if (++view.polls % 6 === 0) refreshStatus();
  } catch {}
  if (active(tab)) {
    view.timer = setTimeout(poll, 120);
    tab.state.timer = view.timer; // 关标签时 workpanel 的 dispose 会 clearTimeout 它
  }
}

function startPolling() {
  if (!view.nodes || view.nodes.reason) return;
  if (view.status && view.status.state !== "ready") return; // 打不开或已经关掉：没有画面可等
  if (!view.timer) view.timer = setTimeout(poll, 0);
  if (!view.status || Date.now() - view.statusAt > 2000) refreshStatus();
}

function paint() {
  const nodes = view.nodes;
  if (!nodes || !nodes.status) return;
  const st = view.status;
  if (nodes.input && st && document.activeElement !== nodes.input) nodes.input.value = st.origin;
  nodes.status.replaceChildren();
  const line = el("div", "preview-line");
  line.append(el("span", "", HOST || "本机"), el("span", "preview-dot", "·"));
  line.append(el("span", "preview-origin", st?.origin || "未打开"), el("span", "preview-dot", "·"));
  line.append(el("span", "preview-state", STATE_TEXT[st?.state] || "未打开"));
  nodes.status.append(line);
  if (st?.reason && st.state !== "ready") {
    nodes.status.append(el("div", "preview-note err", (st.state === "failed" ? "打开失败：" : "会话已关闭：") + st.reason));
  }
  if (st?.left_origin) nodes.status.append(el("div", "preview-note", "页面跳到了授权范围之外，已停在空白页；点刷新回到授权地址。"));
  if (st?.popup_blocked > 0) nodes.status.append(el("div", "preview-note", `关掉了 ${st.popup_blocked} 个弹出页`));
  if (st?.denied > 0) nodes.status.append(el("div", "preview-note", `拦下 ${st.denied} 个不在授权范围的请求`));
  if (view.note) nodes.status.append(el("div", "preview-note err", view.note));
  if (!st && !view.note) {
    nodes.status.append(el("div", "preview-note", `在地址栏写本机开发服务的地址（如 ${DEFAULT_URL}3000），回车打开。`));
  }
}

function build(panel, tab, reason) {
  panel.hidden = false;
  panel.dataset.preview = "";
  const nodes = { tab, reason, panel };
  const head = el("div", "sp-head"), title = el("strong", "", "网页预览");
  head.append(title);
  if (reason) {
    const body = el("div", "sp-body");
    body.append(el("div", "empty", "这台主机不能用网页预览：" + reason));
    panel.replaceChildren(head, body);
    view.nodes = nodes;
    return;
  }
  const bar = el("div", "preview-bar");
  const step = (label, title, run) => {
    const b = el("button", "ghost", label === "refresh" || label === "close" ? "" : label);
    if (label === "refresh" || label === "close") b.innerHTML = icon(label);
    b.type = "button";
    b.title = title;
    b.setAttribute("aria-label", title);
    b.onclick = run;
    return b;
  };
  const input = el("input", "preview-url");
  input.type = "text";
  input.placeholder = DEFAULT_URL;
  input.spellcheck = false;
  input.setAttribute("aria-label", "预览地址：本机开发服务，写端口，例如 " + DEFAULT_URL + "3000");
  input.onkeydown = (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    openSession(input.value);
  };
  bar.append(step("←", "后退", () => act("back")), step("→", "前进", () => act("forward")),
    step("refresh", "刷新", () => act("reload")), input,
    step("close", "关闭预览", closeSession));
  const status = el("div", "preview-status");
  const shot = el("div", "preview-shot");
  const img = el("img", "preview-img");
  img.alt = "预览画面";
  img.tabIndex = 0;
  img.hidden = true;
  img.title = "点一下聚焦，之后可以点击、输入、滚动";
  shot.append(img);
  Object.assign(nodes, { input, status, shot, img });
  panel.replaceChildren(head, bar, status, shot);
  view.nodes = nodes;
  bindInput(nodes);
  watchSize(nodes);
  paint();
}

export function renderPreview() {
  const panel = $("subpanel"), tab = workTab(), run = ui.run;
  if (!tab || tab.kind !== "preview" || ui.page !== "chat" || !run) {
    reset();
    view.tab = null;
    view.run = "";
    return;
  }
  if (view.run !== run.id || view.tab !== tab) {
    reset();
    view.run = run.id;
    view.tab = tab;
  }
  const reason = previewReason();
  const nodes = view.nodes;
  // 结构只建一次：还在面板里就只改状态行，别动已经聚焦的画面。
  if (nodes && nodes.reason === reason && (!nodes.img || panel.contains(nodes.img))) {
    if (nodes.img) paint();
  } else {
    build(panel, tab, reason);
  }
  startPolling();
}

document.addEventListener("tern:open-preview", () => openWorkTab("preview", "", "网页预览"));
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") startPolling();
  else stopPolling();
});
