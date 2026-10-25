#!/usr/bin/env python3
"""Real Codex CLI -> llmTools MCP -> configured OpenCode, synthetic context only."""
import argparse,json,pathlib,sqlite3,subprocess,tempfile,time,re,tomllib,threading
from routing_smoke import save_route, TERRA, SOL

ERROR_MESSAGES = {'AGENT_MAPPING_NOT_FOUND': 'No OpenCode routing is configured for this logical role.', 'MODEL_NOT_AVAILABLE': 'The explicitly routed OpenCode model is not currently available.', 'PENDING_DECISION_LOST': 'The pending runtime question is no longer available; execution was not replayed.', 'PENDING_CONTEXT_LOST': 'The pending runtime context request is no longer available; execution was not replayed.', 'QUESTION_TOOL_UNAVAILABLE': 'Native question is unavailable or not allowed; check runtime question diagnostics.', 'CONTEXT_TOOL_UNAVAILABLE': 'Native context control is unavailable or not allowed; check runtime context diagnostics.', 'EMPTY_AGENT_RESULT': 'Runtime completed without a usable final assistant response.', 'CONFIG_ERROR': 'Configuration is invalid or a legacy request exceeds server policy.', 'ROLE_CAPABILITY_POLICY_INVALID': 'The configured role execution policy is invalid.', 'RUNTIME_NOT_FOUND': 'OpenCode executable was not found.', 'RUNTIME_UNAVAILABLE': 'Runtime is unavailable; check runtime diagnostics.', 'RUNTIME_START_FAILED': 'Managed runtime could not become ready.', 'RUNTIME_PROTOCOL_ERROR': 'Runtime response or event protocol was invalid.', 'AGENT_NOT_FOUND': 'The routed OpenCode agent is not currently available.', 'SESSION_CREATE_FAILED': 'Runtime session could not be created.', 'SESSION_LOST': 'Stored runtime session is no longer available.', 'CONTINUATION_INVALID': 'Run is unknown, busy, terminal, or not awaiting these responses.', 'INVALID_CONTEXT_RESPONSE': 'Context responses must exactly answer the current pending context requests.', 'INVALID_DECISION_RESPONSE': 'Decision responses must exactly answer current requests using an offered selection.', 'TIMEOUT': 'Task exceeded its deadline.', 'CANCELLED': 'Task was cancelled.', 'INVALID_AGENT_RESULT': 'Runtime final result does not satisfy the communication contract.', 'INTERNAL_ERROR': 'Local operation failed; check diagnostics.', 'ABORT_UNCONFIRMED': 'Runtime abort could not be confirmed; task is failed, not reported as safely cancelled. Stop or inspect the runtime.'}

def tool_diagnostic(item):
 result=item.get('result') or {}
 if not isinstance(result,dict):result={}
 data=result.get('structuredContent') or result.get('structured_content') or {}
 if not isinstance(data,dict):data={}
 for content in result.get('content',[]):
  if not data and isinstance(content,dict) and content.get('type')=='text':
   try:data=json.loads(content.get('text',''))
   except (ValueError,TypeError):pass
 if not isinstance(data,dict):data={}
 error=data.get('error') or {};error=error if isinstance(error,dict) else {}
 code=error.get('code');code=code if code in ERROR_MESSAGES else None
 run=data.get('worker_run_id');run=run if isinstance(run,str) and re.fullmatch(r'wrk_[0-9a-f]{32}',run) else None
 explicit=result.get('isError',result.get('is_error'))
 # Codex may omit the wire bit; infer only from a recognized structured status.
 inferred=data.get('status') in ['completed','needs_context','needs_decision','cancelled','failed']
 is_error=bool(explicit) if explicit is not None else (data['status']=='failed' if inferred else None)
 return {'server':item.get('server'),'tool':item.get('tool'),'status':item.get('status'),'tool_result_is_error':is_error,'is_error_source':'wire' if explicit is not None else ('structured_result_status' if inferred else 'unavailable'),'error_code':code,'message':ERROR_MESSAGES.get(code,'Tool transport failed; details omitted.' if item.get('error') else None),'worker_run_id':run,'transport_error':bool(item.get('error'))}

def stderr_summary(stderr):
 # Do not echo stderr: it may contain upstream payloads or credentials.
 return {'bytes':len(stderr.encode()),'lines':len(stderr.splitlines()),'categories':[label for pattern,label in [(r'401|unauthorized','authentication'),(r'timeout|timed out','timeout'),(r'failed to initialize|handshake','mcp_startup'),(r'connection refused','connection_refused')] if re.search(pattern,stderr,re.I)]}


def main():
 p=argparse.ArgumentParser();p.add_argument('--config-dir',required=True);p.add_argument('--binary',default='bin/ai-worker-mcp');p.add_argument('--report',default='.smoke/opencode/codex-e2e.json');p.add_argument('--routing-change',action='store_true');a=p.parse_args();directory=pathlib.Path(a.config_dir).resolve();binary=pathlib.Path(a.binary).resolve();work=tempfile.mkdtemp(prefix='llmtools-codex-e2e-');start=time.time();cmd=['codex','exec','--ignore-user-config','--ephemeral','--skip-git-repo-check','--sandbox','read-only','--json','-C',work]
 overrides={'approval_policy':'never','mcp_servers.ai-worker.command':str(binary),'mcp_servers.ai-worker.args':['--config-dir',str(directory)],'mcp_servers.ai-worker.tool_timeout_sec':120}
 for tool in ['delegate_implementation','delegate_review','delegate_research','ai_worker_continue','ai_worker_cancel']:overrides['mcp_servers.ai-worker.tools.'+tool+'.approval_mode']='approve'
 for k,v in overrides.items():cmd+=['-c',k+'='+json.dumps(v)]
 prompt='''Run three actual MCP integration checks. Use only ai-worker MCP tools, no shell, no files, no web and no other agents. Do not configure or inspect providers. Use delegate_implementation without requesting execution capabilities.
1. Call delegate_implementation with prompt: Reply exactly: LLMTOOLS_PONG
2. Independently call delegate_implementation with prompt: Context escalation fixture. Tell me the exact private acceptance token in contract.txt. The file is deliberately missing from supplied context. Request it from Codex using request_context and do not guess. After receiving the file, include the token in the final response.
If check 2 returns needs_context, call ai_worker_continue using its worker_run_id. Because one request is pending, omit request_id. Supply type=file, requested path, content=acceptance token: LLMTOOLS-CONTINUATION-OK. Never start a replacement task to answer a pending request.
3. Independently call delegate_implementation with this prompt: There are exactly two valid choices: A changes the public API; B preserves the public API and adds an internal adapter. No option is authorized. You are forbidden from choosing. Request a parent decision immediately with the native question tool. After the reply, include PRESERVE_API_WITH_ADAPTER in the final response when B is selected.
If check 3 returns needs_decision, choose the option that preserves the public API and call ai_worker_continue using the same worker_run_id. Because one request is pending, omit request_id. Never start a replacement task.
If a tool fails, report its stable error code without retrying or claiming success. Perform all initial checks even if an earlier check fails. Finish with a concise factual report; no self-reported success without tool evidence.'''
 if a.routing_change:
  save_route(directory/'config.toml',TERRA)
  prompt='''Run three sequential actual ai-worker MCP checks. Use no shell, files, web, or other agents. Use delegate_implementation without requesting execution capabilities.
1. Call delegate_implementation with prompt: Reply exactly: ROUTING_CODEX_TERRA_OK
2. Call delegate_implementation with prompt: Request contract.txt using request_context. Do not guess its missing token. After Codex supplies it, include ROUTING_CODEX_SNAPSHOT_OK in the final response.
When check 2 returns needs_context, call ai_worker_continue on that exact worker_run_id without request_id, with type=file, the requested path, and content=ROUTING_CODEX_SNAPSHOT_OK. Never create a replacement run.
3. Only after check 2 completes, call delegate_implementation with prompt: Reply exactly: ROUTING_CODEX_SOL_OK
Report each tool status and model metadata. Do not retry failures or claim success without tool evidence.'''
 with open(directory/'config.toml','rb') as f:database=tomllib.load(f).get('storage',{}).get('database','llmtools.db')
 observations=[];stop=threading.Event();routing_changed=[False]
 def observe():
  while not stop.is_set():
   try:
    with sqlite3.connect('file:'+str(directory/database)+'?mode=ro',uri=True,timeout=1) as db:
     row=db.execute('SELECT body FROM runtime_instances ORDER BY generation DESC LIMIT 1').fetchone()
     if row:
      r=json.loads(row[0]);v={k:r.get(k) for k in ['runtime_instance_id','generation','status','base_url','owner_pid','pid']}
      # Process names only; never collect argv or environment.
      ps=subprocess.run(['ps','-axo','pid=,comm='],capture_output=True,text=True,timeout=3)
      v['opencode_process_count']=sum(1 for line in ps.stdout.splitlines() if line.strip().split(' ',1)[-1].strip().rsplit('/',1)[-1]=='opencode') if ps.returncode==0 else None
      if not observations or observations[-1]!=v:observations.append(v)
     if a.routing_change and not routing_changed[0]:
      for (body,) in db.execute("SELECT body FROM runs WHERE status='NEEDS_CONTEXT'"):
       pending=json.loads(body)
       if pending.get('objective','').startswith('Request contract.txt using request_context'):
        save_route(directory/'config.toml',SOL);routing_changed[0]=True;break
   except (sqlite3.Error,OSError,ValueError,subprocess.TimeoutExpired):pass
   stop.wait(.05 if a.routing_change else .2)
 observer=threading.Thread(target=observe,daemon=True);observer.start()
 report={'backend' :'REAL_CODEX_REAL_OPENCODE','status':'ENVIRONMENT_BLOCKED','tools':[],'runs':[],'manual_desktop':'MANUAL_DESKTOP_ACCEPTANCE_PENDING'}
 try:
  result=subprocess.run(cmd+[prompt],capture_output=True,text=True,timeout=240)
  report['exit_code']=result.returncode
  report['stderr_summary']=stderr_summary(result.stderr)
  for line in result.stdout.splitlines():
   try:event=json.loads(line)
   except ValueError:continue
   item=event.get('item') or {}
   if item.get('type')=='mcp_tool_call' and event.get('type')=='item.completed':report['tools'].append(tool_diagnostic(item))
  with open(directory/'config.toml','rb') as f:database=tomllib.load(f).get('storage',{}).get('database','llmtools.db')
  with sqlite3.connect('file:'+str(directory/database)+'?mode=ro',uri=True) as db:
   for (body,) in db.execute('SELECT body FROM runs'):
    r=json.loads(body)
    from datetime import datetime
    if datetime.fromisoformat(r['created'].replace('Z','+00:00')).timestamp()<start:continue
    content=r.get('result',{}).get('content','')
    report['runs'].append({'worker_run_id':r['worker_run_id'],'status':r['status'],'runtime_agent':r.get('runtime_agent'),'runtime_model':r.get('runtime_model'),'objective':r.get('objective'),'error_code':(r.get('result',{}).get('error')or{}).get('code'),'pong_verified':content=='LLMTOOLS_PONG','context_token_verified':'LLMTOOLS-CONTINUATION-OK' in content,'decision_verified':'PRESERVE_API_WITH_ADAPTER' in content,'routing_terra_verified':'ROUTING_CODEX_TERRA_OK' in content,'routing_snapshot_verified':'ROUTING_CODEX_SNAPSHOT_OK' in content,'routing_sol_verified':'ROUTING_CODEX_SOL_OK' in content})
  names=[x['tool'] for x in report['tools']]
  if a.routing_change:
   terra=any(r['routing_terra_verified'] and r['runtime_agent']=='build' and r['runtime_model']==TERRA for r in report['runs'])
   snapshot=any(r['routing_snapshot_verified'] and r['runtime_agent']=='build' and r['runtime_model']==TERRA for r in report['runs'])
   sol=any(r['routing_sol_verified'] and r['runtime_agent']=='build' and r['runtime_model']==SOL for r in report['runs'])
   report['routing_changed_while_suspended']=routing_changed[0]
   if result.returncode==0 and terra and snapshot and sol and routing_changed[0] and names.count('delegate_implementation')>=3 and names.count('ai_worker_continue')>=1 and len(report['runs'])==3 and all(r['status']=='COMPLETED' for r in report['runs']):report['status']='PASS'
   else:report['blocked_reason']='RUNTIME_FAILURE_OR_INCOMPLETE_ROUTING_SEQUENCE'
  elif result.returncode==0 and any(r['pong_verified'] for r in report['runs']) and any(r['context_token_verified'] for r in report['runs']) and any(r['decision_verified'] for r in report['runs']) and names.count('delegate_implementation')>=3 and names.count('ai_worker_continue')>=2 and len(report['runs'])==3 and all(r['status']=='COMPLETED' for r in report['runs']):report['status']='PASS'
  else:report['blocked_reason']='RUNTIME_FAILURE_OR_INCOMPLETE_TOOL_SEQUENCE'
 except subprocess.TimeoutExpired as e:
  report['exit_code']=None;report['blocked_reason']='CODEX_TIMEOUT';report['stderr_summary']=stderr_summary(e.stderr.decode(errors='replace') if isinstance(e.stderr,bytes) else e.stderr or '')
 except Exception as e:report['blocked_reason']=type(e).__name__
 stop.set();observer.join(timeout=5);report['runtime_observations']=observations
 path=pathlib.Path(a.report);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2));print(json.dumps(report))
if __name__=='__main__':main()
