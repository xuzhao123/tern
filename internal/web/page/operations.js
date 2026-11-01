import { $, HOST, api, el, ui } from "./util.js";
import { backToInput } from "./composer.js";
import { workTab } from "./workpanel.js";

const states={starting:"启动中",running:"运行中",stopping:"停止中",stopped:"已停止",exited:"已退出",failed:"失败",unavailable:"状态待核对"};
function button(label,fn){const b=el("button","ghost",label);b.type="button";b.onclick=fn;return b;}
export function renderOperations(){
  const tab=workTab(),r=ui.run,s=tab.state,panel=$("subpanel"),account=ui.account;
  if(s.owner!==r){clearTimeout(s.timer);s.node=null;s.owner=r;}
  if(s.node){if(!s.timer)s.resume?.();panel.hidden=false;if(panel.firstElementChild!==s.node)panel.replaceChildren(s.node);return;}
  const root=s.node=el("div","operations-view"),head=el("div","sp-head"),body=el("div","sp-body"),note=el("p","meta"),actions=el("div"),instances=el("div"),logs=el("pre","job-out");
  const prefix=`tern-operation-v1:${encodeURIComponent(account)}:${encodeURIComponent(HOST)}:${r.id}:`;
  let data,selected=null,reading=false,rendered="";
  const valid=()=>ui.run===r&&ui.account===account&&!s.closed;
  head.append(el("strong","","项目操作"),button("刷新",load));body.append(note,actions,instances,logs);root.append(head,body);panel.hidden=false;panel.replaceChildren(root);
  async function readLog(op){try{const res=await fetch(api(`/runs/${op.thread_id}/operations/${op.id}`));if(!res.ok)throw new Error(await res.text());const result=await res.json();if(!valid()||selected?.id!==op.id)return;logs.textContent=result.output||"（尚无输出）";}catch(error){if(valid())logs.textContent="日志读取失败："+error.message;}}
  function draw(){
    actions.replaceChildren();instances.replaceChildren();note.textContent=`${HOST||"当前执行主机"} · ${data.directory} · ${data.supported?"Linux隔离沙箱":"此平台尚不支持持久项目操作"}`;
    if(!data.actions.length)actions.append(el("p","meta","尚未配置项目动作。在项目 .agents/actions.json 定义启动、测试、构建命令；打开项目不会自动运行。"));
    for(const action of data.actions){const row=el("div","feedback-item"),run=button("查看命令并运行",()=>confirmAction(action));run.disabled=!data.supported||!r.bash||(action.platforms?.length&&!action.platforms.includes("linux"));row.append(el("strong","",action.name),el("p","meta",`${action.background?"长期服务，上限24小时":"单次命令，上限5分钟"}${action.ports?.length?" · 沙箱端口 "+action.ports.join("、"):""}`),el("pre","",action.command),run);actions.append(row);}
    for(const op of data.operations){const row=el("div","feedback-item"),bar=el("div","activity-actions");row.append(el("strong","",op.name+" · "+(states[op.state]||op.state)),el("p","meta",`${op.directory} · ${new Date(op.started).toLocaleString()}${op.exit_code!==undefined?` · 退出码 ${op.exit_code}`:""}`));if(op.reason||op.log_error)row.append(el("p","err",[op.reason,op.log_error].filter(Boolean).join("；")));
      bar.append(button("查看日志",()=>{selected=op;readLog(op);}),button("将已读日志加入草稿",()=>{if(selected?.id!==op.id||!logs.textContent)return;backToInput(`[项目操作 ${op.id} · ${op.name} · ${states[op.state]||op.state}]\n目录：${op.directory}\n命令：${op.command}\n已读取的日志尾部（可能截断，不代表整个任务完成）：\n${logs.textContent.slice(-12000)}`);}));
      if(["starting","running","stopping","unavailable"].includes(op.state)){const stop=button("停止此实例",async()=>{stop.disabled=true;try{const res=await fetch(api(`/runs/${op.thread_id}/operations/${op.id}/stop`),{method:"POST"});if(!res.ok)throw new Error(await res.text());await load();}catch(error){if(valid())note.textContent="停止结果："+error.message;}});stop.disabled=op.state==="stopping";bar.append(stop);}
      if(ui.previewEnabled&&op.state==="running")for(const port of op.ports||[])bar.append(button(`预览端口 ${port}`,()=>document.dispatchEvent(new CustomEvent("tern:preview-open",{detail:{service_id:op.id,port,url:`http://127.0.0.1:${port}/`}}))));
      row.append(bar);instances.append(row);
    }
    try{for(let i=0;i<localStorage.length;i++){const name=localStorage.key(i);if(!name?.startsWith(prefix))continue;const pending=JSON.parse(localStorage.getItem(name));if(!pending?.request_id)continue;const op=data.operations.find(x=>x.request_id===pending.request_id);if(op){localStorage.removeItem(name);continue;}const row=el("p","meta","有一个项目操作结果待确认。 ");row.append(button("查询原请求",async()=>{try{const res=await fetch(api(`/requests/${pending.request_id}`));if(!res.ok)throw new Error(await res.text());const receipt=await res.json();if(receipt.state==="accepted"||receipt.state==="rejected"){localStorage.removeItem(name);await load();note.textContent=receipt.state==="rejected"?"未执行："+(receipt.error?.message||""):"原请求已接受，请查看实例";}else note.textContent="原请求仍待确认；请核对实例和进程，不要重复启动。";}catch(error){note.textContent=error.message;}}));instances.append(row);}}catch{note.textContent="操作回执读取失败，请检查浏览器存储。";}
  }
  function confirmAction(action){
    const version=data.version,dialog=el("dialog","resource-dialog"),title=el("h2","","运行项目操作 · "+action.name),message=el("p","meta"),run=button("确认命令与目录，运行",async()=>{
      if(!valid())return;const payload={name:action.name,version,confirmed:true,request_id:crypto.randomUUID()};try{localStorage.setItem(prefix+payload.request_id,JSON.stringify(payload));}catch{message.textContent="无法保存启动回执，本次不执行。";return;}
      run.disabled=true;message.textContent="启动中…";
      try{const res=await fetch(api(`/runs/${r.id}/operations`),{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload),signal:AbortSignal.timeout(15000)});if(!res.ok)throw new Error(await res.text());localStorage.removeItem(prefix+payload.request_id);dialog.close();if(valid())await load();}
      catch(error){message.textContent="启动结果需核对："+error.message+"。原请求已保留，不自动重发。";if(valid())await load();}
    });title.id="operation-confirm-title";dialog.setAttribute("aria-labelledby",title.id);dialog.append(title,el("p","",`${HOST||"当前主机"} · ${data.directory} · 隔离沙箱`),el("pre","",action.command),el("p","meta",`端口：${action.ports?.join("、")||"不导出"}。关闭标签不停止服务；普通agent轮次结束不影响此实例。${action.initialize?"初始化失败可能已有部分副作用，重新运行前请核对。":""}`),message,run,button("关闭",()=>dialog.close()));document.body.append(dialog);dialog.addEventListener("close",()=>dialog.remove());dialog.showModal();
  }
  async function load(){if(reading||!valid())return;reading=true;try{const res=await fetch(api(`/runs/${r.id}/operations`),{signal:AbortSignal.timeout(10000)});if(!res.ok)throw new Error(await res.text());const result=await res.json();if(!valid())return;data=result;const version=JSON.stringify(data);if(version!==rendered){rendered=version;draw();}if(selected)await readLog(selected);}catch(error){if(valid())note.textContent="读取项目操作失败："+error.message;}finally{reading=false;}}
  const tick=async()=>{clearTimeout(s.timer);s.timer=null;if(!valid()||workTab()!==tab)return;await load();s.timer=setTimeout(tick,2500);};s.resume=tick;tick();
}
