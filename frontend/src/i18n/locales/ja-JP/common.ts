export default {
  notifications: {
    pending: '対応が必要',
    history: '通知履歴',
    clearRead: '既読を削除',
    clearReadConfirm: '既読の通知を削除しますか？実行履歴、会話、未対応の項目は保持されます。',
    handle: '対応する',
    review: '承認を確認',
    unreadCount: '未読 {count} 件',
    unread: '未読',
    newItems: '新しい通知があります。更新',
    loadError: '通知を読み込めませんでした',
    awaitingApproval: '操作の承認待ち',
    awaitingAuthorization: '認可の完了待ち',
    awaitingReply: '返信待ち',
    title: '通知',
    markAllRead: 'すべて既読',
    pendingInvitations: '保留中の招待 {count} 件',
    empty: '通知はありません',
    kinds: {
      automation_succeeded: 'オートメーション完了',
      automation_failed: 'オートメーション失敗',
      automation_awaiting_approval: 'オートメーション承認待ち',
      automation_paused: 'オートメーション一時停止'
    }
  },
  toolbox: {
    title: 'ツールボックス',
    description: 'エージェントのスキル、外部ツール、ブラウザー接続を管理します。',
    unavailable: '現在のワークスペースで利用できるツールはありません。'
  },
  resourceSort: {
    title: '並び替え',
    updatedTime: '更新日時',
    updatedTimeDescription: '既定の並び順です。',
    createdTime: '作成日時',
    createdTimeDescription: '新しく作成した項目や古い項目を確認',
    name: '名前',
    nameDescription: '名前が分かっている項目をすばやく検索',
    recentlyUpdated: '更新が新しい順',
    earliestUpdated: '更新が古い順',
    recentlyCreated: '作成が新しい順',
    earliestCreated: '作成が古い順',
    nameAscending: 'A–Z',
    nameDescending: 'Z–A'
  },
  menu: {
    sessionInProgress: '会話中',
    sessionUnread: '新しい回答',
    knowledgeBase: 'ナレッジベース',
    agents: 'エージェント',
    automations: 'オートメーション',
    artifacts: '成果物',
    organizations: '共有スペース',
    newChat: '新しいチャット',
    settings: 'システム設定',
    logout: 'ログアウト',
    clearMessages: 'メッセージをクリア',
    clearMessagesSuccess: 'メッセージをクリアしました',
    clearMessagesFailed: 'メッセージのクリアに失敗しました。しばらくしてから再試行してください',
    renameSession: 'タイトルを変更',
    renameSessionSuccess: 'タイトルを更新しました',
    renameSessionFailed: 'タイトルの更新に失敗しました。しばらくしてから再試行してください',
    batchManage: '一括管理',
    newSession: '新しいチャット',
    pin: 'ピン留め',
    unpin: 'ピン留めを解除',
    pinFailed: 'ピン留めに失敗しました。しばらくしてから再試行してください',
    unpinFailed: 'ピン留めの解除に失敗しました。しばらくしてから再試行してください',
    search: '検索',
    collapseSidebar: 'サイドバーを折りたたむ',
    expandSidebar: 'サイドバーを展開',
    logoutSuccess: 'ログアウトしました',
    myChats: 'マイチャット',
    apiChats: 'APIセッション',
    noSessions: '会話はまだありません'
  },
  assistantEntryGuide: {
    stepOf: 'v0.9.0 の新機能',
    skip: 'わかりました',
    interactHint: '強調表示された入口をクリックしてアシスタントを開いてみましょう。'
  },
  newUserGuide: {
    stepOf: '{current} / {total}',
    skip: 'スキップ',
    prev: '戻る',
    next: '次へ',
    done: '完了',
    reopen: 'プロダクトツアー',
    steps: {
      assistant: {
        title: 'WeKnora アシスタントのご紹介',
        desc: 'WeKnora のバージョン、機能、設定について質問できます。現在のページをもとに、あなたの権限でエージェントの作成、定期タスクの設定、ナレッジの整理も行えます。'
      },
      welcome: {
        title: 'WeKnoraへようこそ',
        desc: '数ステップでナレッジベース、チャット、エージェントの使い方をご紹介します。「次へ」をクリックして開始してください。'
      },
      knowledge: {
        title: 'ナレッジベースを作成',
        desc: 'すべてはここから始まります。ドキュメント、Webページ、FAQをアップロードすると、WeKnoraが自動で解析してインデックスを作成します。ここをクリックしてナレッジベースを開いてください。'
      },
      chat: {
        title: 'AIチャットを開始',
        desc: 'ナレッジベースに基づいて質問すると、出典付きの正確な回答が得られます。ここをクリックして新しいチャットを開始してください。'
      },
      agents: {
        title: '専用エージェントを作成',
        desc: 'ナレッジベース、プロンプト、ツールを組み合わせて、あなたの知見を活かした再利用可能なエージェントを作成できます。'
      },
      settings: {
        title: 'アカウントと設定',
        desc: 'ここからアカウント、メンバー、システム設定を管理できます。このツアーは、メニュー上部のユーザ名の横にあるヘルプボタンからいつでも再開できます。'
      },
      models: {
        title: 'モデルを設定',
        desc: '重要なステップです。モデル管理でチャットモデルや埋め込みモデルなどを追加すると、ナレッジベースとチャットが利用できるようになります。「モデルを追加」をクリックして始めましょう。'
      },
      done: {
        title: '準備完了',
        desc: '基本は以上です。あなただけのナレッジアシスタントを作り始めましょう。このツアーは、メニュー上部のユーザ名の横にあるヘルプボタンからいつでも見直せます。'
      }
    }
  },
  contextualGuide: {
    stepOf: '{current} / {total}',
    skip: 'スキップ',
    prev: '戻る',
    next: '次へ',
    done: 'OK',
    interactHint: 'ハイライトされた箇所をクリックして続行してください',
    kbList: {
      steps: {
        create: {
          title: '最初のナレッジベースを作成',
          desc: 'ナレッジベースにはドキュメントやFAQを格納します。下のハイライトされた「ナレッジベースを作成」ボタンをクリックすると、入力手順をご案内します。'
        }
      }
    },
    tenantModels: {
      needChatModelFirst: 'エージェントを作成する前に、チャットモデル（KnowledgeQA）を追加してください。',
      steps: {
        intro: {
          title: 'まずモデルを設定',
          desc: 'ドキュメント型ナレッジベースには、チャットモデル（要約とQ&A用）と埋め込みモデル（ベクトル検索用）が少なくとも1つずつ必要です。システム設定から追加してください。'
        },
        addModel: {
          title: 'モデルを追加',
          desc: '「モデルを追加」をクリックし、KnowledgeQA（チャット）とEmbeddingの両タイプを設定してください。LiteユーザはOllama経由でローカルモデルを取得できます。'
        },
        done: {
          title: '設定後に続行',
          desc: 'モデルを保存したら設定を閉じ、「ナレッジベースを作成」をクリックしてください。ウィザードでタイプ、インデックス、モデルの紐付けを設定できます。'
        }
      },
      stepsAgent: {
        intro: {
          title: 'まずチャットモデルを設定',
          desc: 'エージェントの作成にはKnowledgeQAモデルが少なくとも1つ必要です。システム設定から追加してください（Embeddingはナレッジベースにのみ必要です）。'
        },
        addModel: {
          title: 'チャットモデルを追加',
          desc: '「モデルを追加」をクリックし、KnowledgeQAタイプを設定してください。'
        },
        done: {
          title: '続いてエージェントを作成',
          desc: '保存したら設定を閉じ、「エージェントを作成」をクリックしてください。ウィザードでモード、ナレッジベース、添付ファイルのアップロード設定を行えます。'
        }
      }
    },
    kbCreate: {
      steps: {
        type: {
          title: 'タイプを選択',
          desc: 'ドキュメント型はPDFやWordなどのアップロード向け、FAQ型は質問と回答のペア向けです。タイプは後から変更できません。'
        },
        name: {
          title: '名前を入力',
          desc: '「製品マニュアル」「サポートFAQ」のような分かりやすい名前を付けてください。説明は任意です。'
        },
        indexing: {
          title: 'インデックス機能',
          desc: 'ベクトル検索とキーワード検索はデフォルトで有効です。Wikiやナレッジグラフのインデックスも有効にできます。検索モードは少なくとも1つ有効にしてください。'
        },
        navModels: {
          title: 'モデル設定（必須）',
          desc: 'ナレッジベースにはチャットモデルが必要で、検索にはさらに埋め込みモデルが必要です。左側の「モデル設定」を開いてください。'
        },
        llm: {
          title: 'チャット／要約モデル',
          desc: '要約と回答の生成に使用します。一覧が空の場合は、ドロップダウンから設定を開いてモデルを追加してください。'
        },
        embedding: {
          title: '埋め込みモデル',
          desc: 'テキストをベクトル化して意味検索を可能にします。上記のベクトル／キーワードインデックスと連携します。'
        },
        parser: {
          title: '解析エンジン（任意）',
          desc: 'PDFやOfficeファイルの解析方法です。ほとんどの場合はデフォルト値で問題ありません。OCRや特殊なレイアウトが必要な場合に調整してください。'
        },
        chunking: {
          title: 'チャンク分割（任意）',
          desc: '検索用にドキュメントを分割する方法です。デフォルトのチャンクサイズはRAG向けに調整済みで、通常は変更不要です。'
        },
        storage: {
          title: 'ストレージ（任意）',
          desc: '元ファイルの保存先（ローカルまたはオブジェクトストレージ）です。通常はワークスペースのデフォルト値で問題ありません。'
        },
        navMultimodal: {
          title: 'マルチモーダル／画像（任意）',
          desc: '図表やスキャン、画像が多く含まれ、視覚的な理解が必要なドキュメントの場合は有効にしてください。'
        },
        multimodalToggle: {
          title: 'マルチモーダル解析を有効化',
          desc: '有効にすると、画像を含むアップロードを視覚言語モデルで処理し、検索精度を高めます。'
        },
        multimodalVllm: {
          title: 'VLMモデルを選択',
          desc: 'マルチモーダルにはVLMが必要です。一覧が空の場合はシステム設定から追加してください。'
        },
        faq: {
          title: 'FAQインデックス',
          desc: 'Q&Aペアのインデックス方法を選択してください。FAQ項目は作成後に追加できます。'
        },
        submit: {
          title: 'ナレッジベースを作成',
          desc: 'ナレッジベースを作成せずにガイドを終了するには「OK」をクリックしてください。準備ができたら名前を入力し、タイプとモデルを確認して「作成」をクリックしてください。'
        }
      }
    },
    agentList: {
      steps: {
        create: {
          title: 'エージェントを作成',
          desc: 'エージェントはモデル、ナレッジベース、ツール、プロンプトを組み合わせた再利用可能なアシスタントです。ハイライトされた「エージェントを作成」ボタンをクリックしてください。'
        }
      }
    },
    agentCreate: {
      steps: {
        mode: {
          title: '実行モードを選択',
          desc: 'シンプルなQ&Aには「クイック回答」、ツールと多段階の思考が必要な複雑なタスクには「スマート推論」を使用します。'
        },
        agentType: {
          title: 'エージェントのタイプを選択',
          desc: 'プリセットを選ぶと、システムプロンプト、推奨ツール、ナレッジ範囲が自動入力されます（例: Wiki作成、データ分析）。用途に応じて切り替えると、名前と説明も併せて更新されます。'
        },
        name: {
          title: '名前と説明',
          desc: '分かりやすい名前を付けてください。スマート推論モードではデフォルトの名前が入力される場合があります。必要に応じて編集してください。'
        },
        navModel: {
          title: 'チャットモデルを紐付け',
          desc: 'すべてのエージェントには推論エンジンとしてKnowledgeQAモデルが必要です。'
        },
        model: {
          title: 'モデルを選択',
          desc: '設定済みのチャットモデルから選択してください。無い場合は先にシステム設定で追加してください。'
        },
        navKnowledge: {
          title: 'ナレッジベースを紐付け',
          desc: 'エージェントが検索できるナレッジの範囲を指定します。デフォルトはすべてのナレッジベースです。'
        },
        knowledge: {
          title: 'ナレッジの範囲',
          desc: '汎用アシスタントには「すべて」、特定領域には「指定」、「なし」の場合はモデル単体またはWeb検索に依存します。'
        },
        navWebsearch: {
          title: 'Web検索（任意）',
          desc: 'エージェントが外部検索を呼び出して最新情報を取得できるようにします。'
        },
        navMultimodal: {
          title: '添付ファイルのアップロード（任意）',
          desc: 'チャットで画像、ドキュメント、音声を送信できるようになります。画像理解にはシステム設定でVLMが必要です。'
        },
        multimodal: {
          title: '添付ファイルのアップロードを有効化',
          desc: '画像と音声のアップロードおよび添付ファイルの解析ルールを設定します。画像アップロードを有効にする場合は、下でVLMを選択してください。'
        },
        navTools: {
          title: 'ツールとMCP（任意）',
          desc: 'スマート推論モードでは、検索やコード実行などの組み込みツールとMCPサービスを有効にできます。'
        },
        submit: {
          title: 'エージェントを保存',
          desc: 'ハイライトされた確認ボタンをクリックして完了してください。その後、チャットでこのエージェントを選択できます。'
        }
      }
    },
    kbDetail: {
      steps: {
        intro: {
          title: 'このナレッジベースは空です',
          desc: '最初の項目を追加すると、検索やチャットで利用できます。対応形式のファイルをドラッグ＆ドロップすることもできます。'
        },
        upload: {
          title: 'ドキュメントを追加',
          desc: 'このメニューから、ファイルやフォルダのアップロード、URLのインポート、オンラインでの作成ができます。'
        },
        done: {
          title: '解析後に利用可能',
          desc: 'ドキュメントのインデックスが完了したら、チャットで{\'@\'}を使ってこのナレッジベースを指定すると、出典付きの回答が得られます。'
        }
      }
    },
    chat: {
      steps: {
        kb: {
          title: 'ナレッジの範囲を選択',
          desc: '{\'@\'}をクリックして、ナレッジベースやファイルを1つ以上選択してください。選択した範囲のみが回答に使用され、未選択の場合は現在のエージェント設定が適用されます。'
        },
        input: {
          title: '質問を入力',
          desc: '知りたいことを入力するか、上部の推奨質問をクリックするとすぐに始められます。'
        },
        send: {
          title: '送信してチャットを開始',
          desc: '送信すると新しいセッションが作成されます。AIがナレッジベースを使って回答し、引用元の該当箇所を表示します。'
        },
        done: {
          title: '準備完了',
          desc: 'アップロードしたドキュメントに関する質問を試して、根拠付きの回答を確認してみましょう。'
        }
      }
    }
  },
  batchManage: {
    selectAll: 'すべて選択',
    cancel: 'キャンセル',
    delete: '会話を削除',
    deleteConfirmTitle: '会話を削除',
    deleteConfirmBody: '選択した{count}件の会話を削除してもよろしいですか？この操作は取り消せません。',
    deleteAllConfirmBody: 'すべての会話を削除してもよろしいですか？この操作は取り消せません。',
    deleteSuccess: '削除しました',
    deleteFailed: '削除に失敗しました。しばらくしてから再試行してください'
  },
  listSpaceSidebar: {
    all: 'すべて',
    workspace: 'ワークスペース',
    spaces: '共有された項目',
    favorites: 'お気に入り',
    recents: '最近'
  },
  resourceOrigin: {
    mine: '自分',
    mineTooltip: '自分が作成',
    tenant: 'ワークスペース',
    tenantTooltip: 'このワークスペースの他のメンバーが作成',
    tenantTooltipWithCreator: '{creator}が作成',
    space: 'スペース',
    spaceTooltip: '共有スペース「{space}」から共有',
    spaceTooltipWithTenant: '共有スペース「{space}」から共有 · 提供元{tenant}',
    shared: '外部',
    sharedTooltip: '共有スペース経由で外部ワークスペースからアクセス'
  },
  common: {
    add: '追加',
    me: '自分',
    confirm: '確認',
    cancel: 'キャンセル',
    unsavedChanges: {
      title: '未保存の変更があります',
      body: '閉じると現在の変更は失われます。閉じますか？',
      discard: '変更を破棄',
      keepEditing: '編集を続ける'
    },
    fullscreen: '全画面',
    exitFullscreen: '全画面を終了',
    save: '保存',
    delete: '削除',
    edit: '編集',
    copy: 'コピー',
    copied: 'コピーしました',
    copySuccess: 'コピーしました',
    default: 'デフォルト',
    create: '作成',
    download: 'ダウンロード',
    refresh: '更新',
    loading: '読み込み中...',
    noData: 'データがありません',
    noMoreData: 'すべての内容を読み込みました',
    loadMore: 'さらに読み込む',
    error: 'エラー',
    success: '成功',
    failed: '失敗',
    info: '情報',
    selectAll: 'すべて選択',
    yes: 'はい',
    no: 'いいえ',
    close: '閉じる',
    back: '戻る',
    next: '次へ',
    finish: '完了',
    all: 'すべて',
    clear: 'クリア',
    website: '公式サイト',
    clawhubSkill: 'Claw Skill',
    github: 'GitHub',
    githubStarTip: 'GitHubでリポジトリを開きます。役に立ったらStarをお願いします',
    on: 'オン',
    off: 'オフ',
    confirmDelete: '削除の確認',
    createSuccess: '作成しました',
    deleteSuccess: '削除しました',
    deleteFailed: '削除に失敗しました',
    updateSuccess: '更新しました',
    saveSuccess: '保存しました',
    saveFailed: '保存に失敗しました',
    operationFailed: '操作に失敗しました',
    file: 'ファイル',
    skill: 'スキル',
    knowledgeBase: 'ナレッジベース',
    noResult: '結果がありません',
    remove: '削除',
    avatar: 'アバター',
    defaultUser: 'ユーザ',
    copyFailed: 'コピーに失敗しました',
    retry: '再試行',
    expand: '展開',
    collapse: '折りたたむ'
  },
  file: {
    upload: 'ファイルをアップロード'
  },
  error: {
    networkError: 'ネットワークエラーです。接続を確認してください',
    requestTimeout: 'リクエストがタイムアウトしました。大きなファイルや低速な回線では再試行してください',
    invalidCredentials: 'ユーザ名またはパスワードが正しくありません',
    tokenRefreshFailed: 'トークンの更新に失敗しました',
    pleaseRelogin: '再度ログインしてください',
    fileSizeExceeded: 'ファイルサイズは{size}MB以下にしてください！',
    unsupportedFileType: '未対応のファイル形式です！',
    invalidFileType: '無効なファイル形式です！',
    invalidImageLink: '無効な画像リンクです',
    missingKbId: 'ナレッジベースIDがありません',
    tokenNotFound: 'ログイントークンが見つかりません。再度ログインしてください',
    streamFailed: 'ストリーム接続に失敗しました',
    auth: {
      loginFailed: 'ログインに失敗しました',
      registerFailed: '登録に失敗しました',
      getUserFailed: 'ユーザ情報の取得に失敗しました',
      getTenantFailed: 'ワークスペース情報の取得に失敗しました',
      updatePreferencesFailed: '個人設定の更新に失敗しました',
      refreshTokenFailed: 'トークンの更新に失敗しました',
      logoutFailed: 'ログアウトに失敗しました',
      validateTokenFailed: 'トークンの検証に失敗しました'
    },
    model: {
      createFailed: 'モデルの作成に失敗しました',
      getFailed: 'モデルの取得に失敗しました',
      updateFailed: 'モデルの更新に失敗しました',
      deleteFailed: 'モデルの削除に失敗しました'
    },
    tenant: {
      listFailed: 'ワークスペース一覧の取得に失敗しました',
      searchFailed: 'ワークスペースの検索に失敗しました',
      getApiPrincipalConfigFailed: 'APIプリンシパル設定の取得に失敗しました',
      updateApiPrincipalConfigFailed: 'APIプリンシパル設定の更新に失敗しました',
      createApiPrincipalTestTokenFailed: 'APIテストトークンの作成に失敗しました',
      updateFailed: 'ワークスペース情報の更新に失敗しました',
      deleteFailed: 'ワークスペースの削除に失敗しました'
    },
    initialization: {
      checkFailed: 'チェックに失敗しました',
      testFailed: 'テストに失敗しました'
    }
  },
  language: {
    zhCN: '简体中文',
    zhTW: '繁体字中国語',
    enUS: 'English',
    ruRU: 'Русский',
    koKR: '한국어',
    jaJP: '日本語',
    selectLanguage: '言語を選択',
    language: '言語',
    languageDescription: 'インターフェースの表示言語を選択します',
    languageSaved: '言語設定を保存しました'
  },
  general: {
    title: '一般設定',
    allSettings: 'すべての設定',
    personalSettings: '個人設定',
    helpAndDocs: 'ヘルプとドキュメント',
    description: '言語や外観などの基本オプションを設定します',
    settings: '設定',
    close: '設定を閉じる'
  },
  theme: {
    theme: 'テーマ',
    themeDescription: 'インターフェースの表示テーマを選択します。システム設定に合わせた自動切り替えにも対応しています',
    light: 'ライト',
    dark: 'ダーク',
    system: 'システムに従う',
    selectTheme: 'テーマを選択'
  },
  font: {
    uiFont: 'インターフェースフォント',
    uiFontDescription: 'メニュー、本文、ボタンなど、ほとんどのUIテキストに使用されるフォントです',
    monoFont: 'コードフォント',
    monoFontDescription: 'コードブロック、ターミナルコマンド、APIキー、ファイルパスなどの技術的なテキストに使用されます。すべての文字が同じ幅のため、0とO、1とlを見分けやすくなります。',
    selectFont: 'フォントを選択',
    sansPreview: 'サンプルSample書体Font — Aa Gg Oo 0123',
    monoPreview: 'const msg = \'Hello\'; // 0O1l',
    sans: {
      system: 'システムデフォルト',
      pingfang: 'PingFang SC',
      georgia: 'Georgia（セリフ）',
      yahei: 'Microsoft YaHei',
      times: 'Times New Roman（セリフ）',
      'noto-cjk': 'Noto Sans CJK',
      'dejavu-serif': 'DejaVu Serif（セリフ）',
      'sans-serif': '汎用サンセリフ'
    },
    mono: {
      system: 'システムデフォルト',
      menlo: 'Menlo',
      monaco: 'Monaco',
      consolas: 'Consolas',
      cascadia: 'Cascadia Code',
      'dejavu-mono': 'DejaVu Sans Mono',
      'liberation-mono': 'Liberation Mono',
      monospace: '汎用等幅'
    },
    fontSize: 'フォントサイズ',
    fontSizeDescription: 'インターフェース全体（文字、アイコン、余白）を拡大縮小し、すぐに反映されます',
    size: {
      small: '小',
      normal: '標準',
      large: '大'
    }
  },
  platform: {
    subtitle: 'LLM駆動のエンタープライズ向けナレッジフレームワーク',
    description: 'RAG検索、エージェント推論、Wikiナレッジベースにより、ドキュメントを本当に理解し活用できます',
    rag: 'RAG拡張生成',
    agent: 'ReActエージェント',
    wiki: 'Wikiナレッジベース',
    hybridSearch: 'ハイブリッド検索',
    multimodalParsing: 'マルチモーダルドキュメント解析',
    hybridSearchEngine: 'ハイブリッド検索 + ナレッジグラフ',
    ragQandA: 'ReActエージェントQ&A',
    independentTenant: '独立したワークスペース',
    fullApiAccess: 'フルAPIアクセス',
    knowledgeBaseManagement: 'ナレッジベース管理',
    carousel: {
      agenticRagTitle: 'エージェンティックRAG',
      agenticRagDesc: 'ReAct推論 + ツール呼び出し + 多段階の思考',
      hybridSearchTitle: 'ハイブリッド検索戦略',
      hybridSearchDesc: 'BM25 + ベクトル + ナレッジグラフ',
      wikiTitle: 'Wikiナレッジベース',
      wikiDesc: 'ドキュメントを構造化され相互にリンクしたナレッジへ凝縮します',
      smartDocRetrievalTitle: 'インテリジェントなドキュメント検索',
      smartDocRetrievalDesc: 'PDF/Word/画像のマルチフォーマット解析'
    }
  },
  time: {
    today: '今日',
    yesterday: '昨日',
    last7Days: '過去7日間',
    last30Days: '過去30日間',
    lastYear: '過去1年',
    earlier: 'それ以前',
    pinned: 'ピン留め'
  },
  promptTemplate: {
    noTemplates: '利用できるテンプレートがありません',
    selectTemplate: 'テンプレートを選択',
    useTemplate: 'テンプレートを使用',
    resetDefault: 'デフォルトに戻す',
    default: 'デフォルト',
    withKnowledgeBase: 'ナレッジベース',
    withWebSearch: 'Web検索'
  },
  preview: {
    tab: 'プレビュー',
    loading: 'ドキュメントのプレビューを読み込み中...',
    loadFailed: 'ドキュメントのプレビューの読み込みに失敗しました',
    retry: '再試行',
    unsupported: 'このファイル形式はオンラインプレビューに対応していません',
    unsupportedHint: 'ダウンロードして、ローカルのアプリケーションで開いてください',
    zoomIn: '拡大',
    zoomOut: '縮小',
    fullscreen: '全画面表示',
    exitFullscreen: '全画面表示を終了',
    htmlRendered: 'レンダリング表示',
    htmlSource: 'ソースを表示',
    audioLoading: '音声を読み込み中…',
    audioNotSupported: 'お使いのブラウザは音声再生に対応していません',
    videoNotSupported: 'お使いのブラウザは動画再生に対応していません'
  },
  commandPalette: {
    placeholder: 'ナレッジベース、ファイル、会話を検索…',
    clearRecent: 'クリア',
    retrieval: '検索設定',
    untitledSession: '無題の会話',
    errors: {
      knowledge: 'ナレッジベースの検索に失敗しました。再試行してください。',
      messages: '会話内容の検索に失敗しました。再試行してください。'
    },
    scope: {
      placeholder: 'このナレッジベース内を検索…',
      remove: '範囲を解除（Backspace）'
    },
    group: {
      chunks: 'ファイル',
      messages: 'メッセージ',
      kbs: 'ナレッジベース',
      agents: 'エージェント',
      sessionsByTitle: 'チャット（タイトル）',
      commands: 'コマンド',
      recent: '最近',
      quickActions: 'クイックアクション'
    },
    match: {
      vector: 'ベクトル',
      keyword: 'キーワード'
    },
    quick: {
      newChat: '新しい会話',
      knowledgeBases: 'ナレッジベースを開く',
      agents: 'エージェントを開く',
      organizations: '共有スペースを開く',
      settings: '設定を開く',
      productTour: 'プロダクトツアー',
      assistant: '{name}を開く'
    },
    empty: {
      noResults: '一致する項目がありません',
      askAi: 'AIに直接質問する',
      adjustRetrieval: '検索設定を調整'
    },
    hotkey: {
      select: '移動',
      enter: '開く',
      cmdNumber: '直接開く',
      cmdEnter: 'チャットを開始',
      esc: '閉じる'
    }
  },
  tools: {
    multiKbSearch: 'ナレッジベース横断検索',
    searchKnowledge: 'ナレッジベースを検索',
    readDocument: 'ドキュメントを読む',
    listDocuments: 'ドキュメント一覧',
    knowledgeSearch: 'ナレッジベース検索',
    grepChunks: 'テキストパターン検索',
    getChunkDetail: 'チャンク詳細の取得',
    listKnowledgeChunks: 'ナレッジチャンクの一覧',
    listKnowledgeBases: 'ナレッジベースの一覧',
    getDocumentInfo: 'ドキュメント情報の取得',
    queryKnowledgeGraph: 'ナレッジグラフの照会',
    think: '深い思考',
    todoWrite: '計画の作成',
    askUser: 'ユーザーに質問'
  },
  mermaid: {
    diagram: '図',
    expand: '全画面表示',
    zoomIn: '拡大',
    zoomOut: '縮小',
    reset: 'リセット',
    download: '画像をダウンロード',
    close: '閉じる',
    downloading: 'ダウンロード中...'
  }
}
