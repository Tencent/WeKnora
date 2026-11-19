export default {
  documentProgress: {
    queueView: '큐 작업',
    title: '자료 및 Wiki 진행 상황',
    description: '수집과 Wiki의 현재 처리 상태를 각각 확인합니다. 복구 후에도 원래 실패 기록은 유지됩니다.',
    refresh: '새로고침',
    search: '자료 또는 지식 베이스 검색',
    searchButton: '검색',
    filter: '진행 상태 필터',
    count: '자료 수 (1–100)',
    batchStart: 'Wiki 일괄 시작',
    confirmBatch: '현재 검색 범위에서 가장 오래 기다린 자료부터 최대 {count}개의 Wiki 처리를 예약할까요?',
    confirmOne: '“{title}”의 기존 진행 상황을 유지하면서 Wiki 처리를 예약할까요?',
    attempt: '처리 시도 {value}',
    pendingOps: '대기 작업 {count}개 · 재시도 횟수 {retries}',
    historyCount: '기록 {count}개',
    details: '상세 정보',
    startNow: '지금 Wiki 시작',
    currentStatus: '현재 상태',
    history: '실패 기록',
    historyDescription: '복구된 실패를 포함한 원래 기록입니다. 기록이 있다는 것이 현재도 실패 중이라는 뜻은 아닙니다.',
    noHistory: '이 자료의 보관된 실패 기록이 없습니다.',
    failureAttempts: '실패 횟수 {count}',
    historyLimit: '최근 50개를 표시합니다. 원래 기록은 유지됩니다.',
    unfinished: '미완료',
    all: '모든 자료',
    document: '자료',
    ingestion: '수집',
    wiki: 'Wiki',
    actions: '작업',
    loadError: '진행 상황을 읽을 수 없습니다. 새로고침 후 다시 시도하세요.',
    started: '{count}개를 예약했습니다. {queued}개는 이미 대기 중입니다. 기존 진행 상황은 유지됩니다.',
    startError: '큐 전달이 중단되기 전에 {count}개가 접수되었습니다. 새로고침 후 나머지를 예약하세요.',
    states: {
      ready: '수집 완료',
      pending: '대기',
      processing: '처리 중',
      finalizing: '마무리 중',
      completed: '완료',
      failed: '실패',
      skipped: '건너뜀',
      cancelled: '취소됨',
      queued: '큐 대기',
      retrying: '재시도 중',
      disabled: '비활성',
      notStarted: '시작 전'
    },
    stages: {
      docreader: '문서 분석',
      chunking: '분할',
      embedding: '색인 생성',
      multimodal: '이미지 처리',
      postprocess: '후처리'
    }
  },
  system: {
    title: '시스템 정보',
    sectionDescription: '시스템 버전 정보 및 사용자 계정 설정 보기',
    loadingInfo: '정보 로딩 중...',
    retry: '재시도',
    versionLabel: '앱 버전',
    versionDescription: '애플리케이션 서비스(weknora-app)의 버전 번호',
    frontendVersionLabel: 'UI 버전',
    frontendVersionDescription: 'UI(weknora-ui) 빌드 버전 번호',
    versionMismatch: '앱 버전과 일치하지 않음',
    buildTimeLabel: '빌드 시간',
    buildTimeDescription: '시스템이 빌드된 시간',
    goVersionLabel: 'Go 버전',
    goVersionDescription: '백엔드에서 사용하는 Go 언어 버전',
    docreaderVersionLabel: 'Docreader 버전',
    docreaderVersionDescription: '현재 문서 분석 서비스(weknora-docreader)의 빌드 버전',
    startedAtLabel: '서비스 시작 시각',
    startedAtDescription: '현재 백엔드 프로세스가 마지막으로 시작된 시각',
    uptimeLabel: '가동 시간',
    uptimeDescription: '이번 시작 이후 연속 실행된 시간',
    uptimeDays: '{n}일',
    uptimeHours: '{n}시간',
    uptimeMinutes: '{n}분',
    uptimeSeconds: '{n}초',
    dbVersionLabel: '데이터베이스 버전',
    dbVersionDescription: '현재 데이터베이스 마이그레이션 버전',
    dbMigrationFailedTag: '마이그레이션 실패',
    dbMigrationFailedTitle: '데이터베이스 마이그레이션 실패',
    dbMigrationFailedDesc: '시작 시 데이터베이스 마이그레이션이 정상적으로 완료되지 않았습니다. 일부 테이블이나 인덱스가 생성되지 않았을 수 있으며, Wiki와 지식 그래프 기능 등이 동작하지 않을 수 있습니다. 먼저 아래 문제 해결 문서를 확인하여 직접 복구해 보고, 그래도 해결되지 않으면 이슈를 등록해 주세요.',
    dbMigrationViewDocs: '문제 해결 문서 보기',
    dbMigrationReportIssue: '해결되지 않나요? 이슈 등록',
    keywordIndexEngineLabel: '키워드 인덱스 엔진',
    keywordIndexEngineDescription: '현재 사용 중인 키워드 인덱스 엔진',
    vectorStoreEngineLabel: '벡터 저장소 엔진',
    vectorStoreEngineDescription: '현재 사용 중인 벡터 저장소 엔진',
    graphDatabaseEngineLabel: '그래프 데이터베이스 엔진',
    graphDatabaseEngineDescription: '현재 사용 중인 그래프 데이터베이스 엔진',
    unknown: '알 수 없음',
    globalSettings: {
      title: '시스템 설정',
      description: '플랫폼 전역 런타임 설정입니다. 저장하면 모든 워크스페이스에 즉시 적용됩니다. 시스템 관리자만 보고 수정할 수 있습니다.',
      loading: '로딩 중...',
      empty: '설정 가능한 시스템 항목이 없습니다',
      saving: '저장 중',
      saved: '저장됨',
      saveAnnouncement: '{label} 저장됨',
      badgeRequiresRestart: '재시작 필요',
      badgeSecret: '민감',
      badgeHighRisk: '고위험',
      badgeOverride: '재정의됨',
      badgeOverrideTooltip: '이 값은 관리자가 DB에 저장하여 환경 변수 및 기본값을 덮어썼습니다.',
      modifiedAt: '마지막 수정: {value}',
      tagInputPlaceholder: '엔터로 항목 추가. 예: example.com / *.foo.com / 10.0.0.0/8',
      audit: {
        tabLabel: '감사 로그',
        description: '플랫폼 수준의 작업을 기록합니다: 시스템 설정 변경, 시스템 관리자 부여/회수, 할당량 일괄 동기화 등. 최신순으로 표시됩니다.',
        refresh: '새로고침',
        retry: '다시 시도',
        loading: '로딩 중...',
        end: '더 이상 항목이 없습니다.',
        empty: '플랫폼 감사 이벤트가 없습니다.',
        forbidden: '플랫폼 감사 로그를 볼 수 있는 권한이 없습니다.',
        systemActor: '시스템',
        drawer: {
          sectionSummary: '이벤트 요약',
          sectionIdentifiers: '관련 식별자',
          sectionRequest: '요청 정보',
          targetChange: '변경 내용',
          requestMethod: '요청 메서드'
        },
        expanded: {
          actorId: '작업자 ID',
          targetUserId: '대상 사용자 ID',
          targetType: '대상 유형',
          targetId: '대상 ID',
          details: '원본 상세'
        },
        target: {
          bulkQuota: '일괄 동기화: 기본 저장 할당량',
          bulkQuotaDiff: '{count}개 워크스페이스에 적용됨 ({gb} GB)',
          promoteIdempotent: '이미 시스템 관리자임 (멱등)',
          revokeNoop: '원래 시스템 관리자가 아니었음 (멱등)',
          requiredRole: '필요 역할: {role}',
          valueNull: '(없음)'
        },
        outcome: {
          success: '성공',
          denied: '거부'
        },
        action: {
          'system.setting_changed': '시스템 설정 변경',
          'system.admin_promoted': '시스템 관리자 부여',
          'system.api_key_created': '플랫폼 API 키 생성',
          'system.api_key_revoked': '플랫폼 API 키 폐기',
          'system.admin_revoked': '시스템 관리자 회수',
          'system.user_password_reset': '사용자 비밀번호 재설정',
          'system.user_created': '사용자 생성',
          'system.queue_task_retried': '실패 작업 다시 실행',
          'system.queue_task_deleted': '실패 작업 기록 삭제',
          'system.queue_task_run_now': '큐 작업 즉시 실행',
          'system.queue_task_cancelled': '큐 작업 취소',
          'system.queue_archived_purged': '실패 작업 모두 지우기'
        },
        columns: {
          time: '시간',
          actor: '작업자',
          action: '이벤트',
          target: '대상',
          path: '요청',
          outcome: '결과'
        },
        actorRole: {
          system_admin: '시스템 관리자'
        },
        errors: {
          generic: '감사 로그 로드 실패'
        }
      },
      bulkApply: {
        label: '모든 기존 워크스페이스에 적용',
        tooltip: '저장한 값은 기본적으로 새로 생성되는 워크스페이스에만 적용됩니다. 이 버튼을 누르면 현재 값을 모든 기존 워크스페이스에도 덮어씁니다.',
        confirmBtn: '적용 확인',
        confirmBody: '모든 기존 워크스페이스의 저장 용량을 {value} {unit}로 덮어씁니다. 운영팀이 개별로 조정한 워크스페이스의 용량도 함께 덮어쓰여집니다. 계속하시겠습니까?',
        success: '{count}개 워크스페이스의 저장 용량을 {value} {unit}로 갱신했습니다',
        failed: '모든 워크스페이스에 적용 실패'
      },
      userList: {
        title: '플랫폼 사용자 목록',
        rowLabel: '사용자 관리',
        rowDesc: '워크스페이스 전체의 등록 사용자를 페이지네이션 및 사용자 이름/이메일 필터와 함께 표시합니다.',
        searchPlaceholder: '사용자 이름 또는 이메일로 필터링',
        refresh: '새로고침',
        active: '활성',
        inactive: '비활성',
        colUsername: '사용자 이름',
        colStatus: '상태',
        colRole: '역할',
        colCreated: '생성일',
        colAction: '작업',
        prev: '이전',
        next: '다음',
        pageCount: '페이지 내 {total}개',
        enable: '활성화',
        disable: '비활성화',
        resetPassword: '비밀번호 재설정',
        confirmDisable: '{name} 계정을 비활성화할까요? 활성 세션이 즉시 취소됩니다.',
        confirmEnable: '{name} 계정을 활성화할까요?',
        enabledMsg: '{name} 활성화됨',
        disabledMsg: '{name} 비활성화됨',
        actionFailed: '작업 실패',
        loadFailed: '사용자 목록을 불러오지 못했습니다',
        roleAdmin: '시스템 관리자',
        roleUser: '일반 사용자',
        confirmRevoke: '{name}의 시스템 관리자 권한을 취소할까요?',
        confirmPromote: '{name}을(를) 시스템 관리자로 승격할까요?',
        revokeBtn: '취소',
        promoteBtn: '승격',
        revokedMsg: '{name}의 관리자 권한을 취소했습니다',
        promotedMsg: '{name}을(를) 시스템 관리자로 승격했습니다',
        roleFailed: '역할 변경 실패'
      },
      passwordReset: {
        dialogTitle: '다른 사용자의 비밀번호 재설정',
        warning: '보안에 민감한 작업입니다. 사용자 이메일을 반드시 확인하세요. 여기서는 자신의 비밀번호를 재설정할 수 없습니다.',
        emailLabel: '사용자 이메일',
        newPasswordLabel: '새 비밀번호',
        newPasswordPlaceholder: '새 비밀번호 입력',
        confirmPasswordLabel: '새 비밀번호 확인',
        confirmPasswordPlaceholder: '새 비밀번호를 다시 입력',
        confirmBtn: '재설정 확인',
        success: '비밀번호가 재설정되고 기존 세션이 만료되었습니다',
        failed: '비밀번호 재설정 실패'
      },
      createUser: {
        action: '사용자 생성',
        dialogTitle: '새 사용자 생성',
        warning: '보안에 민감한 작업입니다. 새 사용자가 로그인할 때 사용할 사용자 이름과 이메일을 신중히 확인하세요.',
        usernameLabel: '사용자 이름',
        usernamePlaceholder: '2~50자',
        emailLabel: '이메일',
        emailPlaceholder: '올바른 이메일 주소를 입력하세요',
        autoGenerateLabel: '무작위 비밀번호 자동 생성',
        newPasswordLabel: '새 비밀번호',
        newPasswordPlaceholder: '8~32자, 문자와 숫자 포함',
        confirmPasswordLabel: '새 비밀번호 확인',
        confirmPasswordPlaceholder: '새 비밀번호를 다시 입력',
        confirmBtn: '사용자 생성',
        success: '사용자가 생성되었습니다',
        successIdempotent: '이미 존재하는 사용자입니다. 변경 사항이 없습니다',
        missingPassword: '사용자는 생성되었지만 생성된 비밀번호가 반환되지 않았습니다. 비밀번호를 재설정해 접근을 복구하세요.',
        failed: '사용자 생성 실패',
        validation: {
          usernameRequired: '사용자 이름을 입력하세요',
          usernameLength: '사용자 이름은 2~50자여야 합니다',
          emailRequired: '이메일을 입력하세요',
          emailInvalid: '올바른 이메일 주소를 입력하세요',
          confirmRequired: '새 비밀번호를 다시 입력하세요',
          passwordMismatch: '비밀번호가 일치하지 않습니다'
        },
        generated: {
          successTitle: '사용자가 생성되었습니다',
          successBody: '이 사용자를 위한 무작위 비밀번호가 생성되었습니다. 이 비밀번호는 한 번만 표시됩니다.',
          usernameLabel: '사용자 이름',
          emailLabel: '이메일',
          passwordLabel: '생성된 비밀번호',
          copyBtn: '계정 정보 복사',
          copySuccess: '계정 정보가 클립보드에 복사되었습니다',
          acknowledgeBtn: '비밀번호를 저장했습니다'
        }
      },
      reset: {
        label: '초기화',
        tooltip: 'UI 재정의 값을 제거하고 환경 변수 또는 내장 기본값으로 되돌립니다',
        confirmBtn: '초기화 확인',
        confirmBody: '「{label}」을(를) 초기화하시겠습니까? DB에 저장된 재정의 값이 삭제되고 환경 변수 또는 내장 기본값으로 되돌아갑니다.',
        success: '기본값으로 초기화되었습니다',
        failed: '초기화 실패'
      },
      messages: {
        loadFailed: '시스템 설정을 불러오지 못했습니다',
        saveSuccess: '저장되었습니다',
        saveFailed: '저장 실패'
      },
      listConfirm: {
        ssrf: {
          whitelist: {
            remove: {
              header: 'SSRF 화이트리스트 항목 제거',
              body: '{entry} 항목을 SSRF 화이트리스트에서 제거하시겠습니까? 제거 후에는 해당 항목이 다시 SSRF 보호 대상이 됩니다.',
              confirmBtn: '제거 확인'
            },
            add: {
              header: 'SSRF 화이트리스트 항목 추가',
              body: '{entry} 항목을 SSRF 화이트리스트에 추가하시겠습니까? 일치하는 호스트 / IP / 대역은 SSRF 보호를 우회하여 에이전트가 내부 서비스에 접근할 수 있게 됩니다. 신뢰하는 항목만 추가하세요.',
              confirmBtn: '추가 확인'
            }
          }
        }
      },
      confirm: {
        confirmBtn: '저장 확인',
        cancelBtn: '취소',
        emptyValue: '(비어 있음)',
        bodyAuthRegistrationMode: '「{label}」을(를) {value}(으)로 변경하려고 합니다.\n\nself_serve로 전환하면 인터넷의 누구나 계정을 만들 수 있습니다. 의도된 동작인지 확인해 주세요.',
        bodySandboxDockerEnabled: '켜면 워크스페이스 관리자가 로컬 Docker 데몬을 샌드박스로 지정할 수 있습니다. 로컬 docker.sock은 호스트 root와 같습니다. 데몬을 마운트했거나 TLS가 있는 원격 tcp:// 를 쓰는 프라이빗 단일 노드에서만 사용하세요.'
      },
      enumLabels: {
        auth: {
          default_tenant_mode: {
            create_personal: '개인 공간 자동 생성',
            tenantless: '공간을 자동 생성하지 않음'
          },
          registration_mode: {
            self_serve: '셀프 가입 (누구나 가입 가능)',
            invite_register: '초대 가입 (유효한 링크 필요)',
            invite_only: '가입 금지 (기존 계정은 초대 수락 가능)'
          }
        }
      },
      keyDescriptions: {
        model: {
          max_concurrency: '백그라운드 작업(문서 색인/보강)이 단일 모델에 대해 갖는 기본 동시 호출 상한이며, 모델 ID 기준으로 모든 복제본이 공유합니다. 매 호출마다 실시간으로 읽고 재시작 없이 즉시 적용됩니다. 0 또는 음수는 기본 상한을 해제합니다(각 모델은 모델 관리에서 설정한 자체 상한을 계속 준수함). 백그라운드 작업에만 영향을 주며 대화형 채팅에는 영향을 주지 않습니다.'
        },
        asynq: {
          core_concurrency: '문서 및 수동 파싱의 프로세스별 보장 동시성입니다. 공유 탄력 풀도 사용할 수 있습니다. 최소 1, 서비스 재시작이 필요합니다.',
          postprocess_concurrency: '파싱 마무리 및 보강 작업 분배 전용 프로세스별 동시성입니다. 최소 1, 서비스 재시작이 필요합니다.',
          enrichment_concurrency: '요약, 이미지, 그래프 및 질문 생성의 프로세스별 보장 동시성입니다. 공유 탄력 풀도 사용할 수 있습니다. 최소 1, 서비스 재시작이 필요합니다.',
          maintenance_concurrency: '동기화, 일괄 작업 및 정리의 프로세스별 동시성으로 사용자 파이프라인과 격리됩니다. 최소 1, 서비스 재시작이 필요합니다.',
          shared_concurrency: '핵심 파싱과 콘텐츠 보강이 적체에 따라 공유하는 프로세스별 탄력 동시성입니다. 최소 1, 서비스 재시작이 필요합니다.',
          wiki_concurrency: '상위 작업과 분리된 전용 Wiki 워커 풀의 프로세스별 동시성입니다. 최소값은 1이며 적용하려면 서비스를 재시작해야 합니다.'
        },
        tenant: {
          max_owned_per_user: '슈퍼유저가 아닌 사용자가 셀프 서비스로 소유할 수 있는 최대 워크스페이스 수입니다. 워크스페이스 생성 시마다 읽으며 저장 즉시 적용됩니다. 0은 내장 기본값 10을 사용하고, 음수는 제한을 완전히 해제합니다(공개 배포에는 권장하지 않음).',
          self_service_creation_enabled: '비슈퍼유저가 공간을 직접 만들 수 있는지 설정합니다. 비활성화하면 일반 사용자는 초대로만 기존 공간에 참여할 수 있으며, 크로스 워크스페이스 슈퍼유저는 계속 만들 수 있습니다.',
          default_storage_quota_gb: '신규 워크스페이스 생성 시 기본으로 할당되는 저장 용량(GB)으로, 벡터·원본·텍스트·인덱스 등을 포함합니다. 생성 시에만 읽으며, 변경은 이후 생성되는 워크스페이스에만 적용되고 기존 워크스페이스에는 소급되지 않습니다. 0 또는 음수는 내장 기본값 10GB를 사용합니다.',
          default_storage_quota_mb: '가입 시 생성되는 개인 공간에도 적용됩니다. 양수는 GB 설정보다 우선하며 0 이하는 GB 설정을 사용합니다. 각 설정은 데이터베이스, 환경 변수, 기본값 순으로 적용되며 새 공간에만 영향을 줍니다.',
          auto_create_api_key: '신규 워크스페이스에 full_access API Key를 자동 생성하고 생성 응답에 평문 token을 반환합니다. 기존 동작에 의존하는 연동에만 사용하세요. 기본값은 비활성화입니다.',
          auto_accept_invitation: '켜면 워크스페이스 관리자가 이메일로 가입된 사용자를 초대할 때 상대가 바로 멤버가 되며, 받은편지함에서 수락할 필요가 없습니다. 끄면 「초대 발송 → 상대가 수락」 흐름을 유지합니다. 저장 즉시 적용됩니다.'
        },
        ssrf: {
          whitelist: 'SSRF 보호 허용 목록입니다. example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1 형식을 입력할 수 있습니다. 저장 즉시 적용됩니다. SSRF_WHITELIST_EXTRA 환경 변수는 배포자가 관리하며 여기서 덮어쓰지 않습니다.'
        },
        sandbox: {
          docker_enabled: 'Docker 샌드박스 백엔드를 허용할지 설정합니다. 로컬 docker.sock은 호스트 root와 같으므로 기본값은 꺼짐입니다. 시스템 관리자만 켤 수 있으며 저장 즉시 적용됩니다. 데몬 소켓을 마운트했거나 TLS가 있는 원격 tcp:// 를 쓰는 프라이빗 단일 노드에서만 켜세요.'
        },
        auth: {
          registration_mode: '가입 모드입니다. 공개 가입은 누구나 계정을 만들 수 있고, 초대 가입은 유효한 초대 링크가 필요합니다. 가입 금지 상태에서도 기존 계정은 초대를 수락할 수 있습니다. 저장 즉시 적용됩니다.',
          default_tenant_mode: '공개 가입 후 공간 초기화 정책입니다. create_personal은 개인 공간을 만들고 Owner를 부여하며, tenantless는 초대 수락 또는 직접 공간 생성 전까지 계정만 만듭니다.',
          complex_password_enabled: '복잡한 비밀번호를 사용할지 여부입니다. 활성화하면 비밀번호에 대문자, 소문자, 숫자 및 특수 문자가 포함되어야 합니다. 변경 사항은 즉시 적용되며, 새로 가입하는 사용자 또는 비밀번호를 새로 변경하거나 재설정하는 경우에만 적용됩니다. 특수 문자는 다음을 포함합니다: {specialChars}'
        }
      },
      keyLabels: {
        model: {
          max_concurrency: '모델 기본 동시 처리 상한'
        },
        asynq: {
          core_concurrency: '핵심 파싱 보장 동시성',
          postprocess_concurrency: '후처리 동시성',
          enrichment_concurrency: '콘텐츠 보강 보장 동시성',
          maintenance_concurrency: '유지 관리 동시성',
          shared_concurrency: '공유 탄력 동시성',
          wiki_concurrency: 'Wiki 워커 동시성'
        },
        tenant: {
          max_owned_per_user: '사용자당 최대 워크스페이스 수',
          self_service_creation_enabled: '사용자 공간 직접 생성 허용',
          default_storage_quota_gb: '신규 워크스페이스 기본 저장 용량 (GB)',
          default_storage_quota_mb: '신규 워크스페이스 기본 저장 용량 (MB)',
          auto_create_api_key: '신규 워크스페이스 API Key 자동 생성',
          auto_accept_invitation: '등록된 사용자 초대 시 자동 가입'
        },
        ssrf: {
          whitelist: 'SSRF 보호 허용 목록'
        },
        sandbox: {
          docker_enabled: 'Docker 샌드박스 사용'
        },
        auth: {
          registration_mode: '가입 모드',
          default_tenant_mode: '기본 공간 프로비저닝',
          complex_password_enabled: '복잡한 비밀번호 사용'
        }
      },
      runtime: {
        title: '작업 큐 런타임',
        description: '백그라운드 작업 큐의 실시간 부하와 각 독립 워커 풀의 프로세스별 동시성 설정입니다. 5초마다 자동 새로고침됩니다.',
        refresh: '새로고침',
        autoRefresh: '자동 새로고침 (5초마다)',
        loading: '불러오는 중...',
        retry: '다시 시도',
        unavailableTitle: '작업 큐를 사용할 수 없음',
        unavailable: '현재 배포는 Redis / asynq 큐가 없습니다(Lite 모드) — 표시할 내용이 없습니다.',
        empty: '표시할 큐 데이터가 없습니다',
        detailsTitle: '큐 세부 정보',
        detailsDescription: '각 처리 경로의 실시간 부하와 대기 상태입니다. ‘최종 실패’는 재시도 한도를 초과하여 자동 실행이 중지된 작업입니다.',
        poolsTitle: '워커 풀',
        poolsDescription: '각 단계에 보장 용량이 있으며 핵심 파싱과 보강은 공유 탄력 풀도 사용할 수 있습니다.',
        perInstance: '카드 값은 클러스터 실행 중/용량',
        poolConfigured: '프로세스별 {value}',
        poolInstances: '인스턴스 {value}개',
        poolUtilization: '사용률 {value}%',
        queueCount: '큐 {value}개',
        weightShort: '가중치 {value}',
        footnote: '카드는 클러스터 실행 중/실시간 용량을 표시합니다. 프로세스별 설정은 재시작 후 적용되며 공유 풀은 핵심 파싱과 보강 큐만 처리합니다.',
        updatedAt: '{value} 업데이트됨',
        models: {
          title: '모델 동시성',
          description: '백그라운드 작업이 실제로 모델 서비스에 진입할 때의 동시성입니다. 위 섹션은 작업 스케줄링이고, 이 섹션은 별도의 모델 서비스 스로틀 단계입니다.',
          scope: '실행 중은 클러스터 전체 · 대기는 로컬',
          disabled: '백그라운드 모델 동시성 제어가 비활성화되어 있습니다. 전역 설정에서 모델 기본 동시성 상한을 구성하세요.',
          empty: '아직 모델 활동이 없습니다. 첫 백그라운드 호출 후 모델이 표시됩니다.',
          backgroundOnly: '백그라운드 작업만; 대화형 채팅은 제외',
          status: {
            queued: '스로틀 중',
            full: '한도 도달'
          },
          columns: {
            model: '모델 ID',
            active: '실행 중',
            waiting: '스로틀 대기',
            usage: '동시성'
          }
        },
        tasks: {
          title: '작업 상세 · {queue}',
          description: '모든 작업 상태를 확인합니다. 예약 및 재시도 작업은 다음 실행 순서로, 나머지는 최신 작업부터 표시됩니다.',
          listTitle: '{state} 작업',
          openAria: '{queue}의 {state} 작업 {count}개 보기',
          unavailable: '현재 배포에서는 실패 작업 상세 정보를 볼 수 없습니다',
          empty: '이 큐에는 {state} 작업이 없습니다',
          loadError: '실패 작업 상세 정보를 불러오지 못했습니다',
          loadMore: '더 보기',
          loadedSummary: '{count}개 로드됨 · 스크롤하거나 눌러 더 보기',
          loadedAll: '총 {count}개 모두 로드됨',
          loadingMore: '더 불러오는 중…',
          attempts: '실행 {current}/{max}',
          unknownTarget: '연결된 대상을 확인할 수 없음',
          knowledgeBaseLabel: '지식 베이스 ID',
          knowledgeLabel: '문서 ID',
          taskIDLabel: '비즈니스 작업 ID',
          tenantLabel: '워크스페이스 ID',
          sourceLabel: '소스 ID',
          targetLabel: '대상 ID',
          sourceKBLabel: '소스 지식 베이스',
          targetKBLabel: '대상 지식 베이스',
          dataSourceLabel: '데이터 소스 ID',
          syncLogLabel: '동기화 기록 ID',
          knowledgeCountLabel: '문서 수',
          enqueuedAt: '큐 등록 시간',
          startedAt: '시작 시간',
          nextProcessAt: '다음 실행',
          lastFailedAt: '마지막 실패',
          completedAt: '완료 시간',
          deadline: '마감 시간',
          worker: '실행 인스턴스',
          health: '실행 상태',
          orphaned: '실행 인스턴스 연결 끊김; 복구 대기 중',
          cancel: '작업 취소',
          cancelConfirm: '비즈니스 취소 흐름을 사용하여 같은 문서의 관련 작업도 중지합니다. 취소할까요?',
          runNow: '지금 실행',
          runNowConfirm: '재시도 횟수를 초기화하지 않고 즉시 대기 상태로 이동합니다. 계속할까요?',
          deleteRecord: '기록 지우기',
          deleteConfirm: '현재 큐에서만 기록을 제거합니다. 원래 작업은 실행되거나 완료되지 않으며 기록 로그는 유지됩니다. 지울까요?',
          purgeArchived: '실패 작업 모두 지우기',
          purgeArchivedConfirm: '현재 큐의 실패 기록 {count}건을 한 번에 제거합니다. 큐의 실패 작업만 정리하며 실행 중이거나 대기 중인 작업에는 영향이 없고, 이미 실패한 문서의 비즈니스 상태도 되돌리지 않습니다. 지울까요?',
          purgeArchivedSuccess: '실패 작업 {count}건을 지웠습니다',
          purgeArchivedError: '실패 작업을 지우지 못했습니다',
          stateFilter: '작업 상태별 필터',
          actionError: {
            cancel: '작업을 취소하지 못했습니다',
            run_now: '작업을 실행하지 못했습니다',
            delete: '실패 기록을 지우지 못했습니다'
          },
          actionSuccess: {
            cancel: '작업을 취소했습니다',
            run_now: '작업이 대기열로 이동했습니다',
            delete: '실패 기록을 지웠습니다'
          },
          guides: {
            active: '실행 인스턴스와 시작 시간, 마감 및 고립 상태를 확인합니다. 안전한 비즈니스 취소가 가능한 작업만 취소할 수 있습니다.',
            pending: '아직 worker가 가져가지 않은 작업입니다. 취소 시 비즈니스 상태도 함께 갱신됩니다.',
            scheduled: '예약 작업을 즉시 실행하거나 지원되는 문서 작업을 안전하게 취소할 수 있습니다.',
            retry: '마지막 오류, 시도 횟수 및 다음 실행 시간을 확인하세요.',
            archived: '원인을 해결한 후 다시 실행하세요. 기록 삭제는 원래 작업을 완료하지 않습니다.',
            completed: '보존 기간이 설정된 최근 완료 작업만 표시되며 관리 작업은 없습니다.'
          },
          states: {
            active: '실행 중',
            pending: '대기 중',
            scheduled: '예약',
            retry: '재시도 중',
            archived: '최종 실패',
            completed: '완료'
          },
          taskTypes: {
            documentProcess: '문서 파싱',
            manualProcess: '수동 재처리',
            temporaryDocumentProcess: '채팅 첨부 파일 파싱',
            postProcess: '문서 후처리',
            summary: '요약 생성',
            tableSummary: '표 요약 생성',
            question: '질문 생성',
            multimodal: '이미지 멀티모달 처리',
            graph: '지식 그래프 추출',
            sync: '데이터 소스 동기화',
            faqImport: 'FAQ 가져오기',
            batchReparse: '일괄 재파싱',
            batchDelete: '일괄 삭제',
            move: '문서 이동',
            indexDelete: '인덱스 삭제',
            kbClone: '지식 베이스 복제',
            kbDelete: '지식 베이스 삭제',
            wikiIngest: 'Wiki 콘텐츠 생성',
            wikiFinalize: 'Wiki 마무리'
          }
        },
        failedNotice: {
          title: '처리 대기 중인 실패 작업 {count}개',
          description: '아래 표의 빨간색 \'최종 실패\' 숫자를 눌러 원인을 확인한 뒤, 수정 후 수동으로 재시도하세요.'
        },
        status: {
          working: '처리 중',
          waiting: '대기 중',
          idle: '유휴',
          actionRequired: '조치 필요',
          retrying: '재시도 중',
          paused: '일시중지됨'
        },
        columns: {
          queue: '큐',
          active: '실행 중',
          pending: '대기',
          scheduled: '예약',
          retry: '재시도',
          archived: '실패',
          completed: '완료',
          latency: '최장 대기',
          status: '상태'
        },
        summary: {
          title: '실행 개요',
          active: '실행 중',
          pending: '대기 중',
          retry: '재시도 중',
          archived: '최종 실패'
        },
        pools: {
          core: '핵심 파싱',
          postprocess: '후처리 오케스트레이션',
          enrichment: '콘텐츠 보강',
          maintenance: '유지 관리 및 동기화',
          shared: '공유 탄력 풀',
          wiki: 'Wiki 풀'
        },
        poolDescriptions: {
          core: '문서 및 수동 파싱 보장 용량',
          postprocess: '파싱 마무리 및 보강 작업 분배',
          enrichment: '요약, 이미지, 그래프 및 질문 생성',
          maintenance: '데이터 소스 동기화, 일괄 작업 및 삭제 정리',
          shared: '적체에 따라 핵심 파싱 또는 보강이 공유',
          wiki: 'Wiki 콘텐츠 생성 및 전체 마무리'
        },
        queueNames: {
          default: '문서 파싱',
          chat_attachment: '대화 첨부파일 파싱',
          postprocess: '후처리 오케스트레이션',
          summary: '요약 생성',
          sync: '데이터 소스 동기화',
          low: '유지 관리 및 일괄 작업',
          multimodal: '멀티모달 처리',
          graph: '그래프 추출',
          question: '질문 생성',
          wiki: 'Wiki 처리'
        },
        queueDescriptions: {
          default: '문서 파싱, 수동 재파싱',
          chat_attachment: '세션 내 업로드 첨부파일 파싱',
          postprocess: '파싱 마무리, 보강 분배',
          summary: '문서 요약, 테이블 요약',
          sync: '수동 및 예약 동기화',
          low: 'FAQ 가져오기, 일괄 재파싱, 삭제 정리',
          multimodal: '이미지 OCR, 시각 설명',
          graph: '청크 단위 그래프 추출',
          question: '청크 단위 질문 생성',
          wiki: '콘텐츠 생성, 인덱스 마무리'
        },
        errors: {
          generic: '큐 상태를 불러오지 못했습니다'
        }
      },
      sections: {
        other: {
          title: '기타 설정',
          description: '표준 제품 그룹에 포함되지 않은 현재 배포의 설정입니다.'
        },
        security: {
          title: '네트워크 보안',
          description: 'SSRF 허용 목록과 Docker 샌드박스 허용 여부(로컬 docker.sock은 호스트 root와 같음)를 관리합니다.'
        },
        runtime: {
          title: '런타임 및 동시성',
          description: '백그라운드 워커 풀과 모델 서비스의 동시 처리 용량을 설정합니다.',
          restartHint: '워커 설정은 재시작 후 적용'
        },
        tenant: {
          title: '워크스페이스 기본값',
          description: '기존 워크스페이스를 변경하지 않고 신규 워크스페이스의 초기 할당량과 호환 동작을 설정합니다.'
        },
        access: {
          title: '가입 및 액세스',
          description: '가입 방식, 비밀번호 요구 사항, 공간 생성 규칙을 관리합니다.'
        }
      },
      priorityHint: {
        disclosure: '설정 소스 및 우선순위',
        tier1: '이 페이지에서 저장한 항목("재정의됨" 배지가 붙은 항목)은 항상 우선 적용되며, 환경 변수는 무시됩니다.',
        tier2: '여기서 저장하지 않은 항목은 환경 변수를 따르며, 환경 변수도 없으면 내장 기본값을 사용합니다.',
        tier3: '특정 항목을 다시 환경 변수 기반으로 되돌리려면 해당 행의 "초기화" 버튼을 누르세요.'
      }
    },
    messages: {
      fetchFailed: '시스템 정보 가져오기 실패',
      networkError: '네트워크 오류, 나중에 다시 시도해주세요'
    }
  }
}
