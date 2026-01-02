# WeKnora MCP Server

이것은 WeKnora 지식 관리 API에 대한 액세스를 제공하는 Model Context Protocol (MCP) 서버입니다.

## 빠른 시작

> [MCP 구성 가이드](./MCP_CONFIG.md)를 직접 참조하는 것이 좋습니다. 다음 작업을 수행할 필요가 없습니다.

### 1. 의존성 설치
```bash
pip install -r requirements.txt
```

### 2. 환경 변수 구성
```bash
# Linux/macOS
export WEKNORA_BASE_URL="http://localhost:8080/api/v1"
export WEKNORA_API_KEY="your_api_key_here"

# Windows PowerShell
$env:WEKNORA_BASE_URL="http://localhost:8080/api/v1"
$env:WEKNORA_API_KEY="your_api_key_here"

# Windows CMD
set WEKNORA_BASE_URL=http://localhost:8080/api/v1
set WEKNORA_API_KEY=your_api_key_here
```

### 3. 서버 실행

**권장 방법 - 메인 진입점 사용:**
```bash
python main.py
```

**기타 실행 방법:**
```bash
# 원본 시작 스크립트 사용
python run_server.py

# 간편 스크립트 사용
python run.py

# 서버 모듈 직접 실행
python weknora_mcp_server.py

# Python 모듈로 실행
python -m weknora_mcp_server
```

### 4. 명령줄 옵션
```bash
python main.py --help                 # 도움말 표시
python main.py --check-only           # 환경 구성만 확인
python main.py --verbose              # 상세 로그 활성화
python main.py --version              # 버전 정보 표시
```

## Python 패키지로 설치

### 개발 모드 설치
```bash
pip install -e .
```

설치 후 명령줄 도구 사용 가능:
```bash
weknora-mcp-server
# 또는
weknora-server
```

### 프로덕션 모드 설치
```bash
pip install .
```

### 배포 패키지 빌드
```bash
# setuptools 사용
python setup.py sdist bdist_wheel

# 최신 빌드 도구 사용
pip install build
python -m build
```

## 모듈 테스트

테스트 스크립트를 실행하여 모듈이 정상적으로 작동하는지 확인:
```bash
python test_module.py
```

## 기능

이 MCP 서버는 다음과 같은 도구를 제공합니다:

### 테넌트 관리
- `create_tenant` - 새 테넌트 생성
- `list_tenants` - 모든 테넌트 목록 조회

### 지식베이스 관리
- `create_knowledge_base` - 지식베이스 생성
- `list_knowledge_bases` - 지식베이스 목록 조회
- `get_knowledge_base` - 지식베이스 상세 조회
- `delete_knowledge_base` - 지식베이스 삭제
- `hybrid_search` - 하이브리드 검색

### 지식 관리
- `create_knowledge_from_url` - URL에서 지식 생성
- `list_knowledge` - 지식 목록 조회
- `get_knowledge` - 지식 상세 조회
- `delete_knowledge` - 지식 삭제

### 모델 관리
- `create_model` - 모델 생성
- `list_models` - 모델 목록 조회
- `get_model` - 모델 상세 조회

### 세션 관리
- `create_session` - 채팅 세션 생성
- `get_session` - 세션 상세 조회
- `list_sessions` - 세션 목록 조회
- `delete_session` - 세션 삭제

### 채팅 기능
- `chat` - 채팅 메시지 전송

### 청크 관리
- `list_chunks` - 지식 청크 목록 조회
- `delete_chunk` - 지식 청크 삭제

## 문제 해결

가져오기 오류가 발생하는 경우 다음을 확인하십시오:
1. 모든 필수 의존성 패키지가 설치되어 있는지 확인
2. Python 버전 호환성 확인 (3.10+ 권장)
3. 파일 이름 충돌 확인 (`mcp.py`를 파일 이름으로 사용하지 마십시오)

## 호출 효과

<img width="950" height="2063" alt="118d078426f42f3d4983c13386085d7f" src="https://github.com/user-attachments/assets/09111ec8-0489-415c-969d-aa3835778e14" />
