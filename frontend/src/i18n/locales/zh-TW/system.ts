export default {
  documentProgress: {
    queueView: '佇列任務',
    title: '資料與 Wiki 進度',
    description: '分別查看目前入庫和 Wiki 處理情況，恢復後的原始失敗記錄繼續保留。',
    refresh: '重新整理',
    search: '搜尋資料或知識庫',
    searchButton: '搜尋',
    filter: '進度篩選',
    count: '啟動數量（1–100）',
    batchStart: '批次整理 Wiki',
    confirmBatch: '依目前搜尋範圍，從等待最久的資料開始，安排最多 {count} 份 Wiki 待辦？',
    confirmOne: '使用「{title}」的既有進度，安排 Wiki 整理？',
    attempt: '第 {value} 次處理',
    pendingOps: '{count} 項待辦 · 重試計數 {retries}',
    historyCount: '{count} 筆記錄',
    details: '詳細資訊',
    startNow: '立即整理',
    currentStatus: '目前狀態',
    history: '失敗歷史',
    historyDescription: '此處保留原始失敗記錄，也包含後來已恢復的失敗；歷史記錄不代表資料目前仍在失敗。',
    noHistory: '此資料沒有封存的失敗記錄。',
    failureAttempts: '累計失敗 {count} 次',
    historyLimit: '顯示最近 50 筆，原始歷史繼續保留。',
    unfinished: '未完成',
    all: '全部資料',
    document: '資料',
    ingestion: '入庫',
    wiki: 'Wiki',
    actions: '操作',
    loadError: '暫時無法讀取資料進度，請重新整理後再試。',
    started: '已安排 {count} 份，另有 {queued} 份已在佇列中；既有進度保留。',
    startError: '佇列交接中斷前已接收 {count} 份；請重新整理後再安排剩餘資料。',
    states: {
      ready: '已入庫',
      pending: '待處理',
      processing: '處理中',
      finalizing: '收尾中',
      completed: '已完成',
      failed: '失敗',
      skipped: '已略過',
      cancelled: '已取消',
      queued: '排隊中',
      retrying: '重試中',
      disabled: '未啟用',
      notStarted: '尚未開始'
    },
    stages: {
      docreader: '文件解析',
      chunking: '分塊',
      embedding: '索引建立',
      multimodal: '圖片處理',
      postprocess: '後處理'
    }
  },
  system: {
    title: '系統資訊',
    sectionDescription: '查看系統版本資訊和使用者帳戶設定',
    loadingInfo: '正在載入資訊...',
    retry: '重試',
    versionLabel: '應用版本',
    versionDescription: '當前應用服務（weknora-app）的版本號',
    frontendVersionLabel: 'UI 版本',
    frontendVersionDescription: '當前 UI 界面（weknora-ui）的建構版本號',
    versionMismatch: '與應用版本不一致',
    buildTimeLabel: '建構時間',
    buildTimeDescription: '系統建構的時間',
    goVersionLabel: 'Go 版本',
    goVersionDescription: '後端使用的 Go 語言版本',
    docreaderVersionLabel: 'Docreader 版本',
    docreaderVersionDescription: '目前文件解析服務（weknora-docreader）的建構版本號',
    startedAtLabel: '服務啟動時間',
    startedAtDescription: '當前後端行程最近一次啟動的時刻',
    uptimeLabel: '運行時長',
    uptimeDescription: '自本次啟動以來的連續運行時間',
    uptimeDays: '{n} 天',
    uptimeHours: '{n} 小時',
    uptimeMinutes: '{n} 分鐘',
    uptimeSeconds: '{n} 秒',
    dbVersionLabel: '資料庫版本',
    dbVersionDescription: '當前資料庫遷移版本號',
    dbMigrationFailedTag: '遷移失敗',
    dbMigrationFailedTitle: '資料庫遷移失敗',
    dbMigrationFailedDesc: '啟動時資料庫遷移未成功完成，部分表或索引可能未建立，會導致 Wiki、知識圖譜等功能例外。建議先查看排查文件自助修復；如仍無法解決，請透過下方連結回饋。',
    dbMigrationViewDocs: '查看排查文件',
    dbMigrationReportIssue: '無法修復？提交 Issue',
    keywordIndexEngineLabel: '關鍵字索引引擎',
    keywordIndexEngineDescription: '當前使用的關鍵字索引引擎',
    vectorStoreEngineLabel: '向量存儲引擎',
    vectorStoreEngineDescription: '當前使用的向量存儲引擎',
    graphDatabaseEngineLabel: '圖資料庫引擎',
    graphDatabaseEngineDescription: '當前使用的圖資料庫引擎',
    unknown: '未知',
    globalSettings: {
      title: '系統設定',
      description: '平台級運行時設定，儲存後立即對所有空間生效。僅系統管理員可見可改。',
      loading: '載入中...',
      empty: '暫無可設定的系統設定',
      saving: '儲存中',
      saved: '已儲存',
      saveAnnouncement: '{label} 已儲存',
      badgeRequiresRestart: '需重啟',
      badgeSecret: '敏感',
      badgeHighRisk: '高風險',
      badgeOverride: '已覆蓋',
      badgeOverrideTooltip: '該值已由管理員儲存到資料庫，覆蓋了環境變數與預設值',
      modifiedAt: '上次修改：{value}',
      tagInputPlaceholder: 'Enter新增條目，例：example.com / *.foo.com / 10.0.0.0/8',
      audit: {
        tabLabel: '稽核日誌',
        description: '記錄平台級操作：系統設定變更、系統管理員授予/回收、配額批次同步等。按時間倒序展示。',
        refresh: '重新整理',
        retry: '重試',
        loading: '載入中...',
        end: '已經到底了。',
        empty: '暫無平台級稽核事件。',
        forbidden: '無權查看平台稽核日誌。',
        systemActor: '系統',
        drawer: {
          sectionSummary: '事件概要',
          sectionIdentifiers: '關聯標識',
          sectionRequest: '請求資訊',
          targetChange: '變更內容',
          requestMethod: '請求方法'
        },
        expanded: {
          actorId: '操作人 ID',
          targetUserId: '目標使用者 ID',
          targetType: '目標類型',
          targetId: '目標 ID',
          details: '原始詳情'
        },
        target: {
          bulkQuota: '批次同步：預設存儲配額',
          bulkQuotaDiff: '應用到 {count} 個空間（{gb} GB）',
          promoteIdempotent: '目標已是系統管理員（冪等）',
          revokeNoop: '目標本就不是系統管理員（冪等）',
          requiredRole: '需要角色：{role}',
          valueNull: '(空)'
        },
        outcome: {
          success: '成功',
          denied: '拒絕'
        },
        action: {
          'system.setting_changed': '系統設定變更',
          'system.admin_promoted': '授予系統管理員',
          'system.api_key_created': '建立平台 API Key',
          'system.api_key_revoked': '吊銷平台 API Key',
          'system.admin_revoked': '回收系統管理員',
          'system.user_password_reset': '重設使用者密碼',
          'system.user_created': '建立使用者',
          'system.queue_task_retried': '重新執行失敗任務',
          'system.queue_task_deleted': '清除失敗任務記錄',
          'system.queue_task_run_now': '立即執行佇列任務',
          'system.queue_task_cancelled': '終止佇列任務',
          'system.queue_archived_purged': '清除全部失敗任務'
        },
        columns: {
          time: '時間',
          actor: '操作人',
          action: '事件',
          target: '目標',
          path: '請求',
          outcome: '結果'
        },
        actorRole: {
          system_admin: '系統管理員'
        },
        errors: {
          generic: '載入稽核日誌失敗'
        }
      },
      bulkApply: {
        label: '應用到所有現有空間',
        tooltip: '儲存的值預設只對之後新建的空間生效；點擊此按鈕將當前值同步寫入所有現有空間。',
        confirmBtn: '應用',
        confirmBody: '將把所有現有空間的存儲配額覆蓋為 {value} {unit}。如有空間被運維單獨調整過的配額，也會一併被覆蓋。是否繼續？',
        success: '已將 {count} 個空間的存儲配額更新為 {value} {unit}',
        failed: '應用到所有空間失敗'
      },
      userList: {
        title: '平台使用者列表',
        rowLabel: '使用者管理',
        rowDesc: '檢視平台上的全部註冊使用者（跨空間），支援分頁與依使用者名稱/郵箱篩選。',
        searchPlaceholder: '依使用者名稱或郵箱篩選（目前頁面）',
        refresh: '重新整理',
        active: '正常',
        inactive: '已停用',
        colUsername: '使用者名稱',
        colStatus: '狀態',
        colRole: '角色',
        colCreated: '建立日期',
        colAction: '操作',
        prev: '上一頁',
        next: '下一頁',
        pageCount: '{shown} 筆 / 本頁 {total}',
        enable: '啟用',
        disable: '停用',
        resetPassword: '重設密碼',
        confirmDisable: '確定停用帳號 {name} 嗎？將立即撤銷其登入狀態。',
        confirmEnable: '確定啟用帳號 {name} 嗎？',
        enabledMsg: '已啟用 {name}',
        disabledMsg: '已停用 {name}',
        actionFailed: '操作失敗',
        loadFailed: '載入使用者列表失敗',
        roleAdmin: '系統管理員',
        roleUser: '一般使用者',
        confirmRevoke: '確定撤銷 {name} 的系統管理員權限嗎？',
        confirmPromote: '確定將 {name} 提升為系統管理員嗎？',
        revokeBtn: '撤銷',
        promoteBtn: '提升',
        revokedMsg: '已撤銷 {name} 的管理員權限',
        promotedMsg: '已將 {name} 提升為系統管理員',
        roleFailed: '修改角色失敗'
      },
      passwordReset: {
        dialogTitle: '重設其他使用者的密碼',
        warning: '這是高風險操作。請核對使用者電子信箱；出於安全考慮，不能在這裡重設自己的密碼。',
        emailLabel: '使用者電子信箱',
        newPasswordLabel: '新密碼',
        newPasswordPlaceholder: '請輸入新密碼',
        confirmPasswordLabel: '確認新密碼',
        confirmPasswordPlaceholder: '再次輸入新密碼',
        confirmBtn: '重設',
        success: '密碼已重設，該使用者的現有會話已失效',
        failed: '重設密碼失敗'
      },
      createUser: {
        action: '建立使用者',
        dialogTitle: '建立新使用者',
        warning: '這是高風險操作。請仔細核對使用者名和電子信箱──它們將作為該使用者登入的唯一憑據。',
        usernameLabel: '使用者名',
        usernamePlaceholder: '2-50 個字符',
        emailLabel: '電子信箱',
        emailPlaceholder: '請輸入有效的電子信箱地址',
        autoGenerateLabel: '自動生成隨機密碼',
        newPasswordLabel: '新密碼',
        newPasswordPlaceholder: '8-32 個字符，包含字母和數字',
        confirmPasswordLabel: '確認新密碼',
        confirmPasswordPlaceholder: '再次輸入新密碼',
        confirmBtn: '建立使用者',
        success: '使用者已建立',
        successIdempotent: '使用者已存在，未做任何更改',
        missingPassword: '使用者已建立，但未返回生成的密碼。請使用重設密碼來復原存取。',
        failed: '建立使用者失敗',
        validation: {
          usernameRequired: '請輸入使用者名',
          usernameLength: '使用者名長度必須為 2-50 個字符',
          emailRequired: '請輸入電子信箱',
          emailInvalid: '請輸入有效的電子信箱地址',
          confirmRequired: '請再次輸入新密碼',
          passwordMismatch: '兩次輸入的密碼不一致'
        },
        generated: {
          successTitle: '使用者已建立',
          successBody: '已為該使用者生成一個隨機密碼。此密碼只會顯示一次。',
          usernameLabel: '使用者名',
          emailLabel: '電子信箱',
          passwordLabel: '生成的密碼',
          copyBtn: '複製帳號資訊',
          copySuccess: '帳號資訊已複製到剪貼簿',
          acknowledgeBtn: '我已儲存密碼'
        }
      },
      reset: {
        label: '重設',
        tooltip: '清除當前 UI 覆蓋，復原使用環境變數或內建預設值',
        confirmBtn: '重設',
        confirmBody: '確定要重設「{label}」嗎？該操作會刪除資料庫中的覆蓋值，回退到環境變數或內建預設值。',
        success: '已重設為預設值',
        failed: '重設失敗'
      },
      messages: {
        loadFailed: '載入系統設定失敗',
        saveSuccess: '已儲存',
        saveFailed: '儲存失敗'
      },
      listConfirm: {
        ssrf: {
          whitelist: {
            remove: {
              header: '移除 SSRF 白名單條目',
              body: '確認從 SSRF 白名單中移除 {entry}？移除後該條目將重新受 SSRF 防護攔截。',
              confirmBtn: '移除'
            },
            add: {
              header: '新增 SSRF 白名單條目',
              body: '確認把 {entry} 加入 SSRF 白名單？該條目比對到的主機 / IP / 網段會繞過 SSRF 防護，可能讓 Agent 存取內網服務，請僅在確知用途時新增。',
              confirmBtn: '新增'
            }
          }
        }
      },
      confirm: {
        confirmBtn: '確認儲存',
        cancelBtn: '取消',
        emptyValue: '（空）',
        bodyAuthRegistrationMode: '即將把「{label}」改為：{value}\n\n如果切到 self_serve，公網任何人都可以註冊帳號 — 務必確認是預期行為。',
        bodySandboxDockerEnabled: '打開後，空間管理員可以把沙箱指到本機 Docker。本機 docker.sock 等同宿主機 root，只適合已掛載 daemon 或配了 TLS 遠程 tcp:// 的私有化單機。'
      },
      enumLabels: {
        auth: {
          default_tenant_mode: {
            create_personal: '自動建立個人空間',
            tenantless: '不自動建立空間'
          },
          registration_mode: {
            self_serve: '自助註冊（任何人可註冊）',
            invite_register: '僅限邀請註冊（需有效邀請連結）',
            invite_only: '禁止註冊（已有帳號仍可接受邀請）'
          }
        }
      },
      keyDescriptions: {
        model: {
          max_concurrency: '後台任務（文件入庫/富化）對單個模型的預設併發上限，按模型 ID 全副本共享。每次呼叫實時讀取，修改後立即生效、無需重啟。0 或負數表示關閉預設限制（各模型仍會尊重自身在模型管理裡設定的上限）。僅影響後台任務，不影響交互式對話。'
        },
        asynq: {
          core_concurrency: '文件解析與手工重解析的每實例保底併發，可額外借用共享彈性池；修改後需重啟。',
          postprocess_concurrency: '解析完成後的輕量收尾和富化扇出專用併發；修改後需重啟。',
          enrichment_concurrency: '摘要、圖片、圖譜與問題生成的每實例保底併發，可額外借用共享彈性池；修改後需重啟。',
          maintenance_concurrency: '資料源同步、批處理和清理任務的每實例併發，與使用者面流水線硬隔離；修改後需重啟。',
          shared_concurrency: '核心解析與內容富化共同使用的每實例彈性併發，由有積壓的一側自動借用；修改後需重啟。',
          wiki_concurrency: '每個服務實例的 Wiki 專用 Worker 併發數，與上遊任務池相互隔離。最小值為 1；修改後需重啟服務行程方可生效。'
        },
        tenant: {
          max_owned_per_user: '每個非超管使用者透過自助建立可擁有的最大空間數。每次建立空間時實時讀取，修改後立即生效。0 表示使用內建預設值 10；負數表示完全關閉限制（不建議在公開部署使用）。',
          self_service_creation_enabled: '是否允許非超管使用者主動建立空間。關閉後，普通使用者只能透過邀請加入已有空間；跨空間超管仍可建立。修改後立即生效。',
          default_storage_quota_gb: '新建空間時預設分配的存儲配額（GB），包含向量、原文、文字、索引等。僅在建立時讀取，修改後只對之後新建的空間生效，不會回寫已存在的空間。0 或負數表示使用內建預設值 10GB。',
          default_storage_quota_mb: '包括註冊自動建立的個人空間。正數優先於 GB 設定；0 或負數沿用 GB 設定。每個設定按資料庫、環境變數、內建預設值的順序讀取，僅影響之後新建的空間。',
          auto_create_api_key: '為新空間自動生成 full_access API Key，並在建立回應中返回明文 token。僅用於相容依賴舊行為的集成；預設關閉，建議透過 API Key 管理顯式建立。',
          auto_accept_invitation: '開啟後，空間管理員透過電子信箱邀請已註冊使用者時，對方會立即成為成員，不再經過收件匣確認。關閉時保持「發出邀請 → 被邀請人確認」流程。修改後立即生效。'
        },
        ssrf: {
          whitelist: 'SSRF 防護白名單。可填入 example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1。修改後立即生效。SSRF_WHITELIST_EXTRA 環境變數仍由部署方維護，不在此處覆蓋。'
        },
        sandbox: {
          docker_enabled: '是否允許 Docker 沙箱後端。本機 docker.sock 等同宿主機 root，預設關閉。僅系統管理員可打開；打開後立即生效，無需重啟。私有化單機且已掛載 daemon socket，或設定了帶 TLS 的遠程 tcp:// 時再啟用。'
        },
        auth: {
          registration_mode: '註冊模式。開放註冊允許任何人建立帳號；僅限邀請註冊要求有效邀請連結；禁止註冊不允許建立帳號，但已有帳號仍可接受邀請。儲存後立即生效。',
          default_tenant_mode: '公開註冊後的空間初始化策略。create_personal 會自動建立個人空間並授予 Owner；tenantless 僅建立帳戶，使用者需要接受邀請或主動建立空間。只影響之後註冊的使用者。',
          complex_password_enabled: '是否啟用複雜密碼。開啟後密碼必須包含大小寫字母、數字和特殊字符。修改後立即生效，只影響新註冊使用者或新密碼修改/重設操作。特殊字符包含：{specialChars}'
        }
      },
      keyLabels: {
        model: {
          max_concurrency: '模型預設併發上限'
        },
        asynq: {
          core_concurrency: '核心解析保底併發數',
          postprocess_concurrency: '後處理編排併發數',
          enrichment_concurrency: '內容富化保底併發數',
          maintenance_concurrency: '維護與同步併發數',
          shared_concurrency: '共享彈性併發數',
          wiki_concurrency: 'Wiki Worker 併發數'
        },
        tenant: {
          max_owned_per_user: '每使用者最大空間數',
          self_service_creation_enabled: '允許使用者自助建立空間',
          default_storage_quota_gb: '新空間預設存儲配額 (GB)',
          default_storage_quota_mb: '新空間預設存儲配額 (MB)',
          auto_create_api_key: '建立空間時自動生成 API Key',
          auto_accept_invitation: '邀請已註冊使用者時自動加入'
        },
        ssrf: {
          whitelist: 'SSRF 防護白名單'
        },
        sandbox: {
          docker_enabled: '啟用 Docker 沙箱'
        },
        auth: {
          registration_mode: '註冊模式',
          default_tenant_mode: '註冊預設空間策略',
          complex_password_enabled: '啟用複雜密碼'
        }
      },
      runtime: {
        title: '任務佇列運行時',
        description: '後台任務佇列的實時負載，以及各獨立 worker 池的每實例併發設定。支援查看任務明細和安全管理，每 5 秒自動重新整理。',
        refresh: '重新整理',
        autoRefresh: '自動重新整理（每 5 秒）',
        loading: '載入中...',
        retry: '重試',
        unavailableTitle: '任務佇列不可用',
        unavailable: '當前部署未啟用 Redis / asynq 佇列（Lite 模式），無佇列可展示。',
        empty: '暫無佇列資料',
        detailsTitle: '佇列明細',
        detailsDescription: '各處理通道的實時負載與等待情況。「最終失敗」表示任務超過重試上限，已停止自動執行。',
        poolsTitle: 'Worker 池',
        poolsDescription: '各階段擁有保底容量，核心解析與內容富化還可借用共享彈性池。',
        perInstance: '卡片主值為叢集運行中/容量',
        poolConfigured: '單實例設定 {value}',
        poolInstances: '{value} 個實例',
        poolUtilization: '利用率 {value}%',
        queueCount: '{value} 個佇列',
        weightShort: '權重 {value}',
        footnote: '卡片主值為叢集運行中/實時容量；單實例設定修改後需重啟。共享彈性池只消費核心解析和內容富化佇列。',
        updatedAt: '更新於 {value}',
        models: {
          title: '模型併發佔用',
          description: '觀察後台任務實際進入模型服務時的併發佔用；上方是任務調度，這裡是模型服務限流，兩者處於不同處理階段。',
          scope: '佔用為叢集全局 · 等待為當前實例',
          disabled: '模型後台併發治理未啟用。可在全局設定中設定模型預設併發上限。',
          empty: '暫無模型呼叫資料；模型首次執行後台任務後會出現在這裡。',
          backgroundOnly: '僅統計後台任務，不包含交互式對話',
          status: {
            queued: '限流中',
            full: '已滿載'
          },
          columns: {
            model: '模型 ID',
            active: '呼叫中',
            waiting: '限流等待',
            usage: '併發用量'
          }
        },
        tasks: {
          title: '任務明細 · {queue}',
          description: '查看各狀態任務及其安全管理動作。定時、重試任務按最近執行時間優先，其餘狀態按時間倒序。',
          listTitle: '{state}任務',
          openAria: '查看{queue}中 {count} 個{state}任務',
          unavailable: '當前部署不支援查看任務明細',
          empty: '這個佇列當前沒有{state}任務',
          loadError: '獲取任務明細失敗',
          loadMore: '載入更多',
          loadedSummary: '已載入 {count} 條，繼續下滑或點擊載入',
          loadedAll: '已全部載入，共 {count} 條',
          loadingMore: '正在載入更多…',
          attempts: '執行 {current}/{max}',
          unknownTarget: '未識別到關聯物件',
          knowledgeBaseLabel: '知識庫 ID',
          knowledgeLabel: '文件 ID',
          taskIDLabel: '業務任務 ID',
          tenantLabel: '空間 ID',
          sourceLabel: '來源 ID',
          targetLabel: '目標 ID',
          sourceKBLabel: '來源知識庫',
          targetKBLabel: '目標知識庫',
          dataSourceLabel: '資料源 ID',
          syncLogLabel: '同步記錄 ID',
          knowledgeCountLabel: '文件數量',
          enqueuedAt: '入隊時間',
          startedAt: '開始時間',
          nextProcessAt: '下次執行',
          lastFailedAt: '最後失敗',
          completedAt: '完成時間',
          deadline: '執行截止',
          worker: '執行實例',
          health: '運行健康',
          orphaned: '執行實例已失聯，等待復原',
          cancel: '終止任務',
          cancelConfirm: '將透過業務取消流程終止該任務及同一文件的相關佇列任務。確認終止？',
          runNow: '立即執行',
          runNowConfirm: '該任務將立即進入待執行佇列，重試次數不會重設。確認繼續？',
          deleteRecord: '清除記錄',
          deleteConfirm: '僅從當前佇列移除這條失敗記錄，不會執行或完成原任務，歷史日誌仍會保留。確認清除？',
          purgeArchived: '清除全部失敗任務',
          purgeArchivedConfirm: '將從當前佇列免洗移除全部 {count} 條失敗記錄，僅清理佇列裡的失敗任務，不影響運行中或排隊中的任務，也不會回滾已失敗文件的業務狀態。確認清除？',
          purgeArchivedSuccess: '已清除 {count} 條失敗任務',
          purgeArchivedError: '清除失敗任務失敗',
          stateFilter: '按任務狀態篩選',
          actionError: {
            cancel: '終止任務失敗',
            run_now: '立即執行任務失敗',
            delete: '清除失敗記錄失敗'
          },
          actionSuccess: {
            cancel: '任務已終止',
            run_now: '任務已進入待執行佇列',
            delete: '失敗記錄已清除'
          },
          guides: {
            active: '運行中任務可查看執行實例、開始時間和截止時間。只有具備完整業務取消語義的任務才允許終止。',
            pending: '排隊任務尚未被 worker 領取。終止操作會同步更新業務狀態，而不是只刪除 Redis 記錄。',
            scheduled: '定時任務可提前立即執行；支援業務取消的文件任務也可以安全終止。',
            retry: '請結合最後錯誤、重試次數和下次執行時間判斷是否立即執行或終止。',
            archived: '請先修復失敗原因再立即執行。清除記錄不會完成原業務任務。',
            completed: '這裡只展示設定了結果保留時間的近期完成任務，不提供管理動作。'
          },
          states: {
            active: '運行中',
            pending: '排隊中',
            scheduled: '定時執行',
            retry: '重試中',
            archived: '最終失敗',
            completed: '已完成'
          },
          taskTypes: {
            documentProcess: '文件解析',
            manualProcess: '手工重新處理',
            temporaryDocumentProcess: '聊天附件解析',
            postProcess: '文件後處理',
            summary: '摘要生成',
            tableSummary: '表格摘要生成',
            question: '問題生成',
            multimodal: '圖片多模態處理',
            graph: '知識圖譜抽取',
            sync: '資料源同步',
            faqImport: 'FAQ 匯入',
            batchReparse: '批次重新解析',
            batchDelete: '批次刪除',
            move: '文件移動',
            indexDelete: '索引刪除',
            kbClone: '知識庫複製',
            kbDelete: '知識庫刪除',
            wikiIngest: 'Wiki 內容生成',
            wikiFinalize: 'Wiki 收尾處理'
          }
        },
        failedNotice: {
          title: '{count} 個任務待處理',
          description: '點擊下方表中紅色「最終失敗」數字查看原因，修復後可手動重試。'
        },
        status: {
          working: '處理中',
          waiting: '等待中',
          idle: '空閒',
          actionRequired: '需處理',
          retrying: '重試中',
          paused: '已暫停'
        },
        columns: {
          queue: '佇列',
          active: '運行中',
          pending: '排隊',
          scheduled: '定時',
          retry: '重試',
          archived: '最終失敗',
          completed: '已完成',
          latency: '最早等待',
          status: '狀態'
        },
        summary: {
          title: '運行概覽',
          active: '運行中',
          pending: '排隊中',
          retry: '重試中',
          archived: '最終失敗'
        },
        pools: {
          core: '核心解析',
          postprocess: '後處理編排',
          enrichment: '內容富化',
          maintenance: '維護與同步',
          shared: '共享彈性',
          wiki: 'Wiki 池'
        },
        poolDescriptions: {
          core: '文件解析與手工重解析的保底容量',
          postprocess: '解析完成後的收尾與富化扇出',
          enrichment: '摘要、圖片、圖譜與問題生成',
          maintenance: '資料源同步、批處理與刪除清理',
          shared: '由核心解析與內容富化按積壓借用',
          wiki: 'Wiki 內容生成與全局收尾'
        },
        queueNames: {
          default: '文件解析',
          chat_attachment: '對話附件解析',
          postprocess: '後處理編排',
          summary: '摘要生成',
          sync: '資料源同步',
          low: '維護與批處理',
          multimodal: '多模態處理',
          graph: '圖譜抽取',
          question: '問題生成',
          wiki: 'Wiki 處理'
        },
        queueDescriptions: {
          default: '文件解析、手工重解析',
          chat_attachment: '會話內上傳附件解析',
          postprocess: '解析收尾、富化扇出',
          summary: '文件摘要、表格摘要',
          sync: '手動與定時同步',
          low: 'FAQ 匯入、批次重解析、刪除清理',
          multimodal: '圖片 OCR、視覺描述',
          graph: '切塊圖譜抽取',
          question: '切塊問題生成',
          wiki: '內容生成、索引收尾'
        },
        errors: {
          generic: '獲取佇列狀態失敗'
        }
      },
      sections: {
        other: {
          title: '其他設定',
          description: '當前部署中未歸入標準分組的設定項。'
        },
        security: {
          title: '網路安全',
          description: '管理 SSRF 白名單，以及是否允許 Docker 沙箱（本機 docker.sock 等同宿主機 root）。'
        },
        runtime: {
          title: '運行與併發',
          description: '設定後台任務池與模型服務的併發容量。',
          restartHint: 'Worker 設定需重啟生效'
        },
        tenant: {
          title: '空間預設值',
          description: '設定新空間的初始配額與相容性行為，不會自動改寫已有空間。'
        },
        access: {
          title: '註冊與存取',
          description: '管理註冊方式、密碼要求及使用者建立空間的規則。'
        }
      },
      priorityHint: {
        disclosure: '設定來源與優先級',
        tier1: '在此頁面儲存過的項（帶「已覆蓋」徽章）— 始終以這裡的值為準，環境變數會被忽略。',
        tier2: '未在此處儲存過的項 — 如果環境變數裡有就用環境變數，否則用程式內建預設值。',
        tier3: '若想讓某項重新由環境變數控制，點擊該行的「重設」按鈕即可清除當前 UI 設定。'
      }
    },
    messages: {
      fetchFailed: '獲取系統資訊失敗',
      networkError: '網路錯誤，請稍後重試'
    }
  }
}
