# search 插件目录

宿主扫描的外部插件目录之一，对应扩展类型 `search`（网络搜索提供方）。
容器内路径由 `WEKNORA_PLUGIN_DIR_SEARCH` 指定，`docker-compose.yml` 默认 `/app/plugins/search`。

## 放什么

每个插件是一个**独立子目录**，内含：

- `plugin.yaml`：声明 `id`、`version`、`extension_type: search`、`permissions`、`config_schema`；
- 可执行文件（**Linux/amd64**，且有可执行权限），或 `entrypoint: docker://<image>@sha256:<digest>` 指向的 OCI 镜像。

示例插件及构建方式见独立仓库
[WeKnora-plugin · search](https://github.com/wei-yan1/WeKnora-plugin/tree/main/search)。

## 这个说明文件为什么必须留着

没有安装此类插件时，本目录只保留这份说明——**目录存在但没有插件，是合法的正常运行状态**。

但**目录不存在不是**：`internal/plugin/discovery.go` 对不存在的扫描根目录直接返回错误，
该错误经 `container.go` 的 `must(container.Invoke(loadExternalPlugins))` 变成 **panic**，
表现为 app 容器启动即失败（配置了重启策略时会反复崩溃）。

Git 无法跟踪空目录，所以需要这份占位文件把目录带进仓库。**请勿删除它。**

> `plugins/` 已被 `.dockerignore` 排除，只在运行期由 `docker-compose.yml` 挂载进容器，不会打进 app 镜像。
