import { $, HOST, api, el, ui } from "./util.js";
import { closeWorkTab, openWorkTab, workTab } from "./workpanel.js";
import { backToInput } from "./composer.js";
import { openDiff } from "./diff.js";
import { renderProjectTree, revealProjectPath, toggleTree, treeRevision, treeVisible } from "./project-tree.js";
import { artifactType, artifactView } from "./artifact-preview.js";
import { highlightSource, sourceLineFragment } from "./source-highlight.js";

const PAGE_LINES = 200;
function redraw() { document.dispatchEvent(new Event("tern:work-changed")); }
function button(text, fn) { const b = el("button", "ghost", text); b.type = "button"; b.onclick = fn; return b; }
function iconButton(label, symbol, fn) { const b = button(symbol, fn); b.className = "sp-close"; b.title = label; b.setAttribute("aria-label", label); return b; }
export function projectReference(value) {
  if (typeof value !== "string" || !value || value.startsWith("//") || /^[a-z][a-z\d+.-]*:/i.test(value) && !/^[^:]+:\d+$/.test(value)) return null;
  let path = value, line = 1;
  const match = /(?::(\d+)|#L(\d+)(?:-L?\d+)?)$/.exec(path);
  if (match) { line = Number(match[1] || match[2]); path = path.slice(0, match.index); }
  if (!path || !Number.isSafeInteger(line) || line < 1 || /[\x00-\x1f]/.test(path)) return null;
  try { path = decodeURIComponent(path); } catch { /* 未编码的路径仍交给服务端校验。 */ }
  return { path: path.replace(/^\.\//, ""), line, positioned: !!match };
}
export function openProjectFile(reference, literal = false) {
  const parsed = literal && typeof reference === "string" ? { path: reference, line: 1, positioned: false } : projectReference(reference);
  if (!parsed || !ui.run) return;
  const root = ui.run.workspaceDir?.replace(/\/$/, "");
  if (root && parsed.path.startsWith(root + "/")) parsed.path = parsed.path.slice(root.length + 1);
  const tab = openWorkTab("project-file", parsed.path, parsed.path.split("/").pop());
  if (!tab) return;
  if (parsed.positioned) { tab.state.line = parsed.line; tab.state.jumpDraft = null; tab.state.jump = true; redraw(); }
}
async function loadFile(tab, r) {
  const s = tab.state, request = s.request = (s.request || 0) + 1;
  s.loading = true;
  try {
    const response = await fetch(api(`/runs/${encodeURIComponent(r.id)}/workspace/file?path=${encodeURIComponent(tab.resource)}`));
    if (!response.ok) throw new Error((await response.text()).trim());
    const data = await response.json();
    if (request !== s.request || s.closed) return;
    s.data = data; s.error = ""; s.lines = null; s.note = ""; s.highlight = null; s.bytes = null; s.text = undefined;
    revealProjectPath(r, data.path);
    if (data.too_large) s.note = "文件超过 2 MiB，当前预览不读取全文；请使用项目工具按段查看。";
    else {
      try {
        s.bytes = Uint8Array.from(atob(data.data || ""), (c) => c.charCodeAt(0));
        const text = new TextDecoder("utf-8", { fatal: true }).decode(s.bytes);
        if (text.includes("\0")) throw new Error("binary");
        s.text = text; s.lines = text.split("\n"); if (s.lines.at(-1) === "" && s.lines.length > 1) s.lines.pop();
        highlightSource(data.path, text).then((result) => {
          if (s.request !== request || s.closed) return;
          s.highlight = result;
          if (ui.run?.id === r.id && workTab() === tab) redraw();
        });
      } catch { s.note = "这是二进制文件或不支持的文本编码，不能按 UTF-8 源码预览。"; }
    }
  } catch (error) { if (request === s.request) s.error = (s.data ? "上次预览可能过期。" : "") + (error.message.includes("没有这个方法") ? "当前服务版本不支持项目文件预览，请升级执行主机。" : error.message); }
  if (request === s.request) s.loading = false;
  if (ui.run?.id === r.id && workTab() === tab) redraw();
}
export function renderProjectFiles() {
  const tab = workTab(), r = ui.run, panel = $("subpanel"), s = tab.state;
  if (panel.dataset.projectRun === r.id && panel.querySelector('.project-tree-query[data-composing="true"]')) return;
  const version = () => JSON.stringify([tab.id, s.request, s.loading, s.error, s.note, s.data?.version, s.highlight?.language, !!s.highlight?.lines, s.highlight?.note, s.line, s.jump, treeRevision(r), treeVisible(r)]);
  if (panel.firstElementChild?.classList.contains("project-view-head") && panel.dataset.projectRun === r.id && panel.dataset.projectTab === tab.id && s.renderKey === version()) return;
  const jumpFocus = panel.dataset.projectTab === tab.id && document.activeElement?.id === "project-line-jump";
  panel.hidden = false; panel.dataset.agent = "";
  panel.dataset.projectRun = r.id; panel.dataset.projectTab = tab.id;
  const head = el("div", "sp-head project-view-head");
  const title = el("strong", "", tab.kind === "project-file" ? tab.resource : "项目文件"); title.title = tab.resource;
  head.append(title);
  if (!r.workspace) { panel.replaceChildren(head, el("div", "empty", "当前任务没有项目工作目录。任务产物在另一个工作视图中查看。")); return; }
  if (tab.kind === "project-files") {
    head.append(iconButton("关闭项目文件", "×", () => closeWorkTab()));
    panel.replaceChildren(head, renderProjectTree(r, "", (path) => openProjectFile(path, true), true));
    s.renderKey = version();
    return;
  }
  if (!s.request) loadFile(tab, r);
  const breadcrumb = el("nav", "file-breadcrumb"); breadcrumb.setAttribute("aria-label", "文件路径");
  const rootName = (s.data?.directory || r.workspaceDir || "").split("/").filter(Boolean).at(-1) || r.workspace;
  const root = button(rootName, () => openWorkTab("project-files", "", "项目文件")); root.title = "浏览此任务的项目文件"; breadcrumb.append(root);
  const parts = (s.data?.path || tab.resource).split("/");
  for (const [index, name] of parts.entries()) { const separator = el("span", "path-separator", "›"); separator.setAttribute("aria-hidden", "true"); breadcrumb.append(separator, el(index === parts.length - 1 ? "strong" : "span", "", name)); }
  breadcrumb.title = (s.data?.directory || r.workspaceDir || r.workspace) + "/" + (s.data?.path || tab.resource);
  title.replaceWith(breadcrumb); head.classList.add("file-heading");
  const changes = button("显示更改", () => openDiff(r, [s.data?.path || tab.resource], "workspace")); changes.classList.add("file-changes"); changes.title = "查看工作目录改动并展开当前文件"; head.append(changes);
  const treeToggle = iconButton(treeVisible(r) ? "收起目录树" : "显示目录树", "", () => toggleTree(r));
  treeToggle.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M3 7h7l2 2h9v11H3ZM3 7V4h7l2 3"/></svg>';
  treeToggle.classList.toggle("on", treeVisible(r)); treeToggle.setAttribute("aria-pressed", String(treeVisible(r))); head.append(treeToggle);
  const controls = el("div", "fp-tools project-file-tools"), refresh = iconButton(s.loading ? "读取中…" : "刷新", "↻", () => { loadFile(tab, r); redraw(); }); refresh.disabled = s.loading;
  head.append(refresh);
  const body = el("div", "sp-body");
  if (s.error) body.append(el("div", "err", s.error));
  if (s.data) {
    const info = el("details", "chrome-menu file-info"), summary = el("summary", "", "ⓘ"), content = el("div", "chrome-menu-content");
    summary.setAttribute("aria-label", "文件信息"); summary.title = "文件信息";
    content.append(el("div", "meta project-file-origin", `${s.data.directory} / ${s.data.path}`), el("div", "meta", `${s.data.size} B · ${new Date(s.data.modified).toLocaleString()}`));
    if (s.data.version) content.append(el("div", "meta", "读取版本 " + s.data.version.slice(0, 12)));
    info.append(summary, content); head.append(info);
  }
  head.append(iconButton("关闭项目文件", "×", () => closeWorkTab()));
  const artifact = s.bytes && artifactType(s.data?.path || "") && artifactView(r, {path:s.data.path,bytes:s.bytes,text:s.text,version:s.data.version,modified:s.data.modified,kind:"project"}, s, async()=>{const response=await fetch(api(`/runs/${encodeURIComponent(r.id)}/workspace/file?path=${encodeURIComponent(s.data.path)}`));if(!response.ok)throw new Error(await response.text());return (await response.json()).version||"";});
  if(artifact)body.append(artifact);
  if (s.note && !artifact) body.append(el("div", "meta", s.note));
  if (s.highlight?.note && !artifact) body.append(el("div", "meta", s.highlight.note));
  if (s.lines && !artifact) {
    s.line = Math.max(1, Math.min(s.line || 1, s.lines.length));
    const start = Math.floor((s.line - 1) / PAGE_LINES) * PAGE_LINES, end = Math.min(start + PAGE_LINES, s.lines.length);
    const jump = el("input"); jump.id = "project-line-jump"; jump.type = "number"; jump.min = "1"; jump.max = String(s.lines.length); jump.value = s.jumpDraft ?? s.line; jump.setAttribute("aria-label", "跳到文件行号"); jump.oninput = () => { s.jumpDraft = jump.value; };
    jump.onkeydown = (event) => { if (event.key === "Enter") { event.preventDefault(); const line = Number(jump.value); if (Number.isSafeInteger(line) && line > 0 && line <= s.lines.length) { s.line = line; s.jumpDraft = null; s.jump = true; redraw(); } } };
    const prev = iconButton("上一段", "‹", () => { s.line = Math.max(1, start - PAGE_LINES + 1); s.jumpDraft = null; s.jump = true; redraw(); }), next = iconButton("下一段", "›", () => { s.line = end + 1; s.jumpDraft = null; s.jump = true; redraw(); });
    prev.disabled = start === 0; next.disabled = end === s.lines.length;
    controls.append(prev, el("span", "meta", `${start + 1}–${end} / ${s.lines.length}`), next, el("span", "meta", "行"), jump);
    const cite = button("引用此行", () => {
      backToInput(`[项目文件引用：${HOST || "当前主机"} · 任务 ${r.id}]\n路径：${s.data.path}\n目录：${s.data.directory}\n读取版本：${s.data.version}\n行号：${s.line}\n原文片段：${s.lines[s.line - 1].slice(0, 4000)}\n（引用是读取时的快照，修改前请核对当前文件。）`);
      ui.hint = "文件引用已加入草稿，尚未发送。"; $("hint").textContent = ui.hint;
    }); cite.title = "加入输入引用"; cite.setAttribute("aria-label", "加入输入引用"); cite.classList.add("project-cite"); cite.disabled = !!s.error || s.loading; controls.append(cite);
    const source = el("div", "project-source");
    s.lines.slice(start, end).forEach((text, i) => {
      const line = start + i + 1, row = el("div", "project-line" + (line === s.line ? " selected" : "")); row.dataset.line = line;
      const number = button(String(line), () => { s.line = line; s.jumpDraft = null; redraw(); }); number.setAttribute("aria-label", `选择第 ${line} 行`);
      const code = el("code"); if (s.highlight?.lines?.[line - 1]) code.append(sourceLineFragment(s.highlight.lines[line - 1])); else code.textContent = text || " ";
      row.append(number, code); source.append(row);
    }); body.append(source);
  } else if (!artifact && !s.note && !s.error) body.append(el("div", "meta", "正在读取文件…"));
  const workspace = el("div", "project-file-workspace"), sourcePane = el("div", "project-source-pane"); sourcePane.append(controls, body); workspace.append(sourcePane);
  if (treeVisible(r)) workspace.append(renderProjectTree(r, s.data?.path || tab.resource, (path) => openProjectFile(path, true)));
  panel.replaceChildren(head, workspace);
  if (s.jump && s.lines) { body.querySelector(`[data-line="${s.line}"]`)?.scrollIntoView({ block: "center" }); s.jump = false; s.jumped = true; }
  if (jumpFocus) $("project-line-jump")?.focus({ preventScroll: true });
  s.renderKey = version();
}
window.addEventListener("resize", () => { if (["project-file", "project-files"].includes(workTab()?.kind)) redraw(); });
