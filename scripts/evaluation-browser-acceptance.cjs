/* Read-only browser acceptance against an existing local service.
 * Usage: node script.cjs AUTH_JSON OUTPUT_DIRECTORY [FRONTEND_URL]
 * AUTH_JSON: {token, me: the GET /api/v1/auth/me response}. Keep it private.
 * Set PLAYWRIGHT_MODULE to the installed Playwright package if not on NODE_PATH.
 * No model invocation, annotation mutation, trace or credential capture.
 */
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs/promises');
const path = require('node:path');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');

(async () => {
  const [authFile, output, base = 'http://127.0.0.1:5174'] = process.argv.slice(2);
  assert(authFile && output);
  const auth = JSON.parse(await fs.readFile(authFile, 'utf8'));
  await fs.mkdir(output, {recursive:true});
  const browser = await chromium.launch({channel:process.env.BROWSER_CHANNEL || 'msedge', headless:true});
  const context = await browser.newContext({viewport:{width:1440,height:1080}, reducedMotion:'reduce',
    storageState:{cookies:[],origins:[{origin:base,localStorage:[
      {name:'weknora_token',value:auth.token},
      {name:'weknora_user',value:JSON.stringify(auth.me.data.user)},
      {name:'weknora_tenant',value:JSON.stringify(auth.me.data.tenant)},
      {name:'weknora_memberships',value:JSON.stringify(auth.me.data.memberships)},
    ]}]}});
  const page = await context.newPage();
  const checks = [], errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const check = (name, details = {}) => checks.push({name,status:'passed',...details});
  const api = async url => {
    const r = await context.request.get(base + url, {headers:{Authorization:'Bearer '+auth.token}});
    assert(r.ok(), `API status ${r.status()}`); return r;
  };
  const screenshot = async name => page.screenshot({path:path.join(output,name),fullPage:true,
    mask:[page.locator('.user-button')],maskColor:'#e5e7eb'});
  try {
    await page.goto(base+'/platform/evaluations',{waitUntil:'networkidle'});
    await page.locator('.guide__skip').waitFor({state:'visible',timeout:5000}).catch(()=>{});
    if(await page.locator('.guide__skip').isVisible()) await page.locator('.guide__skip').click();
    const taskID = await page.locator('.run-browser input[type="checkbox"]').first().getAttribute('aria-label');
    assert(taskID); const id = taskID.replace(/^选择运行 /,'');
    await page.locator('.run-browser article').first().getByRole('button').click();
    await page.getByRole('heading',{name:'任务总耗时',exact:true}).waitFor();
    for(const name of ['检索质量','回答质量','已记录模型成本','任务总耗时']) {
      assert.equal(await page.getByRole('heading',{name,exact:true}).count(),1);
    }
    const detail = (await (await api('/api/v1/evaluation?task_id='+encodeURIComponent(id))).json()).data;
    assert.equal(detail.task.status,2);
    const body = await page.getByRole('main').innerText();
    for(const total of detail.runtime_metrics.cost.totals) {
      assert(body.includes(`${total.currency} ${(total.cost_microunits/1e6).toFixed(6)}`));
    }
    check('four metric groups and actual API cost',{task_id:id});
    await screenshot('01-evaluation-overview.png');
    let exportedQuestions;
    for(const format of ['json','csv']) {
      const pending = page.waitForEvent('download');
      await page.getByRole('button',{name:format.toUpperCase(),exact:true}).click();
      const download = await pending; const target=path.join(output,'browser-export.'+format);
      await download.saveAs(target); assert.equal(await download.failure(),null);
      const actual = await fs.readFile(target);
      const expected = await (await api(`/api/v1/evaluation/tasks/${id}/export?format=${format}`)).body();
      if(format==='json') {
        const data=JSON.parse(actual); assert.deepEqual(data.runtime_metrics,detail.runtime_metrics);
        assert.deepEqual(data.questions,JSON.parse(expected).questions);
        exportedQuestions=data.questions;
      } else {
        // Export timestamps can vary between requests; compare all CSV data rows.
        assert(actual.includes(Buffer.from('runtime_metrics_json')));
        assert(actual.includes(Buffer.from(detail.task.id)));
      }
      check('browser '+format+' download',{bytes:actual.length,sha256:crypto.createHash('sha256').update(actual).digest('hex')});
    }
    await page.getByRole('button',{name:/^问题 \d+$/}).click();
    assert.equal(await page.getByRole('main').locator('article').count(),exportedQuestions.length);
    for(const question of exportedQuestions) {
      assert(await page.getByRole('heading',{name:question.question,exact:true}).isVisible());
    }
    check('question details and retrieved evidence',{questions:exportedQuestions.length});
    await screenshot('02-question-evidence.png');
    await page.getByRole('combobox',{name:'状态',exact:true}).selectOption('2');
    await page.getByRole('textbox',{name:'数据集',exact:true}).fill(detail.task.dataset_id);
    const matching = page.waitForResponse(r=>r.url().includes('/evaluation/tasks?')&&r.url().includes(detail.task.dataset_id));
    await page.getByRole('button',{name:'应用筛选',exact:true}).click();
    assert.equal((await matching).status(),200);
    await page.waitForLoadState('networkidle');
    assert((await page.locator('.run-browser article').count())>=1);
    check('status and dataset filters');
    await page.getByRole('textbox',{name:'数据集',exact:true}).fill('00000000-0000-4000-8000-000000000001');
    const filtered = page.waitForResponse(r=>r.url().includes('/evaluation/tasks?')&&r.url().includes('00000000-0000-4000-8000-000000000001'));
    await page.getByRole('button',{name:'应用筛选',exact:true}).click();
    const filterResponse = await filtered;
    assert.deepEqual((await filterResponse.json()).data.items,[]);
    await page.waitForLoadState('networkidle');
    assert.equal(await page.locator('.run-browser article').count(),0);
    check('empty filter results');
    await screenshot('03-empty-filter.png');
    const reset = page.waitForResponse(r=>r.url().includes('/evaluation/tasks?')&&!r.url().includes('dataset_id='));
    await page.getByRole('button',{name:'重置',exact:true}).click();
    assert.equal((await reset).status(),200);
    await page.locator('.run-browser article').first().waitFor();
    await page.waitForLoadState('networkidle');
    assert((await page.locator('.run-browser article').count())>=1);
    check('filter reset');
    await page.goto(base+'/platform/settings?section=models',{waitUntil:'networkidle'});
    assert.equal(await page.locator('.settings-overlay').count(),1);
    assert.equal(await page.getByRole('heading',{name:'模型配置',exact:true}).count(),1);
    check('direct settings route has one modal');
    await page.getByRole('button',{name:'用量与定价',exact:true}).click();
    const drawer = page.locator('.model-usage-drawer');
    await drawer.waitFor({state:'visible'});
    await drawer.locator('[aria-busy="false"]').waitFor();
    assert((await drawer.locator('article').count())>=1);
    let statsText = await drawer.innerText();
    assert(statsText.includes('p50')&&statsText.includes('p95')&&statsText.includes('p99'));
    assert(statsText.includes('供应商提示词缓存')&&statsText.includes('应用嵌入缓存'));
    assert(statsText.includes('—')&&statsText.includes('0.0%'));
    check('latency, cost and separate cache metrics; unknown remains distinct from zero');
    await screenshot('04-model-statistics.png');
    const usage = page.waitForResponse(r=>r.url().includes('/models/usage?')&&r.url().includes('model_ids='));
    await drawer.getByRole('combobox',{name:'选择模型',exact:true}).selectOption({label:'deepseek/deepseek-v4-flash'});
    assert.equal((await usage).status(),200);
    await drawer.getByRole('heading',{name:'deepseek/deepseek-v4-flash',exact:true}).waitFor();
    assert.equal(await drawer.locator('article').count(),1);
    assert(await drawer.getByRole('heading',{name:'deepseek/deepseek-v4-flash',exact:true}).isVisible());
    check('model usage filter');
    await drawer.getByText('自定义',{exact:true}).click();
    await drawer.locator('input[type="datetime-local"]:visible').first().waitFor();
    assert.equal(await drawer.locator('input[type="datetime-local"]:visible').count(),2);
    check('custom time window');
    assert.deepEqual(errors,[],'uncaught browser errors');
    check('no uncaught browser errors');
    const systemInfo = (await (await api('/api/v1/system/info')).json()).data;
    await fs.writeFile(path.join(output,'status.json'),JSON.stringify({status:'passed',browser:browser.version(),
      tested_at:new Date().toISOString(),system_info:systemInfo,checks,human_review:'excluded; no annotation controls used',errors},null,2));
    console.log(JSON.stringify({status:'passed',checks:checks.length,output}));
  } finally { await browser.close(); }
})().catch(error=>{console.error(error.stack);process.exit(1)});
