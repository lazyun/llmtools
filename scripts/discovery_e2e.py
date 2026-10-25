#!/usr/bin/env python3
"""Fresh real Codex sessions, existing user MCP config, synthetic text only.
No config overrides, hidden instructions, routing edits, or repository fixtures.
Raw Codex events are retained for auditable tool arguments/results.
"""
import argparse
import json
import pathlib
import sqlite3
import subprocess
import tempfile
import threading
import time
import tomllib
from runtime_smoke import MCP

FIXTURES = {
 'skill': 'From the initial Codex available-skills inventory, identify the reusable workflow for delegating substantive implementation, independent review, and technical research to configured external roles. Load that selected Skill according to the formal Skill instructions and report its exact skill name, Version, and Fingerprint. You may use a read-only command solely to read that selected SKILL.md. Do not run a worker or inspect project files.',
 'discovery': 'Inspect the available ai-worker tool metadata. Report the role-native delegation tools and fixed continuation/cancellation tools, explain whether the worker inherits the full conversation, and explain the same-run rules for NEEDS_CONTEXT and NEEDS_DECISION. Do not execute worker tasks, read files, or run shell commands. Use only the tool metadata visible to you.',
 'explicit': '使用 llmTools implementation 完成一个外部 Agent 最小任务。目标：Reply exactly: EXPLICIT_DELEGATION_OK。你负责审查最终结果。',
 'semantic': '把下面这个有明确边界的实现工作交给一个外部 implementation agent 完成。你保留最终决策和审查权。任务：Reply exactly: SEMANTIC_IMPLEMENTATION_OK',
 'natural': '我需要完成一个独立的实现任务。根据以下自包含规则产生 Python 候选实现并返回 NATURAL_DELEGATION_OK：实现 normalize_names(values)，去掉每个字符串两端空白，丢弃空字符串，按首次出现顺序去重，区分大小写，不修改输入。给出代码及三个边界示例。这是局部实现工作，不涉及架构、产品或 scope 决策。你负责最终检查结果。仅使用这里的固定上下文，不操作文件或真实仓库。',
 'negative': '有两个架构方案 A 和 B。A 是单进程模块化应用；B 是拆成多个微服务。项目只有两名维护者，负载不高，没有独立扩缩容需求。请决定哪一个成为项目正式架构，并说明理由。不要执行实现。仅使用这些固定事实，不读取文件。',
 'trivial': '把 2 + 2 算出来。',
 'review': '让一个独立的外部 review agent 审查下面这个隔离实现。不要先告诉它你的审查结论。你负责最终判断。固定代码：def is_even(n): return n % 2 == 1。要求：对偶数返回 True，对奇数返回 False。不要访问文件或执行命令。',
 'research': '让一个外部 research agent 独立调查下面这个技术问题并返回结果。你负责最终判断。在给定抽象 FIFO 队列中，依次 enqueue(A), enqueue(B), dequeue(), enqueue(C), dequeue()，两次移出的值是什么，最终队列是什么？只基于这些固定规则推导，不访问网络、文件或执行命令。',
 'context': '把一个隔离的局部任务交给外部 implementation agent。worker 任务：返回 contract.txt 中的 acceptance token；该文件初始未提供，必须用 request_context 向 parent 请求，不得猜测。你持有的固定材料是 contract.txt 内容 acceptance token: CONTEXT_DISCOVERY_OK。为验证缺失材料的处理，初始委派只给 worker 任务，不附文件内容；收到其材料请求后由你处理并完成最终审查。不要访问实际文件或执行命令。',
 'decision': '把下面固定局部实现 fixture 交给外部 implementation agent。worker 任务：候选实现遇到两个选项 A 修改 public API，B 保持 API 加内部 adapter。两个方案均未授权，worker 必须立即用 native question 向 parent 请求选择，不可自行选择；得到选择后给出候选方案并包含 DECISION_DISCOVERY_OK。你作为 parent 按保持向后兼容的原则做最终决定并审查结果。不要访问实际文件或执行命令。',
}

def data_of(item):
 r=item.get('result') or {}
 data=r.get('structuredContent') or r.get('structured_content')
 if isinstance(data,dict): return data
 for b in r.get('content',[]):
  if b.get('type')=='text':
   try:
    data=json.loads(b['text'])
    if isinstance(data,dict): return data
   except (ValueError,KeyError): pass
 return {}

def run_session(name,prompt,out,dbpath):
 work=tempfile.mkdtemp(prefix='llmtools-discovery-')
 cmd=['codex','exec','--ephemeral','--skip-git-repo-check','--sandbox','read-only','--json','-C',work]
 for tool in ['delegate_implementation','delegate_review','delegate_research','ai_worker_continue','ai_worker_cancel']:
  cmd += ['-c','mcp_servers.ai-worker.tools.'+tool+'.approval_mode="approve"']
 cmd.append(prompt)
 snapshots={}; stop=threading.Event()
 def observe():
  while not stop.wait(.1):
   try:
    with sqlite3.connect('file:'+str(dbpath)+'?mode=ro',uri=True,timeout=1) as db:
     for (body,) in db.execute('SELECT body FROM runs'):
      r=json.loads(body)
      v={k:r.get(k) for k in ['status','runtime_session_id','runtime_agent','runtime_model','routing_revision','runtime_identity','capabilities']}
      arr=snapshots.setdefault(r['worker_run_id'],[])
      if not arr or arr[-1]!=v: arr.append(v)
   except (sqlite3.Error,OSError): pass
 observer=threading.Thread(target=observe,daemon=True);observer.start()
 report={'prompt':prompt,'command':cmd[:-1], 'tools':[], 'messages':[]}
 try:
  r=subprocess.run(cmd,capture_output=True,text=True,timeout=300)
  (out/(name+'.jsonl')).write_text(r.stdout)
  # stderr contains startup notices, keep only inventory lines (not upstream errors).
  report['inventory']=[line for line in r.stderr.splitlines() if line.startswith('mcp:')]
  report['exit_code']=r.returncode
  for line in r.stdout.splitlines():
   try: e=json.loads(line)
   except ValueError: continue
   item=e.get('item') or {}
   if e.get('type')=='item.completed':
    if item.get('type')=='mcp_tool_call': report['tools'].append({'server':item.get('server'),'tool':item.get('tool'),'arguments':item.get('arguments'),'result':data_of(item),'error':item.get('error')})
    elif item.get('type')=='agent_message': report['messages'].append(item.get('text'))
  ids={t['result'].get('worker_run_id') for t in report['tools']}
  report['run_snapshots']={k:v for k,v in snapshots.items() if k in ids}
 except subprocess.TimeoutExpired:
  report['error']='CODEX_TIMEOUT'
 finally:
  stop.set();observer.join(timeout=2)
 report['acceptance']=evaluate(name,report)
 (out/(name+'.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2))
 print(json.dumps({'fixture':name,'exit_code':report.get('exit_code'),'tools':[{ 'tool':t['tool'],'status':t['result'].get('status'),'error':t['result'].get('error')} for t in report['tools']]}),flush=True)
 return report

def evaluate(name, report):
 tools=report.get('tools',[])
 workers=[t for t in tools if str(t.get('tool','')).startswith('delegate_')]
 if name in ['natural','negative','trivial']:
  if name=='natural': return 'AUTO_DELEGATION_OBSERVED' if workers else 'AUTO_DELEGATION_NOT_OBSERVED'
  return 'PASS' if not workers and report.get('exit_code')==0 else 'FAIL'
 if any(t.get('error') for t in tools): return 'TOOL_EXECUTION_BLOCKED'
 if report.get('exit_code')!=0: return 'CODEX_FAILED'
 if name=='skill':
  text='\n'.join(report.get('messages',[]))
  required=['llmtools-delegation','1.4.0','llmtools-delegation-v5']
  return 'PASS' if not tools and all(value in text for value in required) else 'FAIL'
 if name=='discovery': return 'OBSERVATION_ONLY'
 if name=='cancel':
  return 'PASS' if len(tools)==1 and tools[0].get('tool')=='ai_worker_cancel' and tools[0].get('result',{}).get('status')=='cancelled' else 'FAIL'
 if len(workers)!=1: return 'FAIL'
 if 'capabilities' in (workers[0].get('arguments') or {}): return 'CALLER_CAPABILITIES_REGRESSION'
 role={'review':'review','research':'research'}.get(name,'implementation')
 if workers[0].get('tool')!='delegate_'+role: return 'FAIL'
 result=tools[-1].get('result',{})
 rid=workers[0].get('result',{}).get('worker_run_id')
 snapshots=report.get('run_snapshots',{}).get(rid,[])
 if not snapshots or not isinstance(snapshots[-1].get('capabilities'),dict) or any(snapshots[-1]['capabilities'].values()): return 'CAPABILITY_SNAPSHOT_MISSING'
 if role in ['review','research'] and (result.get('error') or {}).get('code')=='AGENT_MAPPING_NOT_FOUND': return 'ROLE_SELECTED_MAPPING_NOT_CONFIGURED'
 tokens={'explicit':'EXPLICIT_DELEGATION_OK','semantic':'SEMANTIC_IMPLEMENTATION_OK','context':'CONTEXT_DISCOVERY_OK','decision':'DECISION_DISCOVERY_OK'}
 content_ok=bool(result.get('content')) if role in ['review','research'] else tokens[name] in result.get('content','')
 if result.get('status')!='completed' or not content_ok: return 'FAIL'
 if name in ['explicit','semantic'] and result.get('content')!=tokens[name]: return 'FAIL'
 if role=='implementation' and (result.get('agent')!='build' or result.get('model')!='omni_router/gpt-5.6-sol'): return 'ROUTING_REGRESSION'
 if name in ['context','decision']:
  initial=workers[0]['result'];rid=initial.get('worker_run_id')
  if initial.get('status')!='needs_'+name or len(tools)!=2 or tools[1].get('tool')!='ai_worker_continue': return 'FAIL'
  if (tools[1].get('arguments') or {}).get('worker_run_id')!=rid or result.get('worker_run_id')!=rid: return 'FAIL'
  snapshots=report.get('run_snapshots',{}).get(rid,[])
  pending=[v for v in snapshots if v.get('status')=='NEEDS_'+name.upper()]
  completed=[v for v in snapshots if v.get('status')=='COMPLETED']
  if not pending or not completed: return 'SNAPSHOT_EVIDENCE_MISSING'
  for key in ['runtime_session_id','runtime_agent','runtime_model','routing_revision','runtime_identity']:
   if not pending[-1].get(key) or pending[-1].get(key)!=completed[-1].get(key): return 'SNAPSHOT_MISMATCH'
  capability_snapshot=pending[-1].get('capabilities')
  if not isinstance(capability_snapshot,dict) or any(capability_snapshot.values()) or capability_snapshot!=completed[-1].get('capabilities'): return 'CAPABILITY_SNAPSHOT_MISMATCH'
 return 'PASS'

def main():
 p=argparse.ArgumentParser();p.add_argument('--only',nargs='*',choices=list(FIXTURES)+['cancel']);p.add_argument('--output',default='.smoke/discovery');a=p.parse_args()
 out=pathlib.Path(a.output).resolve();out.mkdir(parents=True,exist_ok=True)
 config=tomllib.loads((pathlib.Path.home()/'.codex/config.toml').read_text())['mcp_servers']['ai-worker']
 args=config['args'];directory=pathlib.Path(args[args.index('--config-dir')+1]);c=tomllib.loads((directory/'config.toml').read_text());dbpath=directory/c['storage']['database']
 (out/'environment.json').write_text(json.dumps({'binary':config['command'],'config_dir':str(directory),'routing':c['agents'],'runtime':{k:c['runtime'].get(k) for k in ['mode','port']},'config_overrides':False},indent=2))
 for name in a.only or FIXTURES:
  if name!='cancel': run_session(name,FIXTURES[name],out,dbpath)
  else:
   m=MCP(pathlib.Path(config['command']),directory);box={}
   objective='Discovery cancellation fixture. Produce 900 numbered invented planet names with a fictional paragraph for each. No tools or missing context. Complete the entire list before finalizing.'
   def generate(): box['result']=m.call('delegate_implementation',{'prompt':objective})
   thread=threading.Thread(target=generate,daemon=True);thread.start();active=None
   for _ in range(600):
    with sqlite3.connect('file:'+str(dbpath)+'?mode=ro',uri=True) as db:
     for (body,) in db.execute("SELECT body FROM runs WHERE status='RUNNING'"):
      r=json.loads(body)
      if r.get('objective')==objective and r.get('runtime_session_id'): active=r
    if active or not thread.is_alive(): break
    time.sleep(.05)
   try:
    if active:
     run_session(name,'取消刚才的外部 worker。它的运行标识是 '+active['worker_run_id']+'。不要读取文件或执行命令。',out,dbpath)
     thread.join(timeout=20)
     (out/'cancel-origin.json').write_text(json.dumps({'initial_status':active['status'],'worker_run_id':active['worker_run_id'],'result':box.get('result')},indent=2))
    else: (out/'cancel-origin.json').write_text(json.dumps({'error':'RUNNING_NOT_OBSERVED','result':box.get('result')}))
   finally:
    if thread.is_alive() and active:
     cleanup=MCP(pathlib.Path(config['command']),directory)
     try: cleanup.call('ai_worker_cancel',{'worker_run_id':active['worker_run_id']})
     finally: cleanup.close()
     thread.join(timeout=20)
    m.close()
    (out/'cancel-cleanup.json').write_text(json.dumps({'worker_run_id':active.get('worker_run_id') if active else None,'result':box.get('result'),'thread_finished':not thread.is_alive()},indent=2))
if __name__=='__main__': main()
