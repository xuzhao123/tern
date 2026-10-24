import { HOST, el, textOf } from "./util.js";
// ---------- 导出对话 ----------
// 同 Codex 的 /export：整条对话在页面里拼成 Markdown 再下载，不经服务端。
// 导出的是人要贴到别处的文字：每轮的问题、运行中的插话、用到的工具与次数、回答的 Markdown 原文；
// 工具的参数与结果不进文件（要看就打开这条对话）。撤销掉的轮次不导出；分叉带过来的照常导出，标题后面注明。
const title = (r) => r.name || (r.query ? textOf({ content: r.query }) : "") || r.id;
// 回答：这一轮里最后一次不是“过程说明”的正文。和对话页一样，带工具调用的那几段、agent 标明是
// commentary 的是过程，不是回答（见 chat.js 的 renderCall）。
function answer(r, turn) {
  const calls = r.calls.filter((c) => c.turn === turn && !c.compact);
  for (let i = calls.length - 1; i >= 0; i--) {
    const m = (calls[i].choice && calls[i].choice.message) || {};
    if (m.content && !(calls[i].tools.length || calls[i].phase === "commentary")) return m.content;
  }
  return "";
}
// 工具只列名字和次数，按第一次用到的顺序。
function usedTools(r, turn) {
  const counts = new Map();
  for (const c of r.calls) {
    if (c.turn !== turn) continue;
    for (const t of c.tools) counts.set(t.name, (counts.get(t.name) || 0) + 1);
  }
  return [...counts].map(([name, n]) => `${name} × ${n}`).join("、");
}
// 插话记在它排进的那次请求之前（steers.call 是请求序号），按那次请求属于哪一轮归到那一轮。
function steersOf(r, turn) {
  return r.steers.filter((s) => r.calls.find((c) => c.i === s.call)?.turn === turn).map((s) => s.text);
}
function markdown(r) {
  const lines = [
    `# ${title(r)}`,
    "",
    `- 对话 ID：${r.id}`,
    `- 主机：${HOST || "本机"}；工作区：${r.workspace || "无"}；模型：${r.model || "按供应商默认"}`,
    `- 导出时间：${new Date().toLocaleString("zh-CN", { hour12: false })}`,
  ];
  for (const turn of r.turns) {
    if (turn.rewound) continue;
    lines.push("", `## 第 ${turn.n} 轮${turn.inherited ? "（分叉前）" : ""}`, "", `**问：** ${turn.question || ""}`);
    for (const text of steersOf(r, turn.n)) lines.push("", `（运行中补充：${text}）`);
    const tools = usedTools(r, turn.n);
    if (tools) lines.push("", `**用到的工具：** ${tools}`);
    lines.push("", "**答：**", "", answer(r, turn.n) || "（这一轮没有回答）");
  }
  return lines.join("\n") + "\n";
}
// 文件名：名字或第一句话的前 30 个字，去掉文件名里不能用的字符；都没有时用对话 ID。
const BAD_FILE_CHARS = /[\\/:*?"<>|\x00-\x1f]/g;
function fileName(r) {
  const base = [...String(title(r))].slice(0, 30).join("").replace(/\s+/g, " ").replace(BAD_FILE_CHARS, "").trim();
  return (base || `对话-${r.id.slice(0, 8)}`) + ".md";
}
export function exportRun(r) {
  if (!r) return;
  const url = URL.createObjectURL(new Blob([markdown(r)], { type: "text/markdown;charset=utf-8" }));
  const a = el("a");
  a.href = url;
  a.download = fileName(r);
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
