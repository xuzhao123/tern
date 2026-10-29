import { $, HOST, api, el, ui } from "./util.js";
import { backToInput } from "./composer.js";
import { show } from "./follow.js";
import { closeWorkTab, workTab } from "./workpanel.js";

const labels={pending:"待执行",in_progress:"进行中",completed:"已完成",blocked:"受阻"};
function button(label,fn){const b=el("button","ghost",label);b.type="button";b.onclick=fn;return b;}
export function renderPlan(){
  const tab=workTab(),r=ui.run,s=tab.state,panel=$("subpanel"),account=ui.account;
  if(s.owner!==r){s.node=null;s.owner=r;}
  if(s.node){if(r.plan?.revision!==s.loadedRevision&&!s.refreshing){s.refreshing=true;s.refresh?.().finally(()=>s.refreshing=false);}panel.hidden=false;if(panel.firstElementChild!==s.node)panel.replaceChildren(s.node);return;}
  const key=`tern-plan-v1:${encodeURIComponent(account)}:${encodeURIComponent(HOST)}:${r.id}`;
  s.node=el("div","plan-view");const head=el("div","sp-head"),body=el("div","sp-body"),note=el("p","meta"),list=el("div"),editor=el("textarea"),actions=el("div","activity-actions"),permission=el("select");
  editor.rows=7;editor.hidden=true;editor.setAttribute("aria-label","计划编辑草稿");permission.setAttribute("aria-label","开始实施的权限");permission.append(new Option("请求批准","ask"),new Option("工作区自动编辑","edit"));
  const valid=()=>ui.run===r&&ui.account===account&&!s.closed;
  function saveLocal(){try{localStorage.setItem(key,JSON.stringify({text:s.editText,revision:s.editRevision,pending:s.pending}));return true;}catch{note.textContent="计划草稿/回执保存失败，请复制内容后重试。";return false;}}
  try{const stored=JSON.parse(localStorage.getItem(key)||"{}");s.editText=stored.text;s.editRevision=stored.revision;s.pending=stored.pending;}catch{note.textContent="计划草稿未能恢复。";}
  head.append(el("strong","","计划"),button("刷新",load),button("关闭",()=>closeWorkTab()));
  s.node.append(head,body);body.append(note,list,editor,actions);panel.hidden=false;panel.replaceChildren(s.node);
  async function load(){note.textContent="读取计划中…";try{const res=await fetch(api(`/runs/${r.id}/plan`));if(!res.ok)throw new Error(await res.text());const data=await res.json();if(!valid())return;s.plan=data.plan;s.loadedRevision=data.plan?.revision||0;note.textContent=s.plan?`版本 ${s.plan.revision} · ${s.plan.approved===s.plan.revision?"该版本已批准":"尚未批准实施"}`:"还没有结构化计划。";draw();}catch(error){if(valid())note.textContent="无法读取计划："+error.message;}}
  function draw(){
    list.replaceChildren();const p=s.plan;
    for(const [i,step] of (p?.steps||[]).entries())list.append(el("p","",`${i+1}. ${labels[step.status]} · ${step.content}`));
    if(p?.previous?.length){const history=el("details"),summary=el("summary","",`历史版本 · ${p.previous.length}`);history.append(summary);for(const old of [...p.previous].reverse())history.append(el("pre","",`版本 ${old.revision}\n`+old.steps.map(x=>`${labels[x.status]} · ${x.content}`).join("\n")));list.append(history);}
    editor.hidden=typeof s.editText!=="string";if(!editor.hidden)editor.value=s.editText;
    const edit=button(editor.hidden?"编辑计划":"保存新版本",async()=>{
      if(editor.hidden){s.editText=(p?.steps||[{status:"pending",content:""}]).map(x=>`${x.status} | ${x.content.replaceAll("\n"," ")}`).join("\n");s.editRevision=p?.revision||0;saveLocal();draw();editor.focus();return;}
      const steps=editor.value.split("\n").filter(x=>x.trim()).map(line=>{const at=line.indexOf("|");return {status:line.slice(0,at).trim(),content:line.slice(at+1).trim()};});
      try{const res=await fetch(api(`/runs/${r.id}/plan`),{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify({revision:s.editRevision,steps})});if(!res.ok)throw new Error(await res.text());if(!valid())return;s.editText=undefined;saveLocal();await load();}catch(error){note.textContent="未保存，编辑草稿保留："+error.message;}
    });
    const generate=button("准备只读计划请求",()=>{if(!valid())return;backToInput("请只读分析这个任务，用 task_status 获取计划版本，再用 update_plan 保存待执行步骤；给出方案和风险，等待我批准后再实施，不要改文件。");$("perm").value="plan";$("perm").dispatchEvent(new Event("change"));});
    const approve=button("批准此版本并开始实施",approvePlan);approve.disabled=!p||!r.exit||!!r.source||!!s.pending||p.steps.every(x=>x.status==="completed");
    actions.replaceChildren(generate,edit,permission,approve);
    if(s.editText!==undefined&&s.editRevision!==(p?.revision||0))actions.append(button("已核对最新计划，采用新版本",()=>{s.editRevision=p?.revision||0;saveLocal();note.textContent="版本已更新，编辑内容仍保留；请保存。";}));
    if(s.pending)actions.append(button("查询实施请求回执",checkReceipt));
  }
  async function approvePlan(){if(!valid()||!s.plan||!r.exit||s.pending)return;s.pending={request_id:crypto.randomUUID(),revision:s.plan.revision,permission:permission.value};if(!saveLocal()){s.pending=null;return;}draw();try{const res=await fetch(api(`/runs/${r.id}/plan/approve`),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(s.pending),signal:AbortSignal.timeout(15000)});if(res.ok){s.pending=null;saveLocal();if(valid())show(r.id);}else await checkReceipt();}catch{if(valid())note.textContent="实施结果未知，原请求已保留，请查询回执。";}}
  async function checkReceipt(){if(!s.pending||!valid())return;try{const res=await fetch(api(`/requests/${s.pending.request_id}`));if(!res.ok)throw new Error(await res.text());const receipt=await res.json();if(!valid())return;if(receipt.state==="accepted"){s.pending=null;saveLocal();show(r.id);}else if(receipt.state==="rejected"){s.pending=null;saveLocal();draw();note.textContent="未开始实施："+(receipt.error?.message||"");}else note.textContent="实施请求仍待核对，不会自动重发。";}catch(error){note.textContent="回执查询失败："+error.message;}}
  s.refresh=load;editor.oninput=()=>{s.editText=editor.value;saveLocal();};load();
}
