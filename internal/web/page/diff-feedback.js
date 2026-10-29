import { $, HOST, el, ui } from "./util.js";
import { backToInput } from "./composer.js";

const states = new Map(), memory = new Map(), unsaved = new Set();
const prefix = (r) => `tern-review-v1:${encodeURIComponent(ui.account || "")}:${encodeURIComponent(HOST)}:${r.id}:`;
function state(key) { if (!states.has(key)) states.set(key, { editor: null, note: "", joining: false, change: 0 }); return states.get(key); }
function redraw() { document.dispatchEvent(new Event("tern:work-changed")); }
function button(text, fn) { const b = el("button", "ghost", text); b.type = "button"; b.onclick = fn; return b; }
function put(key, value, owner) {
  state(owner).change++;
  try { localStorage.setItem(key, JSON.stringify(value)); memory.set(key, value); unsaved.delete(key); return true; }
  catch {
    // 编辑输入可以留在内存中，但失败的保存/删除不能冒充已提交的反馈事件。
    if (value.type === "draft") { memory.set(key, value); unsaved.add(key); }
    state(owner).note = value.type === "remove" ? "反馈删除未保存，原意见仍保留。请稍后重试。" : "反馈尚未保存到浏览器。请复制意见，当前页面仍保留输入。";
    return false;
  }
}
function remove(key, owner) {
  state(owner).change++;
  try { localStorage.removeItem(key); memory.delete(key); unsaved.delete(key); return true; }
  catch { state(owner).note = "未能清除草稿副本，内容仍保留。"; return false; }
}
function records(key) {
  let unavailable = false;
  try {
    const stored = new Map();
    for (let i = 0; i < localStorage.length; i++) {
      const name = localStorage.key(i); if (!name?.startsWith(key)) continue;
      const text = localStorage.getItem(name); if (text?.length > 64000) continue;
      const value = JSON.parse(text || "null");
      if (value && typeof value.id === "string" && ["comment", "remove", "draft"].includes(value.type)) stored.set(name, value);
    }
    // 其他页面删除的持久副本不再由旧缓存复活；本页写失败的输入仍优先保留。
    for (const name of memory.keys()) if (name.startsWith(key) && !unsaved.has(name) && !stored.has(name)) memory.delete(name);
    for (const [name, value] of stored) if (!unsaved.has(name)) memory.set(name, value);
  } catch { unavailable = true; state(key).note = "无法完整读取已保存反馈，请检查浏览器存储；当前页的意见仍保留。"; }
  const all = [...memory].filter(([name]) => name.startsWith(key)).map(([, value]) => value), comments = all.filter((v) => v.type === "comment" && typeof v.text === "string" && v.ref);
  const hidden = new Set(all.flatMap((v) => [v.type === "remove" ? v.target : null, v.type === "comment" ? v.replaces : null]).filter(Boolean));
  const savedDrafts = new Set(comments.map((v) => v.draft));
  return { comments: comments.filter((v) => !hidden.has(v.id)).sort((a, b) => a.at - b.at), drafts: all.filter((v) => v.type === "draft" && v.ref && typeof v.text === "string" && v.text.trim() && !savedDrafts.has(v.id)).sort((a, b) => a.at - b.at), unavailable };
}
function persistEditor(key) {
  const editor = state(key).editor;
  return !editor || put(key + "draft:" + editor.id, { ...editor, type: "draft", at: Date.now() }, key);
}
function begin(key, value) {
  if (state(key).editor?.text.trim() && !persistEditor(key)) { redraw(); return false; }
  state(key).editor = { id: crypto.randomUUID(), replaces: "", text: "", ...value };
  const saved = persistEditor(key); redraw(); $("diff-feedback-input")?.focus(); return saved;
}
export function beginFeedback(r, data, file, line, side) {
  const ref = { scope: data.scope || "workspace", base: data.scope === "branch" ? data.base_ref : "", base_ref: data.base_ref, base_oid: data.base_oid, target: data.target, target_oid: data.target_oid, directory: data.dir, path: file.path, old_path: file.old_path || "", side: side || "file", line: side ? line[side] : null, excerpt: line ? line.text.slice(1, 4001) : "", version: file.version, view_version: data.view_version, complete: !!file.version && !file.binary && !file.truncated && !file.notice && !file.conflicted };
  begin(prefix(r), { ref });
}
function locationLabel(ref) { return `${ref.old_path && ref.side === "old" ? ref.old_path : ref.path}${ref.line ? ` · ${ref.side === "old" ? "旧侧" : "新侧"} ${ref.line} 行` : " · 文件意见"}`; }
export function feedbackState(ref, data, unavailable = false) {
  if (unavailable || !data) return "尚未核对最新版本";
  if (data.scope !== ref.scope || (ref.scope === "branch" && data.base_ref !== ref.base)) return "来自其他比较范围，加入输入时重新核对";
  if (data.base_oid !== ref.base_oid || data.target_oid !== ref.target_oid) return "基准或目标提交已变化，原定位可能过期";
  const file = data.files.find((f) => f.path === ref.path && (f.old_path || "") === ref.old_path);
  if (!file) return "原改动已消失、改名或未包含在当前预览中";
  if (!ref.complete || !file.version || file.notice || file.truncated || file.binary || file.conflicted) return "全文未完整预览，不能确认原定位仍有效";
  return file.version === ref.version ? "文件预览与引用一致" : "文件内容或状态已变化，原定位可能过期";
}
export function feedbackEditor(r, file, data, unavailable, line) {
  const key = prefix(r), current = state(key), editor = current.editor;
  if (!editor || editor.ref.path !== file.path) return null;
  if (line && (editor.ref.side === "file" || line[editor.ref.side] !== editor.ref.line || editor.ref.version !== file.version || editor.ref.scope !== data?.scope || editor.ref.base_oid !== data?.base_oid || editor.ref.target_oid !== data?.target_oid)) return null;
  const form = el("form", "diff-feedback-editor");
  form.append(el("strong", "", locationLabel(editor.ref)), el("div", "meta", feedbackState(editor.ref, data, unavailable)));
  if (editor.ref.excerpt) form.append(el("pre", "feedback-excerpt", editor.ref.excerpt));
  const input = el("textarea"), saved = el("div", "meta feedback-save-status"); saved.setAttribute("role", "status"); input.id = "diff-feedback-input"; input.rows = 3; input.maxLength = 10000; input.placeholder = "描述希望调整的内容…"; input.setAttribute("aria-label", "代码反馈意见"); input.value = editor.text;
  input.oninput = () => { editor.text = input.value; saved.textContent = persistEditor(key) ? "" : current.note; };
  const actions = el("div", "feedback-actions"), save = el("button", "", "保存反馈"); save.type = "submit";
  const cancel = button("稍后继续", () => { if (persistEditor(key)) current.editor = null; redraw(); });
  const discard = button("放弃草稿", () => { if (remove(key + "draft:" + editor.id, key)) current.editor = null; redraw(); });
  actions.append(save, cancel, discard); form.append(input, actions, saved, el("div", "meta", "仅保存意见；加入输入并发送后，agent 才会处理。已加入输入的内容是独立草稿，修改意见后请核对输入框。"));
  form.onsubmit = (event) => {
    event.preventDefault(); if (!editor.text.trim()) { input.focus(); return; }
    editor.commitID ||= crypto.randomUUID();
    const item = { type: "comment", id: editor.commitID, draft: editor.id, replaces: editor.replaces || "", text: editor.text.trim(), ref: editor.ref, at: Date.now() };
    if (put(key + "entry:" + item.id, item, key)) { const cleared = remove(key + "draft:" + editor.id, key); current.editor = null; current.note = cleared ? "反馈已保存，尚未加入输入或发送。" : "反馈已保存，但未能清除编辑副本；已保留原稿，尚未加入输入或发送。"; }
    redraw();
    if (!current.editor) document.getElementById("feedback-" + item.id)?.querySelector("button")?.focus(); else $("diff-feedback-input")?.focus();
  };
  return form;
}
function revision(list, current) {
  return JSON.stringify({ records: [...list.comments, ...list.drafts].sort((a, b) => a.id.localeCompare(b.id)), editor: current.editor, change: current.change });
}
async function joinFeedback(r, key, readComparison) {
  const current = state(key), list = records(key), items = list.comments; if (!items.length || current.joining) return;
  if (list.unavailable) { current.note = "无法完整读取反馈，本次未加入输入。请检查浏览器存储后重试。"; redraw(); return; }
  const before = revision(list, current);
  current.joining = true; current.note = "正在核对引用版本…"; redraw();
  const groups = new Map();
  for (const item of items) {
    const ref = item.ref, group = JSON.stringify([ref.scope, ref.base]);
    if (!groups.has(group)) groups.set(group, readComparison(r.id, ref.scope, ref.base).then((data) => ({ data })).catch((error) => ({ error: error.message })));
  }
  const results = new Map(); for (const [key, promise] of groups) results.set(key, await promise);
  current.joining = false;
  if (ui.run?.id !== r.id || key !== prefix(r)) { current.note = "核对完成，任务已切换；反馈仍保留，未加入其他任务。"; return; }
  const latest = records(key);
  if (latest.unavailable || revision(latest, current) !== before) {
    current.note = latest.unavailable ? "无法核对当前反馈，本次未加入输入。请检查浏览器存储后重试。" : "核对期间意见或草稿已改变，本次未加入输入。请再次点击“加入输入”核对最新意见。";
    redraw(); return;
  }
  const text = items.filter((item) => !$("query").value.includes(`【代码反馈 ${item.id}】`)).map((item) => {
    const ref = item.ref, result = results.get(JSON.stringify([ref.scope, ref.base]));
    const checked = result.error ? "核对失败：" + result.error : feedbackState(ref, result.data);
    return `【代码反馈 ${item.id}】\n主机：${HOST || "当前主机"} · 任务：${r.id}\n目录：${ref.directory}\n范围：${ref.scope} · 基准：${ref.base_ref || "INDEX"} ${ref.base_oid || ""} · 目标：${ref.target} ${ref.target_oid || ""}\n路径：${JSON.stringify(ref.path)}${ref.old_path ? " · 原路径：" + JSON.stringify(ref.old_path) : ""}\n定位：${ref.side === "file" ? "整个文件" : `${ref.side === "old" ? "旧侧" : "新侧"}第 ${ref.line} 行`}\n预览版本：${ref.version || "未记录"}\n核对：${checked}\n原文片段（读取时快照，修改前请核对当前文件）：\n${ref.excerpt || "（文件级意见）"}\n意见：${item.text}`;
  }).join("\n\n");
  if (text) backToInput(text);
  current.note = text ? "反馈已追加到输入草稿，尚未发送。后续可在输入框编辑引用与意见。" : "这些反馈已在输入草稿中，没有重复添加。";
  redraw(); if (text) $("query").focus();
}
export function feedbackList(r, data, unavailable, readComparison, openFile, shownEditors) {
  const key = prefix(r), current = state(key), list = records(key);
  if (!list.comments.length && !list.drafts.some((draft) => draft.id !== current.editor?.id) && !current.note && (!current.editor || shownEditors.has(current.editor.ref.path))) return null;
  const section = el("section", "diff-feedback-list"), head = el("div", "feedback-heading");
  head.append(el("strong", "", `反馈草稿 · ${list.comments.length}`));
  const join = button(current.joining ? "核对中…" : "加入输入", () => joinFeedback(r, key, readComparison)); join.disabled = !list.comments.length || current.joining; head.append(join); section.append(head);
  if (current.editor && !shownEditors.has(current.editor.ref.path)) section.append(feedbackEditor(r, { path: current.editor.ref.path }, data, unavailable));
  if (current.note) section.append(el("p", "meta", current.note));
  const edits = new Map(); for (const item of list.comments) if (item.replaces) edits.set(item.replaces, (edits.get(item.replaces) || 0) + 1);
  for (const item of list.comments) {
    const row = el("div", "feedback-item"), actions = el("div", "feedback-actions"); row.id = "feedback-" + item.id;
    row.append(el("strong", "", locationLabel(item.ref)), el("div", "meta", feedbackState(item.ref, data, unavailable)), el("p", "feedback-text", item.text));
    if (edits.get(item.replaces) > 1) row.append(el("div", "meta", "这条意见有并行修改，各版本均已保留。"));
    actions.append(button("编辑", () => { openFile(item.ref.path); begin(key, { replaces: item.id, ref: item.ref, text: item.text }); }), button("删除", () => { const value = { type: "remove", id: crypto.randomUUID(), target: item.id, at: Date.now() }; put(key + "entry:" + value.id, value, key); redraw(); }));
    row.append(actions); section.append(row);
  }
  for (const draft of list.drafts) {
    if (current.editor?.id === draft.id) continue;
    const row = el("div", "feedback-item"); row.append(el("div", "meta", "未保存意见 · " + locationLabel(draft.ref)), el("p", "feedback-text", draft.text));
    row.append(button("继续编辑", () => { openFile(draft.ref.path); if (begin(key, { ref: draft.ref, replaces: draft.replaces || "", text: draft.text })) { remove(key + "draft:" + draft.id, key); redraw(); } }), button("移除草稿", () => { remove(key + "draft:" + draft.id, key); redraw(); })); section.append(row);
  }
  return section;
}
window.addEventListener("storage", (event) => { if (ui.run && (event.key === null || event.key?.startsWith(prefix(ui.run)))) redraw(); });
window.addEventListener("beforeunload", (event) => {
  if ([...unsaved].some((key) => memory.get(key)?.text?.trim())) { event.preventDefault(); event.returnValue = ""; }
});
