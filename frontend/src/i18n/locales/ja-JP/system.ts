export default {
  documentProgress: {
    queueView: 'キュータスク',
    title: '資料と Wiki の進捗',
    description: '取り込みと Wiki の現在の状態を個別に確認できます。復旧後も元の失敗履歴は保持されます。',
    refresh: '更新',
    search: '資料・ナレッジベースを検索',
    searchButton: '検索',
    filter: '進捗フィルター',
    count: '資料数（1～100）',
    batchLimit: '上限件数',
    totalCount: '全 {count} 件',
    batchStart: 'Wiki を一括開始',
    confirmBatchScoped: '検索条件「{search}」に一致する待機中の資料から、古い順に最大 {count} 件の Wiki 処理を予約しますか？',
    confirmBatchAll: '全ワークスペースの待機中の資料から、古い順に最大 {count} 件の Wiki 処理を予約しますか？',
    batchDisabledStatus: '「未完了」または「すべての資料」に切り替えると一括開始できます',
    batchDisabledEmpty: '現在の条件に一致する資料がありません',
    confirmOne: '「{title}」の既存の進捗を引き継いで Wiki 処理を予約しますか？',
    attempt: '処理回数 {value}',
    pendingOps: '待機中 {count} 件 · 再試行回数 {retries}',
    historyCount: '履歴 {count} 件',
    details: '詳細',
    startNow: 'Wiki を今すぐ開始',
    currentStatus: '現在の状態',
    stagesTitle: '処理ステージ',
    noStages: 'ステージ記録はまだありません。',
    emptyTitle: '該当する資料がありません',
    emptyUnfinished: '現在、未完了の資料や待機中の Wiki 処理はありません。',
    viewAll: 'すべての資料を表示',
    clearSearch: '検索をクリア',
    history: '失敗履歴',
    historyDescription: '復旧済みのものを含む、元の失敗記録です。履歴があることは、現在も失敗していることを意味しません。',
    noHistory: 'この資料の失敗履歴はありません。',
    failureAttempts: '失敗回数 {count}',
    historyLimit: '最新の 50 件を表示しています。元の履歴は保持されます。',
    unfinished: '未完了',
    all: 'すべての資料',
    document: '資料',
    ingestion: '取り込み',
    wiki: 'Wiki',
    actions: '操作',
    loadError: '進捗を取得できませんでした。更新して再試行してください。',
    started: '{count} 件を予約しました。{queued} 件は予約済みです。既存の進捗は保持されます。',
    startError: 'キューへの受け渡しが中断する前に {count} 件を受け付けました。更新してから残りを予約してください。',
    states: {
      ready: '取り込み済み',
      pending: '待機',
      processing: '処理中',
      finalizing: '仕上げ中',
      completed: '完了',
      failed: '失敗',
      skipped: 'スキップ済み',
      cancelled: 'キャンセル済み',
      deleting: '削除中',
      queued: 'キュー待機',
      retrying: '再試行中',
      disabled: '無効',
      notStarted: '未開始'
    },
    stages: {
      docreader: '文書解析',
      chunking: '分割',
      embedding: '索引作成',
      multimodal: '画像処理',
      postprocess: '後処理'
    }
  },
  system: {
    title: 'システム情報',
    sectionDescription: 'システムのバージョン情報とユーザアカウント設定を表示します',
    loadingInfo: '情報を読み込み中...',
    retry: '再試行',
    versionLabel: 'アプリバージョン',
    versionDescription: 'アプリケーションサービス（weknora-app）のバージョン',
    frontendVersionLabel: 'UIバージョン',
    frontendVersionDescription: 'UI（weknora-ui）のビルドバージョン',
    versionMismatch: 'アプリバージョンと不一致',
    buildTimeLabel: 'ビルド日時',
    buildTimeDescription: 'システムがビルドされた日時',
    goVersionLabel: 'Goバージョン',
    goVersionDescription: 'バックエンドが使用しているGo言語のバージョン',
    docreaderVersionLabel: 'Docreaderバージョン',
    docreaderVersionDescription: '現在の文書解析サービス（weknora-docreader）のビルドバージョン',
    startedAtLabel: 'サービス起動日時',
    startedAtDescription: '現在のバックエンドプロセスが最後に起動した日時',
    uptimeLabel: '稼働時間',
    uptimeDescription: '今回の起動からサービスが稼働している時間',
    uptimeDays: '{n}日',
    uptimeHours: '{n}時間',
    uptimeMinutes: '{n}分',
    uptimeSeconds: '{n}秒',
    dbVersionLabel: 'データベースバージョン',
    dbVersionDescription: '現在のデータベースマイグレーションのバージョン',
    dbMigrationFailedTag: 'マイグレーション失敗',
    dbMigrationFailedTitle: 'データベースのマイグレーションに失敗しました',
    dbMigrationFailedDesc: '起動時のデータベースマイグレーションが正常に完了しませんでした。一部のテーブルやインデックスが欠落し、Wikiの取り込みやナレッジグラフなどの機能が動作しない可能性があります。まず下記のトラブルシューティングガイドを確認し、解決しない場合はリンクから問題を報告してください。',
    dbMigrationViewDocs: 'トラブルシューティングガイドを表示',
    dbMigrationReportIssue: '解決しない場合は問題を報告',
    keywordIndexEngineLabel: 'キーワードインデックスエンジン',
    keywordIndexEngineDescription: '現在使用中のキーワードインデックスエンジン',
    vectorStoreEngineLabel: 'ベクトルストアエンジン',
    vectorStoreEngineDescription: '現在使用中のベクトルストアエンジン',
    graphDatabaseEngineLabel: 'グラフデータベースエンジン',
    graphDatabaseEngineDescription: '現在使用中のグラフデータベースエンジン',
    unknown: '不明',
    messages: {
      fetchFailed: 'システム情報の取得に失敗しました',
      networkError: 'ネットワークエラーです。しばらくしてから再試行してください'
    },
    globalSettings: {
      title: 'システム設定',
      description: 'プラットフォーム全体のランタイム設定です。保存するとすべてのワークスペースに即座に反映されます。システム管理者のみが表示・編集できます。',
      loading: '読み込み中...',
      empty: '設定可能なシステム設定はありません',
      saving: '保存中',
      saved: '保存しました',
      saveAnnouncement: '{label}を保存しました',
      badgeRequiresRestart: '再起動が必要',
      badgeSecret: 'シークレット',
      badgeHighRisk: '高リスク',
      badgeOverride: '上書き済み',
      badgeOverrideTooltip: 'この値は管理者によってデータベースに保存されており、環境変数と組み込みのデフォルト値を上書きしています。',
      modifiedAt: '最終更新: {value}',
      tagInputPlaceholder: 'Enterキーで項目を追加します。例: example.com / *.foo.com / 10.0.0.0/8',
      priorityHint: {
        disclosure: '設定のソースと優先順位',
        tier1: 'このページで保存した項目（「上書き済み」表示）が常に優先され、環境変数は無視されます。',
        tier2: 'ここで保存していない項目は環境変数の値が使われ、環境変数が未設定の場合は組み込みのデフォルト値が使われます。',
        tier3: '項目を環境変数の管理下に戻すには、その行の「リセット」ボタンをクリックしてください。'
      },
      sections: {
        access: {
          title: '登録とアクセス',
          description: '登録方法、パスワード要件、ワークスペース作成のルールを管理します。'
        },
        tenant: {
          title: 'ワークスペースのデフォルト値',
          description: '既存のワークスペースを変更せずに、新規ワークスペースの初期クォータと互換動作を設定します。'
        },
        runtime: {
          title: 'ランタイムと並列実行',
          description: 'バックグラウンドのワーカプールとモデルサービスの並列実行数を設定します。',
          restartHint: 'ワーカ設定の変更には再起動が必要です'
        },
        security: {
          title: 'ネットワークセキュリティ',
          description: 'SSRF許可リストと、Dockerサンドボックスの利用可否を管理します（ローカルのdocker.sockはホストのroot権限に相当します）。'
        },
        other: {
          title: 'その他の設定',
          description: 'このデプロイ環境に存在する、標準の製品グループに属さない設定です。'
        }
      },
      runtime: {
        title: 'タスクキューのランタイム',
        description: '各ワーカプールのバックグラウンドキューの負荷とプロセスごとの容量をリアルタイムに表示します。タスクの詳細と安全な操作が可能で、5秒ごとに自動更新されます。',
        refresh: '更新',
        autoRefresh: '自動更新（5秒ごと）',
        loading: '読み込み中...',
        retry: '再試行',
        unavailableTitle: 'タスクキューを利用できません',
        unavailable: 'このデプロイ環境にはRedis / asynqキューがありません（Liteモード）。表示できる情報はありません。',
        empty: 'キューのデータがありません',
        detailsTitle: 'キューの詳細',
        detailsDescription: '各処理レーンのリアルタイムな負荷と待ち時間です。「失敗（停止）」はタスクが再試行の上限を超え、自動実行が停止したことを示します。',
        poolsTitle: 'ワーカプール',
        poolsDescription: '各ステージには保証容量があり、コア解析とエンリッチメントは共有弾性プールも借用できます。',
        perInstance: 'カードの値はクラスタ全体の実行中/容量です',
        poolConfigured: '1プロセスあたり{value}',
        poolInstances: '{value}インスタンス',
        poolUtilization: '使用率{value}%',
        queueCount: '{value}キュー',
        weightShort: '重み{value}',
        footnote: 'カードの値はクラスタ全体の実行中/リアルタイムの容量です。プロセスごとの設定は変更後に再起動が必要です。共有弾性プールはコア解析とエンリッチメントのキューのみを処理します。',
        updatedAt: '更新日時: {value}',
        errors: {
          generic: 'キュー統計の読み込みに失敗しました'
        },
        summary: {
          title: 'ランタイム概要',
          active: '実行中',
          pending: '保留中',
          retry: '再試行中',
          archived: '失敗（停止）'
        },
        columns: {
          queue: 'キュー',
          active: '実行中',
          pending: '保留中',
          scheduled: '予約済み',
          retry: '再試行',
          archived: '失敗',
          completed: '完了',
          latency: '最長待ち時間',
          status: 'ステータス'
        },
        status: {
          working: '処理中',
          waiting: '待機中',
          idle: 'アイドル',
          actionRequired: '要対応',
          retrying: '再試行中',
          paused: '一時停止'
        },
        failedNotice: {
          title: '{count}件の失敗したタスクに対応が必要です',
          description: '下の表で赤い「失敗」の件数を開くと原因を確認できます。原因を解消してから再試行してください。'
        },
        tasks: {
          title: 'タスクの詳細 · {queue}',
          description: 'すべてのタスク状態を確認できます。予約済みと再試行のタスクは次回実行が早い順、その他の状態は新しい順に表示されます。',
          listTitle: '{state}のタスク',
          openAria: '{queue}の{state}のタスク{count}件を表示',
          unavailable: 'このデプロイ環境ではタスクの詳細を利用できません',
          empty: 'このキューに{state}のタスクはありません',
          loadError: 'タスク詳細の読み込みに失敗しました',
          loadMore: 'さらに読み込む',
          loadedSummary: '{count}件を読み込みました。スクロールまたはタップでさらに読み込みます',
          loadedAll: '全{count}件を読み込みました',
          loadingMore: '読み込み中…',
          attempts: '試行{current}/{max}',
          unknownTarget: '関連するオブジェクトを特定できません',
          knowledgeBaseLabel: 'ナレッジベースID',
          knowledgeLabel: 'ドキュメントID',
          taskIDLabel: '業務タスクID',
          tenantLabel: 'ワークスペースID',
          sourceLabel: 'ソースID',
          targetLabel: 'ターゲットID',
          sourceKBLabel: '移動元ナレッジベース',
          targetKBLabel: '移動先ナレッジベース',
          dataSourceLabel: 'データソースID',
          syncLogLabel: '同期ログID',
          knowledgeCountLabel: 'ドキュメント数',
          enqueuedAt: 'キュー投入日時',
          startedAt: '開始日時',
          nextProcessAt: '次回実行',
          lastFailedAt: '最終失敗',
          completedAt: '完了日時',
          deadline: '実行期限',
          worker: '実行インスタンス',
          health: '実行状態',
          orphaned: '実行インスタンスとの接続が切れています。復旧を待っています',
          cancel: 'タスクを中止',
          cancelConfirm: '業務側の中止フローを使用し、同じドキュメントの関連タスクも停止します。中止しますか？',
          runNow: '今すぐ実行',
          runNowConfirm: 'このタスクは再試行回数をリセットせずに、すぐ保留中に移動します。続行しますか？',
          deleteRecord: 'レコードを削除',
          deleteConfirm: 'このキューからレコードを削除するだけです。元のタスクが実行・完了されることはなく、履歴ログは残ります。削除しますか？',
          purgeArchived: '失敗したタスクをすべて削除',
          purgeArchivedConfirm: 'このキューから失敗レコード{count}件をまとめて削除します。キュー内の失敗タスクのみを削除するもので、実行中や保留中のタスクには影響せず、すでに失敗したドキュメントの業務ステータスも戻りません。削除しますか？',
          purgeArchivedSuccess: '失敗したタスク{count}件を削除しました',
          purgeArchivedError: '失敗したタスクの削除に失敗しました',
          stateFilter: 'タスク状態で絞り込み',
          states: {
            active: '実行中',
            pending: '保留中',
            scheduled: '予約済み',
            retry: '再試行中',
            archived: '失敗',
            completed: '完了'
          },
          guides: {
            active: '実行インスタンス、開始日時、実行期限、接続切れの状態を確認できます。業務ステータスを安全に更新できる場合にのみ中止できます。',
            pending: '保留中のタスクはまだ取得されていません。中止すると、Redisのデータを削除するだけでなく業務ステータスも更新されます。',
            scheduled: '予約済みのタスクは前倒しで実行できます。ドキュメント処理のタスクは業務側の安全な中止も利用できます。',
            retry: '最後のエラー、試行回数、次回実行日時をもとに、今すぐ実行するか中止するかを判断してください。',
            archived: '再実行する前に根本原因を解消してください。レコードを削除しても元の業務タスクは完了しません。',
            completed: '結果の保持期間が設定された最近の完了タスクのみを表示します。管理操作は利用できません。'
          },
          actionSuccess: {
            cancel: 'タスクを中止しました',
            run_now: 'タスクを保留中に移動しました',
            delete: '失敗レコードを削除しました'
          },
          actionError: {
            cancel: 'タスクの中止に失敗しました',
            run_now: 'タスクの実行に失敗しました',
            delete: 'レコードの削除に失敗しました'
          },
          taskTypes: {
            documentProcess: 'ドキュメント解析',
            manualProcess: '手動での再処理',
            temporaryDocumentProcess: 'チャット添付ファイルの解析',
            postProcess: 'ドキュメントの後処理',
            summary: '要約の生成',
            tableSummary: '表の要約の生成',
            question: '質問の生成',
            multimodal: '画像のマルチモーダル処理',
            graph: 'ナレッジグラフの抽出',
            sync: 'データソースの同期',
            faqImport: 'FAQのインポート',
            batchReparse: '一括再解析',
            batchDelete: '一括削除',
            move: 'ドキュメントの移動',
            indexDelete: 'インデックスの削除',
            kbClone: 'ナレッジベースの複製',
            kbDelete: 'ナレッジベースの削除',
            wikiIngest: 'Wikiコンテンツの生成',
            wikiFinalize: 'Wikiの最終処理'
          }
        },
        models: {
          title: 'モデルの並列実行',
          description: 'バックグラウンドタスクによるモデルサービスの実際の並列実行数です。上のセクションはタスクのスケジューリング、こちらは別段階のモデルサービスのレート制限を示します。',
          scope: '実行中はクラスタ全体 · 待機中は現在のインスタンス',
          disabled: 'バックグラウンドのモデル並列実行の制御は無効です。モデルのデフォルトの並列実行上限はシステム設定で変更できます。',
          empty: 'モデルの実行データはまだありません。モデルが初めてバックグラウンドで呼び出されると表示されます。',
          backgroundOnly: 'バックグラウンドタスクのみが対象で、対話型のチャットは含まれません',
          columns: {
            model: 'モデルID',
            active: '実行中',
            waiting: 'レート制限待ち',
            usage: '並列実行数'
          },
          status: {
            queued: 'レート制限中',
            full: '上限に到達'
          }
        },
        pools: {
          core: 'コア解析',
          postprocess: '後処理オーケストレーション',
          enrichment: 'エンリッチメント',
          maintenance: 'メンテナンスと同期',
          shared: '共有弾性プール',
          wiki: 'Wikiプール'
        },
        poolDescriptions: {
          core: 'ドキュメント解析と手動での再解析の保証容量',
          postprocess: '解析の最終処理とエンリッチメントの振り分け',
          enrichment: '要約、画像、グラフ、質問の生成',
          maintenance: 'データソース同期、一括処理、削除のクリーンアップ',
          shared: 'バックログに応じてコア解析またはエンリッチメントが借用',
          wiki: 'Wikiコンテンツの生成と全体の最終処理'
        },
        queueNames: {
          default: 'ドキュメント解析',
          chat_attachment: 'チャット添付ファイル',
          postprocess: '後処理',
          summary: '要約',
          sync: 'データソース同期',
          low: 'メンテナンスと一括処理',
          multimodal: 'マルチモーダル',
          graph: 'グラフ抽出',
          question: '質問',
          wiki: 'Wikiパイプライン'
        },
        queueDescriptions: {
          default: 'ドキュメント解析、手動での再解析',
          chat_attachment: 'セッション内でアップロードされた添付ファイルの解析',
          postprocess: '解析の最終処理、エンリッチメントの振り分け',
          summary: 'ドキュメントと表の要約',
          sync: '手動およびスケジュール同期',
          low: 'FAQインポート、一括再解析、クリーンアップ',
          multimodal: '画像のOCR、視覚キャプション',
          graph: 'チャンク単位のグラフ抽出',
          question: 'チャンク単位の質問生成',
          wiki: 'コンテンツ生成、インデックスの最終処理'
        }
      },
      keyLabels: {
        auth: {
          registration_mode: '登録モード',
          default_tenant_mode: 'デフォルトのワークスペース作成方式',
          complex_password_enabled: '複雑なパスワードを必須にする'
        },
        ssrf: {
          whitelist: 'SSRF保護の許可リスト'
        },
        sandbox: {
          docker_enabled: 'Dockerサンドボックスを有効化'
        },
        tenant: {
          max_owned_per_user: 'ユーザごとの所有ワークスペース数の上限',
          self_service_creation_enabled: 'ユーザによるワークスペース作成を許可',
          default_storage_quota_gb: '新規ワークスペースのデフォルトストレージクォータ（GB）',
          default_storage_quota_mb: '新規ワークスペースのデフォルトストレージクォータ（MB）',
          auto_create_api_key: '新規ワークスペースにAPIキーを自動作成',
          auto_accept_invitation: '招待した登録済みユーザを自動的に参加させる'
        },
        asynq: {
          core_concurrency: 'コア解析の保証並列実行数',
          postprocess_concurrency: '後処理の並列実行数',
          enrichment_concurrency: 'エンリッチメントの保証並列実行数',
          maintenance_concurrency: 'メンテナンスの並列実行数',
          shared_concurrency: '共有弾性プールの並列実行数',
          wiki_concurrency: 'Wikiワーカの並列実行数'
        },
        model: {
          max_concurrency: 'モデルごとのデフォルトの並列実行上限'
        }
      },
      keyDescriptions: {
        auth: {
          registration_mode: '登録モード。公開登録では誰でもアカウントを作成できます。招待登録には有効な招待リンクが必要です。登録禁止でも既存アカウントは招待を承諾できます。保存後すぐに反映されます。',
          default_tenant_mode: '公開登録後のワークスペース作成方式です。create_personalはオーナー権限のワークスペースを作成し、tenantlessはアカウントのみを作成して、ユーザが招待を承諾するかワークスペースを作成するまで待ちます。新規ユーザにのみ適用されます。',
          complex_password_enabled: '複雑なパスワードを必須にするかどうかです。有効にすると、パスワードに大文字・小文字・数字・特殊文字を含める必要があります。変更はすぐに反映され、新規登録ユーザおよび新たなパスワード変更・リセットにのみ適用されます。特殊文字は{specialChars}です'
        },
        ssrf: {
          whitelist: 'SSRF保護の許可リストです。example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1のような項目を指定できます。保存後すぐに反映されます。SSRF_WHITELIST_EXTRA環境変数は引き続きデプロイ担当者が管理し、ここでは上書きされません。'
        },
        sandbox: {
          docker_enabled: 'Dockerサンドボックスのバックエンドを許可します。ローカルのdocker.sockはホストのroot権限に相当するため、デフォルトでは無効です。有効化できるのはシステム管理者のみで、変更はすぐに反映されます。デーモンのソケットをマウントするプライベートな単一ノード環境、またはTLSで保護されたリモートのtcp://エンドポイントを使用する場合にのみ有効にしてください。'
        },
        tenant: {
          max_owned_per_user: 'スーパーユーザ以外がセルフサービスで所有できるワークスペース数の上限です。ワークスペース作成のたびに参照され、保存後すぐに反映されます。0を指定すると組み込みのデフォルト値10が使われ、負の値を指定すると上限が無効になります（公開環境では非推奨）。',
          self_service_creation_enabled: 'スーパーユーザ以外が自分でワークスペースを作成できるかどうかです。無効にすると、一般ユーザは招待によって既存のワークスペースに参加することしかできません。ワークスペース横断のスーパーユーザは対象外です。すぐに反映されます。',
          default_storage_quota_gb: '新規ワークスペースの作成時に割り当てられるデフォルトのストレージクォータ（GB）です。ベクトル、原本、テキスト、インデックスなどのデータが対象です。作成時にのみ参照されるため、変更は新規作成のワークスペースにのみ適用され、既存のワークスペースには遡って反映されません。0または負の値の場合は組み込みのデフォルト値10GBが使われます。',
          default_storage_quota_mb: '登録時に自動作成される個人スペースにも適用します。正の値はGB設定より優先され、0以下はGB設定を使用します。各設定はデータベース、環境変数、既定値の順に解決し、新規スペースにのみ適用します。',
          auto_create_api_key: '新規ワークスペースにfull_accessのAPIキーを自動作成し、作成レスポンスで平文のトークンを返します。従来の動作に依存する連携でのみ利用してください。デフォルトでは無効で、APIキーは明示的に作成することを推奨します。',
          auto_accept_invitation: '有効にすると、登録済みユーザをメールで招待した際に、受信確認を待たずにすぐメンバーとして追加されます。無効の場合、招待された側がメールから承諾する必要があります。すぐに反映されます。'
        },
        asynq: {
          core_concurrency: 'ドキュメント解析と手動での再解析のプロセスごとの保証並列実行数です。コア解析は共有弾性プールも借用できます。最小値は1で、サービスの再起動が必要です。',
          postprocess_concurrency: 'プロセスごとの並列実行数で、解析完了後の軽量な仕上げ処理とエンリッチメントの振り分け専用です。最小値は1で、サービスの再起動が必要です。',
          enrichment_concurrency: '要約、画像、グラフ、質問の生成のプロセスごとの保証並列実行数です。エンリッチメントは共有弾性プールも借用できます。最小値は1で、サービスの再起動が必要です。',
          maintenance_concurrency: 'データソース同期、一括処理、クリーンアップのプロセスごとの並列実行数で、ユーザ向けのパイプラインから完全に分離されています。最小値は1で、サービスの再起動が必要です。',
          shared_concurrency: 'コア解析とエンリッチメントが共有する、プロセスごとの弾性的な並列実行数です。バックログのある側が自動的に借用します。最小値は1で、サービスの再起動が必要です。',
          wiki_concurrency: 'Wiki専用ワーカプールのプロセスごとの並列実行数で、上流のタスクから分離されています。最小値は1で、サービスの再起動が必要です。'
        },
        model: {
          max_concurrency: '単一モデルへのバックグラウンド（取り込み・エンリッチメント）呼び出しのデフォルトの並列実行上限です。モデルIDごとに管理され、すべてのレプリカで共有されます。呼び出しのたびに参照され、再起動なしですぐに適用されます。0または負の値の場合はデフォルトの上限が無効になります（各モデルはモデル管理で設定した独自の上限に従います）。対象はバックグラウンドタスクのみで、対話型のチャットには影響しません。'
        }
      },
      enumLabels: {
        auth: {
          registration_mode: {
            self_serve: 'セルフサービス（誰でも登録可能）',
            invite_register: '招待リンクでのみ登録可能',
            invite_only: '登録禁止（既存アカウントは招待を承諾可能）'
          },
          default_tenant_mode: {
            create_personal: '個人ワークスペースを作成',
            tenantless: 'ワークスペースを作成しない'
          }
        }
      },
      confirm: {
        confirmBtn: '保存を確認',
        cancelBtn: 'キャンセル',
        emptyValue: '（空）',
        bodyAuthRegistrationMode: '「{label}」を次の値に変更します: {value}\n\nself_serveに切り替えると、インターネット上の誰でもアカウントを登録できるようになります。意図した変更かどうか確認してください。',
        bodySandboxDockerEnabled: '有効にすると、ワークスペース管理者がサンドボックスをローカルのDockerデーモンに接続できるようになります。ローカルのdocker.sockはホストのroot権限に相当します。デーモンをマウントするプライベートな単一ノード環境、またはTLSで保護されたリモートのtcp://エンドポイントを使用する場合にのみ利用してください。'
      },
      listConfirm: {
        ssrf: {
          whitelist: {
            add: {
              header: 'SSRF許可リストに項目を追加',
              body: '{entry}をSSRF許可リストに追加しますか？この項目に一致するホスト/IP/CIDRはSSRF保護の対象外となり、エージェントが内部サービスにアクセスできる可能性があります。完全に信頼できる項目のみを追加してください。',
              confirmBtn: '追加'
            },
            remove: {
              header: 'SSRF許可リストから項目を削除',
              body: '{entry}をSSRF許可リストから削除しますか？削除後、この項目は再びSSRF保護によってブロックされます。',
              confirmBtn: '削除'
            }
          }
        }
      },
      messages: {
        loadFailed: 'システム設定の読み込みに失敗しました',
        saveSuccess: '保存しました',
        saveFailed: '保存に失敗しました'
      },
      reset: {
        label: 'リセット',
        tooltip: 'UIでの上書きを解除し、環境変数または組み込みのデフォルト値に戻します',
        confirmBtn: 'リセットを確認',
        confirmBody: '「{label}」をリセットしますか？データベースの上書き設定を削除し、環境変数または組み込みのデフォルト値に戻します。',
        success: 'デフォルト値にリセットしました',
        failed: 'リセットに失敗しました'
      },
      userList: {
        title: 'プラットフォームユーザー一覧',
        rowLabel: 'ユーザー管理',
        rowDesc: 'ワークスペース横断の登録ユーザーを、ページ送りとユーザー名/メール絞り込み付きで表示します。',
        searchPlaceholder: 'ユーザー名またはメールで絞り込み',
        refresh: '更新',
        active: '有効',
        inactive: '無効',
        colUsername: 'ユーザー名',
        colStatus: '状態',
        colRole: 'ロール',
        colCreated: '作成日',
        colAction: '操作',
        prev: '前へ',
        next: '次へ',
        pageCount: 'ページ内 {total} 件',
        enable: '有効化',
        disable: '無効化',
        resetPassword: 'パスワードリセット',
        confirmDisable: 'アカウント {name} を無効化しますか？有効なセッションは即時に失効します。',
        confirmEnable: 'アカウント {name} を有効化しますか？',
        enabledMsg: '{name} を有効化しました',
        disabledMsg: '{name} を無効化しました',
        actionFailed: '操作に失敗しました',
        loadFailed: 'ユーザー一覧の読み込みに失敗しました',
        roleAdmin: 'システム管理者',
        roleUser: '一般ユーザー',
        confirmRevoke: '{name} のシステム管理者権限を取り消しますか？',
        confirmPromote: '{name} をシステム管理者に昇格させますか？',
        revokeBtn: '取り消す',
        promoteBtn: '昇格',
        revokedMsg: '{name} の管理者権限を取り消しました',
        promotedMsg: '{name} をシステム管理者に昇格しました',
        roleFailed: 'ロールの変更に失敗しました'
      },
      passwordReset: {
        dialogTitle: '他のユーザのパスワードをリセット',
        warning: 'これは高リスクの操作です。ユーザのメールアドレスをよく確認してください。ここで自分のパスワードをリセットすることはできません。',
        emailLabel: 'ユーザのメールアドレス',
        newPasswordLabel: '新しいパスワード',
        newPasswordPlaceholder: '新しいパスワードを入力してください',
        confirmPasswordLabel: '新しいパスワード（確認）',
        confirmPasswordPlaceholder: '新しいパスワードをもう一度入力してください',
        confirmBtn: 'リセットを確認',
        success: 'パスワードをリセットし、そのユーザの既存セッションを無効にしました',
        failed: 'パスワードのリセットに失敗しました'
      },
      createUser: {
        action: 'ユーザを作成',
        dialogTitle: '新しいユーザを作成',
        warning: 'これは高リスクの操作です。ユーザ名とメールアドレスをよく確認してください。これらは新しいユーザのログインに使用されます。',
        usernameLabel: 'ユーザ名',
        usernamePlaceholder: '2〜50文字',
        emailLabel: 'メールアドレス',
        emailPlaceholder: '有効なメールアドレスを入力してください',
        autoGenerateLabel: 'ランダムなパスワードを自動生成',
        newPasswordLabel: '新しいパスワード',
        newPasswordPlaceholder: '英字と数字を含む8〜32文字',
        confirmPasswordLabel: '新しいパスワード（確認）',
        confirmPasswordPlaceholder: '新しいパスワードをもう一度入力してください',
        confirmBtn: 'ユーザを作成',
        success: 'ユーザを作成しました',
        successIdempotent: 'ユーザはすでに存在するため、変更はありません',
        missingPassword: 'ユーザは作成されましたが、生成されたパスワードが返されませんでした。パスワードをリセットしてアクセスを復旧してください。',
        failed: 'ユーザの作成に失敗しました',
        validation: {
          usernameRequired: 'ユーザ名を入力してください',
          usernameLength: 'ユーザ名は2〜50文字で入力してください',
          emailRequired: 'メールアドレスを入力してください',
          emailInvalid: '有効なメールアドレスを入力してください',
          confirmRequired: '新しいパスワードをもう一度入力してください',
          passwordMismatch: 'パスワードが一致しません'
        },
        generated: {
          successTitle: 'ユーザを作成しました',
          successBody: 'このユーザにランダムなパスワードを生成しました。パスワードは一度だけ表示されます。',
          usernameLabel: 'ユーザ名',
          emailLabel: 'メールアドレス',
          passwordLabel: '生成されたパスワード',
          copyBtn: 'アカウント情報をコピー',
          copySuccess: 'アカウント情報をクリップボードにコピーしました',
          acknowledgeBtn: 'パスワードを保存しました'
        }
      },
      bulkApply: {
        label: '既存のすべてのワークスペースに適用',
        tooltip: '値を保存してもデフォルトでは新規ワークスペースにのみ適用されます。ここをクリックすると既存のすべてのワークスペースも上書きします。',
        confirmBtn: '適用を確認',
        confirmBody: '既存のすべてのワークスペースのストレージクォータを{value} {unit}に上書きします。運用担当者が個別に調整したワークスペースも上書きされます。続行しますか？',
        success: '{count}件のワークスペースのストレージクォータを{value} {unit}に更新しました',
        failed: 'すべてのワークスペースへの適用に失敗しました'
      },
      audit: {
        tabLabel: '監査ログ',
        description: 'プラットフォームレベルのイベントです。システム設定の変更、システム管理者の付与・解除、クォータの一括同期などを新しい順に表示します。',
        refresh: '更新',
        retry: '再試行',
        loading: '読み込み中…',
        end: 'これ以上の履歴はありません。',
        empty: 'プラットフォームレベルの監査イベントはまだありません。',
        forbidden: 'プラットフォームの監査ログを表示する権限がありません。',
        systemActor: 'システム',
        errors: {
          generic: '監査ログの読み込みに失敗しました'
        },
        actorRole: {
          system_admin: 'システム管理者'
        },
        columns: {
          time: '日時',
          actor: '実行者',
          action: 'イベント',
          target: '対象',
          path: 'リクエスト',
          outcome: '結果'
        },
        action: {
          'system.setting_changed': 'システム設定の変更',
          'system.admin_promoted': 'システム管理者の付与',
          'system.api_key_created': 'プラットフォームAPIキーの作成',
          'system.api_key_revoked': 'プラットフォームAPIキーの取り消し',
          'system.admin_revoked': 'システム管理者の解除',
          'system.user_password_reset': 'ユーザパスワードのリセット',
          'system.user_created': 'ユーザの作成',
          'system.queue_task_retried': '失敗タスクの再実行',
          'system.queue_task_deleted': '失敗タスクのレコード削除',
          'system.queue_task_run_now': 'キュータスクの即時実行',
          'system.queue_task_cancelled': 'キュータスクの中止',
          'system.queue_archived_purged': '失敗タスクの一括削除'
        },
        outcome: {
          success: '成功',
          denied: '拒否'
        },
        target: {
          bulkQuota: '一括同期: デフォルトのストレージクォータ',
          bulkQuotaDiff: '{count}件のワークスペースに適用（{gb}GB）',
          promoteIdempotent: '対象はすでにシステム管理者でした（冪等）',
          revokeNoop: '対象はシステム管理者ではありませんでした（冪等）',
          requiredRole: '必要なロール: {role}',
          valueNull: '（未設定）'
        },
        expanded: {
          actorId: '実行者ID',
          targetUserId: '対象ユーザID',
          targetType: '対象の種類',
          targetId: '対象ID',
          details: '生の詳細'
        },
        drawer: {
          sectionSummary: 'イベントの概要',
          sectionIdentifiers: '関連ID',
          sectionRequest: 'リクエスト',
          targetChange: '変更内容',
          requestMethod: 'メソッド'
        }
      }
    }
  }
}
