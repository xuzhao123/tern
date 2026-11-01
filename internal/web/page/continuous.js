import { $, HOST, api, el, ui } from "./util.js";
import { loadRuns } from "./shell.js";
import { openWorkTab, workTab } from "./workpanel.js";

const states={running:"运行中",pausing:"暂停中",paused:"已暂停",waiting_user:"等待用户",blocked:"受阻",exhausted:"预算耗尽",completed:"已完成",ended:"已结束（未完成）"};
function button(label,fn){const b=el("button","ghost",label);b.type="button";b.onclick=fn;return b;}
const banner=button("",()=>openWorkTab("goal","","持续 Goal"));banner.id="continuous-banner";banner.hidden=true;$("composer-wrap").prepend(banner);
function updateBanner(){const g=ui.run?.continuous;banner.hidden=!g;banner.textContent=g?`◎ 持续 Goal · ${states[g.state]||g.state} · ${g.text}`:"";}
document.addEventListener("tern:task-state-changed",()=>{updateBanner();if(workTab()?.kind==="goal")renderContinuous();});
document.addEventListener("tern:draft-changed",updateBanner);
export function renderContinuous(){
  const tab=workTab(),r=ui.run;if(tab?.kind!=="goal"||!r)return;const s=tab.state,panel=$("subpanel"),account=ui.account;
  if(s.owner!==r){clearTimeout(s.timer);s.node=null;s.owner=r;}
  if(s.node){panel.hidden=false;if(panel.firstElementChild!==s.node)panel.replaceChildren(s.node);s.drawStatus?.();return;}
  const key=`tern-goal-v1:${encodeURIComponent(account)}:${encodeURIComponent(HOST)}:${r.id}`,pendingPrefix=key+":pending:";
  const root=s.node=el("section","goal-view"),head=el("div","sp-head"),body=el("div","sp-body"),summary=el("div"),note=el("p","meta"),form=el("div","goal-form"),actions=el("div","activity-actions");
  const valid=()=>ui.run===r&&ui.account===account&&!s.closed;
  let editing=false,base=0,fields={},pending=null,busy=false,drawn="";
  function readPending(){try{for(let i=0;i<localStorage.length;i++){const name=localStorage.key(i);if(name?.startsWith(pendingPrefix))return JSON.parse(localStorage.getItem(name));}}catch{note.textContent="不能读取Goal操作回执。";}return null;}
  pending=readPending();
  function setPending(value){try{if(value)localStorage.setItem(pendingPrefix+value.request_id,JSON.stringify(value));else if(pending)localStorage.removeItem(pendingPrefix+pending.request_id);pending=value||readPending();return true;}catch{note.textContent="无法保存操作回执，本次未执行；请检查浏览器存储。";return false;}}
  async function load(){try{const res=await fetch(api(`/runs/${r.id}/continuous`));if(!res.ok)throw new Error(await res.text());const data=await res.json();if(!valid())return;r.continuous=data.continuous_goal;drawStatus();updateBanner();}catch(error){if(valid())note.textContent="Goal读取失败："+error.message;}}
  head.append(el("strong","","持续 Goal"),button("刷新",load));root.append(head,body);body.append(note,summary,form,actions);panel.hidden=false;panel.replaceChildren(root);
  function drawStatus(){
    if(!valid())return;const g=r.continuous;const version=JSON.stringify([g,busy,pending,editing,base]);if(version===drawn)return;drawn=version;summary.replaceChildren();
    if(!g)summary.append(el("p","meta","需要明确启动才会持续推进。普通目标文字不会自动执行。"));
    else{
      summary.append(el("h3","",states[g.state]||g.state),el("p","",g.text),el("p","meta",`版本 ${g.revision} · 自动轮次 ${g.turns}/${g.max_turns||"未设置（系统上限100）"} · 已报告 ${g.used_tokens} tokens${g.usage_unknown?"（部分用量未知）":""} / ${g.max_tokens||"未设置"}`));
      summary.append(el("p","meta",`截至本次状态更新，墙钟时间：${Math.max(0,Math.floor(((["completed","ended","exhausted"].includes(g.state)?Date.parse(g.updated):Date.now())-Date.parse(g.started))/1000))} 秒 / ${g.max_seconds||"未设置"}（包含等待与暂停）`));
      if(g.constraints)summary.append(el("p","meta","约束："+g.constraints));
      if(g.reason)summary.append(el("p","meta",g.reason));if(g.progress)summary.append(el("p","","进展："+g.progress));if(g.next)summary.append(el("p","","下一步："+g.next));
      const criteria=el("ol");for(const criterion of g.criteria||[])criteria.append(el("li","",criterion));summary.append(el("h4","","完成标准"),criteria);
      for(const proof of g.evidence||[])summary.append(el("p","meta",`标准 ${proof.criterion}：${proof.detail} · 工具 ${proof.tool_call_id} · 事件 ${proof.seq}`));
    }
    const start=button(g&&!["completed","ended"].includes(g.state)?"修改目标 / 预算":"开始新的持续 Goal",()=>edit(g));start.disabled=busy||!!pending;
    const pause=button("暂停",()=>operate("pause"));pause.disabled=busy||!!pending||!g||!["running","waiting_user"].includes(g.state);
    const resume=button("恢复",()=>operate("resume"));resume.disabled=busy||!!pending||!g||["running","pausing","completed","ended"].includes(g.state)||!r.exit;
    const end=button("结束（未完成）",()=>{if(confirm("结束持续Goal并停止当前轮？历史和文件会保留，未完成项仍记录为未完成。"))operate("end");});end.disabled=busy||!!pending||!g||["completed","ended"].includes(g.state);
    actions.replaceChildren(start,pause,resume,end);if(pending)actions.append(button("查询原操作回执",checkReceipt));
    if(editing&&base!==(g?.revision||0))actions.append(button("已核对最新目标，保留草稿并采用新版本",()=>{base=g?.revision||0;for(const field of Object.values(fields))field.oninput?.();drawStatus();note.textContent="编辑内容已保留，请检查后保存。";}));
  }
  function edit(g){
    editing=true;base=g?.revision||0;form.replaceChildren();fields={};const continuing=g&&!["completed","ended"].includes(g.state);
    let saved={};try{saved=JSON.parse(localStorage.getItem(key)||"{}");}catch{}
    if(continuing&&typeof saved.text==="string"&&Number.isInteger(saved.revision))base=saved.revision;
    for(const [name,label,multiline] of [["text","目标",true],["constraints","约束与停止条件",true],["criteria","完成标准（每行一项）",true],["max_turns","自动轮次上限（0为未设置，系统最多100）",false],["max_tokens","token上限（0为未设置）",false],["max_seconds","墙钟秒数上限（0为未设置）",false]]){
      const control=el(multiline?"textarea":"input");control.setAttribute("aria-label",label);if(multiline)control.rows=name==="criteria"?4:2;else{control.type="number";control.min="0";control.step="1";}
      control.value=saved.revision===base&&saved[name]!==undefined?saved[name]:name==="criteria"?(continuing?g.criteria.join("\n"):""):continuing?(g[name]||""):multiline?"":"0";
      fields[name]=control;const row=el("label","goal-field");row.append(el("span","",label),control);form.append(row);control.oninput=()=>{try{localStorage.setItem(key,JSON.stringify({revision:base,...Object.fromEntries(Object.entries(fields).map(([k,v])=>[k,v.value]))}));}catch{note.textContent="Goal草稿未保存，请先复制。";}};
    }
    form.append(el("p","meta","关闭网页后服务仍会运行。Goal不提升权限、不自动批准、不提交或推送。时间预算含等待与暂停；token预算会保守限制模型输入/输出，图像与未知用量可能阻塞。"),button(continuing?"保存新版本":"确认目标与预算，开始持续执行",()=>operate(continuing?"modify":"start",true)),button("收起编辑",()=>{editing=false;form.replaceChildren();drawStatus();}));drawStatus();
  }
  async function operate(action,fromForm=false){
    if(busy||pending||!valid())return;const g=r.continuous;const payload={action,revision:fromForm?base:g?.revision||0,request_id:crypto.randomUUID()};
    if(fromForm){for(const name of ["text","constraints"])payload[name]=fields[name].value;payload.criteria=fields.criteria.value.split("\n").map(x=>x.trim()).filter(Boolean);for(const name of ["max_turns","max_tokens","max_seconds"])payload[name]=Number(fields[name].value)||0;}
    if(!setPending(payload))return;busy=true;drawStatus();note.textContent="正在保存操作…";
    try{const res=await fetch(api(`/runs/${r.id}/continuous`),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload),signal:AbortSignal.timeout(15000)});if(!valid())return;if(!res.ok){await checkReceipt();return;}const data=await res.json();r.continuous=data.continuous_goal;setPending(null);if(fromForm){editing=false;form.replaceChildren();try{const saved=JSON.parse(localStorage.getItem(key)||"{}");if(saved.text===payload.text&&saved.revision===base)localStorage.removeItem(key);}catch{}}note.textContent="操作已保存";await loadRuns();}
    catch{if(valid())note.textContent="操作结果未知，已保留原请求，不会自动重发。";}
    finally{busy=false;if(valid()){drawStatus();updateBanner();}}
  }
  async function checkReceipt(){if(!pending||!valid())return;try{const res=await fetch(api(`/requests/${pending.request_id}`));if(!res.ok)throw new Error(await res.text());const receipt=await res.json();if(!valid())return;if(receipt.state==="accepted"){setPending(null);note.textContent="原操作已保存";await load();await loadRuns();}else if(receipt.state==="rejected"){setPending(null);note.textContent="未执行："+(receipt.error?.message||"")+"；编辑草稿保留，请刷新后核对版本。";drawStatus();}else note.textContent="原操作仍待确认，不会自动重复执行。";}catch(error){note.textContent="回执查询失败："+error.message;}}
  s.drawStatus=drawStatus;load();
}
