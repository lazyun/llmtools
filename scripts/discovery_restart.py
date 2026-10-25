#!/usr/bin/env python3
"""Real MCP process restart under a production headless Desktop runtime owner."""
import argparse,json,pathlib,sqlite3,subprocess,time,tomllib
from runtime_smoke import MCP

def main():
 p=argparse.ArgumentParser();p.add_argument('--config-dir',required=True);p.add_argument('--report',default='.smoke/discovery/restart.json');a=p.parse_args()
 directory=pathlib.Path(a.config_dir);binary=pathlib.Path('bin/ai-worker-mcp').resolve();stop=directory/'discovery-owner.stop'
 if stop.exists():raise RuntimeError('use a fresh stop-file path')
 owner=subprocess.Popen([str(pathlib.Path('bin/runtime-backend').resolve()),'--config-dir',str(directory),'--binary',str(binary),'--start','--stop-file',str(stop)],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 report={'status':'FAIL'};clients=[]
 try:
  # The owner emits state after managed startup, before diagnostics.
  state=json.loads(owner.stdout.readline());report['ownership']=state.get('ownership')
  c=tomllib.loads((directory/'config.toml').read_text());dbpath=directory/c['storage']['database']
  def run_record(rid):
   with sqlite3.connect('file:'+str(dbpath)+'?mode=ro',uri=True) as db:return json.loads(db.execute('SELECT body FROM runs WHERE id=?',(rid,)).fetchone()[0])
  def connect():
   m=MCP(binary,directory);clients.append(m);return m
  first=connect();pending=first.call('delegate_implementation',{'prompt':'Request the missing contract.txt with request_context immediately; do not guess its token. After receiving it, return its token exactly.'})
  report['pending']=pending
  if pending['status']!='needs_context':raise RuntimeError('NEEDS_CONTEXT_NOT_OBSERVED')
  before=run_record(pending['worker_run_id']);old_pid=first.p.pid;first.close();clients.remove(first)
  second=connect();result=second.call('ai_worker_continue',{'worker_run_id':pending['worker_run_id'],'additional_context':[{'request_id':q['id'],'type':'file','path':q['target'],'content':'RESTART_DISCOVERY_OK'} for q in pending['context_requests']]})
  after=run_record(pending['worker_run_id']);report['result']=result
  report['checks']={'old_mcp_exited':first.p.poll()==0,'different_mcp_process':old_pid!=second.p.pid,'completed':result['status']=='completed' and 'RESTART_DISCOVERY_OK' in result.get('content','')}
  for key in ['worker_run_id','runtime_session_id','runtime_agent','runtime_model','routing_revision','runtime_identity','capabilities']:
   report['checks']['same_'+key]=bool(before.get(key)) and before.get(key)==after.get(key)
  report['checks']['context_only_capability_snapshot']=isinstance(after.get('capabilities'),dict) and not any(after['capabilities'].values())
  report['snapshot']={k:after.get(k) for k in ['worker_run_id','runtime_session_id','runtime_agent','runtime_model','routing_revision','capabilities']}
  report['status']='PASS' if all(report['checks'].values()) else 'FAIL'
 except Exception as e:report['error']=str(e)
 finally:
  for m in clients:m.close()
  stop.touch()
  try:owner.communicate(timeout=20)
  except subprocess.TimeoutExpired:owner.terminate();owner.communicate(timeout=10)
  report['owner_exit_code']=owner.returncode
  with sqlite3.connect('file:'+str(directory/'llmtools.db')+'?mode=ro',uri=True) as db:
   report['residual_running']=db.execute("SELECT count(*) FROM runs WHERE status='RUNNING'").fetchone()[0]
   report['runtime_status']=json.loads(db.execute('SELECT body FROM runtime_instances ORDER BY generation DESC LIMIT 1').fetchone()[0])['status']
  if report['owner_exit_code']!=0 or report['residual_running'] or report['runtime_status']!='STOPPED':report['status']='FAIL'
  pathlib.Path(a.report).write_text(json.dumps(report,indent=2));print(json.dumps(report))
if __name__=='__main__':main()
