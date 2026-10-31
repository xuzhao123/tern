"""真实Goal等待/重启/时间预算、服务重启、隔离副本初始化及shell回执。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid,subprocess,os
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095
p=argparse.ArgumentParser();p.add_argument('--home',required=True);p.add_argument('--binary',required=True);p.add_argument('--resume',action='store_true');a=p.parse_args();home=Path(a.home).resolve();binary=Path(a.binary).resolve();assert home.name.startswith(('.tern-web-product-qa','tern-qa-')) and binary.parent==home
config=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method,path,body=None):
 req=urllib.request.Request(QA_BASE+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
 try:res=opener.open(req,timeout=60)
 except urllib.error.HTTPError as error:res=error
 data=res.read().decode()
 try:data=json.loads(data)
 except json.JSONDecodeError:pass
 return res.status,data
assert call('POST','/login',{'name':config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa')),'password':(config.get('TERN_QA_PASSWORD') or config['OBSERVER_TEST_PASSWORD']).strip('\"\'')})[0]==200
report={'checks':[]};out=home/'closeout-lifecycle.json'
def save():out.write_text(json.dumps(report,ensure_ascii=False,indent=2))
def row(task):
 code,runs=call('GET','/runs');return next((r for r in runs if r['id']==task),None) if code==200 else None
def wait(task,pred,seconds=180):
 end=time.monotonic()+seconds
 while time.monotonic()<end:
  r=row(task)
  if r and pred(r):return r
  time.sleep(.4)
 raise AssertionError('未达到预期，先核对原任务 '+task)
fixture=json.loads((home/'workpanel-results.json').read_text());parent=fixture['task'];repo=Path(fixture['path']);code,ops=call('GET',f'/runs/{parent}/operations');assert code==200
if not a.resume:
 code,op=call('POST',f'/runs/{parent}/operations',{'name':'qa-dev','version':ops['version'],'confirmed':True,'request_id':str(uuid.uuid4())});assert code==200,op
 report['service_id']=op['id'];save()
 code,created=call('POST','/runs',{'provider':'deepseek','permission':'edit','query':'仅回复 LIFECYCLE_READY，不调用工具、不写长期记忆。','request_id':str(uuid.uuid4())});assert code==200,created
 task=created['id'];report['task']=task;save();wait(task,lambda r:r['done'])
 body={'action':'start','revision':0,'text':'仅调用ask_user问“生命周期验收是否继续”，选项继续/等待。等真实用户回答，不代答、不写文件、不联网、不写记忆。','constraints':'必须等待真实用户；不要自动回答。','criteria':['获得用户真实回答'],'max_turns':3,'max_tokens':0,'max_seconds':40,'request_id':str(uuid.uuid4())}
 code,result=call('POST',f'/runs/{task}/continuous',body);assert code==200,result;g=result['continuous_goal'];report['goal']=g['id'];save()
 r=wait(task,lambda r:len(r.get('waiting_requests',[]))==1);request=r['waiting_requests'][0]
 assert call('POST',f'/runs/{task}/continuous',{'action':'pause','revision':0,'request_id':str(uuid.uuid4())})[0]==409
 env={**os.environ,'TERN_HOME':str(home)}
 subprocess.run([str(binary),'app-server','stop'],cwd=home,env=env,check=True,capture_output=True)
 subprocess.run([str(binary),'app-server','start'],cwd=home,env=env,check=True,capture_output=True)
 r=wait(task,lambda r:bool(r.get('waiting_requests')),20);assert r['waiting_requests'][0]['id']==request['id'] and r['continuous_goal']['id']==g['id'],r
 events=lambda:[json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
 assert len([e for e in events() if e.get('goal_id')==g['id'] and e['kind']=='continue'])==1
 r=wait(task,lambda r:r['done'] and r.get('continuous_goal',{}).get('state')=='exhausted',60);assert '墙钟' in r['continuous_goal']['reason'];assert call('POST',f'/runs/{task}/answer',{'call_id':request['call_id'],'request_id':request['id'],'answer':'继续'})[0]==409
else:
 report=json.loads(out.read_text());task=report['task'];op={'id':report['service_id']};g={'id':report['goal']}
 r=row(task);assert r['done'] and r['continuous_goal']['state']=='exhausted' and '墙钟' in r['continuous_goal']['reason']
 history=[json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
 assert len([e for e in history if e.get('goal_id')==g['id'] and e['kind']=='continue'])==1
code,service=call('GET',f'/runs/{parent}/operations/{op["id"]}');assert code==200 and service['operation']['state'] in ('stopped','unavailable','failed'),service
code,stopped=call('POST',f'/runs/{parent}/operations/{op["id"]}/stop',{});assert code==409 and '原进程已经退出' in stopped,stopped
report['checks']+=['stale Goal revision rejected','real waiting request and Goal identity survive app-server restart','adoption creates no duplicate Goal round','wallclock budget interrupts waiting and persists exhaustion','late user answer rejected','owned service stops across backend restart without automatic rerun'];save()
rid=str(uuid.uuid4());shell={'command':'printf SHELL_RECEIPT_OK','request_id':rid};code,result=call('POST',f'/runs/{parent}/shell',shell);assert code==200 and 'SHELL_RECEIPT_OK' in result['output'],result;assert call('POST',f'/runs/{parent}/shell',shell)[1]==result;assert call('GET','/requests/'+rid)[1]['state']=='accepted'
history=[json.loads(x) for x in (home/'.data/runs'/f'{parent}.jsonl').read_text().splitlines()];assert len([e for e in history if e.get('request_id')==rid and e['kind']=='shell'])==1
project=next(x for x in call('GET','/workspaces')[1] if x['path']==str(repo))
code,copy=call('POST','/runs',{'workspace':project['name'],'copy':True,'provider':'deepseek','permission':'edit','query':'仅回复 COPY_READY，不调用工具、不修改文件、不写记忆。','request_id':str(uuid.uuid4())});assert code==200,copy
report['copy_task']=copy['id'];save();r=wait(copy['id'],lambda r:r['done']);code,ops=call('GET',f'/runs/{copy["id"]}/operations');assert code==200 and ops['copy'],ops;copydir=Path(ops['directory']);assert copydir!=repo and copydir.is_relative_to(home)
code,init=call('POST',f'/runs/{copy["id"]}/operations',{'name':'qa-init','version':ops['version'],'confirmed':True,'request_id':str(uuid.uuid4())});assert code==200,init
end=time.monotonic()+30
while time.monotonic()<end:
 code,result=call('GET',f'/runs/{copy["id"]}/operations/{init["id"]}')
 if result['operation']['state']=='failed':break
 time.sleep(.3)
assert result['operation']['exit_code']==9,result;assert (copydir/'qa-init-partial.txt').read_text()=='COPY_INIT_PARTIAL';assert not (repo/'qa-init-partial.txt').exists()
report['checks']+=['real user shell exact request executed once and receipt accepted','copy initializer nonzero exit preserved with partial file only in copy'];report['verified']=True;save();print(json.dumps(report,ensure_ascii=False,indent=2))
