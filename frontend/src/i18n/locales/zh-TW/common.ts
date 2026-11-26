export default {
  notifications: {
    pending: '待處理',
    history: '通知記錄',
    clearRead: '清理已讀',
    clearReadConfirm: '清理已讀通知？不會刪除執行記錄、對話或待處理事項。',
    handle: '去處理',
    review: '去審批',
    unreadCount: '{count} 則未讀',
    unread: '未讀',
    newItems: '有新通知，點擊重新整理',
    loadError: '通知載入失敗',
    awaitingApproval: '等待你確認操作',
    awaitingAuthorization: '等待你完成授權',
    awaitingReply: '等待你回覆',
    title: '通知',
    markAllRead: '全部已讀',
    pendingInvitations: '{count} 個待處理邀請',
    empty: '暫無通知',
    kinds: {
      automation_succeeded: '自動化已完成',
      automation_failed: '自動化失敗',
      automation_awaiting_approval: '自動化等待審批',
      automation_paused: '自動化已暫停'
    }
  },
  toolbox: {
    title: '工具箱',
    description: '管理智慧代理可用的技能、外部工具和瀏覽器連結。',
    unavailable: '當前空間暫無可用的工具。'
  },
  resourceSort: {
    title: '排序',
    updatedTime: '更新時間',
    updatedTimeDescription: '預設選項。',
    createdTime: '建立時間',
    createdTimeDescription: '查看最新或最早建立的內容',
    name: '名稱',
    nameDescription: '快速尋找明確知道名稱的內容',
    recentlyUpdated: '最近更新',
    earliestUpdated: '最早更新',
    recentlyCreated: '最近建立',
    earliestCreated: '最早建立',
    nameAscending: 'A–Z',
    nameDescending: 'Z–A'
  },
  mermaid: {
    diagram: '圖表',
    expand: '全螢幕查看',
    zoomIn: '放大',
    zoomOut: '縮小',
    reset: '重設',
    download: '下載圖片',
    close: '關閉',
    downloading: '下載中...'
  },
  tools: {
    multiKbSearch: '跨庫搜尋',
    searchKnowledge: '檢索知識庫',
    readDocument: '閱讀文件',
    listDocuments: '瀏覽文件清單',
    knowledgeSearch: '知識庫搜尋',
    grepChunks: '搜尋關鍵字',
    getChunkDetail: '獲取片段詳情',
    listKnowledgeChunks: '查看知識切塊',
    listKnowledgeBases: '列出知識庫',
    getDocumentInfo: '獲取文件資訊',
    queryKnowledgeGraph: '查詢知識圖譜',
    think: '深度思考',
    todoWrite: '制定計劃',
    askUser: '向使用者提問'
  },
  commandPalette: {
    placeholder: '搜尋知識庫、檔案、對話…',
    clearRecent: '清除',
    retrieval: '檢索參數',
    untitledSession: '未命名對話',
    errors: {
      knowledge: '知識庫搜尋失敗，請重試。',
      messages: '對話內容搜尋失敗，請重試。'
    },
    hotkey: {
      select: '選擇',
      enter: '打開',
      cmdNumber: '直接打開',
      cmdEnter: '發起對話',
      esc: '關閉'
    },
    empty: {
      noResults: '沒有找到比對結果',
      askAi: '直接向 AI 提問',
      adjustRetrieval: '調整檢索參數'
    },
    quick: {
      newChat: '新建對話',
      knowledgeBases: '打開知識庫',
      agents: '打開智慧代理',
      organizations: '打開共享空間',
      settings: '打開設定',
      productTour: '新手引導',
      assistant: '開啟 {name}'
    },
    match: {
      vector: '向量',
      keyword: '關鍵字'
    },
    group: {
      chunks: '知識庫檔案',
      messages: '對話訊息',
      kbs: '知識庫',
      agents: '智慧代理',
      sessionsByTitle: '對話（按標題）',
      commands: '命令',
      recent: '最近搜尋',
      quickActions: '快捷操作'
    },
    scope: {
      placeholder: '在本知識庫中搜尋…',
      remove: '移除範圍篩選（Backspace）'
    }
  },
  preview: {
    tab: '預覽',
    loading: '正在載入文件預覽...',
    loadFailed: '載入文件預覽失敗',
    retry: '重試',
    unsupported: '該檔案類型暫不支援線上預覽',
    unsupportedHint: '請下載檔案後使用本地應用查看',
    zoomIn: '放大',
    zoomOut: '縮小',
    fullscreen: '全螢幕預覽',
    exitFullscreen: '退出全螢幕',
    htmlRendered: '渲染預覽',
    htmlSource: '查看源碼',
    audioLoading: '載入音訊中…',
    audioNotSupported: '您的瀏覽器不支援音訊播放',
    videoNotSupported: '您的瀏覽器不支援影片播放'
  },
  promptTemplate: {
    noTemplates: '暫無模板',
    selectTemplate: '選擇模板',
    useTemplate: '使用模板',
    resetDefault: '復原預設',
    default: '預設',
    withKnowledgeBase: '知識庫',
    withWebSearch: '網路搜尋'
  },
  time: {
    today: '今天',
    yesterday: '昨天',
    last7Days: '近7天',
    last30Days: '近30天',
    lastYear: '近1年',
    earlier: '更早',
    pinned: '已置頂'
  },
  platform: {
    subtitle: '大型語言模型驅動的企業級知識框架',
    description: 'RAG 檢索、智慧代理推論、Wiki 知識庫，讓文件真正被理解和運用',
    rag: 'RAG 增強生成',
    agent: 'ReAct 智慧代理',
    wiki: 'Wiki 知識庫',
    hybridSearch: '混合檢索',
    multimodalParsing: '多模態文件解析',
    hybridSearchEngine: '混合檢索 + 知識圖譜',
    ragQandA: 'ReAct 智慧代理問答',
    independentTenant: '獨立空間',
    fullApiAccess: '完整 API 存取',
    knowledgeBaseManagement: '知識庫管理',
    carousel: {
      agenticRagTitle: 'Agentic RAG',
      agenticRagDesc: 'ReAct 推論 + 工具呼叫 + 多步思考',
      hybridSearchTitle: '混合檢索策略',
      hybridSearchDesc: 'BM25 + 向量 + 知識圖譜',
      wikiTitle: 'Wiki 知識庫',
      wikiDesc: '文件蒸餾為結構化互聯知識',
      smartDocRetrievalTitle: '智慧文件檢索',
      smartDocRetrievalDesc: 'PDF/Word/圖片多格式解析'
    }
  },
  font: {
    uiFont: '界面字型',
    uiFontDescription: '用於選單、正文、按鈕等界面大部分文字的字型',
    monoFont: '程式碼字型',
    monoFontDescription: '用於程式碼塊、終端命令、API 金鑰、檔案路徑等技術內容，每個字符等寬顯示，避免 0/O、1/l 混淆',
    selectFont: '選擇字型',
    sansPreview: '示例 Sample 字型 Font — Aa Gg Oo 0123',
    monoPreview: 'const msg = \'Hello\'; // 0O1l',
    fontSize: '字型大小',
    fontSizeDescription: '整體縮放界面（文字、圖示、間距等），切換後立即生效',
    size: {
      small: '小',
      normal: '正常',
      large: '大'
    },
    mono: {
      system: '系統預設',
      menlo: 'Menlo',
      monaco: 'Monaco',
      consolas: 'Consolas',
      cascadia: 'Cascadia Code',
      'dejavu-mono': 'DejaVu Sans Mono',
      'liberation-mono': 'Liberation Mono',
      monospace: '通用等寬'
    },
    sans: {
      system: '系統預設',
      pingfang: '蘋方 PingFang SC',
      georgia: 'Georgia 襯線',
      yahei: '微軟雅黑 Microsoft YaHei',
      times: 'Times New Roman 襯線',
      'noto-cjk': 'Noto Sans CJK',
      'dejavu-serif': 'DejaVu Serif 襯線',
      'sans-serif': '通用無襯線'
    }
  },
  theme: {
    theme: '主題模式',
    themeDescription: '選擇界面的顯示主題，支援跟隨系統自動切換',
    light: '淺色',
    dark: '深色',
    system: '跟隨系統',
    selectTheme: '選擇主題'
  },
  general: {
    title: '常規設定',
    allSettings: '全部設定',
    personalSettings: '個人設定',
    helpAndDocs: '幫助與文件',
    description: '設定語言、外觀等基礎選項',
    settings: '設定',
    close: '關閉設定'
  },
  language: {
    zhCN: '簡體中文',
    zhTW: '繁體中文',
    enUS: 'English',
    ruRU: 'Русский',
    viVN: 'Tiếng Việt',
    koKR: '한국어',
    jaJP: '日本語',
    selectLanguage: '選擇語言',
    language: '語言',
    languageDescription: '選擇界面顯示語言',
    languageSaved: '語言設定已儲存'
  },
  error: {
    networkError: '網路錯誤，請檢查您的網路連結',
    requestTimeout: '請求超時，檔案較大或網路較慢時請稍後重試',
    invalidCredentials: '使用者名或密碼錯誤',
    tokenRefreshFailed: 'Token重新整理失敗',
    pleaseRelogin: '請重新登入',
    fileSizeExceeded: '檔案大小不能超過 {size}M！',
    unsupportedFileType: '不支援的檔案類型！',
    invalidFileType: '檔案類型錯誤！',
    invalidImageLink: '無效的圖片連結',
    missingKbId: '缺少知識庫ID',
    tokenNotFound: '未找到登入權杖，請重新登入',
    streamFailed: '流式連結失敗',
    initialization: {
      checkFailed: '檢查失敗',
      testFailed: '測試失敗'
    },
    tenant: {
      listFailed: '獲取空間清單失敗',
      searchFailed: '搜尋空間失敗',
      getApiPrincipalConfigFailed: '獲取 API 使用者身份設定失敗',
      updateApiPrincipalConfigFailed: '更新 API 使用者身份設定失敗',
      createApiPrincipalTestTokenFailed: '生成 API 測試 Token 失敗',
      updateFailed: '更新空間資訊失敗',
      deleteFailed: '刪除空間失敗'
    },
    model: {
      createFailed: '建立模型失敗',
      getFailed: '獲取模型失敗',
      updateFailed: '更新模型失敗',
      deleteFailed: '刪除模型失敗'
    },
    auth: {
      loginFailed: '登入失敗',
      registerFailed: '註冊失敗',
      getUserFailed: '獲取使用者資訊失敗',
      getTenantFailed: '獲取空間資訊失敗',
      updatePreferencesFailed: '更新個性化設定失敗',
      refreshTokenFailed: '重新整理Token失敗',
      logoutFailed: '登出失敗',
      validateTokenFailed: 'Token驗證失敗'
    }
  },
  file: {
    upload: '上傳檔案'
  },
  common: {
    add: '新增',
    me: '我',
    confirm: '確認',
    cancel: '取消',
    unsavedChanges: {
      title: '有未儲存的更改',
      body: '關閉後當前修改將丟失，確定要關閉嗎？',
      discard: '放棄更改',
      keepEditing: '繼續編輯'
    },
    fullscreen: '全螢幕',
    exitFullscreen: '退出全螢幕',
    save: '儲存',
    delete: '刪除',
    edit: '編輯',
    copy: '複製',
    copied: '已複製',
    copySuccess: '已複製',
    default: '預設',
    create: '建立',
    download: '下載',
    refresh: '重新整理',
    loading: '載入中...',
    noData: '暫無資料',
    noMoreData: '已載入全部內容',
    loadMore: '載入更多',
    error: '錯誤',
    success: '成功',
    failed: '失敗',
    info: '資訊',
    selectAll: '全選',
    yes: '是',
    no: '否',
    close: '關閉',
    back: '返回',
    next: '下一步',
    finish: '完成',
    all: '全部',
    clear: '清空',
    website: '官方網站',
    clawhubSkill: 'Claw Skill',
    github: 'GitHub',
    githubStarTip: '在 GitHub 打開倉庫，若覺得有用歡迎點個 Star',
    on: '開啟',
    off: '關閉',
    confirmDelete: '確認刪除',
    createSuccess: '已建立',
    deleteSuccess: '已刪除',
    deleteFailed: '刪除失敗',
    updateSuccess: '已更新',
    saveSuccess: '已儲存',
    saveFailed: '儲存失敗',
    operationFailed: '操作失敗',
    file: '檔案',
    skill: '技能',
    knowledgeBase: '知識庫',
    noResult: '無結果',
    remove: '移除',
    avatar: '頭像',
    defaultUser: '使用者',
    copyFailed: '複製失敗',
    retry: '重試',
    expand: '展開',
    collapse: '收起'
  },
  resourceOrigin: {
    mine: '我建立',
    mineTooltip: '由當前使用者建立',
    tenant: '本空間',
    tenantTooltip: '本空間其他成員建立',
    tenantTooltipWithCreator: '由 {creator} 建立',
    space: '共享空間',
    spaceTooltip: '來自共享空間「{space}」',
    spaceTooltipWithTenant: '來自共享空間「{space}」· 源空間 {tenant}',
    shared: '外部共享',
    sharedTooltip: '透過共享空間從其他空間存取'
  },
  listSpaceSidebar: {
    all: '全部',
    workspace: '本空間',
    spaces: '共享給我',
    favorites: '收藏',
    recents: '最近'
  },
  batchManage: {
    selectAll: '全選',
    cancel: '取消',
    delete: '刪除對話',
    deleteConfirmTitle: '刪除對話',
    deleteConfirmBody: '確定要刪除選中的 {count} 條對話嗎？刪除後無法復原。',
    deleteAllConfirmBody: '確定要刪除所有對話嗎？此操作無法復原。',
    deleteSuccess: '已刪除',
    deleteFailed: '刪除失敗，請稍後再試'
  },
  contextualGuide: {
    stepOf: '{current} / {total}',
    skip: '跳過',
    prev: '上一步',
    next: '下一步',
    done: '知道了',
    interactHint: '請直接點擊標示區域繼續',
    chat: {
      steps: {
        done: {
          title: '開始探索吧',
          desc: '試試提一個與已上傳文件相關的問題，體驗帶引用的精準回答。'
        },
        send: {
          title: '傳送開始對話',
          desc: '傳送後將建立新會話，AI 會結合知識庫內容作答，並標註引用片段。'
        },
        input: {
          title: '輸入你的問題',
          desc: '直接描述你想了解的內容；也可點擊上方推薦問題快速開始。'
        },
        kb: {
          title: '選擇知識範圍',
          desc: '點擊 {\'@\'} 可指定一個或多個知識庫/檔案，僅基於選中內容回答；不選則按當前智慧代理設定檢索。'
        }
      }
    },
    kbDetail: {
      steps: {
        done: {
          title: '解析完成後即可使用',
          desc: '文件解析入庫後，可在對話中 {\'@\'} 本知識庫提問，回答會附帶引用來源。'
        },
        upload: {
          title: '新增文件',
          desc: '點擊此處上傳檔案、資料夾，或匯入網頁與線上編輯內容。'
        },
        intro: {
          title: '知識庫還是空的',
          desc: '新增第一份資料後，才能基於它進行檢索與對話。支援拖曳上傳多種文件格式。'
        }
      }
    },
    agentCreate: {
      steps: {
        submit: {
          title: '儲存智慧代理',
          desc: '確認設定後點擊標示的「確定」完成建立，即可在對話中選擇該智慧代理。'
        },
        navTools: {
          title: '工具與 MCP（可選）',
          desc: '智慧推論模式下可勾選內建工具、MCP 服務等，擴展搜尋、計算等能力。'
        },
        multimodal: {
          title: '啟用附件上傳',
          desc: '可分別設定圖片、音訊上傳及附件解析策略；啟用圖片上傳時需在下方選擇 VLM 模型。'
        },
        navMultimodal: {
          title: '附件上傳（可選）',
          desc: '開啟後，對話中可上傳圖片、文件、音訊等附件；圖片理解需先在系統設定中設定 VLLM 模型。'
        },
        navWebsearch: {
          title: '聯網搜尋（可選）',
          desc: '設定是否允許智慧代理呼叫外部搜尋引擎補充實時資訊。'
        },
        knowledge: {
          title: '知識庫範圍',
          desc: '「全部」適合通用助手；「指定」可限定專業領域；「不關聯」則僅依賴模型自身能力或聯網搜尋。'
        },
        navKnowledge: {
          title: '關聯知識庫',
          desc: '決定智慧代理可檢索哪些知識。預設「全部知識庫」，也可改為指定庫或暫不關聯。'
        },
        model: {
          title: '選擇模型',
          desc: '從下拉清單選擇已設定的對話模型；沒有合適模型時請先到系統設定新增。'
        },
        navModel: {
          title: '綁定對話模型',
          desc: '每個智慧代理必須指定一個 KnowledgeQA 模型作為推論引擎。'
        },
        name: {
          title: '命名與描述',
          desc: '取一個易識別的名稱。智慧推論模式下系統可能已預填預設名稱，可按需修改。'
        },
        agentType: {
          title: '選擇智慧代理類型',
          desc: '預設類型會自動填充系統提示語、推薦工具與知識庫範圍（如 Wiki 建構、資料分析等）。可按情境切換，名稱與描述會隨之更新。'
        },
        mode: {
          title: '選擇運行模式',
          desc: '「普通模式」適合固定流程的快速問答；「智慧推論」可呼叫工具、多步思考，適合複雜任務。'
        }
      }
    },
    agentList: {
      steps: {
        create: {
          title: '建立你的智慧代理',
          desc: '智慧代理把模型、知識庫、工具與提示語組合成可復用的對話助手。點擊下方標示的「建立智慧代理」開始設定。'
        }
      }
    },
    kbCreate: {
      steps: {
        submit: {
          title: '建立知識庫',
          desc: '點擊「知道了」結束引導，無需現在建立知識庫。準備好後，填寫名稱、確認類型與模型，再點擊「建立」。'
        },
        faq: {
          title: 'FAQ 索引方式',
          desc: '設定問答對的索引模式。建立後可在本頁繼續新增 FAQ 條目。'
        },
        multimodalVllm: {
          title: '選擇 VLM 模型',
          desc: '開啟多模態後需指定 VLM（視覺語言模型）。若清單為空，請先在系統設定中新增 VLLM 類型模型。'
        },
        multimodalToggle: {
          title: '開啟多模態解析',
          desc: '啟用後，上傳的圖片類文件將使用視覺語言模型提取內容，便於檢索與問答。'
        },
        navMultimodal: {
          title: '多模態 / 圖片理解（可選）',
          desc: '若文件含大量圖表、掃描件或需理解圖片內容，可在此開啟多模態並選擇 VLM 模型。'
        },
        storage: {
          title: '存儲引擎（可選）',
          desc: '原始檔案存放位置（本地或物件存儲）。預設跟隨空間設定即可。'
        },
        chunking: {
          title: '切塊策略（可選）',
          desc: '決定文件如何切分為檢索片段。預設切塊大小已針對 RAG 最佳化，一般無需修改。'
        },
        parser: {
          title: '解析引擎（可選）',
          desc: '控制 PDF、Office 等檔案的解析方式。預設設定適用於大多數情境，有 OCR 需求時可在此調整。'
        },
        embedding: {
          title: 'Embedding 模型',
          desc: '將文字轉為向量以支援語義檢索。與上方索引策略中的「向量/關鍵字檢索」配合使用。'
        },
        llm: {
          title: '對話 / 摘要模型',
          desc: '用於文件摘要、問答生成等。若清單為空，請透過下拉選單前往系統設定新增模型。'
        },
        navModels: {
          title: '設定模型（必填）',
          desc: '知識庫必須綁定對話模型；開啟檢索時還需 Embedding 模型。點擊左側「模型設定」進入。'
        },
        indexing: {
          title: '選擇索引能力',
          desc: '預設已開啟向量與關鍵字檢索，可按需開啟 Wiki 結構化索引或知識圖譜。至少保留一種檢索方式。'
        },
        name: {
          title: '填寫名稱',
          desc: '取一個便於識別的名稱，例如「產品手冊」或「客服 FAQ」。描述可選填。'
        },
        type: {
          title: '選擇知識庫類型',
          desc: '文件庫適合上傳 PDF、Word 等檔案；FAQ 庫適合問答對。建立後類型不可更改，請按需選擇。'
        }
      }
    },
    tenantModels: {
      needChatModelFirst: '請先新增對話模型（KnowledgeQA），再建立智慧代理。',
      stepsAgent: {
        done: {
          title: '然後建立智慧代理',
          desc: '儲存並關閉設定後，點擊「建立智慧代理」，嚮導會帶你設定模式、知識庫與附件上傳等選項。'
        },
        addModel: {
          title: '新增對話模型',
          desc: '點擊「新增模型」，選擇 KnowledgeQA 類型並填寫接入資訊。'
        },
        intro: {
          title: '需要先設定對話模型',
          desc: '建立智慧代理至少需要一種 KnowledgeQA（對話）模型。請先在系統設定中新增；Embedding 模型僅建立知識庫時需要。'
        }
      },
      steps: {
        done: {
          title: '新增完成後繼續',
          desc: '模型儲存後關閉設定頁，即可點擊「新建知識庫」；建立嚮導會引導你完成類型、索引與模型綁定。'
        },
        addModel: {
          title: '新增模型',
          desc: '點擊「新增模型」，分別設定 KnowledgeQA（對話）與 Embedding 類型。Lite 使用者可使用 Ollama 拉取本地模型。'
        },
        intro: {
          title: '需要先設定模型',
          desc: '建立文件知識庫至少需要：一個對話模型（用於摘要與問答）和一個 Embedding 模型（用於向量檢索）。請先在系統設定中新增。'
        }
      }
    },
    kbList: {
      steps: {
        create: {
          title: '建立第一個知識庫',
          desc: '知識庫用來存放文件與 FAQ。點擊下方標示的「新建知識庫」按鈕，我們會帶你完成表單填寫。'
        }
      }
    }
  },
  assistantEntryGuide: {
    stepOf: 'v0.9.0 新功能',
    skip: '知道了',
    interactHint: '點擊反白的入口，開啟助手試試。'
  },
  newUserGuide: {
    stepOf: '{current} / {total}',
    skip: '跳過引導',
    prev: '上一步',
    next: '下一步',
    done: '完成',
    reopen: '新手引導',
    steps: {
      assistant: {
        title: '認識 WeKnora 助手',
        desc: '可以詢問 WeKnora 的版本、功能與設定，也可以結合目前頁面，以你的身分建立智慧代理、設定定時任務或整理知識。'
      },
      done: {
        title: '一切就緒',
        desc: '你已經了解了核心功能，現在就開始建構你的知識助手吧！隨時可點擊選單頂部暱稱旁的幫助按鈕重新查看引導。'
      },
      models: {
        title: '設定你的模型',
        desc: '這是關鍵一步：在模型管理中新增對話、向量等模型，知識庫與對話功能才能正常工作。點擊「新增模型」即可開始設定。'
      },
      settings: {
        title: '帳戶與設定入口',
        desc: '點開這裡可以管理帳戶、成員與系統設定。需要再次查看本引導時，可點擊選單頂部暱稱旁的幫助按鈕重新打開。'
      },
      agents: {
        title: '打造專屬智慧代理',
        desc: '把知識庫、提示語與工具組合成可復用的智慧代理，沉澱你的專業能力。'
      },
      chat: {
        title: '發起智慧對話',
        desc: '基於知識庫內容向 AI 提問，獲得帶引用來源的精準回答。點擊這裡開始新對話。'
      },
      knowledge: {
        title: '建立你的知識庫',
        desc: '知識庫是一切的起點：上傳文件、網頁或 FAQ，WeKnora 會自動解析並建立索引。點擊這裡進入知識庫。'
      },
      welcome: {
        title: '歡迎使用 WeKnora',
        desc: '只需幾步，帶你快速了解知識庫、對話與智慧代理的核心用法。點擊「下一步」開始。'
      }
    }
  },
  menu: {
    sessionInProgress: '會話進行中',
    sessionUnread: '有新回覆',
    knowledgeBase: '知識庫',
    agents: '智慧代理',
    automations: '自動化',
    artifacts: '產物',
    organizations: '共享空間',
    newChat: '新對話',
    settings: '系統設定',
    logout: '登出',
    clearMessages: '清空訊息',
    clearMessagesSuccess: '訊息已清空',
    clearMessagesFailed: '清空訊息失敗，請稍後再試',
    renameSession: '修改標題',
    renameSessionSuccess: '標題已更新',
    renameSessionFailed: '修改標題失敗，請稍後再試',
    batchManage: '批次管理',
    newSession: '新會話',
    pin: '置頂',
    unpin: '取消置頂',
    pinFailed: '置頂失敗，請稍後再試',
    unpinFailed: '取消置頂失敗，請稍後再試',
    search: '搜尋',
    collapseSidebar: '收起側邊欄',
    expandSidebar: '展開側邊欄',
    logoutSuccess: '已登出',
    myChats: '我的對話',
    apiChats: 'API 會話',
    noSessions: '暫無對話'
  }
}
