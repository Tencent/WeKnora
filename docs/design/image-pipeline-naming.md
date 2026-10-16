# 图片流水线：配置键与轨迹键的命名规范

状态：生效（随「文档照片 OCR 前的几何预处理」一并引入）　适用范围：`internal/application/service/image_*.go`、`internal/types/image_actions.go`、`internal/types/knowledgebase.go`、前端知识库设置面板

这份文档是**长期规范**：新增流水线、动作、参数或轨迹字段时按它取名字；改动既有键名时按 §6 的清单同步改消费方。
可机械检查的部分都由测试钉住（§7），所以"规范"不只是约定，改坏了会红。

## 0. 一条总原则

> **命名 = 归属 + 名字；而"归属"只在同一个容器里有多方写入时才需要写出来。**

这条原则同时解释了"为什么动作要前缀、流水线不要"和"为什么落库用下划线、参数用点号"。

## 1. 三个容器、三种键形

| 容器 | 谁写进去 | 键形 | 例 |
|---|---|---|---|
| **配置**（`image_processing_config` 字段 / `image_pipeline_params` 槽位）| 面板与 API（用户的选择）| KB 字段用 snake_case；参数用 `<pipeline>.<key>` 或 `<pipeline>.<action>.<name>` | `image_attrs_enabled`、`default.ocr_enabled`、`smartocr.preprocess.mode` |
| **落库产出**（`chunks.image_info`，随知识走）| 动作自己 | 名词，snake_case，无状态词 | `caption`、`ocr_text`、`attrs`、`sha256` |
| **过程轨迹**（span output，`runContext.out`）| 多个动作 + 框架 + 服务层 | `config` 容器（配置回显）· `<action>.result` · `<action>.<数据名>` · 图片级裸名 | `config`、`ocr.result`、`ocr.chars`、`outcome`、`actions` |

**点号只出现在"归属边界"**（一层），名字内部只用下划线；**段内不得再出现点号**（否则路径拆分歧义，见 §8）。

槽位是**存放处**，不是第二种归属：一个设置写在谁名下，就存在谁的槽位里（`types.SlotKey` 生成，`types.ImagePipelineParam` 读回），**而且只存在那里**——没有第二种拼法兜底，读不到就是"没存过"，两侧都回落到字段声明的默认值。

这条同样适用于 `preprocess.mode` 与 `ocr_on_unobserved`：它们一度放在知识库顶层（`preprocess_mode`、`image_actions.ocr.on_unobserved`），移进槽位时**不保留兼容读取**——两个键都从未进入 main，没有存量数据值得迁就，旧值一律丢弃、按新默认重设即可（"用户上传或重新解析时一定会过一遍配置面板"是这次敢丢的前提）。前端镜像同一规则（`pipelineParamKey` / `readPipelineParam`）；两侧读法一致，是"面板显示的就是运行时用的"的前提。

## 2. 归属判据：一个调优项该挂"动作"还是"流水线"

按顺序问两个问题：

1. **这个参数在决定什么？**
   - 决定**本动作自己怎么跑**（调用方式、内部子项、介入策略）→ **动作私有**：`<pipeline>.<action>.<name>`
   - 决定**别的动作要不要跑 / 跑的顺序 / 多动作之间的取舍**（编排）→ **流水线私有**：`<pipeline>.<name>`
   - 属于**知识库整体的开关与选择** → **KB 字段**：snake_case
2. **复用语义是否稳定？** 同一动作被别的流水线复用时含义不变 → 动作私有（字段定义、文案、面板渲染都能复用一份）。

> 注意判据不是"它的主语是谁"。例：`ocr_on_unobserved` 的主语是 OCR，但它决定"观察失败时还要不要调度 OCR"——是编排，因此**流水线私有**。

**策略属于配置，不属于状态**：属性策略（哪些观测值触发 OCR、未观察到怎么办）是用户/KB 的选择，与开关同级；一次运行按它**做出了什么决定**才是状态。

## 3. 三族的词形（跨容器不会混）

| 族 | 词形 | 例 |
|---|---|---|
| **设置族** | `*_enabled` · `mode` · 策略名 | `ocr_enabled`、`thinking_enabled`、`preprocess.mode`、`ocr_on_unobserved` |
| **状态族** | `<action>.result`，取值 = 状态词 + 原因 | `ocr.result = skipped_no_text` |
| **数据族** | 名词：`chars` / `raw_chars` / `preview` / `bytes` / `error` / `error_code` / `attrs` | `ocr.chars`、`preprocess.bytes`、`image_bytes` |

- 布尔开关统一 `<name>_enabled`（与系统既有惯例一致：`<name>_enabled` 十余处 vs `enable_*` 三处旧写法）。
- **不设独立的 reason 键**：原因并入 `result` 的取值（`skipped_no_text`、`skipped_declined` …）。
- 一个 owner 的**一个方面一个键**，同一事实不记两处。

## 4. 状态词与结果词表

状态词取自系统既有的 span 状态（`types/knowledge_span.go` 的 `pending/running/done/failed/skipped/cancelled`），本规范只用其子集 + 原因后缀：

| 键 | 取值 |
|---|---|
| `caption.result` | `done` / `empty` / `skipped_disabled` / `failed` |
| `observation.caption_result` | 同上（智能模式里描述是观察的副产品）|
| `observation.result` | `all_done` / `partial` / `none` / `failed`（**只管属性维度**）|
| `preprocess.result` | `done` / `failed` / `skipped_no_capability` / `skipped_off` / `skipped_no_text` / `skipped_already_tidy` / `skipped_declined` |
| `ocr.result` | `done` / `failed` / `skipped_no_text` / `skipped_attr_policy` / `skipped_observation_failed` |
| `outcome`（图片级）| `done` / `partial_failure` / `failed` / `skipped_unreadable_image` / `skipped_no_content` |

三层的"状态词"分工：span 的 `status`（框架：跑没跑完）／动作的 `result`（这一步得出了什么）／图片的 `outcome`（整张图怎么了）。

## 5. 契约键 vs 诊断键

- **契约键**：有生产消费方（写进轨迹后又被谁读走）。例如 `ocr.result`（决定子 span 成功/失败、整图是否算"无产出"）、`ocr.error_code`（落进 span 的 `error_code`，前端本地化标题用它）。
  **契约键用常量**（写入与读取共用一处定义，常量注释写明消费方）——它们分处两个函数，靠两处字面量对齐迟早漂移。
- **诊断键**：零生产读取方，只给人、日志与测试看（如 `preprocess.*`、`ocr.preview`、`config`）。可以自由改名，只动名字。
- 区分二者的价值：**改动风险一眼分明**。

## 6. 改键名时要一起改哪里

1. **写入点**（动作/框架/服务层）
2. **读取点（消费方）**——最容易漏：
   - 动作里的 `resolve()` 闭包（读回 `*.error_code` / `*.error` / `*.chars` 决定 span 成功或失败）
   - 流水线的决策处（读 `observation.result` 决定 skip 原因）
   - 服务层的整图判定（读 `ocr.result` / `caption.error` 决定 `outcome` 与是否向任务层报错）
3. **前端**：设置面板的 i18n 文案路径（`imagePipeline.<pipeline>.<field.key>.<label|description>`，缺文案自动回落后端 Label）；处理时间线的 `error_code` 文案
4. **测试**：golden 序列测试、属性决策测试、错误码测试
5. **文档**：本文件与相关接口的 doc comment
6. **前端槽位镜像**：`frontend/src/utils/imageProcessingConfig.ts` 的 `pipelineParamKey` / `readPipelineParam`（对应 `types.SlotKey` / `types.ImagePipelineParam`）—— 两侧拼法必须一致，否则面板写进去的值运行时读不到。前端**不认字段名**：它渲染注册表给的字段、按槽位读写，所以增删或改名一个字段不需要动前端

## 7. 由哪些测试钉住

| 规则 | 测试 |
|---|---|
| 动作声明的键必须以自己的动作 id 为前缀，裸名不含点 | `TestDeclaredActionFieldsAreNamespaced`（`internal/types`）|
| 流水线只能携带动作声明的带点键，不得自造 | `TestPipelineFieldKeysAreOwned` |
| 同一 owner 内键唯一 | `TestPipelineFieldsArePrivate`、`TestFieldDeclarationOrder` |
| 参数按流水线隔离、无槽位的键不算值、默认值回落 | `TestParamIsIsolatedPerPipeline`、`TestParamReadsOnlyItsOwnNamespace` |
| 流水线声明的字段分三段（常设 / 高级 / 说明）且顺序即面板顺序 | `TestFieldDeclarationOrder` |
| 实际执行的动作序列 | `TestImagePipelineActionSequences` |
| **设置不得与结果同形**（末段不得是 `result`/`chars`/`bytes`…）| `TestNoSettingIsNamedLikeAResult` |
| **`<action>.result` 由动作 id 拼出**，键前缀必须是已注册动作 | `TestActionResultKeysAreNamedAfterTheirAction`、`TestEveryActionTraceKeyCarriesAnActionNamespace` |
| **每个 result 取值都在词表内**（含只在一个分支里写的值）| `TestTraceResultsStayInTheirVocabulary` |
| 槽位拼接不重复前缀、只认槽位、nil 槽位视作未设置 | `TestSlotKeyDoesNotDoubleThePipelineID`、`TestImagePipelineParamReadsOnlyTheSlot` |
| **运行读到的设置 = 轨迹回显的设置** | `TestTheRunReadsTheSettingItsTraceEchoes` |
| 前端写入的键与读取的键一致，且切换方案不丢参数 | `ImagePipelineSettings.test.ts`、`imageProcessingConfig.test.ts` |

## 8. 已知约束

- **点号键**是 string-keyed map 里的普通字符串（Go / JSON / JSONB 均无语法问题），但：
  1. 段内不得再出现点号；
  2. 不能做结构体字段名或 DB 列名（所以落库 `image_info` 用下划线）；
  3. TOML / YAML 这类"点号即结构"的格式不能承载，若将来要导出需转义或改名；
  4. 用 jq 取值要写 `.["smartocr.ocr.thinking_enabled"]`。
- 参数槽位的前缀是字符串拼接（`"<pipeline>.<key>"`）；若将来需要反向解析，规则是"切第一个点"（`strings.Cut`）。
