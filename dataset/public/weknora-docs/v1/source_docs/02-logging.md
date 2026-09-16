# 日志配置

WeKnora 的日志由 `internal/logger` 统一管理，所有行为通过环境变量驱动，无需改代码。

## 环境变量

| 变量 | 是否必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `LOG_LEVEL` | 否 | `debug` | 日志级别，取值 `debug` / `info` / `warn`(`warning`) / `error` / `fatal`，无效值回退到 `debug` |
| `LOG_PATH`  | 否 | 空（仅打到 stdout；macOS `.app` 打包模式下落到 `~/Library/Logs/<AppName>/<AppName>.log`） | 落盘路径，启用后同时写 stdout 与该文件，文件按 lumberjack 滚动（单文件 50MB / 3 份 / 28 天 / 压缩归档） |
| `LOG_FORMAT` | 否 | 空（沿用内置默认格式） | 自定义日志输出模板，支持下列占位符 |

> 终端环境下 ANSI 颜色自动启用；非 TTY（容器日志采集、`docker logs` 重定向等）会自动关闭颜色，避免日志聚合/检索异常。

## `LOG_FORMAT` 模板

`LOG_FORMAT` 为空时使用内置默认格式（与历史行为一致）：

```
INFO [2026-05-21 10:20:30.123] [req-abc k1=v1] file.go:42[fn] | message body
```

配置 `LOG_FORMAT` 后启用模板模式，支持如下占位符：

| 占位符 | 含义 |
| --- | --- |
| `%d`       | 时间戳（`2006-01-02 15:04:05.000`） |
| `%level`   | 日志级别（`DEBUG` / `INFO` / `WARNING` / `ERROR` / `FATAL`），开启颜色时仅此占位符被染色 |
| `%thread`  | 当前 goroutine ID。**未引用该占位符时不会调用 `runtime.Stack`，无额外开销** |
| `%logger`  | caller 信息（`file.go:line[func]`），过长时从末尾截取后 50 字符 |
| `%traceId` | 请求 ID（即上下文中的 `request_id`） |
| `%msg`     | 消息正文 + 剩余结构化字段（`key=value`，按 key 升序拼接） |

示例：

```bash
export LOG_FORMAT='[%d] %level %thread %logger %traceId | %msg'
```

输出（终端开启颜色时仅 `%level` 段着色）：

```
[2026-05-21 10:20:30.123] INFO 17 service.go:88[Handle] req-abc | hello extra=ok
```

### 实现细节与注意事项

- 占位符替换使用 `strings.NewReplacer` 单趟扫描，前一个占位符的值即使包含其它占位符字面串（如 `request_id=%msg`）也不会被二次替换。
- 级别染色在 `%level` 替换阶段直接注入 ANSI 颜色，**不会**误染消息正文中字面出现的 `INFO` / `ERROR` 等字符串。
- 结构化字段（`logger.WithField` / `WithFields`）会被拼接到 `%msg` 之后，模板模式下暂不支持把任意字段抽出为独立占位符 — 若需 K/V 结构化检索，请直接关闭 `LOG_FORMAT` 使用默认格式。
- `LOG_LEVEL` 与 `LOG_PATH` 的设置会在 `main` 加载 `.env` 后由 `logger.ConfigureFromEnv()` 重新生效。
