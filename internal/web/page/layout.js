import { $, HOST, el, ui } from "./util.js";
import { hideWorkPanel, openWorkTab, showWorkPanel } from "./workpanel.js";

const paths = {
  panel: '<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M15 4v16"/>',
  sidebar: '<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M9 4v16"/>',
  chevron: '<path d="m8 10 4 4 4-4"/>',
  diff: '<path d="M8 3v12a4 4 0 0 0 4 4h3M8 7h7"/><circle cx="8" cy="3" r="1.5"/><circle cx="17" cy="7" r="2"/><circle cx="17" cy="19" r="2"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4 4"/>',
  activity: '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4"/>',
  chat: '<path d="M21 11.5a8.5 8.5 0 0 1-8.5 8.5H4l-1 1v-9.5a9 9 0 0 1 18 0Z"/>',
  folder: '<path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3ZM3 10h18"/>',
};
export const chromeIcon = (name) => `<svg class="i" viewBox="0 0 24 24" aria-hidden="true">${paths[name] || paths.chevron}</svg>`;
function action(label, fn) { const button = el("button", "", label); button.type = "button"; button.onclick = fn; return button; }
function menu(label, className = "") {
  const details = el("details", "chrome-menu " + className), summary = el("summary"), content = el("div", "chrome-menu-content");
  summary.append(el("span", "", label)); summary.insertAdjacentHTML("beforeend", chromeIcon("chevron"));
  details.append(summary, content); return { details, summary, content };
}
function fitMenu(details) {
  const content = details.querySelector(":scope > .chrome-menu-content");
  if (!content || !details.open) return;
  content.style.transform = "";
  const box = content.getBoundingClientRect(), shift = box.left < 8 ? 8 - box.left : box.right > innerWidth - 8 ? innerWidth - 8 - box.right : 0;
  if (shift) content.style.transform = `translateX(${shift}px)`;
}
export function initLayout() {
  const brand = document.querySelector(".brand"), collapse = action("", () => { if (matchMedia("(max-width: 760px)").matches) document.body.classList.remove("nav-open"); else document.body.classList.toggle("sidebar-collapsed"); });
  collapse.className = "chrome-icon"; collapse.innerHTML = chromeIcon("sidebar"); collapse.title = "收起侧栏"; collapse.setAttribute("aria-label", "收起侧栏"); brand.append(collapse);
  $("menu").onclick = () => { if (matchMedia("(max-width: 760px)").matches) document.body.classList.toggle("nav-open"); else document.body.classList.toggle("sidebar-collapsed"); };
  const brandActions = el("div", "brand-actions");
  $("command-menu").innerHTML = chromeIcon("search"); $("command-menu").className = "chrome-icon"; $("command-menu").title = "搜索任务"; $("command-menu").setAttribute("aria-label", "搜索任务");
  $("activity-open").innerHTML = chromeIcon("activity"); $("activity-open").append(el("span", "sr-only", "活动")); $("activity-open").lastChild.id = "activity-label"; $("activity-open").className = "chrome-icon"; $("activity-open").title = "活动";
  brandActions.append($("activity-open"), $("command-menu"), collapse); brand.append(brandActions);
  $("new-chat").lastChild.textContent = "新聊天";
  $("add-project").textContent = "+"; $("add-project").title = "添加项目"; $("add-project").setAttribute("aria-label", "添加项目");
  const theme = document.querySelector(".theme-control"); document.querySelector(".nav-tools").append(theme);
  const filter = menu("项目", "sidebar-filter-menu"), navlabel = document.querySelector(".navlabel");
  filter.summary.firstElementChild.id = "filter-label";
  filter.content.append($("list-tools")); navlabel.firstElementChild.replaceWith(filter.details);

  const topbar = $("topbar"), heading = $("heading"), task = menu("", "task-menu");
  task.summary.replaceChildren(heading); task.summary.insertAdjacentHTML("beforeend", chromeIcon("chevron"));
  topbar.insertBefore(task.details, $("work-btn"));
  task.content.append(el("div", "chrome-menu-label", "任务与执行位置"), $("execution-context"), $("run-id"), $("goal"), $("export-btn"), $("side-btn"), $("apply-copy-btn"), document.querySelector(".seg"));
  const open = menu("打开", "resource-menu");
  const projectFiles = action("项目文件", () => openWorkTab("project-files", "", "项目文件")); projectFiles.id = "open-project-files";
  open.content.append(action("项目操作", () => openWorkTab("operations", "", "项目操作")), action("持续 Goal", () => openWorkTab("goal", "", "持续 Goal")), action("计划", () => openWorkTab("plan", "", "计划")), action("选择 Skill", () => document.dispatchEvent(new Event("tern:choose-skill"))), projectFiles, $("files-btn"), $("jobs-btn"), action("子智能体", () => document.dispatchEvent(new Event("tern:open-agents"))), action("浏览器过程", () => document.dispatchEvent(new Event("tern:open-browser"))));
  topbar.insertBefore(open.details, $("work-btn"));
  $("files-btn").textContent = "任务产物"; $("files-btn").title = "本任务临时目录中的文件";
  $("work-btn").innerHTML = chromeIcon("panel"); $("work-btn").title = "显示或收起工作区"; $("work-btn").setAttribute("aria-label", "显示或收起工作区");
  $("work-btn").onclick = () => $("workpanel").hidden ? showWorkPanel() : hideWorkPanel();
  $("diff-btn").innerHTML = chromeIcon("diff"); $("diff-btn").title = "查看改动"; $("diff-btn").setAttribute("aria-label", "查看改动"); topbar.insertBefore($("diff-btn"), $("work-btn"));
  const project = menu("", "task-project-menu"); project.details.id = "task-project-menu"; project.summary.firstElementChild.id = "task-project-name";
  const projectInfo = el("div", "project-execution-info"); projectInfo.id = "task-project-detail"; project.content.append(projectInfo);
  const hostbox = $("hostbox"); $("composer-context").append(hostbox, project.details, $("workspace"), $("copy-pick"));
  $("composer-context").append(el("span", "composer-footnote", "输入 / 查看命令"));
  const crow = document.querySelector(".crow");
  $("attach").innerHTML = chromeIcon("plus");
  const model = menu("", "composer-model-menu"); model.details.id = "composer-model-info";
  model.summary.firstElementChild.id = "composer-model-name";
  model.content.append(el("div", "chrome-menu-label", "模型与推理"), el("div", "composer-model-detail"));
  crow.insertBefore(model.details, $("provider"));
  for (const [id, label] of [["provider", "供应商"], ["model", "具体模型"], ["effort", "推理强度"]]) {
    const field = el("label", "model-setting"), control = $(id); field.id = "model-setting-" + id;
    field.append(el("span", "", label), control); model.content.append(field);
  }
  const newModelTask = action("使用其他模型新建任务", () => $("new-chat").click()); newModelTask.id = "new-model-task"; model.content.append(newModelTask);
  crow.insertBefore($("perm"), model.details);
  crow.insertBefore($("mode-chip"), model.details);
  crow.insertBefore($("ctx-ring"), model.details);
  $("composer-goal").onclick = () => $("goal").click();
  document.addEventListener("toggle", (event) => { if (event.target.matches(".chrome-menu")) fitMenu(event.target); }, true);
  window.addEventListener("resize", () => document.querySelectorAll(".chrome-menu[open]").forEach(fitMenu));
  document.addEventListener("click", (event) => {
    for (const details of document.querySelectorAll(".chrome-menu[open], .nav-tools[open]")) {
      if (!details.contains(event.target)) details.open = false;
      else if (event.target.closest("button") && !event.target.closest("#run-id")) { details.open = false; if (event.target.closest("button") === document.activeElement) details.querySelector("summary").focus(); }
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    const menus = [...document.querySelectorAll(".chrome-menu[open], .nav-tools[open]")], hostOpen = !$("host-menu").hidden;
    if (!menus.length && !hostOpen) return;
    event.preventDefault(); event.stopImmediatePropagation();
    menus.forEach((d) => { d.open = false; d.querySelector("summary").focus(); });
    if (hostOpen) { $("host-menu").hidden = true; $("host").setAttribute("aria-expanded", "false"); $("host").focus(); }
  }, true);
}
export function renderLayout() {
  if (!$("composer-model-info")) return;
  const r = ui.run;
  $("composer-model-info").hidden = !!r?.source;
  const modelName = r ? r.model || "模型未记录" : $("model").selectedOptions[0]?.textContent.replace(/^默认（|）$/g, "") || $("provider").selectedOptions[0]?.textContent || "选择模型";
  const effort = $("effort").hidden ? "" : $("effort").selectedOptions[0]?.textContent.replace(/^推理：/, "") || "";
  $("composer-model-name").textContent = modelName + (effort ? " · " + effort : "");
  $("composer-goal").hidden = ui.page !== "chat" || !r?.goal || !!r.source || !!r.eval;
  $("composer-goal-text").textContent = r?.goal || "";
  $("composer-goal").title = "查看或修改本任务目标";
  // 只读计划与无人值守（沙箱内）平时藏起来，只有对话当前就是这两档时才显示；完全访问标红。
  const perm = $("perm");
  for (const opt of perm.options) if (opt.dataset.advanced) opt.hidden = opt.value !== perm.value;
  perm.dataset.mode = perm.value;
  perm.classList.toggle("warn", perm.value === "unrestricted");
  document.querySelector(".composer-model-detail").textContent = !r ? "" : r.model ? `${r.model}。本任务沿用创建时的模型配置。` : "这条任务未记录具体模型。";
  for (const id of ["provider", "model", "effort"]) $("model-setting-" + id).hidden = $(id).hidden || (id !== "effort" && !!r);
  $("new-model-task").hidden = !r;
  document.querySelector(".resource-menu").hidden = !r || ui.page !== "chat";
  $("open-project-files").disabled = !r?.workspace;
  $("task-project-menu").hidden = !r;
  const project = r && (ui.projects?.[HOST] || []).find((p) => p.id === r.workspaceID);
  $("task-project-name").textContent = r?.source ? "导入记录" : (project?.name || r?.workspace || "临时空间") + (r?.copy ? " · 副本" : "");
  $("task-project-detail").textContent = $("execution-context").textContent;
  const hostTarget = ui.page === "chat" ? $("composer-context") : $("topbar");
  if ($("hostbox").parentElement !== hostTarget) hostTarget.prepend($("hostbox"));
  document.body.classList.toggle("has-task", !!r);
}
