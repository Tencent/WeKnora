# Seafile 接入

`seafile` 将一个 Seafile 资料库中勾选的目录和文件同步到知识库，支持定时、增量、断点续传和源端删除对账。兼容基线为 Seafile 社区版 Server 10.0.1（api2 接口）。通用同步流程见[数据源同步](10-datasource.md)。

## 账号与 API Token

使用一个能登录 Seafile、对目标资料库至少有只读权限的账号；同步范围以该账号的权限为准。凭据是该账号的用户 API Token，连接器只保存和使用 Token，不处理账号密码、SSO 登录或资料库专用令牌。

Seafile 10.0.1 个人设置页的「Web API Auth Token」默认不显示，需要管理员在 `seahub_settings.py` 中设置 `ENABLE_GET_AUTH_TOKEN_BY_SESSION = True` 并重启 Seahub。也可以用账号密码调用 `POST /api2/auth-token/` 获取；启用双因素认证时，按响应中的 `X-Seafile-OTP` 要求补交验证码。

WeKnora 内的连接测试调用 `/api2/auth/ping/` 验证 Token；资料库和目录是否可见要到加载资源树时才会验证。

## 在知识库中配置

1. 知识库设置 → 数据源 → 新建，选择「Seafile」。
2. 填写 Seafile 地址和 API Token，执行连接测试。地址可以带部署路径前缀，如 `https://example.com/seafile`。
3. 在资源树中勾选资料库根目录、任意目录或单个文件。顶层只列出该账号可访问的未加密资料库，目录逐级展开；一个数据源只能选同一个资料库，勾选第二个资料库会被拒绝。
4. 选择全量/增量、同步计划、冲突策略与同步删除；保存后先手动同步少量文件，检查日志和解析结果。

勾选目录表示递归同步，之后新增的文件和子目录自动纳入；勾选单个文件只同步该文件；子项被父目录覆盖时只保留父目录。选择器只列出 WeKnora 支持导入的扩展名，`.zip` 等不支持的文件不出现，也不会同步。

## 网络与 SSRF

| 请求 | 目标 | 携带 Token |
| --- | --- | --- |
| API（`/api2/…`） | 配置的 Seafile 地址 | 是，`Authorization: Token` |
| 文件下载 | Seafile 返回的 fileserver 链接，通常是同域名下的 `/seafhttp/…`，也可能是独立域名 | 否 |

两类地址都经过 SSRF 校验。Seafile 部署在私网或使用独立 fileserver 域名时，把对应主机加入 `.env` 的 `SSRF_WHITELIST`，否则保存凭据时报「base_url SSRF validation failed」，或同步日志出现 `seafile_ssrf_blocked`。

## 目录、格式与大小

同步进来的文件按 `<资料库名>/<资料库内路径>` 落到知识库目录，来源标签显示为「Seafile」。资料库名在首次同步时写入游标并固定，之后在 Seafile 改名不会改变知识库目录，也不会触发重新下载。目录名和文件名逐段清理非法字符。

格式范围与知识库上传一致（PDF、Office、Markdown、HTML、常见图片、音频等），以 `internal/utils/import_filetypes.go` 为准。图片需要知识库配置 VLM，音频需要配置 ASR；未配置时条目在同步日志中记为入库失败，不影响其他文件。

单文件上限沿用 `MAX_FILE_SIZE_MB`（默认 50 MiB）：目录列表中超限的文件直接记为 `seafile_file_too_large` 且不下载，下载中实际字节数超限同样拒绝，不会截断入库。调大该值时须同步调整 DocReader 的 `DOCREADER_GRPC_MAX_FILE_SIZE_MB` 并重启 app、docreader、frontend，否则文件能下载但解析阶段被 gRPC 拒绝。

## 同步行为

每个文件以「对象 ID + 修改时间 + 大小」组成指纹保存在游标中，三者都相同则跳过下载；指纹变化时重新下载并覆盖旧条目（先删后建，解析期间该文档短暂不可用）。无变化的一轮只做目录扫描，请求数等于目录数。

| 源端操作 | 行为 |
| --- | --- |
| 新增文件或子目录 | 下次同步纳入 |
| 修改内容 | 重新下载并更新 |
| 删除文件 | 开启「同步删除」时删除对应知识条目，否则仅从游标移除 |
| 重命名、移动或移出范围 | 新路径按新文件导入，旧路径按删除处理；不保留知识条目 ID |
| 勾选的目录或文件被删除、改名 | 本轮报错停止，不产生任何删除；需重新选择范围 |
| 资料库改名 | 无变化，目录名沿用首次同步时的名字 |

失败处理：

| 情况 | 行为 |
| --- | --- |
| 单文件 403 / 404 / 5xx、网络错误、超限、空文件、下载中变化 | 记为失败条目，其余文件继续；该路径留在游标中，下次同步重试 |
| 目录列表 403 / 404 / 5xx 或响应畸形 | 保存已完成的进度后停止，本轮不产生任何删除 |
| 429 / 502 / 503 / 504 或临时网络错误 | 同一请求最多 3 次尝试，退避 1s、2s；`Retry-After` 不超过 60s 时取其值，超过则停止本轮 |
| Token 失效（401 / 403） | 凭据错误，数据源进入错误状态 |

每 50 次游标变更、发出删除前和结束时保存检查点；任务超时或中断后，下次从最后一个检查点继续，已保存的文件不重复下载。首次同步数万文件的大资料库可能跨多次任务执行才完成。强制全量同步会重新下载全部文件，同时保留旧游标作为删除对账基线。

「连接器获取重试」与「入库解析恢复」是两回事：前者指下载失败的文件在下次同步重新下载；后者指文件已提交入库但解析、索引失败，由知识库侧的重试机制处理，同步日志中显示为入库失败。

同步日志错误码：

| 错误码 | 含义 |
| --- | --- |
| `seafile_permission_denied` | Token 所属账号无权访问该文件或目录 |
| `seafile_not_found` | 文件在扫描后消失；下次同步重试，已从资料库消失则按「同步删除」开关对账 |
| `seafile_file_too_large` | 超过 `MAX_FILE_SIZE_MB` |
| `seafile_empty_file` | 文件为空，跳过 |
| `seafile_source_changed` | 下载过程中文件被修改，下次同步重试 |
| `seafile_invalid_response` | Seafile 返回无法解析的响应，多为版本或反向代理问题 |
| `seafile_ssrf_blocked` | 下载地址被 SSRF 策略拦截 |
| `seafile_fetch_failed` | 其他获取失败，下次同步重试 |

## 注意事项

- 已同步的数据源不能改绑其他资料库：改选后下一次同步会以「该数据源已同步资料库 <旧名>」失败且游标不变，需新建数据源。保存前先取消已选项即可换库。
- 删除对账不保证补删：目录扫描失败的那一轮不产生删除；「同步删除」关闭期间发生的删除，重新开启后不会追溯。
- 删除或重建数据源不会清理已导入的知识条目，遗留内容需在知识库中单独清理。
- 加密资料库不出现在选择器中，也不支持同步；SeaDoc 在线文档、自定义属性和双向同步不在支持范围内。

## 常见问题

| 现象 | 处理 |
| --- | --- |
| 保存凭据时报 SSRF 校验失败 | Seafile 地址是私网或被拦截端口，加入 `SSRF_WHITELIST` |
| 「Seafile library … not found」 | Token 所属账号看不到该资料库，或资料库已删除 |
| 「encrypted Seafile libraries are not supported」 | 选中的资料库是加密库 |
| 同步日志大量 `seafile_ssrf_blocked` | fileserver 域名与 API 域名不同且未加白 |
| 同步日志 `seafile_file_too_large` | 调大 `MAX_FILE_SIZE_MB` 与 `DOCREADER_GRPC_MAX_FILE_SIZE_MB` 后重启 |
| 文件已下载但知识条目解析失败 | 属入库解析问题，检查 VLM / ASR 配置和 DocReader 日志 |
| 「this data source already synced Seafile library …」 | 改绑了资料库，恢复原资料库或新建数据源 |

实现参考：`internal/datasource/connector/seafile/` 和 `internal/application/service/datasource_service.go`。
