# 备份与恢复 API

[返回目录](./README.md)

全实例备份包含所有空间的数据与加密凭据,仅 **SystemAdmin** (JWT) 可调用。API Key 默认拒绝。

| 方法   | 路径                         | 描述             |
| ------ | ---------------------------- | ---------------- |
| GET    | `/backups/export`            | 即时导出并下载   |
| POST   | `/backups`                   | 创建服务器快照   |
| GET    | `/backups`                   | 列出快照         |
| GET    | `/backups/{id}/download`     | 下载快照         |
| DELETE | `/backups/{id}`              | 删除快照         |
| POST   | `/backups/restore`           | 从快照或文件恢复 |

详细语义、归档布局与跨实例迁移见 [备份与恢复.md](../备份与恢复.md)。

**权限**: SystemAdmin。`POST /backups/restore` 必须带 `confirm=true`。

**上传上限**: `MAX_BACKUP_ARCHIVE_SIZE_MB`(默认 4096)。
