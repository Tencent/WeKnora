# Weknora Roadmap
📌 本路线图用于明确项目核心方向，内容会随需求和贡献动态更新。

## Vision
独立部署个人知识库，支持文档、数据、图片，基于传统RAG框架拓展更多LLM应用场景。

## Next Phase
- 上传文件大小可配置
- Agent模式：模型自动判断是否调用文档检索、网页检索等功能
- 配置管理：Web端可配置模型、提示词，启用存储检索模块
- 支持更多文档类型（csv、xls、xlsx、html等）
- 向量数据库支持（milvus等）
- 优化文档解析速度与精度，优化分块策略

## Future
- Agent模式扩展更多调用工具
- 数据Agent：支持数据统计分析
- 批量知识管理：导入、导出、迁移
- 启动模块可配置：自定义可选组件，减少依赖
- 简化配置：Web端统一管理，减少配置文件
- 权限管理：解构用户、租户、知识库等权限，支持管理员、用户组等
- 丰富分块模式（语义分块、关键词分块等）
- 扩展解析工具（minerU、pp-structure等）
- 扩展向量数据库（milvus等）
- 丰富OCR模型

## Done
- 多语言支持（中、英、俄）
- 支持Neo4j图数据库
- XSS注入防护
- 用户登录功能
- MCP服务端实现
- 提供官方Docker镜像，支持Windows、Linux、macOS
- 支持阿里云模型接入

## How to participate
1. 对任何功能有想法？可在 Issues 中发起讨论（标签：`roadmap-discuss`）；
2. 提交 PR 时，关联对应的 Roadmap 阶段；
3. 发现需求缺口？提交 Issue 并添加 `feature-request` 标签，我们会评估后纳入 Roadmap。

## Change Notice
- 本 Roadmap 将不定期更新，同步最新进度和需求调整；
- 若有重大方向变更，会在 Issues 发布公告；
- 优先级会根据用户反馈和贡献者资源动态调整。