# API 参考：引导式学习

引导式学习 API 管理当前调用者在自有 Wiki 知识库中的学习画像、推荐与来源测验。所有路径使用 `/api/v1` 前缀。

接口只接受当前空间内已登录的 Web 用户 Bearer token，不接受 API Key、IM 用户、Embed 访客或共享 Agent 身份。服务端从凭证确定租户和用户，不接受 `subject_id`。学习功能默认关闭，首次使用前调用 `PUT /learning/settings` 开启。

## 设置与概览

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/learning/settings` | 返回 `{enabled,algorithm_version}` |
| PUT | `/learning/settings` | `{"enabled":true}`；返回更新后的设置 |
| GET | `/learning/overview?knowledge_base_id=:id` | 返回节点总数和 unseen、learning、mastered、review_due 计数 |
| GET | `/learning/nodes/:id` | 返回 Wiki 页面、熟悉度、掌握度与来源状态 |
| POST | `/learning/nodes/:id/view` | 记录阅读信号；不更新掌握度 |
| GET | `/learning/recommendations?knowledge_base_id=:id&limit=5` | 返回 1–20 个排序后的学习主题 |
| POST | `/learning/overlay` | 批量返回知识图谱节点状态，最多 2000 个 slug |

```bash
curl -X PUT "$BASE/api/v1/learning/settings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true}'

curl "$BASE/api/v1/learning/recommendations?knowledge_base_id=$KB_ID&limit=5" \
  -H "Authorization: Bearer $TOKEN"

curl -X POST "$BASE/api/v1/learning/overlay" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"knowledge_base_id":"'"$KB_ID"'","slugs":["concept/rag","concept/bkt"]}'
```

阅读和长期记忆中的文档偏好只影响 `familiar` 与推荐排序。`mastery` 仅由已校验测验答案更新。

## 来源测验

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| POST | `/learning/question-sets` | `{"page_id":"..."}`；返回 pending、running、ready、failed 或 stale 测验 |
| GET | `/learning/question-sets/:id` | 获取当前用户拥有的测验；未作答题目不返回答案 |
| POST | `/learning/attempts` | `{"question_id":"...","option_id":"...","attempt_id":"..."}`；返回判分、证据与最新掌握度 |

题目异步生成。客户端收到 pending 或 running 后可轮询 GET；同一页面同时只生成一份活动测验。`attempt_id` 是客户端生成的幂等键，同一个键不能改投其他问题或选项。同一来源版本下，相同题干只计入一次掌握度；来源变化后重新评估。

```bash
QUIZ_ID=$(
  curl -sS -X POST "$BASE/api/v1/learning/question-sets" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d '{"page_id":"'"$PAGE_ID"'"}' | jq -r '.data.id'
)

curl "$BASE/api/v1/learning/question-sets/$QUIZ_ID" \
  -H "Authorization: Bearer $TOKEN"

curl -X POST "$BASE/api/v1/learning/attempts" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"question_id":"'"$QUESTION_ID"'","option_id":"0","attempt_id":"'"$(uuidgen)"'"}'
```

## 导出与删除

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/learning/export` | 导出当前用户的全部学习数据 |
| GET | `/learning/export?knowledge_base_id=:id` | 只导出指定自有 Wiki 知识库的数据 |
| DELETE | `/learning/profile` | 删除当前用户的全部学习数据并关闭个人学习 |
| DELETE | `/learning/profile?knowledge_base_id=:id` | 删除指定知识库的数据 |

删除会提升画像 epoch，使已经排队或正在运行的旧测验任务失效，旧 worker 不能恢复已删除的数据。

## 错误

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `learning_invalid` | 参数、JSON 或标识无效 |
| 403 | `learning_forbidden` | 调用身份不支持或知识库不属于当前空间 |
| 403 | `learning_disabled` | 个人学习尚未开启 |
| 404 | `learning_not_found` | 页面、测验或来源不存在 |
| 409 | `learning_stale` | 来源已变化，需要生成新测验 |
| 409 | `learning_not_ready` | 测验仍在生成 |
| 409 | `learning_conflict` | 题目已作答或幂等键内容冲突 |
| 422 | `learning_evidence` | 当前来源不足以生成可验证题目 |
| 429 | `learning_busy` | 生成队列或数据库暂时繁忙 |
| 500 | `learning_unavailable` | 未分类的服务端故障 |

所有响应带 `Cache-Control: no-store`。实现入口：`internal/handler/learning.go`、`internal/router/routes_learning.go`。
