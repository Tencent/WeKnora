export default {
  notifications: {
    pending: '처리 필요',
    history: '알림 기록',
    clearRead: '읽은 알림 삭제',
    clearReadConfirm: '읽은 알림을 삭제할까요? 실행 기록, 대화 및 처리 대기 항목은 유지됩니다.',
    handle: '열기',
    review: '승인 검토',
    unreadCount: '읽지 않음 {count}개',
    unread: '읽지 않음',
    newItems: '새 알림이 있습니다. 새로고침',
    loadError: '알림을 불러오지 못했습니다',
    awaitingApproval: '작업 승인 대기 중',
    awaitingAuthorization: '인증 완료 대기 중',
    awaitingReply: '답변 대기 중',
    title: '알림',
    markAllRead: '모두 읽음',
    pendingInvitations: '대기 중인 초대 {count}개',
    empty: '알림 없음',
    kinds: {
      automation_succeeded: '자동화 완료',
      automation_failed: '자동화 실패',
      automation_awaiting_approval: '자동화 승인 필요',
      automation_paused: '자동화 일시 중지'
    }
  },
  toolbox: {
    title: '도구함',
    description: '에이전트의 스킬, 외부 도구 및 브라우저 연결을 관리합니다.',
    unavailable: '현재 워크스페이스에서 사용할 수 있는 도구가 없습니다.'
  },
  resourceSort: {
    title: '정렬',
    updatedTime: '업데이트 시간',
    updatedTimeDescription: '기본 옵션입니다.',
    createdTime: '생성 시간',
    createdTimeDescription: '최근 또는 가장 먼저 생성된 항목을 확인합니다',
    name: '이름',
    nameDescription: '이름을 알고 있는 항목을 빠르게 찾습니다',
    recentlyUpdated: '최근 업데이트',
    earliestUpdated: '오래된 업데이트',
    recentlyCreated: '최근 생성',
    earliestCreated: '가장 먼저 생성',
    nameAscending: 'A–Z',
    nameDescending: 'Z–A'
  },
  mermaid: {
    diagram: '다이어그램',
    expand: '전체 화면',
    zoomIn: '확대',
    zoomOut: '축소',
    reset: '초기화',
    download: '이미지 다운로드',
    close: '닫기',
    downloading: '다운로드 중...'
  },
  tools: {
    multiKbSearch: '크로스 KB 검색',
    searchKnowledge: '지식베이스 검색',
    readDocument: '문서 읽기',
    listDocuments: '문서 목록 보기',
    knowledgeSearch: '지식베이스 검색',
    grepChunks: '텍스트 패턴 검색',
    getChunkDetail: '청크 상세 조회',
    listKnowledgeChunks: '지식 청크 목록',
    listKnowledgeBases: '지식베이스 목록',
    getDocumentInfo: '문서 정보 조회',
    queryKnowledgeGraph: '지식 그래프 쿼리',
    think: '깊이 생각하기',
    todoWrite: '계획 수립',
    askUser: '사용자에게 질문'
  },
  commandPalette: {
    placeholder: '지식베이스, 파일, 대화 검색…',
    clearRecent: '지우기',
    retrieval: '검색 설정',
    untitledSession: '제목 없는 대화',
    errors: {
      knowledge: '지식 베이스를 검색하지 못했습니다. 다시 시도해 주세요.',
      messages: '대화 내용을 검색하지 못했습니다. 다시 시도해 주세요.'
    },
    hotkey: {
      select: '이동',
      enter: '열기',
      cmdNumber: '바로 이동',
      cmdEnter: '대화 시작',
      esc: '닫기'
    },
    empty: {
      noResults: '일치하는 결과 없음',
      askAi: 'AI에게 직접 질문하기',
      adjustRetrieval: '검색 설정 조정'
    },
    quick: {
      newChat: '새 대화',
      knowledgeBases: '지식베이스 열기',
      agents: '에이전트 열기',
      organizations: '공유 공간 열기',
      settings: '설정 열기',
      productTour: '사용 가이드',
      assistant: '{name} 열기'
    },
    match: {
      vector: '벡터',
      keyword: '키워드'
    },
    group: {
      chunks: '지식베이스 파일',
      messages: '대화 메시지',
      kbs: '지식베이스',
      agents: '에이전트',
      sessionsByTitle: '대화（제목별）',
      commands: '명령',
      recent: '최근 검색',
      quickActions: '빠른 작업'
    },
    scope: {
      placeholder: '이 지식베이스에서 검색…',
      remove: '범위 해제 (Backspace)'
    }
  },
  preview: {
    tab: '미리보기',
    loading: '문서 미리보기 로딩 중...',
    loadFailed: '문서 미리보기 로드 실패',
    retry: '재시도',
    unsupported: '이 파일 유형은 온라인 미리보기를 지원하지 않습니다',
    unsupportedHint: '파일을 다운로드하여 로컬 앱으로 열어주세요',
    zoomIn: '확대',
    zoomOut: '축소',
    fullscreen: '전체 화면',
    exitFullscreen: '전체 화면 종료',
    htmlRendered: '렌더링 미리보기',
    htmlSource: '원본 보기',
    audioLoading: '오디오 로딩 중…',
    audioNotSupported: '브라우저가 오디오 재생을 지원하지 않습니다',
    videoNotSupported: '브라우저가 동영상 재생을 지원하지 않습니다'
  },
  promptTemplate: {
    noTemplates: '아직 템플릿이 없습니다.',
    selectTemplate: '템플릿 선택',
    useTemplate: '템플릿 사용',
    resetDefault: '기본값 복원',
    default: '기본',
    withKnowledgeBase: '지식베이스',
    withWebSearch: '웹 검색'
  },
  time: {
    today: '오늘',
    yesterday: '어제',
    last7Days: '최근 7일',
    last30Days: '최근 30일',
    lastYear: '최근 1년',
    earlier: '이전',
    pinned: '고정됨'
  },
  platform: {
    subtitle: '대규모 언어 모델 기반 엔터프라이즈 지식 프레임워크',
    description: 'RAG 검색, 에이전트 추론, Wiki 지식베이스로 문서를 진정으로 이해하고 활용합니다',
    rag: 'RAG 강화 생성',
    agent: 'ReAct 에이전트',
    wiki: 'Wiki 지식베이스',
    hybridSearch: '하이브리드 검색',
    multimodalParsing: '멀티모달 문서 파싱',
    hybridSearchEngine: '하이브리드 검색 + 지식 그래프',
    ragQandA: 'ReAct 에이전트 Q&A',
    independentTenant: '독립 워크스페이스',
    fullApiAccess: '전체 API 접근',
    knowledgeBaseManagement: '지식베이스 관리',
    carousel: {
      agenticRagTitle: 'Agentic RAG',
      agenticRagDesc: 'ReAct 추론 + 도구 호출 + 다단계 사고',
      hybridSearchTitle: '하이브리드 검색 전략',
      hybridSearchDesc: 'BM25 + 벡터 + 지식 그래프',
      wikiTitle: 'Wiki 지식베이스',
      wikiDesc: '문서를 구조화된 상호 연결 지식으로 정제',
      smartDocRetrievalTitle: '지능형 문서 검색',
      smartDocRetrievalDesc: 'PDF/Word/이미지 다중 형식 파싱'
    }
  },
  font: {
    uiFont: '인터페이스 글꼴',
    uiFontDescription: '메뉴, 본문, 버튼 등 인터페이스 대부분의 텍스트에 사용되는 글꼴입니다',
    monoFont: '코드 글꼴',
    monoFontDescription: '코드 블록, 터미널 명령, API 키, 파일 경로 등 기술 텍스트에 사용됩니다. 모든 문자가 같은 너비로 표시되어 0과 O, 1과 l을 구분할 수 있습니다.',
    selectFont: '글꼴 선택',
    sansPreview: '샘플 Sample 글꼴 — Aa Gg Oo 0123',
    monoPreview: 'const msg = \'Hello\'; // 0O1l',
    fontSize: '글꼴 크기',
    fontSizeDescription: '인터페이스 전체(텍스트, 아이콘, 간격)를 확대/축소하며 즉시 적용됩니다',
    size: {
      small: '작게',
      normal: '보통',
      large: '크게'
    },
    mono: {
      system: '시스템 기본',
      menlo: 'Menlo',
      monaco: 'Monaco',
      consolas: 'Consolas',
      cascadia: 'Cascadia Code',
      'dejavu-mono': 'DejaVu Sans Mono',
      'liberation-mono': 'Liberation Mono',
      monospace: '일반 고정폭'
    },
    sans: {
      system: '시스템 기본',
      pingfang: 'PingFang SC',
      georgia: 'Georgia (세리프)',
      yahei: 'Microsoft YaHei',
      times: 'Times New Roman (세리프)',
      'noto-cjk': 'Noto Sans CJK',
      'dejavu-serif': 'DejaVu Serif (세리프)',
      'sans-serif': '일반 산세리프'
    }
  },
  theme: {
    theme: '테마',
    themeDescription: '인터페이스의 표시 테마를 선택하세요. 시스템 설정에 따라 자동 전환을 지원합니다',
    light: '라이트',
    dark: '다크',
    system: '시스템 설정',
    selectTheme: '테마 선택'
  },
  general: {
    title: '일반 설정',
    allSettings: '모든 설정',
    personalSettings: '개인 설정',
    helpAndDocs: '도움말 및 문서',
    description: '언어, 외관 등 기본 옵션 설정',
    settings: '설정',
    close: '설정 닫기'
  },
  language: {
    zhCN: '简体中文',
    zhTW: '번체 중국어',
    enUS: 'English',
    ruRU: 'Русский',
    viVN: 'Tiếng Việt',
    koKR: '한국어',
    jaJP: '日本語',
    selectLanguage: '언어 선택',
    language: '언어',
    languageDescription: '인터페이스 표시 언어 선택',
    languageSaved: '언어 설정이 저장되었습니다'
  },
  error: {
    networkError: '네트워크 오류, 연결을 확인해 주세요',
    requestTimeout: '요청 시간이 초과되었습니다. 파일이 크거나 네트워크가 느린 경우 다시 시도해 주세요',
    invalidCredentials: '사용자 이름 또는 비밀번호가 올바르지 않습니다',
    tokenRefreshFailed: '토큰 갱신 실패',
    pleaseRelogin: '다시 로그인해 주세요',
    fileSizeExceeded: '파일 크기는 {size}MB를 초과할 수 없습니다!',
    unsupportedFileType: '지원하지 않는 파일 형식입니다!',
    invalidFileType: '잘못된 파일 형식입니다!',
    invalidImageLink: '유효하지 않은 이미지 링크',
    missingKbId: '지식베이스 ID가 누락되었습니다',
    tokenNotFound: '로그인 토큰을 찾을 수 없습니다. 다시 로그인해주세요',
    streamFailed: '스트림 연결 실패',
    initialization: {
      checkFailed: '검사 실패',
      testFailed: '테스트 실패'
    },
    tenant: {
      listFailed: '워크스페이스 목록 조회 실패',
      searchFailed: '워크스페이스 검색 실패',
      getApiPrincipalConfigFailed: 'API principal config 조회 실패',
      updateApiPrincipalConfigFailed: 'API principal config 업데이트 실패',
      createApiPrincipalTestTokenFailed: 'API 테스트 토큰 생성 실패',
      updateFailed: '워크스페이스 정보 업데이트 실패',
      deleteFailed: 'Failed to delete workspace'
    },
    model: {
      createFailed: '모델 생성 실패',
      getFailed: '모델 조회 실패',
      updateFailed: '모델 업데이트 실패',
      deleteFailed: '모델 삭제 실패'
    },
    auth: {
      loginFailed: '로그인 실패',
      registerFailed: '회원가입 실패',
      getUserFailed: '사용자 정보 조회 실패',
      getTenantFailed: '워크스페이스 정보 조회 실패',
      updatePreferencesFailed: '개인 설정 업데이트 실패',
      refreshTokenFailed: '토큰 갱신 실패',
      logoutFailed: '로그아웃 실패',
      validateTokenFailed: '토큰 검증 실패'
    }
  },
  file: {
    upload: '파일 업로드'
  },
  common: {
    add: '추가',
    me: '나',
    confirm: '확인',
    cancel: '취소',
    unsavedChanges: {
      title: '저장되지 않은 변경 사항',
      body: '지금 닫으면 변경 사항이 사라집니다. 닫으시겠습니까?',
      discard: '변경 사항 버리기',
      keepEditing: '계속 편집'
    },
    fullscreen: '전체 화면',
    exitFullscreen: '전체 화면 종료',
    save: '저장',
    delete: '삭제',
    edit: '편집',
    copy: '복사',
    copied: '복사됨',
    copySuccess: '복사되었습니다',
    default: '기본값',
    create: '생성',
    download: '다운로드',
    refresh: '새로고침',
    loading: '로딩 중...',
    noData: '데이터 없음',
    noMoreData: '모든 내용을 로드했습니다',
    loadMore: '더 보기',
    error: '오류',
    success: '성공',
    failed: '실패',
    info: '정보',
    selectAll: '전체 선택',
    yes: '예',
    no: '아니오',
    close: '닫기',
    back: '뒤로',
    next: '다음',
    finish: '완료',
    all: '전체',
    clear: '지우기',
    website: '공식 웹사이트',
    clawhubSkill: 'Claw Skill',
    github: 'GitHub',
    githubStarTip: 'GitHub에서 저장소로 이동합니다. 유용하다면 Star를 부탁드립니다',
    on: '켜기',
    off: '끄기',
    confirmDelete: '삭제 확인',
    createSuccess: '생성 성공',
    deleteSuccess: '삭제 성공',
    deleteFailed: '삭제 실패',
    updateSuccess: '업데이트 성공',
    saveSuccess: '저장 성공',
    saveFailed: '저장 실패',
    operationFailed: '작업 실패',
    file: '파일',
    skill: '스킬',
    knowledgeBase: '지식베이스',
    noResult: '결과 없음',
    remove: '제거',
    avatar: '아바타',
    defaultUser: '사용자',
    copyFailed: '복사 실패',
    retry: '재시도',
    expand: '펼치기',
    collapse: '접기'
  },
  resourceOrigin: {
    mine: '내 생성',
    mineTooltip: '현재 사용자가 생성',
    tenant: '워크스페이스',
    tenantTooltip: '워크스페이스의 다른 멤버가 생성',
    tenantTooltipWithCreator: '{creator}님이 생성',
    space: '스페이스',
    spaceTooltip: '공유 공간 「{space}」에서 가져옴',
    spaceTooltipWithTenant: '공유 공간 「{space}」 · 출처 {tenant}',
    shared: '외부 공유',
    sharedTooltip: '공유 공간를 통해 외부 워크스페이스에서 접근'
  },
  listSpaceSidebar: {
    all: '모두',
    workspace: '워크스페이스',
    spaces: '나와 공유됨',
    favorites: '즐겨찾기',
    recents: '최근'
  },
  batchManage: {
    selectAll: '전체 선택',
    cancel: '취소',
    delete: '대화 삭제',
    deleteConfirmTitle: '대화 삭제',
    deleteConfirmBody: '선택한 {count}개의 대화를 삭제하시겠습니까? 삭제 후 복구할 수 없습니다.',
    deleteAllConfirmBody: '모든 대화를 삭제하시겠습니까? 이 작업은 취소할 수 없습니다.',
    deleteSuccess: '삭제 성공',
    deleteFailed: '삭제 실패, 나중에 다시 시도해 주세요'
  },
  contextualGuide: {
    stepOf: '{current} / {total}',
    skip: '건너뛰기',
    prev: '이전',
    next: '다음',
    done: '확인',
    interactHint: '강조된 영역을 클릭하여 계속하세요',
    chat: {
      steps: {
        done: {
          title: '이제 탐색해 보세요',
          desc: '업로드한 문서와 관련된 질문을 해 보고, 인용이 포함된 답변을 확인해 보세요.'
        },
        send: {
          title: '보내기로 대화 시작',
          desc: '전송하면 새 세션이 만들어지고, AI가 지식 베이스를 바탕으로 인용과 함께 답변합니다.'
        },
        input: {
          title: '질문 입력',
          desc: '알고 싶은 내용을 입력하거나, 위의 추천 질문을 눌러 빠르게 시작하세요.'
        },
        kb: {
          title: '지식 범위 선택',
          desc: '{\'@\'}를 눌러 지식 베이스나 파일을 선택하세요. 선택한 범위만 사용해 답변합니다. 선택하지 않으면 현재 에이전트 설정이 적용됩니다.'
        }
      }
    },
    kbDetail: {
      steps: {
        done: {
          title: '분석 후 사용 가능',
          desc: '문서가 색인되면 대화에서 {\'@\'}로 이 지식 베이스를 지정해 출처가 포함된 답변을 받을 수 있습니다.'
        },
        upload: {
          title: '문서 추가',
          desc: '여기에서 파일·폴더 업로드, URL 가져오기, 온라인 편집 콘텐츠 생성을 할 수 있습니다.'
        },
        intro: {
          title: '지식 베이스가 비어 있습니다',
          desc: '첫 자료를 추가해야 검색과 대화에 사용할 수 있습니다. 지원 형식은 드래그 앤 드롭으로도 업로드할 수 있습니다.'
        }
      }
    },
    agentCreate: {
      steps: {
        submit: {
          title: '에이전트 저장',
          desc: '강조된 확인 버튼을 눌러 완료하세요. 이후 채팅에서 이 에이전트를 선택할 수 있습니다.'
        },
        navTools: {
          title: '도구 및 MCP(선택)',
          desc: '스마트 추론 모드에서 내장 도구와 MCP로 검색·코드 등을 사용할 수 있습니다.'
        },
        multimodal: {
          title: '첨부 업로드 켜기',
          desc: '이미지·오디오 업로드와 첨부 파싱 규칙을 설정하세요. 이미지 업로드 시 아래에서 VLM을 선택해야 합니다.'
        },
        navMultimodal: {
          title: '첨부 업로드(선택)',
          desc: '채팅에서 이미지, 문서, 오디오 등 첨부를 허용합니다. 이미지 이해에는 시스템 설정의 VLM이 필요합니다.'
        },
        navWebsearch: {
          title: '웹 검색(선택)',
          desc: '최신 정보를 위해 외부 검색을 호출할 수 있게 합니다.'
        },
        knowledge: {
          title: '지식 범위',
          desc: '「전체」는 범용, 「선택」은 특정 도메인, 「없음」은 모델만 또는 웹 검색에 의존합니다.'
        },
        navKnowledge: {
          title: '지식 베이스 연결',
          desc: '에이전트가 검색할 지식 범위를 설정합니다. 기본값은 전체 지식 베이스입니다.'
        },
        model: {
          title: '모델 선택',
          desc: '구성된 대화 모델 중 선택하거나, 먼저 시스템 설정에서 추가하세요.'
        },
        navModel: {
          title: '대화 모델 연결',
          desc: '모든 에이전트에 KnowledgeQA 모델이 추론 엔진으로 필요합니다.'
        },
        name: {
          title: '이름과 설명',
          desc: '알아보기 쉬운 이름을 입력하세요. 스마트 추론 모드는 기본값이 채워질 수 있습니다.'
        },
        agentType: {
          title: '에이전트 유형 선택',
          desc: '프리셋은 시스템 프롬프트, 권장 도구, 지식 범위(Wiki 빌드, 데이터 분석 등)를 자동으로 채웁니다. 시나리오에 맞게 바꾸면 이름·설명도 함께 갱신됩니다.'
        },
        mode: {
          title: '실행 모드 선택',
          desc: '「빠른 답변」은 단순 Q&A용, 「스마트 추론」은 도구와 다단계 사고로 복잡한 작업에 적합합니다.'
        }
      }
    },
    agentList: {
      steps: {
        create: {
          title: '에이전트 만들기',
          desc: '에이전트는 모델·지식 베이스·도구·프롬프트를 묶은 재사용 가능한 어시스턴트입니다. 강조된 「에이전트 생성」을 클릭하세요.'
        }
      }
    },
    kbCreate: {
      steps: {
        submit: {
          title: '지식 베이스 생성',
          desc: '「확인」을 클릭하면 지식 베이스를 생성하지 않고 안내를 마칠 수 있습니다. 준비되면 이름을 입력하고 유형과 모델을 확인한 뒤 「생성」을 클릭하세요.'
        },
        faq: {
          title: 'FAQ 색인',
          desc: 'Q&A 쌍의 색인 방식을 선택합니다. 생성 후 이 페이지에서 FAQ 항목을 추가할 수 있습니다.'
        },
        multimodalVllm: {
          title: 'VLM 모델 선택',
          desc: '멀티모달에는 VLM이 필요합니다. 목록이 비어 있으면 시스템 설정에서 추가하세요.'
        },
        multimodalToggle: {
          title: '멀티모달 파싱 켜기',
          desc: '켜면 이미지가 포함된 업로드를 VLM으로 처리해 검색 품질을 높입니다.'
        },
        navMultimodal: {
          title: '멀티모달/이미지(선택)',
          desc: '차트·스캔·이미지가 많은 문서를 시각 이해가 필요할 때 활성화하세요.'
        },
        storage: {
          title: '스토리지(선택)',
          desc: '원본 파일 저장 위치(로컬 또는 객체 스토리지)입니다. 워크스페이스 기본값을 따르면 됩니다.'
        },
        chunking: {
          title: '청킹(선택)',
          desc: '문서를 검색 단위로 나누는 방식입니다. RAG에 맞춘 기본값을 그대로 쓰면 됩니다.'
        },
        parser: {
          title: '파서 엔진(선택)',
          desc: 'PDF·Office 파일 파싱 방식을 제어합니다. 기본값으로 대부분 충분하며 OCR이 필요할 때 조정하세요.'
        },
        embedding: {
          title: 'Embedding 모델',
          desc: '텍스트를 벡터로 바꿔 의미 검색을 지원합니다. 위의 벡터/키워드 색인과 함께 사용합니다.'
        },
        llm: {
          title: '대화/요약 모델',
          desc: '요약·답변 생성에 사용됩니다. 목록이 비어 있으면 드롭다운에서 설정으로 이동해 모델을 추가하세요.'
        },
        navModels: {
          title: '모델 구성(필수)',
          desc: '모든 지식 베이스에 대화 모델이 필요하며, 검색을 쓰려면 Embedding 모델도 필요합니다. 왼쪽 「모델 구성」을 여세요.'
        },
        indexing: {
          title: '색인 기능',
          desc: '벡터·키워드 검색이 기본으로 켜져 있습니다. Wiki·지식 그래프도 선택할 수 있습니다. 검색 방식은 하나 이상 유지하세요.'
        },
        name: {
          title: '이름 입력',
          desc: '「제품 매뉴얼」「고객 FAQ」처럼 알아보기 쉬운 이름을 입력하세요. 설명은 선택 사항입니다.'
        },
        type: {
          title: '유형 선택',
          desc: '문서 라이브러리는 PDF·Word 등 파일용, FAQ 라이브러리는 질문·답변 쌍용입니다. 생성 후 유형은 변경할 수 없습니다.'
        }
      }
    },
    tenantModels: {
      needChatModelFirst: '에이전트를 만들기 전에 대화 모델(KnowledgeQA)을 추가하세요.',
      stepsAgent: {
        done: {
          title: '이후 에이전트 생성',
          desc: '저장 후 설정을 닫고 「에이전트 생성」을 클릭하세요. 마법사가 모드·지식 베이스·첨부 업로드 옵션을 안내합니다.'
        },
        addModel: {
          title: '대화 모델 추가',
          desc: '「모델 추가」를 눌러 KnowledgeQA 유형을 구성하세요.'
        },
        intro: {
          title: '먼저 대화 모델을 구성하세요',
          desc: '에이전트 생성에는 KnowledgeQA 모델이 최소 하나 필요합니다. 시스템 설정에서 추가하세요(Embedding은 지식 베이스에만 필요).'
        }
      },
      steps: {
        done: {
          title: '추가 후 계속',
          desc: '모델을 저장하고 설정을 닫은 뒤 「새 지식 베이스」를 클릭하세요. 마법사가 유형·색인·모델 연결을 안내합니다.'
        },
        addModel: {
          title: '모델 추가',
          desc: '「모델 추가」를 눌러 KnowledgeQA(대화)와 Embedding 유형을 구성하세요. Lite 사용자는 Ollama로 로컬 모델을 받을 수 있습니다.'
        },
        intro: {
          title: '먼저 모델을 구성하세요',
          desc: '문서 지식 베이스에는 대화 모델(요약·Q&A)과 Embedding 모델(벡터 검색)이 각각 하나 이상 필요합니다. 시스템 설정에서 추가하세요.'
        }
      }
    },
    kbList: {
      steps: {
        create: {
          title: '첫 지식 베이스 만들기',
          desc: '지식 베이스에 문서와 FAQ를 보관합니다. 아래에 강조된 「새 지식 베이스」 버튼을 누르면 양식 작성을 안내합니다.'
        }
      }
    }
  },
  assistantEntryGuide: {
    stepOf: 'v0.9.0의 새로운 기능',
    skip: '알겠습니다',
    interactHint: '강조된 아이콘을 클릭해 어시스턴트를 열어 보세요.'
  },
  newUserGuide: {
    stepOf: '{current} / {total}',
    skip: '건너뛰기',
    prev: '이전',
    next: '다음',
    done: '완료',
    reopen: '사용 가이드',
    steps: {
      assistant: {
        title: 'WeKnora 어시스턴트를 만나보세요',
        desc: 'WeKnora의 버전, 기능, 설정에 대해 질문할 수 있습니다. 현재 페이지를 바탕으로 사용자의 권한 내에서 에이전트를 만들고, 작업을 예약하고, 지식을 정리할 수도 있습니다.'
      },
      done: {
        title: '준비 완료',
        desc: '핵심 기능을 모두 익혔습니다. 이제 나만의 지식 어시스턴트를 만들어 보세요! 메뉴 상단 닉네임 옆 도움말 버튼에서 언제든 가이드를 다시 볼 수 있습니다.'
      },
      models: {
        title: '모델 구성하기',
        desc: '핵심 단계입니다. 모델 관리에서 대화·임베딩 등 모델을 추가해야 지식 베이스와 대화 기능이 정상 작동합니다. \'모델 추가\'를 클릭해 시작하세요.'
      },
      settings: {
        title: '계정 및 설정 입구',
        desc: '여기를 열면 계정, 멤버, 시스템 설정을 관리할 수 있습니다. 이 가이드는 메뉴 상단 닉네임 옆 도움말 버튼에서 다시 열 수 있습니다.'
      },
      agents: {
        title: '전용 에이전트 구축',
        desc: '지식 베이스, 프롬프트, 도구를 결합해 재사용 가능한 에이전트로 전문성을 축적하세요.'
      },
      chat: {
        title: 'AI 대화 시작하기',
        desc: '지식 베이스 내용을 기반으로 질문하고 출처가 포함된 정확한 답변을 받아보세요. 여기를 클릭해 새 대화를 시작하세요.'
      },
      knowledge: {
        title: '지식 베이스 만들기',
        desc: '모든 것의 시작점입니다. 문서, 웹페이지, FAQ를 업로드하면 WeKnora가 자동으로 분석하고 색인합니다. 여기를 클릭해 지식 베이스로 이동하세요.'
      },
      welcome: {
        title: 'WeKnora에 오신 것을 환영합니다',
        desc: '몇 단계만으로 지식 베이스, 대화, 에이전트의 핵심 사용법을 안내합니다. \'다음\'을 눌러 시작하세요.'
      }
    }
  },
  menu: {
    sessionInProgress: '대화 진행 중',
    sessionUnread: '새 답변',
    knowledgeBase: '지식베이스',
    agents: '에이전트',
    automations: '자동화',
    artifacts: '산출물',
    organizations: '공유 공간',
    newChat: '새 대화',
    settings: '시스템 설정',
    logout: '로그아웃',
    clearMessages: '메시지 지우기',
    clearMessagesSuccess: '메시지가 지워졌습니다',
    clearMessagesFailed: '메시지 지우기 실패, 나중에 다시 시도해 주세요',
    renameSession: '제목 수정',
    renameSessionSuccess: '제목이 업데이트되었습니다',
    renameSessionFailed: '제목 수정 실패, 나중에 다시 시도해 주세요',
    batchManage: '일괄 관리',
    newSession: '새 세션',
    pin: '고정',
    unpin: '고정 해제',
    pinFailed: '고정 실패, 나중에 다시 시도해 주세요',
    unpinFailed: '고정 해제 실패, 나중에 다시 시도해 주세요',
    search: '검색',
    collapseSidebar: '사이드바 접기',
    expandSidebar: '사이드바 펼치기',
    logoutSuccess: '로그아웃되었습니다',
    myChats: '내 대화',
    apiChats: 'API 세션',
    noSessions: '대화가 없습니다'
  }
}
