import { SOURCE_LABEL, endLabel } from "./analysis.js";
import { renderChat } from "./chat.js";
import { backToInput, renderSteers, start } from "./composer.js";
import { openEvals } from "./eval.js";
import { exportRun } from "./export.js";
import { show, startReplay, stopReplay, syncRunURL } from "./follow.js";
import { openCompare, select } from "./observe.js";
import { pickWorkspace } from "./pages.js";
import { render } from "./render.js";
import { apply, newRun } from "./runs.js";
import { $, HOST, HOST_STATE, MAX_IMAGES, api, clip, el, hostAPI, hostURL, markSeen, notifyOn, runKey, save, seenAt, ui } from "./util.js";
// 工作区下拉的最后一项：选了它就去浏览目录登记一个新的。值不会和工作区名撞上（名字是小写字母或数字开头的目录名）。
const ADD_WORKSPACE = "__add_workspace__", ADD_WORKSPACE_LABEL = "＋ 添加工作区…";
// 左侧列表按工作区筛：只按名字筛（summary.workspace），工作区改名前的对话还在旧名字那一栏；
// WS_NONE 是“无工作区”（工作区名字都以小写字母或数字开头，不会撞上）。
const WS_FILTER_KEY = "tern-ws-filter", WS_NONE = "__none__";
let wsFilter = (() => { try { return localStorage.getItem(WS_FILTER_KEY) || ""; } catch { return ""; } })();
// ---------- 外壳：顶栏、输入框与对话列表 ----------
export function renderShell() {
  const r = ui.run, chat = ui.page === "chat";
  $("chat-pane").hidden = !chat;
  if (!chat) $("subpanel").hidden = true;
  $("observe-pane").hidden = chat;
  $("page-chat").className = chat ? "on" : "";
  $("page-observe").className = chat ? "" : "on";
  $("cmp-hint").hidden = chat;
  syncRunSearch();
  $("title").textContent = ui.tab === "compare" && !chat ? `对比 ${ui.checked.size} 条运行` : r ? r.query || r.id : "新对话";
  $("files-btn").hidden = !chat || !r?.sandbox;
  $("files-btn").classList.toggle("on", !!ui.filesOpen);
  $("export-btn").hidden = !chat || !r;
  // 旁支提问：对话停下时才能问，运行中灰掉（见 sideQuestion）；导入的对话与评测的试次不能分叉，不显示。
  const sideBusy = !!r && (!r.exit || ui.loading || !!ui.replay);
  $("side-btn").hidden = !chat || !r || !!r.source || !!r.eval;
  $("side-btn").disabled = sideBusy;
  $("side-btn").title = sideBusy ? "这一轮跑完才能问旁支问题" : "从这里临时分出去问一句，问答不进这条对话";
  $("diff-btn").hidden = !chat || !r?.workspace;
  $("diff-btn").classList.toggle("on", !!ui.diffOpen);
  // 副本对话：停下以后可以把副本里的改动应用回原目录（见 workspace/applyCopy）；跑着的时候灰着。
  $("apply-copy-btn").hidden = !chat || !r?.copy;
  $("apply-copy-btn").disabled = !r?.exit || !!ui.loading || !!ui.replay;
  $("apply-copy-btn").textContent = r?.copy ? `应用到 ${r.workspace || "原目录"}` : "";
  $("apply-copy-btn").title = r?.exit ? "把副本里的改动写进原目录的工作区，不提交" : "这一轮跑完才能应用";
  const runID = $("run-id");
  runID.hidden = !r;
  runID.textContent = r ? r.id.slice(0, 8) : "";
  runID.title = r ? `对话 ID：${r.id}，点击复制` : "";
  // 标题下面一行是这条对话的目标（summary.goal）：导入的对话与评测的试次不显示，点了改见 editGoal。
  const goal = $("goal"), goalText = r?.goal || "";
  goal.hidden = !chat || !r || !!r.source || !!r.eval;
  goal.classList.toggle("empty", !goalText);
  $("goal-text").textContent = goalText ? "目标：" + goalText : "＋ 定个目标";
  $("goal-edit").hidden = !goalText;
  document.title = (ui.waiting ? `(${ui.waiting}) ` : "") + (r ? `tern · ${r.id}` : "tern");
  const status = $("status");
  status.className = r && r.exit && r.exit !== "exit 0" ? "bad" : r && !r.exit ? "run" : "";
  const nSub = r ? Object.values(r.subs).reduce((n, s) => n + s.calls.length, 0) : 0;
  const calls = r ? `${r.calls.length + r.aux.length + nSub} 次请求` : "";
  // 正常完成不显示状态（同 Claude Code）；请求次数放在悬停提示里。
  status.textContent = !r ? "" : ui.replay ? "回放中" : !r.exit ? (r.stopRequested ? "正在停止…" : "运行中") : r.exit === "exit 0" ? "" : r.stopRequested ? "已停止" : endLabel(r, true);
  status.title = calls;
}
// 工具在设置里常驻；新对话按设置显示，进行中的对话按它开始时的快照显示。
export const TOOL_INFO = {
  rag:["知识库检索", "search_docs：检索项目知识库（retrieval/corpus.md）。打开后系统提示词要求知识问题先查资料，所以默认关"],
  memory: ["长期记忆", "remember / search / forget_memory：新对话开头先召回，要多发几次模型请求，所以默认关"],
  skills: ["Skills", "Skills 中心里启用的 skill：常驻名字和用途，用到时再加载正文"],
  mcp: ["MCP", "MCP 中心里启用的 server，工具注册为 mcp_<名字>"],
  subagents: ["子 agent", "spawn_agent：独立上下文的子进程，继承这里的工具，不继承长期记忆"],
  ask_user: ["向你提问", "ask_user：模型在运行中向你提一个问题（可带选项），等你在对话里回答再继续"],
  bash: ["Bash 沙箱", "bash：bubblewrap 沙箱，默认断网，联网逐次审批"],
  web_search: ["必应搜索", "web_search：无头 Chrome 打开必应搜索。必应多半不给自动化浏览器结果，搜索交给 DeepSeek，所以默认关"],
  open_page: ["打开网页", "open_page：无头 Chrome 打开网页读正文，会执行页面脚本，适合要渲染后才有内容的页面；只读"],
  ds_search:["DeepSeek 搜索", "ds_search / ds_fetch：DeepSeek 服务端搜索与抓取网页，按次计费，用 DEEPSEEK_API_KEY"],
  coding: ["编码工具", "read_file / glob / grep / edit_file / write_file：只在对话选的工作区里读写"],
};
function runTools(r) {
  // 单个工具按最近一次请求实际带的工具列表显示；还没发请求时按开始时的快照（disallowed_tools）。
  const sent = r.calls.at(-1)?.req.tools;
  const has = (name, group = true) => sent ? sent.some((t) => t.function?.name === name) : group && !r.source && !r.disallowedTools.includes(name);
  return { web_search: has("web_search", r.browser), open_page: has("open_page", r.browser),
    rag: r.rag || (sent || []).some((t) => t.function?.name === "search_docs"), memory: r.memory, skills: r.skills.length > 0, mcp: r.mcp.length > 0, subagents: r.subagents, ask_user: has("ask_user", r.askUser), bash: r.bash, ds_search: r.dsSearch, coding: !!r.workspace };
}
export function toolOn(key) {
  if (ui.run) return !!runTools(ui.run)[key];
  const s = ui.settings?.effective;
  if (!s || s.values[key] !== "on" || key in s.blocked) return false;
  return key !== "coding" || !!$("workspace").value;
}
export async function loadSettings() {
  try { const res = await fetch(api("/settings")); if (res.ok) ui.settings = await res.json(); } catch {}
  syncComposer();
}
// 第二级模型下拉的清单：路由表只在 llm 包里，页面从 /models 读它，前端不抄一份。
export async function loadModels() {
  try {
    const res = await fetch(api("/models"));
    if (res.ok) ui.models = new Map((await res.json()).map((p) => [p.name, p]));
  } catch {}
  syncComposer();
}
// 新对话没在输入框选具体模型时，agent 实际用的是设置里的默认模型（不属于这家供应商时退回供应商默认）。
export function settingsModel(provider) {
  const spec = ui.models?.get(provider || ""), model = ui.settings?.effective?.values.model || "";
  return model && (spec?.models || []).some((m) => m.name === model) ? model : "";
}
// 当前（或这条对话的）模型能不能看图：清单来自 /models，与 agent 用的是同一份路由表。
// model 为空表示用这家供应商的默认值；清单外的模型名（旧对话、.env 自填的）不知道就不拦。
export function modelVision(provider, model) {
  const spec = ui.models?.get(provider || "");
  if (!spec) return { name: model || "", vision: true };
  const name = model || spec.default;
  const found = (spec.models || []).find((m) => m.name === name);
  return { name, vision: found ? !!found.vision : true };
}
// 输入框右侧的上下文用量环（参考 Codex 客户端）。口径与轨迹页“上下文压力”一致：
// 最近一次主请求的服务端输入 tokens（system、工具定义与历史消息都在里面）占可用输入容量的比例。
// 压缩请求的输入同样是满上下文，不能拿它当用量：刚压缩完，环反而会显得更满。
const ctxRingLen = 2 * Math.PI * 11.5;
const ctxK = (n) => n >= 1000 ? +(n / 1000).toFixed(1) + "k" : String(Math.round(n)); // 去掉末尾的 .0：257024 → 257k，151234 → 151.2k
function updateCtxRing(r) {
  const ring = $("ctx-ring"), pct = ring.querySelector(".pct"), fill = ring.querySelector(".fill");
  const last = r && r.calls.filter((c) => !c.compact && c.usage && c.usage.prompt_tokens).at(-1);
  if (!last) { ring.hidden = true; ring._lines = null; return; }
  const used = last.usage.prompt_tokens, cap = Math.max(1, r.inputCapacity || ui.ctxWindow);
  const ratio = Math.max(0, Math.min(1, used / cap));
  const details = last.usage.prompt_tokens_details;
  const cached = (details && details.cached_tokens) || 0, hasCached = !!details && details.cached_tokens != null;
  ring.hidden = false;
  ring.className = ratio >= .9 ? "bad" : ratio >= .7 ? "warn" : "";
  fill.style.strokeDasharray = `${ratio * ctxRingLen} ${ctxRingLen}`;
  pct.textContent = Math.round(ratio * 100);
  // 悬浮详情：不用原生 title（SVG 子元素上不总弹出），存下来由下面的浮层显示。
  const percent = Math.round(ratio * 100);
  ring._lines = [
    "上下文窗口：",
    `${percent}% 已用（剩余 ${100 - percent}%）`,
    `已用 ${ctxK(used)} tokens，共 ${ctxK(cap)}`,
    `缓存命中率：${hasCached ? Math.round(100 * cached / used) + "%" : "—"}`,
  ];
  ring.setAttribute("aria-label", ring._lines.join("，"));
}
// 环的鼠标事件：自己画浮层，位置跟着环走；鼠标在 SVG 或数字上也算（它们 pointer-events: none）。
{
  const ring = $("ctx-ring"), tip = $("ctx-tip");
  ring.addEventListener("mouseenter", () => {
    if (ring.hidden || !ring._lines) return;
    tip.replaceChildren(el("b", "", ring._lines[0]), ...ring._lines.slice(1).map((line) => el("div", "", line)));
    tip.hidden = false;
    const box = ring.getBoundingClientRect(), h = tip.offsetHeight;
    // 输入框在页面底部：上方放不下时放到环下面，避免浮层被夹到屏幕顶部。
    const below = box.top - h - 8 < 8;
    tip.style.left = Math.max(8, Math.min(innerWidth - tip.offsetWidth - 8, box.left + box.width / 2 - tip.offsetWidth / 2)) + "px";
    tip.style.top = (below ? Math.min(innerHeight - h - 8, box.bottom + 8) : box.top - h - 8) + "px";
  });
  ring.addEventListener("mouseleave", () => { tip.hidden = true; });
}
export function syncComposer() {
  // 全部撤销之后（rewoundAll）服务端仍可能允许在这条对话里重新开始（can_continue），那就不是“坏了”；
  // can_continue 由 loadRuns 从对话列表里取（见那里）。
  const r = ui.run, busy = r && (ui.loading || !r.exit), broken = r && ((r.exit && r.exit !== "exit 0" && !r.checkpoint) || (r.rewoundAll && !r.canContinue));
  const s = ui.settings?.effective;
  ui.picked ||= {};
  // 供应商同样来自 /models（显示名在路由表里）；本机没配好的（缺密钥或地址）灰掉，悬停看原因。
  const ps = $("provider"), plist = [...(ui.models?.values() || [])], psig = plist.map((p) => p.name + (p.error ? "!" : "")).join(",");
  if (ps.dataset.sig !== psig) {
    const keep = ps.value;
    ps.replaceChildren(...plist.map((p) => {
      const option = new Option((p.label || p.name) + (p.error ? "（未配置）" : ""), p.name);
      if (p.error) { option.disabled = true; option.title = p.error; }
      return option;
    }));
    ps.dataset.sig = psig;
    if (keep) ps.value = keep;
  }
  // 一条对话固定一个模型和一个工作区：续聊沿用开始时的选择（换模型请新建对话）。
  $("provider").disabled = !!r;
  if (r) $("provider").value = r.provider || "ark";
  else if (!ui.picked.provider && s) $("provider").value = s.values.provider;
  const prov = $("provider").value, spec = ui.models?.get(prov);
  const models = (spec?.models || []).map((m) => m.name);
  // 已有对话只显示它实际用的模型（agent 在 turn/started 里报的），它可以不在清单里：旧对话的带日期版本号的模型名、.env 里自填的名字；
  // 没记录到就不显示，不拿现在的默认值冒充。新对话列出清单，第一项“默认”标的是不选时 agent 实际会用的那个：
  // 设置里的默认具体模型，没设就用这家供应商的默认值（/models 按 .env 解析）。
  const fallback = settingsModel(prov) || spec?.default || "供应商默认";
  const opts = r ? (r.model ? [[r.model, r.model]] : []) : [["", "默认（" + fallback + "）"], ...models.map((m) => [m, m])];
  const pv = $("model"), msig = JSON.stringify(opts);
  if (pv.dataset.sig !== msig) {
    const keep = pv.value;
    pv.replaceChildren(...opts.map(([value, text]) => new Option(text, value)));
    pv.dataset.sig = msig;
    pv.value = opts.some(([value]) => value === keep) ? keep : (opts[0]?.[0] ?? "");
  }
  pv.disabled = !!r;
  pv.hidden = models.length < 2 || (!!r && !r.model);
  if (!r && !ui.picked.model) pv.value = "";
  pv.title = r ? "具体模型：对话开始后固定（换模型请新建对话）" : "具体模型：默认用这家供应商的默认值，清单来自路由表";
  const ws = $("workspace"), names = r ? (r.workspace ? [r.workspace] : []) : (s?.workspaces || []).map((w) => w.name);
  const signature = (r ? "run|" : "new|") + names.join("\n");
  if (ws.dataset.sig !== signature) {
    const keep = ws.value;
    // 新对话的最后一项是“＋ 添加工作区…”：选了它去浏览目录登记一个新的（见页尾的 change 监听）。
    const opts = [new Option("无工作区", ""), ...names.map((n) => new Option(n, n)), ...(r ? [] : [new Option(ADD_WORKSPACE_LABEL, ADD_WORKSPACE)])];
    ws.replaceChildren(...opts);
    ws.dataset.sig = signature;
    ws.value = names.includes(keep) ? keep : "";
  }
  const codingOK = !!s && s.values.coding === "on" && !("coding" in s.blocked);
  if (r) ws.value = r.workspace || "";
  else if (!ui.picked.workspace && s) ws.value = codingOK && names.includes(s.values.workspace) ? s.values.workspace : "";
  ws.disabled = !!r || !codingOK;
  // 编码工具可用时一直显示：没登记过也显示，人才能看到可以在这里添加。
  ws.hidden = !r && !codingOK;
  // 记下当前选中的那一项：选了“＋ 添加工作区…”又取消时退回这里（见页尾的 change 监听）。
  if (ws.value !== ADD_WORKSPACE) ws.dataset.prev = ws.value;
  ws.title = r ? "工作区：对话开始后固定" : !codingOK ? "编码工具没打开或不可用：" + (s?.blocked.coding || "在设置里打开") : "工作区：编码工具只在这个仓库里读写；对话开始后固定";
  // 权限模式每轮可以换：默认取这条对话上一轮的（新对话取设置）；用户改过就保持用户的选择，直到发送。
  if (!ui.picked.permission) $("perm").value = (r && r.permission) || s?.values.permission_mode || "edit";
  $("perm").disabled = !!busy || !!ui.replay;
  // 推理强度同样每轮可以换，默认同样取上一轮的（新对话取设置）。
  if (!ui.picked.effort) $("effort").value = (r && r.effort) || s?.values.reasoning_effort || "high";
  $("effort").disabled = !!busy || !!ui.replay;
  // 运行中发送键变成停止键（Codex 客户端的做法）：第一次优雅停止，第二次强制结束。
  const running = !!r && !r.exit && !ui.replay && (r.calls.length > 0 || r.logs.length > 0);
  $("send").classList.toggle("stop", running);
  $("send").classList.toggle("force", running && r.stopRequested);
  $("send").innerHTML = running ? '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="2" fill="currentColor" stroke="none"/></svg>' : '<svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M5 12l7-7 7 7"/></svg>';
  $("send").title = running ? (r.stopRequested ? "强制结束（SIGKILL）" : "停止（SIGINT：先回填结果、保存检查点）") : "发送";
  $("send").setAttribute("aria-label", running ? (r.stopRequested ? "强制结束" : "停止") : "发送");
  // “排队”只在运行中有意义（Tab 一样）：把输入框里的话排到这一轮结束后再发；没字时灰着。
  $("queue").hidden = !running;
  $("queue").disabled = !running || !$("query").value.trim();
  const imported = SOURCE_LABEL[r?.source];
  // 附图：一条对话的模型固定，能不能看图也固定。不能看图的模型、导入的对话与回放都不给附图；
  // 已经附了图又切到不能看图的模型时，发送前拦下，不自动删图。
  const vis = modelVision(prov, r ? r.model || "" : pv.value || settingsModel(prov));
  $("attach").disabled = !vis.vision || !!imported || !!ui.replay;
  $("attach").title = vis.vision ? `附图：PNG、JPEG、GIF、WebP，每条消息最多 ${MAX_IMAGES} 张；也可以粘贴或拖放`
    : `当前模型 ${vis.name} 不能看图，换一个能看图的模型再附图`;
  // 导入的对话只读：模型、工作区、权限与工具都是对方 agent 的，这里不给选择。
  for (const id of ["provider", "perm", "effort"]) $(id).hidden = !!imported;
  // 模型与工作区上面已按清单和对话决定是否显示，这里只在导入的对话里再藏起来，不能把它们重新显示出来。
  if (imported) $("model").hidden = $("workspace").hidden = true;
  // 副本（thread/start 的 copy）：选了工作区才能勾，开对话之前把副本建好，之后这条对话只碰副本。
  // 已有对话按它实际的（summary 的 copy）显示，并且不能再改。
  const cp = $("copy");
  cp.disabled = !!r || !!imported || !codingOK || !ws.value || ws.value === ADD_WORKSPACE;
  cp.checked = r ? !!r.copy : !!ui.picked.copy;
  $("copy-pick").hidden = ws.hidden || !!imported;
  $("query").disabled = !!imported;
  $("send").disabled = running ? false : ui.sending || !!busy || !!broken || !!ui.replay || !!imported || !$("query").value.trim();
  $("query").placeholder = imported ? "导入的对话只能查看" : running ? "补充或纠正，agent 做完这一步就会看到（Tab 排到这一轮之后） · Esc 打断" : r ? "继续提问" : "给 agent 布置一个任务，/ 选命令";
  // 输入框只放这一轮要选的（同 Claude Code）：工具、请求预算在设置页里看，这里只给导入的对话标出来源。
  $("mode-chip").textContent = imported ? `${imported} · ${r.calls.find((c) => c.res?.model)?.res.model || ""}` : "";
  // 只在需要人注意时提示；常规操作说明不放在输入框下面。
  // 发送失败的原因（ui.hint）优先：它要跟着重画留下来，直到打开对话、新对话时清掉。
  // 输入框以 ! 开头是人在沙箱里跑一条命令（composer.js 的 runShell）：提示行说清它做什么。
  const shellInput = $("query").value.trimStart().startsWith("!");
  $("hint").textContent = ui.hint || (shellInput ? (!r ? "先发一句话开个对话，再用 ! 跑命令" : !r.exit ? "这一轮在跑，等它结束再跑命令" : "! 在这条对话的沙箱里跑一条命令，输出下一轮模型也看得到") : !r ? "" : imported ? `从 ${imported} 导入的记录，只能查看；系统提示词、工具定义与耗时以记录里有的为准` : ui.replay ? "回放中，结束后可继续提问" :
    busy ? (r.stopRequested ? "正在停止：agent 回填结果、保存检查点后退出；再点一次强制结束" : "") : broken ? "上次运行未正常结束、也没有检查点，请新建对话" : "");
  updateCtxRing(r);
  // 待发送区跟着当前对话走：换了对话、重新订阅以后，显示的都是这条对话还没送出去的话。
  renderSteers();
}
// 列表显示最后一次活动的时间：服务端给 Unix 毫秒时间戳，这里按看的人所在的时区格式化。
function relTime(r) {
  const t = new Date(r.updated_at ?? r.created_at ?? NaN);
  if (isNaN(t)) return "";
  const p = (n) => String(n).padStart(2, "0");
  const today = new Date().toDateString() === t.toDateString();
  return (today ? "" : `${p(t.getMonth() + 1)}-${p(t.getDate())} `) + `${p(t.getHours())}:${p(t.getMinutes())}`;
}
// 左侧列表：只连一台主机时和原来一样；连了几台时按主机分组，当前主机排在最上面。
// 同一台的对话点了就地打开；别的主机的整页跳到那台的地址——页面状态（ui.run、各处的缓存）都假定只有一台主机。
export async function loadRuns() {
  const hosts = ui.hosts.length ? ui.hosts : [{ alias: HOST, host: HOST, remote: !!HOST, state: "connected" }];
  const mine = hosts.find((h) => h.alias === HOST);
  const groups = mine ? [mine, ...hosts.filter((h) => h !== mine)] : hosts;
  const lists = await Promise.all(groups.map(runList));
  // 正在看的对话被删了（在这个页面删的，或者在别的界面删的）：回到新对话页。
  if (mine && lists[0] && ui.run && !lists[0].some((r) => r.id === ui.run.id)) { newChat(); return; }
  // 排队的后续消息在服务端（summary.queue）：列表刷新时带回来交给待发送区；别的页面排的、删的这里也跟着变。
  const current = ui.run && lists[0]?.find((r) => r.id === ui.run.id);
  if (current) {
    // 我们这边的订阅已经随 exit 收尾了，服务端却还在跑：队列接着发了，或者别的页面续聊了——重新订阅看这一轮。
    if (!current.done && ui.run.exit && !ui.loading) { show(ui.run.id, false); return; }
    ui.run.queue = current.queue || [];
    ui.run.name = current.name || ""; // 列表里的名字（改过名才有）：导出时当标题
    // 目标（summary.goal）：标题下面那一行，别的页面改的、换了对话都照列表里的显示。
    if ((current.goal || "") !== (ui.run.goal || "")) { ui.run.goal = current.goal || ""; renderShell(); }
    // 旁支（summary.side）：横幅写从哪条分出来，原对话的名字或第一句话从同一份列表里取。
    const side = !!current.side, from = side && current.fork_of ? lists[0].find((x) => x.id === current.fork_of) : null;
    const forkName = from ? from.name || from.query || "" : "";
    if (side !== !!ui.run.side || (side && ui.run.forkName !== forkName)) {
      Object.assign(ui.run, { side, forkOf: side ? current.fork_of || "" : "", forkName });
      render(); // 横幅在对话最上面，重画一次；打开旁支时只变这一次
    }
    // 服务端说这条对话还能不能接着聊：撤销到第一轮之前以后仍为真（可以在同一条对话里重新开始），
    // 输入框据此判断要不要当成坏了（见 syncComposer 的 broken）。
    if (ui.run.canContinue !== !!current.can_continue) {
      ui.run.canContinue = !!current.can_continue;
      syncComposer();
    }
    // 副本对话（summary.copy）：顶栏给“应用到”，输入框的勾选框按这条对话显示；打开时只变这一次。
    if (!!ui.run.copy !== !!current.copy) { ui.run.copy = !!current.copy; render(); }
    renderSteers();
  }
  // 各条的 updated_at 留着记“看过”（markCurrentSeen、runRow 都读它）：先把正在看的这条记成看过，
  // 它的行尾这次就不出未读圆点。
  const updated = {};
  groups.forEach((h, i) => { for (const r of lists[i] || []) updated[runKey(h.alias, r.id)] = r.updated_at || 0; });
  ui.updated = updated;
  markCurrentSeen();
  // 标签页标题的条数：在等审批、等回答的对话（评测的试次不进列表，也不算）。
  const waiting = lists.reduce((n, list) => n + (list || []).filter((r) => r.waiting && !r.eval).length, 0);
  if (waiting !== ui.waiting) { ui.waiting = waiting; renderShell(); }
  // 有搜索词时列表是搜索结果：这一轮刷新不把它画回普通列表，清空搜索框时再画（见 clearSearch）。
  if (!searchText) {
    syncWsFilter(lists);
    $("runs").replaceChildren(...groups.map((h, i) => groupRows(h, lists[i], groups.length > 1)));
  }
  notifyChanges(groups, lists);
}
// 筛选下拉的选项：列表里出现过的工作区名字加上设置里登记的，按名字排，两头是“全部工作区”“无工作区”。
// 选的那个已经不在了（改名、移除、对话都删了）就回到“全部”。
function syncWsFilter(lists) {
  const select = $("run-filter"), names = new Set((ui.settings?.effective?.workspaces || []).map((w) => w.name));
  for (const list of lists) for (const r of list || []) if (r.workspace && !r.eval) names.add(r.workspace);
  const sorted = [...names].sort((a, b) => a.localeCompare(b));
  if (wsFilter !== "" && wsFilter !== WS_NONE && !names.has(wsFilter)) setWsFilter("");
  const signature = sorted.join("\n") + "|" + wsFilter;
  if (select.dataset.sig === signature) return;
  select.replaceChildren(new Option("全部工作区", ""), ...sorted.map((n) => new Option(n, n)), new Option("无工作区", WS_NONE));
  select.value = wsFilter;
  select.dataset.sig = signature;
}
function setWsFilter(value) {
  wsFilter = value;
  save(WS_FILTER_KEY, value);
}
// 这一条在这一栏里吗：选了“无工作区”只留没有工作区的对话，选了名字只留名字相同的（只比名字）。
function wsKeep(r) {
  return wsFilter === "" || (wsFilter === WS_NONE ? !r.workspace : r.workspace === wsFilter);
}
// 正在看的对话、页面又在前台，就算看过了。页面在后台时不记：切回前台时 main.js 补记一次，
// 后台跑完的那些才留得住未读圆点和通知。
export function markCurrentSeen() {
  const r = ui.run;
  if (!r || document.visibilityState !== "visible") return;
  markSeen(HOST, r.id, ui.updated[runKey(HOST, r.id)]);
}
// 和上一次的列表比：原来没跑完、现在跑完了，或者刚开始等审批、等回答的，页面不在前台时发一条浏览器通知。
// 第一次取列表只记下状态，不通知；同一条对话同一种通知带同一个 tag，浏览器会合并。
function notifyChanges(groups, lists) {
  const first = !ui.runStates, states = {};
  groups.forEach((h, i) => {
    for (const r of lists[i] || []) {
      if (r.eval) continue;
      const key = runKey(h.alias, r.id), prev = ui.runStates?.[key], title = clip(r.name || r.query || "（无问题）", 40);
      states[key] = { done: !!r.done, waiting: r.waiting || "" };
      if (first || !prev) continue; // 页面打开后才出现的对话：第一次见到它，只记状态
      if (!prev.waiting && r.waiting) notifyOne(h, r, (r.waiting === "approval" ? "等你审批：" : "等你回答：") + title, r.waiting);
      else if (!prev.done && r.done) notifyOne(h, r, "跑完了：" + title, "done");
    }
  });
  ui.runStates = states;
}
function notifyOne(h, r, title, kind) {
  if (!notifyOn() || typeof Notification === "undefined" || Notification.permission !== "granted" || document.visibilityState === "visible") return;
  const notice = new Notification(title, { tag: `${runKey(h.alias, r.id)}|${kind}` });
  notice.onclick = () => {
    window.focus();
    notice.close();
    if (h.alias === HOST) show(r.id); else location.href = hostURL(h.alias, r.id);
  };
}
// runList 是一台主机的对话列表：取不到（在重连、连不上）就是 null，那一组只显示标题和状态。
async function runList(h) {
  if (h.state === "failed") return null;
  try {
    const res = await fetch(h.alias === HOST ? api("/runs") : hostAPI(h.alias, "/runs"));
    return res.ok ? await res.json() : null;
  } catch { return null; }
}
// 一组：标题写“主机 · 状态”，下面是那台的对话；只有一台主机时不出标题，也不加分组的框。
// 已归档的平时不展开：列表最后一行“已归档（N）”，点开才显示在下面。
function groupRows(h, runs, grouped) {
  const box = el("div", grouped ? "host-group" : "");
  if (grouped) {
    const name = el("div", "host-name" + (h.state === "connected" ? "" : " bad"), `${h.alias || "本机"} · ${HOST_STATE[h.state] || h.state}`);
    name.title = h.error || "";
    box.append(name);
  }
  const list = (runs || []).filter((r) => !r.eval && wsKeep(r)).reverse();
  const rows = list.filter((r) => !r.archived).map((r) => runRow(r, h));
  if (rows.length) box.append(...rows);
  const archived = list.filter((r) => r.archived);
  if (archived.length) box.append(archivedGroup(archived, h));
  return box;
}
// 展开状态按主机记：每次收到通知列表都会重画，展开的还保持展开。
const archivedOpen = new Set();
function archivedGroup(archived, h) {
  const box = el("div", "archived"), open = archivedOpen.has(h.alias);
  const toggle = el("button", "archived-toggle", `已归档（${archived.length}）`);
  toggle.type = "button";
  toggle.setAttribute("aria-expanded", String(open));
  if (open) toggle.classList.add("on");
  const list = el("div", "archived-list");
  list.hidden = !open;
  list.append(...archived.map((r) => runRow(r, h, true)));
  toggle.onclick = () => {
    if (list.hidden) archivedOpen.add(h.alias); else archivedOpen.delete(h.alias);
    list.hidden = !list.hidden;
    toggle.classList.toggle("on", !list.hidden);
    toggle.setAttribute("aria-expanded", String(!list.hidden));
  };
  box.append(toggle, list);
  return box;
}
// 每行右边的“⋯”菜单：改名、归档（已归档的是“取消归档”）、删除。点别处或滚动列表就关掉。
let rowMenu = null;
function closeRowMenus() {
  if (!rowMenu) return;
  rowMenu.hidden = true;
  rowMenu = null;
}
function runMenu(r, h) {
  const box = el("div", "runmenu"), more = el("button", "more", "⋯");
  more.type = "button";
  more.title = "改名、归档、删除";
  more.setAttribute("aria-label", "对话操作");
  const menu = el("div", "runmenu-box");
  menu.hidden = true;
  const item = (text, act) => {
    const button = el("button", "", text);
    button.type = "button";
    button.onclick = (ev) => { ev.stopPropagation(); closeRowMenus(); act(); };
    return button;
  };
  menu.append(item("改名", () => renameRun(r, h)), item(r.archived ? "取消归档" : "归档", () => archiveRun(r, h)), item("删除", () => deleteRun(r, h)));
  more.onclick = (ev) => {
    ev.stopPropagation();
    const opened = rowMenu === menu;
    closeRowMenus();
    if (opened) return;
    // 菜单用 fixed 按这一行的位置摆：列表自己会滚动，外框会把里面的绝对定位元素裁掉。
    const at = more.getBoundingClientRect();
    menu.hidden = false;
    menu.style.left = Math.max(8, Math.min(innerWidth - menu.offsetWidth - 8, at.right - menu.offsetWidth)) + "px";
    menu.style.top = Math.min(innerHeight - menu.offsetHeight - 8, at.bottom + 4) + "px";
    rowMenu = menu;
  };
  menu.onclick = (ev) => ev.stopPropagation();
  box.append(more, menu);
  return box;
}
document.addEventListener("click", closeRowMenus);
window.addEventListener("scroll", closeRowMenus, true);
// 改名、归档、删除都发一次请求，成功后重新加载列表；删的正好是正在看的那条就回到新对话页。
async function threadCall(h, path, method, body) {
  try {
    const res = await fetch(hostAPI(h.alias, path), { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    if (res.ok) return true;
    alert((await res.text()).trim() || `请求失败（${res.status}）`);
  } catch (err) { alert("请求失败：" + err.message); }
  return false;
}
async function renameRun(r, h) {
  const name = prompt("这条对话的名字（留空就显示第一句话）：", r.name || r.query || "");
  if (name === null) return;
  if (await threadCall(h, `/runs/${encodeURIComponent(r.id)}/name`, "PUT", { name })) loadRuns();
}
// 目标：点标题下面那一行改，默认值是现在的目标，留空就是清除（见 thread/goal/set）。
// 改的是下一轮开始时告诉模型的，这一轮不打断，所以这里只更新那一行。
async function editGoal() {
  const r = ui.run;
  if (!r) return;
  const text = prompt("这条对话的目标（留空就是清除）：", r.goal || "");
  if (text === null) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/goal`), { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text }) });
    if (!res.ok) { alert((await res.text()).trim() || `请求失败（${res.status}）`); return; }
    const out = await res.json();
    if (ui.run?.id !== r.id) return;
    ui.run.goal = out.goal || "";
    renderShell();
  } catch (err) { alert("请求失败：" + err.message); }
}
async function archiveRun(r, h) {
  if (await threadCall(h, `/runs/${encodeURIComponent(r.id)}/archive`, r.archived ? "DELETE" : "POST")) loadRuns();
}
async function deleteRun(r, h) {
  if (!confirm("永久删除这条对话？存档、检查点和沙箱里的文件都会删掉，不能撤销。")) return;
  if (!(await threadCall(h, `/runs/${encodeURIComponent(r.id)}`, "DELETE"))) return;
  if (h.alias === HOST && ui.run?.id === r.id) { newChat(); return; }
  loadRuns();
}
function runRow(r, h, archived = false) {
  const current = h.alias === HOST, observe = ui.page === "observe";
  // 评测发起的试次不进对话列表，从“评测”页面打开。
  const row = el("div", "runitem" + (archived ? " archived" : "") + (current && ui.run && r.id === ui.run.id && !(observe && ui.tab === "compare") ? " active" : ""));
  // 观测页的“加入对比”只出现在当前主机这一组：对比的是同一台上的运行。
  if (observe && current) {
    const box = el("input");
    box.type = "checkbox";
    box.checked = ui.checked.has(r.id);
    box.title = "加入对比";
    box.onchange = () => {
      if (box.checked && ui.checked.size >= 5) { box.checked = false; return; }
      box.checked ? ui.checked.add(r.id) : ui.checked.delete(r.id);
      if (ui.tab !== "compare") return render();
      if (ui.checked.size >= 2) openCompare(); else { ui.tab = "trace"; render(); }
    };
    row.append(box);
  }
  const b = el("button"), q = el("span", "q");
  if (!r.done) q.append(el("span", "live"));
  if (SOURCE_LABEL[r.source]) q.append(el("span", "src", SOURCE_LABEL[r.source]));
  // 旁支提问分出来的对话在已归档里，标“旁支”而不是“分叉”：两者都记在 fork_of 上。
  if (r.side) q.append(el("span", "src", "旁支"));
  else if (r.fork_of) q.append(el("span", "src", "分叉"));
  // 副本对话：在工作区的副本里跑，改动要人点“应用到”才回原目录。
  if (r.copy) q.append(el("span", "src", "副本"));
  // 这条对话在等人：醒目的小标签放标题前面，点进对话就能答复。
  if (r.waiting) q.append(el("span", "wait", r.waiting === "approval" ? "等你审批" : "等你回答"));
  // 有名字就显示名字；悬停里仍是第一句话和对话 ID，看得出这是哪条。
  q.append(r.name || r.query || "（无问题）");
  b.title = `${r.query}\n对话 ID：${r.id}`;
  b.append(q, el("small", "", relTime(r)));
  b.onclick = () => {
    document.body.classList.remove("nav-open");
    if (!current) { location.href = hostURL(h.alias, r.id); return; }
    if (ui.tab === "compare") ui.tab = "trace";
    show(r.id);
  };
  row.append(b);
  // 上次看过这条以后又有了新结果：行尾一个小圆点（“看过”的记法见 util.js 的 markSeen）。
  if (r.updated_at > seenAt(h.alias, r.id)) {
    const dot = el("span", "unread");
    dot.title = "有新结果";
    row.append(dot);
  }
  row.append(runMenu(r, h));
  return row;
}
// ---------- 搜索对话 ----------
// 搜索框在列表上面（只有对话页有）：敲字 250 毫秒去抖后请求，连了几台主机就每台各搜一次，
// 只显示最后一次请求的结果。有搜索词时列表换成结果；清空搜索框或者按 Esc 回到原来的列表。
const SEARCH_DEBOUNCE = 250;
// 搜索框与工作区筛选只在对话页：观测页、评测与设置这些面板页（#thread 里是 mempanel）都收起来。
// 面板是在 newChat 之后才换进 #thread 的，所以除了 renderShell 里调，还盯着 #thread 的内容。
function syncRunSearch() {
  const visible = ui.page === "chat" && !$("thread").querySelector(":scope > .mempanel");
  $("list-tools").hidden = !visible;
  $("run-search").hidden = !visible;
  $("run-filter").hidden = !visible;
}
new MutationObserver(syncRunSearch).observe($("thread"), { childList: true });
let searchText = "", searchTimer = 0, searchSeq = 0;
function clearSearch() {
  clearTimeout(searchTimer);
  searchTimer = 0;
  searchText = "";
  searchSeq++; // 还在路上的结果不要了
  $("run-search").value = "";
  loadRuns();
}
// 换筛选只重画列表（正在搜的话先还原成普通列表）：选的记在 localStorage，刷新后还在。
$("run-filter").onchange = () => {
  setWsFilter($("run-filter").value);
  if (searchText) clearSearch(); else loadRuns();
};
// 片段里命中的那几个字加粗：用 textContent 一段段拼，不拼 HTML。
function markHit(text, query) {
  const box = document.createDocumentFragment(), hay = text.toLowerCase(), needle = query.toLowerCase();
  let at = 0;
  while (needle) {
    const i = hay.indexOf(needle, at);
    if (i < 0) break;
    if (i > at) box.append(text.slice(at, i));
    box.append(el("b", "", text.slice(i, i + needle.length)));
    at = i + needle.length;
  }
  if (at < text.length) box.append(text.slice(at));
  return box;
}
// 一台主机的搜索结果：在重连、连不上、出错的回 null，那一组只显示标题。
async function searchHost(h, q) {
  if (h.state === "failed") return null;
  try {
    const res = await fetch(hostAPI(h.alias, `/runs/search?q=${encodeURIComponent(q)}`));
    return res.ok ? await res.json() : null;
  } catch { return null; }
}
async function runSearch(q) {
  const seq = ++searchSeq;
  searchText = q;
  const hosts = ui.hosts.length ? ui.hosts : [{ alias: HOST, host: HOST, remote: !!HOST, state: "connected" }];
  const mine = hosts.find((h) => h.alias === HOST);
  const groups = mine ? [mine, ...hosts.filter((h) => h !== mine)] : hosts;
  const lists = await Promise.all(groups.map((h) => searchHost(h, q)));
  if (seq !== searchSeq) return;
  drawSearch(groups, lists);
}
// 结果和 loadRuns 一样按主机分组；每条显示名字（没有就第一句话）和“问题/回答：片段”，
// 已归档的标出来，点一下打开那条对话（别的主机的整页跳过去，照 runRow）。
function drawSearch(groups, lists) {
  const total = lists.reduce((n, list) => n + (list?.length || 0), 0), box = $("runs");
  if (!total) { box.replaceChildren(el("div", "search-none", "没有找到")); return; }
  box.replaceChildren(...groups.map((h, i) => {
    const group = el("div", groups.length > 1 ? "host-group" : "");
    if (groups.length > 1) {
      const name = el("div", "host-name" + (h.state === "connected" ? "" : " bad"), `${h.alias || "本机"} · ${HOST_STATE[h.state] || h.state}`);
      name.title = h.error || "";
      group.append(name);
    }
    group.append(...(lists[i] || []).map((hit) => searchRow(h, hit)));
    return group;
  }));
}
function searchRow(h, hit) {
  const current = h.alias === HOST, row = el("div", "runitem hit"), b = el("button"), q = el("span", "q");
  if (hit.archived) q.append(el("span", "src", "已归档"));
  q.append(hit.name || hit.query || "（无问题）");
  const where = el("small", "where");
  where.append(`${hit.where}：`, markHit(hit.snippet || "", searchText));
  b.append(q, where);
  b.title = `${hit.query || ""}\n对话 ID：${hit.id}`;
  b.onclick = () => {
    document.body.classList.remove("nav-open");
    if (!current) { location.href = hostURL(h.alias, hit.id); return; }
    clearSearch();
    show(hit.id);
  };
  row.append(b);
  return row;
}
$("run-search").oninput = () => {
  const q = $("run-search").value.trim();
  clearTimeout(searchTimer);
  if (!q) { clearSearch(); return; }
  searchTimer = setTimeout(() => runSearch(q), SEARCH_DEBOUNCE);
};
$("run-search").onkeydown = (ev) => {
  if (ev.key !== "Escape") return;
  ev.preventDefault();
  clearSearch();
};
export function autosize() { const q = $("query"); q.style.height = "auto"; q.style.height = q.scrollHeight + "px"; }

$("files-btn").onclick = () => { ui.filesOpen = !ui.filesOpen; if (ui.filesOpen) { ui.subOpen = ui.webOpen = null; ui.diffOpen = false; ui.files.run = null; } renderChat(); renderShell(); };
// 导出：整条对话在页面里拼成 Markdown 再下载，不经服务端（见 export.js）。
$("export-btn").onclick = () => exportRun(ui.run);
// 旁支提问：按钮的显示见 renderShell，四步见 sideQuestion。
$("side-btn").onclick = sideQuestion;
$("diff-btn").onclick = () => { ui.diffOpen = !ui.diffOpen; if (ui.diffOpen) { ui.subOpen = ui.webOpen = null; ui.filesOpen = false; ui.diff.run = null; } renderChat(); renderShell(); };
// 副本对话的“应用到”：把副本里的改动写回原目录（只改文件，不提交，见 workspace/applyCopy）。
// 原目录从 start 事件的 copy_of 取；成功后说改了哪些文件，冲突（409）时把 git apply 的报错原样显示出来。
$("apply-copy-btn").onclick = async () => {
  const r = ui.run;
  if (!r?.copy) return;
  const dir = ui.events.find((e) => e.kind === "start" && e.copy_of)?.copy_of || r.workspace;
  if (!confirm(`把副本里的改动应用到 ${dir}？只改原目录的文件，不提交。`)) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/apply-copy`), { method: "POST" });
    const text = (await res.text()).trim();
    if (!res.ok) return alert("应用失败：" + text);
    const files = JSON.parse(text).files || [];
    alert(`已应用 ${files.length} 个文件：` + files.join("、"));
  } catch (err) { alert("应用失败：" + err.message); }
};
$("page-chat").onclick = () => { ui.page = "chat"; if (ui.tab === "compare") ui.tab = "trace"; render(); loadRuns(); };
$("page-observe").onclick = () => { ui.page = "observe"; render(); loadRuns(); };
$("run-id").onclick = async () => {
  const id = ui.run?.id;
  if (!id) return;
  let note;
  try { await navigator.clipboard.writeText(id); note = "已复制"; }
  catch { note = "请手动选择复制"; }
  if (ui.run?.id !== id) return;
  $("run-id").textContent = note;
  setTimeout(() => { if (ui.run?.id === id) renderShell(); }, 1800);
};
$("goal").onclick = editGoal;
$("menu").onclick = () => document.body.classList.toggle("nav-open");
$("tab-trace").onclick = () => { ui.tab = "trace"; render(); loadRuns(); };
$("tab-maze").onclick = () => { ui.tab = "maze"; render(); loadRuns(); };
$("tab-spans").onclick = () => { ui.tab = "spans"; render(); loadRuns(); };
$("tab-compare").onclick = () => openCompare();
$("search").oninput = () => { ui.search = $("search").value; render(); };
$("expand").onclick = () => { ui.expanded = true; ui.collapsed.clear(); render(); };
$("replay").onclick = () => {
  if (!ui.replay) return startReplay();
  stopReplay();
  ui.run = newRun(ui.run.id);
  ui.events.forEach((e) => apply(ui.run, e));
  render();
};
$("terminal").onclick = () => select("end", ui.run);
// 对这段对话自动归因。已有归因就直接打开（详情页里可以重新归因）。
$("attribute").onclick = async () => {
  const r = ui.run;
  if (!r) return;
  try {
    const old = (await (await fetch(api("/evals"))).json()).find((e) => e.kind === "attribution" && e.run === r.id);
    if (old) return openEvals(old.id);
  } catch {}
  attribute(r.id);
};
// 撤销：三种退法在对话框里选，每换一种就按这个 mode 重新 dry_run 一次，预览跟着变。
// 有冲突只挡“一起退”和“只退文件”：只退对话不动文件，冲突不影响。执行后同原来，重新读这条对话。
// 编辑重发（edit）用同一个对话框：多一个装着这一轮问题的文本框，确认后先撤销，再用它续聊。
export async function rewindTurn(r, t, edit = false) {
  const dialog = $("rewind-dialog"), preview = $("rw-preview"), apply = $("rw-apply"), field = $("rw-text");
  const mode = () => dialog.querySelector('input[name="rw-mode"]:checked').value;
  const call = async (dryRun, m) => {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/rewind`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ turn: t.checkpoint, dry_run: dryRun, mode: m }) });
    const text = await res.text();
    let data = null;
    try { data = JSON.parse(text); } catch {}
    if (!res.ok) throw new Error(data?.error || text);
    return data;
  };
  const files = (out, label, xs) => { if (xs?.length) out.push(el("div", "meta", label), el("pre", "", xs.join("\n"))); };
  let seq = 0;
  const refresh = async () => {
    const m = mode(), mine = ++seq;
    preview.replaceChildren(el("div", "meta", "正在预览…"));
    apply.disabled = true;
    let plan;
    try { plan = await call(true, m); } catch (err) { if (mine === seq) preview.replaceChildren(el("div", "err", err.message)); return; }
    if (mine !== seq) return; // 换得快，只显示最后一次的预览
    const box = [], total = plan.turns.length;
    if (m === "files") {
      box.push(el("p", "", `只撤销第 ${t.n} 轮及之后的文件改动，文件放回这一轮开始之前；对话不退，下一轮会告诉模型这些文件已经恢复。`));
      files(box, "恢复", plan.restore); files(box, "删除（这几轮新建的）", plan.delete);
      files(box, "bash 改过、不会还原（没有快照）", plan.unsnapshotted);
    } else if (m === "chat") {
      box.push(el("p", "", `对话退回第 ${t.n} 轮之前，文件不动；下一轮会告诉模型之后各轮改过的文件还在。`));
      files(box, "这几轮改过的文件", [...(plan.restore || []), ...(plan.delete || []), ...(plan.unsnapshotted || [])]);
    } else {
      box.push(el("p", "", `撤销第 ${t.n} 轮${total > 1 ? `及之后，共 ${total} 轮` : ""}：文件放回这一轮开始之前，对话${plan.continues ? "退回上一轮" : "也全部撤销，之后在这条对话里重新开始"}。`));
      files(box, "恢复", plan.restore); files(box, "删除（这几轮新建的）", plan.delete);
      if (!plan.restore.length && !plan.delete.length) box.push(el("div", "meta", "这几轮没有用编码工具改过文件。"));
      files(box, "bash 改过、不会还原（没有快照）", plan.unsnapshotted);
      box.push(el("div", "meta", "只撤销 edit_file、write_file 的改动；bash 改的文件没有快照。"));
    }
    if (m !== "chat" && plan.conflicts?.length) {
      box.push(el("div", "err", "这些文件在 agent 最后一次编辑之后又被改过（bash、人或别的程序），撤销会覆盖那些改动："));
      files(box, "冲突", plan.conflicts);
    }
    preview.replaceChildren(...box);
    apply.disabled = m !== "chat" && !!plan.conflicts?.length;
  };
  $("rw-title").textContent = edit ? `改第 ${t.n} 轮的问题，重新发` : `撤销第 ${t.n} 轮及之后`;
  apply.textContent = edit ? "撤销并重新发" : "撤销";
  field.hidden = !edit;
  field.value = edit ? t.question || "" : "";
  // 没有工作区的对话只能只退对话：另外两种要退文件，而文件归 app-server 按工作区管。
  const onlyChat = !r.workspace;
  dialog.querySelectorAll('input[name="rw-mode"]').forEach((x) => { x.disabled = onlyChat && x.value !== "chat"; });
  dialog.querySelector(`input[name="rw-mode"][value="${onlyChat ? "chat" : "both"}"]`).checked = true;
  dialog.querySelectorAll('input[name="rw-mode"]').forEach((x) => { x.onchange = refresh; });
  $("rw-cancel").onclick = () => dialog.close();
  apply.onclick = async () => {
    apply.disabled = true;
    try { await call(false, mode()); } catch (err) { preview.append(el("div", "err", err.message)); apply.disabled = false; return; }
    dialog.close();
    if (!edit) { show(r.id, false); return; }
    // 撤销成功，用文本框里的问题续聊：权限模式、推理强度取输入框旁边当前的选择（和平时续聊一样），附图不带。
    const query = field.value;
    try {
      const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/messages`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query, permission: $("perm").value, effort: $("effort").value }) });
      if (!res.ok) throw new Error(await res.text());
      show((await res.json()).id);
    } catch (err) {
      // 撤销已经生效、这句没发出去：重新读这条对话，把问题放回输入框，原因写在输入框下面，内容不丢。
      show(r.id);
      backToInput(query);
      ui.hint = "没发出去：" + err.message;
      syncComposer();
    }
  };
  dialog.showModal();
  if (edit) { field.focus(); field.setSelectionRange(field.value.length, field.value.length); }
  refresh();
}
// 分叉：从这一轮的检查点另起一条对话，点完不再确认；成功后打开新对话（show 也会刷新列表）。
export async function forkTurn(r, t) {
  let id;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/fork`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ turn: t.checkpoint }) });
    if (!res.ok) throw new Error((await res.text()).trim());
    id = (await res.json()).id;
  } catch (err) { return alert("分叉失败：" + err.message); }
  show(id);
}
// 旁支提问：从最后一轮临时分出去问一句，问答不进这条对话。分出来的对话已归档，名字用问题；四步里
// 哪一步失败就停下，原因写在输入框下面，已经分出来的旁支留着（在已归档里），不回滚。
async function sideQuestion() {
  const r = ui.run;
  // 和每条回答下的“分叉”一样，分叉带过来的轮次不能再分（它们的撤销会走进原对话）。
  const turn = [...(r?.turns || [])].reverse().find((t) => t.checkpoint && !t.rewound && !t.inherited);
  if (!turn) { ui.hint = "这条对话还没有可以分出去的轮次"; syncComposer(); return; }
  const question = (prompt("旁支问题：临时分出去问一句，问答不会回到这条对话") || "").trim();
  if (!question) return;
  const fail = (text) => { ui.hint = text; syncComposer(); };
  let id;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/fork`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ turn: turn.checkpoint, side: true }) });
    if (!res.ok) throw new Error((await res.text()).trim());
    id = (await res.json()).id;
  } catch (err) { return fail("没分成旁支：" + err.message); }
  // 名字：问题的前 20 个字（按字符数，不按字节）。
  const name = "旁支：" + [...question].slice(0, 20).join("");
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(id)}/name`), { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }) });
    if (!res.ok) throw new Error((await res.text()).trim());
  } catch (err) { return fail(`旁支已经分出来了（在已归档里），但没改成名字：${err.message}`); }
  try {
    // 权限模式、推理强度用输入框旁边当前的选择，和平时续聊一样。
    const res = await fetch(api(`/runs/${encodeURIComponent(id)}/messages`), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query: question, permission: $("perm").value, effort: $("effort").value }) });
    if (!res.ok) throw new Error((await res.text()).trim());
  } catch (err) { return fail(`旁支已经分出来了（在已归档里），但问题没发出去：${err.message}`); }
  show(id);
}
export async function attribute(runID) {
  if (!confirm("自动归因这段对话的最后一轮：\n1. DeepSeek 读完整轨迹，判断成败、写出评分标准，指出决定性的那一步和原因；\n2. 失败时从那一步之前分叉回放：插入纠正跑 3 次、插入“继续。”作对照跑 3 次，按这段对话当时的模型真实调用。\n开始？")) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(runID)}/attribute`), { method: "POST" });
    if (!res.ok) throw new Error(await res.text());
    openEvals((await res.json()).id);
  } catch (err) { alert("归因失败：" + err.message); }
}
export function newChat(updateURL = true) {
  stopReplay();
  if (ui.source) ui.source.close();
  Object.assign(ui, { chatFollow: true, unseen: false, page: "chat", tab: "trace", run: null, events: [], source: null, selected: null, loading: false, subOpen: null, webOpen: null, webClosed: false, picked: {}, hint: "" });
  if (updateURL) syncRunURL(null);
  document.body.classList.remove("nav-open");
  $("stats").hidden = true;
  $("view").replaceChildren(el("div", "empty", "先在对话中提问，或从左侧选择历史对话"));
  $("inspector").replaceChildren(el("div", "empty", "点击事件或节点查看详情"));
  render(); loadRuns(); $("query").focus();
}
$("new-chat").onclick = () => newChat();
// 工作区下拉的“＋ 添加工作区…”：打开选目录对话框，登记成功后刷新设置并选中新的；取消就退回原来选的那一项。
{
  $("workspace").addEventListener("change", async (ev) => {
    const ws = ev.currentTarget;
    if (ws.value !== ADD_WORKSPACE) return;
    ws.value = ws.dataset.prev || ""; // 对话框开着的时候先退回原来那一项
    const name = await pickWorkspace();
    if (!name) { syncComposer(); return; }
    ui.picked.workspace = true;
    await loadSettings();
    ws.value = name;
    ws.dataset.prev = name; // 下次再选“添加工作区…”又取消时退回这里（syncComposer 在设值之前已经跑完）
  });
}
// 导入 Codex / Claude Code 的会话文件：服务端换成网页端的存档格式，导入后只读。
$("import").onclick = () => $("import-file").click();
$("import-file").onchange = async () => {
  const file = $("import-file").files[0];
  $("import-file").value = "";
  if (!file) return;
  const label = $("import").lastChild.textContent;
  $("import").lastChild.textContent = "导入中…";
  try {
    const res = await fetch(api("/runs/import"), { method: "POST", body: file });
    if (!res.ok) throw new Error(await res.text());
    document.body.classList.remove("nav-open");
    ui.page = "chat";
    show((await res.json()).id);
  } catch (err) { alert("导入失败：" + err.message); }
  finally { $("import").lastChild.textContent = label; }
};
