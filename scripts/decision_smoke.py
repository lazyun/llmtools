#!/usr/bin/env python3
"""Real native-question acceptance against the existing managed configuration.
A long-lived Desktop backend must own the server for MCP process-restart checks.
Never prints runtime question IDs, transcripts or provider configuration.
"""
import argparse,json,pathlib,sqlite3,time,tomllib
from runtime_smoke import MCP
MINIMAL='''There are exactly two valid implementation choices:
A. Change the public API.
B. Preserve the public API and add an internal adapter.
No option has been authorized. You are forbidden from choosing between A and B yourself.
Do not perform any other work. Request a parent decision immediately using the native question tool.
After the parent selects B, include PRESERVE_API_WITH_ADAPTER in the final response.'''
IMPLEMENTATION='''Draft an advisory implementation plan from supplied context only. Existing clients call lookup(id) and expect a string. New internal code needs a record containing value and metadata. Two scope choices exist: A changes the public lookup return type; B preserves lookup(id) and adds an internal adapter. Neither is authorized. This public API compatibility decision belongs to Codex: ask with native question before choosing or drafting. After the reply, give a brief advisory plan following the parent's choice. Do not read/write files or use shell. If B was selected, include PRESERVE_API_WITH_ADAPTER in the final response.'''
def main():
 p=argparse.ArgumentParser();p.add_argument('--config-dir',required=True);p.add_argument('--binary',default='bin/ai-worker-mcp');p.add_argument('--fixture',choices=['minimal','implementation'],default='minimal');p.add_argument('--report',default='.smoke/native-question/minimal.json');a=p.parse_args()
 directory=pathlib.Path(a.config_dir).resolve();binary=pathlib.Path(a.binary).resolve()
 with open(directory/'config.toml','rb') as f:cfg=tomllib.load(f)
 def stored(run):
  with sqlite3.connect(directory/cfg['storage']['database']) as db:return json.loads(db.execute('SELECT body FROM runs WHERE id=?',(run,)).fetchone()[0])
 report={'backend':'REAL_OPENCODE_NATIVE_QUESTION','fixture':a.fixture,'status':'FAIL','checks':{}};m=None
 try:
  m=MCP(binary,directory);start=time.monotonic();first=m.call('delegate_implementation',{'prompt':MINIMAL if a.fixture=='minimal' else IMPLEMENTATION})
  report['elapsed_to_decision_seconds']=round(time.monotonic()-start,3);report['worker_run_id']=first.get('worker_run_id');report['initial_status']=first.get('status');report['error']=first.get('error')
  assert first['status']=='needs_decision','native_question_not_produced'
  before=stored(first['worker_run_id']);private=before['runtime_decision'];private=json.loads(private) if isinstance(private,str) else private
  report['decision_request_ids']=[q['id'] for q in first['decision_requests']]
  report['checks']['promptly_suspended']=report['elapsed_to_decision_seconds']<75
  report['checks']['durable_pending']=before['status']=='NEEDS_DECISION' and bool(private['request']['id'])
  report['checks']['runtime_ids_not_public']=private['request']['id'] not in json.dumps(first) and before['runtime_session_id'] not in json.dumps(first)
  replies=[]
  for q in first['decision_requests']:
   chosen=next((o for o in q['options'] if 'preserv' in o['description'].lower()),None)
   assert chosen is not None,'preserve_option_missing'
   replies.append({'request_id':q['id'],'selection':chosen['id'],'explanation':'Codex selects B: preserve the public API and add an internal adapter.'})
  m.close();m=None
  # Waiting state outlives the execution call; a new MCP process resumes it.
  time.sleep(1)
  m=MCP(binary,directory);final=m.call('ai_worker_continue',{'worker_run_id':first['worker_run_id'],'decision_responses':replies})
  report['final_status']=final.get('status');report['final_error']=final.get('error');after=stored(first['worker_run_id'])
  report['checks']['same_run']=final.get('worker_run_id')==first['worker_run_id']
  report['checks']['same_session']=before['runtime_session_id']==after['runtime_session_id']
  report['checks']['mcp_process_restart']=True
  report['checks']['completed']=final['status']=='completed' and after['status']=='COMPLETED'
  report['checks']['parent_choice_applied']='PRESERVE_API_WITH_ADAPTER' in final.get('content','')
  report['checks']['pending_mapping_cleared']=not after.get('runtime_decision')
  report['status']='PASS' if all(report['checks'].values()) else 'FAIL'
 except Exception as e:report['failure']=str(e) if isinstance(e,AssertionError) else type(e).__name__
 finally:
  if m:m.close()
  path=pathlib.Path(a.report);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2));print(json.dumps(report))
if __name__=='__main__':main()
