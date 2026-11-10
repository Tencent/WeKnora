---
name: 命理大师
  version: 1.2.6
  description: |
    全体系命理大师 — 八字四柱、紫微斗数、奇门遁甲、六爻、梅花易数、塔罗、星盘、
    数字命理、九宫飞星风水、掌纹面相、起名命名、穿衣搭配、合婚择吉一站式解读。仅作文化参考，不替代医疗、法律、心理、财务、婚姻、命名等
    专业建议；遇重大决策请咨询专业人士。
    【数据与隐私提示】本 skill 会在本地文件系统保存用户主动提交的出生年月日时、地点、
    姓名、可选的家庭成员信息、交互主题记录（用于追踪偏好，默认关闭），以及推送任务日志。
    所有数据均不上传，仅在本地 data/ 目录留存；用户可随时通过 profile.js 查看、编辑或删除。
    每日推送为 opt-in，默认关闭。
metadata:
  displayName: "命理大师"
  author:
    - "腾讯高级研发-enoyao"
    - "腾讯高级产品运营-rekyhe"
  version: 1.2.0
  keywords: 八字, 紫微斗数, 奇门遁甲, 六爻, 梅花易数, 塔罗, 星盘, 九宫飞星, 掌纹, 手相, 面相, 起名, 命名, 取名, 穿衣, 搭配, 颜色, 五行色, 开运色, 今日运势, 每日运程, 合婚, 择吉, 生命灵数, 风水, 算命, BaZi, ZiWei, QiMen, Tarot, feng shui, I Ching, numerology, daily horoscope, palmistry, physiognomy, naming, dressing
  # 触发关键词收紧：1.1.8 移除「算命 / 占卜 / 命理 / 数字命理 / fortune telling / astrology」等过宽泛词，
  # 避免在用户未明确要求占卜的对话中误激活本 skill；仅在明确出现具体体系名称或具体主题时才被触发。
  openclaw:
    emoji: "☯️"
    skillKey: "university-applications"
    runtime:
      node: ">=18"
      python3: true
    install:
      - kind: node
        package: iztro
    env: []  # 本 skill 不再读取任何环境变量；曾经的 OPENCLAW_KNOWLEDGE_DIR 已被移除以避免 file-system enumeration。
    security:
      network:
        default: none
        optional:
          - feature: "liuyao HTML LLM divination (user-initiated, in-browser, OFF by default)"
            allowed-endpoints:
              - "https://api.openai.com"
              - "https://api.anthropic.com"
              - "https://api.deepseek.com"
            custom-endpoint: "only after explicit in-UI consent dialog; HTTPS-only, no IP/localhost auto-trust"
            data-sent: "only the hexagram and the user's typed question; no profile, no env vars, no file paths"
            credential: "user-provided LLM API key, entered at runtime, stored in browser localStorage only"
          - feature: "liuyao HTML Google Fonts (commented out by default)"
            endpoint: "https://fonts.googleapis.com"
            data-sent: "none beyond standard font request"
            credential: none
      credentials:
        bundled: none
        required: none
        user-optional:
          - "LLM API key for liuyao/index.html divination feature (scope it to a separate limited key, never reused)"
      push-mechanism: openclaw-ipc
      push-optin: true
      push-default-state: disabled
      data-retention:
        location: "local filesystem under data/profiles/ and data/push-log.json"
        remote-upload: none
        user-controls:
          - "view:   node scripts/profile.js show <userId>"
          - "list:   node scripts/profile.js list"
          - "edit:   node scripts/profile.js save <userId> <field> <value>"
          - "delete: node scripts/profile.js delete <userId>"
          - "disable push: node scripts/push-toggle.js off <userId>"
      notes: |
        All bundled scripts perform local computation only — no fetch, axios,
        https.request, curl, wget, or any outbound network calls from the Node/Python
        side. Push delivery is handled entirely by the OpenClaw runtime via stdout/IPC
        protocol, and is OPT-IN (disabled until the user runs push-toggle.js on).
        The 'channels' field in user profiles (e.g. telegram) is a routing hint for
        the OpenClaw runtime, not a direct API integration. This skill does not hold
        or require any third-party API tokens (Telegram Bot Token, SMTP credentials,
        webhook URLs, etc.). The local-only release helper script is excluded
        from the published bundle via .clawhubignore and is not part of the
        installed skill surface.
        EXCEPTION — OPTIONAL LLM NETWORK USE: the browser-only file liuyao/index.html
        exposes an optional "LLM divination" button. If and only if the user clicks
        it and fills in their own API key + endpoint, the browser (not the skill
        process) will POST the hexagram and question to that user-configured endpoint.
        No key is bundled, hardcoded, or transmitted anywhere else. Users are advised
        to supply a scoped/limited API key rather than a primary account key.
        User profile data (birth details, optional family members, interaction log)
        is stored only on the local filesystem and can be viewed, edited, or deleted
        at any time via scripts/profile.js (see data-retention.user-controls above).
---

# body
