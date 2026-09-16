"""No-network regression tests for cumulative model-spending reservations."""
import importlib.util
import gzip
import json
from pathlib import Path
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('acceptance',Path(__file__).with_name('evaluation-openrouter-acceptance.py'))
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)

class Response:
    status=200
    def __init__(self,data): self.data=data
    def read(self): return json.dumps(self.data).encode()

class Opener:
    def __init__(self,data): self.data,self.calls=data,0
    def open(self,*args,**kwargs):
        self.calls+=1
        if isinstance(self.data,Exception): raise self.data
        return Response(self.data)

class BudgetTest(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory()
        self.root=Path(self.tmp.name)
        self.relay=module.BudgetRelay(0,'test-placeholder',self.root,self.root/'budget.json')
        self.payload={'model':module.CHAT,'messages':[{'role':'user','content':'fixed test'}],'max_tokens':16}
    def tearDown(self):
        self.relay.server.server_close();self.relay.budget_guard.close();self.tmp.cleanup()
    def test_known_zero_is_settled(self):
        self.relay.opener=Opener({'usage':{'cost':0}})
        self.relay.forward('/v1/chat/completions',self.payload)
        record=json.loads((self.root/'budget.json').read_text())['requests'][0]
        self.assertEqual(record['actual_usd'],'0')
    def test_round_is_captured_before_network_dispatch(self):
        relay = self.relay
        class ChangingOpener:
            def open(self, *args, **kwargs):
                relay.round = 'next-request'
                return Response({'usage': {'cost': 0}})
        relay.round = 'original-request'
        relay.opener = ChangingOpener()
        relay.forward('/v1/chat/completions', self.payload)
        self.assertEqual(relay.records[0]['round'], 'original-request')
    def test_compressed_receipt_preserves_provider_cost(self):
        data = {'usage': {'cost': 0.0000014}, 'id': 'fixture'}
        class CompressedOpener:
            def open(self, *args, **kwargs):
                response = Response(data)
                response.headers = {'Content-Encoding': 'gzip'}
                response.read = lambda: gzip.compress(json.dumps(data).encode())
                return response
        self.relay.opener = CompressedOpener()
        status, raw = self.relay.forward('/v1/chat/completions', self.payload)
        self.assertEqual(status, 200)
        self.assertEqual(json.loads(raw), data)
        self.assertEqual(self.relay.records[0]['usage'], data['usage'])
    def test_unknown_transport_keeps_full_reservation(self):
        self.relay.opener=Opener(TimeoutError('fixture timeout'))
        with self.assertRaises(TimeoutError): self.relay.forward('/v1/chat/completions',self.payload)
        record=json.loads((self.root/'budget.json').read_text())['requests'][0]
        self.assertGreater(float(record['reserved_usd']),0)
        self.assertNotIn('actual_usd',record)
        self.assertEqual(record['state'], 'uncertain')
        receipt = json.loads((self.root/'supplier.jsonl').read_text())
        self.assertIsNone(receipt['status'])
        self.assertEqual(receipt['transport_error'], 'TimeoutError')
    def test_budget_rejects_before_network(self):
        self.relay.budget['requests']=[{'reserved_usd':'19.999'}]
        self.relay.opener=Opener({})
        with self.assertRaisesRegex(AssertionError,'budget exhausted'):
            self.relay.forward('/v1/chat/completions',self.payload)
        self.assertEqual(self.relay.opener.calls,0)
    def test_independent_budget_handle_cannot_take_lock(self):
        import fcntl
        with (self.root/'budget.lock').open('a') as lock:
            with self.assertRaises(BlockingIOError): fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)

class LedgerTest(unittest.TestCase):
    def setUp(self):
        self.record = {'status': 'success', 'accounting_complete': True, 'cost_microunits': 1,
                       'model_snapshot': json.dumps({'name': module.CHAT, 'billing_usage': {
                           'reported_cost': '0.0000014', 'cost_source': 'openrouter_usage_cost'}})}
        self.receipt = {'model': module.CHAT, 'status': 200, 'usage': {'cost': 0.0000014}}
    def test_failed_attempt_remains_unknown(self):
        failed = {'status': 'error', 'accounting_complete': False, 'cost_microunits': None}
        result = module.validate_paid_ledger([self.record, failed], [self.receipt, {'status': None}])
        self.assertEqual(result, {'known_cost_microunits': 1, 'unknown_cost_attempts': 1, 'failed_attempts': 1})
    def test_unknown_must_not_be_zero(self):
        with self.assertRaisesRegex(AssertionError, 'remain null'):
            module.validate_paid_ledger([{'status': 'error', 'accounting_complete': False,
                                          'cost_microunits': 0}], [{'status': None}])
    def test_receipt_cannot_be_reused(self):
        with self.assertRaisesRegex(AssertionError, 'matching supplier'):
            module.validate_paid_ledger([self.record, self.record], [self.receipt, {'status': None}])
    def test_unknown_success_requires_matching_missing_usage_receipt(self):
        row = dict(self.record, accounting_complete=False, cost_microunits=None,
                   model_snapshot=json.dumps({'name': module.CHAT, 'billing_usage': {'usage_reported': False}}))
        with self.assertRaisesRegex(AssertionError, 'unreported supplier'):
            module.validate_paid_ledger([row], [self.receipt])
        result = module.validate_paid_ledger([row], [{'model': module.CHAT, 'status': 200, 'usage': {}}])
        self.assertEqual(result['unknown_cost_attempts'], 1)
        self.assertEqual(result['known_cost_microunits'], 0)

if __name__=='__main__': unittest.main()
