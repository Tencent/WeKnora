package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- helpers -------------------------------------------------------------

func newMiniRepo(t *testing.T) (*pgRepository, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return &pgRepository{db: &gorm.DB{}, rdb: rdb}, mr
}

func nilRdbContext() context.Context {
	return context.Background()
}

func tenantCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func sampleParams() types.RetrieveParams {
	return types.RetrieveParams{
		Query:          "悬念 伏笔 反转",
		KnowledgeType:  "novel",
		KnowledgeBaseIDs: []string{"kb-1"},
		TopK:           25,
		Threshold:      0.3,
	}
}

// ---- FO-01: rdb == nil (Lite mode) never panics ---------------------------

func TestFailOpen_NilRedis(t *testing.T) {
	repo := &pgRepository{db: nil, rdb: nil}
	ctx := tenantCtx()
	if v, ok := repo.corpusVersion(ctx); v != 0 || ok {
		t.Fatalf("corpusVersion=(%d,%v), want (0,false) in Lite mode", v, ok)
	}
	repo.bumpCorpusVersion(ctx) // must be silent no-op
	if got := repo.keywordsCacheGet(ctx, sampleParams()); got != nil {
		t.Fatalf("keywordsCacheGet with nil rdb must return nil, got %v", got)
	}
	repo.keywordsCacheSet(ctx, sampleParams(), &types.RetrieveResult{Results: []*types.IndexWithScore{{}}}) // no panic
}

// FO-02/04: Redis down (closed / error) — reads fail open, writes silent ------

func TestFailOpen_RedisClosed(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	mr.Close()
	if v, ok := repo.corpusVersion(ctx); v != 0 || ok {
		t.Fatalf("corpusVersion on closed Redis must be (0,false), got (%d,%v)", v, ok)
	}
	repo.bumpCorpusVersion(ctx) // warn-only, no panic
	if got := repo.keywordsCacheGet(ctx, sampleParams()); got != nil {
		t.Fatalf("keywordsCacheGet on closed Redis must return nil, got %v", got)
	}
}

func TestFailOpen_RedisSetError(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	mr.SetError("boom")
	if v, ok := repo.corpusVersion(ctx); v != 0 || ok {
		t.Fatalf("corpusVersion under injected error must be (0,false), got (%d,%v)", v, ok)
	}
	repo.bumpCorpusVersion(ctx)
	if got := repo.keywordsCacheGet(ctx, sampleParams()); got != nil {
		t.Fatalf("keywordsCacheGet under injected error must return nil, got %v", got)
	}
}

// FO-05: round-trip set then get ------------------------------------------------

func TestKeywordsCache_RoundTrip(t *testing.T) {
	repo, _ := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	resp := &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "c1"}, {ChunkID: "c2"}}}
	repo.keywordsCacheSet(ctx, p, resp)
	got := repo.keywordsCacheGet(ctx, p)
	if got == nil {
		t.Fatal("expected cache hit after Set")
	}
	if len(got[0].Results) != 2 || got[0].Results[0].ChunkID != "c1" {
		t.Fatalf("round-trip mismatch: %+v", got[0].Results)
	}
}

// FO-06: >1000 rows are not cached -------------------------------------------

func TestKeywordsCache_OverThousandRowsNotCached(t *testing.T) {
	repo, _ := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	big := &types.RetrieveResult{}
	for i := 0; i < 1001; i++ {
		big.Results = append(big.Results, &types.IndexWithScore{ChunkID: "c"})
	}
	repo.keywordsCacheSet(ctx, p, big)
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("1001-row result must not be cached")
	}
}

// FO-07: empty results are not served from cache ----------------------------

func TestKeywordsCache_EmptyResultsNotCached(t *testing.T) {
	repo, _ := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{})
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("empty result must be a miss (treated as uncached)")
	}
	// corrupt payload: garbage bytes
	repo.rdb.Set(ctx, repo.keywordsCacheKey(ctx, p), "{not-json", time.Hour)
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("corrupt payload must fail open to nil")
	}
}

// FO-08 covered above (corrupt JSON). FO-09: TTL ---------------------------------

func TestKeywordsCache_TTLExpiry(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "c1"}}})
	if got := repo.keywordsCacheGet(ctx, p); got == nil {
		t.Fatal("expected hit before TTL")
	}
	mr.FastForward(25 * time.Hour) // cache TTL is 24h
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("expected miss after TTL")
	}
}

// FO-10: key isolation across all hashed dimensions --------------------------

func TestKeywordsCacheKey_Isolation(t *testing.T) {
	repo, _ := newMiniRepo(t)
	ctx := tenantCtx()
	base := sampleParams()
	kb := repo.keywordsCacheKey(ctx, base)

	variants := map[string]types.RetrieveParams{
		"query":  func() types.RetrieveParams { v := base; v.Query = "其他"; return v }(),
		"ktype":  func() types.RetrieveParams { v := base; v.KnowledgeType = "poetry"; return v }(),
		"kb":     func() types.RetrieveParams { v := base; v.KnowledgeBaseIDs = []string{"kb-2"}; return v }(),
		"ki":     func() types.RetrieveParams { v := base; v.KnowledgeIDs = []string{"ki-9"}; return v }(),
		"tags":   func() types.RetrieveParams { v := base; v.TagIDs = []string{"tag-9"}; return v }(),
		"topk":   func() types.RetrieveParams { v := base; v.TopK = 100; return v }(),
		"thresh": func() types.RetrieveParams { v := base; v.Threshold = 0.9; return v }(),
	}
	for name, v := range variants {
		if repo.keywordsCacheKey(ctx, v) == kb {
			t.Fatalf("key not isolated by %s", name)
		}
	}
	// tenant isolation
	ctx2 := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2))
	if repo.keywordsCacheKey(ctx2, base) == kb {
		t.Fatal("key not isolated by tenant")
	}
}

// FO-11: ID order normalization — sorted inputs map to the same key -------------

func TestKeywordsCacheKey_NormalizesIDOrder(t *testing.T) {
	repo, _ := newMiniRepo(t)
	ctx := tenantCtx()
	a := sampleParams()
	a.KnowledgeBaseIDs = []string{"kb-1", "kb-2"}
	a.KnowledgeIDs = []string{"ki-1", "ki-2"}
	a.TagIDs = []string{"t-1", "t-2"}
	b := sampleParams()
	b.KnowledgeBaseIDs = []string{"kb-2", "kb-1"}
	b.KnowledgeIDs = []string{"ki-2", "ki-1"}
	b.TagIDs = []string{"t-2", "t-1"}
	if repo.keywordsCacheKey(ctx, a) != repo.keywordsCacheKey(ctx, b) {
		t.Fatal("ID order must not affect cache key")
	}
}

// FO-12: corpusVersion invalidation chain — bump changes the key ------------------

func TestCorpusVersion_BumpChangesKey(t *testing.T) {
	repo, _ := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	k0 := repo.keywordsCacheKey(ctx, p)
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "c1"}}})
	repo.bumpCorpusVersion(ctx)
	k1 := repo.keywordsCacheKey(ctx, p)
	if k0 == k1 {
		t.Fatal("bump must change cache key (stale entries unreachable)")
	}
	if !strings.HasSuffix(k1, "ver1") {
		t.Fatalf("key should embed ver1 after one bump: %s", k1)
	}
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("entry cached under ver0 must be a miss after bump")
	}
}

// FO-13R (post-R2-fix): during a Redis outage the corpus version is UNKNOWN,
// so both cache read and write bypass entirely — the ver0 keyspace is never
// read from or written to during an outage window. Fixed by R2 option A
// (see .workflow/issues/ISSUE-redis-degradation-stale-read.md).

func TestCorpusVersion_OutageBypassesCache(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	repo.bumpCorpusVersion(ctx) // ver=1
	before := repo.keywordsCacheKey(ctx, p)
	mr.SetError("boom")
	// (a) version reads as unknown
	if v, ok := repo.corpusVersion(ctx); v != 0 || ok {
		t.Fatalf("outage corpusVersion must be (0,false), got (%d,%v)", v, ok)
	}
	// (b) key construction signals unknown
	if _, ok := repo.keywordsCacheKeyVer(ctx, p); ok {
		t.Fatal("outage key construction must report not-ok")
	}
	// (c) reads are misses even if a ver0 entry somehow existed
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("outage read must be a miss (fall through to PG)")
	}
	// (d) writes are skipped entirely
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "outage-row"}}})
	mr.SetError("")
	for _, k := range mr.Keys() {
		if strings.HasPrefix(k, "weknora:kw:") {
			t.Fatalf("outage write must not persist any cache key, found %s", k)
		}
	}
	// recovery: keys return to the ver1 keyspace, clean round-trip
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("no outage-era residue may survive recovery")
	}
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "fresh"}}})
	if got := repo.keywordsCacheGet(ctx, p); got == nil {
		t.Fatal("recovered write must be readable")
	} else if got[0].Results[0].ChunkID != "fresh" {
		t.Fatalf("recovered round-trip corrupted: %+v", got)
	}
	if !strings.HasSuffix(repo.keywordsCacheKey(ctx, p), "ver1") {
		t.Fatalf("recovered key must embed ver1: %s", repo.keywordsCacheKey(ctx, p))
	}
	for _, k := range mr.Keys() {
		if strings.Contains(k, "ver0") {
			t.Fatalf("ver0 pollution via outage path: %s", k)
		}
	}
	_ = before // before is ver1; recovery returns to the same keyspace
}

// R2-A regression 1: redis.Nil (corpus_ver absent) is a GENUINE version 0.
// If a future refactor treats Nil as "unknown", a fresh read-only deployment
// (no write has ever bumped the counter) would silently disable the cache
// forever — this test pins the distinction.

func TestCorpusVersion_NeverBumpedIsLegitZero(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	repo.bumpCorpusVersion(ctx) // key exists...
	mrDelCorpusVer(t, mr)       // ...then is lost (flush scenario) — still redis.Nil
	v, ok := repo.corpusVersion(ctx)
	if v != 0 || !ok {
		t.Fatalf("absent corpus_ver must read as genuine (0,true), got (%d,%v)", v, ok)
	}
	p := sampleParams()
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "legit-ver0"}}})
	if got := repo.keywordsCacheGet(ctx, p); got == nil {
		t.Fatal("genuine ver0 era must cache normally")
	}
	if !strings.HasSuffix(repo.keywordsCacheKey(ctx, p), "ver0") {
		t.Fatalf("genuine-era key must embed ver0: %s", repo.keywordsCacheKey(ctx, p))
	}
}

func mrDelCorpusVer(t *testing.T, mr *miniredis.Miniredis) {
	t.Helper()
	if ok := mr.Exists("weknora:corpus_ver"); !ok {
		t.Fatal("setup: corpus_ver key should exist before del")
	}
	mr.Del("weknora:corpus_ver")
}

// R2-A regression 2: a pre-existing ver0 entry must not be readable while
// the version read is failing (outage), even though the cache GET itself
// would succeed — partial-failure window S1.

func TestKeywordsCache_OutageGetIgnoresVer0(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	// seed the legitimate-ver0 keyspace (never bumped) and prove it round-trips
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "old"}}})
	if got := repo.keywordsCacheGet(ctx, p); got == nil {
		t.Fatal("setup: ver0 era entry must be readable")
	}
	mr.SetError("boom")
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("outage must bypass reads even when ver0 entries exist")
	}
	mr.SetError("")
	if got := repo.keywordsCacheGet(ctx, p); got == nil {
		t.Fatal("recovery must restore ver0-era reads")
	}
}

// R2-A regression 3 (S1 partial-failure materialization): only the
// GET weknora:corpus_ver command fails while every other command (the
// cache SET in particular) succeeds. miniredis SetError is all-or-nothing,
// so a go-redis hook injects the per-command error. The cache write must be
// skipped — writing under an unknown version would pollute ver0.

func TestKeywordsCache_PartialFailureSkipsSet(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	repo.bumpCorpusVersion(ctx) // ver=1
	verGetFail := false
	repo.rdb.AddHook(partialFailHook{active: &verGetFail})
	verGetFail = true
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "polluting-row"}}})
	for _, k := range mr.Keys() {
		if strings.HasPrefix(k, "weknora:kw:") {
			t.Fatalf("partial failure (version GET fails, SET ok) must skip the cache write, found %s", k)
		}
	}
	verGetFail = false
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("no entry may exist from the partial-failure window")
	}
}

type partialFailHook struct {
	active *bool
}

func (h partialFailHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h partialFailHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h partialFailHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if *h.active && len(cmd.Args()) > 1 && cmd.Args()[1] == "weknora:corpus_ver" && cmd.Name() == "get" {
			return fmt.Errorf("injected partial failure on corpus_ver GET")
		}
		return next(ctx, cmd)
	}
}

// R2-A regression 4: full outage then recovery — no residue, clean ver1
// round-trip (guards the S3 cross-era reuse path end to end).

func TestKeywordsCache_RecoveryRoundTrip(t *testing.T) {
	repo, mr := newMiniRepo(t)
	ctx := tenantCtx()
	p := sampleParams()
	repo.bumpCorpusVersion(ctx) // ver=1
	// healthy write at ver1
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "pre-outage"}}})
	// full outage: nothing readable, nothing written
	mr.SetError("boom")
	if got := repo.keywordsCacheGet(ctx, p); got != nil {
		t.Fatal("outage read must miss")
	}
	repo.keywordsCacheSet(ctx, p, &types.RetrieveResult{Results: []*types.IndexWithScore{{ChunkID: "outage-row"}}})
	mr.SetError("")
	// recovery: pre-outage ver1 entry is still valid (it is not stale — no
	// write happened during the outage), and no outage-era row exists
	if got := repo.keywordsCacheGet(ctx, p); got == nil || got[0].Results[0].ChunkID != "pre-outage" {
		t.Fatalf("pre-outage ver1 entry must survive: %+v", got)
	}
	for _, k := range mr.Keys() {
		if strings.Contains(k, "ver0") {
		t.Fatalf("no ver0 keyspace may be created: %s", k)
	}
	}
}

// FO-14: key integrity guard — Exclude* params are not part of the key.
// KeywordsRetrieve does not consume them today; if it starts to, this guard
// must be updated to FAIL, forcing key coverage to be revisited.

func TestKeywordsCacheKey_ExcludeFieldsNotHashed(t *testing.T) {
	repo, _ := newMiniRepo(t)
	ctx := tenantCtx()
	base := sampleParams()
	withExcl := base
	withExcl.ExcludeKnowledgeIDs = []string{"x-1"}
	if repo.keywordsCacheKey(ctx, base) != repo.keywordsCacheKey(ctx, withExcl) {
		t.Fatal("ExcludeKnowledgeIDs currently not used by KeywordsRetrieve; key must not change (guard)")
	}
}

// JSON marshaling sanity for RetrieveResult used above (compile-path guard)
func TestRetrieveResult_Marshalable(t *testing.T) {
	if _, err := json.Marshal(&types.RetrieveResult{}); err != nil {
		t.Fatalf("RetrieveResult must marshal: %v", err)
	}
}
