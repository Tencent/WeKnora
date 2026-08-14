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

共執行 **19 組詞彙映射**，全面替換中國大陸用語為台灣繁體中文用語，總計修正 **685 處**。

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

### 對話圖片閃退現象
**現象**：歷史對話中的圖片載入時「閃一下然後消失」

**原因**：此問題與語系修改**無關**。WeKnora 內建聊天附件過期機制：
```env
WEKNORA_CHAT_ATTACHMENT_TTL_HOURS=24  # 預設 24 小時
```
對話中上傳的圖片超過 24 小時後會被後端自動刪除，前端載入時 API 回傳 `404`，觸發圖片組件 `@error` 隱藏邏輯。

**解決方案**：
修改 `/mnt/weknora/.env` 中的 TTL 設定：
```bash
# 改成 720 小時（30天）
sed -i 's/WEKNORA_CHAT_ATTACHMENT_TTL_HOURS=.*/WEKNORA_CHAT_ATTACHMENT_TTL_HOURS=720/' .env

# 或永不刪除
sed -i 's/WEKNORA_CHAT_ATTACHMENT_TTL_HOURS=.*/WEKNORA_CHAT_ATTACHMENT_TTL_HOURS=0/' .env
```
修改後重啟後端容器：
```bash
docker compose up -d app
```

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
| 2026-08-14 | v0.1 | 初始建立 zh-TW 語系包，完成 19 組用語校正、6 個核心檔案修改、遠端部署與 PR 提交 |

---

*本文件由團隊協作產生，最後更新：2026-08-14*
