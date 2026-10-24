import "./runs.js";
import "./analysis.js";
import "./render.js";
import "./observe.js";
import "./chat.js";
import "./agents.js";
import "./sandbox.js";
import "./browser.js";
import "./files.js";
import "./composer.js";
import "./eval.js";
import { restoreRunURL } from "./follow.js";
import { watchConnection } from "./connection.js";
import { mathLoaded } from "./md.js";
import { loadEnabled } from "./pages.js";
import { loadModels, loadSettings, syncComposer } from "./shell.js";
import { $, ui } from "./util.js";
window.addEventListener("popstate", restoreRunURL);
// 会话过期或被注销时，接口返回 401，回到登录页。
const rawFetch = window.fetch.bind(window);
window.fetch = async (...args) => {
  const res = await rawFetch(...args);
  if (res.status === 401) location.href = "/login";
  return res;
};
// 当前账号：显示名字。
async function loadMe() {
  const res = await fetch("/me");
  if (!res.ok) return;
  const me = await res.json();
  $("avatar").textContent = me.name.slice(0, 1);
  $("whoami").textContent = me.name;
  $("whoami").title = "当前账号";
  syncComposer();
}
$("logout").onclick = async () => { await fetch("/logout", { method: "POST" }); location.href = "/login"; };
// KaTeX 异步加载，不挡页面启动：模块跑起来时还没到，就等它到了重画一次（之前公式先按源码显示）。
if (!window.katex) $("katex-js").addEventListener("load", mathLoaded);
loadMe();
restoreRunURL();
loadEnabled();
loadModels();
loadSettings();
watchConnection();
