"""真实模型验证关闭Bash的旧任务显式启用及当前命令执行位置，不修改日常数据。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid,subprocess,os
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--home',required=True);p.add_argument('--binary',required=True);a=p.parse_args();home=Path(a.home).resolve();binary=Path(a.binary).resolve();assert home.name.startswith('.tern-web-product-qa') and binary.parent==home
config=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method,path,body=None):
 req=urllib.request.Request('http://127.0.0.1:8095'+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
 try:res=opener.open(req,timeout=60)
 except urllib.error.HTTPError as error:res=error
 data=res.read().decode()
 try:data=json.loads(data)
 except json.JSONDecodeError:pass
 return res.status,data
assert call('POST','/login',{'name':config.get('OBSERVER_TEST_USER','qa'),'password':config['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0]==200
code,settings=call('GET','/settings');assert code==200;original=settings.get('user') or {}
report={'checks':[]};out=home/'shell-access-results.json'
def save():out.write_text(json.dumps(report,ensure_ascii=False,indent=2))
def get(task):
 code,runs=call('GET','/runs');return next((r for r in runs if r['id']==task),None) if code==200 else None
def wait(task,pred):
 end=time.monotonic()+180
 while time.monotonic()<end:
  r=get(task)
  if r and pred(r):return r
  time.sleep(.3)
 raise AssertionError('先查看原任务 '+task)
def events(task):return [json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
def create(permission='full',sandbox='host'):
 code,row=call('POST','/runs',{'provider':'deepseek','workspace':'','permission':permission,'sandbox':sandbox,'query':'只回复 SHELL_ACCESS_READY；不调用工具、不写文件、不写长期记忆。','request_id':str(uuid.uuid4())});assert code==200,row;wait(row['id'],lambda r:r['done']);return row['id']
def shell(task,**kw):return call('POST',f'/runs/{task}/shell',{'command':'pwd','request_id':str(uuid.uuid4()),**kw})
try:
 assert call('PUT','/settings',{**original,'bash':'off'})[0]==200
 task=create();report['task']=task;save();history=events(task);assert not [e for e in history if e['kind']=='start'][0].get('bash')
 names=[t['function']['name'] for e in history if e['kind']=='request' for t in e.get('body',{}).get('tools',[])];assert 'bash' not in names
 assert shell(task,permission='full',sandbox='host')[0]==409
 assert shell(task,permission='unexpected',sandbox='host')[0]==400
 assert shell(task,permission='full',sandbox='unexpected')[0]==400
 count=len(history);assert call('POST',f'/runs/{task}/enable-bash')[0]==200;assert call('POST',f'/runs/{task}/enable-bash')[0]==200;assert len(events(task))==count
 assert json.loads((home/'.data/runs'/f'{task}.meta.json').read_text())['bash_enabled']
 rid=str(uuid.uuid4());payload={'command':'pwd','permission':'full','sandbox':'host','request_id':rid};code,host=call('POST',f'/runs/{task}/shell',payload);assert code==200 and host['exit_code']==0,host;assert host['output'].strip().startswith(str(home/'.data/sandbox'))
 assert call('POST',f'/runs/{task}/shell',payload)==(code,host);assert len([e for e in events(task) if e.get('request_id')==rid])==1
 code,sandbox=shell(task,permission='full',sandbox='sandbox');assert code==200 and sandbox['exit_code']==0,sandbox;assert sandbox['output'].strip() in ('/workspace','/scratch'),sandbox
 code,legacy=shell(task);assert code==200 and legacy['sandbox']=='host' and legacy['output'].strip()==host['output'].strip(),legacy
 report['checks']+=['off full+host task has no model bash tool','unenabled task cannot bypass by sending host','invalid permission/location rejected','explicit task enable is idempotent and starts no model round','host command works before any bash-enabled model round','shell request dedup executes once','explicit sandbox request stays isolated','legacy request inherits previous model round host mode'];save()
 code,row=call('POST',f'/runs/{task}/messages',{'query':'请实际调用bash运行pwd，汇报输出。不要写文件、访问网络或写长期记忆。','permission':'full','sandbox':'host','request_id':str(uuid.uuid4())});assert code==200,row;wait(task,lambda r:r['done'])
 history=events(task);starts=[e for e in history if e['kind']=='continue'];assert starts[-1]['bash'] and starts[-1]['sandbox']=='host'
 calls=[e['body']['item']['data'] for e in history if e.get('method')=='item/completed' and e.get('body',{}).get('item',{}).get('type')=='toolCall'];bash=next(x for x in calls if x.get('tool')=='bash');assert bash['observation']['status']=='ok' and host['output'].strip() in json.dumps(bash['observation']),bash
 report['checks']+=['real model gets bash on next round despite old disabled snapshot','real model executes host pwd successfully'];save()
 ui_task=create('edit','sandbox');report['ui_task']=ui_task;save()
 code,row=call('POST',f'/runs/{task}/messages',{'query':'只用ask_user问“命令验收是否结束”，选项结束/等待。等待真实回答，不代答，不调用其他工具、不写记忆。','request_id':str(uuid.uuid4())});assert code==200,row
 r=wait(task,lambda r:bool(r.get('waiting_requests')));req=r['waiting_requests'][0];assert call('POST',f'/runs/{task}/enable-bash')[0]==409;assert shell(task)[0]==409
 assert call('POST',f'/runs/{task}/answer',{'call_id':req['call_id'],'request_id':req['id'],'answer':'结束'})[0]==204;wait(task,lambda r:r['done'])
 env={**os.environ,'TERN_HOME':str(home)}
 subprocess.run([str(binary),'app-server','stop'],env=env,cwd=home,check=True,capture_output=True);subprocess.run([str(binary),'app-server','start'],env=env,cwd=home,check=True,capture_output=True)
 wait(task,lambda r:r['done'] and r.get('bash_enabled'));assert not get(ui_task).get('bash_enabled')
 report['checks']+=['active round rejects enable and manual shell without replacing live tools','task enable survives backend restart while another disabled task stays disabled'];report['verified']=True;save();print(json.dumps(report,ensure_ascii=False,indent=2))
finally:
 assert call('PUT','/settings',original)[0]==200,'QA个人设置恢复失败，请先处理'
