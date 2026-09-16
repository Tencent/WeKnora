"""Production adapter cache probes with persistent shared USD reservations."""
import importlib.util
import argparse
import json
import os
from pathlib import Path
import subprocess
from decimal import Decimal, ROUND_HALF_UP

spec = importlib.util.spec_from_file_location('acceptance', Path(__file__).with_name('evaluation-openrouter-acceptance.py'))
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=Path('/evidence/probes-01'))
    parser.add_argument('--budget-file', type=Path, default=Path('/evidence/budget.json'))
    parser.add_argument('--probe-binary', type=Path, default=Path('/app/.local-service/bin/evaluation-provider-probe'))
    parser.add_argument('--batch-id', default='acceptance-20260909', help='Independent shared-prefix namespace')
    parser.add_argument('--skip-embedding', action='store_true', help='Run only the Wiki paired experiment')
    args = parser.parse_args()
    credentials = json.loads(input())
    assert credentials.get('approved_usd') == 20
    root = args.output; root.mkdir()
    relay = module.BudgetRelay(18810, credentials['openrouter_key'], root, args.budget_file)
    relay.provider_only = 'gmicloud/fp8'
    prices = {}
    for route in ('models','embeddings/models'):
        data = json.loads(relay.opener.open('https://openrouter.ai/api/v1/'+route, timeout=30).read())
        module.save(root/(route.replace('/','-')+'.json'),data)
        prices.update({m['id']:m['pricing'] for m in data['data']})
    def price(model):
        p = prices[model]
        def micro(key): return int((Decimal(str(p[key]))*10**12).quantize(Decimal(1),rounding=ROUND_HALF_UP))
        out = {'input_microunits_per_million':micro('prompt'),'output_microunits_per_million':micro('completion')}
        if 'input_cache_read' in p: out['cache_pricing']={'version':1,'read_microunits_per_million':micro('input_cache_read')}
        return out
    common = {'database':str(root/'probes.sqlite'),'base_url':'http://127.0.0.1:18810/v1',
              'chat_price':price(module.CHAT),'embedding_price':price(module.EMBED)}
    # Thirty previously unseen pages, alternating order, same facts within each
    # pair. Fixed system template and shared context come through the production
    # Wiki renderer. Cache benefit is an observation, never a pass condition.
    shared = f'Experiment namespace: {args.batch_id}.\n' + ''.join(f'Context {i:03d}: This synthetic catalog defines document scope and provenance; context identifiers are framing only and never factual evidence.\n' for i in range(96))
    wiki = []
    for pair in range(1,31):
        arms = ['stable-prefix','page-first'] if pair%2 else ['page-first','stable-prefix']
        data = {'HasAdditions':'true','SharedSourceContexts':shared,'PageSlug':f'acceptance-room-{pair}',
                'PageTitle':f'Acceptance room {pair}','PageType':'room','ExistingContent':'','AvailableSlugs':'','Language':'English',
                'NewContent':f'Acceptance room {pair} contains {100+pair} seats. Its catalog identifier is ACC-{pair:03d}. It opens at 09:00 and closes at 17:00. The source does not specify days of operation.'}
        wiki.extend({'step':f'wiki-{pair:02d}-{arm}','arm':arm,'data':data,'pair':pair} for arm in arms)
    module.save(root/'plan.json',{'pairs':30,'batch_id':args.batch_id,'binary_sha256':module.offline.digest(args.probe_binary.read_bytes()),'order':'alternating','provider_only':'gmicloud/fp8','cache_benefit_required':False,'steps':wiki,
                 'quality_checks':['seat count','catalog identifier','opening hours','no invented daily schedule']})
    results = []
    def run(step):
        relay.round = step['step']
        before = len(relay.records)
        proc = subprocess.run([str(args.probe_binary)],input=json.dumps(common|step).encode('utf-8'),capture_output=True,timeout=120,env=os.environ|{'GOLANG_PROTOBUF_REGISTRATION_CONFLICT':'warn'})
        stderr = proc.stderr.decode('utf-8',errors='replace')
        (root/(step['step']+'.log')).write_text(stderr,encoding='utf-8')
        assert proc.returncode==0, (step['step'],stderr[-2000:])
        # Production logs may precede the final JSON line.
        response = json.loads(proc.stdout.splitlines()[-1].decode('utf-8'))
        attempts = relay.records[before:]
        assert len(response['ledger'])==len(attempts)
        for row, receipt in zip(response['ledger'],attempts):
            assert row['accounting_complete'] and row['status']=='success' and receipt['status']==200
            expected = int((Decimal(str(receipt['usage']['cost']))*10**6).quantize(Decimal(1),rounding=ROUND_HALF_UP))
            assert row['cost_microunits']==expected
        if step.get('inputs'):
            response['vectors_sha256'] = module.offline.digest(json.dumps(response.pop('result')).encode())
        else:
            text = response['result']; pair=step['pair']
            response['fact_checks'] = {'seats':str(100+pair) in text,'catalog':f'ACC-{pair:03d}' in text,
              'hours':'09:00' in text and '17:00' in text,'no_daily_claim':not any(term in text.lower() for term in ('daily','every day','each day','seven days'))}
        response['provider_attempts']=len(attempts)
        results.append(response); module.save(root/'results.json',results)
        print(json.dumps({'completed':step['step'],'provider_attempts':len(attempts)}),flush=True)
        return response
    relay.start()
    try:
        if not args.skip_embedding:
            texts = ['Acceptance cache room A has 17 seats.','Acceptance cache room B has 29 seats.']
            cold=run({'step':'embedding-cold','inputs':texts})
            warm=run({'step':'embedding-restart-warm','inputs':texts})
            changed=run({'step':'embedding-content-change','inputs':[texts[0],texts[1].replace('29','31')]})
            assert cold['provider_attempts']>0 and warm['provider_attempts']==0 and changed['provider_attempts']>0
            assert cold['vectors_sha256']==warm['vectors_sha256']!=changed['vectors_sha256']
            mutation=[r for r in relay.records if r['round']=='embedding-content-change']
            assert sum(len(r['input_sha256']) for r in mutation)==1, 'Only changed content must reach provider'
        for step in wiki: run(step)
        module.save(root/'status.json',{'status':'passed','cache_content_invalidation':None if args.skip_embedding else True,'wiki_pairs':30,'human_review_status':'excluded'})
    finally:
        relay.close()
        module.save(root/'relay-errors.json',relay.errors)

if __name__=='__main__': main()
