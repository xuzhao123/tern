import { fillCount, renderChat, turnFiles } from "./chat.js";
import { renderShell } from "./shell.js";
import { $, HOST, api, el, icon, ui } from "./util.js";
import { openWorkTab } from "./workpanel.js";
import { openProjectFile } from "./projectfiles.js";
import { diffLines } from "./diff-lines.js";
import { beginFeedback, feedbackEditor, feedbackList } from "./diff-feedback.js";

export const DIFF_SCOPES = { workspace: "工作目录", unstaged: "未暂存", staged: "已暂存", branch: "分支提交" };
const requestKey = (d) => JSON.stringify([d.scope || "workspace", d.scope === "branch" ? d.base || "" : ""]);
const badges = new Map();
const badgeKey = (r) => JSON.stringify([ui.account, HOST, r.id]);
// 最后一轮改动卡片用的每个文件的行数：来自输入框上方那份工作目录预览（同一次读取），还没读到时是 null。
export function diffFileCounts(r) {
  const data = badges.get(badgeKey(r))?.data;
  return data ? new Map(data.files.map((f) => [f.path, f])) : null;
}
// 这一轮已经停下、而且最后一轮改过文件：对话末尾的改动卡片已经列出来了，输入框上方不再放同一份摘要。
const cardShown = (r) => !!r.exit && turnFiles(r, r.calls.at(-1)?.turn).length > 0;
function paintDiffBadge(r, value) {
  if (ui.run?.id !== r.id) return;
  const wrap = $("composer-review"), badge = $("composer-diff"), data = value.data;
  if (data) { const counts = new Map(data.files.map((f) => [f.path, f])); document.querySelectorAll(".turn-files.latest .tf-count").forEach((n) => fillCount(n, counts.get(n.dataset.path))); }
  badge.onclick = () => openDiff(r, [], "workspace");
  wrap.hidden = (!!data && !data.files.length && !value.error && !value.loading) || (cardShown(r) && !value.error);
  badge.replaceChildren();
  if (!data || value.error) { badge.textContent = value.error ? "改动读取失败 · 点击重试" : "查看工作目录改动"; badge.title = value.error || "读取当前工作目录的改动"; return; }
  const incomplete = data.truncated || data.files.some((f) => f.truncated || f.notice || f.conflicted);
  badge.append(el("span", "", `${!r.exit || value.loading ? "上次预览 · " : ""}${data.total_files || data.files.length} 个文件已更改`));
  badge.append(el("span", "diff-count-add", `+${data.files.reduce((n, f) => n + f.added, 0)}`), el("span", "diff-count-delete", `−${data.files.reduce((n, f) => n + f.deleted, 0)}`));
  if (incomplete) badge.append(el("span", "", "部分预览"));
  badge.title = "工作目录范围，统计来自最近一次读取；点击重新核对。文本行数不含二进制或未完整预览内容。";
}
export function renderDiffBadge() {
  const r = ui.run, wrap = $("composer-review"); if (!wrap) return;
  wrap.hidden = ui.page !== "chat" || !r?.workspace || !!r.source;
  if (wrap.hidden) return;
  const key = badgeKey(r), signature = r.exit || "running";
  let value = badges.get(key);
  if (!value || value.signature !== signature) {
    value = { signature, loading: true, data: value?.data }; badges.set(key, value);
    readComparison(r.id).then((data) => { value.data = data; value.error = ""; }).catch((error) => { value.error = String(error.message || error); }).finally(() => {
      value.loading = false;
      if (badges.get(key) === value && key === badgeKey(r) && ui.page === "chat" && ui.run?.id === r.id) paintDiffBadge(r, value);
    });
  }
  paintDiffBadge(r, value);
}
function button(label, fn, cls = "ghost") { const b = el("button", cls, label); b.type = "button"; b.onclick = fn; return b; }
export async function readComparison(id, scope = "workspace", base = "") {
  if (scope === "branch" && !base.trim()) throw new Error("请填写基准分支或提交，再点击比较。");
  const query = new URLSearchParams({ scope }); if (scope === "branch") query.set("base", base);
  const res = await fetch(api(`/runs/${encodeURIComponent(id)}/diff?${query}`), { signal: AbortSignal.timeout(20000) });
  if (!res.ok) throw new Error((await res.text()).trim());
  const data = await res.json();
  if (data.scope !== scope && !(scope === "workspace" && !data.scope)) throw new Error("执行主机未返回所选范围，请升级后再比较。");
  return data;
}
async function loadDiff() {
  const r = ui.run, d = ui.diff;
  if (!r?.workspace) return;
  const request = d.request = (d.request || 0) + 1;
  d.fetchKey = requestKey(d); d.loading = true;
  try {
    const data = await readComparison(r.id, d.scope || "workspace", d.base || "");
    if (d.request !== request || d.closed) return;
    Object.assign(d, { data, err: "", loadedKey: d.fetchKey });
    if (data.scope === "workspace" && ui.run?.id === r.id) {
      const value = { signature: r.exit || "running", data, loading: false }; badges.set(badgeKey(r), value); paintDiffBadge(r, value);
    }
  } catch (error) { if (d.request === request) d.err = String(error.message || error); }
  if (d.request === request) d.loading = false;
  if (ui.run?.id === r.id && ui.diff === d) renderChat();
}
export function openDiff(r, files = [], scope = "") {
  if (ui.run !== r) return;
  const tab = openWorkTab("diff", "", "改动"); if (!tab) return;
  if (ui.diff.run !== r.id) { ui.diff.data = null; ui.diff.err = ""; }
  if (scope) ui.diff.scope = scope;
  ui.diff.open ||= new Set(); files.forEach((file) => ui.diff.open.add(file));
  ui.diff.run = r.id; ui.diff.exit = null; ui.diff.fetchKey = null;
  renderChat(); renderShell();
}
const GIT_HEADER = /^(diff --(git|cc|combined) |index |--- |\+\+\+ |(new|deleted) file mode |old mode |new mode |similarity index |dissimilarity index |rename (from|to) |copy (from|to) )/;
function diffBody(file, r, data, unavailable, shownEditors) {
  if (!file.diff) return el("div", "meta diff-empty", file.notice || (file.binary ? "二进制文件" : "没有文本内容变化"));
  const pre = el("div", "diff-body");
  // git 的文件头（diff --git、index、---/+++、mode）不进页面：文件名和状态已经在上面那一行。
  for (const line of diffLines(file).filter((l) => l.kind || l.old !== undefined || l.new !== undefined || !GIT_HEADER.test(l.text))) {
    const row = el("span", "diff-line numbered " + line.kind);
    for (const side of ["old", "new"]) {
      const number = line[side] === undefined ? el("span", "diff-number", "") : button(String(line[side]), () => beginFeedback(r, data, file, line, side), "diff-number feedback-line");
      if (line[side] !== undefined) { number.disabled = unavailable || !file.version || file.conflicted; number.setAttribute("aria-label", `对${file.path}${side === "old" ? "旧侧" : "新侧"}第 ${line[side]} 行写反馈`); number.title = "对这一行写反馈"; }
      row.append(number);
    }
    row.append(el("span", "diff-code", line.text)); pre.append(row);
    if (!shownEditors.has(file.path)) { const editor = feedbackEditor(r, file, data, unavailable, line); if (editor) { pre.append(editor); shownEditors.add(file.path); } }
  }
  return pre;
}
function comparisonLabel(data, r) {
  const scope = data?.scope || "workspace", base = data?.base_ref || "HEAD";
  return scope === "unstaged" ? "暂存区 → 工作目录（含未跟踪）" : scope === "staged" ? `${base} → 暂存区` : scope === "branch" ? `${base} 的共同祖先 → HEAD` : `${base} → 工作目录（含未跟踪）`;
}
export function renderDiffPanel() {
  const panel = $("subpanel"), r = ui.run, d = ui.diff;
  if (!r?.workspace || ui.page !== "chat") { ui.diffOpen = false; panel.hidden = true; panel.replaceChildren(); return; }
  if (d.run !== r.id || d.exit !== r.exit || d.fetchKey !== requestKey(d)) {
    Object.assign(d, d.run !== r.id ? { data: null, open: d.open || new Set() } : {}, { run: r.id, exit: r.exit }); loadDiff();
  }
  const focused = panel.dataset.diffRun === r.id && ["diff-feedback-input", "diff-scope", "diff-base-input"].includes(document.activeElement?.id) ? document.activeElement : null;
  const focusState = focused ? { id: focused.id, start: focused.selectionStart, end: focused.selectionEnd } : null;
  panel.hidden = false; panel.dataset.agent = ""; panel.dataset.diffRun = r.id;
  const head = el("div", "sp-head"), scope = el("select", "diff-scope"); scope.setAttribute("aria-label", "改动范围");
  scope.id = "diff-scope";
  for (const [value, label] of Object.entries(DIFF_SCOPES)) scope.append(new Option(label, value)); scope.value = d.scope || "workspace";
  scope.onchange = () => { d.scope = scope.value; d.err = ""; loadDiff(); renderChat(); $("subpanel").querySelector('.diff-scope')?.focus(); };
  // 标题和关闭已经在上面的资源标签里（“改动 ×”），这一行只留范围和刷新。
  const refresh = button("", () => { loadDiff(); renderChat(); }, "sp-close"); refresh.setAttribute("aria-label", "刷新改动"); refresh.title = "刷新改动"; refresh.disabled = d.loading;
  refresh.innerHTML = '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/></svg>';
  head.append(scope, refresh);
  const nodes = [head];
  if (d.scope === "branch") {
    const form = el("form", "diff-base"), input = el("input"); input.id = "diff-base-input"; input.value = d.baseDraft ?? d.base ?? ""; input.placeholder = "基准分支或提交"; input.setAttribute("aria-label", "比较基准"); input.maxLength = 255; input.oninput = () => { d.baseDraft = input.value; };
    const submit = el("button", "ghost", "比较"); submit.type = "submit";
    form.onsubmit = (event) => { event.preventDefault(); d.base = input.value.trim(); loadDiff(); renderChat(); }; form.append(input, submit); nodes.push(form);
  }
  const data = d.data, files = data?.files || [], stale = !!data && (!!d.err || d.loadedKey !== requestKey(d) || d.loading);
  const summary = el("div", "diff-summary"), info = el("details", "chrome-menu diff-info"), toggle = el("summary"), content = el("div", "chrome-menu-content"); toggle.setAttribute("aria-label", "比较详情"); toggle.innerHTML = icon("info");
  if (data) {
    summary.append(el("span", "", `${stale ? "上次预览 · " : ""}${files.length}${data.total_files > files.length ? "/" + data.total_files : ""} 个文件`), el("span", "diff-totals", `文本 +${files.reduce((n, f) => n + f.added, 0)} −${files.reduce((n, f) => n + f.deleted, 0)}`));
    content.append(el("div", "", comparisonLabel(data, r)), el("div", "meta", data.dir), el("div", "meta", `分支：${data.branch || "分离 HEAD / 未记录"}`), el("div", "meta", `基准：${data.base_oid || "暂存区"}`), el("div", "meta", `目标：${data.target_oid || data.target || "工作目录"}`), el("div", "meta", "预览：" + (data.view_version || "版本未记录")));
    info.append(toggle, content); summary.append(info); nodes.push(summary, el("div", "diff-context meta", comparisonLabel(data, r)));
  }
  const body = el("div", "sp-body diff-content");
  if(data?.filter_notice)body.append(el("p","meta",data.filter_notice));
  if (d.err) body.append(el("div", "meta err", (data ? "上次预览可能过期。" : "") + d.err));
  else if (d.loading) body.append(el("div", "meta", data ? "正在核对最新改动…" : "正在读取改动…"));
  else if (!files.length) body.append(el("div", "empty", data?.filter_notice?"可核对范围没有改动；受限文件仍需另行检查":"所选范围没有改动"));
  const shownEditors = new Set();
  for (const f of files) {
    const open = d.open.has(f.path), row = button("", () => { open ? d.open.delete(f.path) : d.open.add(f.path); renderChat(); }, "diff-file" + (open ? " on" : ""));
    row.title = f.old_path ? `${f.old_path} → ${f.path}` : f.path;
    row.append(el("span", "diff-st", f.conflicted ? "!" : f.status), el("span", "q", f.old_path ? `${f.old_path} → ${f.path}` : f.path), el("span", "sz", f.conflicted ? "冲突" : f.binary ? "二进制" : f.notice && !f.diff ? "—" : `+${f.added} −${f.deleted}`)); body.append(row);
    if (open) {
      const tools = el("div", "diff-file-tools"), source = button("打开当前文件", () => openProjectFile(f.path, true)); source.disabled = f.status === "D"; source.title = "读取当前工作目录中的文件，可能与所选比较目标不同"; tools.append(source);
      const feedback = button("文件反馈", () => beginFeedback(r, data, f)); feedback.disabled = stale || !f.version; tools.append(feedback);
      body.append(tools);
      if (f.notice && f.diff) body.append(el("div", "meta", f.notice));
      body.append(diffBody(f, r, data, stale, shownEditors));
      if (!shownEditors.has(f.path)) { const editor = feedbackEditor(r, f, data, stale); if (editor) { body.append(editor); shownEditors.add(f.path); } }
    }
  }
  if (data?.truncated) body.append(el("div", "meta", `预览已截断：最多 ${data.file_limit || 500} 个文件、每文件 256 KiB、正文合计 1 MiB。省略内容已在对应文件说明。`));
  const feedback = feedbackList(r, data, stale, readComparison, (path) => d.open.add(path), shownEditors); if (feedback) body.append(feedback);
  panel.replaceChildren(...nodes, body);
  if (focusState) { const input = $(focusState.id); input?.focus(); if (focusState.start !== null && input?.setSelectionRange) input.setSelectionRange(focusState.start, focusState.end); }
}
