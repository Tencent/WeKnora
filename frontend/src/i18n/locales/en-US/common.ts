export default {
  notifications: {
    pending: 'Needs attention',
    history: 'Notification history',
    clearRead: 'Clear read',
    clearReadConfirm: 'Clear read notifications? Run records, conversations, and pending actions will be kept.',
    handle: 'Open',
    review: 'Review',
    unreadCount: '{count} unread',
    unread: 'Unread',
    newItems: 'New notifications — refresh',
    loadError: 'Could not load notifications',
    awaitingApproval: 'Waiting for your approval',
    awaitingAuthorization: 'Waiting for authorization',
    awaitingReply: 'Waiting for your reply',
    title: 'Notifications',
    markAllRead: 'Mark all read',
    pendingInvitations: '{count} pending invitations',
    empty: 'No notifications',
    kinds: {
      automation_succeeded: 'Automation finished',
      automation_failed: 'Automation failed',
      automation_awaiting_approval: 'Automation needs approval',
      automation_paused: 'Automation paused'
    }
  },
  toolbox: {
    title: 'Toolbox',
    description: 'Manage skills, external tools, and browser connections for your agents.',
    unavailable: 'No tools are available in the current workspace.'
  },
  resourceSort: {
    title: 'Sort',
    updatedTime: 'Updated time',
    updatedTimeDescription: 'Default option.',
    createdTime: 'Created time',
    createdTimeDescription: 'View the newest or earliest created items',
    name: 'Name',
    nameDescription: 'Quickly find an item when you know its name',
    recentlyUpdated: 'Recently updated',
    earliestUpdated: 'Earliest updated',
    recentlyCreated: 'Recently created',
    earliestCreated: 'Earliest created',
    nameAscending: 'A–Z',
    nameDescending: 'Z–A'
  },
  menu: {
    sessionInProgress: 'Conversation in progress',
    sessionUnread: 'New reply',
    knowledgeBase: 'Knowledge Base',
    agents: 'Agents',
    automations: 'Automations',
    artifacts: 'Artifacts',
    organizations: 'Shared Spaces',
    newChat: 'New Chat',
    settings: 'System Settings',
    logout: 'Logout',
    clearMessages: 'Clear Messages',
    clearMessagesSuccess: 'Messages cleared',
    clearMessagesFailed: 'Failed to clear messages, please try again later',
    renameSession: 'Rename',
    renameSessionSuccess: 'Title updated',
    renameSessionFailed: 'Failed to update title, please try again later',
    batchManage: 'Batch Manage',
    newSession: 'New Chat',
    pin: 'Pin',
    unpin: 'Unpin',
    pinFailed: 'Failed to pin, please try again later',
    unpinFailed: 'Failed to unpin, please try again later',
    search: 'Search',
    collapseSidebar: 'Collapse Sidebar',
    expandSidebar: 'Expand Sidebar',
    logoutSuccess: 'Logged out successfully',
    myChats: 'My chats',
    apiChats: 'API sessions',
    noSessions: 'No conversations yet'
  },
  assistantEntryGuide: {
    stepOf: 'NEW IN v0.9.0',
    skip: 'Got it',
    interactHint: 'Click the highlighted entry to open the assistant.'
  },
  newUserGuide: {
    stepOf: '{current} / {total}',
    skip: 'Skip',
    prev: 'Back',
    next: 'Next',
    done: 'Done',
    reopen: 'Product tour',
    steps: {
      assistant: {
        title: 'Meet the WeKnora Assistant',
        desc: 'Ask about WeKnora versions, features and setup. Using the current page as context, the assistant can create agents, schedule tasks and organize knowledge on your behalf, within your permissions.'
      },
      welcome: {
        title: 'Welcome to WeKnora',
        desc: 'A few quick steps to get you familiar with knowledge bases, chat and agents. Click "Next" to begin.'
      },
      knowledge: {
        title: 'Create your knowledge base',
        desc: 'Everything starts here: upload documents, web pages or FAQs and WeKnora parses and indexes them automatically. Click here to open knowledge bases.'
      },
      chat: {
        title: 'Start an AI chat',
        desc: 'Ask questions grounded in your knowledge base and get accurate answers with cited sources. Click here to start a new chat.'
      },
      agents: {
        title: 'Build dedicated agents',
        desc: 'Combine knowledge bases, prompts and tools into reusable agents that capture your expertise.'
      },
      settings: {
        title: 'Account & settings',
        desc: 'Open this to manage your account, members and system settings. You can reopen this tour from the help button next to your name at the top of this menu.'
      },
      models: {
        title: 'Configure your models',
        desc: 'A key step: add chat, embedding and other models in Model Management so knowledge bases and chat can work. Click "Add model" to get started.'
      },
      done: {
        title: 'You are all set',
        desc: 'You now know the essentials — start building your knowledge assistant! Revisit this tour anytime from the help button next to your name in this menu.'
      }
    }
  },
  contextualGuide: {
    stepOf: '{current} / {total}',
    skip: 'Skip',
    prev: 'Back',
    next: 'Next',
    done: 'Got it',
    interactHint: 'Click the highlighted area to continue',
    kbList: {
      steps: {
        create: {
          title: 'Create your first knowledge base',
          desc: 'Knowledge bases hold documents and FAQs. Click the highlighted "New knowledge base" button below and we will walk you through the form.'
        }
      }
    },
    tenantModels: {
      needChatModelFirst: 'Add a chat model (KnowledgeQA) before creating an agent.',
      steps: {
        intro: {
          title: 'Configure models first',
          desc: 'A document knowledge base needs at least one chat model (summaries and Q&A) and one Embedding model (vector search). Add them in system settings.'
        },
        addModel: {
          title: 'Add models',
          desc: 'Click "Add model" and configure KnowledgeQA (chat) and Embedding types. Lite users can pull local models via Ollama.'
        },
        done: {
          title: 'Then continue',
          desc: 'After saving models, close settings and click "New knowledge base". The wizard will walk you through type, indexing, and model binding.'
        }
      },
      stepsAgent: {
        intro: {
          title: 'Configure a chat model first',
          desc: 'Creating an agent requires at least one KnowledgeQA model. Add it in system settings (Embedding is only required for knowledge bases).'
        },
        addModel: {
          title: 'Add a chat model',
          desc: 'Click "Add model" and configure a KnowledgeQA type.'
        },
        done: {
          title: 'Then create an agent',
          desc: 'After saving, close settings and click "Create agent". The wizard covers mode, knowledge bases, and attachment upload options.'
        }
      }
    },
    kbCreate: {
      steps: {
        type: {
          title: 'Choose a type',
          desc: 'Document bases are for PDFs, Word files, and similar uploads. FAQ bases are for question–answer pairs. The type cannot be changed later.'
        },
        name: {
          title: 'Enter a name',
          desc: 'Pick a clear name such as "Product manual" or "Support FAQ". Description is optional.'
        },
        indexing: {
          title: 'Indexing capabilities',
          desc: 'Vector and keyword search are on by default. You can also enable Wiki or knowledge-graph indexing. Keep at least one search mode enabled.'
        },
        navModels: {
          title: 'Model setup (required)',
          desc: 'Every knowledge base needs a chat model; retrieval also requires an Embedding model. Open "Model configuration" on the left.'
        },
        llm: {
          title: 'Chat / summary model',
          desc: 'Used for summaries and answers. If the list is empty, use the dropdown to open settings and add a model.'
        },
        embedding: {
          title: 'Embedding model',
          desc: 'Turns text into vectors for semantic search. Works with vector/keyword indexing above.'
        },
        parser: {
          title: 'Parser engine (optional)',
          desc: 'How PDFs and Office files are parsed. Defaults work for most cases; adjust if you need OCR or special layouts.'
        },
        chunking: {
          title: 'Chunking (optional)',
          desc: 'How documents are split for retrieval. Default chunk sizes are tuned for RAG and rarely need changes.'
        },
        storage: {
          title: 'Storage (optional)',
          desc: 'Where raw files are stored (local or object storage). The workspace default is usually fine.'
        },
        navMultimodal: {
          title: 'Multimodal / images (optional)',
          desc: 'Enable this if documents contain charts, scans, or image-heavy content that needs vision understanding.'
        },
        multimodalToggle: {
          title: 'Enable multimodal parsing',
          desc: 'When on, image-bearing uploads are processed with a vision-language model for better retrieval.'
        },
        multimodalVllm: {
          title: 'Choose a VLM model',
          desc: 'Multimodal requires a VLM. Add one in system settings if the list is empty.'
        },
        faq: {
          title: 'FAQ indexing',
          desc: 'Choose how Q&A pairs are indexed. You can add FAQ entries after creation.'
        },
        submit: {
          title: 'Create the knowledge base',
          desc: 'Click Got it to finish the guide without creating a knowledge base. When ready, enter a name, confirm the type and models, then click Create.'
        }
      }
    },
    agentList: {
      steps: {
        create: {
          title: 'Create your agent',
          desc: 'Agents combine models, knowledge bases, tools, and prompts into reusable assistants. Click the highlighted "Create agent" button.'
        }
      }
    },
    agentCreate: {
      steps: {
        mode: {
          title: 'Choose run mode',
          desc: '"Quick answer" for straightforward Q&A; "Smart reasoning" uses tools and multi-step thinking for complex tasks.'
        },
        agentType: {
          title: 'Choose an agent type',
          desc: 'Presets fill in the system prompt, recommended tools, and knowledge scope (e.g. Wiki builder, data analysis). Switch by scenario—name and description update accordingly.'
        },
        name: {
          title: 'Name and description',
          desc: 'Pick a recognizable name. Smart-reasoning mode may pre-fill a default you can edit.'
        },
        navModel: {
          title: 'Bind a chat model',
          desc: 'Every agent needs a KnowledgeQA model as its reasoning engine.'
        },
        model: {
          title: 'Select model',
          desc: 'Choose from configured chat models, or add one in system settings first.'
        },
        navKnowledge: {
          title: 'Link knowledge bases',
          desc: 'Control which knowledge the agent can retrieve. Default is all knowledge bases.'
        },
        knowledge: {
          title: 'Knowledge scope',
          desc: '"All" for general assistants; "Selected" for a domain; "None" relies on the model alone or web search.'
        },
        navWebsearch: {
          title: 'Web search (optional)',
          desc: 'Allow the agent to call external search for up-to-date information.'
        },
        navMultimodal: {
          title: 'Attachment upload (optional)',
          desc: 'Lets users send images, documents, and audio in chat; image understanding requires a VLM in system settings.'
        },
        multimodal: {
          title: 'Enable attachment upload',
          desc: 'Configure image and audio upload plus attachment parse rules; select a VLM below when image upload is enabled.'
        },
        navTools: {
          title: 'Tools & MCP (optional)',
          desc: 'In smart-reasoning mode, enable built-in tools and MCP services for search, code, and more.'
        },
        submit: {
          title: 'Save the agent',
          desc: 'Click the highlighted confirm button to finish. You can then select this agent in chat.'
        }
      }
    },
    kbDetail: {
      steps: {
        intro: {
          title: 'This knowledge base is empty',
          desc: 'Add your first item so you can search and chat over it. You can also drag and drop supported file types.'
        },
        upload: {
          title: 'Add documents',
          desc: 'Use this menu to upload files or folders, import a URL, or create content online.'
        },
        done: {
          title: 'Ready after parsing',
          desc: 'Once documents are indexed, mention this knowledge base in chat with {\'@\'} to get answers with citations.'
        }
      }
    },
    chat: {
      steps: {
        kb: {
          title: 'Choose knowledge scope',
          desc: 'Click {\'@\'} to pick one or more knowledge bases or files. Answers use only the selection; otherwise the current agent settings apply.'
        },
        input: {
          title: 'Type your question',
          desc: 'Describe what you want to know, or click a suggested question above to get started quickly.'
        },
        send: {
          title: 'Send to start chatting',
          desc: 'Sending creates a new session. The AI answers using your knowledge base and shows cited passages.'
        },
        done: {
          title: 'You are ready to explore',
          desc: 'Try a question related to your uploaded documents and see grounded answers with references.'
        }
      }
    }
  },
  batchManage: {
    selectAll: 'Select All',
    cancel: 'Cancel',
    delete: 'Delete Conversations',
    deleteConfirmTitle: 'Delete Conversations',
    deleteConfirmBody: 'Are you sure you want to delete the selected {count} conversation(s)? This action cannot be undone.',
    deleteAllConfirmBody: 'Are you sure you want to delete all conversations? This action cannot be undone.',
    deleteSuccess: 'Deleted successfully',
    deleteFailed: 'Delete failed, please try again later'
  },
  listSpaceSidebar: {
    all: 'All',
    workspace: 'Workspace',
    spaces: 'Shared with me',
    favorites: 'Starred',
    recents: 'Recent'
  },
  resourceOrigin: {
    mine: 'Mine',
    mineTooltip: 'Created by you',
    tenant: 'Workspace',
    tenantTooltip: 'Created by another member of this workspace',
    tenantTooltipWithCreator: 'Created by {creator}',
    space: 'Space',
    spaceTooltip: 'Shared via space "{space}"',
    spaceTooltipWithTenant: 'Shared via space "{space}" · from {tenant}',
    shared: 'External',
    sharedTooltip: 'Accessed from an external workspace via a shared space'
  },
  common: {
    quickUse: 'Quick use',
    quickUseResource: 'Start a chat with {name}',
    add: 'Add',
    me: 'Me',
    confirm: 'Confirm',
    cancel: 'Cancel',
    unsavedChanges: {
      title: 'Unsaved changes',
      body: 'Your changes will be lost if you close now. Close anyway?',
      discard: 'Discard changes',
      keepEditing: 'Keep editing'
    },
    fullscreen: 'Full screen',
    exitFullscreen: 'Exit full screen',
    save: 'Save',
    delete: 'Delete',
    edit: 'Edit',
    copy: 'Copy',
    copied: 'Copied',
    copySuccess: 'Copied successfully',
    default: 'Default',
    create: 'Create',
    download: 'Download',
    refresh: 'Refresh',
    loading: 'Loading...',
    noData: 'No data',
    noMoreData: 'All content loaded',
    loadMore: 'Load more',
    error: 'Error',
    success: 'Success',
    failed: 'Failed',
    info: 'Information',
    selectAll: 'Select All',
    yes: 'Yes',
    no: 'No',
    close: 'Close',
    back: 'Back',
    next: 'Next',
    finish: 'Finish',
    all: 'All',
    clear: 'Clear',
    website: 'Official Website',
    clawhubSkill: 'Claw Skill',
    github: 'GitHub',
    githubStarTip: 'Open the repo on GitHub — star it if you find it useful',
    on: 'On',
    off: 'Off',
    confirmDelete: 'Confirm Delete',
    createSuccess: 'Created successfully',
    deleteSuccess: 'Deleted successfully',
    deleteFailed: 'Delete failed',
    updateSuccess: 'Updated successfully',
    saveSuccess: 'Saved successfully',
    saveFailed: 'Save failed',
    operationFailed: 'Operation failed',
    file: 'File',
    skill: 'Skills',
    knowledgeBase: 'Knowledge Base',
    noResult: 'No results',
    remove: 'Remove',
    avatar: 'Avatar',
    defaultUser: 'User',
    copyFailed: 'Copy failed',
    retry: 'Retry',
    expand: 'Expand',
    collapse: 'Collapse'
  },
  file: {
    upload: 'Upload File'
  },
  error: {
    networkError: 'Network error, please check your connection',
    requestTimeout: 'Request timed out. Large files or slow connections may need another try.',
    invalidCredentials: 'Invalid username or password',
    tokenRefreshFailed: 'Token refresh failed',
    pleaseRelogin: 'Please log in again',
    fileSizeExceeded: 'File size cannot exceed {size}MB!',
    unsupportedFileType: 'Unsupported file type!',
    invalidFileType: 'Invalid file type!',
    invalidImageLink: 'Invalid image link',
    missingKbId: 'Missing knowledge base ID',
    tokenNotFound: 'Login token not found, please log in again',
    streamFailed: 'Stream connection failed',
    auth: {
      loginFailed: 'Login failed',
      registerFailed: 'Registration failed',
      getUserFailed: 'Failed to get user info',
      getTenantFailed: 'Failed to get workspace info',
      updatePreferencesFailed: 'Failed to update preferences',
      refreshTokenFailed: 'Token refresh failed',
      logoutFailed: 'Logout failed',
      validateTokenFailed: 'Token validation failed'
    },
    model: {
      createFailed: 'Failed to create model',
      getFailed: 'Failed to get model',
      updateFailed: 'Failed to update model',
      deleteFailed: 'Failed to delete model'
    },
    tenant: {
      listFailed: 'Failed to list workspaces',
      searchFailed: 'Failed to search workspaces',
      getApiPrincipalConfigFailed: 'Failed to get API principal config',
      updateApiPrincipalConfigFailed: 'Failed to update API principal config',
      createApiPrincipalTestTokenFailed: 'Failed to create API test token',
      updateFailed: 'Failed to update workspace information',
      deleteFailed: 'Failed to delete workspace'
    },
    initialization: {
      checkFailed: 'Check failed',
      testFailed: 'Test failed'
    }
  },
  language: {
    zhCN: '简体中文',
    zhTW: 'Traditional Chinese',
    enUS: 'English',
    ruRU: 'Русский',
    koKR: '한국어',
    jaJP: '日本語',
    selectLanguage: 'Select Language',
    language: 'Language',
    languageDescription: 'Select interface display language',
    languageSaved: 'Language settings saved'
  },
  general: {
    title: 'General Settings',
    allSettings: 'All Settings',
    personalSettings: 'Personal Settings',
    helpAndDocs: 'Help & Documentation',
    description: 'Configure language, appearance and other basic options',
    settings: 'Settings',
    close: 'Close Settings'
  },
  theme: {
    theme: 'Theme',
    themeDescription: 'Choose the display theme for the interface, supports automatic switching with system settings',
    light: 'Light',
    dark: 'Dark',
    system: 'Follow System',
    selectTheme: 'Select theme'
  },
  font: {
    uiFont: 'Interface Font',
    uiFontDescription: 'Font used for menus, body text, buttons, and most UI text',
    monoFont: 'Code Font',
    monoFontDescription: 'Used for code blocks, terminal commands, API keys, file paths and other technical text. Every character has the same width so you can tell 0 from O and 1 from l.',
    selectFont: 'Select font',
    sansPreview: 'The quick brown fox jumps — Aa Gg Oo 0123',
    monoPreview: 'const msg = \'Hello\'; // 0O1l',
    sans: {
      system: 'System Default',
      pingfang: 'PingFang SC',
      georgia: 'Georgia (Serif)',
      yahei: 'Microsoft YaHei',
      times: 'Times New Roman (Serif)',
      'noto-cjk': 'Noto Sans CJK',
      'dejavu-serif': 'DejaVu Serif (Serif)',
      'sans-serif': 'Generic Sans-Serif'
    },
    mono: {
      system: 'System Default',
      menlo: 'Menlo',
      monaco: 'Monaco',
      consolas: 'Consolas',
      cascadia: 'Cascadia Code',
      'dejavu-mono': 'DejaVu Sans Mono',
      'liberation-mono': 'Liberation Mono',
      monospace: 'Generic Monospace'
    },
    fontSize: 'Font Size',
    fontSizeDescription: 'Scales the entire interface (text, icons, spacing) and applies immediately',
    size: {
      small: 'Small',
      normal: 'Normal',
      large: 'Large'
    }
  },
  platform: {
    subtitle: 'LLM-Powered Enterprise Knowledge Framework',
    description: 'RAG retrieval, agentic reasoning and Wiki knowledge bases — so your documents are truly understood and put to work',
    rag: 'RAG Enhanced Generation',
    agent: 'ReAct Agent',
    wiki: 'Wiki Knowledge Base',
    hybridSearch: 'Hybrid Search',
    multimodalParsing: 'Multimodal Document Parsing',
    hybridSearchEngine: 'Hybrid Search + Knowledge Graph',
    ragQandA: 'ReAct Agent Q&A',
    independentTenant: 'Independent Workspace',
    fullApiAccess: 'Full API Access',
    knowledgeBaseManagement: 'Knowledge Base Management',
    carousel: {
      agenticRagTitle: 'Agentic RAG',
      agenticRagDesc: 'ReAct reasoning + tool calls + multi-step thinking',
      hybridSearchTitle: 'Hybrid search strategy',
      hybridSearchDesc: 'BM25 + Vector + Knowledge Graph',
      wikiTitle: 'Wiki Knowledge Base',
      wikiDesc: 'Distills documents into structured, interlinked knowledge',
      smartDocRetrievalTitle: 'Intelligent document retrieval',
      smartDocRetrievalDesc: 'PDF/Word/Image multi-format parsing'
    }
  },
  time: {
    today: 'Today',
    yesterday: 'Yesterday',
    last7Days: 'Last 7 Days',
    last30Days: 'Last 30 Days',
    lastYear: 'Last Year',
    earlier: 'Earlier',
    pinned: 'Pinned'
  },
  promptTemplate: {
    noTemplates: 'No templates available',
    selectTemplate: 'Select Template',
    useTemplate: 'Use Template',
    resetDefault: 'Reset Default',
    default: 'Default',
    withKnowledgeBase: 'KB',
    withWebSearch: 'Web Search'
  },
  preview: {
    tab: 'Preview',
    loading: 'Loading document preview...',
    loadFailed: 'Failed to load document preview',
    retry: 'Retry',
    unsupported: 'This file type does not support online preview',
    unsupportedHint: 'Please download and open with a local application',
    zoomIn: 'Zoom in',
    zoomOut: 'Zoom out',
    fullscreen: 'Fullscreen',
    exitFullscreen: 'Exit Fullscreen',
    htmlRendered: 'Rendered preview',
    htmlSource: 'View source',
    audioLoading: 'Loading audio…',
    audioNotSupported: 'Your browser does not support audio playback',
    videoNotSupported: 'Your browser does not support video playback'
  },
  commandPalette: {
    placeholder: 'Search knowledge bases, files, conversations…',
    clearRecent: 'Clear',
    retrieval: 'Retrieval settings',
    untitledSession: 'Untitled conversation',
    errors: {
      knowledge: 'Could not search knowledge bases. Please try again.',
      messages: 'Could not search conversation content. Please try again.'
    },
    scope: {
      placeholder: 'Search within this knowledge base…',
      remove: 'Remove scope (Backspace)'
    },
    group: {
      chunks: 'Files',
      messages: 'Messages',
      kbs: 'Knowledge bases',
      agents: 'Agents',
      sessionsByTitle: 'Chats (by title)',
      commands: 'Commands',
      recent: 'Recent',
      quickActions: 'Quick actions'
    },
    match: {
      vector: 'Vector',
      keyword: 'Keyword'
    },
    quick: {
      newChat: 'New conversation',
      knowledgeBases: 'Open knowledge bases',
      agents: 'Open agents',
      organizations: 'Open shared spaces',
      settings: 'Open settings',
      productTour: 'Product tour',
      assistant: 'Open {name}'
    },
    empty: {
      noResults: 'No matches found',
      askAi: 'Ask the AI directly',
      adjustRetrieval: 'Adjust retrieval'
    },
    hotkey: {
      select: 'Navigate',
      enter: 'Open',
      cmdNumber: 'Jump to',
      cmdEnter: 'Start chat',
      esc: 'Close'
    }
  },
  tools: {
    multiKbSearch: 'Cross-KB Search',
    searchKnowledge: 'Search knowledge',
    readDocument: 'Read document',
    listDocuments: 'List documents',
    knowledgeSearch: 'Knowledge Search',
    grepChunks: 'Text Pattern Search',
    getChunkDetail: 'Get Chunk Detail',
    listKnowledgeChunks: 'List Knowledge Chunks',
    listKnowledgeBases: 'List Knowledge Bases',
    getDocumentInfo: 'Get Document Info',
    queryKnowledgeGraph: 'Query Knowledge Graph',
    think: 'Deep Thinking',
    todoWrite: 'Make Plan',
    askUser: 'Ask User'
  },
  mermaid: {
    diagram: 'Diagram',
    expand: 'Expand',
    zoomIn: 'Zoom In',
    zoomOut: 'Zoom Out',
    reset: 'Reset',
    download: 'Download Image',
    close: 'Close',
    downloading: 'Downloading...'
  }
}
