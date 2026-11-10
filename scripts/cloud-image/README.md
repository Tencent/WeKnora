# 云镜像维护脚本

脚本边界、制镜像注意事项与首启排障统一维护在 [website-docs 开发指南](../../website-docs/06-development/01-dev-guide.md#cloud-image-scripts)。普通安装请使用[安装部署](../../website-docs/01-getting-started/02-installation.md)。

本目录保留 `prepare.sh`、`cleanup.sh`、`firstboot.sh` 及其 systemd 单元。`cleanup.sh` 会清除制作机的数据、密钥、SSH 授权与缓存并关机，只能用于专用制作机；参数和实际行为以脚本为准。
