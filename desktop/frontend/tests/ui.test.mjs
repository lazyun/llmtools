import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync,readdirSync} from 'node:fs';
import {JSDOM} from 'jsdom';
const bundle=readFileSync(new URL('../dist/assets/'+readdirSync(new URL('../dist/assets/',import.meta.url)).find(x=>x.endsWith('.js')),import.meta.url),'utf8');
const clone=x=>JSON.parse(JSON.stringify(x));
const skill={status:'installed',version:'1.4.0',fingerprint:'llmtools-delegation-v5',path:'~/.agents/skills/llmtools-delegation',expected_version:'1.4.0',expected_fingerprint:'llmtools-delegation-v5'};
const runtimeOwned={ownership:'OWNER',health:'PASS',instance:{runtime_instance_id:'rt_example',base_url:'http://127.0.0.1:55115',port:55115,pid:1234,generation:7,status:'READY',version:'1.18.31'}};
const workspaceRecord=(id,over={})=>({id,label:'project',canonical_root:'/tmp/project',state:'ACTIVE',revision:1,source:'desktop_user_grant',created_at:1750000000000,updated_at:1750000000000,...over});
const initial={directory:'/tmp/data',binary:'/tmp/mcp',runtime_owned:false,skill,runtime:{},workspaces:[],role_policies:{implementation:'Repository read-only',review:'Repository read-only',research:'Context-only'},config:{runtime:{type:'pi',mode:'',binary:'/tmp/pi',session_dir:'/tmp/pi-sessions',port:0},general:{log_level:'info',task_timeout_seconds:180,max_context_bytes:1048576},agents:{implementation:{exposed:true,agent:'fixture',model:'mock/terra'},review:{exposed:true,agent:'fixture',model:''},research:{exposed:false,agent:'fixture',model:''}},storage:{database:'llmtools.db'},warnings:[]},runs:[{worker_run_id:'wrk_test',runtime_session_id:'ses_private',runtime_agent:'fixture',runtime_model:'mock/terra',model_source:'explicit',logical_role:'implementation',agent:'implementation',runtime:'opencode',status:'NEEDS_CONTEXT',capabilities:{repoRead:true,repoWrite:false,shell:false,web:false,subagent:false},objective:'<img src=x onerror=alert(1)>',created:'2026-09-17T00:00:00Z',updated:'2026-09-17T00:01:00Z',result:{status:'needs_context',summary:'Missing file',agent:'fixture',model:'mock/terra',context_requests:[{id:'context_1',kind:'file',target:'api.go',reason:'Missing contract'}]}}]};
const wait=async fn=>{for(let n=0;n<200;n++){if(fn())return;await new Promise(r=>setTimeout(r,5))}throw Error('UI timed out')};
const dialogEl=u=>u.doc.getElementById('app-dialog');
const dialogTitle=u=>u.doc.getElementById('app-dialog-title').textContent;
const dialogBody=u=>u.doc.getElementById('app-dialog-body').textContent;
const dialogError=u=>u.doc.getElementById('app-dialog-error');
const dialogOk=u=>u.doc.getElementById('app-dialog-confirm');
const dialogCancel=u=>u.doc.getElementById('app-dialog-cancel');
const dialogInput=u=>u.doc.getElementById('app-dialog-input');
const dialogOpen=u=>dialogEl(u).hasAttribute('open');
const typeLabel=(u,value)=>{const input=dialogInput(u);input.value=value;input.dispatchEvent(new u.dom.window.Event('input',{bubbles:true}))};
const press=(u,target,key)=>target.dispatchEvent(new u.dom.window.KeyboardEvent('keydown',{key,bubbles:true}));
const openRename=(u,id)=>{u.doc.querySelector(`[data-rename-workspace="${id}"]`).click();return dialogEl(u)};
const openRevoke=(u,id)=>{u.doc.querySelector(`[data-revoke-workspace="${id}"]`).click();return dialogEl(u)};
const removedSection=(u)=>u.doc.getElementById('removed-workspaces');
const markDirty=(u,id='runtime-binary')=>{const input=u.doc.getElementById(id);input.value='changed';input.dispatchEvent(new u.dom.window.Event('input',{bubbles:true}))};
const passiveChecks=[{name:'Configuration',status:'PASS',message:''},{name:'Delegation Skill',status:'PASS',message:'Installed'},{name:'Delegation Skill version',status:'PASS',message:'1.4.0'},{name:'Delegation Skill fingerprint',status:'PASS',message:'llmtools-delegation-v5'},{name:'Role Execution Policy: implementation',status:'PASS',message:'Repository read-only'},{name:'Role Execution Policy: research',status:'PASS',message:'Context-only'},{name:'Repository Read Enforcement',status:'PASS',message:'read: enabled/scoped · glob: enabled/scoped · grep: enabled/scoped · write: denied · edit: denied · shell: denied · git command: denied · web: denied · subagent: denied. Every access is re-confined against the run\'s live workspace grant.'}];
const mcpChecks=[{name:'MCP initialize',status:'PASS',message:''},{name:'MCP tools',status:'PASS',message:'ai_worker_cancel, ai_worker_continue, delegate_implementation, delegate_review'},{name:'Codex role refresh',status:'CONFIGURED',message:'Reconnect/restart Codex after exposure changes.'}];
async function setup(overrides={},snapshot=clone(initial)){const calls=[];const dom=new JSDOM('<div id="app"></div>',{runScripts:'outside-only',url:'http://wails.localhost'});const api={State:async()=>clone(snapshot),InstallSkill:async()=>{calls.push(['install-skill']);snapshot.skill={...clone(skill),can_remove:true};return clone(snapshot.skill)},RemoveSkill:async()=>{calls.push(['remove-skill']);snapshot.skill={...clone(skill),status:'missing',version:'',fingerprint:'',can_remove:false};return clone(snapshot.skill)},SaveRuntime:async x=>calls.push(['runtime',x]),SaveSettings:async x=>calls.push(['settings',x]),SetBinary:async x=>calls.push(['binary',x]),DiscoverRouting:async()=>({agents:[{name:'fixture',mode:'primary',description:'Configured externally'}],models:[{id:'mock/terra',provider_id:'mock',model_id:'terra',display_name:'Terra'},{id:'mock/sol',provider_id:'mock',model_id:'sol',display_name:'Sol'}]}),SaveRoutings:async x=>calls.push(['routings',x]),DeleteMapping:async()=>{},StartRuntime:async()=>calls.push(['start']),StopRuntime:async()=>calls.push(['stop']),CodexConfig:async()=> '[mcp_servers.ai-worker]\ncommand="/tmp/mcp"',Diagnostics:async()=>{calls.push(['diagnostics']);const checks=clone(overrides.DiagnosticsResult||passiveChecks);if(!overrides.DiagnosticsResult){for(const check of checks){if(check.name==='Delegation Skill'){check.status=snapshot.skill.status==='installed'?'PASS':'FAIL';check.message=snapshot.skill.status==='installed'?'Installed':snapshot.skill.status==='outdated'?'Outdated':'Missing'}else if(check.name==='Delegation Skill version'){check.status=snapshot.skill.version===skill.expected_version?'PASS':'FAIL';check.message=snapshot.skill.version||'Missing'}else if(check.name==='Delegation Skill fingerprint'){check.status=snapshot.skill.fingerprint===skill.expected_fingerprint?'PASS':'FAIL';check.message=snapshot.skill.fingerprint||'Missing'}}}return checks},ProbeRuntime:async()=>{calls.push(['protocol']);return clone(overrides.ProbeRuntimeResult||[{name:'Pi protocol',status:'PASS',message:'Pi 1.0.2 · Offline RPC handshake verified'}])},ProbeMCP:async()=>{calls.push(['mcp']);return clone(overrides.ProbeMCPResult||mcpChecks)},AddWorkspace:async(...a)=>{calls.push(['add-workspace',a.length]);const w=workspaceRecord('ws_active');if((snapshot.workspaces||[]).some(x=>x.id===w.id))return{workspace:w,created:false};snapshot.workspaces=[...(snapshot.workspaces||[]),w];return{workspace:w,created:true}},RenameWorkspace:async(id,label)=>{calls.push(['rename-workspace',id,label]);const w=(snapshot.workspaces||[]).find(x=>x.id===id);if(w)w.label=label},RevokeWorkspace:async id=>{calls.push(['revoke-workspace',id]);const w=(snapshot.workspaces||[]).find(x=>x.id===id);if(w){w.state='REVOKED';w.revision++}},...overrides};dom.window.go={main:{App:api}};dom.window.runtime={ClipboardSetText:async x=>calls.push(['copy',x])};dom.window.eval(bundle);await wait(()=>dom.window.document.getElementById('save-runtime'));const doc=dom.window.document;const u={dom,doc,calls,nav:p=>doc.querySelector(`[data-nav="${p}"]`).click(),click:id=>doc.getElementById(id).click(),settle:async()=>await wait(()=>!doc.getElementById('app').hasAttribute('aria-busy'))};u.openIntegration=async()=>{u.nav('Integration');await wait(()=>calls.some(x=>x[0]==='mcp'));await u.settle()};return u;}

test('five neutral pages with Codex Integration and no MCP/Diagnostics tabs',async t=>{const u=await setup();t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});assert.deepEqual([...u.doc.querySelectorAll('[data-nav]')].map(x=>x.textContent),['Runtimes','Agents','Runs','Codex Integration','Settings']);assert.equal(u.calls.length,0);assert.equal(u.doc.getElementById('mode'),null);assert.equal(u.doc.getElementById('base-url'),null);for(const p of ['Agents','Runs','Settings']){u.nav(p);assert.equal(u.doc.querySelector('h1').textContent,p);assert.equal(u.doc.querySelector('input[type=password]'),null)}u.nav('Integration');assert.equal(u.doc.querySelector('h1').textContent,'Codex Integration');assert.doesNotMatch(bundle,/RevealKey|SaveProvider|SaveModel|api_key_env/)});

test('language switch translates converged navigation and pages',async t=>{const u=await setup();t.after(()=>u.dom.window.close());u.click('language-toggle');assert.equal(u.doc.documentElement.lang,'zh-CN');assert.equal(u.dom.window.localStorage.getItem('ai-worker-locale'),'zh-CN');assert.deepEqual([...u.doc.querySelectorAll('[data-nav]')].map(x=>x.textContent),['运行时','智能体','任务','Codex 集成','设置']);assert.equal(u.doc.querySelector('h1').textContent,'运行时');assert.match(u.doc.querySelector('.intro').textContent,/自动启动/);for(const [page,title,copy] of [['Agents','智能体','保存路由'],['Runs','任务','任务 / 目标'],['Settings','设置','保存设置']]){u.nav(page);assert.equal(u.doc.querySelector('h1').textContent,title);assert.match(u.doc.getElementById('content').textContent,new RegExp(copy))}u.nav('Integration');assert.equal(u.doc.querySelector('h1').textContent,'Codex 集成');await u.settle();u.click('language-toggle');assert.equal(u.doc.documentElement.lang,'en');assert.equal(u.dom.window.localStorage.getItem('ai-worker-locale'),'en');assert.equal(u.doc.querySelector('h1').textContent,'Codex Integration')});

test('runtime page is Pi-only without external controls and saves a cleared Pi config',async t=>{const u=await setup();t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});assert.equal(u.doc.getElementById('mode'),null);assert.equal(u.doc.getElementById('base-url'),null);assert.equal(u.doc.getElementById('port'),null);assert.equal(u.doc.getElementById('runtime-type'),null);assert.equal(u.doc.getElementById('start-runtime'),null);assert.equal(u.doc.getElementById('stop-runtime'),null);assert.equal(u.doc.getElementById('copy-url'),null);assert.equal(u.doc.getElementById('migrate-runtime'),null);assert.equal(u.doc.getElementById('runtime-instance'),null);assert.equal(u.doc.getElementById('runtime-binary').value,'/tmp/pi');assert.equal(u.doc.getElementById('session-dir').value,'/tmp/pi-sessions');assert.equal(u.doc.getElementById('save-runtime').disabled,false);assert.match(u.doc.querySelector('h1').nextElementSibling.textContent,/Pi starts automatically|No manual start/);u.click('save-runtime');await wait(()=>u.calls.some(x=>x[0]==='runtime'));const saved=u.calls.find(x=>x[0]==='runtime')[1];assert.equal(saved.type,'pi');assert.equal(saved.binary,'/tmp/pi');assert.equal(saved.session_dir,'/tmp/pi-sessions');assert.equal(saved.mode,'');assert.equal(saved.host,'');assert.equal(saved.base_url,'');assert.equal(saved.port,0)});

test('legacy OpenCode snapshots still render Pi-only configuration with no way to run OpenCode',async t=>{const snapshot=clone(initial);snapshot.runtime=runtimeOwned;snapshot.config.runtime.type='opencode';snapshot.config.runtime.mode='external';snapshot.config.runtime.base_url='http://localhost:4096';snapshot.config.warnings=['OpenCode is no longer supported; migrate to Pi.'];const u=await setup({},snapshot);t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});assert.equal(u.doc.getElementById('runtime-type'),null);assert.equal(u.doc.getElementById('mode'),null);assert.equal(u.doc.getElementById('base-url'),null);assert.equal(u.doc.getElementById('start-runtime'),null);assert.equal(u.doc.getElementById('stop-runtime'),null);assert.equal(u.doc.getElementById('migrate-runtime'),null);assert.equal(u.doc.getElementById('save-runtime').disabled,false);assert.match(u.doc.body.textContent,/migrate to Pi/);u.click('save-runtime');await wait(()=>u.calls.some(x=>x[0]==='runtime'));assert.equal(u.calls.find(x=>x[0]==='runtime')[1].type,'pi')});

test('legacy external OpenCode config renders the Pi form with no migration path or external controls',async t=>{const snapshot=clone(initial);snapshot.config.runtime.mode='external';snapshot.config.runtime.base_url='http://localhost:4096';snapshot.runtime_owned=false;const u=await setup({},snapshot);t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});assert.equal(u.doc.getElementById('mode'),null);assert.equal(u.doc.getElementById('base-url'),null);assert.equal(u.doc.getElementById('runtime-type'),null);assert.equal(u.doc.getElementById('migrate-runtime'),null);assert.equal(u.doc.getElementById('start-runtime'),null);assert.equal(u.doc.getElementById('stop-runtime'),null);assert.equal(u.doc.getElementById('save-runtime').disabled,false);assert.equal(u.doc.getElementById('runtime-binary').value,'/tmp/pi');u.click('save-runtime');await wait(()=>u.calls.some(x=>x[0]==='runtime'));const saved=u.calls.find(x=>x[0]==='runtime')[1];assert.equal(saved.type,'pi');assert.equal(saved.base_url,'');assert.equal(saved.mode,'')});

test('integration summary reports ready only when all layers are healthy',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();u.click('check-pi');await u.settle();const summary=u.doc.getElementById('integration-summary');assert.match(summary.className,/ready/);assert.match(summary.textContent,/Ready/);assert.match(summary.textContent,/Connected/);assert.match(summary.textContent,/Installed · v1\.4\.0/);assert.equal(u.doc.getElementById('mcp-tools').textContent.includes('delegate_research'),false)});

test('integration summary needs attention when skill is missing',async t=>{const snapshot=clone(initial);snapshot.skill={...skill,status:'missing',version:''};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());await u.openIntegration();const summary=u.doc.getElementById('integration-summary');assert.match(summary.className,/attention/);assert.match(summary.textContent,/Attention needed/);assert.match(summary.textContent,/Missing/)});

test('integration summary needs attention when MCP is missing',async t=>{const u=await setup({ProbeMCPResult:[{name:'MCP tools',status:'FAIL',code:'RUNTIME_PROTOCOL_ERROR',message:'unavailable'}]});t.after(()=>u.dom.window.close());await u.openIntegration();const summary=u.doc.getElementById('integration-summary');assert.match(summary.className,/attention/);assert.match(summary.textContent,/Missing/)});

test('integration summary needs attention when the Pi probe fails',async t=>{const u=await setup({ProbeRuntimeResult:[{name:'Pi protocol',status:'FAIL',code:'RUNTIME_NOT_FOUND',message:'Runtime executable not found'}]});t.after(()=>u.dom.window.close());await u.openIntegration();u.click('check-pi');await u.settle();const summary=u.doc.getElementById('integration-summary');assert.match(summary.className,/attention/);assert.match(summary.textContent,/Not ready/)});

test('exposed roles are read-only and match the reported MCP tools',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();const rows=[...u.doc.querySelectorAll('#exposed-roles tbody tr')];assert.equal(rows.length,4);const byRole=Object.fromEntries(rows.map(r=>[r.dataset.role,r]));assert.match(byRole.implementation.textContent,/Exposed/);assert.match(byRole.review.textContent,/Exposed/);assert.match(byRole.research.textContent,/Hidden/);assert.match(byRole.developer.textContent,/Hidden/);assert.equal(byRole.implementation.querySelectorAll('input,select').length,0);const tools=u.doc.getElementById('mcp-tools').textContent;for(const tool of ['delegate_implementation','delegate_review','ai_worker_continue','ai_worker_cancel'])assert.ok(tools.includes(tool),tool);assert.equal(tools.includes('delegate_research'),false);assert.equal(u.doc.getElementById('view-runtime')!==null,true)});

test('passive diagnostics never trigger protocol or real model runs',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();await wait(()=>u.doc.getElementById('run-passive'));assert.deepEqual(u.calls.filter(x=>x[0]==='diagnostics').length,1);assert.equal(u.calls.some(x=>x[0]==='protocol'),false);const real=u.doc.getElementById('content').textContent;assert.match(real,/never runs automatically/);assert.match(real,/Delegate a task from Codex to test Pi model execution/);assert.equal(u.doc.getElementById('copy-smoke'),null);assert.equal(u.calls.some(x=>x[0]==='start'),false)});

test('protocol test runs only on explicit action and no real test command is copyable',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();assert.equal(u.calls.some(x=>x[0]==='protocol'),false);u.click('run-protocol');await u.settle();assert.match(u.doc.getElementById('content').textContent,/Pi protocol/);assert.equal(u.doc.getElementById('copy-smoke'),null);assert.equal(u.calls.some(x=>x[0]==='start'),false)});

test('re-check refreshes passive checks without a user-triggered protocol run',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();u.click('run-passive');await wait(()=>u.calls.filter(x=>x[0]==='diagnostics').length===2);await u.settle();assert.equal(u.calls.some(x=>x[0]==='protocol'),false)});

test('saving role exposure sets a reconnect reminder on the integration page',async t=>{const u=await setup();t.after(()=>u.dom.window.close());u.nav('Agents');u.click('discover-routing');await wait(()=>u.doc.querySelectorAll('#route-model-implementation option').length===3);u.click('save-routing');await u.settle();assert.equal(u.dom.window.localStorage.getItem('ai-worker-exposure-changed'),'1');u.nav('Integration');await wait(()=>u.calls.some(x=>x[0]==='mcp'));await u.settle();assert.match(u.doc.getElementById('content').textContent,/Role exposure changed since the last known Codex connection/);u.click('dismiss-exposure');assert.equal(u.dom.window.localStorage.getItem('ai-worker-exposure-changed'),null)});

test('mcp config remains copyable from the integration page',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();u.click('copy-config');await wait(()=>u.calls.some(x=>x[0]==='copy'));assert.match(u.calls.find(x=>x[0]==='copy')[1],/mcp_servers/)});

test('agent exposure and model options save together',async t=>{const u=await setup();t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});u.nav('Agents');assert.equal(u.doc.getElementById('route-exposed-implementation').checked,true);assert.equal(u.doc.getElementById('save-routing').disabled,true);assert.doesNotMatch(u.doc.querySelector('.routing-card').textContent,/Agent available|Not applicable for Pi/);assert.equal(u.doc.getElementById('route-agent-implementation'),null);u.click('discover-routing');await wait(()=>u.doc.querySelectorAll('#route-model-implementation option').length===3);u.doc.getElementById('route-model-implementation').value='mock/sol';u.doc.getElementById('route-exposed-research').checked=false;u.click('save-routing');await wait(()=>u.calls.some(x=>x[0]==='routings'));const saved=u.calls.find(x=>x[0]==='routings')[1];assert.equal(saved.implementation.model,'mock/sol');assert.equal(saved.implementation.agent,'');assert.equal(saved.implementation.exposed,true);assert.equal(saved.research.exposed,false)});

test('model search is per-role, case-insensitive, selection-safe, and does not mark settings dirty',async t=>{const discovered={agents:[{name:'fixture',mode:'primary',description:'Configured externally'}],models:[{id:'mock/terra',provider_id:'mock',model_id:'terra',display_name:'Terra'},{id:'mock/sol',provider_id:'mock',model_id:'sol',display_name:'Solar'},{id:'alpha/opus',provider_id:'alpha',model_id:'opus',display_name:'Opus'}]};const u=await setup({DiscoverRouting:async()=>discovered});t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});const search=(role,value)=>{const input=u.doc.getElementById(`route-model-search-${role}`);input.value=value;input.dispatchEvent(new u.dom.window.Event('input',{bubbles:true}))};u.nav('Agents');u.click('discover-routing');await wait(()=>u.doc.querySelectorAll('#route-model-review option').length===4);const implementation=u.doc.getElementById('route-model-implementation');implementation.value='mock/sol';search('implementation','OPUS');assert.equal(implementation.value,'mock/sol');assert.deepEqual([...implementation.options].map(x=>x.value),['','mock/sol','alpha/opus']);assert.equal(u.doc.querySelectorAll('#route-model-review option').length,4);search('review','solar');assert.deepEqual([...u.doc.querySelectorAll('#route-model-review option')].map(x=>x.value),['','mock/sol']);search('review','');assert.equal(u.doc.querySelectorAll('#route-model-review option').length,4);search('review','alpha');u.nav('Runtimes');assert.equal(u.doc.querySelector('h1').textContent,'Runtimes');assert.equal(dialogOpen(u),false)});

test('model search is bilingual and selected filtered model saves normally',async t=>{const u=await setup();t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});u.nav('Agents');assert.equal(u.doc.querySelector('.route-model-search span').textContent,'Search models');u.click('language-toggle');assert.equal(u.doc.querySelector('.route-model-search span').textContent,'搜索模型');u.click('discover-routing');await wait(()=>u.doc.querySelectorAll('#route-model-review option').length===3);const search=u.doc.getElementById('route-model-search-review');search.value='sol';search.dispatchEvent(new u.dom.window.Event('input',{bubbles:true}));u.doc.getElementById('route-model-review').value='mock/sol';u.click('save-routing');await wait(()=>u.calls.some(x=>x[0]==='routings'));assert.equal(u.calls.find(x=>x[0]==='routings')[1].review.model,'mock/sol')});

test('runs show frozen routing, are escaped and cannot answer pending requests',async t=>{const u=await setup();t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});u.nav('Runs');assert.equal(u.doc.querySelectorAll('img').length,0);u.doc.querySelector('[data-run]').click();const detail=u.doc.getElementById('run-detail');const dl=detail.querySelector('dl');for(const value of ['ses_private','context_1','implementation','mock/terra'])assert.match(detail.textContent,new RegExp(value));assert.equal(dl.textContent.includes('fixture'),false,'Agent is no longer displayed');assert.equal(u.doc.querySelectorAll('#run-detail input,#run-detail textarea,#run-detail select').length,0);assert.equal(u.calls.length,0)});

test('runs hide prompts behind a safe dialog and show each timestamp on its own labeled line',async t=>{const u=await setup();t.after(()=>u.dom.window.close());u.nav('Runs');const objective=initial.runs[0].objective;const row=u.doc.querySelector('tbody tr');assert.doesNotMatch(row.textContent,new RegExp(objective.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')));const lines=[...row.querySelectorAll('.run-times > div')];assert.equal(lines.length,3);assert.deepEqual(lines.map(x=>x.querySelector('span').textContent),['Created','Updated','Elapsed']);assert.equal(lines[2].textContent,'Elapsed60 s');u.doc.querySelector('[data-prompt]').click();const dialog=u.doc.getElementById('prompt-dialog');assert.equal(dialog.hasAttribute('open'),true);assert.equal(u.doc.getElementById('prompt-content').textContent,objective);assert.equal(dialog.querySelector('img'),null);u.click('close-prompt');assert.equal(dialog.hasAttribute('open'),false)});

test('completed runs display opaque content without interpreting markdown or HTML',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='COMPLETED';snapshot.runs[0].result={status:'completed',content:'## Result\n\n```diff\n-old\n+new\n```\n<img src=x onerror=alert(1)>'};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');u.doc.querySelector('[data-run]').click();const result=u.doc.querySelector('.result-content');assert.equal(result.textContent,snapshot.runs[0].result.content);assert.equal(result.querySelector('img'),null);assert.equal(u.doc.querySelector('#run-detail h3').textContent,'Result')});

test('backend rejection is visible and disabled ownership controls stay disabled',async t=>{const u=await setup({SaveRuntime:async()=>{throw Error('CONFIG_ERROR: invalid runtime')}});t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});u.click('save-runtime');await wait(()=>u.doc.getElementById('message').textContent.includes('CONFIG_ERROR'));assert.equal(u.doc.getElementById('stop-runtime'),null);assert.equal(u.doc.getElementById('start-runtime'),null)});

test('native decisions display escaped questions, options, reason and time without answering controls',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='NEEDS_DECISION';snapshot.runs[0].result={status:'needs_decision',summary:'Parent choice',decision_requests:[{id:'dec_public',header:'API',question:'Preserve <API>?',options:[{id:'option_1',description:'Preserve API'}],reason:'Compatibility',created_at:'2026-09-17T01:02:03Z',multiple:false,allow_custom:true}]};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');u.doc.querySelector('[data-run]').click();const block=u.doc.querySelector('.pending-decision');for(const text of ['Preserve <API>?','Preserve API','Compatibility','2026-09-17T01:02:03Z','custom text allowed'])assert.ok(block.textContent.includes(text));assert.equal(block.querySelectorAll('button,input,textarea,select').length,0);assert.equal(u.calls.length,0)});

test('runs table shows only role and model in the second column',async t=>{const u=await setup();t.after(()=>u.dom.window.close());u.nav('Runs');const head=[...u.doc.querySelectorAll('#content thead th')].map(x=>x.textContent);assert.deepEqual(head,['Run / objective','Role / model','Status','Created / updated / elapsed']);const cell=u.doc.querySelector('tbody tr').cells[1];assert.ok(cell.textContent.includes('implementation'));assert.ok(cell.textContent.includes('mock/terra'));assert.equal(cell.textContent.includes('fixture'),false,'Agent is gone from the runs table')});

test('runs display the backend pi --resume command verbatim and copy it without trimming or execution',async t=>{
 const snapshot=clone(initial);
 const cmd="pi --resume '/tmp/pi-sessions/ses 123\"' && echo <img src=x onerror=alert(1)> $HOME";
 snapshot.pi_resume_commands={wrk_test:`  ${cmd}  `};
 const u=await setup({},snapshot);t.after(()=>u.dom.window.close());
 u.nav('Runs');
 const row=u.doc.querySelector('tbody tr');
 const code=row.querySelector('.resume-command code');
 assert.ok(code,'resume command block is rendered');
 assert.equal(code.textContent,`  ${cmd}  `,'command is displayed verbatim including surrounding spaces');
 assert.equal(row.querySelectorAll('.resume-command img').length,0,'HTML in the command is escaped');
 assert.equal(row.querySelector('.copy-resume').textContent,'Copy pi --resume');
 row.querySelector('.copy-resume').click();
 await wait(()=>u.calls.some(x=>x[0]==='copy'));
 assert.equal(u.calls.find(x=>x[0]==='copy')[1],`  ${cmd}  `,'copied verbatim without trimming');
 assert.match(u.doc.getElementById('message').textContent,/Pi resume command copied/);
 assert.equal(u.calls.some(x=>['start','stop','protocol','runtime'].includes(x[0])),false,'copy never executes anything');
});

test('pi --resume copy is translated and failures are reported',async t=>{
 const snapshot=clone(initial);
 snapshot.pi_resume_commands={wrk_test:"pi --resume ses_1"};
 const u=await setup({ClipboardSetTextOverride:true},snapshot);
 t.after(()=>u.dom.window.close());
 u.click('language-toggle');u.nav('Runs');
 const row=u.doc.querySelector('tbody tr');
 assert.equal(row.querySelector('.copy-resume').textContent,'复制 pi --resume');
 row.querySelector('.copy-resume').click();
 await wait(()=>u.calls.some(x=>x[0]==='copy'));
 assert.match(u.doc.getElementById('message').textContent,/已复制 pi --resume 命令/);
 u.dom.window.runtime.ClipboardSetText=async()=>false;
 row.querySelector('.copy-resume').click();
 await wait(()=>u.doc.getElementById('message').textContent.includes('复制失败'));
 u.doc.getElementById('message').textContent='';
 u.dom.window.runtime.ClipboardSetText=async()=>{throw Error('clipboard unavailable')};
 row.querySelector('.copy-resume').click();
 await wait(()=>u.doc.getElementById('message').textContent.includes('复制失败'));
});

test('runs without a backend command show a brief unavailable note and no copy button',async t=>{
 const u=await setup();t.after(()=>u.dom.window.close());
 u.nav('Runs');
 const row=u.doc.querySelector('tbody tr');
 assert.equal(row.querySelector('[data-copy-resume]'),null,'no copy button without a command');
 assert.match(row.querySelector('.resume-command.unavailable').textContent,/pi --resume unavailable/);
 u.click('language-toggle');
 assert.match(u.doc.querySelector('.resume-command.unavailable').textContent,/暂无 pi --resume 命令/);
});

test('result button shows completed result content in dialog with metadata',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='COMPLETED';snapshot.runs[0].result={status:'completed',content:'## Result\n\n```diff\n-old\n+new\n```\n<img src=x onerror=alert(1)>'};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');const btn=u.doc.querySelector('[data-result]');assert.ok(btn,'View result button exists');assert.equal(btn.textContent,'View result');btn.click();const dialog=u.doc.getElementById('result-dialog');assert.equal(dialog.hasAttribute('open'),true);const meta=u.doc.getElementById('result-meta');assert.ok(meta.textContent.includes('wrk_test'),'Shows run ID');assert.ok(meta.textContent.includes('COMPLETED'),'Shows status');assert.ok(meta.textContent.includes('implementation'),'Shows role');assert.equal(meta.textContent.includes('fixture'),false,'Agent is no longer displayed');const content=u.doc.getElementById('result-content');assert.equal(content.textContent,snapshot.runs[0].result.content);assert.equal(content.querySelector('img'),null);u.click('close-result');assert.equal(dialog.hasAttribute('open'),false)});

test('result button for completed run with empty content shows fallback',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='COMPLETED';snapshot.runs[0].result={status:'completed',content:''};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');const btn=u.doc.querySelector('[data-result]');btn.click();const content=u.doc.getElementById('result-content');assert.equal(content.textContent,'No result content')});

test('result button for FAILED run shows error message',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='FAILED';snapshot.runs[0].result={status:'failed',error:{code:'EXEC_ERROR',message:'openCode process exit 1'}};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');const btn=u.doc.querySelector('[data-result]');btn.click();const meta=u.doc.getElementById('result-meta');assert.ok(meta.textContent.includes('FAILED'));const content=u.doc.getElementById('result-content');assert.equal(content.textContent,'openCode process exit 1')});

test('result button for FAILED run without error shows fallback',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='FAILED';snapshot.runs[0].result={status:'failed'};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');const btn=u.doc.querySelector('[data-result]');btn.click();const content=u.doc.getElementById('result-content');assert.equal(content.textContent,'Run failed, no result content to display')});

test('result button for CANCELLED run shows cancellation message',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='CANCELLED';snapshot.runs[0].result={status:'cancelled'};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');const btn=u.doc.querySelector('[data-result]');btn.click();const meta=u.doc.getElementById('result-meta');assert.ok(meta.textContent.includes('CANCELLED'));const content=u.doc.getElementById('result-content');assert.equal(content.textContent,'Task cancelled, no final result')});

test('result button for RUNNING run shows not-yet-produced message',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='RUNNING';const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');const btn=u.doc.querySelector('[data-result]');btn.click();const content=u.doc.getElementById('result-content');assert.equal(content.textContent,'Task has not produced a final result yet')});

test('view prompt still works when view result is present',async t=>{const u=await setup();t.after(()=>u.dom.window.close());u.nav('Runs');const objective=initial.runs[0].objective;const promptBtn=u.doc.querySelector('[data-prompt]');assert.ok(promptBtn,'View prompt button exists');promptBtn.click();const promptDialog=u.doc.getElementById('prompt-dialog');assert.equal(promptDialog.hasAttribute('open'),true);assert.equal(u.doc.getElementById('prompt-content').textContent,objective);u.click('close-prompt');assert.equal(promptDialog.hasAttribute('open'),false)});

test('copy result button copies content via ClipboardSetText',async t=>{const snapshot=clone(initial);snapshot.runs[0].status='COMPLETED';snapshot.runs[0].result={status:'completed',content:'final result output'};const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.nav('Runs');u.doc.querySelector('[data-result]').click();u.click('copy-result');await wait(()=>u.calls.some(x=>x[0]==='copy'));const copy=u.calls.find(x=>x[0]==='copy');assert.ok(copy,'ClipboardSetText was called');assert.equal(copy[1],'final result output')});

test('settings authorizes a workspace through the picker with no path input',async t=>{const u=await setup();t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');assert.equal(u.doc.querySelector('#add-workspace').textContent,'Add Workspace');assert.match(u.doc.getElementById('content').textContent,/No authorized workspaces yet/);assert.equal(u.doc.querySelector('#workspace-path'),null);assert.equal(u.doc.querySelector('input[placeholder*="path" i]'),null);u.click('add-workspace');await u.settle();assert.deepEqual(u.calls.find(x=>x[0]==='add-workspace'),['add-workspace',0]);assert.match(u.doc.querySelector('p.notice').textContent,/Workspace authorized/);const row=u.doc.querySelector('#workspaces tbody tr');assert.equal(row.dataset.workspace,'ws_active');assert.match(row.textContent,/project/);assert.equal(row.querySelector('.badge').textContent,'Authorized');assert.equal(row.querySelector('[data-revoke-workspace]').textContent,'Remove Access')});

test('the authorized list shows only ACTIVE grants while removed ones sit in a collapsed read-only history',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'alpha'}),workspaceRecord('ws_b',{label:'beta',canonical_root:'/tmp/beta'}),workspaceRecord('ws_c',{label:'gamma',canonical_root:'/tmp/gamma',state:'REVOKED',revision:2,updated_at:1750000900000})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');const rows=[...u.doc.querySelectorAll('#workspaces tbody tr')];assert.deepEqual(rows.map(r=>r.dataset.workspace),['ws_a','ws_b']);assert.deepEqual(rows.map(r=>r.querySelector('.badge').textContent),['Authorized','Authorized']);assert.equal(u.doc.querySelector('#workspaces').textContent.includes('gamma'),false);const history=removedSection(u);assert.ok(history,'the removed history section exists');assert.equal(history.tagName,'DETAILS');assert.equal(history.hasAttribute('open'),false);assert.equal(history.querySelector('summary').textContent,'Removed Access (1)');const removed=[...history.querySelectorAll('tbody tr')];assert.equal(removed.length,1);assert.equal(removed[0].dataset.removedWorkspace,'ws_c');assert.match(removed[0].textContent,/gamma/);assert.match(removed[0].textContent,/\/tmp\/gamma/);assert.equal(removed[0].querySelector('.badge').textContent,'Removed');assert.equal(removed[0].querySelectorAll('button').length,0);assert.equal(history.querySelectorAll('[data-rename-workspace],[data-revoke-workspace],input,select').length,0);history.querySelector('summary').click();assert.equal(u.calls.length,0);assert.equal(snapshot.workspaces.length,3);assert.equal(snapshot.workspaces[2].state,'REVOKED');assert.equal(snapshot.workspaces[2].revision,2)});

test('the remove-access UI copy replaces revoke wording in English while rename and remove stay enabled',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a'),workspaceRecord('ws_b',{label:'gone',canonical_root:'/tmp/gone',state:'REVOKED',revision:2,updated_at:1750000900000})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');const content=u.doc.getElementById('content').textContent;assert.match(content,/Remove Access/);assert.match(content,/Authorized/);assert.doesNotMatch(content,/\bRevoke\b|\bRevoked\b|撤销|已撤销/);assert.deepEqual([...removedSection(u).querySelectorAll('thead th')].map(x=>x.textContent),['Label','Folder','Status','Source','Removed time']);assert.match(removedSection(u).querySelector('tbody tr td:last-child').textContent,/\d/);const active=u.doc.querySelector('#workspaces tbody tr');assert.equal(active.querySelector('[data-rename-workspace]').textContent,'Rename');assert.equal(active.querySelector('[data-rename-workspace]').disabled,false);assert.equal(active.querySelector('[data-revoke-workspace]').disabled,false)});

test('renaming a workspace opens a modal prefilled with the current label and keeps authority',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');const dialog=openRename(u,'ws_a');assert.equal(dialogOpen(u),true);assert.equal(dialogTitle(u),'Rename Workspace');assert.match(dialogBody(u),/old/);assert.equal(dialogInput(u).value,'old');assert.equal(u.calls.some(x=>x[0]==='rename-workspace'),false);typeLabel(u,'renamed');dialogOk(u).click();await u.settle();assert.deepEqual(u.calls.filter(x=>x[0]==='rename-workspace'),[['rename-workspace','ws_a','renamed']]);assert.equal(snapshot.workspaces[0].label,'renamed');assert.equal(snapshot.workspaces[0].state,'ACTIVE');assert.equal(snapshot.workspaces[0].revision,1);assert.equal(dialogOpen(u),false);assert.match(u.doc.querySelector('#workspaces tbody tr').textContent,/renamed/)});

test('cancelling the rename modal closes it without any backend call',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');openRename(u,'ws_a');typeLabel(u,'never saved');dialogCancel(u).click();assert.equal(dialogOpen(u),false);await new Promise(r=>setTimeout(r,20));assert.equal(u.calls.length,0);assert.equal(snapshot.workspaces[0].label,'old');assert.match(u.doc.querySelector('#workspaces tbody tr').textContent,/old/)});

test('an empty or whitespace-only workspace name cannot be submitted',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');openRename(u,'ws_a');assert.equal(dialogOk(u).disabled,false);for(const value of ['','   ','\t']){typeLabel(u,value);assert.equal(dialogOk(u).disabled,true);dialogOk(u).click();assert.equal(u.calls.some(x=>x[0]==='rename-workspace'),false);assert.equal(dialogOpen(u),true)}typeLabel(u,'  padded  ');assert.equal(dialogOk(u).disabled,false);dialogOk(u).click();await u.settle();assert.deepEqual(u.calls.filter(x=>x[0]==='rename-workspace'),[['rename-workspace','ws_a','padded']])});

test('pressing Enter in the rename modal submits a valid label once',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');openRename(u,'ws_a');assert.equal(u.doc.activeElement,dialogInput(u));typeLabel(u,'entered');press(u,dialogInput(u),'Enter');await u.settle();assert.deepEqual(u.calls.filter(x=>x[0]==='rename-workspace'),[['rename-workspace','ws_a','entered']]);assert.equal(snapshot.workspaces[0].label,'entered');assert.equal(dialogOpen(u),false)});

test('rename backend failure stays visible with the modal open and the label unchanged',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'})];let attempts=0;const u=await setup({RenameWorkspace:async()=>{attempts++;throw Error('WORKSPACE_INVALID')}},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');openRename(u,'ws_a');typeLabel(u,'new name');dialogOk(u).click();await u.settle();assert.equal(attempts,1);assert.match(u.doc.getElementById('message').textContent,/WORKSPACE_INVALID/);assert.equal(dialogError(u).hidden,false);assert.match(dialogError(u).textContent,/WORKSPACE_INVALID/);assert.equal(dialogOpen(u),true);assert.equal(snapshot.workspaces[0].label,'old');assert.equal(snapshot.workspaces[0].state,'ACTIVE');dialogOk(u).click();await u.settle();assert.equal(attempts,2)});

test('remove access confirms through the app modal, calls revoke exactly once and leaves the authorized list',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a')];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');const dialog=openRevoke(u,'ws_a');assert.equal(dialogOpen(u),true);assert.equal(dialogTitle(u),'Remove workspace access?');assert.match(dialogBody(u),/After access is removed, existing runs lose access and project commands are stopped\. Historical run records and authorization audit information will be retained\./);assert.match(dialogBody(u),/project/);assert.equal(dialogOk(u).textContent,'Remove Access');assert.equal(dialogOk(u).className,'danger');assert.equal(dialogCancel(u).textContent,'Cancel');assert.equal(dialog.querySelector('img'),null);dialogCancel(u).click();await new Promise(r=>setTimeout(r,20));assert.equal(dialogOpen(u),false);assert.equal(u.calls.some(x=>x[0]==='revoke-workspace'),false);assert.equal(snapshot.workspaces[0].state,'ACTIVE');assert.equal(snapshot.workspaces[0].revision,1);assert.equal(u.doc.querySelectorAll('#workspaces tbody tr').length,1);assert.equal(removedSection(u),null);openRevoke(u,'ws_a');dialogOk(u).click();await u.settle();assert.deepEqual(u.calls.filter(x=>x[0]==='revoke-workspace'),[['revoke-workspace','ws_a']]);assert.equal(dialogOpen(u),false);assert.equal(u.doc.getElementById('workspaces'),null);assert.match(u.doc.getElementById('content').textContent,/No authorized workspaces yet/);assert.equal(snapshot.workspaces.length,1);assert.equal(snapshot.workspaces[0].id,'ws_a');assert.equal(snapshot.workspaces[0].state,'REVOKED');assert.equal(snapshot.workspaces[0].revision,2);const history=removedSection(u);assert.equal(history.querySelector('summary').textContent,'Removed Access (1)');const row=history.querySelector('tbody tr');assert.equal(row.dataset.removedWorkspace,'ws_a');assert.match(row.textContent,/project/);assert.equal(row.querySelectorAll('button').length,0)});

test('a failed remove access keeps the grant in the authorized list with the modal open and the error shown',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a')];const u=await setup({RevokeWorkspace:async()=>{throw Error('INTERNAL_ERROR')}},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');openRevoke(u,'ws_a');dialogOk(u).click();await u.settle();assert.match(u.doc.getElementById('message').textContent,/INTERNAL_ERROR/);assert.equal(dialogError(u).hidden,false);assert.match(dialogError(u).textContent,/INTERNAL_ERROR/);assert.equal(dialogOpen(u),true);assert.equal(snapshot.workspaces[0].state,'ACTIVE');assert.equal(snapshot.workspaces[0].revision,1);const rows=[...u.doc.querySelectorAll('#workspaces tbody tr')];assert.deepEqual(rows.map(r=>r.dataset.workspace),['ws_a']);assert.equal(removedSection(u),null);dialogCancel(u).click();assert.equal(dialogOpen(u),false);assert.equal(u.calls.length,0)});

test('closing a modal with Escape cancels without a backend call',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');openRename(u,'ws_a');typeLabel(u,'escape me');press(u,dialogInput(u),'Escape');assert.equal(dialogOpen(u),false);openRevoke(u,'ws_a');press(u,dialogEl(u),'Escape');assert.equal(dialogOpen(u),false);await new Promise(r=>setTimeout(r,20));assert.equal(u.calls.length,0);assert.equal(snapshot.workspaces[0].label,'old');assert.equal(snapshot.workspaces[0].state,'ACTIVE')});

test('dirty settings route the discard guard through the app modal instead of a browser dialog',async t=>{const u=await setup();t.after(async()=>{await u.settle();u.dom.window.close()});markDirty(u);u.nav('Agents');assert.equal(dialogOpen(u),true);assert.equal(dialogTitle(u),'Unsaved changes');assert.match(dialogBody(u),/Discard unsaved changes\?/);assert.equal(dialogOk(u).textContent,'Discard');dialogCancel(u).click();assert.equal(dialogOpen(u),false);assert.equal(u.doc.querySelector('h1').textContent,'Runtimes');markDirty(u);u.nav('Agents');dialogOk(u).click();assert.equal(u.doc.querySelector('h1').textContent,'Agents');assert.equal(dialogOpen(u),false)});

test('the discard guard also gates the language switch and refresh actions',async t=>{const u=await setup();t.after(async()=>{await u.settle();u.dom.window.close()});markDirty(u);u.click('language-toggle');assert.equal(dialogOpen(u),true);assert.equal(dialogTitle(u),'Unsaved changes');dialogCancel(u).click();assert.equal(u.doc.documentElement.lang,'en');assert.equal(u.dom.window.localStorage.getItem('ai-worker-locale'),null);assert.equal(u.doc.querySelector('h1').textContent,'Runtimes');markDirty(u);u.click('language-toggle');dialogOk(u).click();assert.equal(u.doc.documentElement.lang,'zh-CN');assert.equal(u.dom.window.localStorage.getItem('ai-worker-locale'),'zh-CN');assert.equal(dialogOpen(u),false);markDirty(u);u.click('language-toggle');assert.equal(dialogTitle(u),'未保存的更改');dialogOk(u).click();assert.equal(u.doc.documentElement.lang,'en');markDirty(u);u.click('refresh');assert.equal(dialogOpen(u),true);dialogOk(u).click();await u.settle();assert.equal(u.doc.getElementById('runtime-binary').value,'/tmp/pi')});

test('the modal keeps the pending edit out of the unsaved-changes state',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');openRename(u,'ws_a');typeLabel(u,'typing only here');markDirty(u,'mcp-binary');assert.equal(dialogOpen(u),true);u.nav('Agents');assert.equal(dialogTitle(u),'Unsaved changes');dialogCancel(u).click();assert.equal(u.doc.querySelector('h1').textContent,'Settings');assert.equal(u.calls.some(x=>x[0]==='rename-workspace'),false);assert.equal(snapshot.workspaces[0].label,'old')});

test('workspace rename and remove access modals are translated and never fall back to English copy',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'old'}),workspaceRecord('ws_b',{label:'gone',canonical_root:'/tmp/gone',state:'REVOKED',revision:2,updated_at:1750000900000})];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.click('language-toggle');u.nav('Settings');const content=u.doc.getElementById('content').textContent;assert.match(content,/授权工作区/);assert.match(content,/已授权/);assert.match(content,/移除授权/);assert.match(content,/已移除的授权 \(1\)/);assert.match(content,/已移除/);assert.doesNotMatch(content,/撤销|Revoke|Revoked/);openRename(u,'ws_a');assert.equal(dialogTitle(u),'重命名工作区');assert.equal(dialogOk(u).textContent,'重命名');assert.equal(dialogCancel(u).textContent,'取消');assert.match(u.doc.getElementById('app-dialog-body').querySelector('span').textContent,/新工作区名称/);dialogCancel(u).click();openRevoke(u,'ws_a');assert.equal(dialogTitle(u),'移除工作区授权？');assert.equal(dialogOk(u).textContent,'移除授权');assert.equal(dialogCancel(u).textContent,'取消');assert.match(dialogBody(u),/移除后，现有任务将失去访问权限，项目命令也会停止。历史运行记录和授权审计信息仍会保留。/);dialogCancel(u).click();markDirty(u,'mcp-binary');u.nav('Agents');assert.equal(dialogTitle(u),'未保存的更改');assert.match(dialogBody(u),/放弃未保存的更改？/);assert.equal(dialogOk(u).textContent,'放弃');assert.equal(dialogCancel(u).textContent,'取消')});

test('no production frontend code path calls the browser prompt, confirm or alert dialogs',async t=>{const sources=readdirSync(new URL('../src/',import.meta.url)).filter(x=>x.endsWith('.ts'));assert.ok(sources.length);const jsDialog=/\b(?:window\s*\.\s*)?(?:alert|prompt|confirm)\s*\(/g;for(const file of sources){const text=readFileSync(new URL(`../src/${file}`,import.meta.url),'utf8');assert.deepEqual(text.match(jsDialog)||[],[],`${file} must use app-owned DOM modals`)}assert.deepEqual(bundle.match(jsDialog)||[],[],'the built bundle must not call browser dialogs');assert.match(bundle,/id="app-dialog"/);assert.doesNotMatch(readFileSync(new URL('./ui.test.mjs',import.meta.url),'utf8'),/dom\.window\.(confirm|prompt)\s*=/)});

test('re-adding an authorized workspace reports already authorized without duplication',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_active')];const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');u.click('add-workspace');await u.settle();assert.match(u.doc.querySelector('p.notice').textContent,/Already authorized/);assert.equal(snapshot.workspaces.length,1);assert.equal(u.doc.querySelectorAll('#workspaces tbody tr').length,1)});

test('canceled folder picker leaves the workspace list unchanged',async t=>{const u=await setup({AddWorkspace:async()=>({workspace:{},created:false})});t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');u.click('add-workspace');await u.settle();assert.equal((u.doc.querySelector('p.notice')?.textContent||'').includes('Workspace authorized'),false);assert.match(u.doc.getElementById('content').textContent,/No authorized workspaces yet/)});

test('run detail shows the bound workspace label and not the full path',async t=>{const snapshot=clone(initial);snapshot.workspaces=[workspaceRecord('ws_a',{label:'mydir'})];snapshot.runs[0].workspace={workspace_id:'ws_a',canonical_root:'/tmp/project',grant_revision:1};const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Runs');u.doc.querySelector('[data-run]').click();const text=u.doc.getElementById('run-detail').textContent;assert.match(text,/Workspace/);assert.match(text,/mydir/);assert.equal(text.includes('/tmp/project'),false)});

test('execution policy is a read-only display taken from the backend snapshot',async t=>{const u=await setup();t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});u.nav('Agents');const cards=[...u.doc.querySelectorAll('.routing-card')];assert.deepEqual(cards.map(c=>c.querySelector('dl dd').textContent),['Repository read-only','—','Repository read-only','Context-only']);for(const c of cards)assert.equal(c.querySelectorAll('dl dd button,dl dd input,dl dd select').length,0);assert.equal(u.calls.some(x=>['routings','settings','binary'].includes(x[0])),false)});

test('execution policy display follows the reported snapshot and never the exposure toggle',async t=>{const snapshot=clone(initial);snapshot.role_policies={implementation:'Context-only',review:'Context-only',research:'Context-only'};snapshot.config.agents.implementation.exposed=false;const u=await setup({},snapshot);t.after(async()=>{await wait(()=>!u.doc.getElementById('app').hasAttribute('aria-busy'));u.dom.window.close()});u.nav('Agents');assert.equal(u.doc.querySelector('.routing-card dl dd').textContent,'Context-only');assert.match(u.doc.querySelector('#route-exposed-implementation').closest('.routing-card').textContent,/Hidden/)});

test('integration role table reports the server policy beside each delegation tool',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();const head=[...u.doc.querySelectorAll('#exposed-roles thead th')].map(x=>x.textContent);assert.deepEqual(head,['Role','Exposure','Tool','Execution Policy']);const byRole=Object.fromEntries([...u.doc.querySelectorAll('#exposed-roles tbody tr')].map(r=>[r.dataset.role,r]));assert.equal(byRole.implementation.cells[3].textContent,'Repository read-only');assert.equal(byRole.review.cells[3].textContent,'Repository read-only');assert.equal(byRole.research.cells[3].textContent,'Context-only')});

test('passive diagnostics list the role policy and repository read enforcement',async t=>{const u=await setup();t.after(()=>u.dom.window.close());await u.openIntegration();const text=u.doc.getElementById('content').textContent;assert.match(text,/Role Execution Policy: implementation/);assert.match(text,/Repository Read Enforcement/);assert.match(text,/read: enabled\/scoped/);assert.match(text,/git command: denied/);assert.match(text,/Every access is re-confined/)});

test('run detail shows the frozen execution policy for the run role',async t=>{const snapshot=clone(initial);snapshot.runs[0].capabilities={repoRead:false,repoWrite:false,shell:false,web:false,subagent:false};snapshot.runs[0].logical_role='research';snapshot.runs[0].agent='research';const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Runs');u.doc.querySelector('[data-run]').click();const text=u.doc.getElementById('run-detail').textContent;assert.match(text,/Execution Policy/);assert.match(text,/Context-only/);assert.equal(text.includes('Repository read-only'),false)});

test('workspace authorization copy states the read-only scope and no stale denial',async t=>{const u=await setup();t.after(async()=>{await u.settle();u.dom.window.close()});u.nav('Settings');const help=[...u.doc.querySelectorAll('#content p.help')].map(x=>x.textContent).join(' | ');assert.match(help,/Developer can modify and test this project; Implementation and Review can inspect it read-only/);assert.equal(u.doc.getElementById('content').textContent.includes('Repository read remains disabled'),false)});

test('execution policy labels and workspace scope copy are translated',async t=>{const u=await setup();t.after(async()=>{await u.settle();u.dom.window.close()});u.click('language-toggle');u.nav('Agents');const card=u.doc.querySelector('.routing-card');assert.match(card.textContent,/执行策略/);assert.match(card.querySelector('dl dd').textContent,/仓库只读/);u.nav('Settings');assert.match([...u.doc.querySelectorAll('#content p.help')].map(x=>x.textContent).join(' | '),/完整实现角色可以修改和测试该项目，实现与审查角色以只读方式查看/);u.nav('Runs');u.doc.querySelector('[data-run]').click();assert.match(u.doc.getElementById('run-detail').textContent,/执行策略/)});

test('Pi offers an offline check and explains automatic task startup',async t=>{
 const snapshot=clone(initial);
 snapshot.config.runtime={type:'pi',mode:'',binary:'/tmp/pi',session_dir:'/tmp/pi-sessions',port:0};
 snapshot.runtime={};
 const u=await setup({ProbeRuntime:async()=>{u.calls.push(['pi-probe']);return [{name:'Pi protocol',status:'PASS',message:'Pi 1.0.2 · Offline RPC handshake verified'}]}},snapshot);
 t.after(()=>u.dom.window.close());
 assert.equal(u.doc.getElementById('start-runtime'),null);
 assert.equal(u.doc.getElementById('stop-runtime'),null);
 assert.equal(u.doc.getElementById('check-pi').disabled,false);
 assert.match(u.doc.querySelector('.intro').textContent,/No manual start is required/);
 assert.match(u.doc.querySelector('header').textContent,/Pi Runtime/);
 assert.match(u.doc.querySelector('header').textContent,/Starts automatically for each task/);
 assert.equal(u.calls.length,0);
 u.click('check-pi');await u.settle();
 assert.match(u.doc.querySelector('.diagnostic-results').textContent,/PASS/);
 assert.match(u.doc.querySelector('.diagnostic-results').textContent,/Pi 1.0.2/);
 assert.deepEqual(u.calls.map(x=>x[0]),['pi-probe']);
 u.click('language-toggle');
 assert.equal(u.doc.getElementById('check-pi').textContent,'检查 Pi');
 assert.match(u.doc.querySelector('.intro').textContent,/无需手动启动/);
});

test('Pi check reports failures and requires saving edited configuration',async t=>{
 const snapshot=clone(initial);
 snapshot.config.runtime={type:'pi',mode:'',binary:'/tmp/missing-pi',session_dir:'/tmp/pi-sessions',port:0};
 snapshot.runtime={};
 const u=await setup({ProbeRuntime:async()=>{u.calls.push(['pi-probe']);return [{name:'Pi protocol',status:'FAIL',code:'RUNTIME_NOT_FOUND',message:'Runtime executable not found'}]}},snapshot);
 t.after(()=>u.dom.window.close());
 markDirty(u);
 u.click('check-pi');
 assert.match(u.doc.getElementById('message').textContent,/Save runtime before checking Pi/);
 assert.equal(u.calls.length,0);
 u.click('save-runtime');await u.settle();
 u.click('check-pi');await u.settle();
 assert.match(u.doc.querySelector('.diagnostic-results').textContent,/RUNTIME_NOT_FOUND/);
 assert.equal(u.calls.filter(x=>x[0]==='pi-probe').length,1);
});

test('Pi integration shows its probe state without OpenCode server instructions',async t=>{
 const snapshot=clone(initial);
 snapshot.config.runtime={type:'pi',mode:'',binary:'/tmp/pi',session_dir:'/tmp/pi-sessions',port:0};
 snapshot.runtime={};
 const u=await setup({ProbeRuntime:async()=>[{name:'Pi protocol',status:'PASS',message:'Pi 1.0.2'}]},snapshot);
 t.after(()=>u.dom.window.close());
 await u.openIntegration();
 assert.match(u.doc.getElementById('integration-summary').textContent,/Pi Runtime/);
 assert.match(u.doc.getElementById('integration-summary').textContent,/Not checked/);
 assert.doesNotMatch([...u.doc.querySelectorAll('h2')].map(x=>x.textContent).join(' '),/OpenCode Runtime/);
 assert.equal(u.doc.getElementById('copy-smoke'),null);
 u.click('check-pi');await u.settle();
 assert.match(u.doc.getElementById('integration-summary').className,/ready/);
 u.click('language-toggle');
 assert.match(u.doc.getElementById('content').textContent,/Pi 运行时/);
 assert.match(u.doc.getElementById('content').textContent,/无需手动启动/);
 assert.match(u.doc.getElementById('content').textContent,/Pi 协议/);
});

test('workspace method copy is bilingual and explains read-only scope without raw tool names',async t=>{
 const u=await setup();t.after(()=>u.dom.window.close());
 u.nav('Settings');
 assert.match(u.doc.getElementById('content').textContent,/View and remove access here at any time/);
 assert.doesNotMatch(u.doc.querySelector('.authorization-methods').textContent,/ai_worker_/);
 u.click('language-toggle');
 assert.match(u.doc.getElementById('content').textContent,/通过 MCP 授权文件夹/);
 assert.match(u.doc.getElementById('content').textContent,/管理和移除/);
 assert.doesNotMatch(u.doc.querySelector('.authorization-methods').textContent,/Codex can call|revoke|撤销/);
});

test('Pi roles select and persist models without a named agent catalogue',async t=>{
 const snapshot=clone(initial);
 snapshot.config.runtime={type:'pi',mode:'',binary:'/tmp/pi',session_dir:'/tmp/pi-sessions',port:0};
 snapshot.runtime={};
 const catalogue={agents:[],models:[{id:'huggingface/deepseek-ai/DeepSeek-V4-Flash',provider_id:'huggingface',model_id:'deepseek-ai/DeepSeek-V4-Flash',display_name:'DeepSeek'},{id:'openai/gpt-5-mini',provider_id:'openai',model_id:'gpt-5-mini',display_name:'GPT mini'}]};
 const u=await setup({DiscoverRouting:async()=>{u.calls.push(['pi-models']);return catalogue},SaveRoutings:async routes=>{u.calls.push(['routings',clone(routes)]);snapshot.config.agents=clone(routes)}},snapshot);
 t.after(()=>u.dom.window.close());
 u.nav('Agents');
 assert.equal(u.doc.getElementById('discover-routing').textContent,'Refresh Models');
 assert.equal(u.doc.getElementById('route-agent-implementation'),null);
 assert.match(u.doc.querySelector('.intro').textContent,/Choose a Pi model for each role/);
 assert.equal(u.doc.getElementById('save-routing').disabled,true);
 u.click('discover-routing');await u.settle();
 assert.equal(u.doc.getElementById('save-routing').disabled,false);
 const chosen='huggingface/deepseek-ai/DeepSeek-V4-Flash';
 const select=u.doc.getElementById('route-model-implementation');
 assert.equal(select.options[0].textContent,'Select a Pi model');
 assert.ok([...select.options].some(o=>o.value===chosen));
 select.value=chosen;
 const search=u.doc.getElementById('route-model-search-review');search.value='GPT';search.dispatchEvent(new u.dom.window.Event('input',{bubbles:true}));
 assert.ok([...u.doc.getElementById('route-model-review').options].some(o=>o.value==='openai/gpt-5-mini'));
 u.doc.getElementById('route-model-review').value='openai/gpt-5-mini';
 u.doc.getElementById('route-model-research').value='';
 u.doc.getElementById('route-exposed-research').checked=false;
 u.click('save-routing');await u.settle();
 const saved=u.calls.find(x=>x[0]==='routings')[1];
 assert.deepEqual(saved.implementation,{exposed:true,agent:'',model:chosen});
 assert.deepEqual(saved.review,{exposed:true,agent:'',model:'openai/gpt-5-mini'});
 assert.deepEqual(saved.research,{exposed:false,agent:'',model:''});
 assert.equal(u.doc.getElementById('route-model-implementation').value,chosen);
 u.click('language-toggle');
 assert.equal(u.doc.getElementById('discover-routing').textContent,'刷新模型');
 assert.equal(u.doc.getElementById('route-model-review').options[0].textContent,'请选择 Pi 模型');
 assert.match(u.doc.querySelector('.intro').textContent,/每个角色选择 Pi 模型/);
 assert.doesNotMatch(u.doc.querySelector('#content').textContent,/OpenCode/);
});

test('failed Pi model refresh disables saving a previously verified catalogue',async t=>{
 const snapshot=clone(initial);
 snapshot.config.runtime={type:'pi',mode:'',binary:'/tmp/pi',session_dir:'/tmp/pi-sessions',port:0};snapshot.runtime={};
 let fail=false;
 const u=await setup({DiscoverRouting:async()=>{if(fail)throw Error('Pi catalogue unavailable');return {agents:[],models:[{id:'mock/terra',provider_id:'mock',model_id:'terra',display_name:'Terra'}]}}},snapshot);
 t.after(()=>u.dom.window.close());
 u.nav('Agents');u.click('discover-routing');await u.settle();
 assert.equal(u.doc.getElementById('save-routing').disabled,false);
 fail=true;u.click('discover-routing');await u.settle();
 assert.match(u.doc.getElementById('message').textContent,/Pi catalogue unavailable/);
 assert.equal(u.doc.getElementById('save-routing').disabled,true);
 u.click('save-routing');
 assert.equal(u.calls.some(x=>x[0]==='routings'),false);
});

test('saving the Pi runtime resets a previously discovered routing catalogue',async t=>{
 const snapshot=clone(initial);
 const u=await setup({SaveRuntime:async rt=>{snapshot.config.runtime=clone(rt)}},snapshot);
 t.after(()=>u.dom.window.close());
 u.nav('Agents');u.click('discover-routing');await u.settle();
 assert.equal(u.doc.getElementById('save-routing').disabled,false);
 u.nav('Runtimes');
 u.click('save-runtime');await u.settle();
 u.nav('Agents');
 assert.equal(u.doc.getElementById('save-routing').disabled,true);
 assert.equal(u.doc.getElementById('route-agent-review'),null);
 assert.equal(u.doc.querySelector('#route-model-review option[value="mock/sol"]'),null);
});

for(const locale of ['en','zh-CN'])test(`Pi history distinguishes unnamed agents from legacy runs in all routing views (${locale})`,async t=>{
 const snapshot=clone(initial);
 const pi={...clone(snapshot.runs[0]),worker_run_id:'wrk_pi',logical_role:'developer',agent:'developer',runtime:'pi',runtime_agent:'',runtime_model:'magpie-openai/group/gpt6.1',status:'COMPLETED',result:{status:'completed',content:'done'}};
 const legacy={...clone(snapshot.runs[0]),worker_run_id:'wrk_legacy',runtime_agent:'',runtime_model:'',model_source:''};
 // The current configuration is OpenCode; historical Pi runs keep Pi semantics.
 const legacyDefault={...legacy,worker_run_id:'wrk_legacy_default',runtime:'opencode',model_source:'agent_default'};
 snapshot.runs=[pi,...snapshot.runs,legacy,legacyDefault];
 const u=await setup({},snapshot);t.after(()=>u.dom.window.close());
 if(locale==='zh-CN')u.click('language-toggle');
 u.nav('Runs');
 const unknown=locale==='zh-CN'?'未知 / 旧版任务':'Unknown / legacy run';
 const rows=[...u.doc.querySelectorAll('#content table tbody tr')];
 const role=locale==='zh-CN'?'完整实现':'developer';
 assert.ok(rows[0].cells[1].textContent.includes(role));
 assert.ok(rows[0].cells[1].textContent.includes(pi.runtime_model));
 assert.ok(!rows[0].cells[1].textContent.includes(unknown));
 assert.match(rows[1].cells[1].textContent,/mock\/terra/);
 assert.ok(!rows[1].cells[1].textContent.includes('fixture'),'Legacy run shows model without agent name');
 assert.ok(rows[2].cells[1].textContent.includes(unknown));
 assert.ok(rows[3].cells[1].textContent.includes(locale==='zh-CN'?'OpenCode 智能体默认模型':'OpenCode agent default'));
 assert.ok(!rows[3].cells[1].textContent.includes(locale==='zh-CN'?'Pi 默认模型':'Pi default model'));
 u.doc.querySelector('[data-run="wrk_pi"]').click();
 const detail=u.doc.getElementById('run-detail').textContent;
 assert.ok(detail.includes(role));assert.ok(detail.includes(pi.runtime_model));assert.ok(!detail.includes(unknown));
 u.doc.querySelector('[data-result="wrk_pi"]').click();
 const meta=u.doc.getElementById('result-meta').textContent;
 assert.ok(meta.includes(role));assert.ok(meta.includes(pi.runtime_model));assert.ok(!meta.includes(unknown));
});

test('Skill injection installs the missing user copy and refreshes integration status without model calls',async t=>{
 const snapshot=clone(initial);snapshot.skill={...skill,status:'missing',version:'',fingerprint:'',can_remove:false};
 const before=clone(snapshot.config);const u=await setup({},snapshot);t.after(()=>u.dom.window.close());await u.openIntegration();
 assert.equal(u.doc.getElementById('install-skill').textContent,'Inject Skill');assert.equal(u.doc.getElementById('remove-skill').disabled,true);
 assert.equal(u.calls.some(x=>x[0]==='install-skill'),false);
 const checks=u.calls.filter(x=>x[0]==='diagnostics').length;
 u.click('install-skill');await u.settle();
 assert.equal(u.calls.filter(x=>x[0]==='install-skill').length,1);
 assert.match(u.doc.getElementById('skill-section').textContent,/Installed/);
 assert.match(u.doc.getElementById('skill-section').textContent,/1\.4\.0/);
 assert.equal(u.doc.getElementById('install-skill').textContent,'Reinject Skill');assert.equal(u.doc.getElementById('remove-skill').disabled,false);
 assert.match(u.doc.querySelector('p.notice').textContent,/Skill injected/);
 assert.ok(u.calls.filter(x=>x[0]==='diagnostics').length>checks);
 assert.deepEqual(snapshot.config,before);assert.equal(u.calls.some(x=>['start','protocol','runtime','settings','binary','routings'].includes(x[0])),false);
});

test('Skill update uses the bundled version and removal requires confirmation',async t=>{
 const snapshot=clone(initial);snapshot.skill={...skill,status:'outdated',version:'0.9.0',fingerprint:'old',can_remove:true};
 const u=await setup({},snapshot);t.after(()=>u.dom.window.close());await u.openIntegration();
 assert.equal(u.doc.getElementById('install-skill').textContent,'Update Skill');u.click('install-skill');await u.settle();
 assert.match(u.doc.getElementById('skill-section').textContent,/1\.4\.0/);
 u.click('remove-skill');assert.equal(dialogOpen(u),true);assert.equal(dialogTitle(u),'Remove delegation Skill?');
 assert.match(dialogBody(u),/MCP configuration, runtime credentials and session history are kept/);
 assert.equal(u.calls.some(x=>x[0]==='remove-skill'),false);dialogCancel(u).click();assert.equal(dialogOpen(u),false);
 assert.match(u.doc.getElementById('skill-section').textContent,/Installed/);
 u.click('remove-skill');dialogOk(u).click();await u.settle();
 assert.deepEqual(u.calls.filter(x=>x[0]==='remove-skill'),[['remove-skill']]);
 assert.match(u.doc.getElementById('skill-section').textContent,/Missing/);assert.equal(u.doc.getElementById('remove-skill').disabled,true);
 assert.equal(u.doc.getElementById('install-skill').textContent,'Inject Skill');assert.match(u.doc.querySelector('p.notice').textContent,/Skill removed/);
 assert.equal(dialogOpen(u),false);assert.equal(u.doc.getElementById('integration-summary').classList.contains('attention'),true);
});

test('Skill filesystem failures stay visible and do not report success',async t=>{
 const u=await setup({InstallSkill:async()=>{throw Error('Cannot inject llmTools Skill: permission denied')},RemoveSkill:async()=>{throw Error('Cannot remove llmTools Skill: permission denied')}});
 t.after(()=>u.dom.window.close());await u.openIntegration();
 u.click('install-skill');await u.settle();assert.match(u.doc.getElementById('message').textContent,/permission denied/);assert.equal(u.doc.getElementById('install-skill').disabled,false);
 u.click('remove-skill');dialogOk(u).click();await u.settle();
 assert.equal(dialogOpen(u),true);assert.equal(dialogError(u).hidden,false);assert.match(dialogError(u).textContent,/Cannot remove/);
 assert.match(u.doc.getElementById('skill-section').textContent,/Installed/);assert.equal(dialogOk(u).disabled,false);
});

test('Skill injection prevents duplicate actions while pending',async t=>{
 const snapshot=clone(initial);snapshot.skill={...skill,status:'missing',version:'',fingerprint:'',can_remove:false};
 let release,count=0;const pending=new Promise(resolve=>release=resolve);
 const u=await setup({InstallSkill:async()=>{count++;await pending;snapshot.skill={...skill,can_remove:true};return clone(snapshot.skill)}},snapshot);
 t.after(()=>u.dom.window.close());await u.openIntegration();u.click('install-skill');
 assert.equal(u.doc.getElementById('install-skill').disabled,true);u.click('install-skill');assert.equal(count,1);
 release();await u.settle();assert.equal(count,1);assert.equal(u.doc.getElementById('install-skill').disabled,false);
});

test('Skill injection and removal controls and notices are translated',async t=>{
 const snapshot=clone(initial);snapshot.skill={...skill,status:'missing',version:'',fingerprint:'',can_remove:false};
 const u=await setup({},snapshot);t.after(()=>u.dom.window.close());u.click('language-toggle');await u.openIntegration();
 assert.equal(u.doc.getElementById('install-skill').textContent,'注入 Skill');assert.equal(u.doc.getElementById('remove-skill').textContent,'移除 Skill');
 u.click('install-skill');await u.settle();assert.equal(u.doc.getElementById('install-skill').textContent,'重新注入 Skill');assert.match(u.doc.querySelector('p.notice').textContent,/Skill 已注入/);
 u.click('remove-skill');assert.equal(dialogTitle(u),'移除委派 Skill？');assert.match(dialogBody(u),/MCP 配置、运行时凭据和会话历史会保留/);
 dialogOk(u).click();await u.settle();assert.match(u.doc.querySelector('p.notice').textContent,/Skill 已移除/);
});

const developerSnapshot=()=>{const snapshot=clone(initial);snapshot.config.agents.developer={exposed:true,agent:'fixture',model:'mock/terra'};snapshot.role_policies.developer='Project implementation and tests';return snapshot};

test('Agents and Codex Integration surfaces expose the developer role with its project implementation policy',async t=>{
 const snapshot=developerSnapshot();
 const u=await setup({},snapshot);t.after(()=>u.dom.window.close());
 u.nav('Agents');
 const card=u.doc.querySelector('.routing-card[data-role="developer"], #route-model-developer')?.closest('.routing-card')||[...u.doc.querySelectorAll('.routing-card')].find(c=>c.textContent.includes('developer'));
 assert.ok(card,'Agents page renders a developer routing card');
 assert.equal(u.doc.getElementById('route-model-developer').value,'mock/terra');
 assert.equal(u.doc.getElementById('route-agent-developer'),null);
 assert.equal(u.doc.getElementById('route-exposed-developer').checked,true);
 assert.equal(card.querySelector('dl dd').textContent,'Project implementation and tests');
 await u.openIntegration();
 const byRole=Object.fromEntries([...u.doc.querySelectorAll('#exposed-roles tbody tr')].map(r=>[r.dataset.role,r]));
 assert.ok(byRole.developer,'exposed-roles table includes developer');
 assert.match(byRole.developer.textContent,/Exposed/);
 assert.match(byRole.developer.textContent,/delegate_developer/);
 assert.equal(byRole.developer.cells[3].textContent,'Project implementation and tests');
});

test('run detail reports Project implementation and tests for a developer run with write and shell',async t=>{
 const snapshot=developerSnapshot();
 snapshot.runs[0].logical_role='developer';snapshot.runs[0].agent='developer';
 snapshot.runs[0].capabilities={repoRead:true,repoWrite:true,shell:true,web:false,subagent:false};
 const u=await setup({},snapshot);t.after(async()=>{await u.settle();u.dom.window.close()});
 u.nav('Runs');
 u.doc.querySelector('[data-run]').click();
 const text=u.doc.getElementById('run-detail').textContent;
 assert.match(text,/Execution Policy/);
 assert.match(text,/Project implementation and tests/);
 assert.equal(text.includes('Repository read-only'),false);
 assert.match(text,/developer/);
});
