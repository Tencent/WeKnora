"""Small bounded route check; credentials only arrive over stdin."""
import importlib.util
import json
from pathlib import Path

spec = importlib.util.spec_from_file_location('acceptance', Path(__file__).with_name('evaluation-openrouter-acceptance.py'))
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
credentials = json.loads(input())
root = Path('/evidence/preflight-01'); root.mkdir()
relay = module.BudgetRelay(18810, credentials['openrouter_key'], root, Path('/evidence/budget.json'))
results = []
try:
    for model in (module.CHAT, module.COMPARE, 'moonshotai/kimi-k2.5', 'deepseek/deepseek-v4-pro'):
        relay.round = 'preflight'
        status, raw = relay.forward('/v1/chat/completions', {'model':model,'max_tokens':32,'messages':[{'role':'user','content':'仅输出：连接成功'}]})
        data = json.loads(raw)
        results.append({'model':model,'status':status,'provider':data.get('provider'),'usage':data.get('usage'),'choices':data.get('choices'),'error':data.get('error')})
        print(json.dumps({'model':model,'status':status,'provider':data.get('provider'),'usage':data.get('usage')},ensure_ascii=False),flush=True)
finally:
    relay.server.server_close()
    module.save(root/'results.json',results)
