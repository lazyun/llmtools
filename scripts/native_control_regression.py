#!/usr/bin/env python3
"""Real managed attach/abort/lifecycle regression without requiring model completion."""
import argparse,json,pathlib,sqlite3,subprocess,threading,time,tomllib,urllib.request,urllib.parse
from runtime_smoke import MCP

def main():
 p=argparse.ArgumentParser();p.add_argument('--config-dir',required=True);p.add_argument('--expected-owner',choices=['desktop','mcp'],required=True);p.add_argument('--report',required=True);a=p.parse_args();directory=pathlib.Path(a.config_dir).resolve()
 cfg=tomllib.loads((directory/'config.toml').read_text());dbpath=directory/cfg['storage']['database'];binary=pathlib.Path('bin/ai-worker-mcp').resolve();clients=[];result={};report={'backend':'REAL_OPENCODE','owner':a.expected_owner,'status':'FAIL','checks':{}}
 def check(k,v):
  report['checks'][k]=bool(v)
  if not v:raise AssertionError(k)
 def instance():
  with sqlite3.connect(dbpath) as db:return json.loads(db.execute('select body from runtime_instances order by generation desc limit 1').fetchone()[0])
 def api(path):return json.load(urllib.request.urlopen(instance()['base_url']+path+'?directory='+urllib.parse.quote(str(directory/'runtime-workspace'),safe=''),timeout=5))
 try:
  owner=MCP(binary,directory);clients.append(owner);other=MCP(binary,directory);clients.append(other)
  objective='Native control regression cancellation fixture '+str(time.time())+'. Produce 900 invented planet descriptions in the final response. All facts are synthetic. Do not use execution tools.'
  def invoke():
   try:result['value']=owner.call('delegate_implementation',{'prompt':objective})
   except Exception as e:result['error']=type(e).__name__
  thread=threading.Thread(target=invoke,daemon=True);thread.start();active=None;deadline=time.monotonic()+30
  while time.monotonic()<deadline:
   with sqlite3.connect(dbpath) as db:rows=db.execute("select body from runs where status='RUNNING'").fetchall()
   for row in rows:
    r=json.loads(row[0])
    if r.get('objective')==objective and r.get('runtime_session_id') and api('/session/status').get(r['runtime_session_id'],{}).get('type')=='busy':active=r;break
   if active:break
   time.sleep(.1)
  check('runtime_busy',active is not None);report['worker_run_id']=active['worker_run_id'];before=instance()
  check('expected_owner', (before['owner_pid']==owner.p.pid)==(a.expected_owner=='mcp'))
  observed=subprocess.run([str(pathlib.Path('bin/runtime-backend').resolve()),'--config-dir',str(directory),'--binary',str(binary),'--verify-non-owner'],capture_output=True,text=True,timeout=20)
  check('desktop_backend_attach',observed.returncode==0)
  snapshots=[json.loads(x) for x in observed.stdout.splitlines()]
  check('same_runtime_instance',snapshots[0]['instance']['runtime_instance_id']==before['runtime_instance_id'])
  check('non_owner_stop_rejected',snapshots[-1].get('non_owner_stop_rejected') is True)
  cancelled=other.call('ai_worker_cancel',{'worker_run_id':active['worker_run_id']});thread.join(15)
  check('cancel_call',cancelled['status']=='cancelled');check('execution_cancelled',not thread.is_alive() and result.get('value',{}).get('status')=='cancelled')
  check('abort_idle',api('/session/status').get(active['runtime_session_id'],{}).get('type','idle')=='idle')
  check('no_pending_questions',not any(q['sessionID']==active['runtime_session_id'] for q in api('/question')))
  check('no_new_runtime_generation',instance()['generation']==before['generation'])
  processes=subprocess.run(['ps','-axo','pid=,comm='],capture_output=True,text=True,check=True).stdout.splitlines();count=sum(x.strip().split(' ',1)[-1].strip().rsplit('/',1)[-1]=='opencode' for x in processes);report['opencode_process_count']=count;check('no_duplicate_server',count==1)
  for m in reversed(clients):m.close()
  clients=[]
  if a.expected_owner=='mcp':check('owner_shutdown',instance()['status']=='STOPPED')
  else:check('desktop_survives_mcp_exit',api('/global/health')['healthy'])
  report['status']='PASS'
 except Exception as e:report['failure']=str(e) if isinstance(e,AssertionError) else type(e).__name__
 finally:
  for m in reversed(clients):m.close()
  pathlib.Path(a.report).write_text(json.dumps(report,indent=2));print(json.dumps(report))
if __name__=='__main__':main()
