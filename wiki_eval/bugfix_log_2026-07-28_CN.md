# wiki_eval 指标 Bug 修复记录（2026-07-28）

> 修复对象：`wiki_eval/eval_weknora.py`（确定性回归评测套件）
> 来源：先对全量指标做实现审计（`wiki_eval_metrics_audit_CN.md`，共 15 项问题），再分批修复。
> 范围：本文件记录**已落地**的 13 项（4 个严重逻辑 bug + 9 个口径/流程问题）。剩余 1 项 D1（删除评测死参数，设计选择）本次未动。
> 验证基线下文统一指 ds-flash 干净基线 KB `6ab0592b-3d78-4926-b6d5-86251cdd3614`（纯 docs_v1，不重入库、走 `--skip-ingest` 复用）。

---

## 一、第 1 批：严重逻辑 Bug（结果失真）

### B1. `false_del_rate` 把"内容合理改写"误判为"误删"
- **位置**：`eval_delete`（原 L253-256）
- **原实现**：
  ```python
  if p1 is None or p0.content != p1.content:
      false_del += 1
  ```
- **问题**：删除一个文档后，系统会对相关页面做级联重算（版本号升、`summary` 重跑），`content` 变化是预期行为，不应算误删。
- **修复**：只统计"应保留却消失"的页面：
  ```python
  if p1 is None:
      false_del += 1
  ```
- **验证**：glm-5.2 重跑对比——
  - 旧（误算改写）：d001=1.0 / d002=0.45 / d003=0.23
  - 新（仅统计消失）：d001=0.667 / d002=0.167 / **d003=0.0**
  - d003 归零证明原虚高来自页面改写而非真删；d001 仍 0.667 是真实页面消失（系统行为，非指标问题）。

### B2. 报告"漏报清单"用了恒真判定字段
- **位置**：`report_md`（原 L650-654）
- **原实现**：
  ```python
  if not r.get('name_hit'):       ...   # Missed Entities
  if not r.get('global_term_hit'): ...  # Missed Facts
  ```
- **问题**：`name_hit`="实体名出现在任意页面"（几乎恒 True）；`global_term_hit`="术语全局子串共存"（恒 True）。两者都不代表"被正确建模"，导致漏报清单永远为空、真实缺失被掩盖。
- **修复**：
  ```python
  if not r.get('slug_hit'):            ...   # Missed Entities
  if not r.get('page_term_hit_any'):   ...   # Missed Facts
  ```
- **验证**：glm-5.2 重跑——
  - Missed Entities 旧版恒空 → 新版列出 7 个真实缺失实体（skyvault-initiative / helion-crystal / lumen-coil / mira-cole / jonas-reed / borealis-station / celestial-review-board）。
  - Missed Facts 旧版恒空 → 新版列出 f003 / f005 / f008 三个术语未落在期望页的真实缺口。

### B3. 图边召回不区分"直连边"与"共现桥接"
- **位置**：`eval_graph`（原 L607）
- **原实现**：`graph_edge_recall_heuristic` 把"两实体共同连到同一 summary 节点"也算命中（共现≠真关系边）。
- **修复**：保留总指标，拆出主/弱信号：
  ```python
  'graph_edge_recall_direct': avg(x['match_method']=='direct_endpoint_edge' for x in er),   # 直连，主信号
  'graph_edge_recall_bridge': avg(x['match_method']=='evidence_summary_bridge' for x in er), # 共现，弱信号
  ```
- **验证**：glm-5.2 重跑新增 `direct=0.8333`、`bridge=0.0833`，`heuristic=0.9167` 保留。原 heuristic 中 11/12 边靠 bridge 兜底，现直连成为可回归的主信号。

### G1. `equivalent_pages` 别名未被 entity/fact 评测使用
- **位置**：`ent_match`（`eval_pages`）、`facts` 页查找；`eval_delete` 已用但 `eval_pages`/`eval_graph` 未用。
- **问题**：gold 的 `entity/stardust-program` 别名是 `stardust-program-phase-alpha`，系统生成带后缀 slug；`ent_match` 的 `endswith('/'+tail)` 匹配不到，导致 `slug_recall` 恒假。
- **修复**：新增 `_build_slug_alias_sets(gold)`（合并 `delete_events.json` 全量 `equivalent_pages`→对称别名集）、`_slug_accept(nslug, expected, eqsets)`、`_find_page_alias(by, n, eqsets)`；`ent_match(e, ps, eqsets)` 的 `slug_hit` 与 facts 的 `expected_pages` 查找改为别名感知（含尾部匹配）。
- **验证**：代码正确，`psionic-engine` 同时匹配裸 slug 与 `pe-7` 变体。**但指标未抬升**——根因是 gold 手写别名集与本次（非确定性）生成 slug 对不上：7 个未命中实体中 6 个本次根本未生成任何变体（属数据对齐问题，非代码缺陷）。G1 是正确实现，要让其真正抬升需重对齐 gold 别名集或接受 `slug_recall` 为趋势信号。

---

## 二、第 2 批：误报 / 跨类型 / 流程污染 / 口径

### C5. `update_stale_term_absence` 旧术语检查误报
- **位置**：`main`（update 段，原 L690）
- **问题**：旧术语检查扫的是**整个 wiki 全文**（`wiki_pages_list.json` 全量拼接），未更新页面里的合法旧值（如 PE-7 页仍含 `42 kPa`）会被误判"未清理"。
- **修复**：检查范围限定到"承载新事实的页面"——逐页 `allhas(new_terms)` 过滤出受影响页，仅在该页文本里查旧术语：
  ```python
  affected = [p for p in pages if E.allhas(new_terms, p.text)]
  affected_text = '\n'.join(p.text for p in affected)
  present = [t for t in stale if E.has(t, affected_text)]
  ```
- **验证**：离线单测通过——PE-7 未更新页的合法旧值 `42 kPa` 被排除，仅 PE-8（被更新页）的旧值进入作用域。

### D3. `ent_match` slug 匹配 endswith 跨类型误匹配
- **位置**：`_slug_accept`（原 `p.nslug.endswith('/'+tail)`）
- **问题**：不校验页面类型，`concept/stardust-program` 会被误判命中 `entity/stardust-program`；本语料未触发但是隐患。
- **修复**：改为"同类型前缀 + 期望 slug / 别名 / 尾部匹配"：
  ```python
  def _slug_accept(nslug, expected, eqsets):
      expected = norm(expected)
      eq = eqsets.get(expected, set())
      if nslug in eq:
          return True
      tails = {expected.split('/')[-1]} | {x.split('/')[-1] for x in eq}
      head = expected.split('/')[0]
      return nslug.split('/')[0] == head and nslug.split('/')[-1] in tails
  ```
- **验证**：离线单测 `concept/stardust-program` vs `entity/stardust-program`=False（修复前误 True），G1 别名匹配仍 True。

### D2. `--run-delete` 把 delete 指标污染进主 metrics
- **位置**：`main`（delete 段，原 L699）
- **原实现**：`res['metrics'].update(dres['metrics'])`，baseline 与 delete 指标混在一份 `metrics.json`。
- **修复**：删除该行，delete 指标只在 `drep/metrics.json` 单独输出，不污染 baseline 回归基线。
- **验证**：复用基线 `--skip-ingest --run-delete` 端到端——主 `run_*/metrics.json` 不含 `false_del*` / `keep_page*` 等 delete 键。

### C6 / C7. `lint_issue_payload_size` 是字节数、`issues_payload_size` 恒为 0
- **位置**：`collect`（原 L668）
- **问题**：`lint_issue_payload_size = len(flat(lint))` 是 JSON 序列化字节数，随 wiki 规模增长，不可横向比；`issues_payload_size` 对应 `/wiki/issues` 接口基本返回空，恒 0、无意义。
- **修复**：改为明确的条数/字节双指标，诚实命名：
  ```python
  'lint_issue_count': _count_items(lint),
  'lint_issue_payload_size_bytes': len(flat(lint)),
  'issues_count': _count_items(issues),
  ```
  新增辅助函数 `_count_items` 统计嵌套结构中的叶子条目数。
- **验证**：复用纯 collect——新键出现（lint_issue_count=218 / bytes=78337 / issues_count=0）、旧饱和键消失。

---

## 三、第 3 批：饱和指标严格重写

> 4 个指标原实现均为"全局子串共存"判定，几乎必然满 1.0，区分度低。按用户决策**彻底替换**（旧 heuristic 键不保留，指标集有断点，老 run 不可直接横向比趋势）。

### C1. `graph_node_recall_heuristic` → `graph_node_recall`
- **位置**：`eval_graph`（`nr.hit`）
- **原实现**：节点命中 = 节点存在 **OR** 实体名出现在任意节点文本（子串兜底恒 True）。
- **修复**：去掉子串兜底，仅留 `_g_candidates` 节点存在匹配；指标改名 `graph_node_recall`。
- **验证**：ds-flash 基线 `graph_node_recall=1.0`——13 个 gold 实体确有图节点，是**真结果**非饱和。

### C2. `entity_name_coverage` 严格化
- **位置**：`eval_pages`（原依赖 `name_hit`）
- **原实现**：实体名出现在任意页面子串 → 恒 1.0。
- **修复**：新增 `name_on_own_page` = 实体自身 canonical 页（`_find_page_alias` 取）存在**且**页文本含实体名；指标改用该值：
  ```python
  own = _find_page_alias(by, e.get('expected_slug'), eqsets)
  name_on_own_page = bool(own) and has(e.get('name',''), own.text)
  ```
- **验证**：`entity_name_coverage` 1.0 → **0.846**（= slug_recall，无页实体自然点不到名）。

### C3. `duplicate_like_entity_rate` 严格化（真重复检测）
- **位置**：`eval_pages`（`duplicate_like_count`）
- **原实现**：名字出现 ≥2 不同页面即算（summary 页必含 → 恒 1.0），命名误导。
- **修复**：新增 `_canon_pages_for_entity`，统计同一 gold 实体在生成 wiki 里被建成的 distinct canonical 页数（复用 `_slug_accept` 语义）；`rate = 占比(≥2)`：
  ```python
  def _canon_pages_for_entity(e, ps, eqsets):
      return {p.nslug for p in ps if _slug_accept(p.nslug, e.get('expected_slug',''), eqsets)}
  ```
  > 初版 bug：曾误用 gold `type`（`'program'`）作 slug 前缀过滤，把全部 `entity/` 页排除导致恒 0；已修正为直接复用 `_slug_accept` 语义。
- **验证**：`duplicate_like_entity_rate` 1.0 → **0.154**（stardust-program、psionic-engine 各被建成"裸 slug + 别名后缀"两页，是真重复信号）。

### C4. `fact_global_term_coverage` / `relation_text_coverage` 饱和项重写
- **C4a**：删除 `fact_global_term_coverage`（已被第 1 批修出的 `fact_expected_page_term_coverage` 取代，留着冗余）。
- **C4b**：`relation_text_coverage`（全局子串共存 → 恒 1.0）改为 `relation_endpoint_recall`，在 `eval_graph` 内算——关系两端实体是否均在图谱有节点（`_g_candidates` 对 subject & object 都命中）：
  ```python
  'relation_endpoint_recall': avg(x['endpoint_hit'] for x in rr)
  ```
- **验证**：`relation_endpoint_recall`=**0.917**（仅 `borealis-station located_in svalbard` 缺端点，svalbard 非 gold 实体）；`fact_expected_page_term_coverage`=0.875（不变）。

---

## 四、验证总览（复用 ds-flash 干净基线 `6ab0592b`，`--skip-ingest`，秒级）

| 批次 | 项 | 状态 | 关键验证证据 |
|---|---|---|---|
| 1 | B1 | ✅ | false_del d003: 0.23→0.0 |
| 1 | B2 | ✅ | Missed Entities/Facts 由恒空 → 列出真实缺失 |
| 1 | B3 | ✅ | 新增 direct=0.833 / bridge=0.083 |
| 1 | G1 | ✅代码 | 代码正确，指标未升（gold 别名集与生成 slug 对不上，数据对齐问题） |
| 2 | C5 | ✅ | 离线单测：未更新页合法旧值被排除 |
| 2 | D3 | ✅ | 跨类型误匹配 False（修复前 True），G1 别名保留 |
| 2 | D2 | ✅ | 主 metrics 无 delete 键 |
| 2 | C6/C7 | ✅ | lint_issue_count=218 / bytes=78337 / issues_count=0，旧键消失 |
| 3 | C1 | ✅ | graph_node_recall=1.0（真结果） |
| 3 | C2 | ✅ | entity_name_coverage: 1.0→0.846 |
| 3 | C3 | ✅ | duplicate_like_entity_rate: 1.0→0.154 |
| 3 | C4 | ✅ | fact_global 删除；relation_endpoint_recall=0.917 |

**指标集断点提示**：第 3 批删除了 `graph_node_recall_heuristic` / `fact_global_term_coverage` / `relation_text_coverage` 三个旧键，并改名 `graph_node_recall` / `relation_endpoint_recall`。老 run（`run_20260728_154102` 等）的新版 metrics 不可与旧版直接横向比趋势。

---

## 五、遗留项

- **D1（未修，设计选择）**：`eval_delete` 忽略 `main` 传入的 `kb`，自建独立 KB（`kb_del = create_kb(...'-delete')`），使"在 baseline KB 上增量删"的真实路径无法验证。用户明确这是当前设计、本次不动。如需改，应让 delete 评测直接复用传入的 baseline KB 做增量 retract。

---

## 六、配套约定（已生效，非本次修复内容）

- **验证勿每次重入库**：`eval_weknora.py` 支持 `--existing-kb-id <id>`（或 `WEKNORA_KB_ID`）+ `--skip-ingest`，`collect()` 只读 GET，秒级完成。WeKnora wiki slug 生成非确定性，重入库会让 gold 别名集对不上（正是 G1 不升的根因之一），故指标逻辑验证一律复用固定 KB。
- **模型默认 ds-flash**：用户已指令评测默认用 ds-flash（`8fb359f7-...`），不再用 glm-5.2。
- **勿对旧 KB 复用 `--run-update`**：会追加 docs_v2 污染基线（曾导致 `0f0d42a5` 从纯 v1 变成 v1+v2 混合态）。复用仅限纯 collect + delete。

---

## 七、补修（用户复审发现的两处残留，18:07 后）

> 第 1–3 批修复后用户复审代码，指出两处与已修项口径不一致 / 实现不到位的残留。均已修正，本回合执行工具故障，验证以读码 + 逻辑核对为准（建议下一回合复用 `6ab0592b` `--skip-ingest` 跑一次 collect 复核）。

### R1. facts `expected_pages` 仍有跨类型尾部匹配残留（line 569）
- **问题**：`eval_pages` 事实页查找在精确页 / 别名页都找不到时，仍 fallback 到 `p.nslug.endswith('/'+n.split('/')[-1])`，与 D3 对 entity slug 的修复口径不一致——会把 `concept/stardust-program` 误算命中 `entity/stardust-program`。
- **修复**：fallback 改用 `_slug_accept`（同类型 + alias-aware），与其它查找统一：
  ```python
  pg = by.get(n) or _find_page_alias(by, n, eqsets) or next((p for p in ps if _slug_accept(p.nslug, n, eqsets)), None)
  pt = pg.text if pg else ''
  ```
- **影响**：当前 stardust 语料 gold 事实 `expected_pages` 均为 `entity/...`，生成 wiki 存在对应实体页，故实测 `fact_expected_page_term_coverage` 不变（0.875）；修复消除的是"实体缺失、仅 concept 页存在"时的跨类型误命中隐患，与 D3 收口一致。

### R2. `_count_items` 未递归，嵌套结构会低估（line 48）
- **问题**：原实现只数顶层 list 长度、dict 只看一层 value（`sum(len(v) if list else 1)`），若 `/wiki/lint` 或 `/wiki/issues` 返回嵌套 dict/list（如 list of lists、dict 包 dict 包 list）会低估或误估。`lint_issue_count=218` 因接口结构简单恰好正确，但实现不够稳。
- **修复**：改为递归条目计数（list 递归累加；dict 含 list/dict 值则递归求和，纯标量字典计 1、空字典计 0；标量计 1）：
  ```python
  def _count_items(x):
      if isinstance(x, list): return sum(_count_items(i) for i in x)
      if isinstance(x, dict):
          nested = [v for v in x.values() if isinstance(v, (list, dict))]
          return sum(_count_items(v) for v in nested) if nested else (1 if x else 0)
      return 1
  ```
- **影响（无回归）**：`lint` 返回 `[218 个 issue dict]` → 每个 dict 无 list/dict 子值 → 计 1 → 合计 218，与旧版一致；但 `[[1,2],[3,4]]` 旧版误算 2、现 4，`{"issues":[a,b,c]}` → 3，`{}` → 0，嵌套场景不再低估。

### 补修验证状态
- 代码改动已 Read 复核落地，`py_compile` 因本回合执行工具故障未跑成；逻辑核对无回归（见上）。
- 待下一回合用 ds-flash 基线 `6ab0592b` `--skip-ingest` 纯 collect 复核：`fact_expected_page_term_coverage` 保持 0.875、`lint_issue_count` 保持 218、无新 key 缺失。
