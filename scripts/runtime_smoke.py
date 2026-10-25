#!/usr/bin/env python3
"""Real OpenCode -> actual MCP process smoke. No fake model, repository, or UI.
Provide an already configured OpenCode server and a discovered agent name.
Only synthetic text is sent. Provider/model configuration is owned by OpenCode.
"""
import argparse,json,pathlib,queue,sqlite3,subprocess,tempfile,threading,time,urllib.request,urllib.parse
class MCP:
 def __init__(self,binary,directory):
  self.p=subprocess.Popen([str(binary),'--config-dir',str(directory)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
  self.q=queue.Queue();self.sequence=0;self.progress=[];self.stderr=[]
  def read():
   for line in self.p.stdout:
    try:self.q.put(json.loads(line))
    except ValueError:self.q.put({'parse_error':True})
  threading.Thread(target=read,daemon=True).start()
  def read_stderr():
   for line in self.p.stderr:self.stderr.append(line.rstrip())
  threading.Thread(target=read_stderr,daemon=True).start()
  self.initialize_result=self.request('initialize',{'protocolVersion':'2025-11-25','clientInfo':{'name':'llmtools-real-smoke','version':'1'},'capabilities':{}})
  self.send({'jsonrpc':'2.0','method':'notifications/initialized'})
 def send(self,v):self.p.stdin.write(json.dumps(v)+'\n');self.p.stdin.flush()
 def request(self,method,params,timeout=210):
  self.sequence+=1;ident=self.sequence;self.send({'jsonrpc':'2.0','id':ident,'method':method,'params':params});end=time.monotonic()+timeout
  while time.monotonic()<end:
   item=self.q.get(timeout=max(.1,end-time.monotonic()))
   if item.get('method')=='notifications/progress':
    try:self.progress.append(json.loads(item['params']['message']))
    except ValueError:raise RuntimeError('invalid normalized progress')
   if item.get('id')==ident:
    if 'error' in item:raise RuntimeError('MCP protocol error')
    return item['result']
  raise TimeoutError('MCP timeout')
 def call(self,name,args):return self.request('tools/call',{'name':name,'arguments':args,'_meta':{'progressToken':f'smoke-{self.sequence}'}})['structuredContent']
 def close(self):
  self.p.stdin.close()
  try:self.p.wait(timeout=10)
  except subprocess.TimeoutExpired:self.p.terminate();self.p.wait(timeout=10)
  self.p.stdout.close()
  self.p.stderr.close()

def main():
 p=argparse.ArgumentParser();p.add_argument('--url',default='http://127.0.0.1:4096');p.add_argument('--agent',required=True);p.add_argument('--binary',default='bin/ai-worker-mcp');p.add_argument('--report',default='.smoke/opencode/report.json');p.add_argument('--directory');a=p.parse_args()
 directory=pathlib.Path(a.directory or tempfile.mkdtemp(prefix='llmtools-real-'));directory.mkdir(parents=True,exist_ok=True)
 (directory/'config.toml').write_text('[general]\nlog_level="info"\ntask_timeout_seconds=90\nmax_context_bytes=1048576\n[runtime]\ntype="opencode"\nmode="external"\nbase_url='+json.dumps(a.url)+'\n[agents]\nimplementation='+json.dumps(a.agent)+'\n[storage]\ndatabase="llmtools.db"\n')
 report={'runtime':'opencode','directory':str(directory),'backend':'REAL','status':'ENVIRONMENT_BLOCKED','manual_desktop':'MANUAL_DESKTOP_ACCEPTANCE_PENDING','checks':{},'progress':[]}
 m=None
 try:
  health=json.load(urllib.request.urlopen(a.url+'/global/health',timeout=10));report['version']=health.get('version');report['checks']['health']=health.get('healthy') is True
  agents=json.load(urllib.request.urlopen(a.url+'/agent?directory='+urllib.parse.quote(str(directory/'runtime-workspace'),safe=''),timeout=10));report['checks']['agent_discovered']=any(x.get('name')==a.agent for x in agents)
  m=MCP(pathlib.Path(a.binary).resolve(),directory);report['checks']['mcp_tools']=[x['name'] for x in m.request('tools/list',{})['tools']]
  normal=m.call('delegate_implementation',{'prompt':'Reply exactly: LLMTOOLS_PONG'})
  report['normal']={k:normal[k] for k in ['worker_run_id','status','content','error'] if k in normal};report['checks']['normal_completed']=normal['status']=='completed' and normal['content']=='LLMTOOLS_PONG';report['progress']+=m.progress
  # Even if the first model call fails, preserve precise evidence instead of substituting a mock.
  if not report['checks']['normal_completed']:
   report['blocked_reason']=normal.get('error',{}).get('code','NORMAL_RESULT_MISMATCH');report['status']='ENVIRONMENT_BLOCKED' if report['blocked_reason'] in ['RUNTIME_UNAVAILABLE','RUNTIME_NOT_FOUND','RUNTIME_START_FAILED','TIMEOUT'] else 'FAIL';return
  first=m.call('delegate_implementation',{'prompt':'Context escalation fixture. Tell me the exact value of the private acceptance token in contract.txt. Its contents have deliberately not been supplied. Request contract.txt from Codex using request_context; do not guess. After the file is supplied, include its token in your final response.'})
  report['pending']={k:first[k] for k in ['worker_run_id','status','error'] if k in first};report['checks']['needs_context']=first['status']=='needs_context'
  if not report['checks']['needs_context']:report['blocked_reason']='CONTEXT_ESCALATION_NOT_PRODUCED';report['status']='FAIL';return
  with sqlite3.connect(directory/'llmtools.db') as db:before=json.loads(db.execute('SELECT body FROM runs WHERE id=?',(first['worker_run_id'],)).fetchone()[0])['runtime_session_id']
  report['progress']+=m.progress;m.close();m=MCP(pathlib.Path(a.binary).resolve(),directory)
  answers=[{'request_id':r['id'],'type':'file','path':r['target'],'content':'acceptance token: LLMTOOLS-CONTINUATION-OK'} for r in first['context_requests']]
  result=m.call('ai_worker_continue',{'worker_run_id':first['worker_run_id'],'additional_context':answers})
  with sqlite3.connect(directory/'llmtools.db') as db:after=json.loads(db.execute('SELECT body FROM runs WHERE id=?',(first['worker_run_id'],)).fetchone()[0])['runtime_session_id']
  report['continuation']={k:result[k] for k in ['worker_run_id','status','content','error'] if k in result};report['checks']['same_session']=before==after;report['checks']['cross_process_continuation']=result['status']=='completed' and 'LLMTOOLS-CONTINUATION-OK' in result['content'];report['progress']+=m.progress
  if report['checks']['cross_process_continuation'] and before==after:report['status']='PASS'
  else:report['status']='FAIL';report['blocked_reason']='CONTINUATION_ACCEPTANCE_FAILED'
 except Exception as e:report['blocked_reason']=type(e).__name__
 finally:
  if m:m.close()
  path=pathlib.Path(a.report);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,indent=2));print(json.dumps({'status':report['status'],'report':str(path),'blocked_reason':report.get('blocked_reason')}))
if __name__=='__main__':main()
