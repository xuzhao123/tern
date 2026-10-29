import { show } from "./follow.js";
import { md } from "./md.js";
import { TOOL_INFO, loadModels, loadSettings, newChat, syncComposer } from "./shell.js";
import { $, HOST, MEM_KIND, MEM_SOURCE, MEM_STATUS, api, el, fold, notificationSettings, setNotification, ui } from "./util.js";
$("memory").onclick = () => openMemory();
// 审计日志只读。服务端每次从头校验哈希链；页面只展示，不提供修改或删除。
const AUDIT_ACTOR = { user: "人", model: "模型", program: "程序" };
$("audit").onclick = () => openAudit();
async function openAudit(task = "") {
  document.body.classList.remove("nav-open");
  if (ui.sending) return;
  $("new-chat").click();
  const panel = el("div", "mempanel evalpanel");
  $("thread").replaceChildren(panel);
  panel.append(el("div", "meta", "读取中…"));
  let data;
  try { const res = await fetch(api("/audit?task=" + encodeURIComponent(task))); if (!res.ok) throw new Error(await res.text()); data = await res.json(); }
  catch (err) { panel.replaceChildren(el("div", "err", "读取失败：" + err.message)); return; }
  const records = data.records || [];
  const form = el("div", "eval-form"), input = el("input");
  input.placeholder = "按任务ID过滤（含子任务）"; input.value = task; input.style.minWidth = "320px";
  const go = el("button", "", "过滤"); go.type = "button"; go.onclick = () => openAudit(input.value.trim());
  input.onkeydown = (e) => { if (e.key === "Enter") go.click(); };
  form.append(input, go);
  if (task) { const all = el("button", "", "全部"); all.type = "button"; all.onclick = () => openAudit(); form.append(all); }
  const status = data.broken_seq ? el("div", "err", `哈希链校验失败：第 ${data.broken_seq} 条，${data.reason}`) : el("div", "meta", `哈希链完整：共 ${data.total || 0} 条记录`);
  panel.replaceChildren(el("h2", "", "审计日志"), el("p", "meta", ".data/audit/audit.jsonl · 只追加，每条带上一条的哈希。agent 记录程序判断、执行结局与记忆变更，网页端记录你的点击；参数只存哈希，原文在检查点与对话存档里。命令行校验：go run . audit -verify"), status, form);
  if (!records.length) { panel.append(el("div", "empty", "还没有审计记录。")); return; }
  const table = el("table", "eval-table"), head = el("tr");
  for (const h of ["#", "时间", "谁", "事件", "任务", "工具 / 对象", "风险", "决定 / 结局", "说明"]) head.append(el("th", "", h));
  table.append(head);
  for (const r of [...records].reverse()) {
    const tr = el("tr");
    const taskCell = el("td", "mono");
    if (r.task) { const a = el("a", "", r.task.length > 22 ? r.task.slice(0, 8) + "…" + r.task.slice(-10) : r.task); a.href = "#"; a.title = r.task; a.onclick = (e) => { e.preventDefault(); openAudit(r.task); }; taskCell.append(a); }
    if (r.conversation) { const a = el("a", "", " 对话"); a.href = "#"; a.onclick = (e) => { e.preventDefault(); show(r.conversation); }; taskCell.append(a); }
    tr.append(el("td", "num", String(r.seq)), el("td", "num", new Date(r.time).toLocaleString("zh-CN", { hour12: false })), el("td", "num", AUDIT_ACTOR[r.actor] || r.actor), el("td", "mono", r.event), taskCell,
      el("td", "mono", [r.tool, r.target].filter(Boolean).join(" · ")), el("td", "", r.risk || ""), el("td", "", [r.decision, r.status].filter(Boolean).join(" / ")),
      el("td", "", [r.detail, r.args_sha256 ? `参数 sha256 ${r.args_sha256.slice(0, 12)}…（${r.args_bytes} 字节）` : ""].filter(Boolean).join("；")));
    table.append(tr);
  }
  const wrap = el("div", "eval-scroll"); wrap.append(table); panel.append(wrap);
}
// 记忆面板只读 .data/memory.json；删除由 agent 的 -memory-forget 执行，与 agent 共用文件锁。
export async function openMemory(focus) {
  document.body.classList.remove("nav-open");
  if (ui.sending) return;
  $("new-chat").click();
  const panel = el("div", "mempanel"), when = (t) => new Date(t).toLocaleString("zh-CN", { hour12: false });
  $("thread").replaceChildren(panel);
  panel.append(el("div", "meta", "读取中…"));
  let data;
  try { const res = await fetch(api("/memory")); if (!res.ok) throw new Error(await res.text()); data = await res.json(); }
  catch (err) { panel.replaceChildren(el("div", "err", "读取失败：" + err.message)); return; }
  const list = data.file.memories || [], now = Date.parse(data.now);
  const chunks = Object.values(data.index?.chunks || {});
  panel.replaceChildren(el("h2", "", "长期记忆"), el("p", "meta", `${data.path} · 共 ${list.length} 条 · 在设置里打开“长期记忆”后，新对话开头经两路召回、融合和重排，最多注入 5 条；过期条目在 agent 下次读写时删除。原文与来源是依据，背景说明是模型生成的派生数据，可重建`));
  if (!list.length) panel.append(el("div", "empty", "还没有记忆。在设置里打开“长期记忆”，再新建对话让 agent 记住一件事，例如“别忘了我对花生过敏”。"));
  for (const m of [...list].reverse()) {
    const expired = m.expire_at && Date.parse(m.expire_at) <= now;
    const card = el("article", "mem-item" + (expired ? " expired" : "") + (m.id === focus ? " focus" : "")), head = el("div", "mem-head");
    const del = el("button", "ghost", "删除");
    del.onclick = async () => {
      if (!confirm(`删除 ${m.id}？会写回 .data/memory.json，无法撤销。`)) return;
      del.disabled = true;
      const res = await fetch(api(`/memory/${m.id}/forget`), { method: "POST" });
      if (!res.ok) alert(await res.text());
      openMemory();
    };
    head.append(el("strong", "", m.id), el("span", "mem-kind", MEM_KIND[m.kind] || m.kind), el("span", "meta", `${MEM_SOURCE[m.source] || m.source} · 重要性 ${m.importance}`), del);
    const derived = chunks.filter((c) => c.memory_id === m.id).sort((a, b) => a.plan.localeCompare(b.plan) || a.chunk - b.chunk);
    const details = [];
    for (const c of derived) details.push(el("div", "meta", `[${c.plan} · 片段 ${c.chunk}/${c.chunks} · ${MEM_STATUS[c.status] || c.status}] ` + (c.context ? "背景：" + c.context : c.reason || "") + (c.chunks > 1 ? " ｜ 片段：" + c.text : "")));
    if (!derived.length) details.push(el("div", "meta", "还没有索引：下次检索时建立"));
    details.push(m.source_context ? fold(`来源上下文 · ${when(m.source_context.time)}` + (m.source_context.truncated ? " · 已省略更早部分" : ""), m.source_context.text) : el("div", "meta", "来源：无（升级前写入的旧记忆，不猜测来源，按原文检索）"));
    card.append(head, el("div", "mem-text", m.content), ...details, el("div", "meta", `创建 ${when(m.created_at)} · 最近访问 ${when(m.last_access)} · ` + (m.expire_at ? (expired ? "已过期，下次读写时删除" : "过期于 " + when(m.expire_at)) : "不过期")));
    panel.append(card);
  }
}
$("skills-hub").onclick = () => openSkills();
$("mcp-hub").onclick = () => openMCP();
// MCP 登录的回调页在另一个标签页里：它登录完 postMessage 过来，正开着的 MCP 中心重新取一次状态。
window.addEventListener("message", (ev) => {
  if (ev.origin !== location.origin || ev.data !== "mcp-oauth") return;
  if (document.querySelector(".hubpanel[data-hub=mcp]")) openMCP();
});
// 设置页：工具常驻，按 内置默认 → 平台 → 项目 → 个人 合并；项目是所选工作区里的 .agents/tern.json。
// 本机条件只会关掉工具。改动只影响之后的新对话。
const SET_SOURCE = { builtin: "内置默认", platform: "平台", project: "项目", user: "你" };
export async function openSettings() {
  const pickedWs = ui.run?.workspace || $("workspace").value;
  const panel = await openHubPage("设置", "打开的工具在新对话里都会带上，模型自己决定用不用；每个对话只选模型和工作区。改动只影响之后的新对话，进行中的对话沿用开始时的快照。保存在 .data/settings.json。");
  if (!panel) return;
  // 两级下拉（供应商、具体模型）的选项都来自 /models：它由启动时的 loadModels 异步取回，没回来就先取一次，
  // 否则设置页会只剩一个“ark”，看起来像没有第二级。
  if (!ui.models) await loadModels();
  let data;
  // 项目层按页面上新对话下拉里选中的工作区算：没选工作区的对话读不到项目配置（服务端按登记解析目录）。
  try { data = await (await hubCall("GET", "/settings?workspace=" + encodeURIComponent(pickedWs))).json(); }
  catch (err) { panel.lastChild.replaceWith(el("div", "err", "读取失败：" + err.message)); return; }
  if (!panel.isConnected) return;
  // 打开设置页就算重新取过一次：这份是按所选工作区算的，同时当新对话下拉的默认值（见 shell.js 的 loadWsSettings）。
  // 没选工作区的那份才当 ui.settings 用：带项目层的那份不能留着当“不选工作区”的默认值，否则换回去还是项目里的。
  if (pickedWs) (ui.wsSettings ||= {})[pickedWs] = data.effective;
  else ui.settings = data;
  syncComposer();
  const e = data.effective, values = { ...e.values };
  // 来源一栏多了“项目”：所选工作区里 .agents/tern.json 的键；不生效的键与整份没读进来的原因也在这里说明。
  const projectNote = e.project_error ? `项目配置整份没生效：${e.project_error}。`
    : (e.project_ignored || []).length ? `项目配置里这些键不生效：${e.project_ignored.join("、")}。` : "";
  panel.lastChild.replaceWith(el("p", "meta", "生效顺序：内置默认 › 平台 › 项目 › 你。项目是工作区里的 .agents/tern.json，只在对话选了这个工作区时生效，permission_rules 与 net_allow 是追加。" + projectNote));
  const tag = (key) => el("span", "set-tag" + (e.source[key] === "user" ? " user" : ""), SET_SOURCE[e.source[key]] || e.source[key]);
  const select = (key, options) => {
    const node = el("select");
    for (const [v, label] of options) node.append(new Option(label, v));
    node.value = values[key]; node.onchange = () => { values[key] = node.value; };
    return node;
  };
  panel.append(el("h3", "", "工具"));
  const tools = el("div", "set-box");
  for (const key of data.tool_order) {
    const [name, desc] = TOOL_INFO[key], row = el("label", "set-row"), text = el("span"), box = el("input"), blocked = e.blocked[key];
    box.type = "checkbox"; box.checked = values[key] === "on"; box.onchange = () => { values[key] = box.checked ? "on" : "off"; };
    text.append(el("strong", "", name), el("div", "meta", desc));
    if (key === "rag" || key === "memory") {
      const sub = el("div", "sub");
      if (key === "memory") sub.append(select("memory_ttl", [["", "记住：永久"], ["1h", "记住：1小时后过期"], ["24h", "记住：1天后过期"]]));
      sub.append(select("embedding", [["ark", "向量：线上方舟"], ["local", "向量：本地 MiniLM"]]), el("span", "meta", "检索和记忆共用"));
      text.append(sub);
    }
    if (key === "skills") text.append(el("div", "meta", `已启用 ${e.skills.length} 个`));
    if (key === "mcp") text.append(el("div", "meta", `已启用 ${e.mcp.length} 个 server`));
    row.append(text, el("span", "set-tag " + (blocked ? "bad" : "ok"), blocked ? "不可用" : "可用"), tag(key), box);
    tools.append(row);
    if (blocked) text.append(el("div", "err", blocked));
  }
  panel.append(tools);
  // 个人指令只显示：文件在 app-server 那台机器的 TERN_HOME 下，直接编辑它，下一个新对话生效。
  const inst = data.instructions;
  if (inst) {
    panel.append(el("p", "meta", inst.exists
      ? `个人指令：${inst.path}（${inst.chars} 字），每个对话都会带上；直接编辑这个文件，下一个新对话生效。`
      : `个人指令：${inst.path} 还不存在。写在这里的约定每个对话都会带上（类似 ~/.codex/AGENTS.md）。`));
  }
  panel.append(el("h3", "", "工作区"), el("p", "meta", "对话里只能选这里登记的目录（一般是本机上的 git 仓库）。编码工具只在所选目录里读写，bash 在沙箱里把它挂到 /workspace；不是 git 仓库的目录也能登记，只是撤销只能退回 edit_file、write_file 改过的文件，也看不了改动面板。"));
  const wsBox = el("div", "set-box");
  if (!e.workspaces.length) wsBox.append(el("div", "set-row", e.blocked.coding ? "编码工具不可用：" + e.blocked.coding : "还没有登记工作区"));
  for (const w of e.workspaces) {
    const row = el("div", "set-row"), text = el("span");
    text.append(el("strong", "", w.name));
    if (!w.git) text.append(el("span", "set-tag bad", "不是 git 仓库"));
    // 有 .agents/tern.json 的工作区标出来，悬停列出生效的键（这个文件在仓库里，用编辑器改，页面只显示）。
    if (w.project) {
      const proj = el("span", "set-tag", "有项目配置");
      proj.title = "这个工作区的 .agents/tern.json：" + w.project;
      text.append(proj);
    }
    text.append(el("div", "meta", w.path));
    row.append(text, el("span"));
    // 改名：只改设置里登记的名字，目录、已有对话的快照与目录都不动。
    const rename = el("button", "ghost", "改名");
    rename.onclick = async () => {
      const newName = prompt(`工作区 ${w.name} 的新名字（小写字母、数字、_ 或 -，最长 32）：`, w.name);
      if (newName === null || newName.trim() === w.name) return;
      try { await hubCall("PUT", `/settings/workspaces/${encodeURIComponent(w.name)}/name`, { newName: newName.trim() }); openSettings(); } catch (err) { alert(err.message); }
    };
    row.append(rename);
    const del = el("button", "ghost", "移除");
    del.onclick = async () => { if (!confirm(`移除工作区 ${w.name}？目录本身不会删除。`)) return; try { await hubCall("DELETE", `/settings/workspaces/${encodeURIComponent(w.name)}`); openSettings(); } catch (err) { alert(err.message); } };
    row.append(del);
    wsBox.append(row);
  }
  const form = el("form", "set-row"), path = el("input"), add = el("button", "ghost", "添加");
  path.placeholder = "仓库目录的绝对路径，可以用 ~ 开头，例如 ~/workspace/tern"; path.style.width = "100%";
  // 浏览…：在对话框里点目录登记，成功后照旧刷新这一页；手敲路径的输入框保留。
  const browse = el("button", "ghost", "浏览…"); browse.type = "button"; browse.title = "在这台主机上浏览目录，选一个 git 仓库登记";
  browse.onclick = async () => { const name = await pickWorkspace(); if (name) openSettings(); };
  form.append(path, el("span"), el("span"), browse, add);
  form.onsubmit = async (ev) => { ev.preventDefault(); add.disabled = true; try { await hubCall("POST", "/settings/workspaces", { path: path.value }); openSettings(); } catch (err) { alert(err.message); add.disabled = false; } };
  wsBox.append(form);
  // 可能要找的仓库：只在“浏览…”下面列前 5 个，每个后面一个“登记”（app-server 扫常见目录，见 workspace/candidates）。
  const candBox = el("div", "set-cands");
  wsBox.append(candBox);
  fetch(api("/workspaces/candidates")).then((res) => (res.ok ? res.json() : null)).then((data) => {
    const found = (data?.candidates || []).slice(0, 5);
    if (!found.length || !candBox.isConnected) return;
    candBox.append(el("span", "meta", "可能要找的仓库："));
    for (const cand of found) {
      const row = el("span", "cand"), reg = el("button", "ghost", "登记");
      row.title = cand.path;
      reg.type = "button"; reg.title = "登记 " + cand.path;
      reg.onclick = async () => { reg.disabled = true; try { await hubCall("POST", "/settings/workspaces", { path: cand.path }); openSettings(); } catch (err) { alert(err.message); reg.disabled = false; } };
      row.append(el("span", "cand-name", cand.name), reg);
      candBox.append(row);
    }
  }).catch(() => {});
  panel.append(wsBox);
  panel.append(el("h3", "", "默认值"));
  const grid = el("div", "set-box set-grid"), num = (key, label, hint) => {
    const input = el("input"); input.type = "number"; input.value = values[key]; input.oninput = () => { values[key] = input.value; };
    const wrap = el("span"); wrap.append(input, document.createTextNode(" "), el("span", "meta", hint), document.createTextNode(" "), tag(key));
    grid.append(el("span", "meta", label), wrap);
  };
  const line = (label, node, key) => { const wrap = el("span"); wrap.append(node, document.createTextNode(" "), tag(key)); grid.append(el("span", "meta", label), wrap); };
  const cell = (node, key) => { const wrap = el("span"); wrap.append(node, document.createTextNode(" "), tag(key)); return wrap; };
  // 选项与输入框同一份（/models）；没配好的供应商标出来，开对话时 agent 会报出缺什么。
  const providers = [...(ui.models?.values() || [])].map((p) => [p.name, (p.label || p.name) + (p.error ? "（未配置）" : "")]);
  // 一级是供应商与接口，二级才是具体模型，与输入框旁的两个下拉叫法一致（模型清单只在 llm 包里，这里读 /models）。
  const providerPick = select("provider", providers.length ? providers : [[values.provider, values.provider]]);
  providerPick.title = "供应商与接口：选的是哪家服务、走哪种协议，不是具体模型";
  const modelPick = el("select"), modelCell = el("span");
  modelPick.title = "具体模型：空表示用这家供应商的默认值；开对话时页面上没选就以这里为准";
  const fillModels = () => {
    const spec = ui.models?.get(values.provider), models = (spec?.models || []).map((m) => m.name);
    const opts = [["", "供应商默认（" + (spec?.default || "按 .env 与路由表") + "）"], ...models.map((m) => [m, m])];
    modelPick.replaceChildren(...opts.map(([v, text]) => new Option(text, v)));
    modelPick.value = opts.some(([v]) => v === values.model) ? values.model ?? "" : "";
    values.model = modelPick.value;
  };
  providerPick.onchange = () => { values.provider = providerPick.value; values.model = ""; fillModels(); };
  modelPick.onchange = () => { values.model = modelPick.value; };
  fillModels();
  modelCell.append(modelPick);
  grid.append(el("span", "meta", "默认供应商"), cell(providerPick, "provider"), el("span", "meta", "默认具体模型"), cell(modelCell, "model"));
  line("默认工作区", select("workspace", [["", "无工作区"], ...e.workspaces.map((w) => [w.name, w.name])]), "workspace");
  num("max_steps", "模型请求预算", "每轮最多多少次模型请求；0 表示不限，靠重复动作与无进展熔断、停止按钮收住");
  num("context_window", "上下文窗口", "token；0 表示按模型（取路由表里核对过的输入上限，不知道时 262144）。工具全开时每次请求要带全部工具定义，窗口小会很快压缩");
  num("tool_output", "工具输出上限", "单个工具结果写进上下文的 token 上限");
  // 默认权限：三档常用组合。完全访问拆成 permission=full + sandbox_mode=host；只读计划与执行位置是高级项。
  const permPick = el("select");
  for (const [v, label] of [["ask", "请求批准：改文件、跑命令前确认"], ["edit", "帮我批准：工作区内直接做，越界与联网才问"], ["unrestricted", "完全访问：命令在这台机器上直接跑，不再询问"]]) permPick.append(new Option(label, v));
  const currentPerm = values.sandbox_mode === "host" ? "unrestricted" : values.permission_mode;
  permPick.value = ["ask", "edit", "unrestricted"].includes(currentPerm) ? currentPerm : "edit";
  permPick.onchange = () => {
    if (permPick.value === "unrestricted") { values.permission_mode = "full"; values.sandbox_mode = "host"; }
    else { values.permission_mode = permPick.value; values.sandbox_mode = "sandbox"; }
  };
  line("默认权限", permPick, "permission_mode");
  // 高级：只读计划与执行位置。它们和上面的三档是同一组内部值，改这里时上面回到对应档。
  const planBox = el("input"); planBox.type = "checkbox"; planBox.checked = values.permission_mode === "plan";
  planBox.onchange = () => {
    if (planBox.checked) { values.permission_mode = "plan"; values.sandbox_mode = "sandbox"; permPick.value = "ask"; }
    else if (values.permission_mode === "plan") { values.permission_mode = "edit"; permPick.value = "edit"; }
  };
  const planWrap = el("label", "set-row"); planWrap.append(planBox, el("span", "", "只读计划：不能改文件，bash 里工作区只读"));
  grid.append(el("span", "meta", "高级"), planWrap);
  const unattendedBox = el("input"); unattendedBox.type = "checkbox"; unattendedBox.checked = values.permission_mode === "full" && values.sandbox_mode !== "host";
  unattendedBox.onchange = () => {
    if (unattendedBox.checked) { values.permission_mode = "full"; values.sandbox_mode = "sandbox"; permPick.value = "edit"; }
    else if (values.permission_mode === "full" && values.sandbox_mode !== "host") { values.permission_mode = "edit"; }
  };
  const unattendedWrap = el("label", "set-row"); unattendedWrap.append(unattendedBox, el("span", "", "无人值守（沙箱内）：不询问，但命令仍在沙箱里，越界直接失败"));
  grid.append(el("span", "meta", ""), unattendedWrap);
  const sandboxPick = select("sandbox_mode", [["sandbox", "沙箱内（默认）"], ["host", "宿主机（完全访问）"]]);
  sandboxPick.value = values.sandbox_mode || "sandbox";
  sandboxPick.onchange = () => {
    values.sandbox_mode = sandboxPick.value;
    permPick.value = values.sandbox_mode === "host" ? "unrestricted" : (["ask", "edit"].includes(values.permission_mode) ? values.permission_mode : "edit");
  };
  line("执行位置", sandboxPick, "sandbox_mode");
  line("默认推理强度", select("reasoning_effort", [["high", "高：默认，最仔细"], ["medium", "中"], ["low", "低"], ["minimal", "最低"]]), "reasoning_effort");
  const rules = el("textarea"); rules.rows = 4; rules.style.width = "100%"; rules.value = values.permission_rules;
  rules.placeholder = "一行一条，顺序 deny › ask › allow：\nallow: Bash(go build *)\nask: Edit(internal/agent/**)\ndeny: Bash(git push *)";
  rules.oninput = () => { values.permission_rules = rules.value; };
  const rulesWrap = el("span"); rulesWrap.append(rules, tag("permission_rules"));
  grid.append(el("span", "meta", "权限规则"), rulesWrap);
  // 沙箱联网白名单：开对话时拍进快照（-net-allow）。审批时点“始终允许”会加到这里（你的个人设置）。
  const nets = el("textarea"); nets.rows = 3; nets.style.width = "100%"; nets.value = values.net_allow;
  nets.placeholder = "一行一个域名，含子域名，如：\npypi.org\nfiles.pythonhosted.org\nregistry.npmjs.org";
  nets.oninput = () => { values.net_allow = nets.value; };
  const netsWrap = el("span"); netsWrap.append(nets, tag("net_allow"));
  grid.append(el("span", "meta", "沙箱联网白名单"), netsWrap);
  panel.append(grid, el("p", "meta", "沙箱里的命令只能经代理访问白名单里的域名；连到别的域名时代理当场问你，命令等你答复。审批时点“始终允许”会把域名加进你的个人设置。"), el("p", "meta", "输入框旁的权限下拉有三档：请求批准（改文件、跑命令前确认）、帮我批准（工作区内直接做，越界与联网才问）、完全访问（命令在宿主机上直接跑，不询问、不受沙箱限制）。只读计划、无人值守（不询问但仍在沙箱里）与执行位置在设置页的高级项里；规则在对话开始时拍进快照。高风险确认（删除记忆、破坏性 MCP、含私有数据后的外发、续跑重做）在全权以外的模式下都询问；全权模式同 Claude Code、Codex，不再询问，由程序放行并写审计（续跑重做不自动做，回填结果未知）。"));
  // hooks 在本机以网页端的身份运行命令，不在沙箱里，所以只从平台设置读；个人设置里不生效。
  panel.append(el("h3", "", "Hooks"), el("p", "meta", "在固定时刻运行本机命令：用户提交（UserPromptSubmit）、工具执行前（PreToolUse）、工具成功后（PostToolUse）、模型准备结束（Stop）。格式同 Claude Code 的 hooks 段；填配置原文，或相对 agent 目录的文件路径（如 hooks/hooks.json）。对话开始时读成原文拍进快照。命令不在沙箱里运行，只有平台设置里的 hooks 生效。"));
  const hooksBox = el("textarea"); hooksBox.rows = 3; hooksBox.style.width = "100%"; hooksBox.value = values.hooks;
  hooksBox.placeholder = "hooks/hooks.json\n或 {\"hooks\": {\"PostToolUse\": [{\"matcher\": \"edit_file|write_file\", \"hooks\": [{\"type\": \"command\", \"command\": \"...\"}]}]}}";
  hooksBox.oninput = () => { values.hooks = hooksBox.value; };
  const hooksWrap = el("div", "set-box"); hooksWrap.append(hooksBox, tag("hooks"));
  panel.append(hooksWrap);
  const actions = el("div", "set-actions"), mine = el("button", "ghost", "保存为我的设置"), reset = el("button", "ghost", "恢复继承");
  const saveTo = async (url, body) => { try { await hubCall("PUT", url, body); await loadSettings(); openSettings(); } catch (err) { alert(err.message); } };
  mine.onclick = () => saveTo("/settings", values);
  reset.onclick = () => { if (confirm("清掉你的个人设置，改用平台默认？")) saveTo("/settings", {}); };
  actions.append(mine, reset);
  const plat = el("button", "ghost", "保存为平台默认");
  plat.title = "所有账号没单独设过的项都跟着变";
  plat.onclick = () => saveTo("/settings/platform", values);
  actions.append(plat);
  actions.append(el("span", "meta", "只保存和上一层不同的项；标签显示每项来自哪一层"));
  panel.append(actions);
  panel.append(el("h3", "", "通知"));
  const notifyWrap = el("div", "set-box"), prefs = notificationSettings();
  for (const [kind, label] of [["done", "本轮完成"], ["approval", "权限审批"], ["question", "用户提问"]]) {
    const row = el("label", "set-row"), select = el("select"), note = el("span", "meta");
    select.setAttribute("aria-label", label + "通知");
    select.append(new Option("关闭", "off"), new Option("仅页面在后台", "background"), new Option("始终", "always"));
    select.value = prefs[kind] || "off";
    select.onchange = async () => {
      const wanted = select.value;
      if (wanted !== "off" && (typeof Notification === "undefined" || await Notification.requestPermission() !== "granted")) {
        select.value = notificationSettings()[kind] || "off"; note.textContent = "浏览器未允许通知，请在站点设置中开启。"; return;
      }
      if (!setNotification(kind, wanted)) { select.value = notificationSettings()[kind] || "off"; note.textContent = "保存失败，请检查浏览器存储。"; return; }
      note.textContent = wanted !== "off" && !navigator.locks ? "浏览器不支持多页面去重，系统通知暂停，请查看活动中心。" : "已保存";
    };
    row.append(el("strong", "", label), select, note); notifyWrap.append(row);
  }
  notifyWrap.append(el("p", "meta", "按当前账号、本浏览器保存。关闭所有网页后不保证收到通知；活动中心仍保留真实待处理状态。"));
  panel.append(notifyWrap);
  organizeSettings(panel, grid, actions);
}
// 按用途移动原控件，保存逻辑与来源标签仍使用同一份 values。
let settingsCategory = "常规";
function organizeSettings(panel, grid, actions) {
  const names = ["常规", "模型", "项目", "权限", "工具与集成", "连接", "高级", "关于"], sections = new Map(names.map((name) => [name, el("section", "settings-section")]));
  const nav = el("nav", "settings-nav"); nav.setAttribute("aria-label", "设置分类");
  const headingMap = { "工具": "工具与集成", "工作区": "项目", "默认值": "模型", "Hooks": "高级", "通知": "常规" };
  let section;
  for (const node of [...panel.children]) {
    if (node.tagName === "H3") section = sections.get(headingMap[node.textContent]);
    if (section && node !== actions && node !== grid) section.append(node);
  }
  const groups = new Map();
  let last = "模型";
  for (const node of [...grid.children]) {
    if (node.classList.contains("meta")) {
      const label = node.textContent;
      last = label.includes("工作区") ? "项目" : ["默认权限", "高级", "执行位置", "权限规则", "沙箱联网白名单", ""].includes(label) ? "权限" : "模型";
    }
    if (!groups.has(last)) { const group = el("div", "set-box set-grid"); groups.set(last, group); sections.get(last).append(group); }
    groups.get(last).append(node);
  }
  grid.remove();
  const link = (name, action) => { const b = el("button", "ghost", name); b.type = "button"; b.onclick = action; return b; };
  const tools = sections.get("工具与集成");
  tools.prepend(el("p", "meta", `执行主机：${HOST || "当前主机"}。启用状态与实际加载/连接结果分别展示。`), link("Skills 中心", openSkills), link("MCP 连接与授权", openMCP));
  const general = sections.get("常规"), theme = el("select");
  theme.setAttribute("aria-label", "界面主题"); theme.append(new Option("跟随系统", "system"), new Option("浅色", "light"), new Option("深色", "dark")); theme.value = $("theme").value;
  theme.onchange = () => { $("theme").value = theme.value; $("theme").dispatchEvent(new Event("change")); };
  general.prepend(el("h3", "", "外观"), theme, el("p", "meta", "快捷键：Ctrl/⌘ Shift P 操作面板；Ctrl/⌘ Shift F 任务内查找；空输入 Alt ↑ 恢复上一条输入；Tab 移动焦点。"));
  const connection = sections.get("连接"), about = sections.get("关于"), versions = el("div");
  about.append(el("h3", "", "运行版本"), versions, el("p", "meta", "版本来自正在运行的进程。修改源码不会自动更新 Web；刷新网页也不会替换服务进程。+dirty 表示构建时有未提交改动。"));
  async function refreshVersions() {
    versions.textContent = "读取中…";
    try {
      const res = await fetch(api("/version")); if (!res.ok) throw new Error(await res.text()); const data = await res.json(); if (!panel.isConnected) return;
      versions.replaceChildren(el("p", "", `Web：${data.web_version} · build ${data.web_build}`)); connection.replaceChildren(el("h3", "", "执行主机与连接"));
      for (const h of data.connections.hosts) {
        versions.append(el("p", "", `${h.alias || h.host || "本机"}：${h.version || "版本未知"} · build ${h.build || "未知"}${h.stale ? " · 与 Web 构建不同" : ""}`));
        const row = el("div", "set-box"); row.append(el("strong", "", h.alias || h.host || "本机"), el("p", "meta", `${h.remote ? "SSH" : "本地 socket"} · ${h.state} · ${h.home || "运行目录未获取"}`));
        if(h.error || h.hint)row.append(el("p", "err", [h.kind,h.hint || h.error].filter(Boolean).join(" · ")));
        connection.append(row);
      }
      connection.append(el("p", "meta", "远程连接依次检查 SSH 认证、主机指纹、tern 程序和协议握手。通过执行主机菜单重试、安装或确认指纹；复制密钥须明确选择，不会自动同步。"), link("打开执行主机菜单",()=>$("host").click()), link("刷新状态",refreshVersions));
    } catch(error) { versions.textContent = "版本读取失败："+error.message; connection.replaceChildren(el("p", "err", versions.textContent), link("打开执行主机菜单",()=>$("host").click()), link("重试",refreshVersions)); }
  }
  about.append(link("刷新版本",refreshVersions)); refreshVersions();
  function choose(name) { settingsCategory=name; for(const [label,section] of sections)section.hidden=label!==name;for(const b of nav.children)b.setAttribute("aria-current",String(b.textContent===name)); }
  for (const name of names)nav.append(link(name,()=>choose(name)));
  panel.insertBefore(nav,panel.firstElementChild.nextSibling);
  panel.append(...sections.values(),actions); actions.classList.add("settings-save"); choose(settingsCategory);
}

// 选工作区目录：浏览这台主机上的目录，把 git 仓库登记成工作区；返回登记好的名字，取消返回空。
// 远端主机的页面带 /h/<别名>/ 前缀，api() 会补上，浏览的就是远端的目录。第一次从家目录开始，
// 之后从上次停留的目录开始（只记在这个页面里，不写设置）。
let dirLast = "";
export async function pickWorkspace() {
  const dialog = $("dir-dialog"), input = $("dir-path"), list = $("dir-list"), msg = $("dir-msg"), add = $("dir-add"), up = $("dir-up");
  const init = $("dir-init"), skip = $("dir-skip"), cands = $("dir-cands");
  if (dialog.open) return ""; // 已经开着就用那一个，不重复 showModal
  $("dir-title").textContent = "选工作区目录 · " + (HOST || "本机");
  let current = "", info = null, seq = 0, settle = null;
  // 关掉对话框并把结果交回去：取消按钮、Esc、登记成功都走这里（不靠 dialog 的 close 事件，各浏览器时机不一）。
  const finish = (name) => { if (!settle) return; const done = settle; settle = null; if (dialog.open) dialog.close(); done(name); };
  const show = (data) => {
    info = data; current = dirLast = data.path;
    input.value = data.path;
    up.disabled = !data.parent; up.dataset.parent = data.parent;
    const rows = data.entries.map((entry) => {
      const row = el("button", "dir-row"), tag = el("span", "dir-tag");
      row.type = "button";
      row.append(el("span", "dir-name", entry.name + "/"), tag);
      if (entry.git) tag.append(document.createTextNode("git 仓库"));
      if (entry.registered) tag.append(document.createTextNode((entry.git ? " · " : "") + "已登记：" + entry.registered));
      row.onclick = () => load(entry.path);
      return row;
    });
    if (!data.entries.length) rows.push(el("div", "meta", "这里没有子目录"));
    if (data.truncated) rows.push(el("div", "meta", "只显示前 500 个"));
    list.replaceChildren(...rows);
    // 不是 git 仓库的目录：把“登记这个目录”换成两个按钮（先 git init，或者不用 git 也登记）。
    const registered = !!data.registered, plain = !data.git && !registered;
    add.hidden = plain;
    init.hidden = skip.hidden = !plain;
    add.disabled = !data.git || registered;
    add.textContent = registered ? "已登记：" + data.registered : "登记这个目录";
    add.title = registered ? "这个目录已经登记过" : "登记这个目录，之后在对话里可以选它";
    init.disabled = skip.disabled = false;
  };
  const load = async (path) => {
    const mineSeq = ++seq;
    msg.replaceChildren();
    try {
      const res = await fetch(api("/workspaces/browse?path=" + encodeURIComponent(path)));
      const text = await res.text();
      if (!res.ok) throw new Error(text);
      if (mineSeq === seq) show(JSON.parse(text));
    } catch (err) {
      if (mineSeq !== seq) return; // 点得快，只显示最后一次的结果
      list.replaceChildren(); add.disabled = true;
      msg.replaceChildren(el("div", "err", err.message));
    }
  };
  up.onclick = () => { if (up.dataset.parent) load(up.dataset.parent); };
  input.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); load(input.value); } };
  $("dir-cancel").onclick = () => finish("");
  dialog.oncancel = () => finish(""); // Esc
  // 登记当前目录：git 为空表示必须是 git 仓库；init 先在目录里 git init；skip 不是 git 仓库也登记。
  const register = async (git) => {
    for (const button of [add, init, skip]) button.disabled = true;
    try {
      const body = git ? { path: current, git } : { path: current };
      const res = await fetch(api("/settings/workspaces"), { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const text = await res.text();
      if (!res.ok) throw new Error(text);
      finish(JSON.parse(text).name || "");
    } catch (err) {
      msg.replaceChildren(el("div", "err", err.message));
      if (info) show(info); // 按当前目录重新摆按钮
    }
  };
  add.onclick = () => register("");
  init.onclick = () => register("init");
  skip.onclick = () => {
    if (!confirm("这个目录不是 git 仓库：撤销只能退回 edit_file、write_file 改过的文件，bash 改的退不回来，也看不了改动面板。仍然登记？")) return;
    register("skip");
  };
  // 可能要找的仓库：app-server 扫一遍已登记工作区的上一级和家目录下的几个常见目录，只列还没登记的 git 仓库。
  // 每次打开对话框取一次；点一下把路径填进路径框并跳到那个目录，还要再点“登记这个目录”才登记。
  cands.hidden = true;
  cands.replaceChildren();
  fetch(api("/workspaces/candidates")).then((res) => (res.ok ? res.json() : null)).then((data) => {
    const found = data?.candidates || [];
    if (!found.length || !dialog.open) return;
    const rows = found.map((cand) => {
      const row = el("button", "dir-row");
      row.type = "button";
      row.title = cand.path;
      row.append(el("span", "dir-name", cand.name + "/"), el("span", "dir-tag", "git 仓库"));
      row.onclick = () => { input.value = cand.path; load(cand.path); };
      return row;
    });
    cands.replaceChildren(el("div", "meta", "可能要找的仓库"), ...rows);
    cands.hidden = false;
  }).catch(() => {});
  return new Promise((resolve) => {
    settle = resolve;
    dialog.showModal();
    input.value = dirLast;
    load(dirLast);
  });
}
export async function loadEnabled() {
  try { const res = await fetch(api("/hub/enabled")); if (res.ok) ui.hub = await res.json(); } catch {}
  syncComposer();
}
async function hubCall(method, url, body) {
  const res = await fetch(api(url), { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  if (!res.ok) throw new Error(await res.text());
  return res;
}
function hubToggle(enabled, onchange) {
  const label = el("label", "switch"), box = el("input");
  box.type = "checkbox"; box.checked = enabled;
  box.onchange = async () => { box.disabled = true; try { await onchange(box.checked); } catch (err) { alert(err.message); box.checked = !box.checked; } box.disabled = false; loadEnabled(); };
  label.append(box, enabled ? "已启用" : "已停用");
  return label;
}
// 两个中心都在对话区展示，和“长期记忆”一样；数据来自 agent 自己的 -skills-list 与 -mcp-list。
export async function openHubPage(title, intro) {
  document.body.classList.remove("nav-open");
  if (ui.sending) return null;
  if (newChat() === false) return null;
  const panel = el("div", "mempanel hubpanel");
  $("thread").replaceChildren(panel);
  panel.append(el("h2", "", title), el("p", "meta", intro), el("div", "meta", "读取中…"));
  return panel;
}
export async function openSkills() {
  const panel = await openHubPage("Skills 中心", "skills/*/SKILL.md · 由 agent 的 -skills-list 解析和校验 · 开关保存在 .data/hub.json。新对话勾选 Skills 时，打开的 skill 只把名字和用途写进 system；模型判断任务相关时调用 load_skill 读取正文。只支持知识型 skill，不执行脚本。");
  if (!panel) return;
  let data;
  try { data = await (await hubCall("GET", "/hub")).json(); }
  catch (err) { panel.lastChild.replaceWith(el("div", "err", "读取失败：" + err.message)); return; }
  if (!panel.isConnected) return;
  const bar = el("div", "hub-bar"), add = el("button", "ghost", "＋ 新建 skill"), form = el("form", "hub-form");
  form.hidden = true;
  add.onclick = () => { form.hidden = !form.hidden; };
  bar.append(add, el("span", "meta", `${data.skills.length} 个可用` + (data.skill_errors.length ? ` · ${data.skill_errors.length} 个未通过校验` : "")));
  const name = el("input"), desc = el("input"), body = el("textarea"), submit = el("button", "ghost", "创建");
  name.placeholder = "name：小写字母、数字、连字符，如 commit-message"; desc.placeholder = "description：一行，写清做什么、什么时候用（模型靠它判断是否加载）";
  body.placeholder = "# 标题\n\n## 步骤\n1. …\n\n## 输出格式\n…";
  const row = el("div", "row"); row.append(submit, el("span", "meta", "写入 skills/<name>/SKILL.md，随后用 agent 的校验检查；不合规会撤销并给出原因"));
  form.append(name, desc, body, row);
  form.onsubmit = async (ev) => {
    ev.preventDefault(); submit.disabled = true;
    try { await hubCall("POST", "/hub/skills", { name: name.value, description: desc.value, body: body.value }); openSkills(); }
    catch (err) { alert(err.message); submit.disabled = false; }
  };
  panel.lastChild.replaceWith(bar);
  panel.append(form);
  if (!data.skills.length && !data.skill_errors.length) panel.append(el("div", "empty", "还没有 skill。点“新建 skill”写一份操作指南。"));
  for (const sk of data.skills) {
    const card = el("article", "hub-card" + (sk.enabled ? "" : " off")), head = el("div", "hub-head"), push = el("div", "push");
    push.append(hubToggle(sk.enabled, async (on) => { await hubCall("POST", `/hub/skills/${sk.name}/enabled`, { enabled: on }); openSkills(); }));
    // 内置的随程序发布，删不掉，只能关掉；新建同名 skill 就是写一份个人的覆盖它。
    if (!sk.builtin) {
      const del = el("button", "ghost", "删除");
      del.onclick = async () => {
        if (!confirm(`删除 skills/${sk.name}/ 整个目录？无法撤销。`)) return;
        try { await hubCall("DELETE", `/hub/skills/${sk.name}`); openSkills(); loadEnabled(); } catch (err) { alert(err.message); }
      };
      push.append(del);
    }
    head.append(el("strong", "", sk.name), push);
    const full = el("details", "step"), sum = el("summary"), inner = el("div", "body");
    sum.append(el("span", "", `SKILL.md 正文 · ${sk.body.length} 字（load_skill 返回的内容）`)); inner.append(md(sk.body)); full.append(sum, inner);
    card.append(head, el("div", "hub-desc", sk.description), el("div", "cmd", sk.builtin ? "内置（随程序发布）" : sk.path), full);
    panel.append(card);
  }
  for (const bad of data.skill_errors) {
    const card = el("article", "hub-card broken"), head = el("div", "hub-head");
    head.append(el("strong", "", bad.dir), el("span", "err", "未通过校验，agent 启动时跳过"));
    card.append(head, el("div", "hub-desc", bad.error), el("div", "cmd", bad.path));
    panel.append(card);
  }
}
// 预设统一从源码启动，在项目根目录执行，无需提前准备可执行文件。
const MCP_PRESETS = [["tern-web", location.origin + "/mcp", "网页端内置的远程 MCP server（Streamable HTTP），工具是一个计算器 calculator（四则运算、括号、sqrt、floor）"], ["everything", "go run github.com/mark3labs/mcp-go/examples/everything@v1.1.1", "示例 server；首次运行会下载并编译"], ["calculator", "go run . -mcp-serve", "本项目 server，始终用当前源码"]];
export async function openMCP() {
  const panel = await openHubPage("MCP 中心", "两种 server：填 http(s) 地址是远程 server（Streamable HTTP，每条消息一个 POST）；填命令是本地 stdio server（相对 agent 目录执行，按空格切分、不经过 shell）。配置保存在 .data/hub.json。新对话勾选 MCP 时，打开的 server 由 agent 逐个连接，工具注册为 mcp_<原名>；连不上的 server 跳过，与已有工具重名的工具跳过。要求 OAuth 的远程 server 点“登录”，令牌存在 .data/mcp-tokens.json。");
  if (!panel) return;
  panel.dataset.hub = "mcp";
  let data;
  try { data = await (await hubCall("GET", "/hub/enabled")).json(); }
  catch (err) { panel.lastChild.replaceWith(el("div", "err", "读取失败：" + err.message)); return; }
  if (!panel.isConnected) return;
  const form = el("form", "hub-form"), name = el("input"), command = el("input"), submit = el("button", "ghost", "添加 server");
  name.placeholder = "name：如 tern-web"; command.placeholder = "远程地址如 " + location.origin + "/mcp；或 stdio 命令如 go run . -mcp-serve";
  const presets = el("div", "row");
  presets.append(el("span", "meta", "快速填入："));
  for (const [n, c, tip] of MCP_PRESETS) { const b = el("button", "link", n); b.type = "button"; b.title = tip; b.onclick = () => { name.value = n; command.value = c; }; presets.append(b); }
  const row = el("div", "row"); row.append(submit, el("span", "meta", "添加后默认启用；先点“测试连接”看协商结果和工具列表"));
  form.append(presets, name, command, row);
  form.onsubmit = async (ev) => {
    ev.preventDefault(); submit.disabled = true;
    try { await hubCall("POST", "/hub/mcp", { name: name.value, command: command.value }); openMCP(); loadEnabled(); }
    catch (err) { alert(err.message); submit.disabled = false; }
  };
  panel.lastChild.replaceWith(form);
  if (data.hosted) panel.insertBefore(el("div", "banner", `网页端内置远程 MCP server：${location.origin}/mcp · ${data.hosted}。它以 Streamable HTTP 提供一个计算器 calculator（四则运算、括号、sqrt、floor）；要在对话里使用，用“快速填入 → observer”添加。`), form);
  if (!data.mcp_servers.length) panel.append(el("div", "empty", "还没有 MCP server。用上面的表单添加一个。"));
  for (const m of data.mcp_servers) {
    const card = el("article", "hub-card" + (m.enabled ? "" : " off")), head = el("div", "hub-head"), push = el("div", "push");
    const test = el("button", "ghost", "测试连接"), del = el("button", "ghost", "删除"), out = el("div");
    // 远程 server 的登录状态（app-server 的 hub/list 给的 auth）：需要登录、已登录、未登录。
    // 令牌与 client_secret 不经网页端，页面只知道这几种状态。
    const remote = /^https?:\/\//.test(m.command), auth = el("div", "mcp-auth");
    const login = el("button", "ghost", "登录"), logout = el("button", "ghost", "退出登录");
    const clientId = el("input"), clientSecret = el("input");
    clientId.placeholder = "client_id（可选：server 不支持自动注册时填）"; clientSecret.placeholder = "client_secret（可选）"; clientSecret.type = "password";
    let authState = m.auth || "none";
    const renderAuth = () => {
      if (!remote) return;
      if (authState === "logged_in") { auth.replaceChildren(el("span", "meta", "已登录"), logout); return; }
      auth.replaceChildren(el("span", "meta", authState === "required" ? "需要登录" : "未登录"), login, clientId, clientSecret);
    };
    renderAuth();
    login.onclick = async () => {
      // 授权页要在这次点击里就打开：等接口回来再 window.open 多半会被浏览器拦掉。
      const tab = window.open("", "_blank");
      login.disabled = true;
      try {
        const body = { redirect: location.origin + "/mcp-oauth/callback" };
        if (clientId.value.trim()) body.client_id = clientId.value.trim();
        if (clientSecret.value.trim()) body.client_secret = clientSecret.value.trim();
        const started = await (await hubCall("POST", `/hub/mcp/${m.name}/login`, body)).json();
        if (tab) tab.location = started.url; else window.open(started.url, "_blank");
        out.replaceChildren(el("div", "meta", "已在新标签页打开授权页：在那边登录、授权，回到这里会自动刷新。"));
      } catch (err) {
        if (tab) tab.close();
        out.replaceChildren(el("div", "err", err.message));
      }
      login.disabled = false;
    };
    logout.onclick = async () => {
      if (!confirm(`退出登录 ${m.name}？它的令牌会从 .data/mcp-tokens.json 里删掉。`)) return;
      try { await hubCall("POST", `/hub/mcp/${m.name}/logout`); openMCP(); loadEnabled(); } catch (err) { alert(err.message); }
    };
    test.onclick = async () => {
      test.disabled = true; out.replaceChildren(el("div", "shimmer", "agent -mcp-list 连接中…"));
      try {
        const result = await (await hubCall("POST", `/hub/mcp/${m.name}/test`)).json();
        const lines = result.output.split("\n"), count = lines.map((l) => /^MCP tools\/list: count=(\d+)/.exec(l)).find(Boolean);
        const hint = /exec format error/.test(result.output) ? "：请使用上方的源码启动预设，或重新构建后重试" : "";
        // 要求授权时 agent 的报错里写着“MCP 需要登录”：把这一项标成“需要登录”、给出“登录”按钮。
        if (result.auth) { authState = result.auth; renderAuth(); }
        out.replaceChildren(el("div", result.ok ? "meta" : "err", result.ok ? `连接成功 · ${count ? count[1] : "?"} 个工具` : "连接失败" + hint), el("pre", result.ok ? "" : "err", result.output.trim()));
      } catch (err) { out.replaceChildren(el("div", "err", err.message)); }
      test.disabled = false;
    };
    del.onclick = async () => {
      if (!confirm(`从配置中删除 ${m.name}？不会删除它的程序文件。`)) return;
      try { await hubCall("DELETE", `/hub/mcp/${m.name}`); openMCP(); loadEnabled(); } catch (err) { alert(err.message); }
    };
    push.append(hubToggle(m.enabled, async (on) => { await hubCall("POST", `/hub/mcp/${m.name}/enabled`, { enabled: on }); openMCP(); }), test, del);
    head.append(el("strong", "", m.name), push);
    card.append(head, el("div", "cmd", m.command), auth, out);
    panel.append(card);
  }
}
