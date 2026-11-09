import atexit, hashlib, tempfile, unittest
from pathlib import Path
_store = tempfile.TemporaryDirectory();atexit.register(_store.cleanup)
_receipt = Path(_store.name)/"receipt.json";_receipt.write_text("{\"fixture\":true}\n")
_receipt_sha = hashlib.sha256(_receipt.read_bytes()).hexdigest()
from compare_candidates import compare, GATES, IDENTITY

def candidate(name='measured'):
    return {'name':name,'gates':dict.fromkeys(GATES, True),'measurement_receipt':str(_receipt),'measurement_receipt_sha256':_receipt_sha,'evidence':dict(binary_sha256='a'*64,model_sha256='b'*64,**{k:('c'*64 if k.endswith('sha256') else 1) for k in IDENTITY}),'metrics':{'task_success_rate':.95,'citation_precision':1.,'ttft_p95_s':3.,'peak_host_rss_mib':2048,'samples':100}}
class Contract(unittest.TestCase):
    def test_danger_failure_cannot_win_by_speed(self):
        a=candidate('unsafe');a['gates']['critical_semantics']=False;a['metrics']['ttft_p95_s']=.01
        self.assertEqual(compare([a,candidate('safe')],'model')['order'],['safe'])
    def test_unmeasured_or_missing_identity_not_promoted(self):
        a=candidate();del a['evidence']['model_sha256'];self.assertEqual(compare([a],'model')['status'],'NO_QUALIFIED_CANDIDATE')
    def test_prompt_changed_in_model_ab_is_not_comparable(self):
        a=candidate();b=candidate('other');b['evidence']['prompt_sha256']='d'*64
        self.assertEqual(compare([a,b],'model')['status'],'NOT_COMPARABLE')
    def test_cache_changed_in_model_ab_is_not_comparable(self):
        a=candidate();b=candidate('other');b['evidence']['cache_class']='warm'
        self.assertEqual(compare([a,b],'model')['status'],'NOT_COMPARABLE')
    def test_nan_and_tiny_samples_rejected(self):
        for value in [float('nan'),float('inf'),True]:
            a=candidate();a['metrics']['ttft_p95_s']=value;self.assertFalse(compare([a],'model')['assessments'][0]['eligible'])
        a=candidate();a['metrics']['samples']=4;self.assertFalse(compare([a],'model')['assessments'][0]['eligible'])
    def test_missing_or_tampered_receipt_rejected(self):
        a=candidate();a['measurement_receipt_sha256']='0'*64
        self.assertEqual(compare([a],'model')['status'],'NO_QUALIFIED_CANDIDATE')
        a=candidate();a['measurement_receipt']='/nonexistent/must-not-pass.json'
        self.assertEqual(compare([a],'model')['status'],'NO_QUALIFIED_CANDIDATE')
    def test_model_change_hidden_in_prompt_ab_rejected(self):
        a=candidate();b=candidate('other');b['evidence']['model_sha256']='d'*64
        self.assertEqual(compare([a,b],'prompt_sha256')['status'],'NOT_COMPARABLE')
    def test_empty_candidates_cannot_claim_optimum(self):
        self.assertIsNone(compare([],'model')['recommendation'])
    def test_observed_order_never_claims_global_optimum(self):
        result=compare([candidate()],'model');self.assertEqual(result['status'],'MEASURED_ORDER_ONLY');self.assertIsNone(result['recommendation'])
if __name__=='__main__':unittest.main()
