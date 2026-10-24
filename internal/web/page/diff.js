import { renderChat } from "./chat.js";
import { renderShell } from "./shell.js";
import { $, api, el, ui } from "./util.js";
// 改动面板：工作区相对 HEAD 的 diff，包括未跟踪的文件（app-server 的 workspace/diff 里跑 git）。
// 包括所有轮次和人自己的改动，不只某一轮；diff 是 git 与模型写的内容，只用 textContent 显示、按行着色。
async function loadDiff() {
  const r = ui.run, d = ui.diff;
  if (!r?.workspace) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/diff`));
    if (!res.ok) throw new Error(await res.text());
    const data = await res.json();
    if (ui.run !== r) return;
    Object.assign(d, { data, err: "" });
  } catch (e) { d.err = String(e.message || e); }
  if (ui.run === r) renderChat();
}
// 打开面板并展开这几个文件（每轮末尾“看改动”来的）；和沙箱文件、子 agent、浏览器面板互斥。
export function openDiff(r, files = []) {
  ui.diffOpen = true;
  ui.filesOpen = false; ui.subOpen = null; ui.webOpen = null; ui.jobsOpen = false;
  // 换了对话就丢掉上一条的数据；展开的就是这次要看的文件，面板第一次渲染时别把它清掉（见 renderDiffPanel）。
  if (ui.diff.run !== r.id) { ui.diff.data = null; ui.diff.err = ""; }
  ui.diff.open = new Set(files);
  ui.diff.run = r.id;
  ui.diff.exit = null; // 与当前的 exit 不同：面板渲染时重新读一次
  renderChat(); renderShell();
}
function diffBody(file) {
  if (!file.diff) return el("div", "meta", "没有可显示的内容");
  const pre = el("pre", "diff-body");
  // 文件头（--- a/x、+++ b/x）在第一个 @@ 之前，不着色；之后按 +、-、@@ 分。
  let hunks = false;
  for (const line of file.diff.split("\n")) {
    if (!line) continue;
    if (line.startsWith("@@")) hunks = true;
    const cls = line.startsWith("@@") ? "hunk" : !hunks && (line.startsWith("--- ") || line.startsWith("+++ ")) ? "" : line.startsWith("+") ? "add" : line.startsWith("-") ? "del" : "";
    pre.append(el("span", cls ? "diff-line " + cls : "diff-line", line));
  }
  return pre;
}
export function renderDiffPanel() {
  const panel = $("subpanel"), r = ui.run, d = ui.diff;
  if (!r?.workspace || ui.page !== "chat") { ui.diffOpen = false; panel.hidden = true; panel.replaceChildren(); return; }
  // 换了对话就重新读；一轮结束（exit 变化）后也重新读，看到这一轮的改动。
  if (d.run !== r.id || d.exit !== r.exit) { Object.assign(d, d.run !== r.id ? { data: null, open: new Set() } : {}, { run: r.id, exit: r.exit }); loadDiff(); }
  panel.hidden = false;
  panel.dataset.agent = "";
  const head = el("div", "sp-head"), close = el("button", "sp-close", "✕");
  close.type = "button"; close.title = "关闭"; close.setAttribute("aria-label", "关闭改动面板");
  close.onclick = () => { ui.diffOpen = false; renderChat(); renderShell(); };
  const files = d.data?.files || [];
  head.append(el("strong", "", `工作区相对 HEAD 的改动${d.data?.head ? ` · ${d.data.head}` : ""}`), close);
  const note = el("div", "fp-tools"), refresh = el("button", "ghost", "刷新");
  refresh.type = "button";
  refresh.onclick = () => loadDiff();
  note.append(el("span", "", "包括所有轮次和人自己的改动，不只这一轮"), refresh);
  const body = el("div", "sp-body");
  if (d.err) body.append(el("div", "meta err", d.err));
  else if (!d.data) body.append(el("div", "meta", "读取中…"));
  else if (!files.length) body.append(el("div", "meta", "和 HEAD 一样，没有改动"));
  for (const f of files) {
    const open = d.open.has(f.path), row = el("button", "diff-file" + (open ? " on" : ""));
    row.type = "button";
    row.title = open ? "收起这个文件的改动" : "展开这个文件的改动";
    row.append(el("span", "diff-st", f.status), el("span", "q", f.path), el("span", "sz", `+${f.added} −${f.deleted}`));
    row.onclick = () => { if (open) d.open.delete(f.path); else d.open.add(f.path); renderChat(); };
    body.append(row);
    if (open) body.append(diffBody(f));
  }
  if (d.data?.truncated) body.append(el("div", "meta", "改动太多，后面的文件只列了名字"));
  panel.replaceChildren(head, note, body);
}
