"""Create a source-backed technical readout from the scored parser run."""
from __future__ import annotations

import argparse
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess

import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np
from parser_benchmark_metrics import load_omni_metrics

ROOT = Path(__file__).resolve().parents[1]
ENGINES = ['builtin', 'markitdown', 'opendataloader', 'weknoracloud', 'mineru', 'mineru_cloud', 'paddleocr_vl', 'paddleocr_vl_cloud']
LABELS = ['Builtin', 'MarkItDown', 'OpenDataLoader', 'WeKnora Cloud', 'MinerU CPU', 'MinerU Cloud', 'PaddleOCR-VL CPU', 'PaddleOCR-VL Cloud']


def rate(value):
    return '未评分' if value is None else f'{value:.2%}'


def number(value, digits=4):
    return '未评分' if value is None else f'{value:.{digits}f}'


def read_json(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def completion_diagnostics(run):
    """Summarize retained outcomes without retrying or modifying raw evidence."""
    result = {'engines': {}}
    for engine in ENGINES:
        records = [read_json(path) for path in sorted((run / engine).glob('*.json'))]
        durations = [row['duration_ms'] / 1000 for row in records if row.get('duration_ms') is not None]
        categories = Counter()
        for row in records:
            error = str(row.get('error') or '')
            if row.get('status') == 'success':
                continue
            if row.get('status') == 'empty':
                category = '空输出'
            elif 'received message larger than max' in error:
                category = '响应消息超过远端接收上限'
            elif 'zip' in error.lower() and ('Timeout' in error or 'deadline' in error.lower()):
                category = '已完成任务的结果压缩包下载超时'
            elif 'DeadlineExceeded' in error:
                category = '远端解析服务截止时间超限'
            elif 'Timeout' in error or 'deadline exceeded' in error.lower():
                category = '客户端等待超时'
            elif 'EOF' in error:
                category = '连接提前结束'
            elif 'PdfConverter threw KeyError' in error:
                category = 'PDF 转换器键值异常'
            elif error.startswith('Failed to parse:'):
                category = '适配器解析失败，未记录更细原因'
            elif 'java' in error and 'returned non-zero exit status' in error:
                category = 'Java 命令行程序非零退出'
            else:
                category = '其他已记录错误'
            categories[category] += 1
        result['engines'][engine] = {
            'records': len(records), 'non_success_categories': dict(categories),
            'request_seconds_mean': float(np.mean(durations)) if durations else None,
            'request_seconds_median': float(np.median(durations)) if durations else None,
            'request_seconds_p95_linear': float(np.percentile(durations, 95)) if durations else None,
            'request_seconds_max': max(durations) if durations else None,
            'request_seconds_sum': sum(durations)}
    batch = ROOT / 'artifacts/parser-benchmark/cpu-batch-paddle-v1'
    completion = batch / 'batch-complete.json'
    if completion.exists() and (ROOT / read_json(batch / 'batch-state.json')['output']).resolve() == run.resolve():
        state = read_json(batch / 'batch-state.json')
        marker = read_json(completion)
        events = [json.loads(line) for line in (batch / 'events.jsonl').read_text(encoding='utf-8-sig').splitlines() if line.strip()]
        submitted = [e['at'] for e in events if e['event'] == 'page_submitting']
        finished = [e['at'] for e in events if e['event'] == 'postprocess_finished']
        receipts = [read_json(path) for path in (batch / 'receipts').glob('*.json')]
        result['paddle_batch'] = {
            'status': state['status'], 'completion': marker,
            'verified_receipts': len(receipts),
            'recovered_receipts': sum(bool(r.get('recovered_after_interruption')) for r in receipts),
            'service_recoveries': sum(e['event'] == 'paddle_recovery_healthy' for e in events),
            'first_submitted_at': min(submitted), 'last_postprocess_finished_at': max(finished),
            'elapsed_seconds_including_recovery': (datetime.fromisoformat(max(finished)) - datetime.fromisoformat(min(submitted))).total_seconds(),
            'receipt_binary_sha256': sorted({r['binary_sha256'] for r in receipts}),
            'completion_sha256': digest(completion)}
    hardware = ROOT / 'artifacts/parser-benchmark/hardware-inventory.json'
    if hardware.exists():
        result['hardware_inventory'] = read_json(hardware)
        result['hardware_inventory_sha256'] = digest(hardware)
    return result


def evidence_from_records(run, rows):
    """Use only the records included in the scored snapshot, with unchanged identities."""
    records = {}
    for row in rows:
        if row['status'] == 'not_run':
            continue
        path = run / row['engine'] / (row['sample_id'] + '.json')
        if row.get('run_record_sha256') and digest(path) != row['run_record_sha256']:
            raise ValueError('Scored run record changed; refresh scoring before reporting: ' + row['sample_id'])
        records[(row['engine'], row['sample_id'])] = read_json(path)
    starts = sorted(row['started_at'] for row in records.values() if row.get('started_at'))
    costs = {}
    for engine in ENGINES:
        selected = [value for (name, _), value in records.items() if name == engine]
        known = [row['cost_usd'] for row in selected if row.get('cost_usd') is not None]
        costs[engine] = {'scored_call_records': len(selected), 'known_fee_records': len(known),
                         'unknown_fee_records': len(selected) - len(known),
                         'recorded_api_fee_usd': sum(known) if selected and len(known) == len(selected) else None,
                         'basis_counts': dict(Counter(row.get('cost_basis', 'not_recorded') for row in selected))}
    return {'request_start_range_utc': {'first': starts[0] if starts else None, 'last': starts[-1] if starts else None},
            'scored_call_records': len(records),
            'source_commit_counts': dict(Counter(row.get('source_commit', 'not_recorded') for row in records.values())),
            'per_page_binary_sha256_records': sum(bool(row.get('binary_sha256')) for row in records.values()),
            'per_page_binary_sha256_missing': sum(not row.get('binary_sha256') for row in records.values()),
            'costs': costs}


def baseline_identity():
    path = ROOT / 'artifacts/parser-benchmark/baseline-build-identity.json'
    if not path.exists():
        return {'status': 'not_recorded'}
    identity = read_json(path)
    binary = ROOT / 'artifacts/parser-benchmark/bin/parser-benchmark'
    actual_hash = digest(binary) if binary.exists() else None
    sources = []
    for name, expected_hash in identity.get('runner_source_files', {}).items():
        snapshot = ROOT / 'artifacts/parser-benchmark/baseline-source' / name
        source = snapshot.read_bytes() if snapshot.exists() else None
        result = subprocess.run(['git', 'show', identity['baseline_source_commit'] + ':' + name], cwd=ROOT, capture_output=True)
        git_bytes = result.stdout if result.returncode == 0 else None
        sources.append({'path': name, 'expected_sha256': expected_hash,
                        'archived_source_sha256': hashlib.sha256(source).hexdigest() if source is not None else None,
                        'archived_bytes_verified': source is not None and hashlib.sha256(source).hexdigest() == expected_hash,
                        'git_blob_sha256': hashlib.sha256(git_bytes).hexdigest() if git_bytes is not None else None,
                        'git_content_verified_after_LF_normalization': source is not None and git_bytes is not None and source.replace(b'\r\n', b'\n') == git_bytes.replace(b'\r\n', b'\n')})
    return {'record_path': str(path.relative_to(ROOT)), 'record_sha256': digest(path),
            'base_commit': identity.get('base_commit'), 'compiled_label': identity.get('compiled_label'),
            'baseline_source_commit': identity.get('baseline_source_commit'),
            'recorded_binary_sha256': identity.get('binary_sha256'), 'actual_binary_sha256': actual_hash,
            'binary_verified': actual_hash is not None and actual_hash == identity.get('binary_sha256'),
            'archived_sources': sources,
            'scope': 'Frozen executable and separately archived source evidence; this does not add binary hashes to per-page records or assert a bit-reproducible rebuild.'}


def deployment_measurements():
    measurements = {}
    for name in ('mineru', 'paddle'):
        path = ROOT / 'deploy/parser-benchmark' / name / 'validation.json'
        if not path.exists():
            continue
        raw = read_json(path)
        peak = raw.get('container_memory_peak_bytes', raw.get('peak_memory_bytes'))
        measurements[raw['engine']] = {'source_path': str(path.relative_to(ROOT)), 'source_sha256': digest(path),
            'validated_at': raw.get('timestamp_utc', raw.get('validated_at')),
            'service': raw.get('pipeline', f'MinerU {raw.get("version", "unknown")} {raw.get("backend", "")}'),
            'input_sha256': raw.get('input_sha256', raw.get('sample', {}).get('sha256')),
            'sample_id': Path(raw.get('input_filename', raw.get('sample', {}).get('id', 'unknown'))).stem,
            'http_status': raw.get('http_status'), 'http_elapsed_seconds': raw.get('elapsed_seconds', raw.get('http_elapsed_seconds')),
            'peak_memory_bytes': peak, 'peak_memory_gib': peak / (1024 ** 3) if peak is not None else None,
            'resource_limits': raw.get('resource_limits'), 'cold_start_seconds': raw.get('cold_start_seconds'),
            'markdown_characters': raw.get('markdown_characters'),
            'memory_scope': raw.get('memory_scope', 'Linux cgroup cumulative memory.peak including initialization'),
            'measurement_notes': raw.get('measurement_notes', []),
            'model_revision': raw.get('model_revision'), 'models': raw.get('models'),
            'cpu_model': raw.get('cpu_model'), 'host_memory_bytes': raw.get('host_memory_bytes')}
    return measurements


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--run', default='artifacts/parser-benchmark/runs/baseline-v1')
    p.add_argument('--output', default='docs/reports/parser-benchmark')
    args = p.parse_args()
    run = ROOT / args.run
    evaluation = run / 'evaluation'
    summary = read_json(evaluation / 'summary.json')
    rows = read_json(evaluation / 'page-results.json')
    queue = read_json(evaluation / 'human-review.json')
    manifest_path = ROOT / 'dataset/parser-benchmark/manifest-full.json'
    manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
    if hashlib.sha256(manifest_path.read_bytes()).hexdigest() != summary['manifest_sha256']:
        raise ValueError('Scored manifest differs from fixed benchmark')
    out = ROOT / args.output
    out.mkdir(parents=True, exist_ok=True)
    done = sum(s['executed_pages'] for s in summary['engines'].values())
    expected = sum(s['expected_pages'] for s in summary['engines'].values())
    errors = sum(s['olmocr']['evaluator_errors'] for s in summary['engines'].values())
    status = (f'八个引擎各完成 100 页调用，{done} / {expected} 份逐页结果已归档并完成自动评分'
              if done == expected else f'评分快照包含 {done} / {expected} 个已归档逐页调用，{expected - done} 个调用尚无完整记录')
    summary['readout_status'] = status
    summary['readout_generated_at'] = datetime.now(timezone.utc).isoformat()
    summary['omni_standard_metrics'] = load_omni_metrics(run)
    summary['readout_evidence'] = evidence_from_records(run, rows)
    summary['readout_evidence']['snapshot_files_sha256'] = {name: digest(evaluation / name) for name in ('summary.json', 'page-results.json', 'human-review.json')}
    summary['baseline_identity_verification'] = baseline_identity()
    summary['deployment_measurements'] = deployment_measurements()
    summary['completion_diagnostics'] = completion_diagnostics(run)
    reviewed = [item for item in queue['items'] if item['status'] == 'completed' and all(item.get(key) for key in ('reviewer', 'reviewed_at', 'decision'))]
    selected_ids = set(queue['recommended_first_pass'])
    first_pass_pending = [item['sample_id'] for item in queue['items'] if item['sample_id'] in selected_ids and item not in reviewed]
    summary['remaining_scope'] = {
        'pages_without_complete_record': {engine: value['status_counts'].get('not_run', 0) for engine, value in summary['engines'].items()},
        'engines_without_official_omni_results': [engine for engine in ENGINES if engine not in summary['omni_standard_metrics']],
        'engines_without_CDM_results': [engine for engine in ENGINES if not summary['omni_standard_metrics'].get(engine, {}).get('cdm_executed')],
        'human_review_completed_pages': len(reviewed), 'human_review_pending_pages': len(queue['items']) - len(reviewed),
        'recommended_first_pass_pending_ids': first_pass_pending,
        'cloud_invoice_status': 'pending_account_holder_verification'}
    (out / 'summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')

    # Each bar has the same 100-page denominator; pending pages remain visible.
    buckets = []
    for engine in ENGINES:
        selected = [r for r in rows if r['engine'] == engine]
        counts = Counter('pending' if r['status'] == 'not_run' else 'failed' if r['status'] != 'success'
                         else 'text' if r['extraction_state'] == 'usable_text' else 'no_text' for r in selected)
        buckets.append(counts)
    fig, axes = plt.subplots(1, 2, figsize=(14, 5.4), gridspec_kw={'width_ratios': [1.05, 1]})
    y = np.arange(len(ENGINES)); left = np.zeros(len(ENGINES))
    for key, label, color in [('text', 'Success with text', '#087f68'), ('no_text', 'Success without text', '#b9cbbb'),
                               ('failed', 'Error / empty / timeout', '#c16b50'), ('pending', 'No completed record', '#e5e8e5')]:
        values = np.array([v[key] for v in buckets])
        if key == 'pending' and not values.any():
            continue
        axes[0].barh(y, values, left=left, color=color, label=label, height=.66)
        left += values
    axes[0].set_yticks(y, LABELS); axes[0].invert_yaxis(); axes[0].set_xlim(0, 100)
    axes[0].set_xlabel('Pages out of the fixed 100-page subset')
    axes[0].set_title('A. Adapter execution and text presence', loc='left', fontsize=12, pad=15)
    axes[0].legend(loc='upper left', bbox_to_anchor=(0, -.13), ncol=2, frameon=False, fontsize=8)
    values = [summary['engines'][e]['olmocr']['micro_pass_rate'] for e in ENGINES]
    bars = axes[1].barh(y, [0 if v is None else v * 100 for v in values], height=.66, color='#356d89')
    axes[1].set_yticks(y, LABELS); axes[1].invert_yaxis(); axes[1].set_xlim(0, 110)
    axes[1].set_xticks([0, 25, 50, 75, 100]); axes[1].set_xlabel('Official olmOCR assertion pass rate (%)')
    axes[1].set_title('B. Official assertions on 20 olmOCR pages', loc='left', fontsize=12, pad=15)
    for i, engine in enumerate(ENGINES):
        score = summary['engines'][engine]['olmocr']
        v = values[i]
        label = 'Not scored' if v is None else f'{score["passed_tests"]}/{score["scored_tests"]}'
        source = summary['engines'][engine]['by_source']['olmOCR-bench']
        if v is not None and source['executed_pages'] < source['expected_pages']:
            label += ' partial'
            bars[i].set_alpha(.45)
            bars[i].set_hatch('///')
        axes[1].text((0 if v is None else v * 100) + 1, i, label, va='center', fontsize=9)
    for ax in axes:
        ax.spines[['top', 'right', 'left']].set_visible(False)
        ax.tick_params(axis='y', length=0)
        ax.grid(axis='x', color='#e1e6e2', linewidth=.5)
        ax.set_axisbelow(True)
    fig.suptitle('WeKnora parser benchmark | fixed public subset', x=.02, ha='left', fontsize=16)
    fig.text(.02, .025, f'{done}/{expected} page calls recorded; {errors} evaluator errors. Text presence is not correctness. CPU and cloud deployment conditions differ.', fontsize=9, color='#52645b')
    fig.tight_layout(rect=(0, .1, 1, .92))
    fig.savefig(out / 'coverage-and-quality.png', dpi=180, facecolor='white')
    fig.savefig(out / 'coverage-and-quality.svg', facecolor='white')
    svg_path = out / 'coverage-and-quality.svg'
    svg_path.write_text('\n'.join(line.rstrip() for line in svg_path.read_text(encoding='utf-8').splitlines()) + '\n', encoding='utf-8')
    plt.close(fig)

    title = '# 八解析引擎公开基准实验' + ('：过程快照' if done < expected else '')
    missing = [f'{LABELS[ENGINES.index(engine)]} {count} 页' for engine, count in summary['remaining_scope']['pages_without_complete_record'].items() if count]
    pending_text = '完整调用记录的缺口：' + '；'.join(missing) if missing else '全部引擎均有逐页调用记录'
    lines = [title, '', status + '。', '',
             f'本地服务使用中央处理器（Central Processing Unit，CPU）推理。{pending_text}。'
             f'具有正式 OmniDocBench 标准指标的引擎为 {len(summary["omni_standard_metrics"])} / {len(ENGINES)} 个。'
             f'人工签名完成 {len(reviewed)} / {len(queue["items"])} 页，优先核验清单仍有 {len(first_pass_pending)} 页待核验。', '',
             '实验输入为固定的 100 页可移植文档格式（Portable Document Format，PDF）文件，包含 80 页 OmniDocBench 和 20 页 olmOCR-bench。'
             '其中 82 页没有文字层，18 页保留原始文字层。八个引擎接收相同字节，参考标注只进入评分程序。', '',
             '测量对象为生产解析适配器返回的 Markdown，位置处于切块与下游光学字符识别（Optical Character Recognition，OCR）之前。'
             '接口执行状态、正文存在性与内容正确性分别记录；仅图片输出可能需要知识库处理流程中的图片识别。', '',
             '图 1 同时展示固定分母的执行覆盖与官方断言分数。左图保留失败和缺少完整记录的页面；右图在条形末端标出断言通过数及评分分母。' +
             ('全部引擎均完成 20 页 olmOCR 文档的 125 条官方断言。' if done == expected else '未完整覆盖 olmOCR 页面时，条形使用斜线并标记 partial，表示部分样本评分。'), '',
             '![图1：逐页执行覆盖与官方断言质量](coverage-and-quality.png)', '',
             '图中的“有文字”只检查去除图片引用后的正文是否存在。olmOCR-bench 分数由冻结的官方测试对象计算，覆盖文字、表格、阅读顺序与公式。'
             '该结果属于固定公开子集实验，不能作为完整官方排行榜成绩。', '',
             '| 引擎 | 已执行页 | 接口成功页 | 有文字页 | 官方断言通过 / 已评分 | 微平均通过率 | 工具异常 | 平均请求秒数 |',
             '|---|---:|---:|---:|---:|---:|---:|---:|']
    for engine, label in zip(ENGINES, LABELS):
        s = summary['engines'][engine]; score = s['olmocr']; duration = s['mean_duration_ms']
        mean = '未执行' if duration is None else f'{duration / 1000:.2f}'
        lines.append(f'| {label} | {s["executed_pages"]} | {s["status_counts"].get("success", 0)} | {s["extraction_state_counts"].get("usable_text", 0)} | '
                     f'{score["passed_tests"]} / {score["scored_tests"]} | {rate(score["micro_pass_rate"])} | {score["evaluator_errors"]} | {mean} |')
    lines += ['', '表中接口错误仍属于已执行页面。官方断言工具异常单独记录，不按模型回答正确处理。' +
              ('各引擎的页数和断言分母一致，接口错误与空输出均纳入相应评分。' if done == expected else '未完成运行的分母可能不同；not_run 表示尚无完整记录。'), '', '## 运行配置与解释范围', '',
              '本地执行使用 CPU。MinerU 使用 pipeline 后端；PaddleOCR-VL 使用完整布局分析服务。'
              '云服务由现有租户配置指定，程序在内存中读取和解密凭据，各引擎只收到本引擎所需配置。'
              '云端运行记录未包含服务内部模型与运行库的完整版本；冻结输出可重复评分，重新调用云服务时的版本和结果需要另行核对。', '',
              '本地服务在模型下载及内容摘要校验完成后接受正式请求。单页冒烟调用承担模型冷启动验证。'
              '正式运行的本地引擎各为单并发，三个云端引擎各使用三个固定分片。八页冒烟输入包含于正式清单，云端是否采用缓存无法由响应确认。'
              '平均耗时反映该部署和网络条件下的实际请求，不构成算法计算速度排名。', '',
              '本地应用程序接口（Application Programming Interface，API）调用费记为 0，硬件和电费未计入。'
              '云解析响应未提供可核对的逐页账单，费用保存为空值，待账单核验。OpenRouter 模型令牌价格对应模型调用，解析服务费用由实际解析提供方核对。', '',
              '输入和结果使用安全散列算法 256 位（Secure Hash Algorithm 256-bit，SHA-256）记录内容身份。'
              f'固定清单摘要为 `{summary["manifest_sha256"]}`。逐页记录保存输入摘要、运行代码身份、配置、开始时间、耗时、错误和输出摘要。', '',
              '## 人工核验清单', '',
              f'人工清单共有 {len(queue["items"])} 页记录，已有 {len(reviewed)} 页具有完整人工签名，优先抽查清单中还有 {len(first_pass_pending)} 页待核验。'
              '浏览器交互测试与程序审计不计作人工签名。核验者需填写实际姓名或标识、结论和备注。', '',
              '| 优先页面 | 来源 | 人工检查内容 |', '|---|---|---|']
    selected = set(first_pass_pending)
    for item in queue['items']:
        if item['sample_id'] in selected:
            lines.append(f'| `{item["sample_id"]}` | {item["source"]} | {"；".join(item["reasons"])} |')
    lines += ['', '人工核验工作台提供原文预览、官方标注、八引擎原始输出和记录导出。建议先核对表格数字、公式符号、多栏顺序及中英混排空格，'
              '再核对失败页和自动评分与人眼判断不一致的页。服务故障核验与内容审核分别记录。', '',
              '人工事项还包括核对三个云解析账号的实际账单、查看 WeKnora Cloud 的超时及响应消息上限说明，以及在对外发布原始文档前核对来源使用条款。'
              '程序可以提供错误样本和请求证据；账号账单确认、厂商支持沟通和人工签名由实际账号持有人完成。', '',
              '## 来源与复现', '',
              '数据版本、抽样规则、官方评分入口与许可说明见 [公开基准子集说明](../../parser-benchmark-dataset.md)。'
              '固定来源为 [OmniDocBench](https://github.com/opendatalab/OmniDocBench) 与 [olmOCR-bench](https://github.com/allenai/olmocr/tree/main/olmocr/bench)。', '',
              f'原始运行目录：`{args.run}`。本报告的结构化汇总为 [summary.json](summary.json)。'
              '公开原文、参考内容、原始解析结果和模型文件存放于 Git 忽略目录，代码仓库保存清单、摘要、方法、汇总和图表。', '']
    (out / 'README.md').write_text('\n'.join(lines), encoding='utf-8')
    omni_lines = ['## OmniDocBench 标准指标', '',
                  '80 页图像文档的标准评分使用冻结的官方代码。各引擎评分副本统一删除图片引用、替代文字及图片路径，原始结果保持完整。'
                  '编辑距离越低表示差异越小；表格树编辑距离相似度（Tree Edit Distance based Similarity，TEDS）越高表示表格结构与内容越接近标注。'
                  '各项均采用官方按页汇总口径，实际页数随相应标注是否存在而定。', '',
                  '| 引擎 | 文字编辑距离 / 页数 | 表格 TEDS / 页数 | 阅读顺序编辑距离 / 页数 | 公式字符串编辑距离 / 页数 |',
                  '|---|---:|---:|---:|---:|']
    for engine, label in zip(ENGINES, LABELS):
        m = summary['omni_standard_metrics'].get(engine)
        if not m:
            omni_lines.append(f'| {label} | 待完成 | 待完成 | 待完成 | 待完成 |')
        else:
            omni_lines.append(f'| {label} | {number(m["text_edit_distance"])} / {m["text_pages"]} | {rate(m["table_teds_page_mean"])} / {m["table_pages"]} | '
                              f'{number(m["reading_order_edit_distance"])} / {m["reading_order_pages"]} | {number(m["formula_string_edit_distance"])} / {m["formula_pages"]} |')
    cdm_done = sum(item.get('cdm_executed', False) for item in summary['omni_standard_metrics'].values())
    omni_lines += ['', f'公式字符检测匹配（Character Detection Matching，CDM）具有实际结果的引擎为 {cdm_done} / {len(ENGINES)} 个，'
                   '其运行需要独立的排版渲染依赖。公式字符串编辑距离和 olmOCR 公式断言各自按其定义解释，报告不提供官方综合分。', '',
                   '各引擎使用同一版本的评分输入清理函数，函数版本与输入输出摘要保存在评分清单中。该口径与完全原样 Markdown 的官方榜单提交存在区别。'
                   '分母审计分别列出适用标注页、实际评分页以及空输出和服务错误页的纳入数量，缺失分数保持为空。', '']
    report = (out / 'README.md').read_text(encoding='utf-8').replace('## 运行配置与解释范围', '\n'.join(omni_lines) + '\n## 运行配置与解释范围')
    conclusions = []
    if done == expected and len(summary['omni_standard_metrics']) == len(ENGINES):
        local_mineru = summary['engines']['mineru']
        local_paddle = summary['engines']['paddleocr_vl']
        cloud_paddle = summary['omni_standard_metrics']['paddleocr_vl_cloud']
        mineru_metrics = summary['omni_standard_metrics']['mineru']
        cloud_mineru = summary['engines']['mineru_cloud']['olmocr']
        conclusions = ['## 实测结论', '',
            f'MinerU CPU 在本机 100 页中有 {local_mineru["extraction_state_counts"].get("usable_text", 0)} 页产生正文，'
            f'平均请求耗时 {local_mineru["mean_duration_ms"] / 1000:.2f} 秒；'
            f'PaddleOCR-VL CPU 有 {local_paddle["extraction_state_counts"].get("usable_text", 0)} 页产生正文，'
            f'平均耗时 {local_paddle["mean_duration_ms"] / 1000:.2f} 秒。'
            '这组观测反映当前机器、模型实现和客户端等待限制下的运行表现。', '',
            f'PaddleOCR-VL Cloud 的文字编辑距离为 {cloud_paddle["text_edit_distance"]:.4f}，'
            f'阅读顺序编辑距离为 {cloud_paddle["reading_order_edit_distance"]:.4f}。'
            f'MinerU CPU 的表格页均 TEDS 为 {mineru_metrics["table_teds_page_mean"]:.2%}。'
            f'MinerU Cloud 通过 {cloud_mineru["passed_tests"]} / {cloud_mineru["scored_tests"]} 条 olmOCR 官方断言。'
            '文字、表格和断言分数刻画不同对象，各指标分别比较。', '',
            'Builtin、MarkItDown、OpenDataLoader 与 WeKnora Cloud 在本轮 80 页图像文档的适配器输出中没有可用正文。'
            '后续知识库流程的图片识别与问答效果属于独立测量范围。', '']
        report = report.replace('## 运行配置与解释范围', '\n'.join(conclusions) + '\n## 运行配置与解释范围')
    operational = ['### 失败记录与本地批次', '',
                   '下表按原始错误字符串归类，保留所有非成功页面。连接提前结束只说明观察到的接口现象，记录未确定其底层原因。', '',
                   '| 引擎 | 原始记录类别 | 页面数 |', '|---|---|---:|']
    diagnostics = summary['completion_diagnostics']
    for engine in ENGINES:
        for category, count in diagnostics['engines'][engine]['non_success_categories'].items():
            operational.append(f'| {LABELS[ENGINES.index(engine)]} | {category} | {count} |')
    operational += ['', 'WeKnora Cloud 的响应消息上限错误涉及远端接收的 4 MiB 消息，输入 PDF 大小与响应消息大小分别记录。'
                    'MinerU Cloud 的下载超时发生在任务已经完成后的压缩包读取阶段。当前应用包含该下载阶段的有限重试；'
                    '本实验使用冻结基线程序，实验记录不构成这项修复的线上效果对照。', '']
    batch = diagnostics.get('paddle_batch')
    if batch:
        timing = diagnostics['engines']['paddleocr_vl']
        operational += [f'PaddleOCR-VL CPU 批次有 {batch["verified_receipts"]} 份独立收据，'
                        f'其中 {batch["recovered_receipts"]} 份通过中断后的完整性核对补收。'
                        f'批次执行了 {batch["service_recoveries"]} 次服务恢复，已记录的失败页面保持原结果。'
                        f'首个请求提交至五步后处理结束共 {batch["elapsed_seconds_including_recovery"] / 3600:.2f} 小时，包含中断、服务恢复和评分。', '',
                        f'该批次请求耗时中位数为 {timing["request_seconds_median"]:.2f} 秒，'
                        f'采用线性插值的第 95 百分位数为 {timing["request_seconds_p95_linear"]:.2f} 秒。'
                        '耗时统计纳入全部 100 个已记录请求，包括达到客户端等待上限的请求。'
                        '独立收据将逐页输入、输出、执行器与基线二进制摘要绑定；原始记录保持完整。', '']
    hardware = diagnostics.get('hardware_inventory')
    if hardware:
        operational += [f'当前主机处理器为 {hardware["cpu_name"]}，系统报告物理内存 '
                        f'{hardware["host_memory_bytes"] / 1024 ** 3:.2f} GiB，Docker 可用内存 '
                        f'{hardware["docker_memory_bytes"] / 1024 ** 3:.2f} GiB。'
                        '两个本地解析容器均限制为 6 个 CPU 核；MinerU 内存上限为 8 GiB，PaddleOCR-VL 为 9 GiB。'
                        '该资源清单为批次完成后的只读快照，模型配置与逐页运行身份分别保存。', '']
    operational += ['### 本机服务验证实测', '',
                   '下表读取各服务保存的单页验证记录。请求耗时包含服务推理和接口处理，内存峰值采用 Linux 控制组的累计峰值。'
                   'GiB 表示 2 的 30 次方字节。', '',
                   '| 服务 | 验证样本 | 请求秒数 | 峰值内存 GiB | 返回字符数 | 资源限额 |',
                   '|---|---|---:|---:|---:|---|']
    for measurement in summary['deployment_measurements'].values():
        limits = measurement['resource_limits']
        limit_text = f'{limits["cpu_cores"]} 个 CPU 核 / {limits["memory_bytes"] / 1024 ** 3:.0f} GiB' if limits else '验证记录未提供'
        operational.append(f'| {measurement["service"]} | `{measurement["sample_id"]}` | {number(measurement["http_elapsed_seconds"], 3)} | '
                           f'{number(measurement["peak_memory_gib"], 3)} | {measurement["markdown_characters"]} | {limit_text} |')
    operational += ['', '两个服务使用同一页教材样本，模型和服务实现各自固定。PaddleOCR-VL 的累计峰值包含初始化，其验证期间还有 MinerU 正式解析同时运行。'
                    '这些单页测量支持说明当前机器的等待时间与内存需求，不能推导其他硬件上的吞吐量或等资源算法速度排名。'
                    '验证记录未提供主机 CPU 型号和物理内存总量。', '',
                    '实测来源：[MinerU 验证记录](../../../deploy/parser-benchmark/mineru/validation.json)、'
                    '[PaddleOCR-VL 验证记录](../../../deploy/parser-benchmark/paddle/validation.json)。模型版本、输入摘要、依赖版本和测量边界随原始记录保存。', '',
                    '### 运行身份与费用记录', '']
    identity = summary['baseline_identity_verification']
    evidence = summary['readout_evidence']
    if identity.get('baseline_source_commit'):
        archived_count = sum(item['archived_bytes_verified'] for item in identity['archived_sources'])
        git_count = sum(item['git_content_verified_after_LF_normalization'] for item in identity['archived_sources'])
        identity_status = '一致' if identity['binary_verified'] else '不一致或文件缺失'
        operational += [f'基线可执行文件的实际 SHA-256 为 `{identity["actual_binary_sha256"]}`，与运行身份记录{identity_status}。'
                        f'编译标签为 `{identity["compiled_label"]}`，可定位的源码提交为 `{identity["baseline_source_commit"]}`。'
                        f'{archived_count} / {len(identity["archived_sources"])} 份归档源码通过字节摘要核对，'
                        f'{git_count} / {len(identity["archived_sources"])} 份源码与该提交在换行符归一化后内容一致。', '',
                        f'评分快照中的 {evidence["scored_call_records"]} 份调用记录有 {evidence["per_page_binary_sha256_records"]} 份包含逐页二进制摘要，'
                        f'{evidence["per_page_binary_sha256_missing"]} 份未包含该字段。独立二进制身份与逐页字段覆盖分别记录；现有逐页记录保持其原始内容。'
                        '源码归档与二进制摘要用于定位运行材料，其验证范围不包含逐位一致的重新编译。', '']
    start_range = evidence['request_start_range_utc']
    operational += [f'本快照纳入的请求开始时间范围为 `{start_range["first"]}` 至 `{start_range["last"]}`，使用协调世界时（Coordinated Universal Time，UTC）。'
                    '生成时间与证据范围分别保存在结构化汇总中，正在执行且没有完整记录的请求不进入当前评分。', '',
                    '费用表使用当前评分快照中的原始调用记录。未提供费用的云调用保持未知，不并入一个伪精确的总金额。', '',
                    '| 引擎 | 纳入记录 | 有金额记录 | 费用未知记录 | 已记录 API 费用（美元） |',
                    '|---|---:|---:|---:|---:|']
    for engine, label in zip(ENGINES, LABELS):
        costs = evidence['costs'][engine]
        fee = costs['recorded_api_fee_usd']
        fee_text = '尚无调用记录' if not costs['scored_call_records'] else '未知' if fee is None else f'{fee:.2f}'
        operational.append(f'| {label} | {costs["scored_call_records"]} | {costs["known_fee_records"]} | {costs["unknown_fee_records"]} | {fee_text} |')
    operational += ['', '本地 API 金额 0 仅指没有外部接口调用费，不包含购置硬件、电费及运维时间。云服务的免费额度、折扣和实际扣款需账号账单核对。'
                    'OpenRouter 的模型令牌价表不覆盖本实验中的外部文档解析服务费用。', '']
    report = report.replace('## 人工核验清单', '\n'.join(operational) + '\n## 人工核验清单')
    audit_path = out / 'evidence-audit.json'
    if audit_path.exists():
        audit = read_json(audit_path)
        if audit.get('status') != 'passed':
            raise ValueError('Independent evidence audit is not passed')
        report = report.replace('## 来源与复现', '## 完整性复核\n\n'
                                '独立审计核对固定输入、800 份逐页记录及输出、100 份本地长批次收据、八套官方评分回执及其实际分母。'
                                '核对记录见 [独立证据审计](evidence-audit.json)。程序摘要核对与实际人工内容核验分别记录。\n\n## 来源与复现')
    (out / 'README.md').write_text(report, encoding='utf-8')
    print(json.dumps({'status': status, 'readout': str(out / 'README.md'), 'evaluator_errors': errors}, ensure_ascii=False))


if __name__ == '__main__':
    main()
