import { $, HOST, api, el, ui } from "./util.js";
import { saveDraft } from "./drafts.js";

let refs = [], owner = "", generation = 0, trigger = null;
const dialog = el("dialog","resource-dialog"), title = el("h2", "", "选择 Skill"), query = el("input"), list = el("div"), note = el("p", "meta"), close = el("button", "ghost", "关闭");
title.id = "skill-picker-title"; dialog.setAttribute("aria-labelledby", title.id);
query.type = "search"; query.placeholder = "按名称或用途搜索"; query.setAttribute("aria-label", "搜索 Skill"); close.type = "button";
dialog.append(title, query, note, list, close); document.body.append(dialog);
// 这条对话（或新对话所选工作区的设置）能不能用这个 skill：选择框和 / 菜单共用这一条判断。
export function skillUsable(skill) {
  return skill.enabled && !!skill.version && (ui.run ? ui.run.skills.includes(skill.name) : (ui.wsSettings?.[$("workspace").value] || ui.settings?.effective)?.values?.skills === "on");
}
// / 菜单选中一个 skill：同在选择框里点它，加进引用条。返回不能加的原因，加上了返回空串。
export function addSkill(skill) {
  if (!skillUsable(skill)) return "这个 skill 在当前任务里没启用";
  if (refs.some((r) => r.name === skill.name)) return "已经选了这个 skill";
  if (refs.length >= 8) return "最多选 8 个 skill";
  refs.push({ name: skill.name, path: skill.path, version: skill.version }); renderRefs(); saveDraft(); return "";
}
export function selectedSkills() { return refs.map(({name,path,version}) => ({name,path,version})); }
export function restoreSkills(value) {
  refs = (Array.isArray(value) ? value : []).filter((r) => typeof r?.name === "string" && typeof r.path === "string" && typeof r.version === "string").slice(0,8);
  renderRefs();
}
function renderRefs() {
  const strip = $("skill-strip"); if (!strip) return;
  strip.replaceChildren(); strip.hidden = !refs.length;
  for (const ref of refs) {
    const chip = el("button", "ghost", `$${ref.name} ×`); chip.type = "button"; chip.title = `${ref.path} · ${ref.version.slice(0,12)} · 点击移除`;
    chip.onclick = () => { refs=refs.filter((r)=>r.name!==ref.name);renderRefs();saveDraft(); };strip.append(chip);
  }
}
function scope() { return `${ui.account}\0${HOST}\0${ui.run?.id || $("workspace").value}`; }
async function open() {
  owner = scope(); const request = ++generation; query.value = ""; list.replaceChildren(); note.textContent = `${HOST || "当前执行主机"} · 读取技能…`;
  if(!dialog.open)dialog.showModal(); query.focus();
  try {
    const response = await fetch(api("/hub")); if(!response.ok)throw new Error(await response.text()); const data=await response.json();
    if(generation!==request||!dialog.open||scope()!==owner)return;
    const render=()=>{
      list.replaceChildren(); const q=query.value.trim().toLocaleLowerCase();
      for(const skill of data.skills.filter((s)=>`${s.name} ${s.description}`.toLocaleLowerCase().includes(q))) {
        const enabled=skillUsable(skill);
        const button=el("button","command-result");button.type="button";button.disabled=!enabled || refs.length>=8 || refs.some((r)=>r.name===skill.name);
        button.append(el("span","",skill.name),el("small","",`${skill.builtin ? "内置" : "执行主机个人目录"} · ${enabled ? "可用" : "任务未启用或版本不支持"} · ${skill.description}`));
        button.onclick=()=>{
          if(scope()!==owner){note.textContent="任务或项目已变化，请关闭后重新选择";return;}
          refs.push({name:skill.name,path:skill.path,version:skill.version});
          if(trigger && $("query").value===trigger.text) {$("query").value=trigger.text.slice(0,trigger.at)+trigger.text.slice(trigger.at+1);}
          trigger=null;renderRefs();saveDraft();dialog.close();$("query").focus();
        };list.append(button);
      }
      note.textContent=`${HOST || "当前执行主机"} · 已选择 ${refs.length}/8。发送时核对来源和版本；运行中选择须等下一轮发送。`;
      if(!list.children.length)list.append(el("p","meta","没有匹配技能"));
    };
    query.oninput=(event)=>{if(!event.isComposing)render();};query.oncompositionend=render;render();
  } catch(error){if(generation===request)note.textContent="读取失败："+error.message;}
}
close.onclick=()=>dialog.close();dialog.addEventListener("close",()=>{generation++;trigger=null;$("query").focus();});
document.addEventListener("tern:choose-skill",()=>{trigger=null;open();});
$("query").addEventListener("input",(event)=>{
  if(event.isComposing)return;const box=$("query"),at=box.selectionStart;
  if(/(^|\s)\$$/.test(box.value.slice(0,at))) {trigger={text:box.value,at:at-1};open();}
});
