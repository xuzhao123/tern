import { loadRuns } from "./shell.js";
import { $, HOST, HOST_STATE, el, hostURL, ui } from "./util.js";
// ---------- 顶栏的主机与连接状态，以及主机菜单 ----------
// GET /connection 是 SSE：连上先推一次，之后任何一台的状态或 runs 变了再推（网页端自己的接口，不带主机前缀）。
// 断线重连由网页端这一侧的 appclient 负责（它连 app-server），页面这里只是换个字样。
// 顶栏的 #host 点开是主机菜单：连着的各台（每台可以断开）、连不上的（failed，点了重试）与候选。
// 换主机是整页跳转：ui.run 和各页面的缓存都假定只有一台主机，跳过去就全部重来。
let seen = null; // 上一次各台的 runs：任何一台变了就重新加载左侧列表

export function watchConnection() {
  const es = new EventSource("/connection");
  es.onmessage = (m) => {
    const hosts = (ui.hosts = JSON.parse(m.data).hosts || []);
    renderHost();
    // 当前这台被断开了：整页跳到剩下的第一台（本机是 /，远端是 /h/<别名>/）。
    if (!hosts.some((h) => h.alias === HOST)) {
      const next = hosts.find((h) => h.state !== "failed");
      if (next) { location.href = hostURL(next.alias); return; }
    }
    const runs = hosts.map((h) => `${h.alias}:${h.runs}`).join(",");
    // 第一条也要重画列表：页面启动时列表先按当前主机画过一次，这会儿才知道一共几台、怎么分组。
    if (seen !== runs) loadRuns();
    seen = runs;
  };
}

// 顶栏那一个：当前主机 · 连接状态；重连中、连不上换成醒目的样式，悬停看这台是哪种连接或那次连不上的报错。
function renderHost() {
  const host = $("host"), my = ui.hosts.find((h) => h.alias === HOST);
  host.classList.toggle("bad", !!my && my.state !== "connected");
  if (!my) { host.textContent = HOST || "本机"; return; }
  host.textContent = `${my.remote ? my.alias : "本机"} · ${HOST_STATE[my.state] || my.state}`;
  host.title = my.error || (my.remote ? `经 ssh 连到 ${my.alias}` : "连本机的 app-server");
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
  const connected = ui.hosts.filter((h) => h.state !== "failed");
  $("hm-hosts").replaceChildren(...ui.hosts.map((h) => {
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
    if (h.state === "failed" || connected.length > 1) {
      const off = el("button", "hm-off", "断开");
      off.type = "button";
      off.title = h.state === "failed" ? "不再记住这台" : "断开这一台，并且不再记住它";
      off.onclick = () => disconnect(h.alias);
      row.append(off);
    }
    return row;
  }));
  $("hm-candidates").replaceChildren(...(ui.candidates || []).map((alias) => {
    const b = el("button", "hm-cand", alias || "本机");
    b.type = "button";
    b.onclick = () => connect(alias);
    return b;
  }));
}

// /hosts 是网页端自己的接口，不带主机前缀。连上就整页跳到那台，连不上把报错写在菜单里。
async function connect(alias) {
  message("");
  try {
    const res = await fetch("/hosts", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ alias }) });
    if (!res.ok) throw new Error((await res.text()).trim());
    location.href = hostURL(alias);
  } catch (err) { message("连不上：" + err.message); }
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
