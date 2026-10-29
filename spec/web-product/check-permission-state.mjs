// 运行真实前端reducer；移除静态UI imports以便在Node验证纯事件状态，不模拟模型。
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const root=new URL('../../internal/web/page/',import.meta.url);
const runSource=fs.readFileSync(new URL('runs.js',root),'utf8').replace(/^import .*\n/gm,'').replace(/^export /gm,'');
const utilSource=fs.readFileSync(new URL('util.js',root),'utf8');
const choices=utilSource.slice(utilSource.indexOf('export function permChoice'),utilSource.indexOf('export const FULL_ACCESS_NOTE')).replace(/^export /gm,'');
const ctx=vm.createContext({ui:{}});
const {newRun,apply,permChoice,permValue}=vm.runInContext(runSource+'\n'+choices+'\n({newRun,apply,permChoice,permValue})',ctx);
const directory='7e3b89d8-201a-4004-9098-5a47c1c9abb1';
const events=[{kind:'start',permission:'full',sandbox:'host',bash:true},{kind:'notify',method:'turn/started',body:{turn_id:directory,sandbox:directory}},{kind:'exit',text:'exit 0'}].map((e,i)=>({...e,seq:i+1,time:'2026-10-29T01:00:00Z'}));
function replay(rows){const r=newRun('permission-fixture');for(const e of rows)apply(r,e);return r;}
let run=replay(events);assert.equal(run.sandboxMode,'host');assert.equal(run.sandbox,directory);assert.equal(permValue(run.permission,run.sandboxMode),'unrestricted');assert.equal(permChoice(permValue(run.permission,run.sandboxMode)).sandbox,'host');
// 续聊和刷新重放都必须保留host，而不是把目录UUID当作mode。
const more=[...events,{kind:'continue',permission:'full',sandbox:'host',bash:true},{kind:'notify',method:'turn/started',body:{turn_id:'second-turn',sandbox:directory}}];run=replay(more);assert.equal(run.sandboxMode,'host');assert.equal(run.sandbox,directory);assert.equal(permValue(run.permission,run.sandboxMode),'unrestricted');
run=replay([{...events[0],bash:false},{...events[1],body:{turn_id:directory,sandbox:''}},events[2]]);assert.equal(run.sandboxMode,'host');assert.equal(run.sandbox,'');
// 明确选择无人值守，以及缺少模式的老存档，仍然留在沙箱。
run=replay([{...events[0],sandbox:'sandbox'},events[1],events[2]]);assert.equal(permValue(run.permission,run.sandboxMode),'full');assert.equal(permChoice(permValue(run.permission,run.sandboxMode)).sandbox,'sandbox');
run=replay([{...events[0],sandbox:undefined},events[1]]);assert.equal(run.sandboxMode,'sandbox');assert.equal(permValue(run.permission,run.sandboxMode),'full');
console.log('PASS: full-access start/notify/continue/replay preserves host; directory ID stays separate; explicit unattended and legacy modes remain sandboxed');
