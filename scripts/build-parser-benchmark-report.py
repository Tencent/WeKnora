"""Build a local review workbench from immutable parser evidence."""
from __future__ import annotations

import argparse
import hashlib
import html
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
from collections import Counter
from parser_benchmark_metrics import load_omni_metrics

ROOT = Path(__file__).resolve().parents[1]
ENGINES = ['builtin', 'markitdown', 'opendataloader', 'weknoracloud', 'mineru', 'mineru_cloud', 'paddleocr_vl', 'paddleocr_vl_cloud']
LABELS = ['Builtin', 'MarkItDown', 'OpenDataLoader', 'WeKnora Cloud', 'MinerU 本地', 'MinerU Cloud', 'PaddleOCR-VL 本地', 'PaddleOCR-VL Cloud']


def link_file(source: Path, target: Path):
    target.parent.mkdir(parents=True, exist_ok=True)
    if target.exists():
        return
    try:
        os.link(source, target)
    except OSError:
        shutil.copy2(source, target)


def text_present(md: str) -> bool:
    return _scorer.extraction_state(md)[0] == 'usable_text'


def preview_file(sample: dict, out: Path) -> str:
    """Show actual source pixels independently of a browser's PDF plug-in."""
    if sample.get('image_path'):
        source = ROOT / sample['image_path']
        relative = 'preview/' + sample['id'] + source.suffix.lower()
        link_file(source, out / relative)
    else:
        import pypdfium2 as pdfium
        relative = 'preview/' + sample['id'] + '.png'
        target = out / relative
        if not target.exists():
            target.parent.mkdir(parents=True, exist_ok=True)
            with pdfium.PdfDocument(ROOT / sample['pdf_path']) as doc:
                page = doc[0]
                bitmap = page.render(scale=1.5)
                bitmap.to_pil().save(target)
                bitmap.close()
                page.close()
    return relative


def quality_section(results: Path) -> str:
    summary_path = results / 'evaluation/summary.json'
    if not summary_path.exists():
        return '<p>官方质量评分待执行。</p>'
    summary = json.loads(summary_path.read_text(encoding='utf-8'))
    rows = []
    for engine, label in zip(ENGINES, LABELS):
        s = summary['engines'][engine]
        score = s['olmocr']
        rate = score['micro_pass_rate']
        percent = '待评分' if rate is None else f'{rate:.1%}'
        duration = s['mean_duration_ms']
        mean = '—' if duration is None else f'{duration / 1000:.2f} 秒'
        rows.append(f'<tr><th>{html.escape(label)}</th><td>{s["executed_pages"]} / {s["expected_pages"]}</td>'
                    f'<td>{score["passed_tests"]} / {score["scored_tests"]}</td><td>{percent}</td>'
                    f'<td>{score["evaluator_errors"]}</td><td>{mean}</td></tr>')
    omni = load_omni_metrics(results)
    omni_rows = []
    for engine, label in zip(ENGINES, LABELS):
        metrics = omni.get(engine)
        if not metrics:
            omni_rows.append(f'<tr><th>{html.escape(label)}</th><td colspan="4">等待该引擎输入齐全并完成官方评分</td></tr>')
            continue
        omni_rows.append(f'<tr><th>{html.escape(label)}</th><td>{metrics["text_edit_distance"]:.4f} / {metrics["text_pages"]} 页</td>'
                         f'<td>{metrics["table_teds_page_mean"]:.2%} / {metrics["table_pages"]} 页</td>'
                         f'<td>{metrics["reading_order_edit_distance"]:.4f} / {metrics["reading_order_pages"]} 页</td>'
                         f'<td>{metrics["formula_string_edit_distance"]:.4f} / {metrics["formula_pages"]} 页</td></tr>')
    omni_html = ('<h2 style="margin-top:28px">OmniDocBench 结构质量</h2><p>80 页图像文档使用冻结的官方标准评分。编辑距离越低越好；'
                 '表格树编辑距离相似度（TEDS）越高越好。各单元格同时列出实际参与评分的页数。</p>'
                 '<div class="table-scroll"><table><thead><tr><th>引擎</th><th>文字编辑距离</th><th>表格 TEDS</th><th>阅读顺序编辑距离</th>'
                 '<th>公式字符串编辑距离</th></tr></thead><tbody>' + ''.join(omni_rows) + '</tbody></table></div>'
                 '<p>所有引擎的评分副本统一去除图片引用、图片替代文字及路径，原始结果保留。仅存在对应官方标注的页进入各指标分母。'
                 '公式字符检测匹配（CDM）未执行，因此不提供官方综合分。</p>')
    return ('<section class="quality"><h2>官方断言与运行证据</h2><p>olmOCR-bench 使用官方文字、表格、顺序和公式断言。'
            '通过率按已成功执行的断言计算；评测工具异常单独列出。运行未完成时，各引擎分母可能不同。</p>'
            '<div class="table-scroll"><table><thead><tr><th>引擎</th><th>已测试页面</th><th>断言通过 / 已评分</th>'
            '<th>断言通过率</th><th>评测工具异常</th><th>请求平均耗时</th></tr></thead><tbody>' + ''.join(rows) +
            '</tbody></table></div><p>页面覆盖、内容正确性和响应耗时分别解释；部署资源与网络条件不同，耗时不构成算法速度排名。</p>' + omni_html + '</section>')


_spec = importlib.util.spec_from_file_location('parser_score', ROOT / 'scripts/score-parser-benchmark.py')
_scorer = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_scorer)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--manifest', default='dataset/parser-benchmark/manifest-full.json')
    p.add_argument('--results', default='artifacts/parser-benchmark/runs/baseline-v1')
    p.add_argument('--output', default='artifacts/parser-benchmark/report')
    args = p.parse_args()
    manifest_path = ROOT / args.manifest
    manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
    out = ROOT / args.output
    out.mkdir(parents=True, exist_ok=True)
    rows = []
    for sample in manifest['samples']:
        item = {k: sample.get(k) for k in ('id', 'source', 'language', 'category', 'input_kind', 'source_sample_id', 'element_counts')}
        item['pdf'] = 'pdf/' + sample['id'] + '.pdf'
        link_file(ROOT / sample['pdf_path'], out / item['pdf'])
        item['preview'] = preview_file(sample, out)
        item['sha256'] = sample['sha256']
        reference_bytes = (ROOT / sample['reference']['path']).read_bytes()
        item['reference_sha256'] = hashlib.sha256(reference_bytes).hexdigest()
        if item['reference_sha256'] != sample['reference']['sha256']:
            raise ValueError('Reference identity mismatch: ' + sample['id'])
        ref = json.loads(reference_bytes)
        item['reference'] = ref
        item['results'] = {}
        for engine in ENGINES:
            rp = ROOT / args.results / engine / (sample['id'] + '.json')
            if not rp.exists():
                item['results'][engine] = {'status': 'pending', 'markdown': '', 'duration_ms': None, 'text_present': False,
                                           'review_fingerprint': hashlib.sha256((sample['sha256'] + engine + 'pending').encode()).hexdigest()}
                continue
            record_bytes = rp.read_bytes()
            record = json.loads(record_bytes)
            if record['input_sha256'] != sample['sha256']:
                raise ValueError('Result and sample identities differ: ' + sample['id'])
            md = rp.with_suffix('.md').read_text(encoding='utf-8')
            if hashlib.sha256(md.encode()).hexdigest() != record['markdown_sha256']:
                raise ValueError('Markdown identity mismatch: ' + sample['id'])
            record['markdown'] = md
            record['text_present'] = text_present(md)
            record['review_fingerprint'] = hashlib.sha256(record_bytes + sample['sha256'].encode() + item['reference_sha256'].encode()).hexdigest()
            item['results'][engine] = record
        rows.append(item)
    # A fixed, balanced human review queue; all statuses start pending. Selecting
    # an item never asserts that a person has actually inspected it.
    chosen = []
    seen = set()
    for feature in ('category', 'language', 'input_kind'):
        groups = {}
        for row in rows:
            groups.setdefault(str(row[feature]), []).append(row)
        for group in groups.values():
            candidates = sorted(group, key=lambda r: (sum(v['status'] == 'error' for v in r['results'].values()), sum(v['text_present'] for v in r['results'].values())), reverse=True)
            for row in candidates:
                if row['id'] not in seen:
                    chosen.append(row['id']); seen.add(row['id']); break
            if len(chosen) >= 20: break
        if len(chosen) >= 20: break
    for row in rows:
        if len(chosen) >= 20: break
        if row['id'] not in seen:
            chosen.append(row['id']); seen.add(row['id'])
    queue_path = ROOT / args.results / 'evaluation/human-review.json'
    review_reasons = {}
    if queue_path.exists():
        queue = json.loads(queue_path.read_text(encoding='utf-8'))
        chosen = queue['recommended_first_pass']
        review_reasons = {r['sample_id']: r['reasons'] for r in queue['items']}
    review = {'schema_version': 1, 'manifest_sha256': hashlib.sha256(manifest_path.read_bytes()).hexdigest(), 'status': 'pending_human_review', 'reviewer': None,
              'instructions': ['核对原文与官方标注是否一致', '核对文字遗漏、表格单元格及公式', '检查阅读顺序、图片输出及回退解释', '记录评分与人眼判断不一致的案例'],
              'items': [{'sample_id': sid, 'status': 'pending', 'reviewer': None, 'notes': '', 'reasons': review_reasons.get(sid, [])} for sid in chosen]}
    (out / 'human-review-pending.json').write_text(json.dumps(review, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    stats = {e: {'label': LABELS[i], 'total': len(rows), 'counts': dict(Counter(r['results'][e]['status'] for r in rows)), 'with_text': sum(r['results'][e]['text_present'] for r in rows)} for i, e in enumerate(ENGINES)}
    payload = json.dumps({'samples': rows, 'engines': ENGINES, 'labels': LABELS, 'stats': stats, 'review': review, 'sources': manifest['sources']}, ensure_ascii=False).replace('</', '<\\/')
    page = TEMPLATE.replace('__DATA__', payload).replace('__QUALITY__', quality_section(ROOT / args.results))
    (out / 'index.html').write_text(page, encoding='utf-8')
    (out / 'status-summary.json').write_text(json.dumps(stats, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps({'report': str(out / 'index.html'), 'samples': len(rows), 'human_review_pages': len(chosen), 'records': sum(v['status'] != 'pending' for r in rows for v in r['results'].values())}))


TEMPLATE = r'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>犀牛鸟 · 解析引擎横评</title><style>
:root{--ink:#142a25;--muted:#63766e;--green:#008966;--bg:#f3f5f1;--line:#d8e1d8}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.6 "Segoe UI","Microsoft YaHei",sans-serif}header{padding:32px 5vw 22px;background:#fff;border-bottom:1px solid var(--line)}.eyebrow{font-size:12px;letter-spacing:2px;color:var(--green);font-weight:700}h1{font-size:clamp(28px,3vw,44px);margin:8px 0;letter-spacing:-1px}h2{font-size:22px;margin:0 0 8px}p{margin:6px 0;color:var(--muted)}main{padding:28px 5vw}.cards{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:0 0 28px}.card{padding:18px;background:#fff;border:1px solid var(--line);border-radius:12px}.big{font-size:32px;font-weight:600;letter-spacing:-1px}.label{font-size:12px;color:var(--muted)}.bar{height:6px;background:#e6ece7;border-radius:5px;margin:12px 0;overflow:hidden;display:flex}.good{background:#00996d}.bad{background:#c96444}.pending{background:#c7cfca}.notice{background:#e8efdf;border-left:3px solid #8b9f62;padding:14px 18px;margin:20px 0;border-radius:0 8px 8px 0;font-size:13px}.controls{display:flex;gap:10px;flex-wrap:wrap;align-items:center;margin:20px 0}select,input,button{font:inherit;border:1px solid var(--line);border-radius:7px;padding:9px 12px;background:#fff;color:var(--ink)}button{cursor:pointer}button.primary{background:var(--ink);color:#fff}.workbench{display:grid;grid-template-columns:255px 1fr;gap:18px}.list{max-height:790px;overflow:auto;background:#fff;border:1px solid var(--line);border-radius:10px}.sample{padding:13px;border-bottom:1px solid var(--line);cursor:pointer}.sample.active{background:#e4efe8;border-left:3px solid var(--green)}.sample small{display:block;color:var(--muted);font-size:11px}.detail{min-width:0}.meta{background:#fff;border:1px solid var(--line);border-radius:10px;padding:17px;margin-bottom:14px}.columns{display:grid;grid-template-columns:1fr 1fr;gap:14px}.pane{min-width:0;background:#fff;border:1px solid var(--line);border-radius:10px;overflow:hidden}.pane h3{font-size:13px;letter-spacing:1px;margin:0;padding:12px 16px;border-bottom:1px solid var(--line)}.preview{height:650px;overflow:auto;background:#e8ece7;padding:12px}.preview img{display:block;width:100%;height:auto;background:#fff}pre{margin:0;padding:18px;white-space:pre-wrap;word-wrap:break-word;max-height:650px;overflow:auto;font:13px/1.8 "Segoe UI","Microsoft YaHei",sans-serif}.tag{display:inline-block;background:#e5eee8;border-radius:5px;padding:2px 7px;font-size:11px;margin-right:5px}.review{margin-top:14px;padding:18px;border:1px solid var(--line);border-radius:10px;background:#fff}.review textarea{width:100%;margin-top:10px;min-height:70px;border:1px solid var(--line);font:inherit;padding:10px;border-radius:6px}details{margin:12px 0}summary{cursor:pointer;font-weight:600}.sources{font-size:12px;margin-top:30px;padding:20px 0;border-top:1px solid var(--line)}a{color:var(--green)}@media(max-width:1000px){.cards{grid-template-columns:repeat(2,1fr)}.workbench{grid-template-columns:1fr}.list{max-height:200px}.columns{grid-template-columns:1fr}.preview{height:550px}}@media(max-width:600px){main,header{padding:20px}.cards{grid-template-columns:1fr 1fr}.big{font-size:25px}}
.quality{margin:28px 0}.table-scroll{overflow-x:auto;margin:16px 0;border:1px solid var(--line);border-radius:10px}table{width:100%;border-collapse:collapse;background:#fff;font-size:13px;white-space:nowrap}th,td{text-align:left;padding:13px 16px;border-bottom:1px solid var(--line)}thead{background:#e8eee7}tbody th{font-weight:500}</style></head><body><header><div class="eyebrow">WEKNORA / DOCUMENT PARSING BENCHMARK</div><h1>解析质量，有据可查。</h1><p>犀牛鸟课题三 · 八引擎同批文档横评与人工核验工作台</p></header><main><div class="cards" id="cards"></div><div class="notice">页面展示生产解析适配器的输出，位于切块与图片文字识别阶段之前。“接口成功”与“提取到文字”分别统计；有文字不等于内容正确。扫描输入与原始文字层输入分组评估。云服务与本地 CPU 的耗时受部署条件影响。公开基准子集结果不代表完整官方排行榜。</div>__QUALITY__<h2>逐页证据与核验</h2><p>同一份原文、固定内容摘要、八个引擎结果。人工队列为待办，不表示已经完成人工审核。</p><div class="controls"><select id="engine" aria-label="选择引擎"></select><select id="source" aria-label="选择来源"><option value="">全部来源</option><option>OmniDocBench</option><option>olmOCR-bench</option></select><select id="scope" aria-label="筛选核验范围"><option value="all">全部 100 页</option><option value="review">建议人工核验 20 页</option><option value="error">失败或无文字</option></select><input id="search" placeholder="查找样本编号或类别" aria-label="查找样本"><button id="export" class="primary">导出人工核验记录</button></div><div class="workbench"><div class="list" id="list"></div><div class="detail"><div class="meta" id="meta"></div><div class="columns"><div class="pane"><h3>原始测试文档 · <a id="pdf-link" target="_blank" rel="noreferrer">打开 PDF</a></h3><div class="preview"><img id="source-image" alt="原始测试文档页面"></div></div><div class="pane"><h3>解析结果 / 保留原始 Markdown</h3><pre id="result"></pre></div></div><details><summary>查看官方参考标注</summary><pre class="pane" id="reference"></pre></details><div class="review"><strong>人工核验记录</strong><p>填写实际审核人，再选择结论。记录保存在本浏览器，可导出 JSON 文件。</p><div class="controls"><input id="reviewer" placeholder="实际审核人" aria-label="实际审核人"><select id="verdict" aria-label="人工核验结论"><option value="pending">待核验</option><option value="confirmed">已核验，记录一致</option><option value="issue">已核验，发现问题</option></select><button id="save">保存本页核验</button><span id="saved" role="status"></span></div><textarea id="notes" placeholder="记录遗漏、表格或公式问题，以及自动评分不符合人眼判断的情况" aria-label="人工核验备注"></textarea></div></div></div><div class="sources" id="sources"></div></main><script>
const D=__DATA__;const $=id=>document.getElementById(id);const stateKey='weknora-parser-review-'+D.review.manifest_sha256;let reviews={};try{reviews=JSON.parse(localStorage.getItem(stateKey)||'{}')}catch{}let selected=D.samples[0].id;const names={success:'接口成功',error:'失败',empty:'空输出',timeout:'超时',pending:'待运行'};function esc(x){return String(x??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
$('cards').innerHTML=D.engines.map((e,i)=>{const s=D.stats[e],c=s.counts,done=s.total-(c.pending||0);return `<div class="card"><div class="label">${esc(D.labels[i])}</div><div class="big">${s.with_text}<span style="font-size:15px;color:var(--muted)"> / ${s.total} 页有文字</span></div><div class="bar"><span class="good" style="width:${(c.success||0)/s.total*100}%"></span><span class="bad" style="width:${((c.error||0)+(c.empty||0)+(c.timeout||0))/s.total*100}%"></span><span class="pending" style="flex:1"></span></div><div class="label">已运行 ${done} · 接口成功 ${c.success||0} · 失败/空结果 ${((c.error||0)+(c.empty||0)+(c.timeout||0))}</div></div>`}).join('');D.engines.forEach((e,i)=>$('engine').add(new Option(D.labels[i],e)));
function visible(){let text=$('search').value.toLowerCase();return D.samples.filter(s=>(!$('source').value||s.source===$('source').value)&&(!text||(s.id+' '+s.category+' '+s.language).toLowerCase().includes(text))&&($('scope').value!=='review'||D.review.items.some(i=>i.sample_id===s.id))&&($('scope').value!=='error'||!s.results[$('engine').value].text_present||s.results[$('engine').value].status!=='success'))}
function draw(){const rows=visible();if(!rows.some(s=>s.id===selected)&&rows.length)selected=rows[0].id;$('list').innerHTML=rows.map(s=>`<div class="sample ${s.id===selected?'active':''}" data-id="${esc(s.id)}" role="button" tabindex="0"><strong>${esc(s.id)}</strong><small>${esc(s.source)} / ${esc(s.language)}</small><small>${esc(s.category)}</small></div>`).join('')||'<div class="sample">没有符合条件的样本</div>';$('list').querySelectorAll('[data-id]').forEach(n=>{n.onclick=()=>{selected=n.dataset.id;draw()};n.onkeydown=e=>{if(e.key==='Enter'){selected=n.dataset.id;draw()}}});const s=D.samples.find(s=>s.id===selected);if(!s)return;const e=$('engine').value,r=s.results[e];$('meta').innerHTML=`<strong>${esc(s.id)}</strong><p><span class="tag">${esc(names[r.status])}</span><span class="tag">${r.text_present?'有文字输出':'无正文或仅图片'}</span><span class="tag">${esc(({image_wrapped_pdf:'图像封装 PDF',upstream_original_pdf:'官方原始 PDF'})[s.input_kind]||s.input_kind)}</span></p><p>耗时 ${r.duration_ms==null?'待运行':(r.duration_ms/1000).toFixed(2)+' 秒'} · 回退 ${esc(r.fallback_status==='not_reported_by_adapter'?'适配器未报告':(r.fallback_status||'未报告'))}</p><p style="font-size:11px;word-break:break-all">SHA-256 ${esc(s.sha256)}</p>`;$('pdf-link').href=s.pdf;$('source-image').src=s.preview;$('result').textContent=r.error?('错误：'+r.error+'\n\n'+r.markdown):(r.markdown||'尚无文本结果');$('reference').textContent=JSON.stringify(s.reference,null,2);const prior=reviews[s.id+'|'+e+'|'+r.review_fingerprint]||{};$('reviewer').value=prior.reviewer||'';$('verdict').value=prior.status||'pending';$('notes').value=prior.notes||'';$('saved').textContent=''}
function isCurrentReview(r){const s=D.samples.find(s=>s.id===r.sample_id);return !!r.review_fingerprint&&s?.results[r.engine]?.review_fingerprint===r.review_fingerprint}for(const id of ['engine','source','scope'])$(id).onchange=draw;$('search').oninput=draw;$('save').onclick=()=>{const reviewer=$('reviewer').value.trim(),status=$('verdict').value;if(status!=='pending'&&!reviewer){$('saved').textContent='请填写实际审核人';return}const sample=D.samples.find(s=>s.id===selected),result=sample.results[$('engine').value];reviews[selected+'|'+$('engine').value+'|'+result.review_fingerprint]={sample_id:selected,engine:$('engine').value,review_fingerprint:result.review_fingerprint,input_sha256:sample.sha256,markdown_sha256:result.markdown_sha256||null,reference_sha256:sample.reference_sha256,reviewer:reviewer||null,status,notes:$('notes').value,reviewed_at:status==='pending'?null:new Date().toISOString()};localStorage.setItem(stateKey,JSON.stringify(reviews));$('saved').textContent='已保存到本浏览器'};$('export').onclick=()=>{const blob=new Blob([JSON.stringify({schema_version:1,manifest_sha256:D.review.manifest_sha256,items:Object.values(reviews).filter(isCurrentReview),review_history:Object.values(reviews).filter(r=>!isCurrentReview(r)).map(r=>({...r,stale_reason:'result_identity_changed_or_missing'}))},null,2)],{type:'application/json'});const a=document.createElement('a');a.href=URL.createObjectURL(blob);a.download='parser-human-review.json';a.click();URL.revokeObjectURL(a.href)};$('sources').innerHTML='<strong>来源与复现边界</strong>'+D.sources.map(s=>`<p><a href="${esc(s.dataset)}" target="_blank" rel="noreferrer">${esc(s.name)}</a> · 数据版本 ${esc(s.revision)}</p>`).join('')+'<p>官方原文、标注及项目逐页结果保留内容摘要。研究用途；人工审核状态与自动评测独立保存。</p>';draw();
</script></body></html>'''

if __name__ == '__main__':
    main()
