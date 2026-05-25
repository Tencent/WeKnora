# Built-in Storage Engine

为对象/文件存储引擎提供"系统级 fallback"机制：在 `config/builtin_storage_engine.yaml`
里声明一份系统默认 `StorageEngineConfig`（带 `${ENV}` 占位符），租户的
`storage_engine_config` 为空或某 provider 子配置缺失时自动回退到这份配置。

跟 `config/builtin_models.yaml` 完全同构。

## Env 占位符语法

- **`${NAME}`** — 用于 string 字段。env 未设/为空 → 留字面量 `${NAME}`，
  让下游 API 调用时暴露问题，避免被空字符串"静默吞掉"。
- **`${NAME:-default}`** — 用于非 string 字段（bool / int）。env 未设/为空 →
  替换为 `default`。**bool/int 字段必须用这种形式**，否则 YAML 解析时会因
  字面量 `${NAME}` 不是合法 bool/int 而加载失败。

  例：`use_ssl: ${S3_USE_SSL:-true}` —— 不设 env 走默认 `true`，
  设了 `S3_USE_SSL=false` 就用 `false`。

## 部署步骤

1. 把 `config/builtin_storage_engine.yaml.example` 复制为
   `config/builtin_storage_engine.yaml`，只保留你部署需要的 provider 块。
2. 容器编排里 mount 这份 yaml 进容器（docker-compose 已默认配置）：
   ```yaml
   volumes:
     - ./config/builtin_storage_engine.yaml:/app/config/builtin_storage_engine.yaml:ro
   ```
3. 在 `.env-staging`（或部署 secret 管理）填入对应的 env 变量
   （`OSS_ENDPOINT` / `OSS_ACCESS_KEY` / ...）。
4. 重启容器。启动日志会看到一行：
   ```
   Built-in storage engine loaded from /app/config/builtin_storage_engine.yaml (source: configDir): default="oss", providers=[oss]
   ```

## 路径覆盖

通过环境变量 `BUILTIN_STORAGE_ENGINE_CONFIG` 可以覆盖默认搜索路径
`<configDir>/builtin_storage_engine.yaml`。

## 解析顺序（重要）

`factory.go` 和 `handler/system.go` 都通过 `internal/types/storage_resolve.go`
里的统一 resolver 取配置。对每个 provider：

1. 租户 `tenants.storage_engine_config.<Provider>` 非 nil 且关键字段非空 → 用租户配置
2. 否则 → 用 `builtin_storage_engine.yaml` 中的对应块
3. **OBS 例外**：还有第 3 级 `OBS_*` env 兜底（deprecated，见下节）

"关键字段" = endpoint / access_key / secret_key / bucket_name（OBS 还包括 region）。
如果租户 PATCH 时不小心把 OSS 整个字段全清空了（前端发了 `OSS: {}` 空壳），resolver
会识别为"空壳"，自动跳到 builtin 兜底。

## OBS Deprecation Note

历史版本 OBS 通过 `OBS_*` 环境变量直接驱动 `factory.go`。本版本 OBS 改为统一
通过 resolver 取配置，env 变量降级为**第三级兜底**：

- 第 1 级：`tenants.storage_engine_config.OBS`
- 第 2 级：`builtin_storage_engine.yaml` 中的 `obs:` 块
- **第 3 级（deprecated）**：`OBS_ENDPOINT` / `OBS_REGION` / `OBS_ACCESS_KEY` /
  `OBS_SECRET_KEY` / `OBS_BUCKET_NAME` / `OBS_PATH_PREFIX` 环境变量。命中此路径时
  启动后首次上传会打印一行 deprecation warn log（进程生命周期内仅一次，由 `sync.Once` 保证）。

**计划在 vip-v0.7.x 版本完全移除 OBS env 兜底。** 升级前请按以下任一方式迁移：

1. **推荐**：在 `builtin_storage_engine.yaml` 里加 `obs:` 块，复用现有 `OBS_*`
   env 变量名作为占位符（无需改 secret 注入流程）：
   ```yaml
   storage_engine:
     obs:
       endpoint: ${OBS_ENDPOINT}
       region: ${OBS_REGION}
       access_key: ${OBS_ACCESS_KEY}
       secret_key: ${OBS_SECRET_KEY}
       bucket_name: ${OBS_BUCKET_NAME}
       path_prefix: ${OBS_PATH_PREFIX}
   ```
2. **次选**：在每个使用 OBS 的租户的 `storage_engine_config` 里手动填入。

## 排查：当前生效的存储配置是哪个？

如果观察到"租户没配但文件上传成功"或"配置改了没生效"，按以下步骤排查：

### 1. 看启动日志确认 YAML 是否加载

```bash
docker logs <container> 2>&1 | grep "Built-in storage engine"
```

应当看到 `Built-in storage engine loaded from /app/config/builtin_storage_engine.yaml ...`。
如果看到 `not present at ...; skipping`，说明 YAML 文件没挂进容器。

### 2. 看租户当前配置

```sql
SELECT id, name, storage_engine_config FROM tenants WHERE id = <tenant_id>;
```

`storage_engine_config` 为 NULL 或 `{}` 表示走 builtin fallback。

### 3. 看运行时生效的 provider

调用 `GET /system/storage-engine-status`，响应里 `engines[].Available=true`
表示该 provider 可用（可能来自 tenant 或 builtin）。本版本起 `Available`
**包含 builtin fallback 结果**。

### 4. 看 OBS deprecation warn 是否触发

```bash
docker logs <container> 2>&1 | grep "OBS_\* env-var configuration is deprecated"
```

每个进程生命周期内最多打印一次。看到此 warn 说明你正在用 deprecated env 路径，
应尽快迁移到 YAML。

## 测试

```bash
go test ./internal/types/ ./internal/application/service/file/ ./internal/handler/
```

关键测试文件：
- `internal/types/env_interpolation_test.go`
- `internal/types/storage_resolve_test.go`
- `internal/types/builtin_storage_engine_config_test.go`
- `internal/application/service/file/factory_builtin_test.go`
- `internal/handler/system_storage_resolve_test.go`
