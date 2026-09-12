# 开启知识图谱功能指南

本文档介绍如何在 WeKnora 中启用并验证知识图谱（Neo4j）功能，帮助你完成从环境准备到前端配置的全流程。

## 前置条件

- 已完成 WeKnora 后端与前端的基础部署。
- 具备可用的 Docker/Docker Compose 运行环境。
- 本地或远端可访问的 Neo4j 服务（推荐使用项目自带的 Docker Compose）。

## 步骤一：配置环境变量

在项目根目录的 `.env` 文件中新增或修改以下变量：

```
NEO4J_ENABLE=true
NEO4J_URI=bolt://neo4j:7687
NEO4J_USERNAME=neo4j
NEO4J_PASSWORD=your_strong_password
# 可选：NEO4J_DATABASE=neo4j
```

说明：

- `NEO4J_ENABLE` 设置为 `true` 才会启用知识图谱相关逻辑。
- `NEO4J_URI` 中的 `neo4j` 为 docker-compose 服务名，如使用外部实例请替换为实际地址。
- 如果生产环境使用密钥管理，请确保密码通过安全方式注入。

## 步骤二：启动 Neo4j 服务

项目附带 Neo4j 组件，可直接用以下命令启动：

```bash
docker-compose --profile neo4j up -d
```

常见验证命令：

```bash
docker ps | grep neo4j
```

若需要自定义挂载或内存，可编辑 `docker-compose.yml` 中 `neo4j` 服务配置。

## 步骤三：重启 WeKnora 服务

为了让新的环境变量生效，重启后端与前端（示例仅供参考）：

```bash
make stop && make start
# 或者
docker compose up -d --build
```

确保后端日志中出现 `neo4j` 初始化成功的提示。

## 步骤四：在前端启用实体/关系抽取

1. 登录 WeKnora 前端管理页面。
2. 打开「知识库设置」或创建新的知识库。
3. 勾选「启用实体抽取」与「启用关系抽取」开关。
4. 根据界面提示补充所需的 LLM、回调或模型参数（若有）。

保存后，系统会在文档入库阶段自动触发实体与关系抽取任务。

## 步骤五：验证知识图谱

### 方式一：Neo4j 控制台

1. 访问 `http://localhost:7474`（或对应主机/端口）。
2. 使用 `.env` 中的账号密码登录。
3. 执行 `MATCH (n) RETURN n LIMIT 50;` 检查是否有新节点/关系。

### 方式二：WeKnora 界面

在知识库或对话页面中上传文档后，前端应展示图谱可视化入口；对话时系统会自动根据意图查询图谱并返回补充信息。

## 常见问题排查

- **无法连接 Neo4j**：确认网络可达、`NEO4J_URI` 与用户名密码正确，并检查 Neo4j 容器日志。
- **未生成节点**：确认知识库已开启实体/关系抽取，且上传的文档已完成解析；查看后端日志中是否有抽取任务异常。
- **查询无结果**：尝试在 Neo4j 控制台执行 `CALL db.schema.visualization;` 查看 schema 是否存在，必要时重新导入文档。

完成以上步骤后，知识图谱功能即成功启用，可结合 RAG 及 Agent 流程提升问答质量。

