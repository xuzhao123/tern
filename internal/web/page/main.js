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
import "./context.js";
import "./eval.js";
import { restoreRunURL } from "./follow.js";
import { watchConnection } from "./connection.js";
import { mathLoaded } from "./md.js";
import { loadEnabled } from "./pages.js";
import { loadModels, loadRuns, loadSettings, markCurrentSeen, syncComposer } from "./shell.js";
import { $, ui } from "./util.js";
import { initNavigation } from "./navigation.js";
import { initDrafts, saveDraft } from "./drafts.js";
import { initLayout } from "./layout.js";
window.addEventListener("popstate", restoreRunURL);
// 切回前台补记一次“看过”：在后台时跑完、或者停在等你审批的对话，回来就不留未读圆点。
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState !== "visible") return;
  markCurrentSeen();
  loadRuns();
});
// 会话过期或被注销时，接口返回 401，回到登录页。
const rawFetch = window.fetch.bind(window);
window.fetch = async (...args) => {
  const input = args[0], options = args[1] || {};
  if (ui.account && new URL(input instanceof Request ? input.url : input, location.href).origin === location.origin) {
    const headers = new Headers(options.headers || (input instanceof Request ? input.headers : {}));
    headers.set("X-Tern-Account", ui.account);
    args[1] = { ...options, headers };
  }
  const res = await rawFetch(...args);
  if (res.status === 401) location.href = "/login";
  if (res.headers.get("X-Tern-Account-Changed")) { saveDraft(); location.reload(); throw new Error("登录账号已改变，已保留原账号草稿"); }
  return res;
};
// 当前账号：显示名字。
async function loadMe() {
  const res = await fetch("/me");
  if (!res.ok) return;
  const me = await res.json();
  ui.account = me.name;
  $("avatar").textContent = me.name.slice(0, 1);
  $("whoami").textContent = me.name;
  $("whoami").title = "当前账号";
  syncComposer();
}
$("logout").onclick = async () => { saveDraft(); await fetch("/logout", { method: "POST" }); location.href = "/login"; };
// KaTeX 异步加载，不挡页面启动：模块跑起来时还没到，就等它到了重画一次（之前公式先按源码显示）。
if (!window.katex) $("katex-js").addEventListener("load", mathLoaded);
async function boot() {
  await loadMe();
  if (!ui.account) return;
  initLayout();
  initNavigation();
  await Promise.all([loadModels(), loadSettings()]);
  ui.ready = true;
  initDrafts();
  restoreRunURL();
  loadEnabled();
  watchConnection();
}
boot().catch((error) => { console.error("页面初始化失败", error); $("hint").textContent = "页面初始化失败，请刷新重试；已有草稿没有清除。"; });
