export default {
  documentProgress: {
    queueView: 'Queue tasks',
    title: 'Document and Wiki progress',
    description: 'Inspect current ingestion and Wiki work separately. Failure history remains available after recovery.',
    refresh: 'Refresh',
    search: 'Search document or knowledge base',
    searchButton: 'Search',
    filter: 'Progress filter',
    count: 'Number of documents (1–100)',
    batchStart: 'Start Wiki batch',
    confirmBatch: 'Schedule up to {count} eligible waiting documents, oldest first, within the current search?',
    confirmOne: 'Schedule Wiki work for “{title}” using its existing progress?',
    attempt: 'Attempt {value}',
    pendingOps: '{count} pending operations · retry count {retries}',
    historyCount: '{count} records',
    details: 'Details',
    startNow: 'Start Wiki now',
    currentStatus: 'Current status',
    history: 'Failure history',
    historyDescription: 'These are original failure records, including failures followed by later recovery. They do not mean the document is still failing.',
    noHistory: 'No archived failures for this document.',
    failureAttempts: '{count} failed attempts',
    historyLimit: 'Showing the latest 50 records; original history is retained.',
    unfinished: 'Unfinished',
    all: 'All documents',
    document: 'Document',
    ingestion: 'Ingestion',
    wiki: 'Wiki',
    actions: 'Actions',
    loadError: 'Unable to load document progress. Refresh to try again.',
    started: '{count} documents scheduled; {queued} already queued. Existing progress is retained.',
    startError: '{count} documents accepted before the queue handoff stopped. Refresh before scheduling the remaining documents.',
    states: {
      ready: 'Ingested',
      pending: 'Pending',
      processing: 'Processing',
      finalizing: 'Finalizing',
      completed: 'Completed',
      failed: 'Failed',
      skipped: 'Skipped',
      cancelled: 'Cancelled',
      queued: 'Queued',
      retrying: 'Retrying',
      disabled: 'Disabled',
      notStarted: 'Not started'
    },
    stages: {
      docreader: 'Document parsing',
      chunking: 'Chunking',
      embedding: 'Indexing',
      multimodal: 'Image processing',
      postprocess: 'Post-processing'
    }
  },
  system: {
    title: 'System Information',
    sectionDescription: 'View system version information and user account configuration',
    loadingInfo: 'Loading information...',
    retry: 'Retry',
    versionLabel: 'App Version',
    versionDescription: 'Version of the application service (weknora-app)',
    frontendVersionLabel: 'UI Version',
    frontendVersionDescription: 'Build version of the UI (weknora-ui)',
    versionMismatch: 'Mismatch with app version',
    buildTimeLabel: 'Build Time',
    buildTimeDescription: 'Time when the system was built',
    goVersionLabel: 'Go Version',
    goVersionDescription: 'Go language version used by the backend',
    docreaderVersionLabel: 'Docreader Version',
    docreaderVersionDescription: 'Build version of the current document parsing service (weknora-docreader)',
    startedAtLabel: 'Service started at',
    startedAtDescription: 'When the current backend process last started',
    uptimeLabel: 'Uptime',
    uptimeDescription: 'How long the service has been running since this start',
    uptimeDays: '{n}d',
    uptimeHours: '{n}h',
    uptimeMinutes: '{n}m',
    uptimeSeconds: '{n}s',
    dbVersionLabel: 'Database Version',
    dbVersionDescription: 'Current database migration version',
    dbMigrationFailedTag: 'Migration failed',
    dbMigrationFailedTitle: 'Database migration failed',
    dbMigrationFailedDesc: 'The startup database migration did not complete successfully. Some tables or indexes may be missing, which can break Wiki ingest, the knowledge graph, and other features. Check the troubleshooting guide below first; if the issue persists, report it via the link.',
    dbMigrationViewDocs: 'View troubleshooting guide',
    dbMigrationReportIssue: 'Can\'t fix it? Report an issue',
    keywordIndexEngineLabel: 'Keyword Index Engine',
    keywordIndexEngineDescription: 'Currently used keyword index engine',
    vectorStoreEngineLabel: 'Vector Store Engine',
    vectorStoreEngineDescription: 'Currently used vector store engine',
    graphDatabaseEngineLabel: 'Graph Database Engine',
    graphDatabaseEngineDescription: 'Currently used graph database engine',
    unknown: 'Unknown',
    messages: {
      fetchFailed: 'Failed to fetch system information',
      networkError: 'Network error, please try again later'
    },
    globalSettings: {
      title: 'System Settings',
      description: 'Platform-wide runtime configuration. Saves take effect immediately for every workspace. Visible and editable by system administrators only.',
      loading: 'Loading...',
      empty: 'No configurable system settings available',
      saving: 'Saving',
      saved: 'Saved',
      saveAnnouncement: '{label} saved',
      badgeRequiresRestart: 'Restart required',
      badgeSecret: 'Secret',
      badgeHighRisk: 'High risk',
      badgeOverride: 'Overridden',
      badgeOverrideTooltip: 'This value has been saved to the database by an administrator, overriding the environment variable and built-in default.',
      modifiedAt: 'Last modified: {value}',
      tagInputPlaceholder: 'Press Enter to add an entry, e.g. example.com / *.foo.com / 10.0.0.0/8',
      priorityHint: {
        disclosure: 'Configuration source and priority',
        tier1: 'Items saved on this page (marked "Overridden") always win — the environment variable is ignored for them.',
        tier2: 'Items not saved here fall back to the environment variable, or to the built-in default if no env var is set.',
        tier3: 'To put an item back under environment-variable control, click the "Reset" button on its row.'
      },
      sections: {
        access: {
          title: 'Registration & access',
          description: 'Manage registration, password requirements, and workspace-creation rules.'
        },
        tenant: {
          title: 'Workspace defaults',
          description: 'Set initial quotas and compatibility behavior for new workspaces without rewriting existing workspaces.'
        },
        runtime: {
          title: 'Runtime & concurrency',
          description: 'Configure concurrency capacity for background worker pools and model services.',
          restartHint: 'Worker settings require restart'
        },
        security: {
          title: 'Network security',
          description: 'Manage the SSRF allowlist and whether the Docker sandbox is allowed (a local docker.sock is equivalent to root on the host).'
        },
        other: {
          title: 'Other settings',
          description: 'Settings in this deployment that are not part of a standard product group.'
        }
      },
      runtime: {
        title: 'Task Queue Runtime',
        description: 'Live background-queue load and per-process capacity for each isolated worker pool. Includes task details and safe controls; auto-refreshes every 5s.',
        refresh: 'Refresh',
        autoRefresh: 'Auto-refresh (every 5s)',
        loading: 'Loading...',
        retry: 'Retry',
        unavailableTitle: 'Task queues unavailable',
        unavailable: 'This deployment has no Redis / asynq queue (Lite mode) — nothing to display.',
        empty: 'No queue data available',
        detailsTitle: 'Queue details',
        detailsDescription: 'Live load and wait time for every processing lane. “Failed (stopped)” means the task exceeded its retry limit and will not run automatically again.',
        poolsTitle: 'Worker pools',
        poolsDescription: 'Each stage has guaranteed capacity; core and enrichment may also borrow the shared elastic pool.',
        perInstance: 'Card value is cluster active/capacity',
        poolConfigured: 'Per-process {value}',
        poolInstances: '{value} instances',
        poolUtilization: '{value}% utilized',
        queueCount: '{value} queues',
        weightShort: 'Weight {value}',
        footnote: 'Card values show cluster active/live capacity; per-process configuration requires restart. The shared elastic pool only consumes core and enrichment queues.',
        updatedAt: 'Updated at {value}',
        errors: {
          generic: 'Failed to load queue stats'
        },
        summary: {
          title: 'Runtime overview',
          active: 'Active',
          pending: 'Pending',
          retry: 'Retrying',
          archived: 'Failed (stopped)'
        },
        columns: {
          queue: 'Queue',
          active: 'Active',
          pending: 'Pending',
          scheduled: 'Scheduled',
          retry: 'Retry',
          archived: 'Failed',
          completed: 'Completed',
          latency: 'Oldest wait',
          status: 'Status'
        },
        status: {
          working: 'Working',
          waiting: 'Waiting',
          idle: 'Idle',
          actionRequired: 'Action needed',
          retrying: 'Retrying',
          paused: 'Paused'
        },
        failedNotice: {
          title: '{count} failed tasks need attention',
          description: 'Open a red Failed count in the table below to inspect causes, then retry after fixing the root issue.'
        },
        tasks: {
          title: 'Task details · {queue}',
          description: 'Inspect every task state. Scheduled and retry tasks show the next run first; other states show the newest tasks first.',
          listTitle: '{state} tasks',
          openAria: 'View {count} {state} tasks in {queue}',
          unavailable: 'Task details are unavailable in this deployment',
          empty: 'This queue has no {state} tasks',
          loadError: 'Failed to load task details',
          loadMore: 'Load more',
          loadedSummary: '{count} loaded — scroll or tap to load more',
          loadedAll: 'All {count} loaded',
          loadingMore: 'Loading more…',
          attempts: 'Attempt {current}/{max}',
          unknownTarget: 'No related object identified',
          knowledgeBaseLabel: 'Knowledge base ID',
          knowledgeLabel: 'Document ID',
          taskIDLabel: 'Business task ID',
          tenantLabel: 'Workspace ID',
          sourceLabel: 'Source ID',
          targetLabel: 'Target ID',
          sourceKBLabel: 'Source knowledge base',
          targetKBLabel: 'Target knowledge base',
          dataSourceLabel: 'Data source ID',
          syncLogLabel: 'Sync log ID',
          knowledgeCountLabel: 'Documents',
          enqueuedAt: 'Enqueued',
          startedAt: 'Started',
          nextProcessAt: 'Next run',
          lastFailedAt: 'Last failed',
          completedAt: 'Completed',
          deadline: 'Deadline',
          worker: 'Worker',
          health: 'Runtime health',
          orphaned: 'Worker heartbeat lost; waiting for recovery',
          cancel: 'Cancel task',
          cancelConfirm: 'This uses the business cancellation flow and also stops related tasks for the same document. Cancel it?',
          runNow: 'Run now',
          runNowConfirm: 'This task will move to pending immediately without resetting its retry count. Continue?',
          deleteRecord: 'Clear record',
          deleteConfirm: 'This only removes the record from this queue. It will not run or complete the original task, and historical logs remain. Clear it?',
          purgeArchived: 'Clear all failed tasks',
          purgeArchivedConfirm: 'Remove all {count} failed records from this queue at once. This only clears failed tasks in the queue — it does not affect running or pending tasks, nor does it roll back the business status of documents that already failed. Clear them?',
          purgeArchivedSuccess: 'Cleared {count} failed tasks',
          purgeArchivedError: 'Failed to clear failed tasks',
          stateFilter: 'Filter by task state',
          states: {
            active: 'Active',
            pending: 'Pending',
            scheduled: 'Scheduled',
            retry: 'Retrying',
            archived: 'Failed',
            completed: 'Completed'
          },
          guides: {
            active: 'Inspect the worker, start time, deadline, and orphan status. Cancel is offered only when the business state can be updated safely.',
            pending: 'Pending tasks have not been claimed. Cancellation updates business state instead of only removing Redis data.',
            scheduled: 'Scheduled tasks can run early; document pipeline tasks can also use safe business cancellation.',
            retry: 'Use the last error, attempt count, and next run time to decide whether to run now or cancel.',
            archived: 'Fix the root cause before running again. Clearing a record does not complete the original business task.',
            completed: 'Only recently completed tasks with result retention are shown. No management actions are available.'
          },
          actionSuccess: {
            cancel: 'Task cancelled',
            run_now: 'Task moved to pending',
            delete: 'Failure record cleared'
          },
          actionError: {
            cancel: 'Failed to cancel task',
            run_now: 'Failed to run task',
            delete: 'Failed to clear record'
          },
          taskTypes: {
            documentProcess: 'Document parsing',
            manualProcess: 'Manual reprocessing',
            temporaryDocumentProcess: 'Chat attachment parsing',
            postProcess: 'Document post-processing',
            summary: 'Summary generation',
            tableSummary: 'Table summary generation',
            question: 'Question generation',
            multimodal: 'Image multimodal processing',
            graph: 'Knowledge graph extraction',
            sync: 'Data-source sync',
            faqImport: 'FAQ import',
            batchReparse: 'Batch reparse',
            batchDelete: 'Batch delete',
            move: 'Document move',
            indexDelete: 'Index deletion',
            kbClone: 'Knowledge-base clone',
            kbDelete: 'Knowledge-base deletion',
            wikiIngest: 'Wiki content generation',
            wikiFinalize: 'Wiki finalization'
          }
        },
        models: {
          title: 'Model concurrency',
          description: 'Actual model-service concurrency from background tasks. The section above schedules tasks; this section shows the separate model-service throttle stage.',
          scope: 'Active is cluster-wide · waiting is local',
          disabled: 'Background model concurrency governance is disabled. Configure the default model concurrency limit in Global settings.',
          empty: 'No model activity yet. A model appears after its first background call.',
          backgroundOnly: 'Background tasks only; interactive chat is excluded',
          columns: {
            model: 'Model ID',
            active: 'In flight',
            waiting: 'Throttled',
            usage: 'Concurrency'
          },
          status: {
            queued: 'Throttling',
            full: 'At limit'
          }
        },
        pools: {
          core: 'Core parsing',
          postprocess: 'Post-process orchestration',
          enrichment: 'Enrichment',
          maintenance: 'Maintenance & sync',
          shared: 'Shared elastic',
          wiki: 'Wiki pool'
        },
        poolDescriptions: {
          core: 'Guaranteed document and manual parsing capacity',
          postprocess: 'Parse finalization and enrichment fan-out',
          enrichment: 'Summaries, images, graph, and question generation',
          maintenance: 'Source sync, batch work, and deletion cleanup',
          shared: 'Borrowed by core or enrichment according to backlog',
          wiki: 'Wiki content generation and global finalization'
        },
        queueNames: {
          default: 'Document parsing',
          chat_attachment: 'Chat attachments',
          postprocess: 'Post-process',
          summary: 'Summaries',
          sync: 'Source sync',
          low: 'Maintenance & batch',
          multimodal: 'Multimodal',
          graph: 'Graph extraction',
          question: 'Questions',
          wiki: 'Wiki pipeline'
        },
        queueDescriptions: {
          default: 'Document parse, manual reparse',
          chat_attachment: 'Session-scoped chat upload parsing',
          postprocess: 'Parse finalization, enrichment fan-out',
          summary: 'Document & table summaries',
          sync: 'Manual & scheduled sync',
          low: 'FAQ import, batch reparse, cleanup',
          multimodal: 'Image OCR, vision captions',
          graph: 'Chunk-level graph extraction',
          question: 'Chunk-level question generation',
          wiki: 'Content generation, index finalize'
        }
      },
      keyLabels: {
        auth: {
          registration_mode: 'Registration mode',
          default_tenant_mode: 'Default workspace provisioning',
          complex_password_enabled: 'Require complex password'
        },
        ssrf: {
          whitelist: 'SSRF protection allowlist'
        },
        sandbox: {
          docker_enabled: 'Enable Docker sandbox'
        },
        tenant: {
          max_owned_per_user: 'Max workspaces owned per user',
          self_service_creation_enabled: 'Allow self-service workspace creation',
          default_storage_quota_gb: 'Default storage quota for new workspaces (GB)',
          default_storage_quota_mb: 'Default storage quota for new workspaces (MB)',
          auto_create_api_key: 'Automatically create an API key for new workspaces',
          auto_accept_invitation: 'Auto-join invited registered users'
        },
        asynq: {
          core_concurrency: 'Guaranteed core parse concurrency',
          postprocess_concurrency: 'Post-process concurrency',
          enrichment_concurrency: 'Guaranteed enrichment concurrency',
          maintenance_concurrency: 'Maintenance concurrency',
          shared_concurrency: 'Shared elastic concurrency',
          wiki_concurrency: 'Wiki worker concurrency'
        },
        model: {
          max_concurrency: 'Default per-model concurrency limit'
        }
      },
      keyDescriptions: {
        auth: {
          registration_mode: 'Registration mode. Open registration allows anyone to create an account; invitation registration requires a valid invitation link; disabled registration prevents account creation while existing accounts can still accept invitations. Changes take effect immediately.',
          default_tenant_mode: 'Workspace provisioning after public registration. create_personal creates an Owner workspace; tenantless creates only the account until the user accepts an invitation or creates a workspace. Applies to new users only.',
          complex_password_enabled: 'Whether to require complex passwords. When enabled, passwords must contain uppercase and lowercase letters, numbers, and special characters. Changes take effect immediately and only apply to newly registered users or new password changes/resets. Special characters include {specialChars}'
        },
        ssrf: {
          whitelist: 'SSRF protection allowlist. Accepts entries such as example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1. Takes effect immediately after saving. The SSRF_WHITELIST_EXTRA environment variable is still maintained by the deployer and is not overridden here.'
        },
        sandbox: {
          docker_enabled: 'Allow the Docker sandbox backend. A local docker.sock is equivalent to root on the host, so this stays off by default. Only a system admin can turn it on; the change takes effect immediately. Enable it only on a private single-node install that mounts the daemon socket or uses a TLS-protected remote tcp:// endpoint.'
        },
        tenant: {
          max_owned_per_user: 'Maximum number of workspaces a non-superuser may own via self-service creation. Read on every workspace creation and takes effect immediately after saving. 0 uses the built-in default of 10; a negative value disables the cap entirely (not recommended on public deployments).',
          self_service_creation_enabled: 'Whether non-superusers may create workspaces themselves. When disabled, regular users can only join existing workspaces by invitation; cross-workspace superusers remain exempt. Takes effect immediately.',
          default_storage_quota_gb: 'Default storage quota (GB) assigned when a new workspace is created, covering vectors, originals, text, indexes, and related data. Read only at creation time — changes apply to newly created workspaces only and do not retroactively update existing workspaces. 0 or a negative value uses the built-in default of 10 GB.',
          default_storage_quota_mb: 'Includes personal workspaces created at registration. Positive values override the GB setting; zero or negative values use GB. Each setting resolves from database, environment, then built-in default. Changes affect newly created workspaces only.',
          auto_create_api_key: 'Automatically creates a full_access API key for a new workspace and returns its plaintext token in the create response. Use only for integrations that depend on the legacy behavior; it is disabled by default and explicit API-key creation is recommended.',
          auto_accept_invitation: 'When enabled, inviting a registered user by email adds them as a member immediately instead of waiting for inbox confirmation. When off, the invitee must accept from their inbox. Takes effect immediately.'
        },
        asynq: {
          core_concurrency: 'Guaranteed per-process concurrency for document and manual parsing. Core may also borrow the shared elastic pool. Minimum 1; requires a service restart.',
          postprocess_concurrency: 'Dedicated per-process concurrency for lightweight parse finalization and enrichment fan-out. Minimum 1; requires a service restart.',
          enrichment_concurrency: 'Guaranteed per-process concurrency for summaries, images, graph, and question generation. Enrichment may also borrow the shared elastic pool. Minimum 1; requires a service restart.',
          maintenance_concurrency: 'Per-process concurrency for source sync, batch work, and cleanup, hard-isolated from the user-facing pipeline. Minimum 1; requires a service restart.',
          shared_concurrency: 'Per-process elastic concurrency shared by core parsing and enrichment; whichever side has backlog borrows it automatically. Minimum 1; requires a service restart.',
          wiki_concurrency: 'Per-process concurrency for the dedicated Wiki worker pool, isolated from upstream tasks. Minimum 1; requires a service restart.'
        },
        model: {
          max_concurrency: 'Default cap on concurrent background (ingestion/enrichment) calls to a single model, keyed by model ID and shared across replicas. Read on every call and applied immediately with no restart. 0 or a negative value disables the default cap (each model still honours its own limit configured in model management). Affects background tasks only, not interactive chat.'
        }
      },
      enumLabels: {
        auth: {
          registration_mode: {
            self_serve: 'Self-service (anyone can register)',
            invite_register: 'Invitation registration (valid link required)',
            invite_only: 'Registration disabled (existing accounts can accept invitations)'
          },
          default_tenant_mode: {
            create_personal: 'Create personal workspace',
            tenantless: 'Do not create a workspace'
          }
        }
      },
      confirm: {
        confirmBtn: 'Confirm save',
        cancelBtn: 'Cancel',
        emptyValue: '(empty)',
        bodyAuthRegistrationMode: 'About to change "{label}" to: {value}\n\nIf switched to self_serve, anyone on the public internet can register an account — please confirm this is intended.',
        bodySandboxDockerEnabled: 'Once on, workspace admins can point a sandbox at the local Docker daemon. A local docker.sock is equivalent to root on the host. Use this only on a private single-node install that mounts the daemon or uses a TLS-protected remote tcp:// endpoint.'
      },
      listConfirm: {
        ssrf: {
          whitelist: {
            add: {
              header: 'Add SSRF allowlist entry',
              body: 'Add {entry} to the SSRF allowlist? Hosts / IPs / CIDRs that match this entry will bypass SSRF protection and may let agents reach internal services. Only add entries you fully trust.',
              confirmBtn: 'Add'
            },
            remove: {
              header: 'Remove SSRF allowlist entry',
              body: 'Remove {entry} from the SSRF allowlist? After removal this entry will be blocked by SSRF protection again.',
              confirmBtn: 'Remove'
            }
          }
        }
      },
      messages: {
        loadFailed: 'Failed to load system settings',
        saveSuccess: 'Saved',
        saveFailed: 'Save failed'
      },
      reset: {
        label: 'Reset',
        tooltip: 'Clear the UI override and fall back to the environment variable or the built-in default',
        confirmBtn: 'Confirm reset',
        confirmBody: 'Reset "{label}"? This deletes the database override and falls back to the environment variable or the built-in default.',
        success: 'Reset to default',
        failed: 'Reset failed'
      },
      userList: {
        title: 'Platform users',
        rowLabel: 'User management',
        rowDesc: 'View every registered user across workspaces, with pagination and username/email filtering.',
        searchPlaceholder: 'Filter by username or email (current page)',
        refresh: 'Refresh',
        active: 'Active',
        inactive: 'Disabled',
        colUsername: 'Username',
        colStatus: 'Status',
        colRole: 'Role',
        colCreated: 'Created',
        colAction: 'Actions',
        prev: 'Previous',
        next: 'Next',
        pageCount: '{shown} shown / {total} on page',
        enable: 'Enable',
        disable: 'Disable',
        resetPassword: 'Reset password',
        confirmDisable: 'Disable account {name}? Their active sessions will be revoked immediately.',
        confirmEnable: 'Enable account {name}?',
        enabledMsg: 'Enabled {name}',
        disabledMsg: 'Disabled {name}',
        actionFailed: 'Action failed',
        loadFailed: 'Failed to load users',
        roleAdmin: 'System admin',
        roleUser: 'Regular user',
        confirmRevoke: 'Revoke system-admin privileges from {name}?',
        confirmPromote: 'Promote {name} to system administrator?',
        revokeBtn: 'Revoke',
        promoteBtn: 'Promote',
        revokedMsg: 'Revoked admin privileges from {name}',
        promotedMsg: 'Promoted {name} to system administrator',
        roleFailed: 'Failed to change role'
      },
      passwordReset: {
        dialogTitle: 'Reset another user\'s password',
        warning: 'This is a high-risk action. Verify the user email carefully. You cannot reset your own password here.',
        emailLabel: 'User email',
        newPasswordLabel: 'New password',
        newPasswordPlaceholder: 'Enter new password',
        confirmPasswordLabel: 'Confirm new password',
        confirmPasswordPlaceholder: 'Enter the new password again',
        confirmBtn: 'Confirm reset',
        success: 'Password reset; the user\'s existing sessions were revoked',
        failed: 'Failed to reset password'
      },
      createUser: {
        action: 'Create user',
        dialogTitle: 'Create a new user',
        warning: 'This is a high-risk action. Verify the username and email carefully — they are how the new user will sign in.',
        usernameLabel: 'Username',
        usernamePlaceholder: '2-50 characters',
        emailLabel: 'Email',
        emailPlaceholder: 'Enter a valid email address',
        autoGenerateLabel: 'Auto-generate a random password',
        newPasswordLabel: 'New password',
        newPasswordPlaceholder: '8-32 characters, including letters and numbers',
        confirmPasswordLabel: 'Confirm new password',
        confirmPasswordPlaceholder: 'Enter the new password again',
        confirmBtn: 'Create user',
        success: 'User created',
        successIdempotent: 'User already existed; no changes made',
        missingPassword: 'User was created but the generated password was not returned. Reset the password to recover access.',
        failed: 'Failed to create user',
        validation: {
          usernameRequired: 'Enter a username',
          usernameLength: 'Username must be 2-50 characters',
          emailRequired: 'Enter an email address',
          emailInvalid: 'Enter a valid email address',
          confirmRequired: 'Enter the new password again',
          passwordMismatch: 'The passwords do not match'
        },
        generated: {
          successTitle: 'User created',
          successBody: 'A random password was generated for this user. It will only be shown once.',
          usernameLabel: 'Username',
          emailLabel: 'Email',
          passwordLabel: 'Generated password',
          copyBtn: 'Copy account details',
          copySuccess: 'Account details copied to clipboard',
          acknowledgeBtn: 'I have saved the password'
        }
      },
      bulkApply: {
        label: 'Apply to all existing workspaces',
        tooltip: 'Saving the value only affects new workspaces by default; click here to also overwrite every existing workspace.',
        confirmBtn: 'Confirm apply',
        confirmBody: 'Overwrite the storage quota of every existing workspace to {value} {unit}. Workspaces whose quota was tuned manually by operations will also be overwritten. Continue?',
        success: 'Updated storage quota for {count} workspaces to {value} {unit}',
        failed: 'Failed to apply to all workspaces'
      },
      audit: {
        tabLabel: 'Audit log',
        description: 'Platform-level events: system setting changes, system admin grants/revocations, bulk quota syncs. Newest first.',
        refresh: 'Refresh',
        retry: 'Retry',
        loading: 'Loading…',
        end: 'End of feed.',
        empty: 'No platform-level audit events yet.',
        forbidden: 'Not allowed to view the platform audit log.',
        systemActor: 'System',
        errors: {
          generic: 'Failed to load audit log'
        },
        actorRole: {
          system_admin: 'System Admin'
        },
        columns: {
          time: 'Time',
          actor: 'Actor',
          action: 'Event',
          target: 'Target',
          path: 'Request',
          outcome: 'Result'
        },
        action: {
          'system.setting_changed': 'System setting changed',
          'system.admin_promoted': 'System admin granted',
          'system.api_key_created': 'Platform API key created',
          'system.api_key_revoked': 'Platform API key revoked',
          'system.admin_revoked': 'System admin revoked',
          'system.user_password_reset': 'User password reset',
          'system.user_created': 'User created',
          'system.queue_task_retried': 'Failed task run again',
          'system.queue_task_deleted': 'Failed task record cleared',
          'system.queue_task_run_now': 'Queue task run now',
          'system.queue_task_cancelled': 'Queue task cancelled',
          'system.queue_archived_purged': 'All failed tasks cleared'
        },
        outcome: {
          success: 'Success',
          denied: 'Denied'
        },
        target: {
          bulkQuota: 'Bulk sync: default storage quota',
          bulkQuotaDiff: 'Applied to {count} workspaces ({gb} GB)',
          promoteIdempotent: 'Target was already a system admin (idempotent)',
          revokeNoop: 'Target was not a system admin (idempotent)',
          requiredRole: 'Required role: {role}',
          valueNull: '(unset)'
        },
        expanded: {
          actorId: 'Actor ID',
          targetUserId: 'Target user ID',
          targetType: 'Target type',
          targetId: 'Target ID',
          details: 'Raw details'
        },
        drawer: {
          sectionSummary: 'Event summary',
          sectionIdentifiers: 'Related IDs',
          sectionRequest: 'Request',
          targetChange: 'Change',
          requestMethod: 'Method'
        }
      }
    }
  }
}
