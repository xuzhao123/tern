"""真实解析器、来源版本拒绝及真实模型读取显式技能快照。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid,shutil
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095
p=argparse.ArgumentParser();p.add_argument('--home',required=True);a=p.parse_args();home=Path(a.home).resolve();assert home.name.startswith(('.tern-web-product-qa','tern-qa-'))
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
assert call('POST','/login',{'name':config.get('TERN_QA_USER', config.get('OBSERVER_TEST_USER', 'qa')),'password':(config.get('TERN_QA_PASSWORD') or config['OBSERVER_TEST_PASSWORD']).strip('\"\'')})[0]==200
name='closeout-qa-'+uuid.uuid4().hex[:8];folder=home/'skills'/name;folder.mkdir(parents=True);file=folder/'SKILL.md'
source=f'---\nname: {name}\ndescription: Isolated QA text response skill\n---\nWhen explicitly selected, reply PINNED_SKILL_OK. Do not call tools or write memory.\n';file.write_text(source)
try:
 code,hub=call('GET','/hub');assert code==200,hub
 skill=next(x for x in hub['skills'] if x['name']==name);ref={k:skill[k] for k in ('name','path','version')};assert len(ref['version'])==64
 base={'provider':'deepseek','workspace':'','query':'请遵循本条消息显式选择的技能，回复其中指定的文本。不要调用工具，不要写长期记忆。','skill_refs':[ref]}
 before={x['id'] for x in call('GET','/runs')[1]}
 file.write_text(source+'\nChanged version.\n')
 assert call('POST','/runs',{**base,'request_id':str(uuid.uuid4())})[0]==409
 file.write_text(source)
 assert call('POST','/runs',{**base,'skill_refs':[{**ref,'path':'builtin/'+name+'/SKILL.md'}],'request_id':str(uuid.uuid4())})[0]==409
 assert call('POST','/runs',{**base,'skill_refs':[ref,ref],'request_id':str(uuid.uuid4())})[0]==409
 assert before=={x['id'] for x in call('GET','/runs')[1]}
 code,result=call('POST','/runs',{**base,'request_id':str(uuid.uuid4())});assert code==200,result;task=result['id']
 report={'task':task,'checks':['changed content rejected','same-name source replacement rejected','duplicate refs rejected','rejected refs create no task']}
 (home/'skill-refs-results.json').write_text(json.dumps(report,indent=2))
 until=time.monotonic()+180
 while time.monotonic()<until:
  row=next(x for x in call('GET','/runs')[1] if x['id']==task)
  if row['done']:break
  time.sleep(.5)
 else:raise AssertionError('观察原任务，不重复创建')
 events=[json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()]
 assert next(x for x in events if x['kind']=='start')['skill_refs']==[ref]
 answers=[x['body'].get('answer','') for x in events if x.get('method')=='turn/completed']
 assert any('PINNED_SKILL_OK' in x for x in answers)
 file.unlink()
 assert call('POST',f'/runs/{task}/messages',{'query':'再次使用刚才的技能','skill_refs':[ref],'request_id':str(uuid.uuid4())})[0]==409
 report['checks']+=['real model uses pinned skill body','metadata persisted in start','deleted skill rejected on continuation'];report['verified']=True
 (home/'skill-refs-results.json').write_text(json.dumps(report,indent=2));print(json.dumps(report,indent=2))
finally:
 shutil.rmtree(folder)
