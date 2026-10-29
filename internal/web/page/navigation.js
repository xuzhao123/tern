import { show } from "./follow.js";
import { $, HOST, api, hostAPI, hostURL, el, ui } from "./util.js";
import { openProjectFile } from "./projectfiles.js";
import { saveDraft } from "./drafts.js";
import { autosize, syncComposer } from "./shell.js";
import { openWorkTab } from "./workpanel.js";
const actions = [
  ["activity-open", "活动中心", "待处理、运行中、完成未读"],
  ["new-chat", "新任务", "在当前执行主机开始"],
  ["settings", "设置", "模型、权限、项目与通知"],
  ["skills-hub", "Skills", "查看和管理技能"],
  ["mcp-hub", "MCP", "工具连接与授权"],
  ["diff-btn", "查看改动", "当前任务工作目录的差异"],
  ["files-btn", "任务产物", "查看沙箱文件"],
  ["work-btn", "工作面板", "恢复文件、改动与子任务标签"],
  ["jobs-btn", "后台命令", "查看输出和运行状态"],
  ["page-observe", "诊断此任务", "轨迹与调用链"],
  ["memory", "长期记忆", "查看记忆"],
  ["evals", "评测与归因", "开发者工具"],
  ["audit", "审计日志", "查看操作记录"],
  ["import", "导入历史", "Codex / Claude Code，只读"],
  ["export-btn", "导出任务", "保存为 Markdown"],
];
let mode = "actions", selected = 0, entries = [], priorFocus, timer, controller, generation = 0, next = 0, through = 0, historyRun = "";
const dialog = $("command-dialog"), query = $("command-query"), results = $("command-results"), status = $("command-status");
const placeholders = { actions: "搜索操作…", tasks: "搜索所有任务的名称、问题、回答…", files: "搜索当前任务的项目文件…", history: "查找当前任务的问题、回答、工具结果…" };
function cancel() { clearTimeout(timer); controller?.abort(); generation++; }
function close() { cancel(); dialog.close(); priorFocus?.focus?.(); }
function context() { return `${HOST || ui.hosts?.find((h) => !h.alias)?.host || "当前执行主机"} · ${ui.run?.workspace || "无项目"}`; }
function paint(note = "") {
  results.replaceChildren();
  selected = Math.min(selected, Math.max(0, entries.length - 1));
  entries.forEach((item, i) => {
    const button = el("button", "command-result" + (i === selected ? " on" : "")); button.type = "button";
    button.append(el("span", "", item.title), el("small", "", item.detail));
    button.onclick = () => { selected = i; close(); item.run(); }; results.append(button);
  });
  status.textContent = `${context()} · ${note || (entries.length ? `${entries.length} 项` : "没有匹配项")}`;
  $("command-more").hidden = mode !== "history" || !next;
  $("command-prev").hidden = $("command-next").hidden = mode !== "history";
  $("command-prev").disabled = $("command-next").disabled = !entries.length;
}
function restoreInput() {
  if ($("query").value || !$("slash").hidden || !ui.run || ui.historyReplaying) return;
  let text="",automatic=false;
  for(const event of ui.events){
    if(event.sub)continue;
    if(["start","continue"].includes(event.kind)){
      automatic=!!event.goal_id&&event.queue_id?.startsWith(`goal-${event.goal_id}-`);
      if(!automatic&&!event.steer_ids?.length)text=event.text||text;
    }else if(event.method==="item/completed"&&event.body?.item?.type==="userMessage"){
      const data=event.body.item.data;
      if(!automatic||data?.steer)text=data?.text||text;
    }
  }
  if (!text) return;
  $("query").value = text; autosize(); syncComposer(); saveDraft(); $("query").focus();
}
function locate(hit, id) {
  if (ui.run?.id !== id) return;
  ui.chatFollow = false;
  if (hit.sub && hit.task) openWorkTab("agent", hit.task, "子智能体详情");
  const event = ui.events.find((e) => e.seq === hit.seq);
  const run = hit.sub ? ui.run.subs[hit.task] : ui.run;
  let call = hit.tool_id ? run?.calls.find((c) => c.tools.some((t) => t.id === hit.tool_id)) : run?.calls.find((c) => c.n === hit.call);
  if (!call && event) call = [...(run?.calls || [])].reverse().find((c) => c.start <= Date.parse(event.time));
  const scope = hit.sub ? $("subpanel") : $("thread");
  let node = call && scope.querySelector(`[data-call="${call.n}"]`);
  if (hit.where === "问题" && call) node = scope.querySelector(`[data-turn="${call.turn}"]`) || node;
  if (node) {
    for (let parent = node.parentElement; parent && parent !== scope; parent = parent.parentElement) if (parent.tagName === "DETAILS") { parent.open = true; if (parent.dataset.key) ui.open.add(parent.dataset.key); }
    if (hit.tool_id) for (const detail of node.querySelectorAll("details.step")) { detail.open = true; if (detail.dataset.key) ui.open.add(detail.dataset.key); }
    node.scrollIntoView({ block: "center" }); node.classList.add("search-focus"); setTimeout(() => node.classList.remove("search-focus"), 2500);
  }
  ui.hint = `${node ? "已定位" : "此记录尚未在聊天中定位"} · ${hit.where} · 事件 ${hit.seq}：${hit.snippet}`; $("hint").textContent = ui.hint;
}
async function search(more = false) {
  cancel(); const request = generation, signal = (controller = new AbortController()).signal;
  const text = query.value.trim(), currentMode = mode, run = ui.run, account = ui.account;
  const valid = () => dialog.open && request === generation && mode === currentMode && account === ui.account && (mode === "tasks" || mode === "actions" || ui.run === run);
  if (!more) { entries = []; selected = 0; next = 0; through = 0; historyRun = run?.id || ""; }
  if (mode === "actions") {
    entries = actions.filter(([id]) => $(id) && !$(id).hidden && !$(id).disabled && (id !== "page-observe" || ui.run)).map(([id, title, detail]) => ({ title, detail, run: () => $(id).click() }));
    if (run) entries.push({title:"任务内查找", detail:"问题、回答与工具结果 · Ctrl/⌘ Shift F", run:()=>openCommands("history")}, {title:"文件快开",detail:"当前任务实际工作目录",run:()=>openCommands("files")}, {title:"恢复上一条输入",detail:"空输入且补全关闭时恢复为草稿 · Alt ↑",run:restoreInput});
    entries = entries.filter((x) => `${x.title} ${x.detail}`.toLocaleLowerCase().includes(text.toLocaleLowerCase())); paint(); return;
  }
  if (["files", "history"].includes(mode) && (!run || mode === "files" && !run.workspace)) { paint(mode === "files" ? "请先打开有项目目录的任务" : "请先打开任务"); return; }
  if (!text && mode === "tasks") {
    entries = (ui.taskCatalog || []).map(({host,task}) => ({ title:task.name || task.query || "未命名任务",detail:`${host.alias || host.host} · ${task.workspace || "无项目"}`,run:()=>host.alias === HOST ? show(task.id) : location.assign(hostURL(host.alias,task.id)) })); paint("最近任务；输入关键词搜索完整历史"); return;
  }
  if (!text && mode === "history") { paint("输入关键词；每页最多50个命中事件"); return; }
  paint("查询中…");
  const read = async (url) => { const res = await fetch(url, {signal}); if (!res.ok) throw new Error((await res.text()).trim()); return res.json(); };
  try {
    if (mode === "tasks") {
      const hosts = ui.hosts?.length ? ui.hosts : [{alias:HOST,host:HOST || "当前主机"}];
      const found = await Promise.allSettled(hosts.map((h) => read(hostAPI(h.alias, `/runs/search?q=${encodeURIComponent(text)}`))));
      if (!valid()) return;
      const failed = [];
      found.forEach((result,i) => { const h=hosts[i]; if(result.status!=="fulfilled") {failed.push(h.alias || h.host);return;}
        for(const hit of result.value) entries.push({title:hit.name || hit.query || "未命名任务",detail:`${h.alias || h.host} · ${hit.workspace || "无项目"}${hit.archived ? " · 已归档" : ""} · ${hit.where}：${hit.snippet}`,run:()=>h.alias===HOST ? show(hit.id) : location.assign(hostURL(h.alias,hit.id))});
      });
      paint(`${entries.length} 个任务（每台主机最多50项）${failed.length ? `；未能查询：${failed.join("、")}` : ""}`);
    } else if (mode === "files") {
      const data = await read(api(`/workspaces/files?threadId=${encodeURIComponent(run.id)}&q=${encodeURIComponent(text)}`)); if (!valid()) return;
      entries = (data.files || []).map((path) => ({title:path,detail:run.workspaceDir || run.workspace,run:()=>{if(ui.run===run)openProjectFile(path,true);}})); paint(`${entries.length} 个 Git 可见文件，最多20项`);
    } else {
      const data = await read(api(`/runs/${encodeURIComponent(historyRun)}/find?`+new URLSearchParams({q:text,after:String(more ? next : 0),through:String(more ? through : 0)}))); if(!valid())return;
      if(!Array.isArray(data.hits)||!Number.isSafeInteger(data.through))throw new Error("执行主机不支持历史查找，请升级后重试");
      through=data.through; next=data.next;
      entries.push(...data.hits.map((hit)=>({title:hit.snippet,detail:`${hit.where} · 事件 ${hit.seq}${hit.sub ? " · 子任务" : ""}`,run:()=>locate(hit,run.id)})));
      paint(`${entries.length} 个命中事件${next ? "，还有历史未查询" : "，已查到历史边界"} · #${through}`);
    }
  } catch(error) { if(valid() && error.name!=="AbortError")paint("查询失败："+error.message); }
}
export function openCommands(nextMode="actions") {
  if(!dialog.open)priorFocus=document.activeElement;
  mode=nextMode; query.value=""; query.placeholder=placeholders[mode]; query.maxLength=mode==="files" ? 4096 : 200;
  for(const b of dialog.querySelectorAll("[data-command-mode]"))b.classList.toggle("on",b.dataset.commandMode===mode);
  if(!dialog.open)dialog.showModal(); search(); query.focus();
}
$("command-menu").onclick=()=>openCommands("tasks"); $("command-close").onclick=close;
dialog.addEventListener("close",()=>{if(!dialog.open){cancel();priorFocus?.focus?.();}});
for(const b of dialog.querySelectorAll("[data-command-mode]"))b.onclick=()=>openCommands(b.dataset.commandMode);
function scheduleSearch() { cancel(); entries=[]; next=0; paint("查询中…"); timer=setTimeout(()=>search(),200); }
query.oninput=(event)=>{if(!event.isComposing)scheduleSearch();};
query.oncompositionend=scheduleSearch;
function move(delta){if(!entries.length)return;selected=(selected+delta+entries.length)%entries.length;paint(`${selected+1} / ${entries.length} 个已查询结果${next ? "，可继续查找" : ""}`);results.children[selected]?.scrollIntoView({block:"nearest"});}
$("command-prev").onclick=()=>move(-1);$("command-next").onclick=()=>move(1);$("command-more").onclick=()=>search(true);
query.onkeydown=(e)=>{if(e.isComposing)return;if(["ArrowDown","ArrowUp"].includes(e.key)){e.preventDefault();move(e.key==="ArrowDown"?1:-1);}else if(e.key==="Enter"&&entries[selected]){e.preventDefault();const item=entries[selected];close();item.run();}};
document.addEventListener("keydown",(e)=>{
  if(e.isComposing||e.defaultPrevented||document.querySelector("dialog[open]"))return;
  if((e.metaKey||e.ctrlKey)&&e.shiftKey&&["p","f"].includes(e.key.toLowerCase())){e.preventDefault();openCommands(e.key.toLowerCase()==="f"?"history":"actions");}
  if(e.altKey&&e.key==="ArrowUp"&&document.activeElement===$("query")&&!$("query").value&&$("slash").hidden){e.preventDefault();restoreInput();}
});

export function initNavigation() {
  const theme = $("theme");
  const apply = (value) => { theme.value = ["light", "dark"].includes(value) ? value : "system"; document.documentElement.dataset.theme = theme.value; };
  try { apply(localStorage.getItem("tern-theme")); } catch { apply("system"); }
  theme.onchange = () => { apply(theme.value); try { localStorage.setItem("tern-theme", theme.value); } catch {} };
  window.addEventListener("storage", (e) => { if (e.key === "tern-theme") apply(e.newValue); });
}
