import unittest,copy
from model_regression_gate import compare
class Tests(unittest.TestCase):
 def runpair(self,n=10):
  a={'manifest':{k:'a'*64 for k in ['task_set_sha256','rubric_sha256','harness_sha256','system_prompt_sha256']},'cases':[{'id':str(i),'passed':i!=1} for i in range(n)]};return a,copy.deepcopy(a)
 def test_no_trade(self):
  a,b=self.runpair();b['cases'][0]['passed']=False;b['cases'][1]['passed']=True;r=compare(a,b);self.assertEqual(r['decision'],'BLOCK_REGRESSION');self.assertEqual(r['improvements'],['1']);self.assertFalse(r['promotion_allowed'])
 def test_small_set(self):a,b=self.runpair(8);self.assertEqual(compare(a,b)['decision'],'INCONCLUSIVE_SMALL_SET')
 def test_identity(self):
  for k in ['task_set_sha256','rubric_sha256','harness_sha256','system_prompt_sha256']:
   a,b=self.runpair();b['manifest'][k]='b'*64;self.assertEqual(compare(a,b)['decision'],'INVALID_IDENTITY')
 def test_case_order_missing_duplicate(self):
  a,b=self.runpair();b['cases'].reverse();self.assertEqual(compare(a,b)['decision'],'INVALID_IDENTITY')
  b['cases'][0]['id']=b['cases'][1]['id']
  with self.assertRaises(ValueError):compare(a,b)
 def test_pending_and_selfgrade(self):
  a,b=self.runpair();b['cases'][0]['passed']=None
  with self.assertRaises(ValueError):compare(a,b)
  a,b=self.runpair();b['manifest']['candidate_self_graded']=True;self.assertFalse(compare(a,b)['promotion_allowed'])
 def test_equal_valid_only_regression_gate(self):a,b=self.runpair();self.assertEqual(compare(a,b)['decision'],'ALLOW_REGRESSION_GATE_ONLY')
if __name__=='__main__':unittest.main(verbosity=2)
