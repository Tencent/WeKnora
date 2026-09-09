# WeKnora 模型管理

租户接入并使用外部 LLM 服务（Chat / Embedding / Rerank / ASR / VLM）的配置与调用体系。

## Language

**模型目录（Catalog）**:
型号参数数据源（`models.json`，WeKnora 自定义 schema；models.dev 的 api.json 仅作示例参考，不采用其格式），用于在配置表单中预填参数。全局单份、服务启动时拉取，不可被租户覆盖。
_Avoid_: 模型市场、模型仓库、models.dev 目录

**预填（Prefill）**:
在表单尚未保存时，按取值链（接口元数据 → 模型目录 → 留空）自动填入参数值的动作。仅作用于未保存的表单；模型记录一旦保存，存储值即终态，目录更新永不回刷（见 ADR 0001）。
_Avoid_: 自动同步、自动纠正

**思考档位（Thinking Level）**:
平台五档词表（low / medium / high / xhigh / max）中厂商声明支持的子集。选项来自厂商适配器声明；某模型的选中值来自目录命中或用户多选，目录未命中则留空待用户勾（见 ADR 0002）。
_Avoid_: 思考强度数值、reasoning effort（厂商私有表达）

**模型类型（Model Type）**:
一条模型记录的服务类别，枚举 KnowledgeQA / VLLM / Embedding / Rerank / ASR。KnowledgeQA 与 VLLM 共用 Chat 参数分片，多模态输入经 InputModalities 表达；本期不改名。
_Avoid_: LLM、VLM（曾议改名，已否决）

**能力声明（Capabilities）**:
厂商适配器在编译期声明的、按模型类型分片的能力事实（支持哪些档位、输入模态、协议族、usage 上报等），随厂商接口下发给前端渲染。厂商知识的唯一存放处。
_Avoid_: 厂商配置、前端厂商表