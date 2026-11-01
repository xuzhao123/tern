import { $, HOST, api, el, ui } from "./util.js";
import { archiveRun, editGoal, forkTurn, newChat, pinRun, renameRun, rewindTurn, sideQuestion, syncComposer, autosize } from "./shell.js";
import { openMCP, openMemory, openSkills } from "./pages.js";
import { openDiff } from "./diff.js";
import { exportRun } from "./export.js";
import { openWorkTab } from "./workpanel.js";
import { openContext } from "./context.js";
import { addSkill, skillUsable } from "./skillrefs.js";
import { saveDraft } from "./drafts.js";

// 输入框的 / 菜单（参照 Codex 应用，见 spec/web-slash-commands.md）。三组：
// 界面命令在页面里执行、不发给模型；提示词命令是 internal/commands 的模板，整句发给 agent 展开；skill 选了加进引用条。
// 界面命令的 when 返回不能用的原因（能用返回空串），条件照抄对应按钮原来的禁用条件。

const entry = () => ui.taskCatalog?.find((e) => e.host.alias === HOST && e.task.id === ui.run?.id);
// 最后一轮能分叉、能撤销的检查点：条件同每轮下面的“分叉”“撤销”按钮（chat.js）。
const forkable = (r) => [...(r?.turns || [])].reverse().find((t) => t.checkpoint && !t.rewound);
const rewindable = (r) => [...(r?.turns || [])].reverse().find((t) => t.checkpoint && !t.rewound && !t.inherited);
const idle = (r) => !r ? "新任务还没有对话" : r.source ? "导入的对话只能查看" : !r.exit || ui.loading || ui.replay ? "这一轮跑完才能用" : "";
const lastAnswer = (r) => r?.calls.filter((c) => !c.compact && c.choice?.message?.content && !c.tools.length).at(-1)?.choice.message.content;
function note(text) { ui.hint = text; $("hint").textContent = text; }
function openModelMenu(focus) {
  const menu = $("composer-model-info"); if (!menu || menu.hidden) return false;
  menu.open = true; setTimeout(() => (focus ? $(focus) : menu.querySelector("select, button"))?.focus(), 0); return true;
}
function pick(id) { const node = $(id); node.focus(); try { node.showPicker?.(); } catch {} }
// 带参数的命令：参数可选的（[...]）不带也能执行；必填的写成 <...>。
const COMMANDS = [
  { name: "new", desc: "新任务", when: () => "", run: () => newChat() },
  { name: "compact", args: "[下一条消息]", desc: "下一条消息发出前先压缩上下文；带上文字就直接把它发出去",
    when: (r) => idle(r) || (!r.canContinue ? "这条对话不能续聊" : ""),
    run: (r, arg) => { ui.compactNext = true; saveDraft(); if (!arg) { note("下一条消息发出前会先压缩上下文"); return; } send(arg); } },
  { name: "fork", desc: "从最后一轮分叉出一条新对话，原对话不变",
    when: (r) => idle(r) || (r.eval ? "评测的试次不能分叉" : !forkable(r) ? "还没有可以分叉的轮次" : ""), run: (r) => forkTurn(r, forkable(r)) },
  { name: "side", args: "[问题]", desc: "旁支提问：临时分出去问一句，问答不进这条对话",
    when: (r) => idle(r) || (r.eval ? "评测的试次不能分叉" : ""), run: (r, arg) => sideQuestion(arg) },
  { name: "rewind", desc: "撤销最后一轮（可选退文件、退对话或一起退）",
    when: (r) => idle(r) || (!rewindable(r) ? "没有可以撤销的轮次" : ""), run: (r) => rewindTurn(r, rewindable(r)) },
  { name: "goal", args: "[目标]", desc: "设这条对话的目标；留空就打开编辑",
    when: (r) => !r ? "新任务请在发送后再设目标" : r.source || r.eval ? "这条对话不能设目标" : "",
    run: (r, arg) => editGoal(arg || undefined) },
  { name: "plan", args: "[提示]", desc: "切到只读计划模式；带上文字就直接发出去",
    when: () => $("perm").hidden ? "这条对话不能换权限" : $("perm").disabled ? "这一轮跑完才能换权限" : "",
    run: (r, arg) => { $("perm").value = "plan"; $("perm").dispatchEvent(new Event("change")); if (arg) send(arg); else note("已切到只读计划：下一条消息只分析和出计划，不改文件"); } },
  { name: "model", desc: "选模型（已有对话固定一个模型，换模型请新建任务）",
    when: () => $("composer-model-info")?.hidden ? "导入的对话不能选模型" : "",
    run: (r) => { openModelMenu(); if (r) note(`这条对话固定用 ${r.model || "开始时的模型"}；换模型请用菜单里的“使用其他模型新建任务”`); } },
  { name: "reasoning", desc: "选推理强度（每轮可以换）",
    when: () => $("effort").hidden ? "当前模型不支持选推理强度" : "", run: () => openModelMenu("effort") },
  { name: "permissions", desc: "选权限：请求批准、帮我批准、完全访问",
    when: () => $("perm").hidden ? "这条对话不能换权限" : $("perm").disabled ? "这一轮跑完才能换权限" : "", run: () => pick("perm") },
  { name: "project", desc: "选新任务的项目",
    when: (r) => r ? "已有对话的项目不能换" : $("workspace").hidden ? "编码工具没打开，不能选项目" : "", run: () => pick("workspace") },
  { name: "status", desc: "对话 ID、模型、权限、执行位置与上下文用量",
    when: (r) => !r ? "新任务还没有状态" : "", run: () => openContext() },
  { name: "rename", args: "[名字]", desc: "给这条对话改名；留空就打开编辑",
    when: () => !entry() ? "新任务还没有对话" : "", run: (r, arg) => { const e = entry(); return renameRun(e.task, e.host, arg || undefined).then((ok) => ok && arg && note("已改名")); } },
  { name: "archive", desc: "归档或取消归档这条对话",
    when: () => !entry() ? "新任务还没有对话" : "", run: () => { const e = entry(), was = e.task.archived; return archiveRun(e.task, e.host).then((ok) => ok && note(was ? "已取消归档" : "已归档")); } },
  { name: "pin", desc: "置顶或取消置顶这条对话",
    when: () => !entry() ? "新任务还没有对话" : "", run: () => { const e = entry(), was = e.task.pinned; return pinRun(e.task, e.host).then((ok) => ok && note(was ? "已取消置顶" : "已置顶")); } },
  { name: "diff", desc: "在右侧打开工作目录的改动",
    when: (r) => !r ? "新任务还没有改动" : !r.workspace ? "这条对话没有项目目录" : "", run: (r) => openDiff(r, [], "workspace") },
  { name: "copy", desc: "复制最后一轮回答的 Markdown 原文",
    when: (r) => !lastAnswer(r) ? "还没有可以复制的回答" : "",
    run: async (r) => { try { await navigator.clipboard.writeText(lastAnswer(r)); note("已复制最后一轮回答"); } catch { note("复制不了：浏览器没有给剪贴板权限"); } } },
  { name: "export", desc: "把这条对话导出成 Markdown 文件", when: (r) => !r ? "新任务还没有对话" : "", run: (r) => exportRun(r) },
  { name: "ps", desc: "看模型起的后台命令", when: (r) => !r?.jobs?.length ? "这条对话还没有后台命令" : "", run: () => openWorkTab("jobs", "", "后台命令") },
  { name: "mcp", desc: "MCP 连接与授权", when: () => ui.sending ? "正在发送" : "", run: () => openMCP() },
  { name: "skills", desc: "Skills 中心", when: () => ui.sending ? "正在发送" : "", run: () => openSkills() },
  { name: "memories", desc: "长期记忆", when: () => ui.sending ? "正在发送" : "", run: () => openMemory() },
  { name: "help", desc: "列出全部命令", when: () => "", run: () => { $("query").value = "/"; $("query").focus(); renderSlash(); } },
];
const byName = new Map(COMMANDS.map((c) => [c.name, c]));
// 带参数的界面命令补上参数后照常发送（/compact、/plan 带文字时）：走表单提交，附件、skill 引用照常带上。
// 要等这次提交事件结束再提交：在提交事件里再 requestSubmit，浏览器会忽略。
function send(text) { $("query").value = text; autosize(); syncComposer(); setTimeout(() => $("composer").requestSubmit(), 0); }

// 整句是“/名字”或“/名字 参数”、名字又是界面命令时在这里执行，返回 true；否则返回 false，照原来发出去。
export function runSlash(text) {
  const m = /^\/([a-z-]+)(?:\s+([\s\S]*))?$/.exec(text.trim()), cmd = m && byName.get(m[1]);
  if (!cmd) return false;
  const why = cmd.when(ui.run);
  if (why) { note(`/${cmd.name} 现在用不了：${why}`); return true; }
  $("query").value = ""; autosize(); syncComposer(); saveDraft(); $("slash").hidden = true;
  Promise.resolve(cmd.run(ui.run, (m[2] || "").trim())).catch((err) => note(`/${cmd.name} 失败：${err.message}`));
  return true;
}

// ---------- 菜单 ----------
let items = [], active = 0, skillCache = null;
async function loadPrompts() {
  const ws = ui.run ? ui.run.workspace || "" : $("workspace").value;
  if (ui.commands && ui.commandsFor === ws) return;
  try { const res = await fetch(api("/commands?workspace=" + encodeURIComponent(ws))); ui.commands = res.ok ? await res.json() : []; } catch { ui.commands = []; }
  ui.commandsFor = ws;
}
async function loadSkills() {
  const key = `${ui.account}\0${HOST}`;
  if (skillCache?.key === key) return skillCache.list;
  try { const res = await fetch(api("/hub")); skillCache = { key, list: res.ok ? (await res.json()).skills || [] : [] }; } catch { skillCache = { key, list: [] }; }
  return skillCache.list;
}
$("workspace").addEventListener("change", () => { ui.commands = null; });
const matches = (q, name, desc) => !q || name.startsWith(q) || (desc || "").includes(q);
// 光标前只有 /前缀（没有空格）时打开；q 可以是中文，按说明里包含来筛（“/压缩”找得到 /compact）。
export async function renderSlash() {
  const box = $("slash"), m = /^\/(\S*)$/.exec($("query").value);
  if (!m) { box.hidden = true; items = []; return; }
  const q = m[1], r = ui.run;
  await Promise.all([loadPrompts(), loadSkills()]);
  if ($("query").value !== "/" + q) return; // 等列表时又敲了字：以最后一次为准
  const scope = { builtin: "内置", user: "个人", project: "项目" };
  const groups = [
    ["操作", COMMANDS.filter((c) => matches(q, c.name, c.desc)).map((c) => ({ label: "/" + c.name + (c.args ? " " + c.args : ""), desc: c.desc, why: c.when(r), act: () => c.args ? fill(c.name) : runSlash("/" + c.name) }))],
    ["提示词", (ui.commands || []).filter((c) => matches(q, c.name, c.description)).map((c) => ({ label: "/" + c.name + (c.argument_hint ? " " + c.argument_hint : ""), desc: (scope[c.scope] ? scope[c.scope] + " · " : "") + (c.description || ""), why: byName.has(c.name) ? "和内置操作重名，改个文件名才能用" : "", act: () => fill(c.name) }))],
    ["Skill", (skillCache?.list || []).filter((s) => matches(q, s.name, s.description)).map((s) => ({ label: "$" + s.name, desc: s.description || "", why: skillUsable(s) ? "" : "当前任务没启用", act: () => { const why = addSkill(s); $("query").value = ""; autosize(); syncComposer(); box.hidden = true; note(why || `已加上 skill：${s.name}`); } }))],
  ];
  items = []; box.replaceChildren();
  for (const [title, all] of groups) {
    // 每组里能用的排在前面，用不了的（灰、带原因）放后面，新任务时一眼看到的是能做的。
    const list = [...all.filter((i) => !i.why), ...all.filter((i) => i.why)];
    if (!list.length) continue;
    box.append(el("div", "slash-group", title));
    for (const item of list) {
      const b = el("button", "slash-item"); b.type = "button"; b.disabled = !!item.why;
      b.append(el("span", "slash-name", item.label), el("span", "meta", item.why ? `${item.desc} · ${item.why}` : item.desc));
      b.onmousedown = (ev) => ev.preventDefault();
      b.onclick = () => choose(item);
      item.node = b; items.push(item); box.append(b);
    }
  }
  if (!items.length) box.append(el("div", "meta", "没有匹配的命令"));
  active = Math.max(0, items.findIndex((i) => !i.why));
  paint(); box.hidden = false;
}
function fill(name) { $("query").value = "/" + name + " "; $("slash").hidden = true; $("query").focus(); autosize(); syncComposer(); }
function choose(item) { if (!item.why) item.act(); }
function paint() { items.forEach((it, i) => it.node.classList.toggle("on", i === active)); items[active]?.node.scrollIntoView({ block: "nearest" }); }
// 菜单开着时：上下键选（跳过灰掉的），Enter / Tab 选中，Esc 关闭。返回 true 表示这一下按键归菜单。
export function slashKey(ev) {
  const box = $("slash");
  if (box.hidden || !items.length || ev.isComposing) return false;
  if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    const step = ev.key === "ArrowDown" ? 1 : -1;
    for (let i = 1; i <= items.length; i++) { const j = (active + step * i + items.length) % items.length; if (!items[j].why) { active = j; break; } }
    paint(); return true;
  }
  if ((ev.key === "Enter" && !ev.shiftKey) || ev.key === "Tab") { const item = items[active]; if (item && !item.why) choose(item); return true; }
  if (ev.key === "Escape") { box.hidden = true; return true; }
  return false;
}
