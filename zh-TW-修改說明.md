# 台灣繁體中文（zh-TW）語系包修改說明

## 專案概述
本文件記錄 WeKnora 前端專案新增台灣繁體中文語系包的所有修改內容，包含語系檔案建立、國際化框架註冊、UI 選單適配、用語校正與部署流程。

---

## 修改檔案清單

### 1. 語系核心檔案
| 檔案路徑 | 修改內容 |
|---------|---------|
| `frontend/src/i18n/locales/zh-TW.ts` | **新增**。以 `zh-CN.ts` 為基準模板，建立台灣繁體中文語系包（共 6358 行） |

### 2. 國際化框架註冊
| 檔案路徑 | 修改內容 |
|---------|---------|
| `frontend/src/i18n/index.ts` | 新增 `zh_TW` 動態匯入邏輯與載入註冊 |
| `frontend/src/i18n/resolveDefaultLocale.ts` | 將 `zh-TW` 加入預設語系解析優先級（瀏覽器語言比對） |
| `frontend/src/i18n/localeKeyAudit.ts` | 將 `zh-TW` 納入語系金鑰審計清單，確保與 `zh-CN` key 數量一致 |
| `frontend/src/i18n/embed.ts` | 支援嵌入頁面自動偵測 `zh-TW` 語系偏好 |

### 3. 全域 UI 適配
| 檔案路徑 | 修改內容 |
|---------|---------|
| `frontend/src/App.vue` | 匯入 TDesign `zh_TW` 本地化配置並註冊至 `TDesignProvider` |
| `frontend/src/views/auth/Login.vue` | 登陸頁面語系切換選單新增 `zh-TW` 選項 |
| `frontend/src/views/settings/GeneralSettings.vue` | 一般設定頁語系下拉選單支援繁體中文 |
| `frontend/src/views/agent/AgentEmbedChannelPanel.vue` | Agent 嵌入渠道設定頁語系適配 |

---

## 用語校正統計

初始版本執行 **19 組詞彙映射**，全面替換中國大陸用語為台灣繁體中文用語，初步修正 **685 處**。後續完整複核再以 OpenCC `s2twp` 僅處理翻譯值，並人工保留台灣慣用字（如「群」、「核」）；共校正 2,076 個語系項目，包含「快速問答」、「智慧體」、「載入」、「連結」、「登入」、「使用者」、「支援」等用語。

### 主要替換對應表
| 大陸用語 | 台灣繁體中文 | 說明 |
|---------|-------------|------|
| 创建 | 建立 | Create |
| 搜索 | 搜尋 | Search |
| 账户 | 帳號 | Account |
| 密钥 | 金鑰 | Secret Key |
| 服务器 | 伺服器 | Server |
| 二维码 | QR 碼 | QR Code |
| 变量 | 變數 | Variable |
| 恢复 | 還原 | Restore |
| 凭证 | 憑證 | Credential |
| 配置 | 設定 | Configuration |
| 取消 | 取消 | Cancel（一致） |
| 确定 | 確定 | Confirm |
| 删除 | 刪除 | Delete |
| 编辑 | 編輯 | Edit |
| 详情 | 詳細資訊 | Details |
| 状态 | 狀態 | Status |
| 参数 | 參數 | Parameter |
| 权限 | 權限 | Permission |
| 导入/导出 | 匯入/匯出 | Import/Export |

### 校正方法
使用 Python Regex 腳本進行批量替換，確保：
- 僅替換翻譯值（value），不影響程式碼金鑰（key）
- 避免誤觸發變數名稱與 API 端點
- 執行多次 Regex 交叉驗證，確認零殘留

---

## 技術細節

### 國際化架構
- **框架**：`vue-i18n` v9+
- **UI 組件庫**：`TDesign Vue Next`（含內建本地化配置）
- **動態載入**：使用 `import()` 惰性載入語系 chunk，減少初始包體積
- **語系偵測順序**：
  1. 使用者手動設定（localStorage）
  2. 瀏覽器 `navigator.language`
  3. 預設回退至 `zh-CN`

### 語系金鑰審計
專案內建 `localeKeyAudit.ts` 腳本，執行 `npm run locale-key-audit` 時會自動：
1. 掃描 `zh-CN.ts` 所有金鑰
2. 比對 `zh-TW.ts` 是否存在對應金鑰
3. 輸出缺失金鑰報告，防止 `missing translation` 警告

---

## 部署流程

### 遠端環境資訊
- **容器主機**：`172.21.10.92`
- **SSH 跳板**：`172.21.10.94`
- **登入帳號**：`leonoxo`
- **專案路徑**：`/mnt/weknora/`
- **容器名稱**：`WeKnora-frontend`、`WeKnora-app`

### 部署步驟
1. 本機執行 `npm ci` 安裝依賴
2. 執行 `npm run build` 編譯前端
3. 壓縮 `dist/` 為 `frontend-dist.tar.gz`
4. 透過 `scp` 推送至遠端 `/tmp/`
5. 解壓縮至 `/mnt/weknora/frontend/dist/`
6. 執行 `docker compose up -d --build frontend` 重建映像檔並重啟容器

### 驗證方式
- 檢查容器 JS Bundle 是否包含 `zh-TW` 字串
- 瀏覽器開發者工具 `Network` 面板確認 `/assets/` 請求回傳 `200`
- 強制無快取重新載入（`⌘+Shift+R`）驗證語系生效

---

## 已知問題與解決方案

### 對話圖片顯示相容性（2026-08-14 更新）
**現象**：部分自架環境在載入 RAG 或歷史對話的知識庫圖片時，圖片短暫出現後消失；Nginx 可見下列請求回傳 `404`：
```text
/api/v1/sessions/<session-id>/messages/<message-id>/files?file_path=resource://...
```

**根因**：較新的前端具有 message-scoped 圖片代理機制，但搭配尚未提供此 API 路由的較舊自架後端時，該路由會回傳 `404`。前端既有邏輯將 `404` 視為檔案遺失並移除圖片，造成閃退觀感。這不是 MinerU 未保存圖片，也不是繁中翻譯內容造成。

**已部署的相容性修復**：前端仍先使用 message-scoped 路由；若取得 `404`，僅針對已登入的 message scope 自動以既有的租戶檔案路由重試：
```text
/files?file_path=resource://...
```
Embed 訪客模式不會降級，以維持其 Embed token 的權限邊界。

**驗證**：使用繁中介面重新開啟既有 RAG 對話，圖片已可正常顯示，無須重新提問。

**目前部署 Image**：
```text
weknora-ui:zh-tw-v0.7.2-overlay-20260814
```

**部署驗證（2026-08-14）**：已在官方 `v0.7.2` 前後端基準上重新編譯並部署；確認 served bundle 含 `zh-TW` 與受保護圖片相容性 fallback，並保留官方基準 Image：
```text
weknora-ui:official-v0.7.2-before-zh-tw-overlay-20260814
```

**後續處置**：此相容性修復應以獨立 PR 提交，不混入 zh-TW 語系 PR。待後端版本原生支援上述 session/message 檔案路由後，才評估移除此 fallback。

### 聊天附件 TTL
聊天中「使用者直接上傳」的暫存附件另受下列設定管理，和知識庫（RAG）圖片是不同路徑：
```env
WEKNORA_CHAT_ATTACHMENT_TTL_HOURS=24
```
超過 TTL 的聊天附件可能被後端清理；可依實際保留政策調整 `.env` 後重啟 app 容器。

### 知識庫圖片儲存位置
知識庫圖片與聊天附件不同，**不會被自動刪除**。存放位置：
- **容器內**：`WeKnora-app` 的 `/data/files/<空間ID>/<知識庫ID>/`
- **Docker Volume**：`weknora_data-files`
- 除非手動刪除知識庫或文件，否則圖片會永久保留

---

## PR 資訊
- **PR 編號**：#2708
- **狀態**：OPEN
- **URL**：https://github.com/Tencent/WeKnora/pull/2708
- **CI 狀態**：CodeCC 安全掃描（自動執行，無需手動回覆）

---

## 維護建議

### 未來語系更新
1. 官方發布新版本後，執行 `npm run locale-key-audit` 檢查是否有新增金鑰
2. 若有缺失，執行 `npm run regenerate-pruned-locales` 自動補齊
3. 手動校對新增內容的台灣繁體中文翻譯
4. 提交 PR 至官方倉庫

### 使用者偏好儲存
語系偏好儲存在瀏覽器 `localStorage` 的 `weknora-locale` 金鑰中，清除快取會重置為系統預設。

---

## 版本紀錄
| 日期 | 版本 | 修改內容 |
|------|------|---------|
| 2026-08-14 | v0.4 | 官方 UI Image 更新後，以 v0.7.2 基準重新部署 zh-TW Overlay；保留官方 Image 備份並驗證語系 bundle、內建智能體繁中名稱與圖片 fallback |
| 2026-08-14 | v0.3 | 完整複核 zh-TW 全部翻譯值；以 OpenCC `s2twp` 校正 2,076 個項目，確認「快速問答」等簡體用語零殘留，並完成語系審計與 production build 驗證 |
| 2026-08-14 | v0.2 | 更正 RAG 圖片閃退根因為新舊前後端圖片代理 API 不相容；記錄部署的 fallback Image 與獨立修復 PR 策略 |
| 2026-08-14 | v0.1 | 初始建立 zh-TW 語系包，完成 19 組用語校正、6 個核心檔案修改、遠端部署與 PR 提交 |

---

*本文件由團隊協作產生，最後更新：2026-08-14*
