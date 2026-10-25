#!/usr/bin/env python3
"""Real managed MCP lifecycle/continuation/abort regression; no runtime URL input.
Only fixed synthetic fixtures reach the model. No UI or provider configuration.
"""
import argparse,json,pathlib,sqlite3,subprocess,threading,time,tomllib,urllib.request,urllib.parse
from runtime_smoke import MCP

def main():
 p=argparse.ArgumentParser();p.add_argument('--config-dir',required=True);p.add_argument('--binary',default='bin/ai-worker-mcp');p.add_argument('--backend',default='bin/runtime-backend');p.add_argument('--skip-normal',action='store_true');p.add_argument('--skip-context',action='store_true');p.add_argument('--report',default='.smoke/managed-discovery/regression.json');a=p.parse_args()
 directory=pathlib.Path(a.config_dir).resolve();binary=pathlib.Path(a.binary).resolve()
 with open(directory/'config.toml','rb') as f:c=tomllib.load(f)
 dbpath=directory/c['storage']['database']
 report={'backend':'REAL_OPENCODE_REAL_MCP','status':'FAIL','checks':{},'runs':[]};clients=[]
 def connection():
  m=MCP(binary,directory);clients.append(m);return m
 def run_record(runid):
  with sqlite3.connect(dbpath) as db:return json.loads(db.execute('SELECT body FROM runs WHERE id=?',(runid,)).fetchone()[0])
 def instance():
  with sqlite3.connect(dbpath) as db:return json.loads(db.execute('SELECT body FROM runtime_instances ORDER BY generation DESC LIMIT 1').fetchone()[0])
 def task(m,objective):return m.call('delegate_implementation',{'prompt':objective})
 def record(r):
  report['runs'].append({k:r.get(k) for k in ['worker_run_id','status','error'] if r.get(k) is not None})
 def check(name,value):
  report['checks'][name]=bool(value)
  print(json.dumps({'check':name,'pass':bool(value)}),flush=True)
  if not value:raise RuntimeError(name)
 def api(path):
  r=instance();url=r['base_url']+path+'?directory='+urllib.parse.quote(str(directory/'runtime-workspace'),safe='')
  return json.load(urllib.request.urlopen(url,timeout=5))
 try:
  owner=connection()
  try:
   if not a.skip_normal:
    normal=task(owner,'Reply exactly: LLMTOOLS_PONG');record(normal);check('normal',normal['status']=='completed' and normal['content']=='LLMTOOLS_PONG')
  except RuntimeError:pass
  other=connection()
  try:
   if not a.skip_context:
    pending=task(owner,'Context escalation fixture. Tell me the exact value of the private acceptance token in contract.txt. Its contents have deliberately not been supplied. Request contract.txt from Codex using request_context; do not guess. After the file is supplied, include its token in your final response.');record(pending);check('needs_context',pending['status']=='needs_context')
    before_record=run_record(pending['worker_run_id']);before=before_record['runtime_session_id']
    result=other.call('ai_worker_continue',{'worker_run_id':pending['worker_run_id'],'additional_context':[{'request_id':q['id'],'type':'file','path':q['target'],'content':'acceptance token: MANAGED-CONTEXT-OK'} for q in pending['context_requests']]});record(result)
    check('context_same_frozen_routing', all(run_record(result['worker_run_id']).get(k)==before_record.get(k) for k in ['runtime_agent','runtime_model','routing_revision']))
    check('context_same_run_session_completed',result['status']=='completed' and result['worker_run_id']==pending['worker_run_id'] and 'MANAGED-CONTEXT-OK' in result['content'] and run_record(result['worker_run_id'])['runtime_session_id']==before)
  except RuntimeError:pass
  try:
   pending=task(owner,'Pure text decision fixture. Immediately use the native question tool to ask Codex to choose AMBER or TEAL. Neither option is authorized and you must not choose. After the native reply, include the chosen label in your final response. No execution tools are allowed.');record(pending);check('needs_decision',pending['status']=='needs_decision')
   before_record=run_record(pending['worker_run_id']);before=before_record['runtime_session_id'];answers=[]
   for q in pending['decision_requests']:answers.append({'request_id':q['id'],'selection':q['options'][0]['id'],'explanation':'Synthetic fixture: the parent selects the first offered label.'})
   result=other.call('ai_worker_continue',{'worker_run_id':pending['worker_run_id'],'decision_responses':answers});record(result)
   check('decision_same_frozen_routing', all(run_record(result['worker_run_id']).get(k)==before_record.get(k) for k in ['runtime_agent','runtime_model','routing_revision']))
   check('decision_same_run_session_completed',result['status']=='completed' and result['worker_run_id']==pending['worker_run_id'] and run_record(result['worker_run_id'])['runtime_session_id']==before)
  except RuntimeError:pass
  current=instance();check('mcp_is_owner',current['owner_pid']==owner.p.pid and current['status']=='READY');report['runtime_instance']={k:current[k] for k in ['runtime_instance_id','generation','owner_pid','pid','base_url']}
  observed=subprocess.run([str(pathlib.Path(a.backend).resolve()),'--config-dir',str(directory),'--binary',str(binary),'--verify-non-owner'],capture_output=True,text=True,timeout=20)
  check('desktop_backend_exit',observed.returncode==0)
  snapshot=json.loads(observed.stdout.splitlines()[0]);check('desktop_attached_non_owner',snapshot['ownership']=='ATTACHED_NON_OWNER' and snapshot['instance']['runtime_instance_id']==current['runtime_instance_id']);check('non_owner_stop_rejected',json.loads(observed.stdout.splitlines()[-1]).get('non_owner_stop_rejected') is True)
  check('desktop_close_keeps_owner_healthy',api('/global/health')['healthy'])
  result_box={};start=time.time()
  def generate():
   try:result_box['result']=task(owner,'Cancellation fixture. Produce a final response containing 900 numbered invented planet names and a short fictional description for each. All information is synthetic and supplied by this instruction. No tools, missing context or decisions. Generate the entire response before finalizing.')
   except Exception as e:result_box['error']=type(e).__name__
  thread=threading.Thread(target=generate,daemon=True);thread.start();active=None
  deadline=time.monotonic()+30
  while time.monotonic()<deadline:
   with sqlite3.connect(dbpath) as db:
    rows=db.execute("SELECT body FROM runs WHERE status='RUNNING'").fetchall()
   for (body,) in rows:
    r=json.loads(body)
    if r.get('runtime_session_id') and r['objective'].startswith('Cancellation fixture.'):
     if api('/session/status').get(r['runtime_session_id'],{}).get('type')=='busy':active=r;break
   if active:break
   if not thread.is_alive():break
   time.sleep(.05)
  check('cancel_observed_running_and_runtime_busy',active is not None)
  result=other.call('ai_worker_cancel',{'worker_run_id':active['worker_run_id']});record(result);thread.join(timeout=15)
  check('cancel_completed_call',not thread.is_alive())
  terminal=run_record(active['worker_run_id']);status=api('/session/status').get(active['runtime_session_id'],{}).get('type','idle')
  check('cancelled_durable_and_abort_idle',result['status']=='cancelled' and terminal['status']=='CANCELLED' and status=='idle')
  check('active_call_cancelled',result_box.get('result',{}).get('status')=='cancelled')
  report['status']='PASS' if all(report['checks'].values()) else 'FAIL'
 except Exception as e:
  report['failure']=str(e) if isinstance(e,RuntimeError) and str(e) in report['checks'] else type(e).__name__
 finally:
  for m in reversed(clients):m.close()
  try:report['owner_shutdown_status']=instance()['status']
  except Exception:report['owner_shutdown_status']='UNKNOWN'
  path=pathlib.Path(a.report);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2));print(json.dumps(report))
if __name__=='__main__':main()
