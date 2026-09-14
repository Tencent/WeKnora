# FAQ 条目启用状态筛选设计

## 背景

FAQ 条目管理接口支持分页、标签、关键词和排序，但不能按 `is_enabled` 筛选。前端在分页结果返回后过滤会导致当前页数据和总数不准确。

## 接口

`GET /api/v1/knowledge-bases/{id}/faq/entries` 增加可选查询参数 `is_enabled`：

- 不传：返回全部条目，保持现有行为。
- `true`：只返回已启用条目。
- `false`：只返回未启用条目。
- 其他值：返回 `400 Bad Request`。

## 实现

Handler 区分参数缺失与布尔值，Service 将 `*bool` 传给已有的 Repository 查询。Repository 已在统计总数和分页数据查询中应用该条件，不需要数据库变更。

前端 API 参数类型补充 `is_enabled?: boolean`，本次不增加界面控件。Go 客户端保留现有方法签名，避免破坏调用方。

## 测试

补充查询参数解析测试，覆盖未传、`true`、`false` 和非法值；执行相关 Go 测试、前端类型检查及差异检查。
