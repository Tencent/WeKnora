#!/usr/bin/env python3
"""Case-granular promotion gate. Historical packets cannot mint missing identities."""
import argparse,json,hashlib
from pathlib import Path
IDENTITIES=('task_set_sha256','rubric_sha256','harness_sha256','system_prompt_sha256')
def compare(a,b):
 errors=[]
 def index(run):
  rows=run.get('cases',[]);ids=[x.get('id') for x in rows]
  if not all(isinstance(x,str) and x for x in ids) or len(set(ids))!=len(ids):raise ValueError('missing/duplicate case identity')
  if not all(type(x.get('passed')) is bool for x in rows):raise ValueError('binary independent result required; pending is not PASS')
  return {x['id']:x for x in rows}
 aa=index(a);bb=index(b)
 if list(aa)!=list(bb):errors.append('case set/order mismatch')
 for key in IDENTITIES:
  v=a.get('manifest',{}).get(key);w=b.get('manifest',{}).get(key)
  if not v or not w:errors.append('missing '+key)
  elif v!=w:errors.append('mismatched '+key)
 if a.get('manifest',{}).get('candidate_self_graded') or b.get('manifest',{}).get('candidate_self_graded'):errors.append('candidate self grading prohibited')
 for k in aa.keys()&bb.keys():
  for field in ('query','query_sha256'):
   if aa[k].get(field)!=bb[k].get(field):errors.append('case input mismatch '+k+':'+field)
  for row in (aa[k],bb[k]):
   if 'query' in row and hashlib.sha256(row['query'].encode()).hexdigest()!=row.get('query_sha256'):errors.append('case hash invalid '+k)
 ca=a.get('manifest',{}).get('actual_persisted_config');cb=b.get('manifest',{}).get('actual_persisted_config')
 if ca is not None or cb is not None:
  if not isinstance(ca,dict) or not isinstance(cb,dict):errors.append('missing persisted config')
  elif {k:v for k,v in ca.items() if k!='model_id'}!={k:v for k,v in cb.items() if k!='model_id'}:errors.append('persisted harness differs beyond model_id')
 regressions=[];improvements=[]
 for k in aa.keys()&bb.keys():
  x,y=aa[k],bb[k]
  if x['passed'] and not y['passed']:regressions.append({'case_id':k,'baseline_output':x.get('output'),'candidate_output':y.get('output'),'failing_check':y.get('check'),'baseline':x,'candidate':y})
  elif not x['passed'] and y['passed']:improvements.append(k)
 regressions.sort(key=lambda x:list(aa).index(x['case_id']));improvements.sort(key=list(aa).index)
 decision='BLOCK_REGRESSION' if regressions else 'INVALID_IDENTITY' if errors else 'INCONCLUSIVE_SMALL_SET' if len(aa)<10 else 'ALLOW_REGRESSION_GATE_ONLY'
 return {'decision':decision,'promotion_allowed':decision=='ALLOW_REGRESSION_GATE_ONLY','comparison_identity_valid':not errors,'identity_errors':errors,'case_count':len(aa),'minimum_cases':10,'regressions':regressions,'improvements':improvements,'improvements_offset_regressions':False,'manifest_A':a.get('manifest'),'manifest_B':b.get('manifest'),'quality_acceptance_separate':True,'retention_days_minimum':30}
def main():
 p=argparse.ArgumentParser();p.add_argument('--baseline',required=True);p.add_argument('--candidate',required=True);p.add_argument('--output',required=True);a=p.parse_args();r=compare(json.loads(Path(a.baseline).read_text()),json.loads(Path(a.candidate).read_text()));r['input_sha256']={k:hashlib.sha256(Path(v).read_bytes()).hexdigest() for k,v in [('baseline',a.baseline),('candidate',a.candidate)]};Path(a.output).write_text(json.dumps(r,ensure_ascii=False,indent=2)+'\n');print(r['decision']);raise SystemExit(0 if r['promotion_allowed'] else 1)
if __name__=='__main__':main()
