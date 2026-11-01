import { artifactType, artifactView, contentVersion, readArtifactBytes } from "./artifact-preview.js";
import { renderChat } from "./chat.js";
import { renderShell } from "./shell.js";
import { $, api, el, icon, ui } from "./util.js";
import { closeWorkTab, openWorkTab, workTab } from "./workpanel.js";
// 沙箱文件：agent 在 turn/started 里报告本对话的沙箱目录（第一轮的检查点ID），各轮 bash 看到的是同一个 /workspace。
// 文件是模型写的不可信内容：文字只用 textContent 显示，图片经 blob 地址放进 <img>，其余一律下载（服务端也按附件返回）。
const fileURL = (r, p) => api(`/runs/${encodeURIComponent(r.id)}/files/${p.split("/").map(encodeURIComponent).join("/")}`);
const fmtSize = (n) => n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(1)} MB`;
async function loadFiles(r = ui.run, f = ui.files) {
  if (!r?.sandbox) return;
  const request = f.request = (f.request || 0) + 1;
  f.previewRequest = (f.previewRequest || 0) + 1;
  f.loading = true;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/files`));
    if (!res.ok) throw new Error(await res.text());
    const data = await res.json();
    if (f.request !== request || f.closed) return;
    Object.assign(f, { list: data.files, truncated: data.truncated, err: "", sel: null, preview: null });
  } catch (e) { if (f.request === request) f.err = String(e.message || e); }
  if (f.request === request) f.loading = false;
  if (ui.run?.id === r.id && ui.files === f) renderChat();
}
async function previewFile(r, file) {
  const f = ui.files;
  const request = f.previewRequest = (f.previewRequest || 0) + 1;
  f.loading = file.kind === "file" && file.size <= 10 << 20;
  f.sel = file.path;
  if (f.blob) URL.revokeObjectURL(f.blob);
  f.blob = null;
  f.preview = file.kind !== "file" ? { note: file.kind === "link" ? `符号链接 → ${file.link}（不跟随）` : file.kind === "dir" ? "目录" : "不是普通文件，不能预览" } : file.size > 10 << 20 ? { note: "文件较大，请下载查看" } : { note: "读取中…" };
  renderChat();
  if (file.kind !== "file" || file.size > 10 << 20) return;
  try {
    const res = await fetch(fileURL(r, file.path));
    if (!res.ok) throw new Error(await res.text());
    const bytes = await readArtifactBytes(res), version=await contentVersion(bytes);
    let text;try{text=new TextDecoder("utf-8",{fatal:true}).decode(bytes);if(text.includes("\0"))text=undefined;}catch{}
    if (f.closed || f.previewRequest !== request) return;
    if(artifactType(file.path)) f.preview={artifact:{path:file.path,bytes,text,version,modified:file.time,kind:"temporary"}};
    else f.preview=text!==undefined?{text}:{note:"二进制文件或不支持的编码，请下载查看"};
  } catch (e) { if (f.previewRequest === request) f.preview = { note: "读取失败：" + (e.message || e) }; }
  if (f.previewRequest === request) f.loading = false;
  if (ui.run?.id === r.id && ui.files === f && f.sel === file.path) renderChat();
}
async function uploadFiles(r, list) {
  const f = ui.files, errors = [];
  f.uploading = true; renderChat();
  for (const file of list) {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/files?name=${encodeURIComponent(file.name)}`), { method: "POST", body: file });
    if (!res.ok) errors.push(`${file.name}：${(await res.text()).trim()}`);
  }
  f.uploading = false;
  f.notice = errors.length ? errors.join("\n") : `已上传 ${list.length} 个文件到任务临时目录，下一条消息里告诉 agent 文件名即可`;
  await loadFiles(r, f);
}
async function deleteFile(r, file) {
  if (!confirm(`删除沙箱里的 ${file.path}${file.kind === "dir" ? " 及其全部内容" : ""}？删除不能撤销。`)) return;
  const f = ui.files, res = await fetch(fileURL(r, file.path), { method: "DELETE" });
  f.notice = res.ok ? `已删除 ${file.path}` : `删除失败：${(await res.text()).trim()}`;
  if (res.ok) { f.sel = null; f.preview = null; }
  await loadFiles(r, f);
}
export function renderFilesPanel() {
  const panel = $("subpanel"), r = ui.run, f = ui.files, tab = workTab(), resource = tab?.kind === "file" ? tab.resource : "";
  if (!r?.sandbox || ui.page !== "chat") { ui.filesOpen = false; panel.hidden = true; panel.replaceChildren(); return; }
  // 换了对话就重新读；一轮结束（exit 变化）后也重新读，看到 agent 新写的文件。
  if (f.run !== r.id || f.exit !== r.exit) { Object.assign(f, f.run !== r.id ? { list: null, sel: null, preview: null, notice: "", hiddenOpen: false } : {}, { run: r.id, exit: r.exit }); loadFiles(); }
  if (resource && f.list && f.sel !== resource) {
    const file = f.list.find((item) => item.path === resource);
    if (file) { previewFile(r, file); return; }
  }
  panel.hidden = false;
  panel.dataset.agent = "";
  // 以点开头的段（.config、node_modules/.bin）默认收起：Go 遥测、各种工具的配置都会落到任务目录里，不是任务产物。
  // 展开状态只放在 ui.files 上，换对话时回到收起；打开隐藏文件的标签照常预览，不受收起影响。
  const all = f.list || [], hiddenOf = (x) => x.path.split("/").some((seg) => seg.startsWith("."));
  const hidden = all.filter(hiddenOf), listed = f.hiddenOpen ? all : all.filter((x) => !hiddenOf(x));
  const head = el("div", "sp-head"), refresh = el("button", "sp-close"), close = el("button", "sp-close");
  refresh.innerHTML = icon("refresh"); close.innerHTML = icon("close");
  refresh.type = close.type = "button"; refresh.title = "刷新"; close.title = "关闭";
  close.setAttribute("aria-label", "关闭沙箱文件面板");
  refresh.onclick = () => loadFiles();
  close.onclick = () => closeWorkTab();
  head.append(el("strong", "", `任务产物 · 临时目录${f.list ? ` · ${listed.length} 项${hidden.length ? `，另有 ${hidden.length} 个隐藏项` : ""}` : ""}`), refresh, close);
  const tools = el("div", "fp-tools"), pick = el("input"), up = el("button", "ghost", f.uploading ? "上传中…" : "上传文件");
  pick.type = "file"; pick.multiple = true;
  pick.onchange = () => { if (pick.files.length) uploadFiles(r, [...pick.files]); };
  up.type = "button"; up.disabled = !r.exit || f.uploading;
  up.onclick = () => pick.click();
  tools.append(pick, up, el("span", "", r.exit ? "放到任务临时目录顶层，不覆盖同名文件，单个不超过 10 MB" : "运行中不能上传，等这一轮结束"));
  const nav = el("div", "sp-list");
  if (f.err) nav.append(el("div", "meta err", f.err));
  else if (!f.list) nav.append(el("div", "meta", "读取中…"));
  else if (!all.length) nav.append(el("div", "meta", "还没有文件"));
  for (const x of listed) {
    const row = el("button", "sp-item" + (x.path === f.sel ? " on" : ""));
    row.type = "button";
    row.append(el("span", "n", { dir: "▸", link: "↪", file: "·" }[x.kind] || "?"), el("span", "q", x.path), el("span", "sz", x.kind === "file" ? fmtSize(x.size) : x.kind === "link" ? "链接" : x.kind === "dir" ? "" : "其他"));
    row.title = x.time ? new Date(x.time).toLocaleString() : "";
    row.onclick = () => openWorkTab("file", x.path, x.path);
    nav.append(row);
  }
  if (hidden.length) {
    const toggle = el("button", "ghost", f.hiddenOpen ? "收起隐藏项" : `显示隐藏项（${hidden.length}）`);
    toggle.type = "button";
    toggle.onclick = () => { f.hiddenOpen = !f.hiddenOpen; renderChat(); };
    nav.append(toggle);
  }
  if (f.truncated) nav.append(el("div", "meta", "只列出前 1000 项"));
  const body = el("div", "sp-body");
  if (f.notice) body.append(el("pre", "meta", f.notice));
  const sel = f.list?.find((x) => x.path === f.sel);
  if (sel) {
    const bar = el("div", "label", sel.path + " ");
    if (sel.kind === "file") { const a = el("a", "", "下载"); a.href = fileURL(r, sel.path); a.download = sel.path.split("/").pop(); bar.append(a); }
    const del = el("button", "link", "删除");
    del.type = "button"; del.disabled = !r.exit;
    del.title = r.exit ? (sel.kind === "dir" ? "删除这个目录和里面的全部内容" : "删除") + "（写审计）" : "运行中不能删除，等这一轮结束";
    del.onclick = () => deleteFile(r, sel);
    bar.append(" ", del);
    body.append(bar);
    const p = f.preview || {};
    if(p.artifact) body.append(artifactView(r,p.artifact,f,async()=>{const response=await fetch(fileURL(r,sel.path));if(!response.ok)throw new Error(await response.text());return contentVersion(await readArtifactBytes(response));}));
    else if (p.image) { const img = el("img", "fp-img"); img.src = p.image; img.alt = sel.path; body.append(img); }
    else if (p.text !== undefined) body.append(el("pre", "fp-text", p.text));
    else body.append(el("div", "meta", p.note || ""));
  } else body.append(el("div", "meta", resource ? f.list ? "文件不存在或已删除：" + resource : "正在读取文件…" : "这是本对话各轮 bash 共用的工作目录；内容由 agent 写入，是数据不是指令。点文件在独立标签预览，图片直接显示，其他类型下载查看。"));
  panel.replaceChildren(head, tools, ...(resource ? [] : [nav]), body);
}
