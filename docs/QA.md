# 常见问题

## 1. 如何查看日志？
```bash
docker compose logs -f app docreader postgres
```

## 2. 如何启动和停止服务？
```bash
# 启动服务
./scripts/start_all.sh

# 停止服务
./scripts/start_all.sh --stop

# 清空数据库
./scripts/start_all.sh --stop && make clean-db
```

## 3. 服务启动后无法正常上传文档？

通常是Embedding模型和对话模型没有正确被设置导致。按照以下步骤进行排查

1. 查看`.env`配置中的模型信息是否配置完整，其中如果使用ollama访问本地模型，需要确保本地ollama服务正常运行，同时在`.env`中的如下环境变量需要正确设置:
```bash
# LLM Model
INIT_LLM_MODEL_NAME=your_llm_model
# Embedding Model
INIT_EMBEDDING_MODEL_NAME=your_embedding_model
# Embedding模型向量维度
INIT_EMBEDDING_MODEL_DIMENSION=your_embedding_model_dimension
# Embedding模型的ID，通常是一个字符串
INIT_EMBEDDING_MODEL_ID=your_embedding_model_id
```

如果是通过remote api访问模型，则需要额外提供对应的`BASE_URL`和`API_KEY`:
```bash
# LLM模型的访问地址
INIT_LLM_MODEL_BASE_URL=your_llm_model_base_url
# LLM模型的API密钥，如果需要身份验证，可以设置
INIT_LLM_MODEL_API_KEY=your_llm_model_api_key
# Embedding模型的访问地址
INIT_EMBEDDING_MODEL_BASE_URL=your_embedding_model_base_url
# Embedding模型的API密钥，如果需要身份验证，可以设置
INIT_EMBEDDING_MODEL_API_KEY=your_embedding_model_api_key
```

当需要重排序功能时，需要额外配置Rerank模型，具体配置如下：
```bash
# 使用的Rerank模型名称
INIT_RERANK_MODEL_NAME=your_rerank_model_name
# Rerank模型的访问地址
INIT_RERANK_MODEL_BASE_URL=your_rerank_model_base_url
# Rerank模型的API密钥，如果需要身份验证，可以设置
INIT_RERANK_MODEL_API_KEY=your_rerank_model_api_key
```

2. 查看主服务日志，是否有`ERROR`日志输出

## 4. 如何开启多模态功能？
1. 确保 `.env` 如下配置被正确设置:
```bash
# VLM_MODEL_NAME 使用的多模态模型名称
VLM_MODEL_NAME=your_vlm_model_name

# VLM_MODEL_BASE_URL 使用的多模态模型访问地址
VLM_MODEL_BASE_URL=your_vlm_model_base_url

# VLM_MODEL_API_KEY 使用的多模态模型API密钥
VLM_MODEL_API_KEY=your_vlm_model_api_key
```
注：多模态大模型当前仅支持remote api访问，固需要提供`VLM_MODEL_BASE_URL`和`VLM_MODEL_API_KEY`

2. 解析后的文件需要上传到COS中，确保 `.env` 中 `COS` 信息正确设置：
```bash
# 腾讯云COS的访问密钥ID
COS_SECRET_ID=your_cos_secret_id

# 腾讯云COS的密钥
COS_SECRET_KEY=your_cos_secret_key

# 腾讯云COS的区域，例如 ap-guangzhou
COS_REGION=your_cos_region

# 腾讯云COS的桶名称
COS_BUCKET_NAME=your_cos_bucket_name

# 腾讯云COS的应用ID
COS_APP_ID=your_cos_app_id

# 腾讯云COS的路径前缀，用于存储文件
COS_PATH_PREFIX=your_cos_path_prefix
```
重要：务必将COS中文件的权限设置为**公有读**，否则文档解析模块无法正常解析文件

3. 查看文档解析模块日志，查看OCR和Caption是否正确解析和打印


## 5. 如何使用数据分析功能？

在使用数据分析功能前，请确保智能体已配置相关工具：

1. **智能推理**：需在工具配置中勾选以下两个工具：
   - 查看数据元信息
   - 数据分析

2. **快速问答智能体**：无需手动选择工具，即可直接进行简单的数据查询操作。

### 注意事项与使用规范

1. **支持的文件格式**
   - 目前仅支持 **CSV** (`.csv`) 和 **Excel** (`.xlsx`, `.xls`) 格式的文件。
   - 对于复杂的 Excel 文件，如果读取失败，建议将其转换为标准的 CSV 格式后重新上传。

2. **查询限制**
   - 仅支持 **只读查询**，包括 `SELECT`, `SHOW`, `DESCRIBE`, `EXPLAIN`, `PRAGMA` 等语句。
   - 禁止执行任何修改数据的操作，如 `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `DROP` 等。


## P.S.
如果以上方式未解决问题，请在issue中描述您的问题，并提供必要的日志信息辅助我们进行问题排查