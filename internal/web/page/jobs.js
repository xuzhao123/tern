import { renderChat } from "./chat.js";
import { renderShell } from "./shell.js";
import { $, api, el, icon, ui } from "./util.js";
import { closeWorkTab, openWorkTab } from "./workpanel.js";
// 后台命令侧栏：模型用 bash 带 background=true 起的命令（见 runs.js 的 job/updated）。
// 列表是这条对话最新一轮的；一轮结束以后还在（只读），没有按钮。
// 命令与输出都是模型那边写出来的，不可信：只用 textContent 显示。
const POLL_MS = 2000;

// 跑了多久：在跑的是从现在往回算，结束的是从开始到结束。
function fmtAge(j) {
  const start = Date.parse(j.started_at || "");
  if (!start) return "";
  const end = j.state === "running" ? Date.now() : Date.parse(j.ended_at || "") || start;
  const sec = Math.max(0, Math.round((end - start) / 1000));
  return sec < 60 ? `${sec} 秒` : sec < 3600 ? `${Math.floor(sec / 60)} 分 ${sec % 60} 秒` : `${Math.floor(sec / 3600)} 小时 ${Math.floor((sec % 3600) / 60)} 分`;
}

function stateText(j) {
  if (j.state === "running") return "在跑";
  if (j.state === "stopped") return "已停止" + (j.stopped ? "：" + j.stopped : "");
  return "已结束" + (j.exit_code !== undefined ? `，退出码 ${j.exit_code}` : "");
}

// 看一次输出：协议里只看不消费，模型下次 bash_output 照样从上次的位置读到。
// 交回状态，还在跑（running）就接着每 2 秒看一次，直到结束或人收起。
async function loadOutput(r, j) {
  const s = ui.jobs;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/jobs/${encodeURIComponent(j.id)}/output`));
    const text = (await res.text()).trim();
    if (!res.ok) throw new Error(text);
    const data = JSON.parse(text);
    if (ui.run !== r || s.open !== j.id) return "";
    Object.assign(s, { output: data.output || "", dropped: data.dropped || 0, err: "" });
    return data.state || "";
  } catch (e) {
    if (ui.run === r && s.open === j.id) s.err = String(e.message || e);
    return "";
  }
}

function poll(r, j) {
  const s = ui.jobs;
  clearTimeout(s.timer);
  const tick = async () => {
    s.timer = null;
    if (s.closed || ui.run !== r || s.open !== j.id) return;
    s.polling = true;
    const state = await loadOutput(r, j);
    s.polling = false;
    if (s.closed || ui.run !== r || s.open !== j.id) return;
    s.settled = state && state !== "running" ? j.id : "";
    if (state === "running") s.timer = setTimeout(tick, POLL_MS);
    renderChat();
  };
  tick();
}

// 停掉一条（先问）：停止与状态由 agent 的 job/updated 通知带回来，这里只发请求。
async function stopJob(r, j) {
  if (!confirm(`停掉这条后台命令？\n\n${j.command}`)) return;
  try {
    const res = await fetch(api(`/runs/${encodeURIComponent(r.id)}/jobs/${encodeURIComponent(j.id)}/kill`), { method: "POST" });
    const text = (await res.text()).trim();
    if (!res.ok) throw new Error(text);
  } catch (e) {
    if (ui.run !== r) return;
    ui.jobs.err = "停不掉：" + (e.message || e);
    renderChat();
  }
}

export function renderJobsPanel() {
  const panel = $("subpanel"), r = ui.run, s = ui.jobs;
  if (!r || ui.page !== "chat" ) {
    clearTimeout(s.timer);
    Object.assign(s, { open: null, timer: null });
    ui.jobsOpen = false;
    panel.hidden = true;
    panel.replaceChildren();
    return;
  }
  if (s.run !== r.id) { clearTimeout(s.timer); Object.assign(s, { run: r.id, open: null, timer: null, output: "", dropped: 0, err: "" }); }
  const selected = r.jobs.find((j) => j.id === s.open);
  if (selected?.state === "running" && selected.id !== s.settled && !r.exit && !s.timer && !s.polling && !s.err) poll(r, selected);
  panel.hidden = false;
  panel.dataset.agent = "";
  const head = el("div", "sp-head"), close = el("button", "sp-close"); close.innerHTML = icon("close");
  close.type = "button";
  close.title = "关闭";
  close.setAttribute("aria-label", "关闭后台命令面板");
  close.onclick = () => closeWorkTab();
  head.append(el("strong", "", `后台命令 · ${r.jobs.length} 个`), close);
  const body = el("div", "sp-body");
  const operations=el("button","ghost","项目操作与开发服务");operations.type="button";operations.onclick=()=>openWorkTab("operations","","项目操作");body.append(operations);
  for(const command of r.shells||[]) {const row=el("details");row.append(el("summary","",`用户命令 · ${command.text} · 退出码 ${command.body.exit_code}`),el("pre","job-out",command.body.output||"（无输出）"));body.append(row);}
  const prepare=el("button","ghost","在输入区准备命令");prepare.type="button";prepare.disabled=!!$("query").value;prepare.onclick=()=>{if($("query").value)return;$("query").value="! ";$("query").dispatchEvent(new Event("input"));$("query").focus();};body.append(prepare);
  if(!r.jobs.length)body.append(el("p","meta","本轮没有后台命令。上面的用户命令记录来自实际执行；! 命令在当前轮结束后运行，不经过模型。"));
  body.append(el("div", "meta", r.exit
    ? "这一轮已经结束，后台命令也一起结束了；这里只留着当时的状态。"
    : "模型用 bash 带 background=true 起的命令，只活到这一轮结束；看输出不影响模型自己读（bash_output 照样从上次的位置读到）。"));
  if (s.err) body.append(el("div", "meta err", s.err));
  for (const j of r.jobs) {
    const row = el("div", "job-row" + (s.open === j.id ? " on" : ""));
    row.append(el("div", "cmd", j.command || j.id));
    const bar = el("div", "bar");
    bar.append(el("span", "sub-st " + (j.state === "running" ? "running" : j.state === "stopped" ? "stopped" : "done"), stateText(j)), el("span", "age", fmtAge(j)));
    if (j.state === "running" && !r.exit) {
      const view = el("button", "ghost", s.open === j.id ? "收起输出" : "看输出");
      view.type = "button";
      view.onclick = () => {
        if (s.open === j.id) { s.open = null; clearTimeout(s.timer); s.timer = null; }
        else { s.open = j.id; Object.assign(s, { output: "", dropped: 0, err: "" }); poll(r, j); }
        renderChat();
      };
      const stop = el("button", "ghost", "停止");
      stop.type = "button";
      stop.title = "停掉这条命令（连同它起的进程），写审计";
      stop.onclick = () => stopJob(r, j);
      bar.append(view, stop);
    }
    row.append(bar);
    body.append(row);
    if (s.open === j.id) {
      if (s.dropped) body.append(el("div", "meta", `前面有 ${s.dropped} 字节没有显示（命令输出太多，只取最近的 8KB）`));
      body.append(el("pre", "job-out", s.output || "（还没有输出）"));
    }
  }
  panel.replaceChildren(head, body);
}
