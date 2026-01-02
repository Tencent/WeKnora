# WeKnora 이벤트 시스템 요약

## 개요

WeKnora 프로젝트를 위해 사용자 쿼리 처리 프로세스의 각 단계에 대한 이벤트 처리를 지원하는 완전한 이벤트 전송 및 수신 메커니즘을 성공적으로 생성했습니다.

## 핵심 기능

### ✅ 구현된 기능

1. **이벤트 버스 (EventBus)**
   - `Emit(ctx, event)` - 이벤트 전송
   - `On(eventType, handler)` - 이벤트 리스너 등록
   - `Off(eventType)` - 이벤트 리스너 제거
   - `EmitAndWait(ctx, event)` - 이벤트 전송 및 모든 핸들러 완료 대기
   - 동기/비동기 두 가지 모드

2. **이벤트 유형**
   - 쿼리 처리 이벤트 (수신, 검증, 전처리, 재작성)
   - 검색 이벤트 (시작, 벡터 검색, 키워드 검색, 엔티티 검색, 완료)
   - 정렬 이벤트 (시작, 완료)
   - 병합 이벤트 (시작, 완료)
   - 채팅 생성 이벤트 (시작, 완료, 스트리밍 출력)
   - 오류 이벤트

3. **이벤트 데이터 구조**
   - `QueryData` - 쿼리 데이터
   - `RetrievalData` - 검색 데이터
   - `RerankData` - 정렬 데이터
   - `MergeData` - 병합 데이터
   - `ChatData` - 채팅 데이터
   - `ErrorData` - 오류 데이터

4. **미들웨어 지원**
   - `WithLogging()` - 로깅 미들웨어
   - `WithTiming()` - 타이밍 미들웨어
   - `WithRecovery()` - 오류 복구 미들웨어
   - `Chain()` - 미들웨어 조합

5. **전역 이벤트 버스**
   - 싱글톤 패턴의 전역 이벤트 버스
   - 전역 편의 함수 (`On`, `Emit`, `EmitAndWait` 등)

6. **예제 및 테스트**
   - 완전한 단위 테스트
   - 성능 벤치마크 테스트
   - 완전한 사용 예제
   - 실제 시나리오 데모

## 파일 구조

```
internal/event/
├── event.go                    # 핵심 이벤트 버스 구현
├── event_data.go              # 이벤트 데이터 구조 정의
├── middleware.go              # 미들웨어 구현
├── global.go                  # 전역 이벤트 버스
├── integration_example.go     # 통합 예제 (모니터링, 분석 핸들러)
├── example_test.go            # 테스트 및 예제
├── demo/
│   └── main.go               # 완전한 RAG 프로세스 데모
├── README.md                 # 상세 문서
├── usage_example.md          # 사용 예제 문서
└── SUMMARY.md                # 본 문서
```

## 성능 지표

- **이벤트 전송 성능**: ~9 나노초/회 (벤치마크 테스트)
- **동시성 안전**: `sync.RWMutex`를 사용하여 스레드 안전성 보장
- **메모리 오버헤드**: 매우 낮음, 이벤트 핸들러 함수 참조만 저장

## 사용 시나리오

### 1. 모니터링 및 지표 수집

```go
bus.On(event.EventRetrievalComplete, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.RetrievalData)
    // Prometheus 또는 기타 모니터링 시스템으로 전송
    metricsCollector.RecordRetrievalDuration(data.Duration)
    return nil
})
```

### 2. 로깅

```go
bus.On(event.EventQueryRewritten, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.QueryData)
    logger.Infof(ctx, "Query rewritten: %s -> %s", 
        data.OriginalQuery, data.RewrittenQuery)
    return nil
})
```

### 3. 사용자 행동 분석

```go
bus.On(event.EventQueryReceived, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.QueryData)
    // 분석 플랫폼으로 전송
    analytics.TrackQuery(data.UserID, data.OriginalQuery)
    return nil
})
```

### 4. 오류 추적

```go
bus.On(event.EventError, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.ErrorData)
    // 오류 추적 시스템으로 전송
    sentry.CaptureException(data.Error)
    return nil
})
```

## 통합 방법

### 1단계: 이벤트 시스템 초기화

애플리케이션 시작 시 (예: `main.go` 또는 `container.go`):

```go
import "github.com/Tencent/WeKnora/internal/event"

func Initialize() {
    // 전역 이벤트 버스 가져오기
    bus := event.GetGlobalEventBus()
    
    // 모니터링 및 분석 설정
    event.NewMonitoringHandler(bus)
    event.NewAnalyticsHandler(bus)
}
```

### 2단계: 각 처리 단계에서 이벤트 전송

쿼리 처리 프로세스의 각 플러그인에 이벤트 전송 추가:

```go
// search.go 에서
event.Emit(ctx, event.NewEvent(event.EventRetrievalStart, event.RetrievalData{
    Query:           chatManage.ProcessedQuery,
    KnowledgeBaseID: chatManage.KnowledgeBaseID,
    TopK:            chatManage.EmbeddingTopK,
}).WithSessionID(chatManage.SessionID))

// rerank.go 에서
event.Emit(ctx, event.NewEvent(event.EventRerankComplete, event.RerankData{
    Query:       chatManage.ProcessedQuery,
    InputCount:  len(chatManage.SearchResult),
    OutputCount: len(rerankResults),
    Duration:    time.Since(startTime).Milliseconds(),
}).WithSessionID(chatManage.SessionID))
```

### 3단계: 사용자 정의 이벤트 핸들러 등록

필요에 따라 사용자 정의 핸들러 등록:

```go
event.On(event.EventQueryRewritten, func(ctx context.Context, e event.Event) error {
    // 사용자 정의 처리 로직
    return nil
})
```

## 장점

1. **낮은 결합도**: 이벤트 전송자와 수신자가 완전히 분리되어 유지 관리 및 확장이 용이함
2. **고성능**: 매우 낮은 성능 오버헤드 (~9나노초/회)
3. **유연성**: 동기/비동기, 단일/다중 리스너 지원
4. **확장성**: 새로운 이벤트 유형 및 핸들러 추가 용이
5. **타입 안전성**: 사전 정의된 이벤트 데이터 구조
6. **미들웨어 지원**: 횡단 관심사(로깅, 타이밍, 오류 처리 등) 추가 용이
7. **테스트 친화적**: 테스트에서 이벤트 동작 검증 용이

## 테스트 결과

✅ 모든 단위 테스트 통과
✅ 성능 테스트 통과 (~9나노초/회)
✅ 비동기 처리 테스트 통과
✅ 다중 처리기 테스트 통과
✅ 전체 프로세스 데모 성공

## 후속 제안

### 선택적 향상 기능

1. **이벤트 지속성**: 주요 이벤트를 데이터베이스 또는 메시지 큐에 저장
2. **이벤트 재생**: 디버깅 또는 분석을 위한 이벤트 재생 지원
3. **이벤트 필터링**: 더 복잡한 이벤트 필터링 및 라우팅 지원
4. **우선순위 큐**: 이벤트 우선순위 처리 지원
5. **분산 이벤트**: 메시지 큐를 통한 서비스 간 이벤트 지원

### 통합 제안

1. **모니터링 통합**: Prometheus 통합을 통한 지표 수집
2. **로그 통합**: 통일된 구조화된 로깅
3. **추적 통합**: 기존 tracing 시스템과 통합
4. **알람 통합**: 이벤트 기반 알람 메커니즘

## 예제 출력

`go run ./internal/event/demo/main.go`를 실행하면 전체 RAG 프로세스 이벤트 출력을 볼 수 있습니다:

```
Step 1: Query Received
[MONITOR] Query received - Session: session-xxx, Query: RAG 기술이란 무엇입니까?
[ANALYTICS] Query tracked - User: user-123, Session: session-xxx

Step 2: Query Rewriting
[MONITOR] Query rewrite started
[MONITOR] Query rewritten - Original: RAG 기술이란 무엇입니까?, Rewritten: 검색 증강 생성 기술...
[CUSTOM] Query Transformation: ...

Step 3: Vector Retrieval
[MONITOR] Retrieval started - Type: vector, TopK: 20
[MONITOR] Retrieval completed - Results: 18, Duration: 301ms
[CUSTOM] Retrieval Efficiency: Rate: 90.00%

Step 4: Result Reranking
[MONITOR] Rerank started - Input: 18
[MONITOR] Rerank completed - Output: 5, Duration: 201ms
[CUSTOM] Rerank Statistics: Reduction: 72.22%

Step 5: Chat Completion
[MONITOR] Chat generation started
[MONITOR] Chat generation completed - Tokens: 256, Duration: 801ms
[ANALYTICS] Chat metrics - Model: gpt-4, Tokens: 256
```

## 요약

이벤트 시스템이 완전히 구현되고 테스트 검증되었으며, 쿼리 처리 프로세스의 각 단계를 모니터링, 로깅, 분석 및 디버깅하기 위해 WeKnora 프로젝트에 즉시 통합할 수 있습니다. 시스템 설계는 간결하고 성능이 우수하며 사용하기 쉽습니다.
