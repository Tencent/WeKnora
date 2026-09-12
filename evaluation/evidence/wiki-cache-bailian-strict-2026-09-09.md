# Wiki 缓存严格 A/B：阿里云百炼 qwen3.7-plus

本报告记录 2026-09-09（北京时间）在本地 WeKnora 上执行的真实 Provider 实验。原始结构化证据为 `wiki-cache-bailian-strict-2026-09-09.json`。报告不包含 Prompt、模型回答、API Key、租户标识或其他密钥。

## 实验协议

- 使用生产 Wiki 页面更新的提示结构构造 3 个隔离请求。
- 先执行 3 次冷请求，再逐字节重放对应的 3 次暖请求。
- 冷暖组的完整请求 HMAC 指纹和公共前缀 HMAC 指纹必须逐组一致。
- 缓存命中率由 Provider 返回的 `cached_tokens` usage 字段判定，而不是根据结果反推。
- 不创建、修改或删除任何 Wiki 页面。

## 固定配置

- Provider：阿里云百炼，华北 2（北京）
- 模型：`qwen3.7-plus`
- 重复次数：3 组，共 6 次模型调用
- 价格快照：普通输入 2 CNY/M、输出 8 CNY/M、缓存读取 0.4 CNY/M、缓存创建 2 CNY/M
- 后端代码版本：`0ffea0d9`
- 工作负载 SHA-256：`sha256:b5d695c3f741b2af87b7518845ee9f994a478c1ad2538e51a9b8ff9a6290db08`
- 配置 SHA-256：`sha256:3fb9c133581c260c70238fb0961484ab1935aa9b5d4a60c97f8aecdf6d70b7bd`

价格依据：阿里云百炼 `qwen3.7-plus` 公开价格页与上下文缓存计费说明。隐式缓存创建按普通输入价格计费，命中部分按普通输入价格的 20% 计费。实际账单可能由免费额度抵扣；本报告比较的是同一价格快照下的估算原价。

## 结果

| 指标 | 冷缓存组 | 暖缓存组 | 变化 |
| --- | ---: | ---: | ---: |
| 成功调用 | 3/3 | 3/3 | 不变 |
| Prompt Token | 6,279 | 6,279 | 不变 |
| 缓存读取 Token | 0 | 6,255 | +6,255 |
| 缓存 Token 命中率 | 0.00% | 99.62% | +99.62 pp |
| Token 总量 | 6,559 | 6,563 | +4（来自输出长度波动） |
| 耗时中位数 | 2.675 s | 2.364 s | -0.311 s（-11.63%） |
| 耗时 P95 | 3.219 s | 2.397 s | -0.822 s（-25.54%） |
| 估算成本 | 0.014798 CNY | 0.004822 CNY | -0.009976 CNY（-67.41%） |

## 严格性与隐私校验

- `strict_validation.passed=true`，无警告。
- 3 组完整请求指纹逐组一致，3 组公共前缀指纹逐组一致。
- 六次调用均包含 Provider 上报的缓存用量；暖组 3/3 命中。
- 六次调用使用同一模型及同一价格快照。
- 导出仅包含模型元数据、用途、Token、缓存、费用、耗时和 HMAC 指纹，不包含请求或响应正文。
- 报告 SHA-256：`sha256:7f4d1de8e1d094f26a23f3be040114cd607dab8c9037e6ec9a52d8d04aa22b3a`

可在仓库根目录复核证据完整性：

```bash
go run ./cmd/evidenceverify -report evaluation/evidence/wiki-cache-bailian-strict-2026-09-09.json
```

## 结论边界

本实验可以证明：在请求完全配对、模型和价格配置固定的条件下，Wiki 公共前缀缓存命中率显著提高，同时估算成本与延迟均下降。回答质量不由本实验判断；质量结论应引用固定数据集的 RAG 重复评测，避免用缓存 A/B 的输出反推质量。

阿里云官方参考：

- <https://help.aliyun.com/zh/model-studio/qwen3-7-plus>
- <https://help.aliyun.com/zh/model-studio/context-cache>
