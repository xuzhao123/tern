import { loadRuns } from "./shell.js";
import { $ } from "./util.js";
// ---------- 顶栏的主机与连接状态 ----------
// GET /connection 是 SSE：连上先推一次，之后状态或 runs 变了再推。
// 断线重连由网页端这一侧的 appclient 负责（它连 app-server），页面这里只是换个字样；
// runs 加一说明对话列表变了，重新加载。
const STATE = { connected: "已连接", reconnecting: "重连中" };
let runs = -1;
export function watchConnection() {
  const es = new EventSource("/connection");
  es.onmessage = (m) => {
    const c = JSON.parse(m.data);
    const host = $("host");
    host.textContent = `${c.remote ? c.host : "本机"} · ${STATE[c.state] || c.state}`;
    host.title = c.remote ? `经 ssh 连到 ${c.host}` : "连本机的 app-server";
    host.classList.toggle("bad", c.state === "reconnecting");
    if (c.runs === runs) return;
    const first = runs < 0;
    runs = c.runs;
    // 第一条只是记下起点的数目：启动时 main.js 自己会加载一次列表。
    if (!first) loadRuns();
  };
}
