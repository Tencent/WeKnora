# 변경 로그 (Changelog)

WeKnora MCP Server 프로젝트의 모든 주요 변경 사항은 이 문서에 기록됩니다.

## [1.0.0] - 2025-10-20

### ✨ 새로운 기능 (New Features)

- **완전한 MCP 서버 구현**:
  - Model Context Protocol (MCP) 표준 완벽 지원
  - Python 기반의 독립 실행형 서버 아키텍처
  - 비동기 I/O (AsyncIO) 기반의 고성능 처리

- **WeKnora API 통합**:
  - `WeKnoraClient` 클래스를 통한 완전한 API 래핑
  - 테넌트 관리 (Tenant Management)
  - 지식베이스 관리 (Knowledge Base Management)
  - 지식 관리 (Knowledge Management)
  - 모델 관리 (Model Management)
  - 세션 관리 (Session Management)
  - 채팅 및 질의응답 (Chat & QA)
  - 청크 관리 (Chunk Management)

- **MCP 도구 (Tools) 지원**:
  - `list_tenants`, `create_tenant`
  - `list_knowledge_bases`, `create_knowledge_base`, `get_knowledge_base`, `delete_knowledge_base`
  - `list_knowledge`, `create_knowledge_from_url`, `get_knowledge`, `delete_knowledge`
  - `list_models`, `create_model`, `get_model`
  - `list_sessions`, `create_session`, `get_session`, `delete_session`
  - `list_chunks`, `delete_chunk`
  - `chat`, `hybrid_search`

- **패키징 및 배포**:
  - `setup.py` 및 `pyproject.toml` 지원
  - PyPI 배포 가능한 구조
  - Docker 컨테이너 지원 예제

- **개발자 경험 개선**:
  - 다양한 시작 스크립트 (`main.py`, `run.py`, `run_server.py`)
  - 상세한 로깅 시스템 (`--verbose` 플래그)
  - 환경 구성 검사 도구 (`--check-only` 플래그)
  - 포괄적인 문서화 (`README.md`, `INSTALL.md`, `EXAMPLES.md`)

- **테스트 시스템**:
  - `test_module.py`: 모듈 기능 자동 테스트
  - `test_imports.py`: 의존성 및 가져오기 검증

### 🐛 버그 수정 (Bug Fixes)

- 초기 릴리스이므로 알려진 버그 수정 없음

### 🛠️ 기타 변경 사항 (Others)

- 프로젝트 구조 재구성
- 라이선스 파일 추가 (MIT)
- `.gitignore` 및 `MANIFEST.in` 구성

---

## [0.1.0] - 2025-10-01 (프리뷰)

### 초기 개발

- MCP 프로토콜 기본 구현
- WeKnora API 연결 테스트
- 기본 파일 구조 설정
