# 微信登录（Web 与移动应用）

WeKnora 提供微信身份认证入口。Web 用户点击登录页的「微信登录」，在弹出的微信二维码中扫码，并在手机上确认登录；iOS / Android 使用微信官方 OpenSDK 获得授权码后调用后端接口。两种入口最终使用同一套 WeKnora 用户、空间、JWT 和权限体系，无需先注册邮箱密码账号。

这与「IM 渠道」中的微信机器人二维码绑定是独立功能。`/wechat/qrcode` 返回的是机器人连接凭据，不能用来登录 WeKnora。网站扫码授权也不同于微信公众号内的网页授权，本功能未接入公众号 `snsapi_base` 流程。

## 申请 AppID / AppSecret 与主体资格

以下按 2026-10-14 可访问的官方资料整理。注册开放平台账号、创建应用、通过主体认证、获得微信登录权限是不同步骤；拿到 AppID 不代表已取得正式登录权限。资格、收费、额度及材料以当前账号后台和审核结果为准。

### 个人开发者能否申请

| 场景 | 资格与准备 |
| --- | --- |
| 注册微信开放平台账号 | 官方注册指南包含个人主体入口。个人注册成功不代表可以开通所有应用能力。 |
| Web 网站微信登录 | 正式接入按企业、个体工商户等可完成开发者资质认证的主体准备；另需网站应用审核和微信登录权限。不要把个人公众号或小程序的 AppID 用于网站扫码登录。 |
| iOS 微信登录 | 使用开放平台的移动应用，另行通过应用与登录能力审核。正式运营按已认证主体准备；未认证或未上架应用可能只有受限能力，不能据此承诺个人账号可正式上线。 |
| iOS 的 Apple 开发者资格 | Apple 支持个人加入 Apple Developer Program，发布 App / 使用 TestFlight 无需先成为公司。它不会替代微信的主体认证。 |

个人资格有一个需要在后台确认的边界：开放平台公开页面资源中存在未认证账号每日 20 次登录的提示，也存在个人主体创建新应用受限的提示；公开开发指南没有完整说明这些提示各自适用的账号、入口与历史条件。因此本指南不把受限测试额度作为新注册个人账号一定能申请的保证。如果只有个人身份，先在自己的账号中确认能否创建目标应用、申请微信登录及实际额度；正式产品计划应先解决主体资格。持有营业执照的个体工商户与纯个人身份不同，应按对应主体类型提交真实材料。

参考：[开放平台注册指南](https://developers.weixin.qq.com/doc/oplatform/Third-party_Platforms/2.0/operation/open/create.html)、[开放平台注册入口](https://open.weixin.qq.com/wxaopen/regist/index)、[Apple Developer Program](https://developer.apple.com/programs/)。Apple 公布的会员费用为每年 99 美元，实际本地币种价格以注册时为准；微信认证费用、有效期及续审要求在申请页面确认，不把旧教程中的价格或审核时长当作承诺。

### 申请步骤与材料

1. 在 [微信开放平台](https://open.weixin.qq.com/) 注册账号、验证邮箱并登记主体。这里使用开放平台，不能用公众平台账号的公众号 / 小程序凭据替代。
2. 在「账号管理 / 账号中心 → 开发者资质认证」提交认证。按主体类型准备营业执照或组织登记证明、管理员与认证联系人资料，以及页面要求的认证公函、授权和验证材料；不同主体及地区的要求可能不同。
3. 在「管理中心」分别创建一个**网站应用**和一个**移动应用**。为了后续通过 UnionID 识别同一用户，建议放在同一个开放平台账号下。它们使用不同的 AppID / AppSecret；同一个移动产品的 iOS、Android 则按官方规范共用移动应用 AppID。
4. 完成应用审核，并在应用详情 / 能力专区确认「微信登录」已获批、可用。在各应用详情获取 AppID，按管理员验证流程生成或重置 AppSecret，保存到后端的安全配置中。密钥遗失时按后台流程重置，并同步更新所有后端实例。

Web 应准备可访问的网站、应用名称和图标、网站介绍及对应主体信息，并按审核页面提交《微信开放平台网站信息登记表》、备案等材料。官方审核规范要求登记表、备案和主办单位信息对应；主体不一致时按要求提供授权证明。应用官网不能只有无法查看产品信息的登录页，应展示功能说明、用户协议、联系方式等内容。详见[应用审核规范](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/operation.html)。

iOS 应准备应用官网、名称和图标、真实运行截图或交互流程图、Bundle ID，以及用于 OpenSDK 回跳的 Universal Links。**未上架也可提交**，选择未上架状态并提供相应流程材料；已经上架则提交有效 App Store 下载链接。当前基本资料指南还要求选择已上架时填写 App 备案号。需要的行业资质、著作权或主体授权材料按应用类目及审核要求补充，不将软件著作权证书列为所有普通 App 的统一前置条件。详见[创建移动应用](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/guideline/create.html)和[基本资料填写说明](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/guideline/basic_info.html)。

### 接入与上线注意事项

- **两种回调不能混用**：网站应用的授权回调域名例如 `weknora.example.com`；WeKnora 配置中的完整回调地址为 `https://weknora.example.com/api/v1/auth/wechat/callback`。iOS Universal Links 用于微信回到原生 App，是另一项配置，应按 OpenSDK 和 Apple 的关联域配置完成验证。
- **未上架额度**：移动登录官方 FAQ 当前说明，已认证主体的未上架应用为每日 100 次登录，超限可能报 `10060`。各平台上架状态需分别提交并审核，不能靠修改 WeKnora 配置解除额度限制。详见[移动登录指南 FAQ](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/WeChat_Login/Development_Guide.html)。
- **iOS 登录审核**：如果用微信建立或验证主要账号，需检查 Apple 审核指南 4.8，提供符合隐私条件的同等登录选项，通常选择 Sign in with Apple；存在明确豁免，不能概括成所有 App 一律必须接入 Apple 登录。普通邮箱密码入口并不自动满足该条的全部要求。微信官方还建议未安装微信时隐藏 iOS 微信登录入口并保留其他方式。当前 WeKnora 微信登录实现不包含原生 iOS 客户端或 Apple 登录。
- **密钥只在服务端**：AppID 可以进入客户端，AppSecret 不可打包进 iOS、前端代码或提交到 Git。生成或重置时妥善保存；配置界面不应回显密钥。Web 和移动应用需要分别配置各自密钥。

Apple 审核依据：[4.8 登录服务](https://developer.apple.com/cn/app-store/review/guidelines/#login-services)。

## 启用

需要在微信开放平台分别创建并审核通过网站应用、移动应用，取得各自的 AppID 和 AppSecret，并开通微信登录能力。Web 需要设置授权回调域名；移动端需要完成 OpenSDK、Bundle ID / 应用签名和 iOS Universal Links 配置。

### 通过系统设置管理

系统管理员打开「设置 → 系统设置 → 微信登录」，可分别配置网站和移动应用的开关、AppID、AppSecret，以及网站回调地址和开放平台账号标识。某个应用的开关打开后才展开它的 AppID、密钥等字段；有未保存的修改时，页面底部出现操作栏，点击保存后整组配置即时生效，无需重启后端。普通用户、空间管理员和 API Key 均不能访问这些管理接口。

- 尚未保存页面配置时，使用部署时的环境变量 / YAML 配置；保存后，数据库中的**整组配置**优先，不再逐字段回退到环境变量。页面会显示当前配置来源。关闭某个入口或清空字段后，不会因为环境变量仍存在而自动重新启用。
- AppSecret 为只写字段，读取接口只返回 `secret_configured`。输入框留空表示保留当前有效密钥；输入新值表示替换；点击清除并确认后，会在保存时显式清空（保存前可取消清除）。更换 AppID 时必须同时替换或清除对应密钥，避免把旧应用的密钥带给新应用。
- 数据库密钥使用 AES-GCM 加密。保存非空密钥前必须在所有实例设置相同且稳定的 **32 字节 `SYSTEM_AES_KEY`**，并妥善备份。此加密主密钥仍由部署环境管理，不放进系统设置。没有主密钥时仍可使用环境变量接入；不能把非空凭据保存为数据库明文。主密钥丢失或变更后需恢复原密钥，或重新输入两端凭据。
- 配置读取或解密失败时停止微信登录，不回退到可能已过时的环境变量。配置变更会使此前配置发起的、尚未完成的授权尝试失效，用户需重新发起授权；已经签发的 WeKnora 会话沿用原有会话规则。
- 已存在 UnionID 身份关联后，不允许更改开放平台账号标识。某个 AppID 已有用户登录过后也不允许再更换，否则这些用户再次登录时会被识别为新用户并另开账号；确需迁移应用时应先确认应用归属和账号迁移方案。
- 未保存的修改切换页签后仍保留；点击底部操作栏的「放弃修改」会重新加载已保存的配置。保存前会检查已启用应用的 AppID、密钥和网站回调地址。
- 多个管理员同时编辑时，过时版本的保存返回 `409`；重新加载已保存配置后再编辑。保存动作记录 `system.wechat_auth_updated` 审计事件，审计不包含密钥。

管理接口为 `GET /api/v1/system/admin/auth/wechat` 和 `PUT /api/v1/system/admin/auth/wechat`。GET 返回 `source`（`deployment` / `database`）、`version`、`platform_id`、`encryption_ready`，以及 `web` / `mobile` 配置和密钥存在标记。PUT 必须提交 GET 得到的 `version` 和完整的 `web` / `mobile` 对象；其中 `app_secret` 不传或传 null 为保留，传空字符串为清除。示例：

```json
{
  "version": 0,
  "platform_id": "my-open-platform-account",
  "web": {
    "enabled": true,
    "app_id": "website_appid",
    "app_secret": "replace-with-real-secret",
    "callback_url": "https://weknora.example.com/api/v1/auth/wechat/callback"
  },
  "mobile": { "enabled": false, "app_id": "", "app_secret": "" }
}
```

### 通过部署配置提供默认值

服务默认关闭微信登录，可只启用其中一个入口。未保存系统设置时，服务端 `.env` 示例：

```dotenv
WECHAT_AUTH_WEB_ENABLED=true
WECHAT_AUTH_WEB_APP_ID=your_website_appid
WECHAT_AUTH_WEB_APP_SECRET=your_website_secret
WECHAT_AUTH_WEB_CALLBACK_URL=https://weknora.example.com/api/v1/auth/wechat/callback

WECHAT_AUTH_MOBILE_ENABLED=true
WECHAT_AUTH_MOBILE_APP_ID=your_mobile_appid
WECHAT_AUTH_MOBILE_APP_SECRET=your_mobile_secret

# 两个应用属于同一个微信开放平台账号时配置；否则留空。
WECHAT_AUTH_PLATFORM_ID=my-open-platform-account
```

也可在 `config.yaml` 配置 `wechat_auth.web` / `wechat_auth.mobile` 的 `enabled`、`app_id`、`app_secret`，以及 `wechat_auth.web.callback_url`、`wechat_auth.platform_id`。环境变量优先于 YAML；修改部署默认值需要重启服务，且不会覆盖已保存的数据库配置。AppSecret 只放在后端，不放进 Web 或移动客户端。

Web 必须使用 HTTPS，回调地址与打开登录页的域名保持一致，并匹配微信开放平台审核的回调域名。此版本使用同源 SPA 和 API 部署：浏览器登录绑定 Cookie 是 Secure、HttpOnly、SameSite=Lax，回调完成后返回同站点 `/`。不支持通过请求参数指定回调或登录后跳转到其他站点。

`GET /api/v1/auth/config` 返回 `wechat_web_enabled` 和 `wechat_mobile_enabled`。Web 登录页仅在网站应用启用时展示入口，原有密码和 OIDC 登录仍可使用。

## 账号与空间

- 服务端使用微信验证得到的 AppID + OpenID 查找用户，不接受客户端上报的 OpenID、UnionID、昵称或邮箱作为身份证明。
- 同一微信开放平台账号下的应用，配置相同且稳定的 `WECHAT_AUTH_PLATFORM_ID` 后，可依据微信返回的 UnionID 共用内部用户。该配置是部署者为一个已确认的开放平台账号设置的命名空间，不是微信返回的字段；上线后不要随意更改。
- UnionID 缺失或命名空间未配置时，仍可按 AppID + OpenID 登录；不同 AppID 不自动关联。若后来发现 OpenID 与 UnionID 已指向不同用户，拒绝登录并交由人工处理，不自动合并已有数据。
- 首次开户遵守当前 `auth.registration_mode`：仅 `self_serve` 允许直接创建。`invite_only` / `invite_register` 下，已有关联用户仍可登录，新身份不能借微信绕过邀请限制。本版不支持携带邀请令牌创建微信账号。
- 首次空间配置遵守 `auth.default_tenant_mode`：`create_personal` 创建个人空间、默认存储和 Owner 成员关系；`tenantless` 创建无空间用户。微信登录不会授予平台管理员或跨空间权限。
- 用户名和内部邮箱是随机生成的占位信息，邮箱使用 `.invalid` 域名，不代表用户的真实邮箱。随机初始密码不返回给用户。微信昵称、头像和手机号暂不拉取。
- 用户、空间、身份映射和登录令牌在同一事务内写入，失败时回滚。身份唯一约束防止并发开户重复创建账号；停用或删除用户后不能通过同一个微信身份重新开户。

已有邮箱 / OIDC 账号不会按昵称、邮箱或头像自动绑定微信。「验证原账号后绑定微信」尚未提供，已有数据需要保留的用户应继续使用原登录方式。

「系统设置 → 用户管理」新增账号来源列，显示本地、OIDC 或微信，对应 `users.auth_source`。它记录**账号创建来源**，不代表最近一次登录方式或当前全部可用的登录方式：本地用户之后通过 OIDC 登录仍保留本地来源；密码重置、偏好和角色更新不会改写来源。新建 OIDC / 微信账号由服务端写入来源，注册 API 不允许客户端指定。

迁移仅把仍带有 `preferences.oidc_only_login=true` 的历史账号标为 OIDC，其他历史账号保留本地默认值。旧版本没有完整记录创建来源，已经清除该标记的历史 OIDC 账号不能可靠追溯，不能将本列作为历史登录审计或权限依据。

## Web 流程

1. 登录页调用 `POST /api/v1/auth/wechat/web-attempts`。后端持久化有效期 5 分钟的登录尝试，将随机验证器存入浏览器 Cookie，只向页面返回 `app_id`、`scope`、`state`、`redirect_uri` 和 `expires_at`。
2. 登录页在弹窗中嵌入微信官方 `qrconnect` 二维码（`login_type=jssdk`，参数与官方 `wxLogin.js` 一致，但不加载该第三方脚本）。用户扫码并在手机上确认后，微信把整个页面跳转到回调地址。二维码在登录尝试过期前提示刷新，刷新会创建新的尝试并替换 Cookie。
   - 也可直接打开 `GET /api/v1/auth/wechat/start`，由后端创建同样的尝试后整页跳转到微信官方扫码页。
   - 网站扫码登录面向电脑浏览器；在微信内置浏览器中打开登录页时无法扫描本页二维码，弹窗会提示改用电脑浏览器。
3. 微信回调 `GET /api/v1/auth/wechat/callback?code=...&state=...`。
4. 后端验证浏览器绑定及 state，一次性消费登录尝试，用网站应用的凭据交换微信身份。
5. 后端创建或查找用户，签发 WeKnora 令牌，通过 `#wechat_ticket=...` 返回 SPA，票据有效期为 1 分钟。前端立即清除片段，再调用 `POST /api/v1/auth/wechat/complete`，请求 `{ "ticket": "..." }`。后端要求发起登录浏览器的 HttpOnly Cookie 并一次性消费票据，成功返回 `token` / `refresh_token`；前端读取 `/auth/me` 并进入对应空间。令牌不出现在重定向 URL 中。

失败时返回 `#wechat_error=...`，前端按 `registration_disabled`、`invalid_attempt`、`rate_limited` 等原因显示对应提示。回调和授权入口响应禁止缓存；微信凭据不返回浏览器。

## 移动端接口

### 创建授权尝试

客户端生成并保留至少 32 字节随机数编码得到的 `code_verifier`（43–128 个 RFC 7636 unreserved 字符），计算 `BASE64URL(SHA256(code_verifier))`，去掉 Base64 填充。

```http
POST /api/v1/auth/wechat/attempts
Content-Type: application/json

{"code_challenge":"<43-character base64url SHA-256 digest>"}
```

```json
{
  "success": true,
  "data": {
    "attempt_id": "<uuid>",
    "state": "<random state>",
    "app_id": "<configured mobile AppID>",
    "scope": "snsapi_userinfo",
    "expires_at": "2026-10-14T10:05:00Z"
  }
}
```

使用返回的 AppID、scope、state 发起 OpenSDK 授权。SDK 回调必须对应当前尝试，校验返回的 state；取消或拒绝授权不发起交换。验证器绑定是 WeKnora 登录尝试的保护机制，不表示微信授权服务器实现了 PKCE。

### 交换登录凭据

```http
POST /api/v1/auth/wechat/exchange
Content-Type: application/json

{
  "attempt_id": "<uuid>",
  "state": "<state returned by SDK>",
  "code": "<code returned by SDK>",
  "code_verifier": "<original random verifier>"
}
```

成功响应包含 `success`、`access_token`、`refresh_token`、`user`、`active_tenant`、`memberships`、`is_new_user`。`active_tenant` 可为 null；客户端随后调用 `/auth/me` 获取最新身份和权限。请求不能选择 AppID、AppSecret、内部用户 ID、空间或身份标识。

登录尝试存储于数据库，一次性消费，适用于多个后端实例。过期、错误验证器、错误 state、Web / mobile 入口混用会被拒绝。对外接口使用独立于邀请链接的 IP 限流器（单进程 120 次/分钟，一次网站登录消耗 3 次；多副本部署应在网关补充统一限流）。网站入口和回调被限流时跳回登录页并提示稍后重试，而不是返回 JSON 错误。

交换请求开始调用微信前就消费尝试；不缓存可重放的登录结果。上游失败、客户端丢失响应或登录尝试过期时，应新建尝试并重新授权，不自动重放旧 code。

## 会话与当前边界

后续 API 使用 WeKnora `access_token`，不能使用微信 access_token。刷新沿用 `POST /auth/refresh`，请求字段 `refreshToken`，响应为 `access_token` / `refresh_token`。

本版沿用现有会话语义：`POST /auth/logout` 撤销该用户所有会话；刷新空间依据账号的最近活跃空间，其他设备切换空间可能影响刷新结果。尚未实现设备级会话、当前设备退出、微信账号绑定 / 解绑、PersonalAgent 初始化或 iOS 客户端。移动端凭据应保存在 Keychain 等系统安全存储中。

数据库迁移：PostgreSQL `000120_wechat_auth`，SQLite `000038_wechat_auth`。启用前运行常规迁移。回滚迁移会删除身份映射、已保存的微信配置和账号来源字段，因此应先关闭微信登录并按部署备份流程处理。

## 官方参考

- [网站应用微信登录开发指南](https://developers.weixin.qq.com/doc/oplatform/Website_App/WeChat_Login/Wechat_Login.html)
- [移动应用微信登录开发指南](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/WeChat_Login/Development_Guide.html)
- [iOS 接入指南](https://developers.weixin.qq.com/doc/oplatform/Mobile_App/Access_Guide/iOS.html)

## 实现参考

系统配置的数据库优先、密钥只写不读及用户来源标识，参考 [PR #4133 — User Management console (local users / OIDC / LDAP)](https://github.com/Tencent/WeKnora/pull/4133)。该 PR 在本次核对时尚未合并；这里借鉴设计，没有引入其 LDAP 实现或 OIDC 配置管理代码。微信配置使用独立的密钥存储与管理接口，强制加密保存，并在读取失败时停止认证。

界面扩展当前代码中已合并的 [PR #4153 — platform user management and streamline settings](https://github.com/Tencent/WeKnora/pull/4153)，保留现有用户管理入口和系统管理员权限边界。
