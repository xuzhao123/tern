"""隔离真实项目、沙箱命令、端口导出与跨轮生命周期验收。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid,socket,os
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
fixture=json.loads((home/'workpanel-results.json').read_text());task=fixture['task'];repo=Path(fixture['path']);assert repo.is_relative_to(home/'panel-fixtures')
(repo/'.agents').mkdir(exist_ok=True);path=repo/'.agents/actions.json'
source={'version':1,'actions':[{'name':'qa-dev','command':'python3 -u -m http.server 18080 --bind 127.0.0.1','platforms':['linux'],'background':True,'ports':[18080]},{'name':'qa-fail','command':"printf 'EXPECTED_FAILURE\\n'; exit 7",'platforms':['linux']},{'name':'qa-init','command':"printf 'COPY_INIT_PARTIAL' > qa-init-partial.txt; exit 9",'initialize':True,'platforms':['linux']}]};
if path.exists(): assert json.loads(path.read_text())==source and (home/'operations-results.json').exists(), '不覆盖非本脚本的配置'
path.write_text(json.dumps(source));
(repo/'closeout-fixture/live.html').write_text('<!doctype html><h1>LIVE_PREVIEW_FIXTURE</h1><input aria-label="Name"><button id="increment" onclick="document.getElementById(\'count\').textContent=Number(document.getElementById(\'count\').textContent)+1">Increment</button><b id="count">0</b>')
report={'task':task,'checks':[]};out=home/'operations-results.json';out.write_text(json.dumps(report,indent=2));base=f'/runs/{task}/operations'
code,data=call('GET',base);assert code==200 and data['supported'] and len(data['actions'])==3,data;version=data['version']
def start(name,version=version,task=task):return call('POST',f'/runs/{task}/operations',{'name':name,'version':version,'confirmed':True,'request_id':str(uuid.uuid4())})
assert start('qa-dev','old')[0]==409
rid=str(uuid.uuid4());body={'name':'qa-dev','version':version,'confirmed':True,'request_id':rid};code,op=call('POST',base,body);assert code==200,op;report['operation']=op['id'];out.write_text(json.dumps(report,indent=2));assert call('POST',base,body)[1]['id']==op['id'];assert start('qa-dev')[0]==409
sockdir=home/'.data/services'/op['id']/'scratch/.tern-ports'
def request():
 fd=os.open(sockdir,os.O_RDONLY);s=socket.socket(socket.AF_UNIX);s.settimeout(3)
 try:s.connect(f'/proc/self/fd/{fd}/18080.sock');s.sendall(b'GET /closeout-fixture/live.html HTTP/1.0\r\nHost: 127.0.0.1:18080\r\n\r\n');response=b''
 except: s.close();os.close(fd);raise
 while True:
  data=s.recv(65536)
  if not data:break
  response+=data
 s.close();os.close(fd);return response
until=time.monotonic()+30
while True:
 try:assert b'LIVE_PREVIEW_FIXTURE' in request();break
 except (OSError,AssertionError):
  if time.monotonic()>until:raise
  time.sleep(.3)
assert not (sockdir/'18081.sock').exists()
assert call('GET',f'/runs/af61140a-4907-42e5-ab0c-23ed0157ce6b/operations/{op["id"]}')[0]==403
code,continued=call('POST',f'/runs/{task}/messages',{'query':'仅回复 SERVICE_STILL_RUNNING，不调用工具、不修改文件、不写长期记忆。','request_id':str(uuid.uuid4())});assert code==200,continued
until=time.monotonic()+120
while time.monotonic()<until:
 row=next(x for x in call('GET','/runs')[1] if x['id']==task)
 if row['done']:break
 time.sleep(.4)
else:raise AssertionError('原任务未结束')
assert b'LIVE_PREVIEW_FIXTURE' in request()
code,log=call('GET',base+'/'+op['id']);assert code==200 and 'Serving HTTP' in log['output'] and 'GET /closeout-fixture/live.html' in log['output'],log
code,failed=start('qa-fail');assert code==200,failed
until=time.monotonic()+30
while time.monotonic()<until:
 code,failed_read=call('GET',base+'/'+failed['id'])
 if failed_read['operation']['state']=='failed':break
 time.sleep(.2)
assert failed_read['operation']['exit_code']==7,failed_read
assert start('qa-init')[0]==400
assert call('POST',base+'/'+op['id']+'/stop',{})[0]==200
until=time.monotonic()+20
while time.monotonic()<until:
 code,stopped=call('GET',base+'/'+op['id'])
 if stopped['operation']['state']=='stopped':break
 time.sleep(.2)
assert stopped['operation']['state']=='stopped',stopped
report['checks']=['config-version mismatch rejected','exact request dedup and duplicate service rejection','real isolated HTTP reachable through only exported Unix port','wrong task cannot read instance','service survives real model continuation','live logs preserve actual server requests','nonzero command exit reported','initialization forbidden on original project','owned service stops and confirms process exit'];report['verified']=True;out.write_text(json.dumps(report,indent=2));print(json.dumps(report,indent=2))
