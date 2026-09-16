# 离线镜像存放目录

由 `../scripts/pack-images.sh` 生成，勿手工改镜像名。

预期文件（版本以 `.env` 中 `WEKNORA_VERSION` 为准）：

- `registry.cn-beijing.aliyuncs.com_gov-claw_tree-rag-ui_v0.8.23.tar`
- `registry.cn-beijing.aliyuncs.com_gov-claw_tree-rag-app_v0.8.23.tar`
- `registry.cn-beijing.aliyuncs.com_gov-claw_tree-rag-docreader_v0.8.23.tar`
- `paradedb_paradedb_v0.22.2-pg17.tar`
- `redis_7.0-alpine.tar`
- `manifest-v0.8.23.txt`
- `treerag-images-v0.8.23-linux-amd64.tar.gz`（可选汇总包）

内网机执行：`../scripts/load-images.sh`
