# WeKnora MCP Server 사용 예제

이 문서는 WeKnora MCP Server의 상세한 사용 예제를 제공합니다.

## 기본 사용법

### 1. 서버 시작

```bash
# 권장 방식 - 메인 진입점 사용
python main.py

# 환경 구성 확인
python main.py --check-only

# 상세 로그 활성화
python main.py --verbose
```

### 2. 환경 구성 예제

```bash
# 환경 변수 설정
export WEKNORA_BASE_URL="http://localhost:8080/api/v1"
export WEKNORA_API_KEY="your_api_key_here"

# 또는 .env 파일에서 설정
echo "WEKNORA_BASE_URL=http://localhost:8080/api/v1" > .env
echo "WEKNORA_API_KEY=your_api_key_here" >> .env
```

## MCP 도구 사용 예제

다음은 다양한 MCP 도구의 사용 예제입니다:

### 테넌트 관리

#### 테넌트 생성
```json
{
  "tool": "create_tenant",
  "arguments": {
    "name": "내 회사",
    "description": "회사 지식 관리 시스템",
    "business": "technology",
    "retriever_engines": {
      "engines": [
        {"retriever_type": "keywords", "retriever_engine_type": "postgres"},
        {"retriever_type": "vector", "retriever_engine_type": "postgres"}
      ]
    }
  }
}
```

#### 모든 테넌트 목록 조회
```json
{
  "tool": "list_tenants",
  "arguments": {}
}
```

### 지식베이스 관리

#### 지식베이스 생성
```json
{
  "tool": "create_knowledge_base",
  "arguments": {
    "name": "제품 문서 라이브러리",
    "description": "제품 관련 문서 및 자료",
    "embedding_model_id": "text-embedding-ada-002",
    "summary_model_id": "gpt-3.5-turbo"
  }
}
```

#### 지식베이스 목록 조회
```json
{
  "tool": "list_knowledge_bases",
  "arguments": {}
}
```

#### 지식베이스 상세 조회
```json
{
  "tool": "get_knowledge_base",
  "arguments": {
    "kb_id": "kb_123456"
  }
}
```

#### 하이브리드 검색
```json
{
  "tool": "hybrid_search",
  "arguments": {
    "kb_id": "kb_123456",
    "query": "API 사용 방법",
    "vector_threshold": 0.7,
    "keyword_threshold": 0.5,
    "match_count": 10
  }
}
```

### 지식 관리

#### URL에서 지식 생성
```json
{
  "tool": "create_knowledge_from_url",
  "arguments": {
    "kb_id": "kb_123456",
    "url": "https://docs.example.com/api-guide",
    "enable_multimodel": true
  }
}
```

#### 지식 목록 조회
```json
{
  "tool": "list_knowledge",
  "arguments": {
    "kb_id": "kb_123456",
    "page": 1,
    "page_size": 20
  }
}
```

#### 지식 상세 조회
```json
{
  "tool": "get_knowledge",
  "arguments": {
    "knowledge_id": "know_789012"
  }
}
```

### 모델 관리

#### 모델 생성
```json
{
  "tool": "create_model",
  "arguments": {
    "name": "GPT-4 Chat Model",
    "type": "KnowledgeQA",
    "source": "openai",
    "description": "지식 QA용 OpenAI GPT-4 모델",
    "base_url": "https://api.openai.com/v1",
    "api_key": "sk-...",
    "is_default": true
  }
}
```

#### 모델 목록 조회
```json
{
  "tool": "list_models",
  "arguments": {}
}
```

### 세션 관리

#### 채팅 세션 생성
```json
{
  "tool": "create_session",
  "arguments": {
    "kb_id": "kb_123456",
    "max_rounds": 10,
    "enable_rewrite": true,
    "fallback_response": "죄송합니다. 질문에 답변할 수 없습니다.",
    "summary_model_id": "gpt-3.5-turbo"
  }
}
```

#### 세션 상세 조회
```json
{
  "tool": "get_session",
  "arguments": {
    "session_id": "sess_345678"
  }
}
```

#### 세션 목록 조회
```json
{
  "tool": "list_sessions",
  "arguments": {
    "page": 1,
    "page_size": 10
  }
}
```

### 채팅 기능

#### 채팅 메시지 전송
```json
{
  "tool": "chat",
  "arguments": {
    "session_id": "sess_345678",
    "query": "제품의 주요 기능을 소개해 주세요"
  }
}
```

### 청크 관리

#### 지식 청크 목록 조회
```json
{
  "tool": "list_chunks",
  "arguments": {
    "knowledge_id": "know_789012",
    "page": 1,
    "page_size": 50
  }
}
```

#### 지식 청크 삭제
```json
{
  "tool": "delete_chunk",
  "arguments": {
    "knowledge_id": "know_789012",
    "chunk_id": "chunk_456789"
  }
}
```

## 전체 워크플로우 예제

### 시나리오: 완전한 지식 QA 시스템 생성

```bash
# 1. 서버 시작
python main.py --verbose

# 2. MCP 클라이언트에서 다음 단계 수행:
```

#### 1단계: 테넌트 생성
```json
{
  "tool": "create_tenant",
  "arguments": {
    "name": "기술 문서 센터",
    "description": "회사 기술 문서 지식 관리",
    "business": "technology"
  }
}
```

#### 2단계: 지식베이스 생성
```json
{
  "tool": "create_knowledge_base",
  "arguments": {
    "name": "API 문서 라이브러리",
    "description": "모든 API 관련 문서"
  }
}
```

#### 3단계: 지식 콘텐츠 추가
```json
{
  "tool": "create_knowledge_from_url",
  "arguments": {
    "kb_id": "반환된 지식베이스 ID",
    "url": "https://docs.company.com/api",
    "enable_multimodel": true
  }
}
```

#### 4단계: 채팅 세션 생성
```json
{
  "tool": "create_session",
  "arguments": {
    "kb_id": "지식베이스 ID",
    "max_rounds": 5,
    "enable_rewrite": true
  }
}
```

#### 5단계: 대화 시작
```json
{
  "tool": "chat",
  "arguments": {
    "session_id": "세션 ID",
    "query": "사용자 인증 API는 어떻게 사용하나요?"
  }
}
```

## 오류 처리 예제

### 일반적인 오류 및 해결 방법

#### 1. 연결 오류
```json
{
  "error": "Connection refused",
  "solution": "WEKNORA_BASE_URL이 올바른지 확인하고 서비스가 실행 중인지 확인하십시오"
}
```

#### 2. 인증 오류
```json
{
  "error": "Unauthorized",
  "solution": "WEKNORA_API_KEY가 올바르게 설정되었는지 확인하십시오"
}
```

#### 3. 리소스 없음
```json
{
  "error": "Knowledge base not found",
  "solution": "지식베이스 ID가 올바른지 확인하거나 먼저 지식베이스를 생성하십시오"
}
```

## 고급 구성 예제

### 사용자 정의 검색 구성
```json
{
  "tool": "hybrid_search",
  "arguments": {
    "kb_id": "kb_123456",
    "query": "검색 쿼리",
    "vector_threshold": 0.8,
    "keyword_threshold": 0.6,
    "match_count": 15
  }
}
```

### 사용자 정의 세션 전략
```json
{
  "tool": "create_session",
  "arguments": {
    "kb_id": "kb_123456",
    "max_rounds": 20,
    "enable_rewrite": true,
    "fallback_response": "기존 지식으로는 질문에 정확하게 답변할 수 없습니다. 다시 질문하거나 기술 지원에 문의하십시오."
  }
}
```

## 성능 최적화 제안

1. **일괄 작업**: 가능한 경우 지식 생성 및 업데이트를 일괄 처리하십시오
2. **캐싱 전략**: 정확성과 성능의 균형을 맞추기 위해 검색 임계값을 적절하게 설정하십시오
3. **세션 관리**: 불필요한 세션을 적시에 정리하여 리소스를 절약하십시오
4. **모니터링 로그**: `--verbose` 옵션을 사용하여 성능 지표를 모니터링하십시오

## 통합 예제

### Claude Desktop 통합
Claude Desktop의 구성 파일에 추가:
```json
{
  "mcpServers": {
    "weknora": {
      "command": "python",
      "args": ["path/to/main.py"],
      "env": {
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1",
        "WEKNORA_API_KEY": "your_api_key"
      }
    }
  }
}
```

프로젝트 저장소: https://github.com/NannaOlympicBroadcast/WeKnoraMCP

### 기타 MCP 클라이언트 통합
각 클라이언트의 문서를 참조하여 서버 시작 명령 및 환경 변수를 구성하십시오.

## 문제 해결

문제가 발생하면:
1. `python main.py --check-only`를 실행하여 환경을 확인하십시오
2. `python main.py --verbose`를 사용하여 상세 로그를 확인하십시오
3. WeKnora 서비스가 정상적으로 실행 중인지 확인하십시오
4. 네트워크 연결 및 방화벽 설정을 검증하십시오
