"""Download and validate official development sets; freeze disjoint test cases."""
import argparse, collections, concurrent.futures, hashlib, importlib.util, json, shutil, subprocess
from pathlib import Path
from urllib.request import Request, build_opener, ProxyHandler

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('public',ROOT/'scripts/prepare-public-evaluation.py')
public=importlib.util.module_from_spec(spec);spec.loader.exec_module(public)
SEED='20260911-excellence-v1'
def sha(b):return hashlib.sha256(b).hexdigest()
def save(p,x):p.write_text(json.dumps(x,ensure_ascii=False,indent=2),encoding='utf-8',newline='\n')
def rank(q):return sha((SEED+q['qid']).encode())

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--per-dataset',type=int,default=200)
    args=parser.parse_args();out=args.output;out.mkdir(parents=True,exist_ok=True)
    raw=out/'sources';raw.mkdir(exist_ok=True)
    sources={k:public.SOURCES[k] for k in ('cmrc2018-dev.json','squad2-dev.json','cmrc2018-LICENCE','squad2-home.html')}
    sources['hotpot-dev.parquet']={'url':'https://huggingface.co/datasets/hotpotqa/hotpot_qa/resolve/1908d6afbbead072334abe2965f91bd2709910ab/distractor/validation-00000-of-00001.parquet',
       'revision':'1908d6afbbead072334abe2965f91bd2709910ab','sha256':'c20b638ca82b21d04fe12e14ff417ad05153d4d215a65de54497fca4e972f7c6','original_unavailable':'http://curtis.ml.cmu.edu/datasets/hotpot/hotpot_dev_distractor_v1.json'}
    sources['hotpot-home.html']={'url':'https://hotpotqa.github.io/'}
    def fetch(item):
        name,meta=item;p=raw/name
        if not p.exists():
            try:
                with build_opener(ProxyHandler({})).open(Request(meta['url'],headers={'User-Agent':'WeKnora-acceptance/1'}),timeout=90) as r:b=r.read(100*1024*1024+1)
            except OSError:
                # Windows curl uses the host certificate store; validate the
                # pinned content digest after either download mechanism.
                curl=shutil.which('curl.exe') or shutil.which('curl')
                if not curl:raise
                result=subprocess.run([curl,'-L','--fail','--connect-timeout','15','--max-time','120',
                    '--max-filesize',str(100*1024*1024),meta['url']],capture_output=True,check=True)
                b=result.stdout
            assert len(b)<=100*1024*1024
            if 'sha256' in meta:assert sha(b)==meta['sha256']
            p.write_bytes(b)
        b=p.read_bytes()
        if 'sha256' in meta:assert sha(b)==meta['sha256']
        return name,dict(meta,sha256=sha(b),bytes=len(b))
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        sources=dict(pool.map(fetch,sources.items()))
    save(out/'sources.json',sources)
    profiles={};all_cases={}
    for dataset,filename in [('cmrc','cmrc2018-dev.json'),('squad','squad2-dev.json')]:
        d=json.loads((raw/filename).read_text(encoding='utf-8'));cases=[];bad=[];ids=[]
        for article in d['data']:
            for paragraph in article['paragraphs']:
                context=paragraph['context']
                for q in paragraph['qas']:
                    ids.append(q['id'])
                    if not public.valid_question(q,context):bad.append(q['id']);continue
                    cases.append({'qid':q['id'],'dataset':dataset,'group':sha(article['title'].encode()),'question':q['question'],
                      'answers':[a['text'] for a in q.get('answers',[])],'contexts':[[article['title'],context]],
                      'type':'unanswerable' if q.get('is_impossible') else 'answerable'})
        assert len(ids)==len(set(ids))
        profiles[dataset]={'total':len(ids),'valid':len(cases),'excluded_invalid_span_or_schema':bad,'duplicate_ids':0}
        all_cases[dataset]=cases
    import pyarrow.parquet as pq
    data=[]
    for row in pq.read_table(raw/'hotpot-dev.parquet').to_pylist():
        data.append(dict(row,_id=row['id'],context=list(zip(row['context']['title'],row['context']['sentences'])),
                         supporting_facts=list(zip(row['supporting_facts']['title'],row['supporting_facts']['sent_id']))))
    cases=[];bad=[]
    assert len(data)==len({q['_id'] for q in data})
    for q in data:
        contexts=dict(q['context'])
        if not q['question'].strip() or not q['answer'].strip() or not q['supporting_facts'] or any(t not in contexts or i<0 or i>=len(contexts[t]) for t,i in q['supporting_facts']):
            bad.append(q['_id']);continue
        cases.append({'qid':q['_id'],'dataset':'hotpot','group':q['_id'],'question':q['question'],
          'answers':[q['answer']],'contexts':[[t,''.join(ss)] for t,ss in q['context']],
          'supporting_facts':q['supporting_facts'],'type':q['type']})
    all_cases['hotpot']=cases
    profiles['hotpot']={'total':len(data),'valid':len(cases),'excluded_invalid_support_or_schema':bad,'duplicate_ids':0}
    # No raw content from previous inspected experiments can enter the holdout.
    old_contexts=set()
    for p in (ROOT/'dataset/public').glob('*/v1/registry-input.json'):
        old_contexts.update(sha(x['content'].encode()) for x in json.loads(p.read_text(encoding='utf-8')).get('passages',[]))
    frozen=[];tuning=[]
    for dataset,cases in all_cases.items():
        cases=sorted(cases,key=rank)
        tune=[];groups=set();context_hashes=set(old_contexts)
        for q in cases:
            if any(sha(c.encode()) in old_contexts for _,c in q['contexts']):continue
            tune.append(q);groups.add(q['group']);context_hashes.update(sha(c.encode()) for _,c in q['contexts'])
            if len(tune)==32:break
        candidates=[q for q in cases if q['group'] not in groups and not any(sha(c.encode()) in context_hashes for _,c in q['contexts'])]
        types=sorted({q['type'] for q in candidates});selected=[]
        for kind in types:
            selected.extend([q for q in candidates if q['type']==kind][:args.per_dataset//len(types)])
        assert len(selected)==args.per_dataset,(dataset,len(selected))
        assert not ({q['qid'] for q in tune}&{q['qid'] for q in selected})
        profiles[dataset].update(tuning=len(tune),test=len(selected),test_types=dict(collections.Counter(q['type'] for q in selected)),
          test_contexts=len({sha(c.encode()) for q in selected for _,c in q['contexts']}))
        tuning.extend(tune);frozen.extend(sorted(selected,key=rank))
    save(out/'quality.json',profiles);save(out/'tuning.json',tuning);save(out/'holdout.json',frozen)
    save(out/'plan.json',{'seed':SEED,'license':'CC-BY-SA-4.0','human_review':'excluded by user; upstream labels retained',
      'selection':'All reference spans/support indices validated; previous inspected contexts excluded; tuning and test source groups and context bytes disjoint.',
      'quality_sha256':sha((out/'quality.json').read_bytes()),'holdout_sha256':sha((out/'holdout.json').read_bytes()),
      'tuning_sha256':sha((out/'tuning.json').read_bytes()),'test_cases':len(frozen),
      'limitations':['Public development data may be present in model pretraining; source annotations are not infallible.',
                      'Stratified filtered sample is not an official full-development benchmark result.',
                      'SQuAD no-answer labels apply only to their original context.']})
    print(json.dumps(profiles,ensure_ascii=False))
if __name__=='__main__':main()
