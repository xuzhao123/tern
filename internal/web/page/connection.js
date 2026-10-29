import { invalidateActivity } from "./activity.js";
import { loadRuns, renderShell } from "./shell.js";
import { $, HOST, HOST_STATE, el, hostURL, ui } from "./util.js";
// ---------- 顶栏的主机与连接状态，以及主机菜单 ----------
// GET /connection 是 SSE：连上先推一次，之后任何一台的状态或 runs 变了再推（网页端自己的接口，不带主机前缀）。
// 断线重连由网页端这一侧的 appclient 负责（它连 app-server），页面这里只是换个字样。
// 顶栏的 #host 点开是主机菜单：连着的各台（每台可以断开、更新远端的程序、复制本机的模型密钥）、
// 连不上的（写清是哪一步没成，按 kind 给按钮）与候选。
// 换主机是整页跳转：ui.run 和各页面的缓存都假定只有一台主机，跳过去就全部重来。
let seen = null; // 上一次各台的 runs：任何一台变了就重新加载左侧列表

export function watchConnection() {
  const es = new EventSource("/connection");
  es.onmessage = (m) => {
    ui.connectionLost = false;
    const hosts = (ui.hosts = JSON.parse(m.data).hosts || []);
    renderHost();
    renderShell();
    // 当前这台被断开了：整页跳到剩下的第一台（本机是 /，远端是 /h/<别名>/）。
    if (!hosts.some((h) => h.alias === HOST)) {
      const next = hosts.find((h) => h.state !== "failed");
      if (next) { location.href = hostURL(next.alias); return; }
    }
    const runs = hosts.map((h) => `${h.alias}:${h.runs}:${h.state}`).join(",");
    // 第一条也要重画列表：页面启动时列表先按当前主机画过一次，这会儿才知道一共几台、怎么分组。
    if (seen !== runs) loadRuns();
    seen = runs;
  };
  es.onerror = () => { seen = null; ui.connectionLost = true; invalidateActivity(); };
}

// 顶栏那一个：当前主机 · 连接状态；重连中、连不上换成醒目的样式，悬停看这台是哪种连接或那次连不上的报错。
function renderHost() {
  const host = $("host"), my = ui.hosts.find((h) => h.alias === HOST);
  host.classList.toggle("bad", !!my && my.state !== "connected");
  if (!my) { host.textContent = HOST || "本机"; return; }
  host.textContent = `${my.remote ? my.alias : "本机"} · ${HOST_STATE[my.state] || my.state}`;
  host.title = my.hint || my.error || (my.remote ? `经 ssh 连到 ${my.alias}` : "连本机的 app-server");
  renderMenu();
}

function openMenu(open) {
  $("host-menu").hidden = !open;
  $("host").setAttribute("aria-expanded", String(open));
  if (!open) return;
  renderMenu();
  loadCandidates().then(renderMenu);
}

// 菜单上半部分：连着的各台与连不上的；下半部分是还能连的候选和手填的别名。
function renderMenu() {
  if ($("host-menu").hidden) return;
  const manyConnected = ui.hosts.filter((h) => h.state !== "failed").length > 1;
  $("hm-hosts").replaceChildren(...ui.hosts.map((h) => hostItem(h, manyConnected)));
  $("hm-candidates").replaceChildren(...(ui.candidates || []).map((alias) => {
    const b = el("button", "hm-cand", alias || "本机");
    b.type = "button";
    b.onclick = () => connect(alias);
    return b;
  }));
}

// 一台主机的一项：上面一行是名字、状态与断开，下面按情况补一行说明与按钮。
function hostItem(h, manyConnected) {
  const item = el("div", "hm-item");
  const row = el("div", "hm-row" + (h.alias === HOST ? " on" : ""));
  const go = el("button", "hm-go");
  go.type = "button";
  go.append(el("span", "hm-name", h.remote ? h.alias : "本机"), el("span", "hm-state" + (h.state === "connected" ? "" : " bad"), HOST_STATE[h.state] || h.state));
  if (h.error) go.title = h.error;
  go.onclick = () => {
    if (h.state === "failed") connect(h.alias); // 连不上的：点它就是再连一次
    else if (h.alias === HOST) openMenu(false);
    else location.href = hostURL(h.alias);
  };
  row.append(go);
  // 断开一台就不再记住它；只剩一台时没有“断开”。
  if (h.state === "failed" || manyConnected) {
    const off = el("button", "hm-off", "断开");
    off.type = "button";
    off.title = h.state === "failed" ? "不再记住这台" : "断开这一台，并且不再记住它";
    off.onclick = () => disconnect(h.alias);
    row.append(off);
  }
  item.append(row);
  const more = el("div", "hm-more");
  if (h.state === "failed") fillFailure(more, h);
  else if (h.remote) fillRemote(more, h);
  if (more.children.length) item.append(more);
  return item;
}

// 连不上的那台：hint 是给人看的下一步，detail 是 ssh 的原文；按钮按 kind 给。
function fillFailure(more, h) {
  if (h.hint) more.append(el("div", "hm-hint", h.hint));
  if (h.detail) more.append(el("div", "hm-detail", h.detail));
  const actions = el("div", "hm-actions");
  switch (h.kind) {
    case "hostkey": actions.append(hmButton("确认指纹", () => trustHostKeys(h))); break;
    case "no-tern": actions.append(hmButton("装到远端", () => install(h, "装到远端"))); break;
    case "protocol": actions.append(hmButton("用本机的 tern 更新远端", () => install(h, "更新远端"))); break;
    default: actions.append(hmButton("再试一次", () => connect(h.alias)));
  }
  more.append(actions);
}

// 连着的远端：程序不同就给“更新”，那边没配模型密钥就给“从本机复制密钥”。
function fillRemote(more, h) {
  const actions = el("div", "hm-actions");
  if (h.stale) actions.append(hmButton("用本机的 tern 更新远端", () => install(h, "更新远端")));
  if (!(h.providers || []).length) {
    more.append(el("div", "hm-hint", `远端没有配模型密钥（${h.home || "~"}/.env）`));
    more.append(keyPicker(h));
  }
  if (actions.children.length) more.append(actions);
}

function hmButton(text, onclick) {
  const b = el("button", "hm-btn", text);
  b.type = "button";
  b.onclick = onclick;
  return b;
}

// /hosts 是网页端自己的接口，不带主机前缀。连上就整页跳到那台，连不上把说明写在菜单里。
async function connect(alias) {
  message("");
  try {
    const res = await fetch("/hosts", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ alias }) });
    if (!res.ok) throw new Error(await failure(res));
    location.href = hostURL(alias);
  } catch (err) { message(err.message); }
}

async function disconnect(alias) {
  message("");
  try {
    // 路径里不能是空的：本机用 - 表示。
    const res = await fetch(`/hosts/${alias ? encodeURIComponent(alias) : "-"}`, { method: "DELETE" });
    if (!res.ok) throw new Error((await res.text()).trim());
    // 断开的是当前这台时，下一条 /connection 推来就整页跳到剩下的第一台。
    loadCandidates().then(renderMenu);
  } catch (err) { message(err.message); }
}

// 把本机的 tern 拷到远端：点了先确认，成功后照常连一次（远端的 proxy 会把那边的 app-server 换成新的）。
async function install(h, what) {
  message("");
  if (!confirm(`把本机的 tern 程序拷到 ${h.alias} 的 ~/.tern/bin/tern？远端正在跑的对话不会被打断。`)) return;
  try {
    const res = await fetch(`/hosts/${encodeURIComponent(h.alias)}/install`, { method: "POST" });
    if (!res.ok) throw new Error(await failure(res));
  } catch (err) { message(`${what}失败：${err.message}`); return; }
  connect(h.alias);
}

// 确认指纹：列出这台机器的公钥类型与指纹，人核对过才写进本机的 known_hosts，然后照常连一次。
async function trustHostKeys(h) {
  message("");
  let info;
  try {
    const res = await fetch(`/hosts/${encodeURIComponent(h.alias)}/hostkey`);
    if (!res.ok) throw new Error(await failure(res));
    info = await res.json();
  } catch (err) { message(err.message); return; }
  const lines = (info.keys || []).map((k) => `${k.type} ${k.fingerprint}`).join("\n");
  if (!confirm(`${h.alias}（${info.host}:${info.port}）的主机公钥：\n\n${lines}\n\n和你在那台机器上看到的一致再确认（在那台机器上：ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub）。`)) return;
  try {
    const res = await fetch(`/hosts/${encodeURIComponent(h.alias)}/hostkey`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ keys: info.keys || [] }) });
    if (!res.ok) throw new Error(await failure(res));
  } catch (err) { message(`写入 known_hosts 失败：${err.message}`); return; }
  connect(h.alias);
}

// “从本机复制密钥”：先列出本机 .env 里有值的密钥名，勾选后经 ssh 写进远端。
// 勾选状态按别名留着：/connection 每推一次菜单就重画，勾了的不能丢。
const keyPick = new Map(); // 别名 → {names, checked: Set}

function keyPicker(h) {
  const box = el("div", "hm-keys");
  const picked = keyPick.get(h.alias);
  if (!picked) {
    box.append(hmButton("从本机复制密钥", async () => {
      try {
        const res = await fetch(`/hosts/${encodeURIComponent(h.alias)}/keys`);
        if (!res.ok) throw new Error(await failure(res));
        const names = (await res.json()).names || [];
        keyPick.set(h.alias, { names, checked: new Set(names) });
      } catch (err) { message(err.message); return; }
      renderMenu();
    }));
    return box;
  }
  if (!picked.names.length) {
    box.append(el("div", "hm-detail", "本机的 .env 里没有可以复制的密钥"));
    return box;
  }
  for (const name of picked.names) {
    const label = el("label", "hm-key");
    const input = document.createElement("input");
    input.type = "checkbox";
    input.checked = picked.checked.has(name);
    input.onchange = () => { if (input.checked) picked.checked.add(name); else picked.checked.delete(name); };
    label.append(input, el("span", "", name));
    box.append(label);
  }
  box.append(hmButton(`复制到 ${h.alias}`, () => copyKeys(h, picked)));
  return box;
}

async function copyKeys(h, picked) {
  const names = [...picked.checked];
  if (!names.length) { message("一个密钥都没勾"); return; }
  if (!confirm(`这些密钥会写进 ${h.alias} 上的 ${h.home || "~"}/.env（权限 0600），那台机器上能读你文件的人都能看到。继续？`)) return;
  message("");
  try {
    const res = await fetch(`/hosts/${encodeURIComponent(h.alias)}/keys`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ names }) });
    if (!res.ok) throw new Error(await failure(res));
  } catch (err) { message(`写不进去：${err.message}`); return; }
  keyPick.delete(h.alias);
  message("已写入，新对话生效");
  renderMenu();
}

// 接口失败时的文字：这几条远端接口回的是 {error, kind, hint, detail}，优先给人看的 hint。
async function failure(res) {
  const text = (await res.text()).trim();
  try {
    const info = JSON.parse(text);
    return info.hint || info.error || text;
  } catch { return text; }
}

async function loadCandidates() {
  try {
    const res = await fetch("/hosts");
    ui.candidates = res.ok ? (await res.json()).candidates : [];
  } catch { ui.candidates = []; }
}

const message = (text) => { $("hm-msg").textContent = text; };

$("host").onclick = (ev) => { ev.stopPropagation(); openMenu($("host-menu").hidden); };
$("host-menu").onclick = (ev) => ev.stopPropagation();
document.addEventListener("click", () => openMenu(false));
$("hm-form").onsubmit = (ev) => {
  ev.preventDefault();
  const alias = $("hm-alias").value.trim();
  $("hm-alias").value = "";
  connect(alias);
};
