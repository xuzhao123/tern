"""隔离副本的基线、应用冲突与分叉引用生命周期真实回归。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid,subprocess
from pathlib import Path
import os as _os; QA_BASE = _os.environ.get('TERN_QA_BASE', 'http://127.0.0.1:8095')  # 网页端地址，默认是原来的 8095
p=argparse.ArgumentParser();p.add_argument('--home',required=True);a=p.parse_args();home=Path(a.home).resolve();assert home.name.startswith(('.tern-web-product-qa','tern-qa-'))
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
assert call('POST','/login',{'name':config.get('OBSERVER_TEST_USER','qa'),'password':config['OBSERVER_TEST_PASSWORD'].strip('\"\'')})[0]==200
repo=home/'copy-delivery-fixtures'/uuid.uuid4().hex;repo.mkdir(parents=True)
def git(*args):return subprocess.check_output(['git',*args],cwd=repo,text=True).strip()
git('init','-b','main');git('config','user.name','QA');git('config','user.email','qa@example.invalid');git('config','commit.gpgSign','false');(repo/'file.txt').write_text('baseline\n');git('add','file.txt');git('commit','-qm','fixture baseline');head=git('rev-parse','HEAD');(repo/'file.txt').write_text('existing user edit\n')
code,project=call('POST','/settings/workspaces',{'path':str(repo)});assert code==200,project
code,created=call('POST','/runs',{'workspace':project['name'],'copy':True,'provider':'deepseek','permission':'plan','query':'只回复 COPY_BOUNDARY_READY，不调用工具、不写文件、不写记忆。','request_id':str(uuid.uuid4())});assert code==200,created
task=created['id'];report={'task':task,'original':str(repo),'checks':[]};out=home/'copy-delivery.json';out.write_text(json.dumps(report,indent=2))
until=time.monotonic()+180
while time.monotonic()<until:
 r=next(r for r in call('GET','/runs')[1] if r['id']==task)
 if r['done']:break
 time.sleep(.3)
else:raise AssertionError('任务未结束')
code,data=call('GET',f'/runs/{task}/operations');assert code==200 and data['copy'];copy=Path(data['directory']);assert copy!=repo and copy.is_relative_to(home/'.data/copies');assert (copy/'file.txt').read_text()=='existing user edit\n'
assert subprocess.check_output(['git','status','--porcelain'],cwd=copy)==b''
(copy/'file.txt').write_text('copy desired edit\n');(copy/'new.txt').write_text('copy new file\n');(repo/'file.txt').write_text('conflicting external edit\n')
code,error=call('POST',f'/runs/{task}/apply-copy',{});assert code==409,error;assert (repo/'file.txt').read_text()=='conflicting external edit\n';assert not (repo/'new.txt').exists();assert git('rev-parse','HEAD')==head
(repo/'file.txt').write_text('existing user edit\n');code,result=call('POST',f'/runs/{task}/apply-copy',{});assert code==200,result;assert (repo/'file.txt').read_text()=='copy desired edit\n';assert (repo/'new.txt').read_text()=='copy new file\n';assert git('rev-parse','HEAD')==head
history=[json.loads(x) for x in (home/'.data/runs'/f'{task}.jsonl').read_text().splitlines()];turn=next(e['body']['turn_id'] for e in history if e.get('method')=='turn/started')
code,fork=call('POST',f'/runs/{task}/fork',{'turn':turn});assert code==200,fork;report['fork']=fork['id'];report['copy']=str(copy);out.write_text(json.dumps(report,indent=2))
assert call('DELETE',f'/runs/{task}')[0]==204;assert copy.is_dir();assert call('GET',f'/runs/{fork["id"]}/operations')[1]['directory']==str(copy)
assert call('DELETE',f'/runs/{fork["id"]}')[0]==204;assert not copy.exists();assert repo.is_dir() and (repo/'new.txt').read_text()=='copy new file\n'
report['checks']=['copy baseline includes original uncommitted bytes','apply conflict leaves original untouched','apply success leaves original commit unchanged','fork shares copy and deletion retains while referenced','last owner deletion removes only copy'];report['verified']=True;out.write_text(json.dumps(report,ensure_ascii=False,indent=2));print(json.dumps(report,ensure_ascii=False,indent=2))
