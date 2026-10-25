import json
import unittest
from discovery_e2e import FIXTURES, data_of, evaluate

class DiscoveryEvidence(unittest.TestCase):
 def test_skill_inventory_prompt_does_not_name_the_skill(self):
  self.assertNotIn('llmtools-delegation',FIXTURES['skill'])
  self.assertNotIn('delegate_',FIXTURES['skill'])
 def test_selection_prompts_do_not_name_tools(self):
  for name in ['skill','semantic','natural','negative','trivial','review','research','context','decision']:
   for forbidden in ['ai_worker','ai-worker','MCP']:
    self.assertNotIn(forbidden,FIXTURES[name])
 def test_natural_prompt_does_not_request_delegation(self):
  for word in ['llmTools','外部','委派','agent','delegate']:
   self.assertNotIn(word,FIXTURES['natural'])
 def test_semantic_role_prompts_request_external_role_without_tool_name(self):
  for name,role in [('semantic','implementation'),('review','review'),('research','research')]:
   self.assertIn('外部',FIXTURES[name])
   self.assertIn(role,FIXTURES[name])
   for forbidden in ['llmTools','ai_worker','ai-worker','MCP']:
    self.assertNotIn(forbidden,FIXTURES[name])
 def test_review_and_research_accept_real_completed_content_without_test_token(self):
  for name in ['review','research']:
   report={'exit_code':0,'tools':[{'tool':'delegate_'+name,'arguments':{},'result':{'worker_run_id':'wrk_fixture','status':'completed','content':'independent result'}}],'run_snapshots':{'wrk_fixture':[{'capabilities':{'repoRead':False,'repoWrite':False,'shell':False,'web':False,'subagent':False}}]}}
   self.assertEqual('PASS',evaluate(name,report))
 def test_caller_capabilities_are_rejected_by_evidence_gate(self):
  report={'exit_code':0,'tools':[{'tool':'delegate_review','arguments':{'capabilities':{}},'result':{'worker_run_id':'wrk_fixture','status':'completed','content':'result'}}],'run_snapshots':{'wrk_fixture':[{'capabilities':{}}]}}
  self.assertEqual('CALLER_CAPABILITIES_REGRESSION',evaluate('review',report))
 def test_approval_failure_cannot_be_success_token(self):
  self.assertEqual({},data_of({'result':None,'error':{'message':'approval required'},'text':'EXPLICIT_DELEGATION_OK'}))
 def test_final_message_does_not_override_failed_tool(self):
  r={'exit_code':0,'messages':['EXPLICIT_DELEGATION_OK'],'tools':[{'tool':'delegate_implementation','arguments':{},'result':{},'error':{'message':'approval required'}}]}
  self.assertEqual('TOOL_EXECUTION_BLOCKED',evaluate('explicit',r))
 def test_completed_token_without_worker_is_not_pass(self):
  self.assertEqual('FAIL',evaluate('explicit',{'exit_code':0,'messages':['EXPLICIT_DELEGATION_OK'],'tools':[]}))
 def test_replacement_run_fails_continuation(self):
  r={'exit_code':0,'tools':[{'tool':'delegate_implementation','arguments':{},'result':{'status':'needs_context'}},{'tool':'delegate_implementation','arguments':{},'result':{'status':'completed','content':'CONTEXT_DISCOVERY_OK'}}]}
  self.assertEqual('FAIL',evaluate('context',r))
 def test_result_encodings(self):
  result={'worker_run_id':'wrk_fixture','status':'completed','content':'OK'}
  self.assertEqual(result,data_of({'result':{'structuredContent':result}}))
  self.assertEqual(result,data_of({'result':{'structured_content':result}}))
  self.assertEqual(result,data_of({'result':{'content':[{'type':'text','text':json.dumps(result)}]}}))
if __name__=='__main__': unittest.main()
