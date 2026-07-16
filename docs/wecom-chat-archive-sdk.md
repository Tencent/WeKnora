# 企业微信会话存档 SDK 接入说明

## SDK 版本

- SDK 目录：`sdk_x86_v3_20250205/C_sdk`
- SDK 版本：`20250205`
- 头文件：`WeWorkFinanceSdk_C.h`
- 动态库：`libWeWorkFinanceSdk_C.so`
- MVP 生产目标：Linux amd64

## 本地构建

非 Linux 或 `CGO_ENABLED=0` 构建会使用 unavailable client，并返回 `wecom chat archive SDK client is not configured in this build`。

Linux amd64 构建需要：

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build ./cmd/server
```

## Docker 构建

```bash
docker build -f docker/Dockerfile.app -t weknora-app-wecom-sdk .
```

## 运行要求

容器内必须能加载：

```text
/usr/local/lib/libWeWorkFinanceSdk_C.so
```

## 故障排查

- `SDK client is not configured`: 当前构建不是 Linux amd64 CGO 构建，或 SDK 文件未进入构建上下文。
- `Init ret != 0`: 检查 `corp_id`、`secret` 和企业微信会话存档授权。
- `GetChatData ret != 0`: 检查网络、IP 白名单、代理配置和 SDK 返回码。
- `DecryptData ret != 0`: 检查私钥版本、私钥内容和 `encrypt_random_key`。

日志不得输出 `secret`、`private_key`、解密密钥、加密消息体或解密正文。
