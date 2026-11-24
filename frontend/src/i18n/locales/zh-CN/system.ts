export default {
  documentProgress: {
    queueView: '队列任务',
    title: '资料与 Wiki 进度',
    description: '分别查看当前入库和 Wiki 处理情况，恢复后的原始失败记录继续保留。',
    refresh: '刷新',
    search: '搜索资料或知识库',
    searchButton: '搜索',
    filter: '进度筛选',
    count: '启动数量（1–100）',
    batchLimit: '最多启动',
    totalCount: '共 {count} 份资料',
    batchStart: '批量整理 Wiki',
    confirmBatchScoped: '按当前搜索“{search}”，从等待最久的资料开始，安排最多 {count} 份 Wiki 待办？',
    confirmBatchAll: '从全平台等待最久的资料开始，安排最多 {count} 份 Wiki 待办？',
    batchDisabledStatus: '切换到“未完成”或“全部资料”后可批量安排 Wiki',
    batchDisabledEmpty: '当前筛选范围内没有可安排的资料',
    confirmOne: '使用“{title}”的已有进度，安排 Wiki 整理？',
    attempt: '第 {value} 次处理',
    pendingOps: '{count} 项待办 · 重试计数 {retries}',
    historyCount: '{count} 条记录',
    details: '详情',
    startNow: '立即整理',
    currentStatus: '当前状态',
    stagesTitle: '处理阶段',
    noStages: '暂无阶段记录。',
    emptyTitle: '暂无匹配的资料',
    emptyUnfinished: '当前没有未完成的资料或 Wiki 待办。',
    viewAll: '查看全部资料',
    clearSearch: '清空搜索',
    history: '失败历史',
    historyDescription: '这里保留原始失败记录，也包含后来已恢复的失败；历史记录不代表资料当前仍在失败。',
    noHistory: '该资料没有归档失败记录。',
    failureAttempts: '累计失败 {count} 次',
    historyLimit: '显示最近 50 条，原始历史继续保留。',
    unfinished: '未完成',
    all: '全部资料',
    document: '资料',
    ingestion: '入库',
    wiki: 'Wiki',
    actions: '操作',
    loadError: '暂时无法读取资料进度，请刷新重试。',
    started: '已安排 {count} 份，另有 {queued} 份已在队列中；已有进度保留。',
    startError: '队列交接中断前已接收 {count} 份；请刷新后再安排剩余资料。',
    states: {
      ready: '已入库',
      pending: '待处理',
      processing: '处理中',
      finalizing: '收尾中',
      completed: '已完成',
      failed: '失败',
      skipped: '已跳过',
      cancelled: '已取消',
      deleting: '删除中',
      queued: '排队中',
      retrying: '重试中',
      disabled: '未启用',
      notStarted: '尚未开始'
    },
    stages: {
      docreader: '文档解析',
      chunking: '分块',
      embedding: '索引构建',
      multimodal: '图片处理',
      postprocess: '后处理'
    }
  },
  system: {
    title: '系统信息',
    sectionDescription: '查看系统版本信息和用户账户配置',
    loadingInfo: '正在加载信息...',
    retry: '重试',
    versionLabel: '应用版本',
    versionDescription: '当前应用服务（weknora-app）的版本号',
    frontendVersionLabel: 'UI 版本',
    frontendVersionDescription: '当前 UI 界面（weknora-ui）的构建版本号',
    versionMismatch: '与应用版本不一致',
    buildTimeLabel: '构建时间',
    buildTimeDescription: '系统构建的时间',
    goVersionLabel: 'Go 版本',
    goVersionDescription: '后端使用的 Go 语言版本',
    docreaderVersionLabel: 'Docreader 版本',
    docreaderVersionDescription: '当前文档解析服务（weknora-docreader）的构建版本号',
    startedAtLabel: '服务启动时间',
    startedAtDescription: '当前后端进程最近一次启动的时刻',
    uptimeLabel: '运行时长',
    uptimeDescription: '自本次启动以来的连续运行时间',
    uptimeDays: '{n} 天',
    uptimeHours: '{n} 小时',
    uptimeMinutes: '{n} 分钟',
    uptimeSeconds: '{n} 秒',
    dbVersionLabel: '数据库版本',
    dbVersionDescription: '当前数据库迁移版本号',
    dbMigrationFailedTag: '迁移失败',
    dbMigrationFailedTitle: '数据库迁移失败',
    dbMigrationFailedDesc: '启动时数据库迁移未成功完成，部分表或索引可能未创建，会导致 Wiki、知识图谱等功能异常。建议先查看排查文档自助修复；如仍无法解决，请通过下方链接反馈。',
    dbMigrationViewDocs: '查看排查文档',
    dbMigrationReportIssue: '无法修复？提交 Issue',
    keywordIndexEngineLabel: '关键词索引引擎',
    keywordIndexEngineDescription: '当前使用的关键词索引引擎',
    vectorStoreEngineLabel: '向量存储引擎',
    vectorStoreEngineDescription: '当前使用的向量存储引擎',
    graphDatabaseEngineLabel: '图数据库引擎',
    graphDatabaseEngineDescription: '当前使用的图数据库引擎',
    unknown: '未知',
    globalSettings: {
      title: '系统设置',
      description: '平台级运行时配置，保存后立即对所有空间生效。仅系统管理员可见可改。',
      loading: '加载中...',
      empty: '暂无可配置的系统设置',
      saving: '保存中',
      saved: '已保存',
      saveAnnouncement: '{label} 已保存',
      badgeRequiresRestart: '需重启',
      badgeSecret: '敏感',
      badgeHighRisk: '高风险',
      badgeOverride: '已覆盖',
      badgeOverrideTooltip: '该值已由管理员保存到数据库，覆盖了环境变量与默认值',
      modifiedAt: '上次修改：{value}',
      tagInputPlaceholder: '回车添加条目，例：example.com / *.foo.com / 10.0.0.0/8',
      audit: {
        tabLabel: '审计日志',
        description: '记录平台级操作：系统设置变更、系统管理员授予/回收、配额批量同步等。按时间倒序展示。',
        refresh: '刷新',
        retry: '重试',
        loading: '加载中...',
        end: '已经到底了。',
        empty: '暂无平台级审计事件。',
        forbidden: '无权查看平台审计日志。',
        systemActor: '系统',
        drawer: {
          sectionSummary: '事件概要',
          sectionIdentifiers: '关联标识',
          sectionRequest: '请求信息',
          targetChange: '变更内容',
          requestMethod: '请求方法'
        },
        expanded: {
          actorId: '操作人 ID',
          targetUserId: '目标用户 ID',
          targetType: '目标类型',
          targetId: '目标 ID',
          details: '原始详情'
        },
        target: {
          bulkQuota: '批量同步：默认存储配额',
          bulkQuotaDiff: '应用到 {count} 个空间（{gb} GB）',
          promoteIdempotent: '目标已是系统管理员（幂等）',
          revokeNoop: '目标本就不是系统管理员（幂等）',
          requiredRole: '需要角色：{role}',
          valueNull: '(空)'
        },
        outcome: {
          success: '成功',
          denied: '拒绝'
        },
        action: {
          'system.setting_changed': '系统设置变更',
          'system.admin_promoted': '授予系统管理员',
          'system.api_key_created': '创建平台 API Key',
          'system.api_key_revoked': '吊销平台 API Key',
          'system.admin_revoked': '回收系统管理员',
          'system.user_password_reset': '重置用户密码',
          'system.user_created': '创建用户',
          'system.queue_task_retried': '重新执行失败任务',
          'system.queue_task_deleted': '清除失败任务记录',
          'system.queue_task_run_now': '立即执行队列任务',
          'system.queue_task_cancelled': '终止队列任务',
          'system.queue_archived_purged': '清除全部失败任务'
        },
        columns: {
          time: '时间',
          actor: '操作人',
          action: '事件',
          target: '目标',
          path: '请求',
          outcome: '结果'
        },
        actorRole: {
          system_admin: '系统管理员'
        },
        errors: {
          generic: '加载审计日志失败'
        }
      },
      bulkApply: {
        label: '应用到所有现有空间',
        tooltip: '保存的值默认只对之后新建的空间生效；点击此按钮将当前值同步写入所有现有空间。',
        confirmBtn: '应用',
        confirmBody: '将把所有现有空间的存储配额覆盖为 {value} {unit}。如有空间被运维单独调整过的配额，也会一并被覆盖。是否继续？',
        success: '已将 {count} 个空间的存储配额更新为 {value} {unit}',
        failed: '应用到所有空间失败'
      },
      userList: {
        title: '平台用户列表',
        rowLabel: '用户管理',
        rowDesc: '查看平台上的全部注册用户（跨空间），支持分页与按用户名/邮箱筛选。',
        searchPlaceholder: '按用户名或邮箱筛选',
        refresh: '刷新',
        active: '正常',
        inactive: '已停用',
        colUsername: '用户名',
        colStatus: '状态',
        colRole: '角色',
        colCreated: '创建日期',
        colAction: '操作',
        prev: '上一页',
        next: '下一页',
        pageCount: '本页 {total} 条',
        enable: '启用',
        disable: '停用',
        resetPassword: '重置密码',
        confirmDisable: '确定停用账号 {name} 吗？将立即吊销其登录状态。',
        confirmEnable: '确定启用账号 {name} 吗？',
        enabledMsg: '已启用 {name}',
        disabledMsg: '已停用 {name}',
        actionFailed: '操作失败',
        loadFailed: '加载用户列表失败',
        roleAdmin: '系统管理员',
        roleUser: '普通用户',
        confirmRevoke: '确定撤销 {name} 的系统管理员权限吗？',
        confirmPromote: '确定将 {name} 提升为系统管理员吗？',
        revokeBtn: '撤销',
        promoteBtn: '提升',
        revokedMsg: '已撤销 {name} 的管理员权限',
        promotedMsg: '已将 {name} 提升为系统管理员',
        roleFailed: '修改角色失败'
      },
      passwordReset: {
        dialogTitle: '重置其他用户的密码',
        warning: '这是高风险操作。请核对用户邮箱；出于安全考虑，不能在这里重置自己的密码。',
        emailLabel: '用户邮箱',
        newPasswordLabel: '新密码',
        newPasswordPlaceholder: '请输入新密码',
        confirmPasswordLabel: '确认新密码',
        confirmPasswordPlaceholder: '再次输入新密码',
        confirmBtn: '重置',
        success: '密码已重置，该用户的现有会话已失效',
        failed: '重置密码失败'
      },
      createUser: {
        action: '创建用户',
        dialogTitle: '创建新用户',
        warning: '这是高风险操作。请仔细核对用户名和邮箱——它们将作为该用户登录的唯一凭据。',
        usernameLabel: '用户名',
        usernamePlaceholder: '2-50 个字符',
        emailLabel: '邮箱',
        emailPlaceholder: '请输入有效的邮箱地址',
        autoGenerateLabel: '自动生成随机密码',
        newPasswordLabel: '新密码',
        newPasswordPlaceholder: '8-32 个字符，包含字母和数字',
        confirmPasswordLabel: '确认新密码',
        confirmPasswordPlaceholder: '再次输入新密码',
        confirmBtn: '创建用户',
        success: '用户已创建',
        successIdempotent: '用户已存在，未做任何更改',
        missingPassword: '用户已创建，但未返回生成的密码。请使用重置密码来恢复访问。',
        failed: '创建用户失败',
        validation: {
          usernameRequired: '请输入用户名',
          usernameLength: '用户名长度必须为 2-50 个字符',
          emailRequired: '请输入邮箱',
          emailInvalid: '请输入有效的邮箱地址',
          confirmRequired: '请再次输入新密码',
          passwordMismatch: '两次输入的密码不一致'
        },
        generated: {
          successTitle: '用户已创建',
          successBody: '已为该用户生成一个随机密码。此密码只会显示一次。',
          usernameLabel: '用户名',
          emailLabel: '邮箱',
          passwordLabel: '生成的密码',
          copyBtn: '复制账号信息',
          copySuccess: '账号信息已复制到剪贴板',
          acknowledgeBtn: '我已保存密码'
        }
      },
      reset: {
        label: '重置',
        tooltip: '清除当前 UI 覆盖，恢复使用环境变量或内置默认值',
        confirmBtn: '重置',
        confirmBody: '确定要重置「{label}」吗？该操作会删除数据库中的覆盖值，回退到环境变量或内置默认值。',
        success: '已重置为默认值',
        failed: '重置失败'
      },
      messages: {
        loadFailed: '加载系统设置失败',
        saveSuccess: '已保存',
        saveFailed: '保存失败'
      },
      listConfirm: {
        ssrf: {
          whitelist: {
            remove: {
              header: '移除 SSRF 白名单条目',
              body: '确认从 SSRF 白名单中移除 {entry}？移除后该条目将重新受 SSRF 防护拦截。',
              confirmBtn: '移除'
            },
            add: {
              header: '添加 SSRF 白名单条目',
              body: '确认把 {entry} 加入 SSRF 白名单？该条目匹配到的主机 / IP / 网段会绕过 SSRF 防护，可能让 Agent 访问内网服务，请仅在确知用途时添加。',
              confirmBtn: '添加'
            }
          }
        }
      },
      confirm: {
        confirmBtn: '确认保存',
        cancelBtn: '取消',
        emptyValue: '（空）',
        bodyAuthRegistrationMode: '即将把「{label}」改为：{value}\n\n如果切到 self_serve，公网任何人都可以注册账号 — 务必确认是预期行为。',
        bodySandboxDockerEnabled: '打开后，空间管理员可以把沙箱指到本机 Docker。本机 docker.sock 等同宿主机 root，只适合已挂载 daemon 或配了 TLS 远程 tcp:// 的私有化单机。'
      },
      enumLabels: {
        auth: {
          default_tenant_mode: {
            create_personal: '自动创建个人空间',
            tenantless: '不自动创建空间'
          },
          registration_mode: {
            self_serve: '自助注册（任何人可注册）',
            invite_register: '仅限邀请注册（需有效邀请链接）',
            invite_only: '禁止注册（已有账号仍可接受邀请）'
          }
        }
      },
      keyDescriptions: {
        model: {
          max_concurrency: '后台任务（文档入库/富化）对单个模型的默认并发上限，按模型 ID 全副本共享。每次调用实时读取，修改后立即生效、无需重启。0 或负数表示关闭默认限制（各模型仍会尊重自身在模型管理里配置的上限）。仅影响后台任务，不影响交互式对话。'
        },
        asynq: {
          core_concurrency: '文档解析与手工重解析的每实例保底并发，可额外借用共享弹性池；修改后需重启。',
          postprocess_concurrency: '解析完成后的轻量收尾和富化扇出专用并发；修改后需重启。',
          enrichment_concurrency: '摘要、图片、图谱与问题生成的每实例保底并发，可额外借用共享弹性池；修改后需重启。',
          maintenance_concurrency: '数据源同步、批处理和清理任务的每实例并发，与用户面流水线硬隔离；修改后需重启。',
          shared_concurrency: '核心解析与内容富化共同使用的每实例弹性并发，由有积压的一侧自动借用；修改后需重启。',
          wiki_concurrency: '每个服务实例的 Wiki 专用 Worker 并发数，与上游任务池相互隔离。最小值为 1；修改后需重启服务进程方可生效。'
        },
        tenant: {
          max_owned_per_user: '每个非超管用户通过自助创建可拥有的最大空间数。每次创建空间时实时读取，修改后立即生效。0 表示使用内置默认值 10；负数表示完全关闭限制（不建议在公开部署使用）。',
          self_service_creation_enabled: '是否允许非超管用户主动创建空间。关闭后，普通用户只能通过邀请加入已有空间；跨空间超管仍可创建。修改后立即生效。',
          default_storage_quota_gb: '新建空间时默认分配的存储配额（GB），包含向量、原文、文本、索引等。仅在创建时读取，修改后只对之后新建的空间生效，不会回写已存在的空间。0 或负数表示使用内置默认值 10GB。',
          default_storage_quota_mb: '包括注册自动创建的个人空间。正数优先于 GB 设置；0 或负数沿用 GB 设置。每个设置按数据库、环境变量、内置默认值的顺序读取，仅影响之后新建的空间。',
          auto_create_api_key: '为新空间自动生成 full_access API Key，并在创建响应中返回明文 token。仅用于兼容依赖旧行为的集成；默认关闭，建议通过 API Key 管理显式创建。',
          auto_accept_invitation: '开启后，空间管理员通过邮箱邀请已注册用户时，对方会立即成为成员，不再经过收件箱确认。关闭时保持「发出邀请 → 被邀请人确认」流程。修改后立即生效。'
        },
        ssrf: {
          whitelist: 'SSRF 防护白名单。可填入 example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1。修改后立即生效。SSRF_WHITELIST_EXTRA 环境变量仍由部署方维护，不在此处覆盖。'
        },
        sandbox: {
          docker_enabled: '是否允许 Docker 沙箱后端。本机 docker.sock 等同宿主机 root，默认关闭。仅系统管理员可打开；打开后立即生效，无需重启。私有化单机且已挂载 daemon socket，或配置了带 TLS 的远程 tcp:// 时再启用。'
        },
        auth: {
          registration_mode: '注册模式。开放注册允许任何人创建账号；仅限邀请注册要求有效邀请链接；禁止注册不允许创建账号，但已有账号仍可接受邀请。保存后立即生效。',
          default_tenant_mode: '公开注册后的空间初始化策略。create_personal 会自动创建个人空间并授予 Owner；tenantless 仅创建账户，用户需要接受邀请或主动创建空间。只影响之后注册的用户。',
          complex_password_enabled: '是否启用复杂密码。开启后密码必须包含大小写字母、数字和特殊字符。修改后立即生效，只影响新注册用户或新密码修改/重置操作。特殊字符包含：{specialChars}'
        }
      },
      keyLabels: {
        model: {
          max_concurrency: '模型默认并发上限'
        },
        asynq: {
          core_concurrency: '核心解析保底并发数',
          postprocess_concurrency: '后处理编排并发数',
          enrichment_concurrency: '内容富化保底并发数',
          maintenance_concurrency: '维护与同步并发数',
          shared_concurrency: '共享弹性并发数',
          wiki_concurrency: 'Wiki Worker 并发数'
        },
        tenant: {
          max_owned_per_user: '每用户最大空间数',
          self_service_creation_enabled: '允许用户自助创建空间',
          default_storage_quota_gb: '新空间默认存储配额 (GB)',
          default_storage_quota_mb: '新空间默认存储配额 (MB)',
          auto_create_api_key: '创建空间时自动生成 API Key',
          auto_accept_invitation: '邀请已注册用户时自动加入'
        },
        ssrf: {
          whitelist: 'SSRF 防护白名单'
        },
        sandbox: {
          docker_enabled: '启用 Docker 沙箱'
        },
        auth: {
          registration_mode: '注册模式',
          default_tenant_mode: '注册默认空间策略',
          complex_password_enabled: '启用复杂密码'
        }
      },
      runtime: {
        title: '任务队列运行时',
        description: '后台任务队列的实时负载，以及各独立 worker 池的每实例并发配置。支持查看任务明细和安全管理，每 5 秒自动刷新。',
        refresh: '刷新',
        autoRefresh: '自动刷新（每 5 秒）',
        loading: '加载中...',
        retry: '重试',
        unavailableTitle: '任务队列不可用',
        unavailable: '当前部署未启用 Redis / asynq 队列（Lite 模式），无队列可展示。',
        empty: '暂无队列数据',
        detailsTitle: '队列明细',
        detailsDescription: '各处理通道的实时负载与等待情况。“最终失败”表示任务超过重试上限，已停止自动执行。',
        poolsTitle: 'Worker 池',
        poolsDescription: '各阶段拥有保底容量，核心解析与内容富化还可借用共享弹性池。',
        perInstance: '卡片主值为集群运行中/容量',
        poolConfigured: '单实例配置 {value}',
        poolInstances: '{value} 个实例',
        poolUtilization: '利用率 {value}%',
        queueCount: '{value} 个队列',
        weightShort: '权重 {value}',
        footnote: '卡片主值为集群运行中/实时容量；单实例配置修改后需重启。共享弹性池只消费核心解析和内容富化队列。',
        updatedAt: '更新于 {value}',
        models: {
          title: '模型并发占用',
          description: '观察后台任务实际进入模型服务时的并发占用；上方是任务调度，这里是模型服务限流，两者处于不同处理阶段。',
          scope: '占用为集群全局 · 等待为当前实例',
          disabled: '模型后台并发治理未启用。可在全局设置中配置模型默认并发上限。',
          empty: '暂无模型调用数据；模型首次执行后台任务后会出现在这里。',
          backgroundOnly: '仅统计后台任务，不包含交互式对话',
          status: {
            queued: '限流中',
            full: '已满载'
          },
          columns: {
            model: '模型 ID',
            active: '调用中',
            waiting: '限流等待',
            usage: '并发用量'
          }
        },
        tasks: {
          title: '任务明细 · {queue}',
          description: '查看各状态任务及其安全管理动作。定时、重试任务按最近执行时间优先，其余状态按时间倒序。',
          listTitle: '{state}任务',
          openAria: '查看{queue}中 {count} 个{state}任务',
          unavailable: '当前部署不支持查看任务明细',
          empty: '这个队列当前没有{state}任务',
          loadError: '获取任务明细失败',
          loadMore: '加载更多',
          loadedSummary: '已加载 {count} 条，继续下滑或点击加载',
          loadedAll: '已全部加载，共 {count} 条',
          loadingMore: '正在加载更多…',
          attempts: '执行 {current}/{max}',
          unknownTarget: '未识别到关联对象',
          knowledgeBaseLabel: '知识库 ID',
          knowledgeLabel: '文档 ID',
          taskIDLabel: '业务任务 ID',
          tenantLabel: '空间 ID',
          sourceLabel: '来源 ID',
          targetLabel: '目标 ID',
          sourceKBLabel: '来源知识库',
          targetKBLabel: '目标知识库',
          dataSourceLabel: '数据源 ID',
          syncLogLabel: '同步记录 ID',
          knowledgeCountLabel: '文档数量',
          enqueuedAt: '入队时间',
          startedAt: '开始时间',
          nextProcessAt: '下次执行',
          lastFailedAt: '最后失败',
          completedAt: '完成时间',
          deadline: '执行截止',
          worker: '执行实例',
          health: '运行健康',
          orphaned: '执行实例已失联，等待恢复',
          cancel: '终止任务',
          cancelConfirm: '将通过业务取消流程终止该任务及同一文档的相关队列任务。确认终止？',
          runNow: '立即执行',
          runNowConfirm: '该任务将立即进入待执行队列，重试次数不会重置。确认继续？',
          deleteRecord: '清除记录',
          deleteConfirm: '仅从当前队列移除这条失败记录，不会执行或完成原任务，历史日志仍会保留。确认清除？',
          purgeArchived: '清除全部失败任务',
          purgeArchivedConfirm: '将从当前队列一次性移除全部 {count} 条失败记录，仅清理队列里的失败任务，不影响运行中或排队中的任务，也不会回滚已失败文档的业务状态。确认清除？',
          purgeArchivedSuccess: '已清除 {count} 条失败任务',
          purgeArchivedError: '清除失败任务失败',
          stateFilter: '按任务状态筛选',
          actionError: {
            cancel: '终止任务失败',
            run_now: '立即执行任务失败',
            delete: '清除失败记录失败'
          },
          actionSuccess: {
            cancel: '任务已终止',
            run_now: '任务已进入待执行队列',
            delete: '失败记录已清除'
          },
          guides: {
            active: '运行中任务可查看执行实例、开始时间和截止时间。只有具备完整业务取消语义的任务才允许终止。',
            pending: '排队任务尚未被 worker 领取。终止操作会同步更新业务状态，而不是只删除 Redis 记录。',
            scheduled: '定时任务可提前立即执行；支持业务取消的文档任务也可以安全终止。',
            retry: '请结合最后错误、重试次数和下次执行时间判断是否立即执行或终止。',
            archived: '请先修复失败原因再立即执行。清除记录不会完成原业务任务。',
            completed: '这里只展示设置了结果保留时间的近期完成任务，不提供管理动作。'
          },
          states: {
            active: '运行中',
            pending: '排队中',
            scheduled: '定时执行',
            retry: '重试中',
            archived: '最终失败',
            completed: '已完成'
          },
          taskTypes: {
            documentProcess: '文档解析',
            manualProcess: '手工重新处理',
            temporaryDocumentProcess: '聊天附件解析',
            postProcess: '文档后处理',
            summary: '摘要生成',
            tableSummary: '表格摘要生成',
            question: '问题生成',
            multimodal: '图片多模态处理',
            graph: '知识图谱抽取',
            sync: '数据源同步',
            faqImport: 'FAQ 导入',
            batchReparse: '批量重新解析',
            batchDelete: '批量删除',
            move: '文档移动',
            indexDelete: '索引删除',
            kbClone: '知识库复制',
            kbDelete: '知识库删除',
            wikiIngest: 'Wiki 内容生成',
            wikiFinalize: 'Wiki 收尾处理'
          }
        },
        failedNotice: {
          title: '{count} 个任务待处理',
          description: '点击下方表中红色「最终失败」数字查看原因，修复后可手动重试。'
        },
        status: {
          working: '处理中',
          waiting: '等待中',
          idle: '空闲',
          actionRequired: '需处理',
          retrying: '重试中',
          paused: '已暂停'
        },
        columns: {
          queue: '队列',
          active: '运行中',
          pending: '排队',
          scheduled: '定时',
          retry: '重试',
          archived: '最终失败',
          completed: '已完成',
          latency: '最早等待',
          status: '状态'
        },
        summary: {
          title: '运行概览',
          active: '运行中',
          pending: '排队中',
          retry: '重试中',
          archived: '最终失败'
        },
        pools: {
          core: '核心解析',
          postprocess: '后处理编排',
          enrichment: '内容富化',
          maintenance: '维护与同步',
          shared: '共享弹性',
          wiki: 'Wiki 池'
        },
        poolDescriptions: {
          core: '文档解析与手工重解析的保底容量',
          postprocess: '解析完成后的收尾与富化扇出',
          enrichment: '摘要、图片、图谱与问题生成',
          maintenance: '数据源同步、批处理与删除清理',
          shared: '由核心解析与内容富化按积压借用',
          wiki: 'Wiki 内容生成与全局收尾'
        },
        queueNames: {
          default: '文档解析',
          chat_attachment: '对话附件解析',
          postprocess: '后处理编排',
          summary: '摘要生成',
          sync: '数据源同步',
          low: '维护与批处理',
          multimodal: '多模态处理',
          graph: '图谱抽取',
          question: '问题生成',
          wiki: 'Wiki 处理'
        },
        queueDescriptions: {
          default: '文档解析、手工重解析',
          chat_attachment: '会话内上传附件解析',
          postprocess: '解析收尾、富化扇出',
          summary: '文档摘要、表格摘要',
          sync: '手动与定时同步',
          low: 'FAQ 导入、批量重解析、删除清理',
          multimodal: '图片 OCR、视觉描述',
          graph: '分块图谱抽取',
          question: '分块问题生成',
          wiki: '内容生成、索引收尾'
        },
        errors: {
          generic: '获取队列状态失败'
        }
      },
      sections: {
        other: {
          title: '其他配置',
          description: '当前部署中未归入标准分组的配置项。'
        },
        security: {
          title: '网络安全',
          description: '管理 SSRF 白名单，以及是否允许 Docker 沙箱（本机 docker.sock 等同宿主机 root）。'
        },
        runtime: {
          title: '运行与并发',
          description: '配置后台任务池与模型服务的并发容量。',
          restartHint: 'Worker 配置需重启生效'
        },
        tenant: {
          title: '空间默认值',
          description: '设置新空间的初始配额与兼容性行为，不会自动改写已有空间。'
        },
        access: {
          title: '注册与访问',
          description: '管理注册方式、密码要求和用户创建空间的规则。'
        }
      },
      priorityHint: {
        disclosure: '配置来源与优先级',
        tier1: '在此页面保存过的项（带「已覆盖」徽章）— 始终以这里的值为准，环境变量会被忽略。',
        tier2: '未在此处保存过的项 — 如果环境变量里有就用环境变量，否则用程序内置默认值。',
        tier3: '若想让某项重新由环境变量控制，点击该行的「重置」按钮即可清除当前 UI 设置。'
      }
    },
    messages: {
      fetchFailed: '获取系统信息失败',
      networkError: '网络错误，请稍后重试'
    }
  }
}
