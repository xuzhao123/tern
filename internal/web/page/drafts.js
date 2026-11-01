import { $, HOST, ui } from "./util.js";
import { readDraftInput, writeDraftInput } from "./composer.js";

const author = crypto.randomUUID();
let key = "", base = null, restoring = false, unsafe = false, conflicted = false, localEdited = false;
const signature = (value) => JSON.stringify(value || {});
const read = (name) => { const raw = localStorage.getItem(name); return raw ? JSON.parse(raw) : null; };
const blank = () => ({ text: "", images: [], picks: {} });
const prefix = () => "tern-draft-v1:" + encodeURIComponent(ui.account || "") + ":" + encodeURIComponent(HOST) + ":";
function draftKey(id) {
  const name = $("workspace").value;
  const project = ui.settings?.effective?.workspaces?.find((w) => w.name === name);
  return prefix() + (id ? "task:" + id : "new:" + (project?.id || name || "none"));
}
function copies(name) {
  const values = [];
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i);
    if (k?.startsWith(name + ":copy:")) {
      const record = read(k);
      if (record?.value && record.revision) values.push({ key: k, record });
    }
  }
  return values;
}
function warn(text) {
  const box = $("draft-notice");
  box.replaceChildren(document.createTextNode(text)); box.hidden = false;
}
function persist(name, value, ancestors = []) {
  // ancestors 最新的在前，截取保留前 64 个；保留后面的会丢掉刚刚的上一版，旧页面的副本就永远认不出祖先，每次打开都报冲突。
  const record = { revision: crypto.randomUUID(), author, at: Date.now(), ancestors: [...new Set(ancestors)].slice(0, 64), value };
  // 独立副本先写，两个页面同时覆盖主记录时仍各自保留输入。
  localStorage.setItem(name + ":copy:" + author, JSON.stringify(record));
  localStorage.setItem(name, JSON.stringify(record));
  for (const entry of copies(name)) if (record.ancestors.includes(entry.record.revision)) localStorage.removeItem(entry.key);
  return record;
}
function conflict(remote) {
  conflicted = true;
  const box = $("draft-notice");
  warn("另一页面修改了这份草稿。两个版本均已保留，请选择：");
  const local = readDraftInput();
  try {
    localStorage.setItem(key + ":copy:" + author, JSON.stringify({ revision: crypto.randomUUID(), author, at: Date.now(), ancestors: [base?.revision].filter(Boolean), value: local }));
  } catch { unsafe = true; warn("草稿冲突，且本地副本保存失败。请先复制当前输入，再选择版本。"); }
  const preview = document.createElement("details"), label = document.createElement("summary"), text = document.createElement("pre");
  label.textContent = "查看另一版本"; text.textContent = remote.value.text || "（没有文字）";
  preview.append(label, text); box.append(preview);
  const choose = (value) => {
    try {
      const primary = read(key);
      base = persist(key, value, [base?.revision, remote.revision, primary?.revision, ...(base?.ancestors || []), ...(remote.ancestors || [])].filter(Boolean));
      restoring = true; writeDraftInput(value); restoring = false;
      unsafe = false; conflicted = false; localEdited = false; box.hidden = true;
    } catch { restoring = false; unsafe = true; warn("草稿没有保存成功，请先复制输入内容。"); }
  };
  for (const [label, value] of [["保留我的版本", local], ["加载另一版本", remote.value], ["合并文字", { ...local, text: [local.text, remote.value.text].filter(Boolean).join("\n\n"), images: [...new Map([...(local.images || []), ...(remote.value.images || [])].map((i) => [i.ref, i])).values()] }]]) {
    const b = document.createElement("button"); b.type = "button"; b.textContent = label; b.onclick = () => choose(value); box.append(b);
  }
}
export function saveDraft() {
  if (!ui.account || !key || restoring) return true;
  const value = readDraftInput();
  if (signature(value) !== signature(base?.value || blank())) localEdited = true;
  try {
    const remote = read(key);
    if (remote?.revision !== base?.revision && localEdited && remote && signature(value) !== signature(remote.value)) {
      const backup = { revision: crypto.randomUUID(), author, at: Date.now(), ancestors: [base?.revision, ...(base?.ancestors || [])].filter(Boolean), value };
      localStorage.setItem(key + ":copy:" + author, JSON.stringify(backup));
      conflict(remote); return true;
    }
    if (remote && !localEdited && remote.revision !== base?.revision) {
      base = remote; restoring = true; writeDraftInput(remote.value); restoring = false;
    } else if (!remote || signature(value) !== signature(remote.value)) {
      base = persist(key, value, [remote?.revision, ...(remote?.ancestors || [])].filter(Boolean));
    } else base = remote;
    unsafe = false;
    return true;
  } catch { restoring = false; unsafe = true; warn("草稿未保存：浏览器存储不可用或空间不足。离开前请复制输入内容。"); return false; }
}

export function leaveDraft() {
  return saveDraft() || confirm("草稿尚未保存。仍要切换？请先复制需要保留的内容。");
}
export function switchDraft(id, saved = false) {
  if (!ui.account) return;
  const next = draftKey(id);
  if (next === key) return;
  if (!saved && !leaveDraft()) return false;
  key = next; base = null; conflicted = false; localEdited = false; $("draft-notice").hidden = true;
  try {
    base = read(key);
    const alternatives = copies(key).filter(({ record }) => record.revision !== base?.revision && !base?.ancestors?.includes(record.revision) && signature(record.value) !== signature(base?.value));
    restoring = true; writeDraftInput(base?.value || blank()); restoring = false;
    if (alternatives.length) conflict(alternatives.sort((a, b) => b.record.at - a.record.at)[0].record);
    unsafe = false;
  } catch {
    restoring = true; writeDraftInput(blank()); restoring = false;
    unsafe = true; warn("无法读取此任务草稿，已清空输入区以防串用。请检查浏览器存储或粘贴已复制的内容。");
  }
  document.dispatchEvent(new Event("tern:draft-changed"));
  return true;
}
export function captureDraft() {
  if (!saveDraft()) return null;
  if (conflicted) return null;
  return { key, value: readDraftInput(), revision: base?.revision };
}
export function rememberDelivery(token, request) {
  try { localStorage.setItem(token.key + ":pending", JSON.stringify({ ...request, token })); return true; }
  catch { warn("无法保存发送回执信息，本次未发送。请先复制草稿并检查存储。"); return false; }
}
export function pendingDelivery() { try { return key ? read(key + ":pending") : null; } catch { return null; } }
export function finishDelivery(token, accepted) {
  try {
    localStorage.removeItem(token.key + ":pending");
    if (!accepted) return;
    const current = token.key === key ? readDraftInput() : read(token.key)?.value;
    if (signature(current) !== signature(token.value) || read(token.key)?.revision !== token.revision) return;
    const cleared = { ...current, text: "", images: [], compact: false, skillRefs: [] }, previous = read(token.key);
    const record = persist(token.key, cleared, [previous?.revision, ...(previous?.ancestors || [])].filter(Boolean));
    if (token.key === key) { base = record; localEdited = false; restoring = true; writeDraftInput(cleared); restoring = false; }
  } catch { restoring = false; unsafe = true; warn("发送回执已收到，但草稿清理失败；请勿重复发送原消息。"); }
}
export const currentDraftKey = () => key;

export function readPendingSteers(id) {
  try {
    const saved = read(prefix() + "steer:" + id);
    return saved?.items?.length ? { ...saved, restored: true } : { items: [], immediate: false };
  } catch { return { items: [], immediate: false }; }
}
export function savePendingSteers(id, value) {
  try { localStorage.setItem(prefix() + "steer:" + id, JSON.stringify(value)); return true; }
  catch { warn("待采纳补充未保存，本次不会发送；请先复制输入内容。"); return false; }
}

export function attachToDraft(token, image) {
  if (token.key === key) return false;
  try {
    const record = read(token.key), value = record?.value || token.value;
    persist(token.key, { ...value, images: [...(value.images || []), { ref: image.ref, name: image.name }] }, [record?.revision, ...(record?.ancestors || [])].filter(Boolean));
    return true;
  } catch { warn("上一份草稿的附件已上传，但保存引用失败，请重新选择文件。"); return true; }
}

// 一次性清理：ancestors 截错方向的旧版本留下了大量认不出祖先的页面副本，打开页面就误报冲突。
// 比主记录早写的副本当作已被取代，删掉；比主记录晚的（冲突时另存的）和没有主记录的照旧保留。清理过一次就记下，不再执行。
const CLEANED = "tern-draft-v1-copies-cleaned";
function cleanStaleCopies() {
  try {
    if (localStorage.getItem(CLEANED)) return;
    const stale = [];
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i), at = k?.startsWith("tern-draft-v1:") ? k.indexOf(":copy:") : -1;
      if (at < 0) continue;
      const main = read(k.slice(0, at)), copy = read(k);
      if (main && (!copy || copy.at <= main.at)) stale.push(k);
    }
    stale.forEach((k) => localStorage.removeItem(k));
    localStorage.setItem(CLEANED, String(Date.now()));
  } catch { /* 读不了存储时不清理，草稿照旧按原样处理 */ }
}
export function initDrafts() {
  cleanStaleCopies();
  $("query").addEventListener("input", saveDraft);
  for (const id of ["provider", "model", "perm", "effort"]) $(id).addEventListener("change", saveDraft);
  $("workspace").addEventListener("change", () => { if (!ui.run && !$("workspace").value.startsWith("__add_")) switchDraft(null); });
  window.addEventListener("pagehide", saveDraft);
  window.addEventListener("beforeunload", (event) => { saveDraft(); if (unsafe) { event.preventDefault(); event.returnValue = ""; } });
  window.addEventListener("storage", (event) => {
    if (event.key !== key || !key) return;
    try {
      const remote = read(key);
      if (!remote || remote.author === author) return;
      if (signature(readDraftInput()) === signature(remote.value)) { base = remote; return; }
      if (!localEdited) {
        base = remote; restoring = true; writeDraftInput(remote.value); restoring = false;
      } else conflict(remote);
    } catch { restoring = false; warn("另一页面更新了草稿，但读取失败。"); }
  });
}
