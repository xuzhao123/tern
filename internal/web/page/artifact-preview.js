import { $, HOST, el, ui } from "./util.js";
import { md } from "./md.js";
import { backToInput } from "./composer.js";

const imageTypes={png:"image/png",jpg:"image/jpeg",jpeg:"image/jpeg",gif:"image/gif",webp:"image/webp"};
export const artifactType=(path)=>{const ext=path.split(".").pop().toLowerCase();return imageTypes[ext]?"image":["md","markdown"].includes(ext)?"markdown":["html","htm"].includes(ext)?"html":ext==="pdf"?"pdf":"";};
export async function contentVersion(bytes){return [...new Uint8Array(await crypto.subtle.digest("SHA-256",bytes))].map(x=>x.toString(16).padStart(2,"0")).join("");}
export async function readArtifactBytes(response){
  const reader=response.body.getReader(),chunks=[];let size=0;
  try{for(;;){const {value,done}=await reader.read();if(done)break;size+=value.length;if(size>10*1024*1024){await reader.cancel();throw new Error("文件超过10MiB，请下载查看");}chunks.push(value);}}finally{reader.releaseLock();}
  const bytes=new Uint8Array(size);let at=0;for(const chunk of chunks){bytes.set(chunk,at);at+=chunk.length;}return bytes;
}
function button(label,fn){const b=el("button","ghost",label);b.type="button";b.onclick=fn;return b;}
export function disposeArtifact(s){if(!s.artifact)return;s.artifact.closed=true;window.removeEventListener("storage",s.artifact.onStorage);s.artifact.pdfTask?.destroy();if(s.artifact.url)URL.revokeObjectURL(s.artifact.url);if(s.artifact.downloadURL)URL.revokeObjectURL(s.artifact.downloadURL);s.artifact=null;}

// 资源内容始终是数据。HTML 不开放脚本/同源权限，PDF 仅绘制画布，不执行文档中的 JavaScript。
export function artifactView(r,data,s,readVersion){
  const type=artifactType(data.path), textual=["markdown","html"].includes(type);if(!type)return null;
  if(["markdown","html"].includes(type)&&typeof data.text!=="string")return el("p","meta","文件编码不支持，无法渲染；请下载后查看原文件。");
  const account=ui.account,id=[account,r.id,data.kind,data.path,data.version].join("\0");
  if(s.artifact?.id===id&&s.artifact.run===r)return s.artifact.root;
  disposeArtifact(s);
  const a=s.artifact={id,run:r,root:el("section","artifact-view"),closed:false};
  const key=`tern-artifact-v1:${encodeURIComponent(account)}:${encodeURIComponent(HOST)}:${r.id}:${data.kind}:${encodeURIComponent(data.path)}:`;
  const toolbar=el("div","fp-tools"),status=el("p","meta"),scroller=el("div","artifact-scroll"),stage=el("div","artifact-stage"),notes=el("section","artifact-notes");scroller.append(stage);
  const live=()=>!a.closed&&ui.account===account&&ui.run===r;
  a.root.append(toolbar,status,scroller,notes);
  a.downloadURL=URL.createObjectURL(new Blob([data.bytes],{type:"application/octet-stream"}));
  const download=el("a","","下载读取版本");download.href=a.downloadURL;download.download=data.path.split("/").pop();toolbar.append(download);
  toolbar.append(el("span","meta",`${data.kind==="project"?"项目文件":"任务产物"} · ${HOST||"当前主机"} · ${data.version.slice(0,12)}`));
  let rendered=null,region=null,paintSeq=0,editor=null,joining=false;
  const view=()=>{s.artifactPage=Math.max(1,Math.min(s.artifactPage||1,a.pdf?.numPages||Number.MAX_SAFE_INTEGER));s.artifactZoom=Math.max(.25,Math.min(3,s.artifactZoom||1));};view();
  function store(record){try{localStorage.setItem(key+record.id,JSON.stringify(record));return true;}catch{status.textContent="批注保存失败，当前输入仍保留，请先复制。";return false;}}
  function read(){const all=[];for(let i=0;i<localStorage.length;i++){const name=localStorage.key(i);if(!name?.startsWith(key))continue;const value=JSON.parse(localStorage.getItem(name)||"null");if(value?.id&&(["comment","draft"].includes(value.type)&&typeof value.text==="string"&&value.ref&&typeof value.ref.version==="string"||value.type==="remove"))all.push(value);}const hidden=new Set(all.flatMap(x=>[x.replaces,x.target,x.draft].filter(Boolean)));return all.filter(x=>!hidden.has(x.id)&&x.type!=="remove");}
  function ref(){return {kind:data.kind,path:data.path,version:data.version,at:new Date().toISOString(),page:type==="pdf"?s.artifactPage:null,region:region?{...region}:null,line:s.artifactLine||1,excerpt:(textual?data.text:undefined)?.split("\n").slice((s.artifactLine||1)-1,(s.artifactLine||1)+2).join("\n").slice(0,2000)||""};}
  const label=(ref)=>ref.region?`${ref.page?`第${ref.page}页 · `:""}区域 ${JSON.stringify(ref.region)}`:ref.page?`第${ref.page}页`:data.text?`第${ref.line}行`:"整个文件";
  function begin(value){
    if(editor?.text.trim()&&!store(editor))return;
    editor={id:crypto.randomUUID(),type:"draft",text:"",ref:ref(),...value};drawNotes();
  }
  async function join(){
    if(joining||!live())return;let records;
    try{records=read().filter(x=>x.type==="comment");}catch{status.textContent="无法读取完整批注，未加入输入。";return;}
    const before=JSON.stringify(records);joining=true;status.textContent="正在核对文件版本…";
    let version="",error="";try{version=await readVersion();}catch(e){error=e.message;}
    joining=false;if(!live())return;
    try{if(JSON.stringify(read().filter(x=>x.type==="comment"))!==before){status.textContent="核对期间批注已改变，请重新加入输入。";return;}}catch{status.textContent="批注读取失败，未加入输入。";return;}
    const text=records.filter(x=>!$("query").value.includes(`【产物批注 ${x.id}】`)).map(x=>`【产物批注 ${x.id}】\n主机：${HOST||"当前主机"} · 任务：${r.id}\n来源：${x.ref.kind} · 路径：${JSON.stringify(x.ref.path)}\n版本：${x.ref.version} · 捕获时间：${x.ref.at}\n定位：${label(x.ref)}\n核对：${error?"失败："+error:version===x.ref.version?"文件版本一致":"文件已改变，原定位可能过期，请重新核对"}\n原片段：${x.ref.excerpt||"（区域/文件引用）"}\n意见：${x.text}`).join("\n\n");
    if(text)backToInput(text);status.textContent=text?"批注已加入输入草稿，尚未发送。":"批注已在草稿中，没有重复添加。";
  }
  function drawNotes(){
    notes.replaceChildren();let records=[];try{records=read();}catch{status.textContent="无法读取已保存批注。";}
    notes.append(button("添加批注",()=>begin()),button("核对并加入输入",join));
    if(textual){const line=el("input");line.type="number";line.min="1";line.max=String(data.text.split("\n").length);line.value=String(s.artifactLine||1);line.setAttribute("aria-label","批注源码行号");line.onchange=()=>{s.artifactLine=Math.max(1,Math.min(Number(line.max),Number(line.value)||1));};notes.append(el("span","meta","源码行 "),line);}
    if(editor){
      const form=el("div","artifact-editor"),input=el("textarea");input.rows=3;input.maxLength=8000;input.value=editor.text;input.setAttribute("aria-label","产物批注内容");
      input.oninput=()=>{editor.text=input.value;store(editor);};
      form.append(el("p","meta",label(editor.ref)),input,button("保存批注",()=>{if(!editor.text.trim())return;const record={...editor,id:crypto.randomUUID(),type:"comment",draft:editor.id};if(store(record)){editor=null;drawNotes();}}),button("稍后继续",()=>{if(store(editor)){editor=null;drawNotes();}}));notes.append(form);
    }
    for(const record of records){if(record.id===editor?.id)continue;const row=el("div","feedback-item");row.append(el("strong","",label(record.ref)),el("p","meta",record.ref.version===data.version?"读取版本一致（加入前重新核对）":"原文件已改变，保留原定位"),el("p","",record.text));
      row.append(button(record.type==="draft"?"继续编辑":"编辑",()=>begin({text:record.text,ref:record.ref,replaces:record.id})),button("删除",()=>{if(store({id:crypto.randomUUID(),type:"remove",target:record.id})){drawNotes();}}));notes.append(row);
    }
  }
  async function paint(){
    const sequence=++paintSeq;a.renderTask?.cancel();view();stage.replaceChildren();region=null;rendered=null;
    if(s.artifactMode==="source"&&data.text!==undefined){stage.append(el("pre","fp-text",data.text));return;}
    if(type==="markdown"||type==="html"){
      const frame=el("iframe","artifact-document");frame.title=data.path;frame.sandbox="";
      const template=document.createElement("template");template.innerHTML=type==="markdown"?md(data.text||"",false).innerHTML:data.text||"";
      for(const node of template.content.querySelectorAll("script,meta[http-equiv],base,iframe,object,embed"))node.remove();
      const content=template.innerHTML;
      frame.srcdoc=`<!doctype html><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'"><meta charset="utf-8"><style>body{font:15px/1.6 system-ui;padding:18px;overflow-wrap:anywhere;color:#252525;background:white}pre{white-space:pre-wrap}img{max-width:100%}</style>${content}`;
      stage.append(frame);status.textContent="静态隔离预览：脚本、外部资源与表单提交关闭。批注使用源码行定位。";return;
    }
    if(type==="image"){
      a.url ||= URL.createObjectURL(new Blob([data.bytes],{type:imageTypes[data.path.split(".").pop().toLowerCase()]}));
      const img=el("img");img.alt=data.path;img.src=a.url;img.style.width=`${s.artifactZoom*100}%`;img.style.maxWidth="none";img.draggable=false;img.onload=()=>{if(!a.closed&&sequence===paintSeq){rendered=img;status.textContent="拖动选择区域后添加批注；区域定位基于读取版本。";}};img.onerror=()=>{status.textContent="图像无法解码，请下载检查原文件。";};stage.append(img);return;
    }
    if(!a.pdf){stage.append(el("p","meta",a.failed?"无法在此预览，请下载原文件。":"正在读取 PDF…"));return;}
    a.previous.disabled=s.artifactPage<=1;a.next.disabled=s.artifactPage>=a.pdf.numPages;a.zoom.disabled=false;
    try{
      const page=await a.pdf.getPage(s.artifactPage),viewport=page.getViewport({scale:s.artifactZoom});if(a.closed||sequence!==paintSeq)return;
      if(viewport.width*viewport.height>16000000)throw new Error("页面尺寸过大，请减小缩放或下载查看");
      const canvas=el("canvas");canvas.setAttribute("role","img");canvas.setAttribute("aria-label",`PDF 第 ${s.artifactPage} 页`);canvas.width=Math.ceil(viewport.width);canvas.height=Math.ceil(viewport.height);stage.append(canvas);a.renderTask=page.render({canvasContext:canvas.getContext("2d"),viewport});await a.renderTask.promise;
      if(a.closed||sequence!==paintSeq)return;rendered=canvas;status.textContent=`第 ${s.artifactPage} / ${a.pdf.numPages} 页 · 拖动选择页内区域后添加批注`;
    }catch(error){if(!a.closed&&sequence===paintSeq)status.textContent="PDF 渲染失败："+error.message+"。可下载原文件查看。";}
  }
  if(textual){toolbar.append(button("预览",()=>{s.artifactMode="preview";paint();}),button("源码",()=>{s.artifactMode="source";paint();}));}
  if(type==="image"||type==="pdf"){
    const zoom=el("select");zoom.setAttribute("aria-label","预览缩放");for(const n of [.25,.5,.75,1,1.5,2,3])zoom.append(new Option(`${n*100}%`,n));zoom.value=s.artifactZoom;zoom.onchange=()=>{s.artifactZoom=Number(zoom.value);paint();};toolbar.append(zoom);
    if(type==="pdf"){a.zoom=zoom;zoom.disabled=true;a.previous=button("上一页",()=>{s.artifactPage=Math.max(1,s.artifactPage-1);paint();});a.next=button("下一页",()=>{s.artifactPage=Math.min(a.pdf?.numPages||1,s.artifactPage+1);paint();});a.previous.disabled=a.next.disabled=true;toolbar.append(a.previous,a.next);}
    stage.onpointerdown=(event)=>{
      if(!rendered||event.button!==0||event.target!==rendered)return;event.preventDefault();const box=rendered.getBoundingClientRect(),start={x:(event.clientX-box.left)/box.width,y:(event.clientY-box.top)/box.height},overlay=el("div","artifact-region");stage.querySelector(".artifact-region")?.remove();stage.append(overlay);stage.setPointerCapture(event.pointerId);
      stage.onpointermove=(e)=>{const end={x:Math.max(0,Math.min(1,(e.clientX-box.left)/box.width)),y:Math.max(0,Math.min(1,(e.clientY-box.top)/box.height))};region={x:+Math.min(start.x,end.x).toFixed(4),y:+Math.min(start.y,end.y).toFixed(4),width:+Math.abs(start.x-end.x).toFixed(4),height:+Math.abs(start.y-end.y).toFixed(4)};Object.assign(overlay.style,{left:region.x*box.width+"px",top:region.y*box.height+"px",width:region.width*box.width+"px",height:region.height*box.height+"px"});};
      stage.onpointerup=stage.onpointercancel=()=>{stage.onpointermove=null;if(region?.width&&region?.height)begin();};
    };
  }
  if(type==="pdf"){
    import("./vendor/pdfjs-5.4.54/pdf.mjs").then(async pdfjs=>{
      if(a.closed)return;pdfjs.GlobalWorkerOptions.workerSrc=new URL("./vendor/pdfjs-5.4.54/pdf.worker.mjs",import.meta.url).href;
      a.pdfTask=pdfjs.getDocument({data:data.bytes.slice(),isEvalSupported:false,useWasm:false,useSystemFonts:true,stopAtErrors:true});
      a.pdfTask.onPassword=()=>{a.encrypted=true;a.failed=true;stage.replaceChildren(el("p","meta","无法在此预览，请下载原文件。"));status.textContent="PDF 已加密，请下载后使用本地阅读器打开。";a.pdfTask.destroy();};
      a.pdf=await a.pdfTask.promise;if(a.closed){a.pdf.destroy();return;}paint();
    }).catch(error=>{if(!a.closed){a.failed=true;stage.replaceChildren(el("p","meta","无法在此预览，请下载原文件。"));status.textContent=a.encrypted?"PDF 已加密，请下载后使用本地阅读器打开。":"PDF 无法读取（可能损坏或不支持）："+error.message+"。可下载原文件。";}});
  }
  const storage=(event)=>{if(a.closed){window.removeEventListener("storage",storage);return;}if(event.key?.startsWith(key)&&!notes.contains(document.activeElement))drawNotes();};a.onStorage=storage;window.addEventListener("storage",storage);
  drawNotes();paint();return a.root;
}
