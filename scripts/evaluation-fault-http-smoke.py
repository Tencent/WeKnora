"""Zero-cost cancellation and process-kill recovery through the actual service."""
import argparse
import importlib.util
import json
from pathlib import Path
import sqlite3
import threading
import time

spec=importlib.util.spec_from_file_location('offline',Path(__file__).with_name('evaluation-rag-http-smoke.py'))
offline=importlib.util.module_from_spec(spec); spec.loader.exec_module(offline)

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--server-binary',type=Path,required=True)
    parser.add_argument('--port',type=int,default=18908)
    parser.add_argument('--supplier-port',type=int,default=18910)
    args=parser.parse_args()
    run=offline.Regression(args)
    run.env['GOLANG_PROTOBUF_REGISTRATION_CONFLICT']='warn'
    arrived,release,completed=threading.Event(),threading.Event(),threading.Event()
    handler=run.supplier.server.RequestHandlerClass
    original=handler.do_POST
    def delayed(self):
        arrived.set()
        release.wait(180)
        try: original(self)
        except (BrokenPipeError,ConnectionResetError): pass
        finally: completed.set()
    handler.do_POST=delayed
    result={'mode':'offline-fault-injection','paid_provider_requests':0,'cases':[]}
    def create():
        data,_=run.request('create fault task','POST','/api/v1/evaluation',run.creation)
        tid=data['data']['task']['id']
        assert arrived.wait(45),'Provider call did not start'
        with sqlite3.connect(run.database) as db:
            assert db.execute('SELECT count(*) FROM model_call_records WHERE evaluation_task_id=? AND status=?',(tid,'started')).fetchone()[0]>0
        return tid
    def terminal(tid,expected,timeout=240):
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            data,_=run.request('fault state','GET','/api/v1/evaluation?task_id='+tid,record=False)
            if data['data']['task']['status'] not in (0,1):
                assert data['data']['task']['status']==expected,data['data']['task']
                return data['data']
            time.sleep(2)
        raise AssertionError('Terminal recovery deadline exceeded')
    run.supplier.start()
    try:
        run.start(1); run.register()
        tid=create()
        first,_=run.request('cancel blocked call','POST',f'/api/v1/evaluation/{tid}/cancel',{})
        second,_=run.request('repeat cancel','POST',f'/api/v1/evaluation/{tid}/cancel',{})
        canceled=terminal(tid,6,60)
        release.set(); assert completed.wait(10)
        result['cases'].append({'case':'cancel_running_call','detail':canceled,'first_cancel':first,'second_cancel':second})
        offline.dump(run.output/'fault-results.json',result)
        print('Cancellation and repeated cancellation passed',flush=True)
        arrived.clear(); release.clear(); completed.clear()
        tid=create()
        run.process.kill(); run.process.wait(timeout=5); run.log.close()
        release.set(); assert completed.wait(10)
        handler.do_POST=original
        run.start(2)
        interrupted=terminal(tid,5)
        result['cases'].append({'case':'process_kill_and_lease_recovery','detail':interrupted})
        with sqlite3.connect(run.database) as db:
            db.row_factory=sqlite3.Row
            result['ledger']=[dict(r) for r in db.execute('SELECT evaluation_task_id,status,accounting_complete,error_code FROM model_call_records')]
        result['status']='passed'
        offline.dump(run.output/'fault-results.json',result)
        print('Process kill and automatic lease recovery passed',flush=True)
    finally:
        release.set();run.stop();run.supplier.close()
        result['expected_disconnected_supplier_errors']=run.supplier.errors
        offline.dump(run.output/'fault-results.json',result)

if __name__=='__main__': main()
