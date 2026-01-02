### 새로운 벡터 데이터베이스 통합 방법

이 문서는 WeKnora 프로젝트에 새로운 벡터 데이터베이스 지원을 추가하는 전체 가이드를 제공합니다. 표준화된 인터페이스를 구현하고 구조화된 프로세스를 따름으로써, 개발자는 사용자 정의 벡터 데이터베이스를 효율적으로 통합할 수 있습니다.

### 통합 프로세스

#### 1. 기본 검색 엔진 인터페이스 구현

먼저 `interfaces` 패키지의 `RetrieveEngine` 인터페이스를 구현하여 검색 엔진의 핵심 기능을 정의해야 합니다:

```go
type RetrieveEngine interface {
    // 검색 엔진의 유형 식별자를 반환
    EngineType() types.RetrieverEngineType

    // 검색 작업을 실행하고 일치하는 결과를 반환
    Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error)

    // 이 엔진이 지원하는 검색 유형 목록을 반환
    Support() []types.RetrieverType
}
```

#### 2. 저장소 계층 인터페이스 구현

`RetrieveEngineRepository` 인터페이스를 구현하여 기본 검색 엔진 기능을 확장하고 인덱스 관리 기능을 추가합니다:

```go
type RetrieveEngineRepository interface {
    // 단일 인덱스 정보 저장
    Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error
    
    // 여러 인덱스 정보 일괄 저장
    BatchSave(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) error
    
    // 인덱스 저장에 필요한 공간 추정
    EstimateStorageSize(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) int64
    
    // 청크 ID 목록을 통해 인덱스 삭제
    DeleteByChunkIDList(ctx context.Context, indexIDList []string, dimension int) error
    
    // 인덱스 데이터를 복사하여 임베딩 벡터 재계산 방지
    CopyIndices(
        ctx context.Context,
        sourceKnowledgeBaseID string,
        sourceToTargetKBIDMap map[string]string,
        sourceToTargetChunkIDMap map[string]string,
        targetKnowledgeBaseID string,
        dimension int,
    ) error
    
    // 지식 ID 목록을 통해 인덱스 삭제
    DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int) error
    
    // RetrieveEngine 인터페이스 상속
    RetrieveEngine
}
```

#### 3. 서비스 계층 인터페이스 구현

인덱스 생성 및 관리의 비즈니스 로직을 처리하는 `RetrieveEngineService` 인터페이스를 구현하는 서비스를 생성합니다:

```go
type RetrieveEngineService interface {
    // 단일 인덱스 생성
    Index(ctx context.Context,
        embedder embedding.Embedder,
        indexInfo *types.IndexInfo,
        retrieverTypes []types.RetrieverType,
    ) error

    // 일괄 인덱스 생성
    BatchIndex(ctx context.Context,
        embedder embedding.Embedder,
        indexInfoList []*types.IndexInfo,
        retrieverTypes []types.RetrieverType,
    ) error

    // 인덱스 저장 공간 추정
    EstimateStorageSize(ctx context.Context,
        embedder embedding.Embedder,
        indexInfoList []*types.IndexInfo,
        retrieverTypes []types.RetrieverType,
    ) int64
    
    // 인덱스 데이터 복사
    CopyIndices(
        ctx context.Context,
        sourceKnowledgeBaseID string,
        sourceToTargetKBIDMap map[string]string,
        sourceToTargetChunkIDMap map[string]string,
        targetKnowledgeBaseID string,
        dimension int,
    ) error

    // 인덱스 삭제
    DeleteByChunkIDList(ctx context.Context, indexIDList []string, dimension int) error
    DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int) error

    // RetrieveEngine 인터페이스 상속
    RetrieveEngine
}
```

#### 4. 환경 변수 구성 추가

환경 구성에 새 데이터베이스의 필요한 연결 매개변수를 추가합니다:

```
# RETRIEVE_DRIVER에 새 데이터베이스 드라이버 이름 추가 (여러 드라이버는 쉼표로 구분)
RETRIEVE_DRIVER=postgres,elasticsearch_v8,your_database

# 새 데이터베이스의 연결 매개변수
YOUR_DATABASE_ADDR=your_database_host:port
YOUR_DATABASE_USERNAME=username
YOUR_DATABASE_PASSWORD=password
# 기타 필요한 연결 매개변수...
```

#### 5. 검색 엔진 등록

`internal/container/container.go` 파일의 `initRetrieveEngineRegistry` 함수에 새 데이터베이스의 초기화 및 등록 로직을 추가합니다:

```go
func initRetrieveEngineRegistry(db *gorm.DB, cfg *config.Config) (interfaces.RetrieveEngineRegistry, error) {
    registry := retriever.NewRetrieveEngineRegistry()
    retrieveDriver := strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",")
    log := logger.GetLogger(context.Background())

    // 기존 PostgreSQL 및 Elasticsearch 초기화 코드...
    
    // 새 벡터 데이터베이스 초기화 코드 추가
    if slices.Contains(retrieveDriver, "your_database") {
        // 데이터베이스 클라이언트 초기화
        client, err := your_database.NewClient(your_database.Config{
            Addresses: []string{os.Getenv("YOUR_DATABASE_ADDR")},
            Username:  os.Getenv("YOUR_DATABASE_USERNAME"),
            Password:  os.Getenv("YOUR_DATABASE_PASSWORD"),
            // 기타 연결 매개변수...
        })
        
        if err != nil {
            log.Errorf("Create your_database client failed: %v", err)
        } else {
            // 검색 엔진 리포지토리 생성
            yourDatabaseRepo := your_database.NewYourDatabaseRepository(client, cfg)
            
            // 검색 엔진 등록
            if err := registry.Register(
                retriever.NewKVHybridRetrieveEngine(
                    yourDatabaseRepo, types.YourDatabaseRetrieverEngineType,
                ),
            ); err != nil {
                log.Errorf("Register your_database retrieve engine failed: %v", err)
            } else {
                log.Infof("Register your_database retrieve engine success")
            }
        }
    }

    return registry, nil
}
```

#### 6. 검색 엔진 유형 상수 정의

`internal/types/retriever.go` 파일에 새로운 검색 엔진 유형 상수를 추가합니다:

```go
// RetrieverEngineType 검색 엔진 유형 정의
const (
    ElasticsearchRetrieverEngineType RetrieverEngineType = "elasticsearch"
    PostgresRetrieverEngineType      RetrieverEngineType = "postgres"
    YourDatabaseRetrieverEngineType  RetrieverEngineType = "your_database" // 새 데이터베이스 유형 추가
)
```

## 참고 구현 예시

기존의 PostgreSQL 및 Elasticsearch 구현을 개발 템플릿으로 참고하는 것이 좋습니다. 이러한 구현은 다음 디렉터리에 위치합니다:

- PostgreSQL: `internal/application/repository/retriever/postgres/`
- ElasticsearchV7: `internal/application/repository/retriever/elasticsearch/v7/`
- ElasticsearchV8: `internal/application/repository/retriever/elasticsearch/v8/`

위의 단계를 따르고 기존 구현을 참고하면, 새로운 벡터 데이터베이스를 WeKnora 시스템에 성공적으로 통합하여 벡터 검색 기능을 확장할 수 있습니다.
