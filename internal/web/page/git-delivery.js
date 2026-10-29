import { $, HOST, api, el, ui } from "./util.js";
import { closeWorkTab, openWorkTab, workTab } from "./workpanel.js";

const labels={stage:"暂存所选文件",unstage:"取消所选暂存",commit:"本地提交"};
function button(label,fn){const b=el("button","ghost",label);b.type="button";b.onclick=fn;return b;}
export function renderGitDelivery(){
  const tab=workTab(),s=tab.state,r=ui.run,account=ui.account;
  if(s.owner!==r){s.node=null;s.owner=r;}
  const panel=$("subpanel");panel.hidden=false;
  if(s.node){if(panel.firstElementChild!==s.node)panel.replaceChildren(s.node);return;}
  const root=s.node=el("div","git-delivery"),head=el("div","sp-head"),body=el("div","sp-body"),note=el("p","meta"),receipt=el("div"),list=el("div"),bar=el("div","activity-actions"),message=el("textarea"),commit=button("检查提交",()=>confirm("commit"));
  const key=`tern-git-v1:${encodeURIComponent(account)}:${encodeURIComponent(HOST)}:${r.id}`,selected=new Set();
  let data,busy=false,pending=null;
  const valid=()=>ui.run===r&&ui.account===account&&!s.closed;
  message.placeholder="提交说明";message.setAttribute("aria-label","提交说明");message.maxLength=10000;
  try{message.value=localStorage.getItem(key+":message")||"";pending=readPending();}catch{note.textContent="无法读取提交草稿和回执，请检查浏览器存储。";}
  message.oninput=()=>{try{localStorage.setItem(key+":message",message.value);}catch{note.textContent="提交说明尚未保存，关闭页面前请复制。";}};
  head.append(el("strong","","交付修改"),button("刷新",load),button("查看差异",()=>openWorkTab("diff","","改动")),button("关闭",()=>closeWorkTab()));
  for(const action of ["stage","unstage"])bar.append(button(labels[action],()=>confirm(action)));
  body.append(note,receipt,list,bar,message,commit);root.append(head,body);panel.replaceChildren(root);
  function readPending(){for(let i=0;i<localStorage.length;i++){const name=localStorage.key(i);if(name?.startsWith(key+":pending:")){const value=JSON.parse(localStorage.getItem(name));if(value?.request_id)return value;}}return null;}
  function drawReceipt(){receipt.replaceChildren();if(!pending)return;receipt.append(el("p","meta",`${labels[pending.action]||"Git操作"}的结果待核对。保留原请求，不自动重做。`),button("查询原操作",query));}
  function clearPending(){if(pending)localStorage.removeItem(key+":pending:"+pending.request_id);pending=readPending();drawReceipt();}
  async function query(){
    if(!pending)return;const original=pending;
    try{const res=await fetch(api(`/runs/${r.id}/git/operation/${original.request_id}`));if(!res.ok)throw new Error(await res.text());let result=await res.json();
      if(result.state==="not_found"){const check=await fetch(api(`/requests/${original.request_id}`));if(check.ok){const req=await check.json();if(req.state==="rejected")result={state:"rejected",reason:req.error?.message};}}
      if(!valid())return;
      if(["completed","rejected"].includes(result.state)){clearPending();await load();note.textContent=result.state==="completed"?"原操作已完成"+(result.commit?" · "+result.commit:""):"原操作未执行："+(result.reason||"");}
      else note.textContent="原操作结果仍无法确定，请核对Git状态和日志；不要再次提交相同修改。";
    }catch(error){if(valid())note.textContent="查询失败："+error.message;}
  }
  async function load(){
    if(busy||!valid())return;busy=true;
    try{const res=await fetch(api(`/runs/${r.id}/git`),{signal:AbortSignal.timeout(15000)});if(!res.ok)throw new Error(await res.text());const result=await res.json();if(!valid())return;data=result;selected.clear();list.replaceChildren();
      note.textContent=`${HOST||"当前主机"} · ${data.directory}\n分支：${data.branch.replace(/^refs\/heads\//,"")||"游离HEAD"} · 作者：${data.author||"未配置"}${data.commit_blocked?"\n"+data.commit_blocked:""}`;
      if(!data.files.length)list.append(el("p","meta","工作目录干净，没有待交付的修改。"));
      for(const file of data.files){const row=el("label","feedback-item"),pick=el("input"),name=el("span","",file.path);pick.type="checkbox";pick.disabled=!!file.notice;pick.onchange=()=>pick.checked?selected.add(file.path):selected.delete(file.path);row.append(pick,name,el("p","meta",`暂存 ${file.index.trim()||"—"} · 工作目录 ${file.worktree.trim()||"—"}${file.notice?" · "+file.notice:""}`));list.append(row);}
      commit.disabled=!!data.commit_blocked;drawReceipt();
    }catch(error){if(valid())note.textContent="读取Git状态失败："+error.message;}finally{busy=false;}
  }
  function confirm(action){
    if(!data||busy)return;if(pending){note.textContent="请先核对上一次Git操作的结果。";return;}
    const paths=action==="commit"?data.files.filter(f=>f.index!==" "&&f.index!=="?").map(f=>f.path):[...selected];
    if(!paths.length){note.textContent=action==="commit"?"没有已暂存的变化。":"请先选择文件。";return;}
    if(action==="commit"&&(!message.value.trim()||data.commit_blocked)){note.textContent=data.commit_blocked||"请填写提交说明。";return;}
    const version=data.version,text=message.value,dialog=el("dialog","resource-dialog"),status=el("p","meta"),run=button("确认"+labels[action],async()=>{
      if(!valid()||pending)return;
      const payload={action,version,paths:action==="commit"?[]:paths,message:action==="commit"?text:"",confirmed:true,request_id:crypto.randomUUID()};
      try{localStorage.setItem(key+":pending:"+payload.request_id,JSON.stringify(payload));pending=payload;}catch{status.textContent="无法保存操作回执，本次不执行。";return;}
      run.disabled=true;busy=true;drawReceipt();status.textContent="正在执行…";
      try{const res=await fetch(api(`/runs/${r.id}/git`),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload),signal:AbortSignal.timeout(60000)});if(!res.ok)throw new Error(await res.text());const result=await res.json();if(result.state!=="completed")throw new Error(result.reason||"结果待确认");clearPending();dialog.close();busy=false;if(valid()){await load();note.textContent=labels[action]+"完成"+(result.commit?" · "+result.commit:"");}}
      catch(error){status.textContent="操作结果需核对："+error.message+"。草稿与原请求已保留。";}finally{busy=false;}
    });
    dialog.setAttribute("aria-label",labels[action]+"确认");dialog.append(el("h2","",labels[action]),el("p","",`${data.directory}\n${data.branch}\n${data.author}`),el("pre","",paths.join("\n")));
    if(action==="commit")dialog.append(el("p","meta","提交包含全部已暂存文件；只生成本地提交。"),el("pre","",text));
    dialog.append(status,run,button("关闭",()=>dialog.close()));document.body.append(dialog);dialog.addEventListener("close",()=>dialog.remove());dialog.showModal();
  }
  drawReceipt();load();
}
