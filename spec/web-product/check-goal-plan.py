"""真实模型验证计划、显式Goal多轮与预算；不伪造模型或历史。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095
p=argparse.ArgumentParser();p.add_argument('--home',required=True);p.add_argument('--mode',choices=['plan','goal','budget','pause'],required=True);a=p.parse_args();home=Path(a.home).resolve();assert home.name.startswith(('.tern-web-product-qa','tern-qa-'))
config=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method,path,body=None):
 req=urllib.request.Request(QA_BASE+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
 try:res=opener.open(req,timeout=30)
 except urllib.error.HTTPError as error:res=error
 data=res.read().decode()
 try:data=json.loads(data)
 except json.JSONDecodeError:pass
 return res.status,data
assert call('POST','/login',{'name':config.get('OBSERVER_TEST_USER','qa'),'password':config['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0]==200
report={'mode':a.mode,'checks':[]};out=home/f'goal-plan-{a.mode}.json'
def save():out.write_text(json.dumps(report,ensure_ascii=False,indent=2))
def get():return next(x for x in call('GET','/runs')[1] if x['id']==task)
def wait(condition,seconds=180):
 until=time.monotonic()+seconds
 while time.monotonic()<until:
  s=get()
  if condition(s):return s
  time.sleep(.4)
 raise AssertionError('原任务未达到预期，先查看 '+task)
def events():return [json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
def action(action,revision,**kwargs):return call('POST',f'/runs/{task}/continuous',{'action':action,'revision':revision,'request_id':str(uuid.uuid4()),**kwargs})
query='只回复 GOAL_READY，不调用工具，不写长期记忆。'
if a.mode=='plan':query='请只制定计划，不实施。用 task_status 获取当前版本，再用 update_plan 保存两步计划：1 用bash输出PLAN_STEP_ONE；2 用bash输出PLAN_STEP_TWO并核验。两步状态均为pending。最后回复PLAN_READY，不调用bash或其他工具。'
code,created=call('POST','/runs',{'provider':'deepseek','workspace':'','permission':'plan' if a.mode=='plan' else 'edit','query':query,'request_id':str(uuid.uuid4())});assert code==200,created
task=created['id'];report['task']=task;save();wait(lambda r:r['done'])
if a.mode=='plan':
 code,data=call('GET',f'/runs/{task}/plan');assert code==200 and len(data['plan']['steps'])==2,data
 plan=data['plan'];assert plan['approved']==0
 assert call('PUT',f'/runs/{task}/plan',{'revision':plan['revision']-1,'steps':plan['steps']})[0]==409
 rid=str(uuid.uuid4());body={'revision':plan['revision'],'permission':'edit','request_id':rid}
 code,data=call('POST',f'/runs/{task}/plan/approve',body);assert code==200,data
 assert call('POST',f'/runs/{task}/plan/approve',body)[0]==200
 wait(lambda r:r['done']);history=events();assert len([x for x in history if x.get('request_id')==rid and x['kind'] in ('start','continue')])==1
 code,data=call('GET',f'/runs/{task}/plan');assert code==200 and all(x['status']=='completed' for x in data['plan']['steps']),data
 report['checks']=['real model creates versioned readonly plan','stale plan save rejected','approval deduplicates exact request','real implementation updates completed steps']
else:
 if a.mode=='goal':
  text='隔离Goal验收，不改文件、不联网、不写记忆。必须分两轮：自动轮次turns=1时用bash输出GOAL_STAGE_ONE，再report_goal continue，随后结束本轮；turns>=2时用bash输出GOAL_STAGE_TWO，然后用两个阶段真实成功bash调用ID作为证据，对唯一完成标准report_goal complete。不要在同一轮执行两阶段。'
 elif a.mode=='pause':text='Goal暂停验收：先用ask_user问“Goal是否继续”，选项继续/等待，等待用户真实回答。不要替用户回答；不改文件、不联网、不写记忆。'
 else:text='仅执行一次bash输出BUDGET_SHOULD_NOT_RUN，之后用真实调用证据report_goal complete。'
 body={'action':'start','revision':0,'text':text,'constraints':'禁止写文件、联网和长期记忆。','criteria':['按要求取得真实执行证据'],'max_turns':4,'max_tokens':1 if a.mode=='budget' else 0,'max_seconds':120,'request_id':str(uuid.uuid4())}
 code,data=call('POST',f'/runs/{task}/continuous',body);assert code==200,data;goal_id=data['continuous_goal']['id'];report['goal_id']=goal_id;save()
 assert call('POST',f'/runs/{task}/continuous',body)[0]==200
 assert call('POST',f'/runs/{task}/continuous',{**body,'text':'changed'})[0]==409
 if a.mode=='goal':
  s=wait(lambda r:r['done'] and r.get('continuous_goal',{}).get('state') in ('completed','blocked','exhausted'))
  g=s['continuous_goal'];assert g['state']=='completed' and g['turns']==2,g
  history=events();starts=[x for x in history if x.get('goal_id')==goal_id and x['kind']=='continue'];assert len(starts)==2 and len({x['queue_id'] for x in starts})==2
  assert g['evidence'] and all(x['seq'] and x['hash'] for x in g['evidence'])
  report['checks']=['explicit start with request dedup','two real automatic turns through durable queue','successful tool evidence required and persisted','completed goal stops continuation']
 elif a.mode=='budget':
  s=wait(lambda r:r['done'] and r.get('continuous_goal',{}).get('state')=='exhausted');g=s['continuous_goal'];history=events()
  assert not [x for x in history if x['kind']=='request' and x['seq']>g['start_seq']]
  assert g['used_tokens']==0
  report['checks']=['tiny token cap rejects request before upstream model call','exhausted state persisted','no automatic budget expansion']
 else:
  s=wait(lambda r:len(r.get('waiting_requests',[]))==1);g=s['continuous_goal'];assert action('pause',g['revision'])[0]==200
  s=wait(lambda r:r['done'] and r.get('continuous_goal',{}).get('state')=='paused');g=s['continuous_goal'];count=len([x for x in events() if x['kind']=='continue']);time.sleep(2);assert count==len([x for x in events() if x['kind']=='continue'])
  code,data=action('modify',g['revision'],text='用bash输出GOAL_RESUMED，然后引用这次真实成功调用ID，report_goal complete。不要写文件、联网、写记忆。',constraints=g['constraints'],criteria=['bash实际输出GOAL_RESUMED'],max_turns=4,max_tokens=0,max_seconds=120);assert code==200,data
  g=data['continuous_goal'];assert action('resume',g['revision'])[0]==200
  s=wait(lambda r:r['done'] and r.get('continuous_goal',{}).get('state') in ('completed','blocked','exhausted'));assert s['continuous_goal']['state']=='completed',s['continuous_goal']
  assert s['continuous_goal']['id']==goal_id and s['continuous_goal']['turns']==2
  report['checks']=['real waiting request remains user-owned','pause interrupts current round and prohibits followups','modify bumps revision and keeps goal identity','resume continues goal without resetting turn count','real resume completion evidence persisted']
report['verified']=True;save();print(json.dumps(report,ensure_ascii=False,indent=2))
