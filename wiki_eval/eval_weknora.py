
#!/usr/bin/env python3
from __future__ import annotations
import argparse,json,os,re,sys,time
from datetime import datetime
from pathlib import Path
from typing import Any,Dict,List,Iterable,Optional,Tuple
from urllib.parse import quote
try:
    import requests
except Exception:
    requests=None
try:
    import yaml
except Exception:
    yaml=None

DEFAULT_KB={"name":"wiki-eval-stardust","type":"document","description":"Automated synthetic wiki evaluation corpus: Stardust Program","indexing_strategy":{"vector_enabled":True,"keyword_enabled":True,"wiki_enabled":True,"graph_enabled":True},"wiki_config":{"extraction_granularity":"standard","ingest_batch_size":5,"ingest_map_parallel":4,"ingest_reduce_parallel":4}}
DONE=("success","completed","complete","done","finished","ready","published")
PROC=("pending","processing","parsing","indexing","running","queued","created")
FAIL=("failed","error","cancel","timeout")

def stamp(): return datetime.now().strftime('%Y%m%d_%H%M%S')
def rjson(p:Path,d=None): return json.loads(p.read_text(encoding='utf-8-sig')) if p.exists() else d
def wjson(p:Path,o:Any): p.parent.mkdir(parents=True,exist_ok=True); p.write_text(json.dumps(o,ensure_ascii=False,indent=2),encoding='utf-8')
def load_cfg(p:Optional[Path]):
    if not p or not p.exists(): return {}
    s=p.read_text(encoding='utf-8')
    if p.suffix.lower()=='.json': return json.loads(s)
    if yaml is None: raise RuntimeError('PyYAML not installed; install requirements.txt or use JSON config')
    return yaml.safe_load(s) or {}
def merge(a:Dict[str,Any],b:Dict[str,Any]):
    o=dict(a)
    for k,v in b.items(): o[k]=merge(o[k],v) if isinstance(v,dict) and isinstance(o.get(k),dict) else v
    return o
def unwrap(x):
    cur=x
    for k in ('data','result','items','list','pages','records'):
        if isinstance(cur,dict) and k in cur: cur=cur[k]
    return cur
def flat(x):
    if x is None: return ''
    if isinstance(x,str): return x
    if isinstance(x,(int,float,bool)): return str(x)
    if isinstance(x,list): return '\n'.join(flat(i) for i in x)
    if isinstance(x,dict): return '\n'.join(str(k)+'\n'+flat(v) for k,v in x.items() if str(k).lower() not in {'embedding','vector'})
    return str(x)
def _count_items(x):
    """递归统计嵌套结构中的条目数（叶子/条目视角）：
    - list：递归累加各元素；
    - dict：若含 list/dict 值则递归求和（即统计内部集合条目），纯标量字典计 1 个条目，空字典计 0；
    - 标量：计 1。
    用于 /wiki/lint、/wiki/issues 等返回结构未知时的稳定条数统计，避免嵌套导致低估。"""
    if isinstance(x, list):
        return sum(_count_items(i) for i in x)
    if isinstance(x, dict):
        nested = [v for v in x.values() if isinstance(v, (list, dict))]
        return sum(_count_items(v) for v in nested) if nested else (1 if x else 0)
    return 1
def slugify(s):
    s=re.sub(r'[^a-z0-9\u4e00-\u9fff]+','-',s.lower().strip())
    return re.sub(r'-+','-',s).strip('-')
def norm(s): return str(s or '').strip().strip('/').lower()
def has(t,txt): return str(t).lower() in txt.lower()
def allhas(ts,txt): return all(has(t,txt) for t in ts)
def avg(vs:Iterable[Any]):
    v=[1 if bool(x) else 0 for x in vs]; return sum(v)/len(v) if v else 0.0
def navg(vs:Iterable[Any]):
    v=[float(x) for x in vs if isinstance(x,(int,float)) and not isinstance(x,bool)]; return sum(v)/len(v) if v else 0.0
def first(*xs):
    for x in xs:
        if x not in (None,''): return x
    return None
class C:
    def __init__(self,base,prefix='/api/v1',timeout=60,headers=None):
        if requests is None: raise RuntimeError('requests not installed; run: pip install -r requirements.txt')
        self.base=base.rstrip('/'); self.prefix='/' + prefix.strip('/') if prefix else ''; self.timeout=timeout; self.s=requests.Session(); self.s.headers.update({'Accept':'application/json'}); self.s.headers.update(headers or {})
    def url(self,path,api=True): return self.base+(self.prefix if api else '')+'/'+path.strip('/')
    def req(self,m,path,api=True,**kw):
        u=self.url(path,api); r=self.s.request(m,u,timeout=self.timeout,**kw)
        try: body=r.json()
        except Exception: body=r.text
        if r.status_code>=400: raise requests.HTTPError(f'{m} {u} -> {r.status_code}: {body}',response=r)
        return body
    def get(self,p,**kw): return self.req('GET',p,**kw)
    def post(self,p,**kw): return self.req('POST',p,**kw)
    def delete(self,p,**kw): return self.req('DELETE',p,**kw)
class Page:
    def __init__(self,slug,title,content,raw): self.slug=str(slug); self.title=str(title); self.content=str(content or ''); self.raw=raw
    @property
    def nslug(self): return norm(self.slug)
    @property
    def text(self): return f'{self.slug}\n{self.title}\n{self.content}\n{flat(self.raw)}'
def mlist(x):
    if isinstance(x,list): return x
    if isinstance(x,dict):
        for k in ('data','items','list','pages','records','results'):
            v=x.get(k)
            if isinstance(v,list): return v
            if isinstance(v,dict):
                y=mlist(v)
                if y: return y
    return []
def pages(resp):
    out=[]
    for it in mlist(resp):
        if not isinstance(it,dict): continue
        sg=first(it.get('slug'),it.get('path'),it.get('page_slug'),it.get('id'),it.get('name'))
        ti=first(it.get('title'),it.get('name'),it.get('slug'),sg) or ''
        co=first(it.get('content'),it.get('markdown'),it.get('body'),it.get('summary'),it.get('text')) or ''
        out.append(Page(sg or slugify(str(ti)),ti,co,it))
    if not out and isinstance(unwrap(resp),dict):
        it=unwrap(resp); sg=first(it.get('slug'),it.get('path'),it.get('page_slug'),it.get('id'),it.get('name')); ti=first(it.get('title'),it.get('name'),it.get('slug'),sg) or ''; co=first(it.get('content'),it.get('markdown'),it.get('body'),it.get('summary'),it.get('text')) or ''
        if sg or ti or co: out=[Page(sg or slugify(str(ti)),ti,co,it)]
    return out
def xid(resp):
    d=unwrap(resp)
    if isinstance(d,dict):
        for k in ('id','knowledge_base_id','kb_id','knowledge_id','session_id'):
            if d.get(k): return str(d[k])
    return None
def xstatus(resp):
    t=flat(resp).lower()
    if any(w in t for w in FAIL): return 'failed'
    if any(w in t for w in DONE): return 'done'
    if any(w in t for w in PROC): return 'processing'
    return 'unknown'
def headers(cfg):
    h=dict(cfg.get('headers',{}) or {})
    tok=os.getenv('WEKNORA_TOKEN') or cfg.get('token')
    key=os.getenv('WEKNORA_API_KEY') or cfg.get('api_key')
    kh=os.getenv('WEKNORA_API_KEY_HEADER') or cfg.get('api_key_header') or 'X-API-Key'
    if tok: h['Authorization']='Bearer '+tok
    if key: h[kh]=key
    return h
def create_kb(c,cfg,run):
    body=merge(DEFAULT_KB,cfg.get('knowledge_base',{})); body['name']=f"{body.get('name','wiki-eval-stardust')}-{run}"
    kb_cfg=cfg.get('knowledge_base',{}) or {}
    em=kb_cfg.get('embedding_model_id') or os.getenv('WEKNORA_EMBEDDING_MODEL_ID')
    sm=kb_cfg.get('summary_model_id') or os.getenv('WEKNORA_SUMMARY_MODEL_ID')
    wm=kb_cfg.get('wiki_synthesis_model_id') or os.getenv('WEKNORA_WIKI_SYNTHESIS_MODEL_ID')
    if em: body['embedding_model_id']=em
    if sm: body['summary_model_id']=sm
    if wm:
        body.setdefault('wiki_config',{})['synthesis_model_id']=wm
    r=c.post('/knowledge-bases',json=body); kid=xid(r)
    if not kid: raise RuntimeError('no kb id in '+json.dumps(r,ensure_ascii=False)[:1000])
    return kid
def ingest(c,kb,docs:Path):
    made=[]
    for p in sorted(docs.glob('*.md')):
        r=c.post(f'/knowledge-bases/{kb}/knowledge/manual',json={'title':p.name,'content':p.read_text(encoding='utf-8'),'status':'publish'})
        kid=xid(r); print(f'[ingest] {p.name} -> {kid}'); made.append({'doc':p.name,'knowledge_id':kid,'response':r})
    return made


def _refs_of(page):
    return [str(r).split('|')[0] for r in (getattr(page, 'raw', {}) or {}).get('source_refs') or []]

def _links_of(page, field):
    return [str(r) for r in (getattr(page, 'raw', {}) or {}).get(field) or []]

def _pages_by_slug(pages_list):
    return {p.nslug: p for p in pages_list}

def _equivalent_slugs(case, slug):
    impact = case.get('expected_impact', {}) or {}
    equivalents = impact.get('equivalent_pages', {}) or {}
    candidates = [slug]
    if isinstance(equivalents, dict):
        candidates.extend(equivalents.get(slug, []) or equivalents.get(norm(slug), []) or [])
    return [norm(s) for s in candidates if s]

def _find_expected_page(by_slug, slug, case):
    for candidate in _equivalent_slugs(case, slug):
        if candidate in by_slug:
            return by_slug[candidate]
    return None

def _delete_link_metrics(ps, target_id, target_doc, remove_links):
    stale_link_pages = []
    stale_link_token = f'summary/{target_id}'
    for p in ps:
        stale_links = [x for x in (_links_of(p, 'in_links') + _links_of(p, 'out_links')) if target_id in x or stale_link_token in x]
        if stale_links:
            stale_link_pages.append({'slug': p.slug, 'stale_links': stale_links})
    resolved_links = [str(x).replace('{target_knowledge_id}', target_id).replace('{target_doc_id}', str(target_doc)) for x in (remove_links or [])]
    after_link_blob = '\n'.join(flat(_links_of(p, 'in_links')) + '\n' + flat(_links_of(p, 'out_links')) for p in ps)
    return {
        'must_remove_in_links_rate': (sum(1 for link in resolved_links if link and link not in after_link_blob) / len(resolved_links)) if resolved_links else None,
        'stale_inlink_count': sum(len(x['stale_links']) for x in stale_link_pages),
        'stale_inlink_page_rate': len(stale_link_pages) / len(ps) if ps else 0.0,
        'stale_inlink_pages': stale_link_pages,
    }

def wait_deleted(c,kb,kid,raw,timeout=900,interval=10):
    print(f'[wait-delete] timeout={timeout}s')
    start=time.time(); last=-1; stable=0; i=0
    while time.time()-start<timeout:
        i+=1
        gone=False
        try:
            c.get(f'/knowledge/{kid}')
        except Exception:
            gone=True
        try:
            pc=len(pages(c.get(f'/knowledgebase/{kb}/wiki/pages',params={'limit':500})))
        except Exception:
            pc=-1
        wjson(raw/f'poll_{i:03d}.json',{'gone':gone,'page_count':pc})
        print(f'[wait-delete] poll={i} gone={gone} pages={pc}')
        stable = stable+1 if gone and pc==last and pc>=0 else 0
        last = pc
        if stable>=2:
            return
        time.sleep(interval)
    print('[wait-delete] timeout; continuing')

def eval_delete(c,kb,dataset,report,cfg,run,timeout_sec=900,interval_sec=10,min_pages=5):
    gold = dataset/'gold'
    cases = rjson(gold/'delete_events.json',[])
    if isinstance(cases, dict):
        cases = [cases]
    if not cases:
        return {'metrics':{},'delete_eval':[]}
    del_raw = report/'raw'/'delete'
    del_raw.mkdir(parents=True,exist_ok=True)
    kb_del = create_kb(c,cfg,f'{run}-delete')
    print(f'[delete] using kb {kb_del}')
    made = ingest(c,kb_del,dataset/'docs_v1')
    wjson(del_raw/'created_knowledge_v1.json',made)
    wait_wiki(c,kb_del,[x.get('knowledge_id') for x in made if x.get('knowledge_id')],del_raw/'wait',min_pages,timeout_sec,interval_sec)
    kid_by_doc = {x['doc']: x['knowledge_id'] for x in made if x.get('doc') and x.get('knowledge_id')}
    rows = []
    for case in cases:
        case_raw = del_raw/case.get('event_id','delete_case')
        case_raw.mkdir(parents=True,exist_ok=True)
        target_doc = case.get('target_doc_id')
        impact = case.get('expected_impact',{}) or {}
        target_id = kid_by_doc.get(target_doc)
        if not target_id:
            rows.append({'event_id':case.get('event_id'),'target_doc_id':target_doc,'error':'target doc not ingested','source_ref_cleanup_rate':None,'must_remove_source_refs_rate':None,'must_remove_in_links_rate':None,'must_remove_terms_absence_rate':None,'expected_deleted_page_absence_rate':None,'keep_page_presence_rate':None,'keep_page_unchanged_rate':None,'stale_inlink_count':None,'stale_inlink_page_rate':None,'false_del_rate':None,'idempotent_retract':False})
            continue
        s0 = fetch_pages(c,kb_del,case_raw/'before')
        s0_by_slug = _pages_by_slug(s0)
        pages_with_target_s0 = [p for p in s0 if target_id in _refs_of(p)]
        try:
            c.delete(f'/knowledge/{target_id}')
        except Exception as e:
            rows.append({'event_id':case.get('event_id'),'target_doc_id':target_doc,'error':str(e),'source_ref_cleanup_rate':None,'must_remove_source_refs_rate':None,'must_remove_in_links_rate':None,'must_remove_terms_absence_rate':None,'expected_deleted_page_absence_rate':None,'keep_page_presence_rate':None,'keep_page_unchanged_rate':None,'stale_inlink_count':None,'stale_inlink_page_rate':None,'false_del_rate':None,'idempotent_retract':False})
            continue
        wait_deleted(c,kb_del,target_id,case_raw/'after_delete',timeout_sec,interval_sec)
        time.sleep(interval_sec*2)
        s1 = fetch_pages(c,kb_del,case_raw/'after')
        s1_by_slug = _pages_by_slug(s1)
        cleaned_refs = 0
        for p in pages_with_target_s0:
            s1p = s1_by_slug.get(p.nslug)
            if s1p is None:
                cleaned_refs += 1
                continue
            if target_id not in _refs_of(s1p):
                cleaned_refs += 1
        source_ref_cleanup_rate = cleaned_refs / len(pages_with_target_s0) if pages_with_target_s0 else None
        # Backward-compatible alias. Historically named leak_strip_rate, but it is a cleanup rate.
        leak_strip_rate = source_ref_cleanup_rate
        remove_links = [str(x) for x in impact.get('must_remove_in_links',[]) or []]
        native_link_metrics = _delete_link_metrics(s1, target_id, target_doc, remove_links)
        stale_link_pages = native_link_metrics['stale_inlink_pages']
        stale_inlink_count = native_link_metrics['stale_inlink_count']
        stale_inlink_page_rate = native_link_metrics['stale_inlink_page_rate']
        must_remove_in_links_rate = native_link_metrics['must_remove_in_links_rate']
        false_del = 0
        common_kept = 0
        for slug, p0 in s0_by_slug.items():
            if target_id in _refs_of(p0):
                continue
            p1 = s1_by_slug.get(slug)
            # 仅"应保留却消失"算误删；删除场景下页面被合理改写是预期行为，不计入
            if p1 is None:
                false_del += 1
            common_kept += 1
        false_del_rate = false_del / common_kept if common_kept else 0.0
        keep_pages = impact.get('must_keep_pages',[])
        must_keep_present = [slug for slug in keep_pages if _find_expected_page(s1_by_slug, slug, case)]
        keep_page_presence_rate = len(must_keep_present) / len(keep_pages) if keep_pages else None
        must_not_change = impact.get('must_not_change_pages',[])
        unchanged_hits = 0
        unchanged_total = 0
        for slug in must_not_change:
            p0 = _find_expected_page(s0_by_slug, slug, case)
            p1 = _find_expected_page(s1_by_slug, slug, case)
            if p0 is None or p1 is None:
                continue
            unchanged_total += 1
            if p0.content == p1.content:
                unchanged_hits += 1
        keep_page_unchanged_rate = unchanged_hits / unchanged_total if unchanged_total else None
        remove_terms = [str(x) for x in impact.get('must_remove_terms',[]) or []]
        impact_texts = [s1_by_slug[p.nslug].text for p in pages_with_target_s0 if s1_by_slug.get(p.nslug)] or [p.text for p in s1]
        must_remove_terms_absence_rate = None
        if remove_terms and impact_texts:
            must_remove_terms_absence_rate = sum(1 for term in remove_terms if not any(has(term, txt) for txt in impact_texts)) / len(remove_terms)

        expected_deleted_pages = [str(x) for x in impact.get('expected_deleted_pages',[]) or []]
        expected_deleted_page_absence_rate = None
        if expected_deleted_pages:
            deleted_absent = 0
            for slug in expected_deleted_pages:
                resolved = slug.replace('{target_knowledge_id}', target_id).replace('{target_doc_id}', str(target_doc))
                if _find_expected_page(s1_by_slug, resolved, case) is None:
                    deleted_absent += 1
            expected_deleted_page_absence_rate = deleted_absent / len(expected_deleted_pages)
        autofix_error = None
        autofix_fixed_count = None
        autofix_link_metrics = {'must_remove_in_links_rate': None, 'stale_inlink_count': None, 'stale_inlink_page_rate': None}
        try:
            autofix = c.post(f'/knowledgebase/{kb_del}/wiki/auto-fix', json={})
            wjson(case_raw/'autofix.json', autofix)
            autofix_data = unwrap(autofix)
            if isinstance(autofix_data, dict):
                autofix_fixed_count = autofix_data.get('fixed')
            sfix = fetch_pages(c, kb_del, case_raw/'after_autofix')
            autofix_link_metrics = _delete_link_metrics(sfix, target_id, target_doc, remove_links)
        except Exception as e:
            autofix_error = str(e)
            wjson(case_raw/'autofix_error.json', {'error': autofix_error})
        try:
            c.delete(f'/knowledge/{target_id}')
        except Exception:
            pass
        wait_deleted(c,kb_del,target_id,case_raw/'after_redelete',timeout_sec,interval_sec)
        time.sleep(interval_sec*2)
        s2 = fetch_pages(c,kb_del,case_raw/'after_redelete_pages')
        s2_has_target = any(target_id in _refs_of(p) for p in s2)
        rows.append({
            'event_id':case.get('event_id'),
            'target_doc_id':target_doc,
            'target_ref_page_count':len(pages_with_target_s0),
            'source_ref_cleanup_rate':source_ref_cleanup_rate,
            'must_remove_source_refs_rate':source_ref_cleanup_rate,
            'leak_strip_rate':leak_strip_rate,
            'must_remove_in_links_rate':must_remove_in_links_rate,
            'must_remove_terms_absence_rate':must_remove_terms_absence_rate,
            'expected_deleted_page_absence_rate':expected_deleted_page_absence_rate,
            'false_del_rate':false_del_rate,
            'keep_page_presence_rate':keep_page_presence_rate,
            'keep_page_unchanged_rate':keep_page_unchanged_rate,
            'stale_inlink_count':stale_inlink_count,
            'stale_inlink_page_rate':stale_inlink_page_rate,
            'stale_inlink_pages':stale_link_pages,
            'autofix_fixed_count':autofix_fixed_count,
            'autofix_error':autofix_error,
            'autofix_must_remove_in_links_rate':autofix_link_metrics['must_remove_in_links_rate'],
            'autofix_stale_inlink_count':autofix_link_metrics['stale_inlink_count'],
            'autofix_stale_inlink_page_rate':autofix_link_metrics['stale_inlink_page_rate'],
            'idempotent_retract':not s2_has_target,
        })
    metrics = {
        'delete_target_ref_page_count': navg(r.get('target_ref_page_count') for r in rows),
        'delete_source_ref_cleanup_rate': navg(r.get('source_ref_cleanup_rate') for r in rows),
        'delete_must_remove_source_refs_rate': navg(r.get('must_remove_source_refs_rate') for r in rows),
        # Backward-compatible alias. Prefer delete_source_ref_cleanup_rate in new docs/reports.
        'delete_leak_strip_rate': navg(r.get('leak_strip_rate') for r in rows),
        'delete_must_remove_in_links_rate': navg(r.get('must_remove_in_links_rate') for r in rows),
        'delete_must_remove_terms_absence_rate': navg(r.get('must_remove_terms_absence_rate') for r in rows),
        'delete_expected_deleted_page_absence_rate': navg(r.get('expected_deleted_page_absence_rate') for r in rows),
        'delete_false_del_rate': navg(r.get('false_del_rate') for r in rows),
        'delete_keep_page_presence_rate': navg(r.get('keep_page_presence_rate') for r in rows),
        'delete_keep_page_unchanged_rate': navg(r.get('keep_page_unchanged_rate') for r in rows),
        'delete_stale_inlink_count': navg(r.get('stale_inlink_count') for r in rows),
        'delete_stale_inlink_page_rate': navg(r.get('stale_inlink_page_rate') for r in rows),
        'delete_autofix_fixed_count': navg(r.get('autofix_fixed_count') for r in rows),
        'delete_autofix_must_remove_in_links_rate': navg(r.get('autofix_must_remove_in_links_rate') for r in rows),
        'delete_autofix_stale_inlink_count': navg(r.get('autofix_stale_inlink_count') for r in rows),
        'delete_autofix_stale_inlink_page_rate': navg(r.get('autofix_stale_inlink_page_rate') for r in rows),
        'delete_idempotent_retract': avg(r.get('idempotent_retract') for r in rows),
    }
    wjson(del_raw/'delete_eval.json',{'kb_id':kb_del,'metrics':metrics,'cases':rows})
    return {'metrics':metrics,'delete_eval':rows,'kb_id':kb_del}

def wait_wiki(c,kb,kids,raw,min_pages=5,timeout=900,interval=10):
    print(f'[wait] timeout={timeout}s min_pages={min_pages}')
    poll_dir = raw / 'poll'
    poll_dir.mkdir(parents=True, exist_ok=True)
    if not kids:
        summary = {'outcome': 'noop', 'reason': 'no knowledge ids to wait for', 'polls': 0, 'page_count': None, 'statuses': [], 'details': [], 'stats': None, 'knowledge_ids': []}
        wjson(raw / 'wait_summary.json', summary)
        print('[wait] no knowledge ids; skip waiting')
        return summary

    start = time.time()
    last = -1
    stable = 0
    stalled = 0
    stall_polls = int(os.getenv('WEKNORA_EVAL_STALL_POLLS', '6'))
    i = 0
    summary = {'outcome': 'timeout', 'reason': 'timeout reached', 'polls': 0, 'elapsed_sec': 0.0, 'page_count': None, 'statuses': [], 'details': [], 'stats': None, 'knowledge_ids': [str(k) for k in kids]}

    def finish(outcome, reason, statuses, details, page_count, stats):
        payload = {
            'outcome': outcome,
            'reason': reason,
            'polls': i,
            'elapsed_sec': round(time.time() - start, 3),
            'page_count': page_count,
            'statuses': statuses,
            'details': details,
            'stats': stats,
            'knowledge_ids': [str(k) for k in kids],
        }
        wjson(raw / 'wait_summary.json', payload)
        return payload

    while time.time() - start < timeout:
        i += 1
        details = []
        sts = []
        errors = []
        for kid in kids:
            detail = {'id': str(kid)}
            try:
                data = unwrap(c.get(f'/knowledge/{kid}'))
                if not isinstance(data, dict):
                    data = {'value': data}
                status = first(data.get('parse_status'), data.get('status'), xstatus(data), 'unknown')
                detail.update({
                    'status': str(status),
                    'parse_status': str(status),
                    'title': data.get('title'),
                    'error_message': data.get('error_message') or data.get('error') or '',
                    'pending_subtasks_count': data.get('pending_subtasks_count'),
                    'summary_status': data.get('summary_status'),
                    'processed_at': data.get('processed_at'),
                    'updated_at': data.get('updated_at'),
                })
            except Exception as e:
                status = 'unknown'
                detail.update({'status': status, 'parse_status': status, 'error_message': str(e)})
            if detail.get('error_message'):
                errors.append({'id': detail['id'], 'status': detail['status'], 'error_message': detail['error_message']})
            sts.append(detail['status'])
            details.append(detail)

        try:
            pc = len(pages(c.get(f'/knowledgebase/{kb}/wiki/pages', params={'limit': 500})))
        except Exception:
            pc = 0
        try:
            st = c.get(f'/knowledgebase/{kb}/wiki/stats')
        except Exception as e:
            st = {'error': str(e)}

        all_failed = bool(sts) and all(s == 'failed' for s in sts)
        any_active = any(s in ('pending', 'processing', 'finalizing') for s in sts)
        all_terminal = bool(sts) and all(s in ('completed', 'failed', 'cancelled') for s in sts)
        stats_data = unwrap(st)
        if not isinstance(stats_data, dict):
            stats_data = {}
        try:
            pending_tasks = int(stats_data.get('pending_tasks', -1))
        except Exception:
            pending_tasks = -1
        is_active = stats_data.get('is_active')
        no_wiki_work = (pending_tasks == 0) and (is_active is False or str(is_active).lower() == 'false')
        same_page_count = pc == last
        stalled_candidate = bool(any_active and no_wiki_work and pc < min_pages and same_page_count)
        stalled = stalled + 1 if stalled_candidate else 0

        poll_payload = {
            'statuses': sts,
            'details': details,
            'page_count': pc,
            'stats': st,
            'all_failed': all_failed,
            'all_terminal': all_terminal,
            'any_active': any_active,
            'error_messages': errors,
            'stalled_candidate': stalled_candidate,
            'stall_polls_seen': stalled,
            'stall_polls_threshold': stall_polls,
            'pending_tasks': pending_tasks,
            'is_active': is_active,
        }
        wjson(poll_dir / f'poll_{i:03d}.json', poll_payload)
        print(f'[wait] poll={i} pages={pc} statuses={sts[:4]}')

        stable = stable + 1 if same_page_count and pc >= min_pages else 0
        last = pc

        if stalled >= stall_polls:
            return finish('stalled', f'no wiki progress for {stalled} polls while knowledge remains active and wiki has no pending tasks', sts, details, pc, st)
        if all_failed:
            return finish('failed', 'all knowledge items failed', sts, details, pc, st)
        if all_terminal and not any_active and any(s == 'failed' for s in sts):
            return finish('failed', 'terminal state reached with failures', sts, details, pc, st)
        if stable >= 3 and 'processing' not in sts and 'finalizing' not in sts:
            print('[wait] stable')
            return finish('stable', 'wiki page count stabilized', sts, details, pc, st)

        time.sleep(interval)

    print('[wait] timeout; continuing')
    return finish('timeout', 'timeout reached', sts if 'sts' in locals() else [], details if 'details' in locals() else [], pc if 'pc' in locals() else None, st if 'st' in locals() else None)
def fetch_pages(c,kb,raw):
    r=c.get(f'/knowledgebase/{kb}/wiki/pages',params={'limit':500}); wjson(raw/'wiki_pages_list.json',r); base=pages(r); out=[]; seen=set()
    for p in base:
        if p.nslug in seen: continue
        seen.add(p.nslug)
        try:
            d=c.get(f"/knowledgebase/{kb}/wiki/pages/{quote(p.slug.strip('/'),safe='/')}"); wjson(raw/'pages'/(slugify(p.nslug)+'.json'),d); ps=pages(d); out.append(ps[0] if ps else Page(p.slug,p.title,p.content,d))
        except Exception as e:
            wjson(raw/'page_fetch_errors'/(slugify(p.nslug)+'.json'),{'slug':p.slug,'error':str(e)}); out.append(p)
    return out
def eget(c,path,raw,**kw):
    try:
        r=c.get(path,**kw); wjson(raw,r); return r
    except Exception as e:
        r={'error':str(e),'path':path}; wjson(raw,r); return r
def _build_slug_alias_sets(gold):
    """合并所有 delete_events 的 equivalent_pages 为对称别名映射: slug -> set(slugs)。"""
    cases = rjson(gold/'delete_events.json', [])
    if isinstance(cases, dict):
        cases = cases.get('cases', []) or []
    eqsets = {}
    def link(a, b):
        a, b = norm(a), norm(b)
        if not a or not b:
            return
        eqsets.setdefault(a, set()).add(a)
        eqsets.setdefault(a, set()).add(b)
        eqsets.setdefault(b, set()).add(a)
        eqsets.setdefault(b, set()).add(b)
    for c in cases:
        eq = ((c.get('expected_impact', {}) or {}).get('equivalent_pages', {}) or {})
        for canon, alist in eq.items():
            for a in (alist or []):
                link(canon, a)
    return eqsets

def _slug_accept(nslug, expected, eqsets):
    """expected（及其别名）是否对应 nslug：要求同类型 + 精确匹配或同类型尾部匹配。

    收紧 D3：原实现用裸 tail ('/'+last_segment) 做 endswith，会把 concept/x 误判命中 entity/x。
    现在要求 nslug 的类型前缀（'/' 之前部分）与 expected（及其别名）一致。
    """
    expected = norm(expected)
    exp_type = expected.rsplit('/', 1)[0] if '/' in expected else ''
    eq = eqsets.get(expected, set())
    if nslug in eq:
        return True
    n_type = nslug.rsplit('/', 1)[0] if '/' in nslug else ''
    exp_tail = expected.split('/')[-1]
    if n_type == exp_type and nslug.endswith('/' + exp_tail):
        return True
    for t in eq:
        if '/' not in t:
            continue
        if n_type == t.rsplit('/', 1)[0] and nslug.endswith('/' + t.split('/')[-1]):
            return True
    return False

def _find_page_alias(by, n, eqsets):
    n = norm(n)
    if n in by:
        return by[n]
    for cand in eqsets.get(n, set()):
        if cand in by:
            return by[cand]
    return None

def ent_match(e,ps,eqsets):
    ex=norm(e.get('expected_slug','')); terms=[e.get('name','')]+(e.get('aliases') or []); matched=[]
    slug_hit=any(_slug_accept(p.nslug, ex, eqsets) for p in ps)
    for p in ps:
        if any(t and has(t,p.text) for t in terms): matched.append(p.slug)
    return slug_hit,bool(matched),matched
def _canon_pages_for_entity(e,ps,eqsets):
    """C3 严格化：统计该 gold 实体在生成 wiki 里被建成了几个 distinct canonical 页。
    匹配规则与 slug 命中(_slug_accept)一致：期望 slug / 别名 / 尾部匹配（含 G1/D3 同类型约束）。"""
    exp=norm(e.get('expected_slug',''))
    return {p.nslug for p in ps if _slug_accept(p.nslug,exp,eqsets)}
def eval_pages(ps,gold):
    ents=rjson(gold/'entities.json',[]); facts=rjson(gold/'facts.json',[]); rels=rjson(gold/'relations.json',[]); txt='\n'.join(p.text for p in ps); by={p.nslug:p for p in ps}
    eqsets=_build_slug_alias_sets(gold)
    er=[]
    for e in ents:
        sh,nh,m=ent_match(e,ps,eqsets)
        # C2 严格化：实体既有自身 canonical 页（slug 命中）且该页文本确实点名
        canon=_find_page_alias(by,e.get('expected_slug',''),eqsets)
        name_on_own=bool(canon) and has(e.get('name',''),canon.text)
        # C3 严格化：该实体被建成了几个 distinct canonical 页（真重复检测）
        canon_pages=_canon_pages_for_entity(e,ps,eqsets)
        er.append({'id':e.get('id'),'name':e.get('name'),'expected_slug':e.get('expected_slug'),'slug_hit':sh,'name_on_own_page':name_on_own,'matched_pages':m,'canonical_page_count':len(canon_pages),'duplicate_like':len(canon_pages)>1})
    fr=[]
    for f in facts:
        terms=f.get('expected_terms',[]); hits=[]
        for s in f.get('expected_pages',[]) or []:
            n=norm(s)
            # D3 对齐：用 _slug_accept 做同类型+alias-aware 查找，避免跨类型 endswith 误命中（如 concept/x 误算命中 entity/x）
            pg=by.get(n) or _find_page_alias(by,n,eqsets) or next((p for p in ps if _slug_accept(p.nslug,n,eqsets)), None)
            pt=pg.text if pg else ''
            hits.append({'slug':s,'all_terms_on_page':allhas(terms,pt) if pt else False})
        fr.append({'id':f.get('id'),'claim':f.get('claim'),'page_term_hit_any':any(x['all_terms_on_page'] for x in hits),'page_hits':hits})
    rr=[{'subject':r.get('subject'),'predicate':r.get('predicate'),'object':r.get('object')} for r in rels]
    # C2/C3 严格指标；C4a：删除饱和的 fact_global_term_coverage（已由 fact_expected_page_term_coverage 取代）
    return {'metrics':{'actual_page_count':len(ps),'entity_slug_recall':avg(x['slug_hit'] for x in er),'entity_name_coverage':avg(x['name_on_own_page'] for x in er),'duplicate_like_entity_rate':avg(x['duplicate_like'] for x in er),'fact_expected_page_term_coverage':avg(x['page_term_hit_any'] for x in fr)},'entities':er,'facts':fr,'relations':rr}
def graph_ne(resp):
    d=unwrap(resp); nodes=[]; edges=[]
    if isinstance(d,dict):
        for k in ('nodes','vertices','pages'):
            if isinstance(d.get(k),list): nodes=d[k]; break
        for k in ('edges','links','relations'):
            if isinstance(d.get(k),list): edges=d[k]; break
    return nodes,edges

def _gval(x):
    if isinstance(x,dict):
        for k in ('slug','page_slug','node_id','id','path','name','title'):
            if x.get(k): return str(x.get(k))
        return flat(x)
    return str(x or '')

def _g_node_slug(n):
    if isinstance(n,dict): return norm(first(n.get('slug'),n.get('page_slug'),n.get('node_id'),n.get('id'),n.get('path'),n.get('name'),n.get('title')))
    return norm(n)

def _g_node_type(n):
    if isinstance(n,dict): return norm(first(n.get('page_type'),n.get('type'),n.get('kind'),''))
    return ''

def _g_edge_pairs(edges):
    pairs=[]
    for e in edges:
        if not isinstance(e,dict): continue
        s=first(e.get('source'),e.get('source_slug'),e.get('source_id'),e.get('from'),e.get('from_slug'),e.get('from_id'),e.get('subject'))
        t=first(e.get('target'),e.get('target_slug'),e.get('target_id'),e.get('to'),e.get('to_slug'),e.get('to_id'),e.get('object'))
        s=norm(_gval(s)); t=norm(_gval(t))
        if s and t: pairs.append((s,t,e))
    return pairs

def _g_endpoint_terms(endpoint, ents_by_key):
    raw=str(endpoint or '')
    ent=ents_by_key.get(norm(raw)) or ents_by_key.get(norm(raw).split('/')[-1])
    prim=[raw,raw.replace('-',' '),raw.replace('_',' '),raw.split('/')[-1]]
    alias=[]
    if ent:
        prim += [ent.get('id',''),ent.get('name',''),ent.get('expected_slug',''),str(ent.get('expected_slug','')).split('/')[-1]]
        alias += ent.get('aliases') or []
    def uniq(xs):
        out=[]; seen=set()
        for x in xs:
            x=str(x or '').strip()
            if x and x not in seen: seen.add(x); out.append(x)
        return out
    return uniq(prim),uniq(alias)

def _g_node_matches(n,terms):
    if not isinstance(n,dict): return False
    slug=_g_node_slug(n); tail=slug.split('/')[-1]; title=str(first(n.get('title'),n.get('name'),n.get('label'),'')); title_l=title.lower(); title_s=slugify(title)
    for term in terms:
        t=str(term or '').strip().lower(); ts=slugify(t); tn=norm(t).split('/')[-1]; tns=slugify(tn)
        for x in (ts,tns):
            if not x: continue
            if x in {slug,tail,title_s} or tail.startswith(x+'-') or tail.endswith('-'+x) or title_s.startswith(x+'-') or title_s.endswith('-'+x) or x in tail or x in title_s:
                return True
        if t and (t==title_l or t in title_l): return True
    return False

def _g_candidates(nodes,endpoint,ents_by_key):
    prim,alias=_g_endpoint_terms(endpoint,ents_by_key)
    def collect(terms):
        out=[]; seen=set()
        for n in nodes:
            sg=_g_node_slug(n)
            if not sg or _g_node_type(n)=='summary': continue
            if _g_node_matches(n,terms) and sg not in seen:
                seen.add(sg); out.append(sg)
        return out
    return collect(prim) or collect(alias)

def _g_summary_nodes(nodes,evidence_docs):
    docs=[str(x).lower() for x in (evidence_docs or []) if x]
    out=[]
    if not docs: return out
    for n in nodes:
        sg=_g_node_slug(n)
        if not sg: continue
        if _g_node_type(n)!='summary' and not sg.startswith('summary/'): continue
        txt=(str(first(n.get('title'),n.get('name'),''))+'\n'+sg).lower()
        if any(d in txt for d in docs): out.append(sg)
    return out

def eval_graph(resp,gold):
    ents=rjson(gold/'entities.json',[]); rels=rjson(gold/'relations.json',[]); nodes,edges=graph_ne(resp)
    ents_by_key={}
    for e in ents:
        for k in (e.get('id'),e.get('expected_slug'),str(e.get('expected_slug','')).split('/')[-1]):
            if k: ents_by_key[norm(k)]=e
    nts=[flat(n) for n in nodes]
    nr=[]
    for e in ents:
        # C1 严格化：仅看图谱是否真有该实体的节点，去掉「实体名出现在任意节点文本」的子串兜底
        nr.append({'id':e.get('id'),'name':e.get('name'),'hit':bool(_g_candidates(nodes,e.get('id') or e.get('expected_slug'),ents_by_key))})
    pairs=_g_edge_pairs(edges); adj={}
    for s,t,_ in pairs:
        adj.setdefault(s,set()).add(t); adj.setdefault(t,set()).add(s)
    er=[]
    for r in rels:
        sub_nodes=_g_candidates(nodes,r.get('subject',''),ents_by_key); obj_nodes=_g_candidates(nodes,r.get('object',''),ents_by_key)
        method='miss'; matched=None
        for s in sub_nodes:
            for o in obj_nodes:
                if o in adj.get(s,set()): method='direct_endpoint_edge'; matched={'source':s,'target':o}; break
            if matched: break
        if not matched:
            for mid in _g_summary_nodes(nodes,r.get('evidence_docs')):
                if any(mid in adj.get(s,set()) for s in sub_nodes) and any(mid in adj.get(o,set()) for o in obj_nodes):
                    method='evidence_summary_bridge'; matched={'bridge':mid,'subject_candidates':sub_nodes[:5],'object_candidates':obj_nodes[:5]}; break
        er.append({'subject':r.get('subject'),'predicate':r.get('predicate'),'object':r.get('object'),'hit':bool(matched),'match_method':method,'matched':matched,'subject_candidates':sub_nodes[:8],'object_candidates':obj_nodes[:8],'evidence_docs':r.get('evidence_docs',[])})
    # C4b 严格化：关系两端实体是否都在图谱里有节点（替代饱和的 relation_text_coverage）
    rel_recall=[]
    for r in rels:
        s=_g_candidates(nodes,r.get('subject',''),ents_by_key); o=_g_candidates(nodes,r.get('object',''),ents_by_key)
        rel_recall.append(bool(s) and bool(o))
    return {'metrics':{'graph_node_count':len(nodes),'graph_edge_count':len(edges),'graph_node_recall':avg(x['hit'] for x in nr),'graph_edge_recall_direct':avg(x['match_method']=='direct_endpoint_edge' for x in er),'graph_edge_recall_bridge':avg(x['match_method']=='evidence_summary_bridge' for x in er),'graph_edge_recall_heuristic':avg(x['hit'] for x in er),'relation_endpoint_recall':avg(rel_recall)},'nodes':nr,'edges':er}
def eval_search(c,kb,gold,raw,k=5):
    rows=[]
    for case in rjson(gold/'search_cases.json',[]):
        try:
            r=c.get(f'/knowledgebase/{kb}/wiki/search',params={'q':case['query'],'query':case['query'],'limit':k}); wjson(raw/'search'/f"{case['id']}.json",r); ps=pages(r); rank=None; hits=[]
            for i,p in enumerate(ps[:k],1):
                hits.append({'rank':i,'slug':p.slug,'title':p.title})
                for ex in case.get('expected_slugs',[]):
                    tail=norm(ex).split('/')[-1].replace('-',' ')
                    if p.nslug==norm(ex) or has(tail,p.text): rank=i if rank is None else min(rank,i)
            rows.append({'id':case['id'],'query':case['query'],'hit_rank':rank,'hits':hits})
        except Exception as e: rows.append({'id':case['id'],'query':case['query'],'error':str(e),'hit_rank':None,'hits':[]})
    rec={f'recall@{kk}':avg((r.get('hit_rank') is not None and r['hit_rank']<=kk) for r in rows) for kk in (1,3,5)}; mrr=sum((1/r['hit_rank'] if r.get('hit_rank') else 0) for r in rows)/len(rows) if rows else 0
    return {'metrics':{**rec,'mrr':mrr},'cases':rows}
def create_session(c,kb,cfg):
    for p in cfg.get('session_create_payloads') or [{'title':'wiki eval session','knowledge_base_ids':[kb]},{'name':'wiki eval session','knowledge_base_ids':[kb]},{'title':'wiki eval session'},{}]:
        try:
            sid=xid(c.post('/sessions',json=p))
            if sid: return sid
        except Exception: pass
    return None
def ans_text(r):
    d=unwrap(r)
    if isinstance(d,dict):
        for k in ('answer','content','message','text','output','response'):
            if d.get(k): return flat(d[k])
    return flat(r)
def eval_qa(c,kb,gold,raw,cfg):
    sid=create_session(c,kb,cfg)
    if not sid: return {'metrics':{'answer_contains':0.0},'error':'could not create session'}
    rows=[]
    for case in rjson(gold/'qa.json',[]):
        p=cfg.get('knowledge_chat_payload_template') or {'message':case['question'],'query':case['question'],'knowledge_base_ids':[kb],'stream':False}
        p=json.loads(json.dumps(p).replace('${question}',case['question']).replace('${kb_id}',kb))
        try:
            r=c.post(f'/knowledge-chat/{sid}',json=p); wjson(raw/'qa'/f"{case['id']}.json",r); t=ans_text(r); ok=any(has(a,t) for a in case.get('answers',[])); rows.append({'id':case['id'],'question':case['question'],'answer_text':t[:2000],'contains_gold_answer':ok})
        except Exception as e: rows.append({'id':case['id'],'question':case['question'],'error':str(e),'contains_gold_answer':False})
    return {'metrics':{'answer_contains':avg(r['contains_gold_answer'] for r in rows)},'session_id':sid,'cases':rows}
def report_md(path,res):
    lines=['# WeKnora Wiki Evaluation Report','',f"- KB ID: `{res.get('kb_id')}`",f"- Generated at: `{datetime.now().isoformat(timespec='seconds')}`",'','## Summary Metrics','','| Metric | Value |','|---|---:|']
    for k,v in sorted(res.get('metrics',{}).items()): lines.append(f"| `{k}` | {v:.4f} |" if isinstance(v,float) else f"| `{k}` | {v} |")
    lines+=['','## Missed Entities','']
    for r in res.get('page_eval',{}).get('entities',[]):
        if not r.get('slug_hit'): lines.append(f"- `{r.get('id')}` / {r.get('name')} expected `{r.get('expected_slug')}`")
    lines+=['','## Missed Facts','']
    for r in res.get('page_eval',{}).get('facts',[]):
        if not r.get('page_term_hit_any'): lines.append(f"- `{r.get('id')}` {r.get('claim')}")
    lines+=['','## Wiki Search Failures','']
    for r in res.get('search_eval',{}).get('cases',[]):
        if not r.get('hit_rank'): lines.append(f"- `{r.get('id')}` query={r.get('query')!r} error={r.get('error','')}")
    lines+=['','## Delete Regression','']
    for r in res.get('delete_eval',[]):
        cleanup = r.get('source_ref_cleanup_rate', r.get('leak_strip_rate'))
        lines.append(f"- `{r.get('event_id')}` target={r.get('target_doc_id')} source_ref_cleanup={cleanup} must_remove_terms_absence={r.get('must_remove_terms_absence_rate')} must_remove_in_links={r.get('must_remove_in_links_rate')} stale_inlinks={r.get('stale_inlink_count')} autofix_fixed={r.get('autofix_fixed_count')} autofix_must_remove_in_links={r.get('autofix_must_remove_in_links_rate')} autofix_stale_inlinks={r.get('autofix_stale_inlink_count')} expected_deleted_page_absence={r.get('expected_deleted_page_absence_rate')} false_del={r.get('false_del_rate')} idempotent={r.get('idempotent_retract')}")
    lines+=['','## Notes','','This version uses deterministic string/slug heuristics. Treat scores as regression signals, not final human-quality judgments.']
    path.write_text('\n'.join(lines),encoding='utf-8')
def collect(c,kb,dataset,report,run_qa,cfg):
    raw=report/'raw'; gold=dataset/'gold'; ps=fetch_pages(c,kb,raw)
    graph=eget(c,f'/knowledgebase/{kb}/wiki/graph',raw/'wiki_graph.json'); stats=eget(c,f'/knowledgebase/{kb}/wiki/stats',raw/'wiki_stats.json'); lint=eget(c,f'/knowledgebase/{kb}/wiki/lint',raw/'wiki_lint.json'); issues=eget(c,f'/knowledgebase/{kb}/wiki/issues',raw/'wiki_issues.json'); index=eget(c,f'/knowledgebase/{kb}/wiki/index',raw/'wiki_index.json')
    pe=eval_pages(ps,gold); ge=eval_graph(graph,gold); se=eval_search(c,kb,gold,raw,int(cfg.get('search_k',5))); qe=eval_qa(c,kb,gold,raw,cfg) if run_qa else {'metrics':{}}
    metrics={**pe['metrics'],**ge['metrics'],**{f'wiki_search_{k}':v for k,v in se['metrics'].items()},**{f'qa_{k}':v for k,v in qe.get('metrics',{}).items()},
            'lint_issue_count':_count_items(lint),'lint_issue_payload_size_bytes':len(flat(lint)),
            'issues_count':_count_items(issues)}
    res={'kb_id':kb,'metrics':metrics,'page_eval':pe,'graph_eval':ge,'search_eval':se,'qa_eval':qe,'stats':stats,'index_text_sample':flat(index)[:2000]}
    wjson(report/'metrics.json',res); report_md(report/'report.md',res); return res
def parse():
    p=argparse.ArgumentParser(description='Run deterministic WeKnora wiki E2E evaluation')
    p.add_argument('--config',type=Path); p.add_argument('--base-url',default=os.getenv('WEKNORA_BASE_URL','http://localhost:8080')); p.add_argument('--api-prefix',default=os.getenv('WEKNORA_API_PREFIX','/api/v1'))
    p.add_argument('--dataset',type=Path,default=Path(__file__).parent/'datasets'/'stardust'); p.add_argument('--reports-dir',type=Path,default=Path(__file__).parent/'reports'); p.add_argument('--existing-kb-id',default=os.getenv('WEKNORA_KB_ID'))
    p.add_argument('--skip-ingest',action='store_true'); p.add_argument('--run-qa',action='store_true'); p.add_argument('--run-update',action='store_true'); p.add_argument('--run-delete',action='store_true'); p.add_argument('--timeout-sec',type=int,default=int(os.getenv('WEKNORA_EVAL_TIMEOUT_SEC','900'))); p.add_argument('--interval-sec',type=int,default=int(os.getenv('WEKNORA_EVAL_INTERVAL_SEC','10'))); p.add_argument('--min-pages',type=int,default=int(os.getenv('WEKNORA_EVAL_MIN_PAGES','5')))
    return p.parse_args()
def main():
    a=parse(); cfg=load_cfg(a.config); run=stamp(); report=a.reports_dir/f'run_{run}'; raw=report/'raw'; raw.mkdir(parents=True,exist_ok=True)
    c=C(cfg.get('base_url') or a.base_url,cfg.get('api_prefix') or a.api_prefix,int(cfg.get('http_timeout',60)),headers(cfg))
    try: h=c.get('/health',api=False); wjson(raw/'health.json',h); print(f'[health] OK {c.base}/health')
    except Exception as e: print(f'[health] WARN {e}'); wjson(raw/'health_error.json',{'error':str(e)})
    kb=a.existing_kb_id or create_kb(c,cfg,run); print(f"[kb] {'using existing' if a.existing_kb_id else 'created'} {kb}")
    if not a.skip_ingest:
        made=ingest(c,kb,a.dataset/'docs_v1'); wjson(raw/'created_knowledge_v1.json',made); wait_wiki(c,kb,[x.get('knowledge_id') for x in made if x.get('knowledge_id')],raw,a.min_pages,a.timeout_sec,a.interval_sec)
    res=collect(c,kb,a.dataset,report,a.run_qa,cfg); print(f"[report] {report/'report.md'}"); print(json.dumps(res['metrics'],ensure_ascii=False,indent=2))
    if a.run_update:
        print('[update] ingesting docs_v2 as additive update'); made=ingest(c,kb,a.dataset/'docs_v2'); wjson(raw/'created_knowledge_v2.json',made); wait_wiki(c,kb,[x.get('knowledge_id') for x in made if x.get('knowledge_id')],raw/'update',a.min_pages,a.timeout_sec,a.interval_sec)
        urep=a.reports_dir/f'run_{run}_after_update'; ures=collect(c,kb,a.dataset,urep,a.run_qa,cfg)
        raw_pages=rjson(urep/'raw'/'wiki_pages_list.json',{}); page_objs=pages(raw_pages); text=flat(raw_pages); rows=[]
        for ev in rjson(a.dataset/'gold'/'update_events.json',[]):
            for nf in ev.get('new_facts',[]):
                new_terms=nf.get('expected_terms',[]); stale_terms=nf.get('old_terms_must_disappear_for_latest',[])
                # C5: 仅在「承载新事实的页面」(含全部 new_terms 的页) 上检查旧术语，避免未更新页面里的合法旧值被误判未清理
                affected=[p for p in page_objs if allhas(new_terms,p.text)] if new_terms else list(page_objs)
                affected_text='\n'.join(p.text for p in affected)
                rows.append({'event':ev.get('id'),'claim':nf.get('claim'),'new_terms_hit':allhas(new_terms,text),'stale_terms_present':[t for t in stale_terms if has(t,affected_text)]})
        ures['update_eval']=rows; ures['metrics']['update_new_fact_term_coverage']=avg(r['new_terms_hit'] for r in rows); ures['metrics']['update_stale_term_absence']=avg(not r['stale_terms_present'] for r in rows); wjson(urep/'metrics.json',ures); report_md(urep/'report.md',ures); print(f"[update-report] {urep/'report.md'}")
    if a.run_delete:
        print('[delete] running delete regression')
        drep=a.reports_dir/f'run_{run}_after_delete'
        dres=eval_delete(c,kb,a.dataset,drep,cfg,run,a.timeout_sec,a.interval_sec,a.min_pages)
        wjson(drep/'metrics.json',dres)
        report_md(drep/'report.md',{'kb_id':dres.get('kb_id'),'metrics':dres.get('metrics',{}),'delete_eval':dres.get('delete_eval',[])})
        res['delete_eval']=dres.get('delete_eval',[])
        # D2: delete 指标不并入主 metrics（已在 drep/metrics.json 单独输出），避免污染 baseline 回归基线
        wjson(report/'metrics.json',res)
        report_md(report/'report.md',res)
        print(f"[delete-report] {drep/'report.md'}")
    return 0
if __name__=='__main__':
    try: raise SystemExit(main())
    except KeyboardInterrupt: print('Interrupted',file=sys.stderr); raise SystemExit(130)



