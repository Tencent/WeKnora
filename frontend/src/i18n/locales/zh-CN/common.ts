export default {
  notifications: {
    pending: '待处理',
    history: '通知记录',
    clearRead: '清理已读',
    clearReadConfirm: '清理已读通知？不会删除运行记录、对话或待处理事项。',
    handle: '去处理',
    review: '去审批',
    unreadCount: '{count} 条未读',
    unread: '未读',
    newItems: '有新通知，点击刷新',
    loadError: '通知加载失败',
    awaitingApproval: '等待你确认操作',
    awaitingAuthorization: '等待你完成授权',
    awaitingReply: '等待你回复',
    title: '通知',
    markAllRead: '全部已读',
    pendingInvitations: '{count} 个待处理邀请',
    empty: '暂无通知',
    kinds: {
      automation_succeeded: '自动化已完成',
      automation_failed: '自动化失败',
      automation_awaiting_approval: '自动化等待审批',
      automation_paused: '自动化已暂停'
    }
  },
  toolbox: {
    title: '工具箱',
    description: '管理智能体可用的技能、外部工具和浏览器连接。',
    unavailable: '当前空间暂无可用的工具。'
  },
  resourceSort: {
    title: '排序',
    updatedTime: '更新时间',
    updatedTimeDescription: '默认选项。',
    createdTime: '创建时间',
    createdTimeDescription: '查看最新或最早创建的内容',
    name: '名称',
    nameDescription: '快速寻找明确知道名称的内容',
    recentlyUpdated: '最近更新',
    earliestUpdated: '最早更新',
    recentlyCreated: '最近创建',
    earliestCreated: '最早创建',
    nameAscending: 'A–Z',
    nameDescending: 'Z–A'
  },
  mermaid: {
    diagram: '图表',
    expand: '全屏查看',
    zoomIn: '放大',
    zoomOut: '缩小',
    reset: '重置',
    download: '下载图片',
    close: '关闭',
    downloading: '下载中...'
  },
  tools: {
    multiKbSearch: '跨库搜索',
    searchKnowledge: '检索知识库',
    readDocument: '阅读文档',
    listDocuments: '浏览文档列表',
    knowledgeSearch: '知识库搜索',
    grepChunks: '搜索关键词',
    getChunkDetail: '获取片段详情',
    listKnowledgeChunks: '查看知识分块',
    listKnowledgeBases: '列出知识库',
    getDocumentInfo: '获取文档信息',
    queryKnowledgeGraph: '查询知识图谱',
    think: '深度思考',
    todoWrite: '制定计划',
    askUser: '向用户提问'
  },
  commandPalette: {
    placeholder: '搜索知识库、文件、对话…',
    clearRecent: '清除',
    retrieval: '检索参数',
    untitledSession: '未命名对话',
    errors: {
      knowledge: '知识库搜索失败，请重试。',
      messages: '对话内容搜索失败，请重试。'
    },
    hotkey: {
      select: '选择',
      enter: '打开',
      cmdNumber: '直接打开',
      cmdEnter: '发起对话',
      esc: '关闭'
    },
    empty: {
      noResults: '没有找到匹配结果',
      askAi: '直接向 AI 提问',
      adjustRetrieval: '调整检索参数'
    },
    quick: {
      newChat: '新建对话',
      knowledgeBases: '打开知识库',
      agents: '打开智能体',
      organizations: '打开共享空间',
      settings: '打开设置',
      productTour: '新手引导',
      assistant: '打开 {name}'
    },
    match: {
      vector: '向量',
      keyword: '关键字'
    },
    group: {
      chunks: '知识库文件',
      messages: '对话消息',
      kbs: '知识库',
      agents: '智能体',
      sessionsByTitle: '对话（按标题）',
      commands: '命令',
      recent: '最近搜索',
      quickActions: '快捷操作'
    },
    scope: {
      placeholder: '在本知识库中搜索…',
      remove: '移除范围过滤（Backspace）'
    }
  },
  preview: {
    tab: '预览',
    loading: '正在加载文档预览...',
    loadFailed: '加载文档预览失败',
    retry: '重试',
    unsupported: '该文件类型暂不支持在线预览',
    unsupportedHint: '请下载文件后使用本地应用查看',
    zoomIn: '放大',
    zoomOut: '缩小',
    fullscreen: '全屏预览',
    exitFullscreen: '退出全屏',
    htmlRendered: '渲染预览',
    htmlSource: '查看源码',
    audioLoading: '加载音频中…',
    audioNotSupported: '您的浏览器不支持音频播放',
    videoNotSupported: '您的浏览器不支持视频播放'
  },
  promptTemplate: {
    noTemplates: '暂无模板',
    selectTemplate: '选择模板',
    useTemplate: '使用模板',
    resetDefault: '恢复默认',
    default: '默认',
    withKnowledgeBase: '知识库',
    withWebSearch: '网络搜索'
  },
  time: {
    today: '今天',
    yesterday: '昨天',
    last7Days: '近7天',
    last30Days: '近30天',
    lastYear: '近1年',
    earlier: '更早',
    pinned: '已置顶'
  },
  platform: {
    subtitle: '大模型驱动的企业级知识框架',
    description: 'RAG 检索、智能体推理、Wiki 知识库，让文档真正被理解和运用',
    rag: 'RAG 增强生成',
    agent: 'ReAct 智能体',
    wiki: 'Wiki 知识库',
    hybridSearch: '混合检索',
    multimodalParsing: '多模态文档解析',
    hybridSearchEngine: '混合检索 + 知识图谱',
    ragQandA: 'ReAct 智能体问答',
    independentTenant: '独立空间',
    fullApiAccess: '完整 API 访问',
    knowledgeBaseManagement: '知识库管理',
    carousel: {
      agenticRagTitle: 'Agentic RAG',
      agenticRagDesc: 'ReAct 推理 + 工具调用 + 多步思考',
      hybridSearchTitle: '混合检索策略',
      hybridSearchDesc: 'BM25 + 向量 + 知识图谱',
      wikiTitle: 'Wiki 知识库',
      wikiDesc: '文档蒸馏为结构化互联知识',
      smartDocRetrievalTitle: '智能文档检索',
      smartDocRetrievalDesc: 'PDF/Word/图片多格式解析'
    }
  },
  font: {
    uiFont: '界面字体',
    uiFontDescription: '用于菜单、正文、按钮等界面大部分文字的字体',
    monoFont: '代码字体',
    monoFontDescription: '用于代码块、终端命令、API 密钥、文件路径等技术内容，每个字符等宽显示，避免 0/O、1/l 混淆',
    selectFont: '选择字体',
    sansPreview: '示例 Sample 字体 Font — Aa Gg Oo 0123',
    monoPreview: 'const msg = \'Hello\'; // 0O1l',
    fontSize: '字体大小',
    fontSizeDescription: '整体缩放界面（文字、图标、间距等），切换后立即生效',
    size: {
      small: '小',
      normal: '正常',
      large: '大'
    },
    mono: {
      system: '系统默认',
      menlo: 'Menlo',
      monaco: 'Monaco',
      consolas: 'Consolas',
      cascadia: 'Cascadia Code',
      'dejavu-mono': 'DejaVu Sans Mono',
      'liberation-mono': 'Liberation Mono',
      monospace: '通用等宽'
    },
    sans: {
      system: '系统默认',
      pingfang: '苹方 PingFang SC',
      georgia: 'Georgia 衬线',
      yahei: '微软雅黑 Microsoft YaHei',
      times: 'Times New Roman 衬线',
      'noto-cjk': 'Noto Sans CJK',
      'dejavu-serif': 'DejaVu Serif 衬线',
      'sans-serif': '通用无衬线'
    }
  },
  theme: {
    theme: '主题模式',
    themeDescription: '选择界面的显示主题，支持跟随系统自动切换',
    light: '浅色',
    dark: '深色',
    system: '跟随系统',
    selectTheme: '选择主题'
  },
  general: {
    title: '常规设置',
    allSettings: '全部设置',
    personalSettings: '个人设置',
    helpAndDocs: '帮助与文档',
    description: '配置语言、外观等基础选项',
    settings: '设置',
    close: '关闭设置'
  },
  language: {
    zhCN: '简体中文',
    zhTW: '繁體中文',
    enUS: 'English',
    ruRU: 'Русский',
    koKR: '한국어',
    jaJP: '日本語',
    selectLanguage: '选择语言',
    language: '语言',
    languageDescription: '选择界面显示语言',
    languageSaved: '语言设置已保存'
  },
  error: {
    networkError: '网络错误，请检查您的网络连接',
    requestTimeout: '请求超时，文件较大或网络较慢时请稍后重试',
    invalidCredentials: '用户名或密码错误',
    tokenRefreshFailed: 'Token刷新失败',
    pleaseRelogin: '请重新登录',
    fileSizeExceeded: '文件大小不能超过 {size}M！',
    unsupportedFileType: '不支持的文件类型！',
    invalidFileType: '文件类型错误！',
    invalidImageLink: '无效的图片链接',
    missingKbId: '缺少知识库ID',
    tokenNotFound: '未找到登录令牌，请重新登录',
    streamFailed: '流式连接失败',
    initialization: {
      checkFailed: '检查失败',
      testFailed: '测试失败'
    },
    tenant: {
      listFailed: '获取空间列表失败',
      searchFailed: '搜索空间失败',
      getApiPrincipalConfigFailed: '获取 API 用户身份配置失败',
      updateApiPrincipalConfigFailed: '更新 API 用户身份配置失败',
      createApiPrincipalTestTokenFailed: '生成 API 测试 Token 失败',
      updateFailed: '更新空间信息失败',
      deleteFailed: '删除空间失败'
    },
    model: {
      createFailed: '创建模型失败',
      getFailed: '获取模型失败',
      updateFailed: '更新模型失败',
      deleteFailed: '删除模型失败'
    },
    auth: {
      loginFailed: '登录失败',
      registerFailed: '注册失败',
      getUserFailed: '获取用户信息失败',
      getTenantFailed: '获取空间信息失败',
      updatePreferencesFailed: '更新个性化设置失败',
      refreshTokenFailed: '刷新Token失败',
      logoutFailed: '登出失败',
      validateTokenFailed: 'Token验证失败'
    }
  },
  file: {
    upload: '上传文件'
  },
  common: {
    add: '添加',
    me: '我',
    confirm: '确认',
    cancel: '取消',
    unsavedChanges: {
      title: '有未保存的更改',
      body: '关闭后当前修改将丢失，确定要关闭吗？',
      discard: '放弃更改',
      keepEditing: '继续编辑'
    },
    fullscreen: '全屏',
    exitFullscreen: '退出全屏',
    save: '保存',
    delete: '删除',
    edit: '编辑',
    copy: '复制',
    copied: '已复制',
    copySuccess: '已复制',
    default: '默认',
    create: '创建',
    download: '下载',
    refresh: '刷新',
    loading: '加载中...',
    noData: '暂无数据',
    noMoreData: '已加载全部内容',
    loadMore: '加载更多',
    error: '错误',
    success: '成功',
    failed: '失败',
    info: '信息',
    selectAll: '全选',
    yes: '是',
    no: '否',
    close: '关闭',
    back: '返回',
    next: '下一步',
    finish: '完成',
    all: '全部',
    clear: '清空',
    website: '官方网站',
    clawhubSkill: 'Claw Skill',
    github: 'GitHub',
    githubStarTip: '在 GitHub 打开仓库，若觉得有用欢迎点个 Star',
    on: '开启',
    off: '关闭',
    confirmDelete: '确认删除',
    createSuccess: '已创建',
    deleteSuccess: '已删除',
    deleteFailed: '删除失败',
    updateSuccess: '已更新',
    saveSuccess: '已保存',
    saveFailed: '保存失败',
    operationFailed: '操作失败',
    file: '文件',
    skill: '技能',
    knowledgeBase: '知识库',
    noResult: '无结果',
    remove: '移除',
    avatar: '头像',
    defaultUser: '用户',
    copyFailed: '复制失败',
    retry: '重试',
    expand: '展开',
    collapse: '收起'
  },
  resourceOrigin: {
    mine: '我创建',
    mineTooltip: '由当前用户创建',
    tenant: '本空间',
    tenantTooltip: '本空间其他成员创建',
    tenantTooltipWithCreator: '由 {creator} 创建',
    space: '共享空间',
    spaceTooltip: '来自共享空间「{space}」',
    spaceTooltipWithTenant: '来自共享空间「{space}」· 源空间 {tenant}',
    shared: '外部共享',
    sharedTooltip: '通过共享空间从其他空间访问'
  },
  listSpaceSidebar: {
    all: '全部',
    workspace: '本空间',
    spaces: '共享给我',
    favorites: '收藏',
    recents: '最近'
  },
  batchManage: {
    selectAll: '全选',
    cancel: '取消',
    delete: '删除对话',
    deleteConfirmTitle: '删除对话',
    deleteConfirmBody: '确定要删除选中的 {count} 条对话吗？删除后无法恢复。',
    deleteAllConfirmBody: '确定要删除所有对话吗？此操作无法恢复。',
    deleteSuccess: '已删除',
    deleteFailed: '删除失败，请稍后再试'
  },
  contextualGuide: {
    stepOf: '{current} / {total}',
    skip: '跳过',
    prev: '上一步',
    next: '下一步',
    done: '知道了',
    interactHint: '请直接点击高亮区域继续',
    chat: {
      steps: {
        done: {
          title: '开始探索吧',
          desc: '试试提一个与已上传文档相关的问题，体验带引用的精准回答。'
        },
        send: {
          title: '发送开始对话',
          desc: '发送后将创建新会话，AI 会结合知识库内容作答，并标注引用片段。'
        },
        input: {
          title: '输入你的问题',
          desc: '直接描述你想了解的内容；也可点击上方推荐问题快速开始。'
        },
        kb: {
          title: '选择知识范围',
          desc: '点击 {\'@\'} 可指定一个或多个知识库/文件，仅基于选中内容回答；不选则按当前智能体配置检索。'
        }
      }
    },
    kbDetail: {
      steps: {
        done: {
          title: '解析完成后即可使用',
          desc: '文档解析入库后，可在对话中 {\'@\'} 本知识库提问，回答会附带引用来源。'
        },
        upload: {
          title: '添加文档',
          desc: '点击此处上传文件、文件夹，或导入网页与在线编辑内容。'
        },
        intro: {
          title: '知识库还是空的',
          desc: '添加第一份资料后，才能基于它进行检索与对话。支持拖拽上传多种文档格式。'
        }
      }
    },
    agentCreate: {
      steps: {
        submit: {
          title: '保存智能体',
          desc: '确认配置后点击高亮的「确定」完成创建，即可在对话中选择该智能体。'
        },
        navTools: {
          title: '工具与 MCP（可选）',
          desc: '智能推理模式下可勾选内置工具、MCP 服务等，扩展搜索、计算等能力。'
        },
        multimodal: {
          title: '启用附件上传',
          desc: '可分别配置图片、音频上传及附件解析策略；启用图片上传时需在下方选择 VLM 模型。'
        },
        navMultimodal: {
          title: '附件上传（可选）',
          desc: '开启后，对话中可上传图片、文档、音频等附件；图片理解需先在系统设置中配置 VLLM 模型。'
        },
        navWebsearch: {
          title: '联网搜索（可选）',
          desc: '配置是否允许智能体调用外部搜索引擎补充实时信息。'
        },
        knowledge: {
          title: '知识库范围',
          desc: '「全部」适合通用助手；「指定」可限定专业领域；「不关联」则仅依赖模型自身能力或联网搜索。'
        },
        navKnowledge: {
          title: '关联知识库',
          desc: '决定智能体可检索哪些知识。默认「全部知识库」，也可改为指定库或暂不关联。'
        },
        model: {
          title: '选择模型',
          desc: '从下拉列表选择已配置的对话模型；没有合适模型时请先到系统设置添加。'
        },
        navModel: {
          title: '绑定对话模型',
          desc: '每个智能体必须指定一个 KnowledgeQA 模型作为推理引擎。'
        },
        name: {
          title: '命名与描述',
          desc: '取一个易识别的名称。智能推理模式下系统可能已预填默认名称，可按需修改。'
        },
        agentType: {
          title: '选择智能体类型',
          desc: '预设类型会自动填充系统提示词、推荐工具与知识库范围（如 Wiki 构建、数据分析等）。可按场景切换，名称与描述会随之更新。'
        },
        mode: {
          title: '选择运行模式',
          desc: '「普通模式」适合固定流程的快速问答；「智能推理」可调用工具、多步思考，适合复杂任务。'
        }
      }
    },
    agentList: {
      steps: {
        create: {
          title: '创建你的智能体',
          desc: '智能体把模型、知识库、工具与提示词组合成可复用的对话助手。点击下方高亮的「创建智能体」开始配置。'
        }
      }
    },
    kbCreate: {
      steps: {
        submit: {
          title: '创建知识库',
          desc: '点击「知道了」结束引导，无需现在创建知识库。准备好后，填写名称、确认类型与模型，再点击「创建」。'
        },
        faq: {
          title: 'FAQ 索引方式',
          desc: '配置问答对的索引模式。创建后可在本页继续添加 FAQ 条目。'
        },
        multimodalVllm: {
          title: '选择 VLM 模型',
          desc: '开启多模态后需指定 VLM（视觉语言模型）。若列表为空，请先在系统设置中添加 VLLM 类型模型。'
        },
        multimodalToggle: {
          title: '开启多模态解析',
          desc: '启用后，上传的图片类文档将使用视觉语言模型提取内容，便于检索与问答。'
        },
        navMultimodal: {
          title: '多模态 / 图片理解（可选）',
          desc: '若文档含大量图表、扫描件或需理解图片内容，可在此开启多模态并选择 VLM 模型。'
        },
        storage: {
          title: '存储引擎（可选）',
          desc: '原始文件存放位置（本地或对象存储）。默认跟随空间设置即可。'
        },
        chunking: {
          title: '分块策略（可选）',
          desc: '决定文档如何切分为检索片段。默认分块大小已针对 RAG 优化，一般无需修改。'
        },
        parser: {
          title: '解析引擎（可选）',
          desc: '控制 PDF、Office 等文件的解析方式。默认配置适用于大多数场景，有 OCR 需求时可在此调整。'
        },
        embedding: {
          title: 'Embedding 模型',
          desc: '将文本转为向量以支持语义检索。与上方索引策略中的「向量/关键词检索」配合使用。'
        },
        llm: {
          title: '对话 / 摘要模型',
          desc: '用于文档摘要、问答生成等。若列表为空，请通过下拉菜单前往系统设置添加模型。'
        },
        navModels: {
          title: '配置模型（必填）',
          desc: '知识库必须绑定对话模型；开启检索时还需 Embedding 模型。点击左侧「模型配置」进入。'
        },
        indexing: {
          title: '选择索引能力',
          desc: '默认已开启向量与关键词检索，可按需开启 Wiki 结构化索引或知识图谱。至少保留一种检索方式。'
        },
        name: {
          title: '填写名称',
          desc: '取一个便于识别的名称，例如「产品手册」或「客服 FAQ」。描述可选填。'
        },
        type: {
          title: '选择知识库类型',
          desc: '文档库适合上传 PDF、Word 等文件；FAQ 库适合问答对。创建后类型不可更改，请按需选择。'
        }
      }
    },
    tenantModels: {
      needChatModelFirst: '请先添加对话模型（KnowledgeQA），再创建智能体。',
      stepsAgent: {
        done: {
          title: '然后创建智能体',
          desc: '保存并关闭设置后，点击「创建智能体」，向导会带你配置模式、知识库与附件上传等选项。'
        },
        addModel: {
          title: '添加对话模型',
          desc: '点击「添加模型」，选择 KnowledgeQA 类型并填写接入信息。'
        },
        intro: {
          title: '需要先配置对话模型',
          desc: '创建智能体至少需要一种 KnowledgeQA（对话）模型。请先在系统设置中添加；Embedding 模型仅创建知识库时需要。'
        }
      },
      steps: {
        done: {
          title: '添加完成后继续',
          desc: '模型保存后关闭设置页，即可点击「新建知识库」；创建向导会引导你完成类型、索引与模型绑定。'
        },
        addModel: {
          title: '添加模型',
          desc: '点击「添加模型」，分别配置 KnowledgeQA（对话）与 Embedding 类型。Lite 用户可使用 Ollama 拉取本地模型。'
        },
        intro: {
          title: '需要先配置模型',
          desc: '创建文档知识库至少需要：一个对话模型（用于摘要与问答）和一个 Embedding 模型（用于向量检索）。请先在系统设置中添加。'
        }
      }
    },
    kbList: {
      steps: {
        create: {
          title: '创建第一个知识库',
          desc: '知识库用来存放文档与 FAQ。点击下方高亮的「新建知识库」按钮，我们会带你完成表单填写。'
        }
      }
    }
  },
  assistantEntryGuide: {
    stepOf: 'v0.9.0 新功能',
    skip: '知道了',
    interactHint: '点击高亮入口，打开助手试试。'
  },
  newUserGuide: {
    stepOf: '{current} / {total}',
    skip: '跳过引导',
    prev: '上一步',
    next: '下一步',
    done: '完成',
    reopen: '新手引导',
    steps: {
      assistant: {
        title: '认识 WeKnora 助手',
        desc: '可以问 WeKnora 的版本、功能和配置，也可以结合当前页面，以你的身份创建智能体、设置定时任务或整理知识。'
      },
      done: {
        title: '一切就绪',
        desc: '你已经了解了核心功能，现在就开始构建你的知识助手吧！随时可点击菜单顶部昵称旁的帮助按钮重新查看引导。'
      },
      models: {
        title: '配置你的模型',
        desc: '这是关键一步：在模型管理中添加对话、向量等模型，知识库与对话功能才能正常工作。点击「添加模型」即可开始配置。'
      },
      settings: {
        title: '账户与设置入口',
        desc: '点开这里可以管理账户、成员与系统设置。需要再次查看本引导时，可点击菜单顶部昵称旁的帮助按钮重新打开。'
      },
      agents: {
        title: '打造专属智能体',
        desc: '把知识库、提示词与工具组合成可复用的智能体，沉淀你的专业能力。'
      },
      chat: {
        title: '发起智能对话',
        desc: '基于知识库内容向 AI 提问，获得带引用来源的精准回答。点击这里开始新对话。'
      },
      knowledge: {
        title: '创建你的知识库',
        desc: '知识库是一切的起点：上传文档、网页或 FAQ，WeKnora 会自动解析并建立索引。点击这里进入知识库。'
      },
      welcome: {
        title: '欢迎使用 WeKnora',
        desc: '只需几步，带你快速了解知识库、对话与智能体的核心用法。点击「下一步」开始。'
      }
    }
  },
  menu: {
    sessionInProgress: '会话进行中',
    sessionUnread: '有新回复',
    knowledgeBase: '知识库',
    agents: '智能体',
    automations: '自动化',
    artifacts: '产物',
    organizations: '共享空间',
    newChat: '新对话',
    settings: '系统设置',
    logout: '退出登录',
    clearMessages: '清空消息',
    clearMessagesSuccess: '消息已清空',
    clearMessagesFailed: '清空消息失败，请稍后再试',
    renameSession: '修改标题',
    renameSessionSuccess: '标题已更新',
    renameSessionFailed: '修改标题失败，请稍后再试',
    batchManage: '批量管理',
    newSession: '新会话',
    pin: '置顶',
    unpin: '取消置顶',
    pinFailed: '置顶失败，请稍后再试',
    unpinFailed: '取消置顶失败，请稍后再试',
    search: '搜索',
    collapseSidebar: '收起侧边栏',
    expandSidebar: '展开侧边栏',
    logoutSuccess: '已退出登录',
    myChats: '我的对话',
    apiChats: 'API 会话',
    noSessions: '暂无对话'
  }
}
