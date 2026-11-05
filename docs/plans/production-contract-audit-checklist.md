# 生产 WeKnora 契约审计清单

## 1. 审计原则

- R0 只获取文档、版本信息和只读响应。
- 不调用 `POST /evaluation`，不上传数据集，不触发模型调用。
- API Key 只在本机运行时输入，不粘贴到文档、聊天、截图或 Git。
- 保存响应前删除 API Key、Cookie、Token、内部域名、人员信息和业务数据。
- HTTP 重定向关闭；遇到跳转先由运维确认目标地址。

## 2. 需要生产方提供的信息

| 信息 | 示例 | 是否包含密钥 |
| --- | --- | --- |
| WeKnora Base URL | `https://weknora.example.com` | 否 |
| 版本与构建标识 | `1.2.0` / commit / build time | 否 |
| Edition | community / standard / enterprise | 否 |
| API 文档或 OpenAPI | 文件或只读链接 | 否 |
| API 认证方式 | `X-API-Key` / Bearer / 其他 | 否 |
| 空间选择规则 | Key 绑定空间或额外 Header | 否 |
| 数据包格式与部署流程 | 五 Parquet / 上传接口 / 人工部署 | 否 |
| dataset ID 规则 | 固定、可配置或服务端生成 | 否 |

不要把真实 API Key 作为上述信息的一部分发送。

## 3. 只读检查顺序

以下命令仅是运维或开发人员本机执行模板。将值放入临时环境变量，终端关闭后失效；不要把命令历史或响应原文提交到仓库。

```bash
export WEKNORA_AUDIT_BASE_URL='https://weknora.example.com'
export WEKNORA_AUDIT_API_KEY='在本机临时输入'
```

先检查无需认证的健康端点（如果生产公开）：

```bash
curl --fail-with-body --max-time 15 \
  "${WEKNORA_AUDIT_BASE_URL}/health"
```

再检查系统能力和版本。若返回 401/403，只记录状态码并请运维提供版本，不扩大 Key 权限：

```bash
curl --fail-with-body --max-time 15 --max-redirs 0 \
  -H "X-API-Key: ${WEKNORA_AUDIT_API_KEY}" \
  "${WEKNORA_AUDIT_BASE_URL}/api/v1/system/capabilities"

curl --fail-with-body --max-time 15 --max-redirs 0 \
  -H "X-API-Key: ${WEKNORA_AUDIT_API_KEY}" \
  "${WEKNORA_AUDIT_BASE_URL}/api/v1/system/info"
```

最后读取资源列表：

```bash
curl --fail-with-body --max-time 15 --max-redirs 0 \
  -H "X-API-Key: ${WEKNORA_AUDIT_API_KEY}" \
  "${WEKNORA_AUDIT_BASE_URL}/api/v1/knowledge-bases"

curl --fail-with-body --max-time 15 --max-redirs 0 \
  -H "X-API-Key: ${WEKNORA_AUDIT_API_KEY}" \
  "${WEKNORA_AUDIT_BASE_URL}/api/v1/models"
```

完成后清除临时凭据：

```bash
unset WEKNORA_AUDIT_API_KEY
unset WEKNORA_AUDIT_BASE_URL
```

如果生产认证不是 `X-API-Key`，不要自行尝试其他 Header，先按生产文档修改审计模板。

## 4. 评测接口的非写入审计

R0 不创建评测任务。按以下顺序确认契约：

1. 从生产 API 文档确认创建、查询、取消和逐题结果接口是否存在。
2. 确认创建请求字段、dataset ID 规则、模型 ID 字段和响应 envelope。
3. 如已有可公开的历史测试任务，由运维提供脱敏后的查询响应。
4. 没有历史任务时，查询接口保持“未验证”，留到 R5 在费用确认后验证。

禁止为了探测能力调用未知的 `POST`、`PUT`、`PATCH` 或 `DELETE` 路径。

## 5. 响应脱敏规则

保留：

- HTTP 方法、相对路径和状态码。
- JSON 字段名、数据类型、数组/对象层级。
- 版本、edition、能力布尔值、任务状态枚举和指标名。
- 经过替换的 UUID、名称和时间。

替换或删除：

- `X-API-Key`、`Authorization`、Cookie、Token、Secret。
- `api_key`、模型服务凭据和完整请求 Header。
- 真实域名、IP、租户 ID、知识库名、模型显示名和人员信息。
- 文档内容、问题、答案、模型输出和业务指标值（除非确认可公开）。

建议替换值使用 `00000000-0000-4000-8000-000000000001`、`example-kb`、`https://weknora.example.invalid` 等明显的测试标识。

## 6. 交付物检查

- [ ] 生产版本/构建标识已记录。
- [ ] API 根路径与认证方式已确认。
- [ ] 空间选择规则已确认。
- [ ] 知识库列表契约已确认。
- [ ] 模型列表和类型枚举已确认。
- [ ] 评测创建/查询能力已由文档确认。
- [ ] 数据包格式、部署方式和 dataset ID 已确认。
- [ ] 所有 fixture 已脱敏且不含凭据。
- [ ] 兼容矩阵中的生产列已更新。
- [ ] 未向生产发起模型调用或付费评测。
