import json,unittest
from codex_e2e import tool_diagnostic,stderr_summary

class Diagnostics(unittest.TestCase):
 def test_structured_failure_is_not_transport_false(self):
  r=tool_diagnostic({'tool':'ai_worker','status':'failed','error':None,'result':{'structuredContent':{'status':'failed','worker_run_id':'wrk_'+'a'*32,'error':{'code':'RUNTIME_UNAVAILABLE','message':'secret Authorization: bearer hidden'}}}})
  self.assertTrue(r['tool_result_is_error']);self.assertEqual(r['error_code'],'RUNTIME_UNAVAILABLE');self.assertEqual(r['worker_run_id'],'wrk_'+'a'*32);self.assertNotIn('secret',json.dumps(r));self.assertNotIn('Authorization',json.dumps(r))
 def test_text_and_snake_case(self):
  r=tool_diagnostic({'result':{'is_error':True,'content':[{'type':'text','text':json.dumps({'status':'failed','error':{'code':'AGENT_NOT_FOUND'}})}]}})
  self.assertTrue(r['tool_result_is_error']);self.assertEqual(r['error_code'],'AGENT_NOT_FOUND');self.assertEqual(r['is_error_source'],'wire')
 def test_unknown_is_null_not_false(self):
  r=tool_diagnostic({'error':{'message':'api key secret'},'status':'failed'})
  self.assertIsNone(r['tool_result_is_error']);self.assertNotIn('secret',json.dumps(r));self.assertTrue(r['transport_error'])
 def test_stderr_only_categories(self):
  r=stderr_summary('HTTP 401 Authorization: secret\nconnection refused')
  self.assertEqual(r['categories'],['authentication','connection_refused']);self.assertNotIn('secret',json.dumps(r))
if __name__=='__main__':unittest.main()
