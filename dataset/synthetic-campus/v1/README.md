# 星帆共享实验室合成评测语料

本数据集由人工智能（Artificial Intelligence，AI）编写，含 6 份虚构文档、24 个段落与 20 道参考题。参考答案保持待人工核验标记。文件摘要按 UTF-8 编码、LF 换行计算。

在项目根目录执行 `python3 scripts/prepare-synthetic-evaluation.py`，得到 `artifacts/synthetic-evaluation/version-input.json`。该文件可作为数据集版本接口 `POST /api/v1/evaluation/datasets/{id}/versions` 的请求体；数据集身份通过 `POST /api/v1/evaluation/datasets` 创建。制备命令只读源文件，不调用模型。

`registry-input.json` 保存已制备的标准请求体。`python3 scripts/prepare-synthetic-evaluation.py --check` 校验源文档摘要、段落原文、证据映射与请求体漂移，检查模式不写文件。更新请求体使用 `python3 scripts/prepare-synthetic-evaluation.py --output dataset/synthetic-campus/v1/registry-input.json`。文件比较统一 Windows 与 Unix 换行符。

持续集成（Continuous Integration，CI）中的 Go 测试直接读取已检入请求体，无须运行 Python：`go test ./internal/application/service -run '^TestSyntheticCampusRegistryExecutionIntegration$' -count=1`。该测试通过真实 SQLite 注册表创建不可变版本，并由评测服务读取冻结版本，核对原始问题顺序、来源元数据、17 道有答案题、3 道无答案题及 97 条相关性记录。新增版本、跨租户请求与冻结摘要篡改均有独立断言，测试不调用模型。

段落元数据记录源文件与摘要。评测服务根据不可变数据集版本建立实验分块，并保存段落到分块的对应关系。来源段落标识不承担数据库分块编号的作用。三道无答案题为全部语料段落登记 0 级相关性，答案质量与检索指标各自按指标定义处理空相关集合。

文档上传只选择 `source_docs/`；题目和参考答案保留在评测侧。实验报告标注 AI 合成语料，质量结论以该数据集和实际运行范围为限。
