import { $, HOST, api, el, ui } from "./util.js";
import { backToInput } from "./composer.js";
import { subState, subTasks } from "./substate.js";

const dialog=el("dialog","resource-dialog"),heading=el("h2"),text=el("textarea"),note=el("p","meta"),buttons=el("div","activity-actions");
heading.id="subinput-title";dialog.setAttribute("aria-labelledby",heading.id);text.rows=6;text.maxLength=32000;text.setAttribute("aria-label","子任务补充");dialog.append(heading,text,note,buttons);document.body.append(dialog);
let target,record={},busy=false,focus;
function button(label,fn){const b=el("button","ghost",label);b.type="button";b.onclick=fn;return b;}
function current(){return target && ui.account===target.account && ui.run===target.run;}
function key(){return `tern-subinput-v1:${encodeURIComponent(target.account)}:${encodeURIComponent(HOST)}:${target.run.id}:${target.id}:${target.call}`;}
function save(){try{localStorage.setItem(key(),JSON.stringify(record));return true;}catch{note.textContent="保存失败，请先复制输入；没有派发新请求。";return false;}}
function active(){const item=current()&&subTasks(target.run).find((i)=>i.id===target.id);return item?.t.id===target.call && ["starting","running","waiting"].includes(subState(target.run,item).key) && target.run.subControl && !target.run.exit;}
function render(){
  if(!current()){note.textContent="任务或账号已改变，请关闭后重新打开";text.disabled=true;buttons.replaceChildren(button("关闭",()=>dialog.close()));return;}
  text.disabled=busy||!!record.request;
  const pending=record.state||"draft";
  note.textContent=({draft:"此输入仅属于所选子任务。直接发送由服务端核对目标；告诉主 agent 只加入主输入草稿。",pending:"子任务已接收，尚未确认采纳。",consumed:"子任务已采纳，重复查询不会重发。",blocked:"补充被子任务提交规则拦截，原输入仍保留。",unknown:"结果未知，请查询状态；不要直接重复发送。",stopping:"停止请求已送达，实际结束以子任务状态为准。"})[pending]||pending;
  const send=button("直接补充此子任务",()=>submit("send"));send.disabled=busy||!active()||!!record.request||!text.value.trim();
  const stop=button("只停止此子任务",()=>{if(confirm(`只停止子任务「${target.title}」？已发生的文件修改不会撤回。`))submit("stop");});stop.disabled=busy||!active()||!!record.request;
  const check=button("查询原请求状态",checkStatus);check.disabled=busy||!record.request;
  const parent=button("告诉主 agent（加入草稿）",()=>{
    if(!current())return;
    backToInput(`[子任务 ${target.id} · 尝试 ${target.call} · 当前补充状态 ${record.state||"未发送"}]\n${text.value.trim() || "请检查此子任务状态；若需要恢复，沿原 task_id 续跑，先核对已完成副作用。"}`);dialog.close();
  });
  buttons.replaceChildren(send,stop,check,parent,button("关闭",()=>dialog.close()));
  if(record.request && ["consumed","blocked"].includes(record.state))buttons.append(button("开始新的补充",()=>{record={text:""};text.value="";save();render();}));
  if(!active())note.append(" 当前不能直接操作：子任务已结束、尝试已改变或旧运行实例不支持。可让主 agent 核对并按原 ID 恢复。");
}
async function call(body){const res=await fetch(api(`/runs/${target.run.id}/subagent/control`),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({task_id:target.id,call_id:target.call,...body}),signal:AbortSignal.timeout(15000)});if(!res.ok)throw new Error((await res.text()).trim());return res.json();}
async function submit(action){
  if(busy||!current()||!active()||record.request)return;
  record={text:text.value,request:crypto.randomUUID(),action,state:"unknown"};if(!save()){record.request=null;return;}
  const owner=target;busy=true;render();
  try{const data=await call({action,text:action==="send"?record.text:"",request_id:record.request});if(target!==owner)return;record.state=data.state||"unknown";}
  catch(error){if(target===owner)record.state="unknown";}
  finally{if(target===owner){busy=false;save();render();}}
}
async function checkStatus(){
  if(busy||!current()||!record.request)return;const owner=target;busy=true;render();
  try{
    if(record.action==="stop") {const res=await fetch(api(`/requests/${record.request}`));if(!res.ok)throw new Error("回执不可用");const data=await res.json();record.state=data.state==="accepted"?(data.response?.state||"unknown"):"unknown";}
    else {const data=await call({action:"status",request_id:record.request});if(target!==owner)return;record.state=data.state||data.items?.[record.request]?.state||"unknown";}
  }catch{if(target===owner)record.state="unknown";}
  finally{if(target===owner){busy=false;save();render();}}
}
function open(r,item){
  focus=document.activeElement;target={account:ui.account,run:r,id:item.id,call:item.t.id,title:item.title};record={};busy=false;
  try{record=JSON.parse(localStorage.getItem(key())||"{}");}catch{record={};}
  text.value=typeof record.text==="string"?record.text:"";heading.textContent="干预子任务 · "+item.title.slice(0,80);heading.title=item.title;render();if(!dialog.open)dialog.showModal();text.focus();
}
text.oninput=()=>{record.text=text.value;save();render();};
dialog.addEventListener("close",()=>focus?.focus?.());
export function subInputActions(r,item){
  const bar=el("div","activity-actions");
  bar.append(button("补充 / 停止 / 恢复",()=>open(r,item)),button("复制子任务链接",async()=>{
    const url=new URL(location.href);url.searchParams.set("run",r.id);url.searchParams.set("sub",item.id);
    try{await navigator.clipboard.writeText(url.href);ui.hint="子任务链接已复制";}catch{ui.hint="无法复制，可手动使用："+url.href;}$("hint").textContent=ui.hint;
  }));return bar;
}
