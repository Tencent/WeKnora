# 开启多模态（图像）向量检索

默认情况下，WeKnora 只能通过 OCR 文字和 VLM 生成的图片描述间接检索到一张图。
开启本功能后，系统会**直接对图片本身做向量化**：文本提问即可召回图片，图片与文字落在同一个向量空间里，一次检索同时命中两者。

> 典型效果：问「文档里那张红色消防车的图」，即使正文和 OCR 里一个字都没提到「消防车」，也能把那张图召回。

## 前置条件

- 已完成 WeKnora 后端与前端的基础部署
- 一个**支持图像输入**的 Embedding 服务（下文的 vLLM 部署样例，或云厂商的 vision embedding 服务）
- 知识库已启用**向量检索**

> 图像向量不能独立开启：它是向量检索的一个修饰项，依附于同一套向量索引和同一个 Embedding 模型。
> 若知识库未开启向量检索，图像条目会被直接丢弃——否则只有 `![](url)` 的行会污染 BM25 关键词索引。

### 与 OCR / 图片描述的区别

| | 图像向量（本功能） | OCR / 图片描述 |
|---|---|---|
| 编码对象 | 图片像素 | 图片里的文字、VLM 生成的描述 |
| 能召回「图里没写字」的图 | 能 | 不能 |
| 需要多模态 Embedding 模型 | 是 | 否（只需 VLM） |
| 成本 | 每张图一次 embedding 调用 | 每张图一次 VLM 调用 |

两者**不冲突**，可以都开。图像向量缺失时会自然退化到 OCR / 图片描述。

---

## 步骤一：部署多模态 Embedding 服务

### vLLM（已验证）

以 `Qwen3-VL-Embedding-2B` 为例：

```bash
vllm serve Qwen/Qwen3-VL-Embedding-2B \
  --runner pooling \
  --trust-remote-code \
  --served-model-name qwen3-vl-embedding-2b \
  --port 8000
```

参数说明：

- **`--runner pooling` 必需**。不加会按文本生成模型加载，`/v1/embeddings` 直接不可用。旧版本 vLLM 用 `--task embedding`。
- `--served-model-name` 建议起一个短名，稍后要原样填进 WeKnora 的模型名称。
- 部分模型自带的 chat template 不适合 embedding API，需要用 `--chat-template` 覆盖（例如 NVIDIA Nemotron VL 系列）。表现为「能启动、能返回向量，但检索效果很差」，务必按模型官方文档确认。
- 维度：2B 为 2048，8B 为 4096。首次启动会从 HuggingFace 拉取权重，注意磁盘与显存。

其他可选模型：`Alibaba-NLP/gme-Qwen2-VL-2B-Instruct`、`nvidia/llama-nemotron-embed-vl-1b-v2`、`TIGER-Lab/VLM2Vec-Full`（后者需 `--task embedding`）。

### 验证服务本身

多模态 embedding 走的是 **chat-style `messages` 信封**，不是普通的 `input: [string]`：

```bash
curl -s http://localhost:8000/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3-vl-embedding-2b",
    "messages": [
      {"role": "user", "content": [{"type": "text", "text": "一只红色的消防车"}]}
    ],
    "encoding_format": "float"
  }' | head -c 200
```

预期返回 `data[0].embedding`，是一个长度等于模型维度的浮点数组。**这一步必须先通过再往下配置**，
否则后面所有排查都会被「模型根本不支持」这件事掩盖。

建议再对比一次「同一句话走 `input`」与「走 `messages`」的向量：两者应当不同。如果完全相同，
说明服务端没有对 `messages` 走 chat template，该功能无法正常工作（见文末「工作原理」）。

---

## 步骤二：在 WeKnora 注册 Embedding 模型

进入「设置 → 模型 → 新建模型」，类型选 **Embedding**：

| 字段 | 填写 |
|---|---|
| 模型名称 | 与服务端 `--served-model-name` 一致，如 `qwen3-vl-embedding-2b` |
| Base URL | `http://<host>:8000/v1` |
| 维度 | 与模型一致。**务必实测**，不同系列的 2B 并不一样：`Qwen3-VL-Embedding-2B → 2048`、8B → 4096，而 `gme-Qwen2-VL-2B-Instruct → 1536` |
| 额外配置 | 见下方「能力判定」；用 SGLang 部署时还要加 `multimodal_envelope: "sglang"` |

> 拿不准维度就先跑一次上面的 curl，它会打印实际 `dim`。维度填错不会立刻报错，
> 而是在写入向量时才暴露为「维度不匹配」，排查成本比现在实测高得多。

### 能力判定：系统怎么知道这个模型能不能吃图

判定顺序如下，**显式声明优先**：

1. 模型额外配置里写了 `supports_image_embedding` → 以它为准（`true`/`1`/`yes`/`on` 视为是）
2. 否则按模型名匹配关键词：`vision`、`multimodal`、`clip`、`siglip`、`colpali`、`colqwen`、`wemm`、`vl-embedding`、`embed-vl`、`vlm2vec`

因此：

- `Qwen3-VL-Embedding-2B`、`nvidia/llama-nemotron-embed-vl-1b-v2` 这类名字**能被自动识别**（靠 `vl-embedding` / `embed-vl`）。
- `gme-Qwen2-VL-2B-Instruct` **不在关键词列表里**（`gme` 不是 hint），需要在模型编辑弹窗
  （设置 → 模型 → Embedding → 高级选项）里打开**「支持图像输入（多模态向量）」**，
  否则知识库里的「图像向量」开关会灰掉。打开后保存，等价于写入下面的 `supports_image_embedding: "true"`。
- 自托管了一个名字毫无特征的 checkpoint（例如就叫 `my-embedding`）时，同样打开该开关，或显式声明：

```yaml
parameters:
  extra_config:
    supports_image_embedding: "true"
```

- 反过来，如果某个 `vision` 名字的模型实际只接受文本，在编辑弹窗里把该开关关掉即可
  （等价于 `supports_image_embedding: "false"`），不必改名。

> 界面上开关的初始状态就是后端算出来的判定结果，所以「看到开着」就是真的支持。
> 新建模型时如果没动过这个开关，前端不会写入 `false`——否则会静默关掉那些靠模型名
> 本该自动识别的多模态模型（如 `qwen3-vl-embedding`）。

> 这个判定只在**保存知识库配置时**做一次校验。判错不会让服务崩溃，但会导致「开关能开、图片却永远不入库」。

### 预置到配置文件（可选）

`config/builtin_models.yaml`：

```yaml
builtin_models:
  - id: builtin-embedding-multimodal
    name: qwen3-vl-embedding-2b
    type: Embedding
    source: remote
    is_default: false
    parameters:
      base_url: http://vllm:8000/v1
      api_key: ${EMBEDDING_API_KEY}
      provider: openai
      extra_config:
        supports_image_embedding: "true"
    embedding_parameters:
      dimension: 2048
```

---

## 步骤三：在知识库开启「图像向量」

1. 打开知识库的索引策略设置
2. 勾选 **「图像向量（以文搜图）」**
3. 保存

保存时的行为：

- 会校验当前 Embedding 模型是否**真的**支持图像（构造真实 embedder 去问，不是只看名字）。不支持时**拒绝保存**并提示模型名，而不是让你保存一个永远不生效的配置。
- 开关从关到开、或从开到关，都会自动在后台入队一个**存量向量重算**任务（维护队列）。原因见下一节。
- 必须先开启向量检索，否则保存会被拒绝。

---

## 步骤四：验证

1. 上传一个**含图片**的文档（PDF / Word / Markdown 均可），等待解析完成
2. 提一个只有看图才答得上的问题，例如「文档里那张架构图」
3. 观察检索结果里出现 `image_vector` 类型的片段

后端日志关键字：`[ImageMultimodal]`、`image_vector`。

### 存量文档怎么办

开启开关后，后台任务会一次性做三件事，**不需要重新解析文档**：

| 动作 | 说明 |
|---|---|
| 重算文本向量 | 把已有文本 chunk 重新编码到多模态空间。不重新读取文档、不调用 VLM |
| 翻转已有图像向量的可见性 | 图像向量只存在于多模态空间，开关关掉时它们不可达，会被隐藏而非删除（再开即恢复，不花一分钱） |
| 回补缺失的图像向量 | 重新从存储读取图片字节，为开关开启前入库的图补生成图像向量 |

回补是逐图独立、失败不中断的：某张图已从存储删除或凭据轮换导致读不到，
只会跳过它并记录一条 warning，其余图片照常处理。任务本身也**不重试**——
一个坏文档不该让整个知识库重跑一遍并重新计费。

> 回补的范围是「文档里能定位到的图片」：优先取 OCR / 图片描述 chunk 上记录的
> 图片信息；连 OCR 和描述都为空的图（纯线条图、扫描件常见）则回退到从文本
> chunk 的 `![](url)` 引用里提取。若一张图既没留下 chunk、文本里也没留下引用，
> 就只能靠重新解析。

新上传的文档不受影响，入库时就直接生成图像向量。

---

## 工作原理（为什么文本也得走同一信封）

这段不是实现细节，而是**排查「检索变差」时必须知道的事**。

1. 图像只能通过多模态信封发送，普通的 `input: [string]` 数组没有承载图片的字段。
   vLLM 用 chat-style `messages`，SGLang 用 `input` 的 item 数组（见上文）。
2. 服务端会把多模态请求交给模型的 **chat template** 渲染后再编码。这意味着同一句话，
   走 `input` 和走多模态信封得到的**是两个不同的向量**。
3. 于是：只要知识库里存了图像向量，它的查询就被永久绑定在多模态空间里。
   **同一批文本 chunk 也必须用同一信封**，否则查询到不了它们。

WeKnora 用一个唯一谓词（`usesMultimodalEnvelope`）同时驱动索引侧和查询侧，
两者结构上不可能漂移。

**这个失效模式很隐蔽**：文本走错信封时，分数会明显下降，但 top-1 往往仍然是对的，
且不报任何错。如果你自己实现 provider，务必保证查询与入库走同一信封。

---

## 限制与降级行为

| 场景 | 行为 |
|---|---|
| 模型不支持图像 | 保存知识库配置时被**拒绝**并提示模型名（不静默失败） |
| 某张图 embedding 失败 | 只丢弃这张图的图像向量，退化回 OCR / 图片描述，**不阻塞整批** |
| 整批全是图且全部失败 | 返回错误（否则 chunk 会被标记为已索引却没有向量，属于静默失效） |
| 未开启向量检索 | 图像条目直接丢弃，避免 `![](url)` 污染 BM25 |
| rerank 阶段 | 图像向量片段**豁免 rerank**：它的内容只有图片链接，文本 reranker 会把它当空文本丢弃 |
| 换用另一个多模态模型 | 不同模型的向量空间不同，需要**重新解析**文档，光重算文本向量不够（重算只重编文本，不重读图片字节） |
| 成本 | 每张图一次 embedding 调用，图片多的文档成本线性增长 |

---

## 常见问题排查

| 现象 | 原因与处理 |
|---|---|
| 保存时提示「模型不支持图像」 | 模型名不含可识别关键词且未声明 `supports_image_embedding`；或模型确实只支持文本。换多模态模型，或显式声明该参数 |
| 服务起得来但 `/v1/embeddings` 404 或报「not an embedding model」 | 启动命令缺 `--runner pooling`（旧版 `--task embedding`） |
| 图像一直没入库，日志无报错 | 检查模型能力判定是否误判为「不支持」；确认知识库已开启向量检索 |
| 开启后**文本**检索明显变差 | 极可能是信封错配。确认服务端对 `messages` 请求返回的是 chat template 渲染后的向量；部分模型需要 `--chat-template` 覆盖 |
| 开启后搜不到任何图（存量文档） | 查日志 `Image vector backfill for knowledge ...: N created`；为 0 则看是否有 `Backfill: cannot read image` 告警（存储里已无此对象或凭据已轮换） |
| 能召回图但排序很差 | 图像片段已豁免 rerank，排名即向量检索分；确认查询语言与模型训练语料匹配 |

相关页面：[模型管理](/03-features/06-models)、[知识库](/03-features/02-knowledge-base)、[检索引擎](/03-features/05-retrieval-engines)。
