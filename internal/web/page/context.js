import { $, el, ui } from "./util.js";
import { saveDraft } from "./drafts.js";

const dialog = el("dialog", "context-dialog resource-dialog"), title = el("h2", "", "上下文与用量"), metrics = el("div"), note = el("p", "meta"), compact = el("button", "ghost"), close = el("button", "ghost", "关闭");
title.id = "context-title"; dialog.setAttribute("aria-labelledby", title.id);
compact.type = close.type = "button";
dialog.append(title, metrics, note, compact, close);
document.body.append(dialog);
let run, focus;
function refresh() {
  if (!dialog.open) return;
  if (ui.run !== run || !run) { dialog.close(); return; }
  const calls = [...run.calls, ...run.aux, ...Object.values(run.subs).flatMap((s) => [...s.calls, ...s.aux])];
  const last = run.calls.findLast((c) => !c.compact && c.usage?.prompt_tokens), capacity = run.inputCapacity;
  const total = calls.reduce((n,c) => n + Number(c.usage?.total_tokens ?? (Number(c.usage?.prompt_tokens || 0) + Number(c.usage?.completion_tokens || 0))), 0);
  const missing = calls.filter((c) => c.end && !Object.keys(c.usage || {}).length).length;
  const compression = run.calls.findLast((c) => c.compact);
  const state = ui.compactNext ? "已准备：下一条消息发送前压缩，可取消"
    : !compression ? "尚无压缩记录" : !compression.end ? "压缩中" : compression.choice && !compression.invalidSummary && compression.status === 200 ? "压缩模型已返回；有效上下文以随后请求为准" : "压缩失败，查看原始历史与错误";
  metrics.replaceChildren(
    el("p", "", `实际模型：${run.provider || "未记录"} / ${run.model || "未记录"}`),
    el("p", "", `最近输入：${last ? last.usage.prompt_tokens.toLocaleString() + " tokens" : "供应商未报告"} · 输入容量：${capacity ? capacity.toLocaleString() + " tokens（运行实例报告）" : "未知"}`),
    el("p", "meta", `用量更新时间：${last?.end ? new Date(last.end).toLocaleString() : "未知"}。上下文占用为最近一次请求，压缩后的值要等下一次请求报告。`),
    el("p", "", `任务累计已报告：${total.toLocaleString()} tokens（主任务、子任务及辅助调用）${missing ? `；${missing} 次结束的请求缺少用量` : ""}`),
    el("p", "", `每轮模型请求上限：${run.maxSteps > 0 ? run.maxSteps : "未设置"} · 账户额度与费用：供应商未提供`),
    el("p", "", `压缩：${state}`),
  );
  compact.textContent = ui.compactNext ? "取消准备压缩" : "下一条消息发送前压缩";
  compact.disabled = !ui.compactNext && (!run.exit || ui.loading || !!run.source || !run.canContinue || ui.sending);
  note.textContent = "压缩在服务端轮次锁内随下一次明确发送启动，不会立即调用模型。保留原始对话历史；可先继续编辑草稿。压缩不改变模型、权限或预算。";
}
compact.onclick = () => { ui.compactNext = !ui.compactNext; saveDraft(); refresh(); };
close.onclick = () => dialog.close();
dialog.addEventListener("close", () => focus?.focus?.());
function open() { if (!ui.run) return; run = ui.run; focus = document.activeElement; dialog.showModal(); refresh(); close.focus(); $("ctx-tip").hidden = true; }
$("ctx-ring").onclick = open;
$("ctx-ring").onkeydown = (event) => { if (["Enter", " "].includes(event.key) && !event.isComposing) { event.preventDefault(); open(); } };
setInterval(refresh, 1000);
