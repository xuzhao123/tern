import { $, HOST, api, el, ui } from "./util.js";
import { workTab } from "./workpanel.js";

const trees = new Map();
const key = (r) => `tern-tree-v1:${encodeURIComponent(ui.account || "")}:${encodeURIComponent(HOST)}:${r.id}`;
const folder = (name) => typeof name === "string" && name.length <= 4096 && (name === "." || !name.startsWith("/") && name.split("/").every((part) => part && part !== "." && part !== ".."));
function state(r) {
  const id = key(r);
  if (!trees.has(id)) {
    let saved = {}, note = "";
    try { saved = JSON.parse(localStorage.getItem(id) || "{}") || {}; } catch { note = "目录阅读偏好未能恢复。"; }
    trees.set(id, { key: id, nodes: new Map(), expanded: new Set([".", ...(Array.isArray(saved.expanded) ? saved.expanded.filter(folder).slice(0, 200) : [])]), visible: typeof saved.visible === "boolean" ? saved.visible : null, query: typeof saved.query === "string" ? saved.query.slice(0, 4096) : "", scroll: Number.isFinite(saved.scroll) ? Math.max(0, saved.scroll) : 0, search: { request: 0 }, revision: 0, note });
  }
  return trees.get(id);
}
function persist(s) {
  try { localStorage.setItem(s.key, JSON.stringify({ expanded: [...s.expanded].slice(0, 200), visible: s.visible, query: s.query, scroll: s.scroll })); s.note = ""; }
  catch { s.note = "目录阅读偏好未保存，当前展开状态仍保留。"; }
}
function repaint(r, s) {
  s.revision++;
  if (ui.run?.id === r.id && key(r) === s.key && ["project-file", "project-files"].includes(workTab()?.kind) && !document.querySelector('.project-tree-query[data-composing="true"]')) document.dispatchEvent(new Event("tern:work-changed"));
}
function button(label, fn, cls = "") { const b = el("button", cls, label); b.type = "button"; b.onclick = fn; return b; }
async function readFolder(r, path, more = false) {
  const s = state(r), node = s.nodes.get(path) || { entries: null, request: 0 };
  s.nodes.set(path, node);
  const request = ++node.request, cursor = more ? node.cursor : "";
  node.loading = true; node.dirty = false;
  try {
    const params = new URLSearchParams({ path }); if (cursor) params.set("cursor", cursor);
    const response = await fetch(api(`/runs/${encodeURIComponent(r.id)}/workspace/tree?${params}`), { signal: AbortSignal.timeout(15000) });
    if (!response.ok) { const message = (await response.text()).trim(); throw new Error(response.status === 404 && message.includes("没有这个方法") ? "执行主机尚不支持目录树，请升级主机；仍可输入完整路径打开文件。" : message); }
    const data = await response.json();
    if (node.request !== request || s.nodes.get(path) !== node) return;
    if (data.path !== path || !Array.isArray(data.entries) || !data.version) throw new Error("执行主机未返回有效目录列表，请升级后重试。");
    if (more && node.version !== data.version) throw new Error("目录列表已变化，请刷新后重新读取。");
    node.entries = more ? [...node.entries, ...data.entries] : data.entries;
    Object.assign(node, { cursor: data.next_cursor || "", version: data.version, total: data.total, error: "" });
  } catch (error) { if (node.request === request) node.error = String(error.message || error); }
  if (node.request === request) { node.loading = false; repaint(r, s); }
}
async function search(r) {
  const s = state(r), query = s.query.trim(), current = s.search, request = ++current.request;
  if (!query) { current.files = null; current.loading = false; current.error = ""; repaint(r, s); return; }
  current.query = query; current.loading = true; queueMicrotask(() => repaint(r, s));
  try {
    const response = await fetch(api(`/workspaces/files?threadId=${encodeURIComponent(r.id)}&q=${encodeURIComponent(query)}`), { signal: AbortSignal.timeout(15000) });
    if (!response.ok) throw new Error((await response.text()).trim());
    const data = await response.json();
    if (current.request !== request || s.query.trim() !== query) return;
    current.files = data.files || []; current.error = "";
  } catch (error) { if (current.request === request) current.error = String(error.message || error); }
  if (current.request === request) { current.loading = false; repaint(r, s); }
}
export function treeVisible(r) { return state(r).visible ?? $("workpanel").clientWidth >= 480; }
export function treeRevision(r) { return state(r).revision; }
export function toggleTree(r) { const s = state(r); s.visible = !treeVisible(r); persist(s); repaint(r, s); }
export function revealProjectPath(r, path) {
  if (!folder(path)) return;
  const s = state(r), parts = path.split("/");
  for (let i = 1; i < parts.length; i++) s.expanded.add(parts.slice(0, i).join("/"));
  persist(s);
}
function treeIcon(kind, link) {
  const icon = el("span", "tree-icon"); icon.setAttribute("aria-hidden", "true");
  const path = kind === "directory" ? '<path d="M3 7h7l2 2h9v11H3ZM3 7V4h7l2 3"/>' : '<path d="M14 3H5v18h14V8ZM14 3v5h5"/>';
  icon.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true">' + path + (link ? '<path d="m12 16 3-3-3-3M9 13h6"/>' : "") + '</svg>'; return icon;
}
function treeRows(r, s, path, selected, openFile) {
  const list = el("ul", "project-tree-list"), node = s.nodes.get(path);
  if (!node || node.dirty && !node.loading) { readFolder(r, path); }
  const current = s.nodes.get(path);
  if (current?.error) { const retry = el("li"); retry.append(button("重新读取", () => { readFolder(r, path); repaint(r, s); }, "tree-retry")); list.append(el("li", "tree-note err", (current.entries ? "上次列表可能过期。" : "") + current.error), retry); }
  for (const entry of current?.entries || []) {
    const isFolder = entry.kind === "directory", opened = isFolder && s.expanded.has(entry.path), li = el("li"), row = button("", () => {
      if (isFolder) { opened ? s.expanded.delete(entry.path) : s.expanded.add(entry.path); persist(s); repaint(r, s); }
      else openFile(entry.path);
    }, "project-tree-row" + (entry.path === selected ? " selected" : ""));
    row.dataset.treePath = entry.path; row.title = entry.path + (entry.unavailable ? " · " + entry.unavailable : entry.symlink ? " · 符号链接" : ""); row.disabled = !!entry.unavailable;
    if (isFolder) row.setAttribute("aria-expanded", String(opened)); else if (entry.path === selected) row.setAttribute("aria-current", "true");
    const chevron = el("span", "tree-chevron", isFolder ? opened ? "⌄" : "›" : ""); chevron.setAttribute("aria-hidden", "true");
    row.append(chevron, treeIcon(entry.kind, entry.symlink), el("span", "tree-name", entry.name));
    row.onkeydown = (event) => {
      if (event.key === "ArrowRight" && isFolder && !opened || event.key === "ArrowLeft" && isFolder && opened) { event.preventDefault(); row.click(); return; }
      if (!["ArrowUp", "ArrowDown", "ArrowLeft"].includes(event.key)) return;
      event.preventDefault();
      if (event.key === "ArrowLeft") { li.parentElement.closest("li")?.querySelector(".project-tree-row")?.focus(); return; }
      const rows = [...row.closest(".project-tree").querySelectorAll(".project-tree-row:not(:disabled)")], index = rows.indexOf(row);
      rows[index + (event.key === "ArrowUp" ? -1 : 1)]?.focus();
    };
    li.append(row); if (entry.unavailable) li.append(el("span", "sr-only", entry.unavailable));
    if (opened && !entry.unavailable) li.append(treeRows(r, s, entry.path, selected, openFile)); list.append(li);
  }
  if (current?.loading) list.append(el("li", "tree-note", "读取中…"));
  else if (current?.entries && !current.entries.length && !current.error) list.append(el("li", "tree-note", "空目录"));
  if (current?.cursor && !current.error) { const more = button(current.loading ? "读取中…" : `加载更多（${current.entries.length}/${current.total}）`, () => { readFolder(r, path, true); repaint(r, s); }, "tree-more"); more.disabled = current.loading; const li = el("li"); li.append(more); list.append(li); }
  return list;
}
export function renderProjectTree(r, selected, openFile, standalone = false) {
  const s = state(r), old = document.querySelector(".project-tree-query"), focus = document.activeElement === old && old?.dataset.treeOwner === s.key, selection = focus ? [old.selectionStart, old.selectionEnd] : null;
  const focusedPath = document.activeElement?.closest(".project-tree")?.dataset.treeOwner === s.key ? document.activeElement.dataset.treePath : "", aside = el("aside", "project-tree" + (standalone ? " standalone" : "")); aside.setAttribute("aria-label", "项目目录树"); aside.dataset.treeOwner = s.key;
  const bar = el("div", "project-tree-search"), input = el("input"), refresh = button("↻", () => {
    for (const node of s.nodes.values()) { node.request++; node.loading = false; node.dirty = true; }
    s.search.request++; s.search.query = ""; s.search.loading = false; repaint(r, s);
  });
  refresh.title = "刷新目录树"; refresh.setAttribute("aria-label", refresh.title);
  input.type = "search"; input.className = "project-tree-query"; input.dataset.treeOwner = s.key; input.placeholder = "筛选文件…"; input.setAttribute("aria-label", "筛选项目文件"); input.value = s.query; input.maxLength = 4096;
  const changed = () => { s.query = input.value; s.search.request++; persist(s); clearTimeout(s.timer); s.timer = setTimeout(() => search(r), 220); };
  input.oninput = (event) => { if (!event.isComposing) changed(); };
  input.oncompositionstart = () => { input.dataset.composing = "true"; };
  input.oncompositionend = () => { input.dataset.composing = "false"; changed(); };
  input.onkeydown = (event) => { if (event.isComposing) return; if (event.key === "Enter" && input.value.trim()) { event.preventDefault(); openFile(input.value.trim()); } else if (event.key === "Escape" && input.value) { event.preventDefault(); event.stopPropagation(); input.value = ""; changed(); search(r); } };
  bar.append(input, refresh); aside.append(bar);
  const body = el("div", "project-tree-body"); body.onscroll = () => { s.scroll = body.scrollTop; clearTimeout(s.scrollTimer); s.scrollTimer = setTimeout(() => persist(s), 120); };
  if (s.query.trim()) {
    if (s.search.query !== s.query.trim() && !s.search.loading) search(r);
    body.append(el("div", "tree-note", "Git可见文件，最多20项；回车按完整路径打开。"));
    if (s.search.error) body.append(el("div", "tree-note err", s.search.error));
    else if (s.search.loading || s.search.query !== s.query.trim()) body.append(el("div", "tree-note", "筛选中…"));
    else if (!s.search.files?.length) body.append(el("div", "tree-note", "没有匹配文件，可按完整路径打开。"));
    if (s.search.query === s.query.trim() && !s.search.error && !s.search.loading) for (const path of s.search.files || []) { const row = button(path, () => openFile(path), "project-tree-result"); row.title = path; body.append(row); }
  } else body.append(treeRows(r, s, ".", selected, openFile));
  if (s.note) body.append(el("div", "tree-note err", s.note)); aside.append(body);
  queueMicrotask(() => {
    if (!aside.isConnected || ui.run?.id !== r.id || key(r) !== s.key) return;
    body.scrollTop = s.scroll;
    if (focus) { input.focus({ preventScroll: true }); input.setSelectionRange(...selection); }
    else if (focusedPath) [...aside.querySelectorAll(".project-tree-row")].find((row) => row.dataset.treePath === focusedPath)?.focus({ preventScroll: true });
  });
  return aside;
}
