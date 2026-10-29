"""通过真实父子模型运行验证直接补充、单独停止、身份与持久回执。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--home',required=True);p.add_argument('--verify-existing',action='store_true');a=p.parse_args();home=Path(a.home).resolve();assert home.name.startswith('.tern-web-product-qa')
config=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method,path,body=None):
 req=urllib.request.Request('http://127.0.0.1:8095'+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
 try:res=opener.open(req,timeout=30)
 except urllib.error.HTTPError as error:res=error
 data=res.read().decode()
 try:data=json.loads(data)
 except json.JSONDecodeError:pass
 return res.status,data
assert call('POST','/login',{'name':config.get('OBSERVER_TEST_USER','qa'),'password':config['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0]==200
if a.verify_existing:
 report=json.loads((home/'subcontrol-results.json').read_text());task=report['task']
 history=[json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
 starts=[x['body'] for x in history if x.get('method')=='subagent/started'];alpha=next(x for x in starts if 'ALPHA' in x['task'])
 messages=[x['body']['message']['params']['item']['data'] for x in history if x.get('method')=='subagent/message' and x['body'].get('task_id')==alpha['task_id'] and x['body'].get('message',{}).get('method')=='item/completed' and x['body']['message'].get('params',{}).get('item',{}).get('type')=='userMessage']
 refs=[x for x in messages if x.get('client_id')];assert len(refs)==1,refs;rid=refs[0]['client_id']
 code,status=call('POST',f'/runs/{task}/subagent/control',{'task_id':alpha['task_id'],'call_id':alpha['call_id'],'action':'status','request_id':rid});assert code==200 and status['state']=='consumed',(code,status)
 assert len(starts)==2 and len([x for x in history if x['kind']=='answer'])==1
 assert any(x.get('method')=='subagent/completed' and x['body'].get('state')=='done' and 'DIRECT_CHILD_731' in x['body'].get('answer','') for x in history)
 assert history[-1]['kind']=='exit' and history[-1]['text']=='exit 0'
 report.update({'verified':True,'request_id':rid,'checks':['initial API identity/dedup/stop/sibling assertions passed before incorrect HTTP 200 assertion (actual successful answer is 204)','existing real run completed without re-dispatch','one consumed child input and one user answer','checkpoint consumption survives child exit','target stopped while sibling returned DIRECT_CHILD_731']})
 (home/'subcontrol-results.json').write_text(json.dumps(report,indent=2));print(json.dumps(report,indent=2));raise SystemExit
code,result=call('POST','/runs',{'provider':'deepseek','workspace':'','request_id':str(uuid.uuid4()),'query':'这是子任务直控验收。请用 spawn_agent 启动两个 background=true 子任务，再 agent_wait 等结果。ALPHA：用 ask_user 问“ALPHA验收是否继续”，选项继续/取消；用户回答后输出 ALPHA_DONE 并遵循新的补充。BETA：用 ask_user 问“BETA验收是否继续”，选项继续/取消；用户回答后输出 BETA_DONE。父任务不要自己提问，不要代答；若某子任务被用户停止，不要重派或续跑它，等待另一个结果后汇总。不要修改文件、访问网络或写长期记忆。'});assert code==200,result
task=result['id'];report={'task':task,'checks':[]};out=home/'subcontrol-results.json';out.write_text(json.dumps(report,indent=2))
def state():return next(x for x in call('GET','/runs')[1] if x['id']==task)
def wait(condition):
 until=time.monotonic()+180
 while time.monotonic()<until:
  s=state()
  if condition(s):return s
  time.sleep(.4)
 raise AssertionError('观察原任务，不重复创建: '+task)
def events():return [json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
s=wait(lambda r:len(r.get('waiting_requests',[]))==2)
alpha=next(x for x in s['waiting_requests'] if 'ALPHA' in x['task_title']);beta=next(x for x in s['waiting_requests'] if 'BETA' in x['task_title'])
def target(item):return next(e['body'] for e in events() if e.get('method')=='subagent/started' and e['body']['task_id']==item['task_id'])
a,b=target(alpha),target(beta);url=f'/runs/{task}/subagent/control';rid=str(uuid.uuid4())
body={'task_id':a['task_id'],'call_id':a['call_id'],'action':'send','text':'直接补充：真实回答后最终输出中增加 DIRECT_CHILD_731，不要再提问。','request_id':rid}
assert call('POST',url,{**body,'call_id':b['call_id'],'request_id':str(uuid.uuid4())})[0]==409
code,first=call('POST',url,body);assert code==200 and first['state']=='pending',(code,first)
assert call('POST',url,body)==(code,first)
assert call('POST',url,{**body,'text':'另一个内容'})[0]==409
report['checks']+=['wrong attempt rejected','direct managed send acknowledged pending','same request id deduplicated','different payload same id rejected']
stop={'task_id':b['task_id'],'call_id':b['call_id'],'action':'stop','request_id':str(uuid.uuid4())}
code,stopped=call('POST',url,stop);assert code==200 and stopped['state']=='stopping',(code,stopped)
assert call('POST',url,stop)==(code,stopped)
s=wait(lambda r:len(r.get('waiting_requests',[]))==1)
assert s['waiting_requests'][0]['task_id']==a['task_id'] and not s['done']
assert call('POST',f'/runs/{task}/answer',{'call_id':beta['call_id'],'request_id':beta['id'],'answer':'继续'})[0]==409
assert call('POST',f'/runs/{task}/answer',{'call_id':alpha['call_id'],'request_id':alpha['id'],'answer':'继续'})[0]==204
wait(lambda r:r['done'])
code,status=call('POST',url,{'task_id':a['task_id'],'call_id':a['call_id'],'action':'status','request_id':rid});assert code==200 and status['state']=='consumed',(code,status)
completed=[e['body'] for e in events() if e.get('method')=='subagent/completed']
assert any(x['task_id']==a['task_id'] and 'DIRECT_CHILD_731' in x.get('answer','') for x in completed)
assert len([e for e in events() if e.get('method')=='subagent/started'])==2
report['checks']+=['stop affects only target child','late answer rejected','sibling completes with direct input','consumption recovered from child checkpoint','no child re-dispatch'];report['verified']=True;report['request_id']=rid
out.write_text(json.dumps(report,indent=2));print(json.dumps(report,indent=2))
