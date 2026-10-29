"""真实模型Goal的队列优先、无证据拒绝、用户结束。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--home',required=True);a=p.parse_args();home=Path(a.home).resolve();assert home.name.startswith('.tern-web-product-qa')
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
report={'checks':[]};out=home/'goal-arbitration.json'
def save():out.write_text(json.dumps(report,ensure_ascii=False,indent=2))
def get():return next(r for r in call('GET','/runs')[1] if r['id']==task)
def wait(pred):
 end=time.monotonic()+180
 while time.monotonic()<end:
  r=get()
  if pred(r):return r
  time.sleep(.3)
 raise AssertionError('请查看原任务 '+task)
def events():return [json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
code,created=call('POST','/runs',{'provider':'deepseek','permission':'edit','query':'只回复 ARBITRATION_READY，不调用工具、不写长期记忆。','request_id':str(uuid.uuid4())});assert code==200,created
task=created['id'];report['task']=task;save();wait(lambda r:r['done'])
text='Goal队列仲裁验收，不改文件、不联网、不写记忆。自动轮次turns=1时仅调用ask_user问“仲裁是否继续”，选项继续/等待；得到真实回答后报告continue，progress第一阶段已完成、next检查第二阶段，再结束本轮。turns>=2时先尝试report_goal complete且evidence=[]，观察缺证据拒绝；然后report_goal blocked，reason缺少实际工具验收证据，不要用task_status作为验收证据。不要执行bash或文件工具。'
code,data=call('POST',f'/runs/{task}/continuous',{'action':'start','revision':0,'text':text,'constraints':'不写文件，不联网；必须先拒绝缺少证据的完成报告。','criteria':['执行实际项目检查'],'max_turns':4,'max_seconds':180,'max_tokens':0,'request_id':str(uuid.uuid4())});assert code==200,data;goal=data['continuous_goal'];report['goal']=goal['id'];save()
r=wait(lambda r:len(r.get('waiting_requests',[]))==1);req=r['waiting_requests'][0]
qid=str(uuid.uuid4());code,queued=call('POST',f'/runs/{task}/queue',{'text':'这是优先处理的用户队列消息。仅回复 USER_QUEUE_FIRST，不调用工具，不报告Goal，不写记忆。','request_id':qid});assert code==200,queued
assert call('POST',f'/runs/{task}/answer',{'call_id':req['call_id'],'request_id':req['id'],'answer':'继续'})[0]==204
r=wait(lambda r:r['done'] and r.get('continuous_goal',{}).get('state') in ('blocked','exhausted','completed'))
assert r['continuous_goal']['state']=='blocked',r['continuous_goal'];history=events();starts=[e for e in history if e['kind']=='continue'];assert len(starts)==3,starts;assert starts[0].get('goal_id')==goal['id'] and starts[1].get('queue_id')==queued['id'] and starts[1].get('goal_id')==goal['id'] and starts[2].get('goal_id')==goal['id']
assert any('缺少完成标准对应的真实工具证据' in json.dumps(e,ensure_ascii=False) for e in history),'模型未实际尝试缺证据完成报告；不能算通过'
assert r['continuous_goal']['turns']==2
code,data=call('POST',f'/runs/{task}/continuous',{'action':'end','revision':r['continuous_goal']['revision'],'request_id':str(uuid.uuid4())});assert code==200 and data['continuous_goal']['state']=='ended',data
n=len(events());time.sleep(2);assert len(events())==n
report['checks']=['user queue round runs between two Goal rounds without reset','missing evidence completion report actually rejected','blocked Goal does not autonomously spin','user end persists as incomplete and schedules no more work'];report['verified']=True;save();print(json.dumps(report,ensure_ascii=False,indent=2))
