"""只在新隔离夹具提交，注册真实只读模型任务；不碰开发仓库。"""
import argparse,http.cookiejar,json,time,urllib.request,urllib.error,uuid,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--home',required=True);a=p.parse_args();home=Path(a.home).resolve();assert home.name.startswith('.tern-web-product-qa')
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
repo=home/'git-delivery-fixtures'/uuid.uuid4().hex;repo.mkdir(parents=True)
def git(*args):return subprocess.check_output(['git',*args],cwd=repo,text=True).strip()
git('init','-b','main');git('config','user.name','Tern QA');git('config','user.email','tern-qa@example.invalid');git('config','commit.gpgSign','false');git('config','core.hooksPath',str(repo/'.git/hooks'));git('config','core.autocrlf','false')
(repo/'a.txt').write_text('a original\n');(repo/'中文 file.txt').write_text('untracked\n')
code,project=call('POST','/settings/workspaces',{'path':str(repo)});assert code==200,project
code,created=call('POST','/runs',{'workspace':project['name'],'provider':'deepseek','permission':'plan','query':'只回复 GIT_READY；不调用工具，不改文件，不写记忆。','request_id':str(uuid.uuid4())});assert code==200,created
task=created['id'];report={'task':task,'path':str(repo),'checks':[]};out=repo/'../report.json';out.write_text(json.dumps(report,indent=2));base=f'/runs/{task}/git'
until=time.monotonic()+180
while time.monotonic()<until:
 if next(r for r in call('GET','/runs')[1] if r['id']==task)['done']:break
 time.sleep(.4)
else:raise AssertionError('真实任务未结束')
def state():
 code,data=call('GET',base);assert code==200,data;return data
def act(action,paths=None,version=None,message='QA fixture commit'):
 payload={'action':action,'paths':paths or [],'version':version or state()['version'],'message':message,'confirmed':True,'request_id':str(uuid.uuid4())}
 return call('POST',base,payload),payload
s=state();time.sleep(1.1);assert state()['version']==s['version'];assert not s.get('commit_blocked'),s
assert act('commit')[0][0]==409,'unborn empty commit must reject'
(code,result),body=act('stage',['a.txt']);assert code==200 and result['state']=='completed',result;assert git('diff','--cached','--name-only')=='a.txt';assert call('POST',base,body)[1]==result
assert call('POST',base,{**body,'paths':['中文 file.txt']})[0]==409
assert act('unstage',['a.txt'])[0][0]==200;assert git('diff','--cached','--name-only')=='';assert (repo/'a.txt').read_text()=='a original\n'
assert act('stage',['a.txt'])[0][0]==200
(code,result),body=act('commit');assert code==200 and result['state']=='completed',result;first=result['commit'];assert git('rev-parse','HEAD')==first
assert call('POST',base,body)[1]['commit']==first;assert call('GET',base+'/operation/'+body['request_id'])[1]['state']=='completed';assert git('rev-list','--count','HEAD')=='1';assert git('status','--porcelain')=='?? "\\344\\270\\255\\346\\226\\207 file.txt"'
report['checks']+=['stable review version across clock changes','unborn empty commit rejected','selected path staging only','exact request dedup and changed payload rejected','unstage retains worktree','initial local commit and receipt recovery without duplicate']
(repo/'a.txt').write_text('staged two\n');assert act('stage',['a.txt'])[0][0]==200;(repo/'a.txt').write_text('unstaged three\n')
s=state();assert next(f for f in s['files'] if f['path']=='a.txt')['index']=='M';assert act('commit')[0][1]['state']=='completed';assert git('show','HEAD:a.txt')=='staged two';assert (repo/'a.txt').read_text()=='unstaged three\n'
s=state();(repo/'中文 file.txt').write_text('changed externally\n');assert act('stage',['中文 file.txt'],s['version'])[0][0]==409
lock=repo/'.git/index.lock';lock.write_text('foreign-lock');assert act('stage',['a.txt'])[0][0]==409;assert lock.read_text()=='foreign-lock';lock.unlink()
hook=repo/'.git/hooks/pre-commit';hook.write_text('#!/bin/sh\ntouch should-not-run\n');hook.chmod(0o755);assert '钩子' in state()['commit_blocked'];assert act('commit')[0][0]==409;assert not (repo/'should-not-run').exists();hook.unlink()
(repo/'.gitattributes').write_text('*.txt filter=qa\n');git('config','filter.qa.clean','touch should-not-run');s=state();assert next(f for f in s['files'] if f['path']=='a.txt')['notice'];assert act('stage',['a.txt'])[0][0]==409;assert not (repo/'should-not-run').exists();(repo/'.gitattributes').unlink();git('config','--unset','filter.qa.clean')
(repo/'binary.bin').write_bytes(bytes(range(256)));(repo/'-literal').write_text('dash\n');(repo/'link').symlink_to('/etc/passwd');(repo/'executable.sh').write_text('#!/bin/sh\nexit 0\n');(repo/'executable.sh').chmod(0o755)
assert act('stage',['binary.bin','-literal','link','executable.sh'])[0][0]==200
assert subprocess.check_output(['git','show',':binary.bin'],cwd=repo)==bytes(range(256));assert git('show',':link')=='/etc/passwd';assert git('ls-files','--stage','--','executable.sh').startswith('100755 ')
(repo/'binary.bin').unlink();assert act('stage',['binary.bin'])[0][0]==200;assert 'binary.bin' not in git('ls-files')
git('config','core.autocrlf','true');assert act('stage',['a.txt'])[0][0]==409;git('config','core.autocrlf','false')
hook=repo/'.git/hooks/post-index-change';hook.write_text('#!/bin/sh\ntouch should-not-run\n');hook.chmod(0o755);assert act('stage',['a.txt'])[0][0]==200;assert act('unstage',['a.txt'])[0][0]==200;assert not (repo/'should-not-run').exists();hook.unlink()
report['checks']+=['raw staging preserves binary, literal dash path, executable and symlink bytes','staging deletion removes only index entry','line-ending conversion refuses with explicit reason','stage/unstage never execute post-index-change hook']
report['checks']+=['commit includes only staged bytes and retains unstaged edits','external content changes reject stale review','foreign index lock retained','configured executable hooks refuse commit without execution','custom clean filter refuses staging without execution'];report['verified']=True;out.write_text(json.dumps(report,ensure_ascii=False,indent=2));print(json.dumps(report,ensure_ascii=False,indent=2))
