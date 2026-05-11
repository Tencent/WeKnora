# 腾讯云轻量应用服务器 / CVM 镜像制作指南

> **前置阅读**：通用脚本与流程见 [`scripts/cloud-image/README.md`](../../scripts/cloud-image/README.md)。本文只补充腾讯云平台专属的操作步骤。

## 实例规格建议

| 项 | 建议值 |
|---|---|
| 实例类型 | 轻量应用服务器（Lighthouse） / 云服务器 CVM 标准型 |
| CPU / 内存 | 至少 4 核 8G（推荐 4 核 16G，跑全功能 RAG + Agent） |
| 系统盘 | 至少 80G SSD |
| 镜像 | Ubuntu Server 22.04 LTS / TencentOS Server 3.1 |
| 地域 | 选你后续主要用户所在地域（同地域才能跨账号共享） |

## 完整流程

1. 控制台买一台符合上述规格的实例
2. SSH 进去，按 [scripts/cloud-image/README.md](../../scripts/cloud-image/README.md) 执行 `prepare.sh`
3. 浏览器访问 `http://<公网IP>` 验证功能
4. 执行 `cleanup.sh`（会自动 `poweroff`）
5. 控制台「**制作镜像**」（见下）
6. 用新镜像创建一台测试实例，验证 firstboot 工作正常
7. 「**共享镜像**」给其他账号 / 申请「**镜像市场**」上架（见下）

## 制作镜像

**轻量应用服务器**：

1. 控制台 → 轻量应用服务器 → 选中已关机的实例
2. 「更多」→「**制作镜像**」
3. 镜像名建议带版本号：`weknora-v0.5.0-ubuntu2204`
4. 等待 5–30 分钟（取决于系统盘大小）

**云服务器 CVM**：

1. 控制台 → 云服务器 → 选中已关机的实例
2. 「更多」→「制作镜像」→ 选「整机镜像」
3. 同样建议带版本号

> 同一账号下，自定义镜像有数量配额（默认 20 个），可在控制台查看。

## 验证镜像

强烈建议用新镜像创建一台测试实例，至少验证：

- [ ] 能 SSH 进去（用控制台的默认密码 / 你导入的 key）
- [ ] `systemctl status weknora-firstboot` 显示已成功执行（或已 disable + 文件被删）
- [ ] `cat /root/weknora-credentials.txt` 里有随机密码
- [ ] 浏览器打开公网 IP 能访问 WeKnora，能注册管理员
- [ ] `docker compose -f /opt/WeKnora/docker-compose.yml ps` 全部 healthy
- [ ] `cat /opt/WeKnora/.cloud-image-meta` 显示正确的版本

## 共享给其他用户

按「覆盖范围」递增有 3 种方式：

### 方式 A：跨账号共享（私下分享）

控制台 → 自定义镜像 → 选中镜像 → 「**共享**」→ 输入对方腾讯云账号 ID（UIN）。

- 对方在自己的「共享镜像」列表能看到，可直接基于它创建实例
- **限制**：必须同地域；对方账号必须已开通对应产品（Lighthouse / CVM）
- 适合小范围、合作伙伴、内测用户

### 方式 B：跨地域使用

Lighthouse 的镜像可以「共享给 CVM」，转成 CVM 自定义镜像后即可：

- 跨地域复制
- 导出为 `qcow2` 文件下载到本地（更通用，可用于 KVM / 其他云）

### 方式 C：通过腾讯云市场上架镜像商品（公开一键部署）

这是真正「任何用户都能在云市场搜到并一键购买/部署」的形态。流程比较重，适合长期运营。

参考文档（请以官方为准）：

- [云市场 - 镜像服务上架流程](https://cloud.tencent.com/document/product/306/3019)
- [云市场 - 镜像商品制作说明](https://cloud.tencent.com/document/product/306/30128)
- [Lighthouse - 应用镜像使用说明](https://cloud.tencent.com/document/product/1207/72665)
- [Lighthouse - 管理镜像（操作指南）](https://cloud.tencent.com/document/product/1207/63263)

大致步骤：

1. 登录 [腾讯云市场服务商管理控制台](https://console.cloud.tencent.com/serviceprovider)，注册成为服务商
2. 「商品管理 → 商品列表 → 新建商品」，接入类型选「**镜像**」
3. 选择已制作好的自定义镜像（即上面方式 A/B 制作的那份）
4. **完成主机安全（专业版）检测**——这是镜像上架的硬性前置条件，需要自付 CVM + 主机安全专业版费用，建议用按量付费跑完即释放
5. 填写商品名称、版本、亮点、详情、使用指南（必传 PDF/Word/PPT/ZIP/RAR，≤2MB）、售后说明
6. 选择售卖方式（按量计费 / 按周期计费），按量计费目前仅支持 0 元规格
7. 提交审核（云市场运营人员审核约 7 个工作日）
8. 审核通过后，用户在云市场或购买 CVM 时即可选到你的镜像

> WeKnora 是腾讯系开源项目（`Tencent/WeKnora`），如果想推动官方上架，建议在 [WeKnora GitHub Issues](https://github.com/Tencent/WeKnora/issues) 联系维护团队，而不是个人单独申请。

## 注意事项

- 腾讯云的 cloud-init 兼容性良好，`cleanup.sh` 里的 `cloud-init clean` 能正常工作
- Lighthouse 的镜像默认大小限制为系统盘大小，请提前预估
- 若使用域名 + HTTPS，建议在 firstboot 之外通过 [acme.sh](https://acme.sh) / certbot 单独配置，不要把证书烤进镜像
