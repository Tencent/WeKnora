# 文档已迁至 website-docs

产品、部署、API 和开发文档统一维护在 [website-docs](../website-docs/README.md)。已迁移、重复和过时的旧手写文档已删除；历史内容可从 Git 历史查看。

- [常见问题与升级排障](../website-docs/01-getting-started/05-troubleshooting.md)
- [API 参考](../website-docs/04-api/01-api-overview.md)
- [开发指南](../website-docs/06-development/01-dev-guide.md)
- [迁移对应表与剩余依赖](../website-docs/MIGRATION.md)

本目录仅保留以下工程资源及本入口说明：

- `docs.go`、`swagger.json`、`swagger.yaml` 与契约测试：参与后端编译和测试，生成物仍由 `make docs` 更新。
- `LITE.md`：Lite 发布包复制为离线 README。
- `images/`、`assets/`：README、Helm 等仍使用的图片资源。
- `poc/docker-sandbox/`：独立 Go 实验模块，保留源码和运行说明，不作为当前产品指南。

这些资源尚有编译、发布或历史实验用途，不能直接整目录删除。新的产品说明不要再放入此处。
