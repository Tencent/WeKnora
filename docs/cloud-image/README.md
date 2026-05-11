# WeKnora 云镜像打包指南

把 WeKnora 打包成可分发的云镜像（AMI / 自定义镜像 / Snapshot），用户基于镜像创建实例后开机即用、自动随机化密钥、零私密泄漏。

## 通用工具

云无关的脚本和详细说明：[`scripts/cloud-image/README.md`](../../scripts/cloud-image/README.md)

包含 `prepare.sh` / `cleanup.sh` / `firstboot.sh` 三个脚本和两个 systemd 单元，已在多种发行版（Ubuntu / Debian / CentOS / TencentOS）上验证。

## 各平台具体操作

| 平台 | 文档 | 状态 |
|---|---|---|
| 腾讯云轻量应用服务器 / CVM | [tencent-lighthouse.md](./tencent-lighthouse.md) | ✅ |
| AWS EC2 (AMI) | _欢迎贡献_ | ⏳ |
| 阿里云 ECS | _欢迎贡献_ | ⏳ |
| 火山引擎 ECS | _欢迎贡献_ | ⏳ |
| 华为云 ECS | _欢迎贡献_ | ⏳ |
| 本地 KVM / Proxmox | _欢迎贡献_ | ⏳ |

> 各平台文档结构尽量保持一致：实例规格建议 → 制作镜像操作 → 共享/公开方式 → 该平台独有的注意事项。
