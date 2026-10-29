"""在专用 QA 运行目录验证文件树、历史查找与真实模型压缩。"""
import argparse, hashlib, http.cookiejar, json, os, time, urllib.request, urllib.error, urllib.parse, uuid
from pathlib import Path
p=argparse.ArgumentParser(); p.add_argument('--home',required=True); p.add_argument('--model',action='store_true'); a=p.parse_args()
home=Path(a.home).resolve(); assert home.name.startswith('.tern-web-product-qa')
config=dict(x.split('=',1) for x in (home/'.env').read_text().splitlines() if '=' in x and not x.startswith('#'))
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def call(method,path,body=None):
 req=urllib.request.Request('http://127.0.0.1:8095'+path,method=method,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
 try: res=opener.open(req,timeout=30)
 except urllib.error.HTTPError as error: res=error
 data=res.read().decode()
 try: data=json.loads(data)
 except json.JSONDecodeError: pass
 return res.status,data
assert call('POST','/login',{'name':config.get('OBSERVER_TEST_USER','qa'),'password':config['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0]==200
fixture=json.loads((home/'workpanel-results.json').read_text()); task=fixture['task']; repo=Path(fixture['path']); assert repo.is_relative_to(home/'panel-fixtures')
folder=repo/'closeout-fixture'; folder.mkdir(exist_ok=True)
(folder/'syntax.js').write_text('const 中文 = "<script>window.UNSAFE=true</script>";\n// preserved & < >\nconsole.log(中文);\n')
paged=folder/'many'; paged.mkdir(exist_ok=True)
for i in range(205): (paged/f'file-{i:03}.txt').touch()
def tree(path='.',cursor=''): return call('GET',f'/runs/{task}/workspace/tree?'+urllib.parse.urlencode({'path':path,'cursor':cursor}))
code,root=tree(); assert code==200 and root['directory']==str(repo) and all(x['name']!='.git' for x in root['entries'])
code,first=tree('closeout-fixture/many'); assert code==200 and len(first['entries'])==200 and first['next_cursor']
code,second=tree('closeout-fixture/many',first['next_cursor']); assert code==200 and len(second['entries'])==5 and not second['next_cursor']
assert len({e['path'] for e in first['entries']+second['entries']})==205
(paged/'new.txt').write_text('new')
assert tree('closeout-fixture/many',first['next_cursor'])[0]==409
(paged/'new.txt').unlink()
assert tree('../')[0] in (403,404)
assert tree('/etc')[0] in (403,404)
assert tree('preview-fixture/outside-link')[0] in (400,404)
assert tree('preview-fixture/pipe')[0]==400
assert tree('closeout-fixture/many','invalid')[0]==400
hits=[]; after=through=0
for _ in range(100):
 code,data=call('GET',f'/runs/{task}/find?'+urllib.parse.urlencode({'q':'PANEL','after':after,'through':through})); assert code==200,data
 assert len(data['hits'])<=50
 if through: assert data['through']==through
 hits+=data['hits']; after,through=data['next'],data['through']
 if not after: break
else: raise AssertionError('pagination did not finish')
assert hits and len({x['seq'] for x in hits})==len(hits)
assert any(x['where']=='问题' for x in hits) and any(x['where']=='回答' for x in hits)
for params in ['q=','q=x&after=-1','q=x&through=999999999','q='+('x'*201)]:
 assert call('GET',f'/runs/{task}/find?'+params)[0] in (400,409)
code,version=call('GET','/version'); assert code==200 and version['web_build']
assert all(x['build']==version['web_build'] for x in version['connections']['hosts'] if x['state']=='connected')
checks=['tree root and restricted path','200-entry pagination and directory version conflict','special files and invalid cursor rejected','history pagination with stable boundary and no duplicated events','invalid search requests rejected','running Web/backend build match']
report={'checks':checks,'task':task,'model':None}
if a.model:
 code,result=call('POST','/runs',{'provider':'deepseek','workspace':'','query':'这是隔离验收。记住当前对话中的标记 CLOSEOUT_CONTEXT_731，不要写长期记忆，不调用工具。仅回复 CLOSEOUT_READY。','request_id':str(uuid.uuid4())}); assert code==200,result
 model_task=result['id']; report['model']={'task':model_task}
 (home/'closeout-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
 def wait():
  until=time.monotonic()+180
  while time.monotonic()<until:
   code,rows=call('GET','/runs'); assert code==200
   state=next(x for x in rows if x['id']==model_task)
   if state['done']: return state
   time.sleep(.5)
  raise AssertionError('原任务仍在执行，请查看原任务，不重复创建')
 wait()
 code,result=call('POST',f'/runs/{model_task}/messages',{'query':'请回复刚才的完整标记，然后加 CLOSEOUT_COMPACT_OK。不要调用工具，不要写长期记忆。','compact':True,'request_id':str(uuid.uuid4())}); assert code==200,result
 wait()
 events=[json.loads(x) for x in (home/'.data/runs'/f'{model_task}.jsonl').read_text().splitlines()]
 compact=[x for x in events if x['kind']=='request' and x.get('purpose')=='compact']
 answers=[x['body'].get('answer','') for x in events if x.get('method')=='turn/completed']
 assert compact and any('CLOSEOUT_CONTEXT_731' in x and 'CLOSEOUT_COMPACT_OK' in x for x in answers), '查看原任务的压缩/回答记录'
 report['model']['compact_requests']=len(compact);report['model']['context_preserved']=True
 checks.append('real DeepSeek compaction preserves earlier context')
report['verified']=True
(home/'closeout-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2));print(json.dumps(report,ensure_ascii=False,indent=2))
