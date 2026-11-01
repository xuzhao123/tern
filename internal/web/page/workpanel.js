import { disposeArtifact } from "./artifact-preview.js";
import { $, HOST, el, icon, ui } from "./util.js";

const sessions = new Map(), kinds = new Set(["diff", "files", "file", "project-files", "project-file", "jobs", "browser", "preview", "agent", "plan", "goal", "operations", "git"]);
let shown = null, error = "";
const key = () => ui.account && ui.run ? `tern-work-v1:${encodeURIComponent(ui.account)}:${encodeURIComponent(HOST)}:${ui.run.id}` : "";
const tabID = (kind, resource) => JSON.stringify([kind, resource]);
function resourceIcon(kind) {
  const paths = {
    agent: '<circle cx="12" cy="6" r="3"/><path d="M5 21v-3a7 7 0 0 1 14 0v3M3 11h4M17 11h4"/>',
    browser: '<circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/>',
    diff: '<path d="M8 4v12a4 4 0 0 0 4 4h4M8 8h8"/><circle cx="8" cy="4" r="2"/><circle cx="18" cy="8" r="2"/><circle cx="18" cy="20" r="2"/>',
    jobs: '<path d="m4 6 6 6-6 6M13 18h7"/>',
    preview: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M7 6.5v.01"/>',
  };
  const box = el("span", "work-tab-icon"); box.setAttribute("aria-hidden", "true");
  box.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true">' + (paths[kind] || '<path d="M14 3H5v18h14V8ZM14 3v5h5"/>') + '</svg>'; return box;
}
// 标签放不下时横向滚动：当前标签要完整露出，最左边从一个标签的左边开始，不露半个标签。
// 滚到头也对不齐时，末尾的空白（.work-tabs-end）补上差的那段。滚动条藏起来了，竖向滚轮也换成左右滚。
function revealTab(tabs) {
  const active = tabs.querySelector(".work-tab.active"), end = tabs.querySelector(".work-tabs-end"); if (!active || !end) return;
  const base = tabs.getBoundingClientRect().left - tabs.scrollLeft, items = [...tabs.querySelectorAll(".work-tab")], starts = items.map((t) => t.getBoundingClientRect().left - base);
  const left = active.getBoundingClientRect().left - base, need = left + active.offsetWidth - tabs.clientWidth;
  if (tabs.scrollLeft <= left + 0.5 && tabs.scrollLeft >= need - 0.5 && starts.some((x) => Math.abs(x - tabs.scrollLeft) < 1)) return;
  const target = need <= 0 ? 0 : starts.find((x) => x >= need - 0.5) ?? left, last = items.at(-1).getBoundingClientRect().right - base;
  end.style.width = Math.max(0, Math.ceil(target + tabs.clientWidth - last)) + "px";
  tabs.scrollLeft = target;
}
function scrollTabs(event) {
  const tabs = event.currentTarget;
  if (tabs.scrollWidth <= tabs.clientWidth || Math.abs(event.deltaX) >= Math.abs(event.deltaY)) return;
  event.preventDefault(); tabs.scrollLeft += event.deltaY;
}
function descriptor(tab) {
  return { kind: tab.kind, resource: tab.resource, title: tab.title, scroll: tab.scroll || {}, artifactPage:tab.state.artifactPage, artifactZoom:tab.state.artifactZoom, artifactMode:tab.state.artifactMode, line: tab.state.line, query: tab.state.query, scope: tab.state.scope, base: tab.state.base, baseDraft: tab.state.baseDraft, expanded: [...(tab.state.open instanceof Set ? tab.state.open : [])] };
}
function restoreTab(raw) {
  if (!raw || !kinds.has(raw.kind) || typeof raw.resource !== "string" || raw.resource.length > 4096 || typeof raw.title !== "string") return null;
  const scroll = {};
  for (const [name, value] of Object.entries(raw.scroll || {}).slice(0, 20)) if (/^\.(sp-body|sp-list|diff-body|job-out|fp-text|artifact-scroll):\d+$/.test(name) && Array.isArray(value) && value.length === 2 && value.every(Number.isFinite)) scroll[name] = value.map((v) => Math.max(0, v));
  return { id: tabID(raw.kind, raw.resource), kind: raw.kind, resource: raw.resource, title: raw.kind === "agent" && !raw.resource ? "子智能体" : raw.title.slice(0, 200), scroll, state: { artifactPage:Number.isSafeInteger(raw.artifactPage)?Math.max(1,raw.artifactPage):1, artifactZoom:Number.isFinite(raw.artifactZoom)?Math.max(.25,Math.min(3,raw.artifactZoom)):1, artifactMode:raw.artifactMode==="source"?"source":"preview", line: Number.isSafeInteger(raw.line) && raw.line > 0 ? raw.line : 1, query: typeof raw.query === "string" ? raw.query.slice(0, 4096) : "", scope: ["workspace", "unstaged", "staged", "branch"].includes(raw.scope) ? raw.scope : "workspace", base: typeof raw.base === "string" ? raw.base.slice(0, 255) : "", baseDraft: typeof raw.baseDraft === "string" ? raw.baseDraft.slice(0, 255) : undefined, open: raw.kind === "diff" ? new Set((Array.isArray(raw.expanded) ? raw.expanded : []).filter((v) => typeof v === "string").slice(0, 200)) : null } };
}
function session() {
  const id = key();
  if (!id) return null;
  if (!sessions.has(id)) {
    let raw = {};
    try { raw = JSON.parse(localStorage.getItem(id) || "{}") || {}; } catch { error = "无法恢复工作面板偏好，已有任务与文件不受影响。"; }
    const tabs = (Array.isArray(raw.tabs) ? raw.tabs : []).slice(0, 40).map(restoreTab).filter(Boolean);
    const unique = [...new Map(tabs.map((t) => [t.id, t])).values()];
    sessions.set(id, { key: id, tabs: unique, closed: (Array.isArray(raw.closed) ? raw.closed : []).slice(-10).map(restoreTab).filter(Boolean), active: unique.some((t) => t.id === raw.active) ? raw.active : unique[0]?.id, visible: !!raw.visible, wide: !!raw.wide, width: Number.isFinite(raw.width) ? Math.max(320, Math.min(800, raw.width)) : 480 });
  }
  return sessions.get(id);
}
function persist(s = session()) {
  if (!s) return;
  try { localStorage.setItem(s.key, JSON.stringify({ tabs: s.tabs.map(descriptor), closed: s.closed.map(descriptor), active: s.active, visible: s.visible, wide: s.wide, width: s.width })); error = ""; }
  catch { error = "工作面板偏好未保存，当前标签仍保留。"; }
}
function capture() {
  if (!shown?.ready || $("workpanel").hidden) return;
  const body = $("subpanel");
  for (const selector of [".sp-body", ".sp-list", ".diff-body", ".job-out", ".fp-text", ".artifact-scroll"]) {
    body.querySelectorAll(selector).forEach((node, i) => { shown.tab.scroll[selector + ":" + i] = [node.scrollLeft, node.scrollTop]; });
  }
}
function restoreScroll(tab) {
  for (const [name, [left, top]] of Object.entries(tab.scroll)) {
    const at = name.lastIndexOf(":"), node = $("subpanel").querySelectorAll(name.slice(0, at))[Number(name.slice(at + 1))];
    if (node) { node.scrollLeft = left; node.scrollTop = top; }
  }
}
function refresh() { document.dispatchEvent(new Event("tern:work-changed")); }
function bridge(tab) {
  ui.diffOpen = tab?.kind === "diff";
  ui.filesOpen = tab?.kind === "files" || tab?.kind === "file";
  ui.jobsOpen = tab?.kind === "jobs";
  ui.subOpen = tab?.kind === "agent" ? tab.resource : null;
  ui.webOpen = tab?.kind === "browser" ? { id: tab.resource, follow: false } : null;
  if (ui.diffOpen) ui.diff = tab.state;
  if (ui.filesOpen) ui.files = tab.state;
  if (ui.jobsOpen) ui.jobs = tab.state;
}
export function workTab() { const s = session(); return s?.tabs.find((t) => t.id === s.active); }
export function openWorkTab(kind, resource = "", title = "") {
  const s = session();
  if (!s || !kinds.has(kind)) return null;
  capture();
  const id = tabID(kind, resource);
  let tab = s.tabs.find((t) => t.id === id);
  if (!tab) {
    if (s.tabs.length >= 40) { error = "工作标签已达 40 个，请先关闭不需要的标签。"; refresh(); return null; }
    tab = restoreTab({ kind, resource, title: title || resource }); s.tabs.push(tab);
  }
  s.active = id; s.visible = true; shown = null;
  bridge(tab); persist(s); refresh();
  $("work-tabs").querySelector('[aria-selected="true"]')?.focus();
  return tab;
}
export function leaveWorkPanel() { capture(); if (shown) persist(shown.session); shown = null; }
export function hideWorkPanel() {
  const s = session(); if (!s) return;
  capture(); s.visible = false; persist(s); bridge(null); shown = null; refresh();
  $("work-btn")?.focus();
}
function dispose(tab) { disposeArtifact(tab.state); tab.state.closed = true; if (tab.state.blob) URL.revokeObjectURL(tab.state.blob); clearTimeout(tab.state.timer); }
export function closeWorkTab(id = session()?.active) {
  const s = session(); if (!s) return;
  capture();
  const index = s.tabs.findIndex((t) => t.id === id); if (index < 0) return;
  const tab = s.tabs.splice(index, 1)[0];
  s.closed.push(restoreTab(descriptor(tab))); s.closed = s.closed.slice(-10); dispose(tab);
  if (s.active === id) s.active = s.tabs[Math.min(index, s.tabs.length - 1)]?.id;
  if (!s.tabs.length) s.visible = false;
  shown = null; bridge(s.visible ? workTab() : null); persist(s); refresh();
  $("work-tabs").querySelector('[aria-selected="true"]')?.focus();
  if (!s.visible) $("work-btn")?.focus();
}
function button(label, fn) { const b = el("button", "ghost", label); b.type = "button"; b.onclick = fn; return b; }
function panelMenu(label, symbol) {
  const details = el("details", "chrome-menu work-menu"), summary = el("summary", "", symbol), content = el("div", "chrome-menu-content");
  summary.setAttribute("aria-label", label); summary.title = label;
  details.append(summary, content); return { details, content };
}
function resizePanel(root, s) {
  const handle = $("work-resize");
  const limit = () => Math.max(320, Math.min(800, Math.floor($("chat-pane").clientWidth * .6)));
  const current = () => Math.round(root.getBoundingClientRect().width);
  const setWidth = (width) => { s.width = Math.max(320, Math.min(limit(), Math.round(width))); root.style.setProperty("--work-width", s.width + "px"); handle.setAttribute("aria-valuenow", s.width); };
  handle.setAttribute("aria-valuemax", limit()); handle.setAttribute("aria-valuenow", current());
  handle.onpointerdown = (event) => {
    if (event.button !== 0) return;
    event.preventDefault(); handle.focus();
    const start = event.clientX, width = root.getBoundingClientRect().width;
    handle.setPointerCapture(event.pointerId);
    handle.onpointermove = (next) => { if (key() === s.key) setWidth(width + start - next.clientX); };
    handle.onpointerup = handle.onpointercancel = () => { handle.onpointermove = null; persist(s); };
  };
  handle.onkeydown = (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    setWidth(event.key === "Home" ? 320 : event.key === "End" ? limit() : current() + (event.key === "ArrowLeft" ? 20 : -20)); persist(s);
  };
}
export function showWorkPanel() {
  const s = session(); if (!s) return;
  if (s.tabs.length) { s.visible = true; bridge(workTab()); persist(s); refresh(); }
  else openWorkTab(ui.run.workspace ? "diff" : "files", "", ui.run.workspace ? "改动" : "任务产物");
}
export function renderWorkPanel(renderers) {
  const s = session(), root = $("workpanel"), tab = workTab();
  if (!s || ui.page !== "chat" || !s.visible || !tab) {
    if (shown) leaveWorkPanel();
    root.hidden = true; $("chat-pane").classList.remove("work-wide"); bridge(null); return;
  }
  if (shown?.session !== s || shown?.tab !== tab) { capture(); if (shown) persist(shown.session); shown = null; }
  root.hidden = false; root.style.setProperty("--work-width", s.width + "px");
  resizePanel(root, s);
  $("chat-pane").classList.toggle("work-wide", s.wide);
  bridge(tab);
  const selection = window.getSelection();
  if (shown?.tab === tab && selection && !selection.isCollapsed && $("subpanel").contains(selection.anchorNode)) return;
  if (shown?.tab === tab) capture();
  const chrome = JSON.stringify([s.key, s.tabs.map((t) => [t.id, t.title]), s.active, s.closed.length, s.wide, !!ui.run.workspace]);
  if (root.dataset.chrome !== chrome) {
  root.dataset.chrome = chrome;
  const controls = $("work-tools"); controls.replaceChildren();
  const add = panelMenu("打开工作视图", "+"), options = panelMenu("工作区选项", "⋯");
  for (const [kind, label] of [["git", "交付修改"], ["operations", "项目操作"], ["goal", "持续 Goal"], ["plan", "计划"], ["project-files", "项目文件"], ["diff", "改动"], ["files", "任务产物"], ["jobs", "后台命令"], ["browser", "浏览器过程"], ["preview", "网页预览"], ["agent", "子智能体"]]) {
    const entry = button(label, () => {
      if (kind === "agent") return document.dispatchEvent(new Event("tern:open-agents"));
      if (kind === "browser") return document.dispatchEvent(new Event("tern:open-browser"));
      if (kind === "preview") return document.dispatchEvent(new Event("tern:open-preview"));
      openWorkTab(kind, "", label);
    });
    entry.disabled = ["diff", "project-files", "git", "operations"].includes(kind) && !ui.run.workspace;
    add.content.append(entry);
  }
  options.content.append(button(s.wide ? "返回分屏" : "面板全宽", () => { capture(); s.wide = !s.wide; persist(s); refresh(); $("work-tools").querySelector('[aria-label="工作区选项"]')?.focus(); }));
  const reopen = button("恢复标签", () => { const last = s.closed.pop(); if (last) { capture(); if (!s.tabs.some((t) => t.id === last.id)) s.tabs.push(last); s.active = last.id; s.visible = true; shown = null; persist(s); refresh(); $("work-tabs").querySelector('[aria-selected="true"]')?.focus(); } });
  reopen.disabled = !s.closed.length;
  const others = button("关闭其他", () => { capture(); for (const other of [...s.tabs]) if (other.id !== s.active) { s.closed.push(restoreTab(descriptor(other))); dispose(other); } s.closed = s.closed.slice(-10); s.tabs = [tab]; persist(s); refresh(); $("work-tools").querySelector('[aria-label="工作区选项"]')?.focus(); });
  others.disabled = s.tabs.length < 2; options.content.append(others, reopen);
  const hide = button("×", hideWorkPanel); hide.className = "chrome-icon"; hide.title = "回到对话"; hide.setAttribute("aria-label", "回到对话");
  controls.append(add.details, options.details, hide);
  const tabs = $("work-tabs"); tabs.replaceChildren(); tabs.onwheel = scrollTabs;
  s.tabs.forEach((item, i) => {
    const wrap = el("div", "work-tab" + (item === tab ? " active" : "")), pick = button(item.title || item.kind, () => openWorkTab(item.kind, item.resource, item.title));
    pick.replaceChildren(resourceIcon(item.kind), el("span", "work-tab-title", item.title || item.kind));
    pick.setAttribute("role", "tab"); pick.setAttribute("aria-selected", String(item === tab)); pick.setAttribute("aria-controls", "subpanel"); pick.tabIndex = item === tab ? 0 : -1; pick.title = item.resource || item.title;
    pick.onkeydown = (event) => {
      const next = event.key === "ArrowRight" ? (i + 1) % s.tabs.length : event.key === "ArrowLeft" ? (i + s.tabs.length - 1) % s.tabs.length : event.key === "Home" ? 0 : event.key === "End" ? s.tabs.length - 1 : -1;
      if (next >= 0) { event.preventDefault(); const target = s.tabs[next]; openWorkTab(target.kind, target.resource, target.title); $("work-tabs").querySelector('[aria-selected="true"]')?.focus(); }
      else if (event.key === "Delete") { event.preventDefault(); closeWorkTab(item.id); }
    };
    const close = button("", () => closeWorkTab(item.id)); close.className = "work-tab-close"; close.innerHTML = icon("close"); close.title = "关闭标签"; close.setAttribute("aria-label", "关闭标签：" + item.title);
    wrap.append(pick, close); tabs.append(wrap);
  });
  tabs.append(el("div", "work-tabs-end"));
  revealTab(tabs);
  }
  $("work-notice").textContent = error; $("work-notice").hidden = !error;
  renderers[tab.kind]?.();
  if ($("subpanel").hidden) { $("subpanel").hidden = false; $("subpanel").replaceChildren(el("div", "empty", ui.loading ? "正在恢复任务与资源…" : "当前任务没有可查看的这项资源，或资源已失效。")); }
  if (tab.state.jumped) { tab.scroll[".sp-body:0"] = [$("subpanel").querySelector(".sp-body")?.scrollLeft || 0, $("subpanel").querySelector(".sp-body")?.scrollTop || 0]; tab.state.jumped = false; }
  else restoreScroll(tab);
  shown = { session: s, tab, ready: !tab.state.loading && !ui.loading && !ui.historyReplaying };
}
document.addEventListener("scroll", (event) => { if ($("subpanel")?.contains(event.target)) { capture(); if (shown) persist(shown.session); } }, true);
window.addEventListener("pagehide", leaveWorkPanel);
