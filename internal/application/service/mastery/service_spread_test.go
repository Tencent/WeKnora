package mastery

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// RecordPageView 是唯一把「浏览证据」与「邻居预热」写在一起的地方。这条链路
// （handler 传下来的邻居 → 预热账本）此前只有仓储层单测，缺了中间那一环：邻居
// 是否真的从服务层传到了写入、越界时长是否在折算之前就被钳制。这两点错了都不
// 会报错，只会让水位安静地偏高，所以用端到端测试钉住。
func TestRecordPageViewSpreadsClampedTimeToDedupedNeighbors(t *testing.T) {
	db := newMasteryServiceDB(t, "mastery-service-spread")
	svc := NewMasteryService(repository.NewMasteryRepository(db))
	ctx := masteryScopeCtx()

	// 传入超过单次上限的时长：钳制必须发生在折算给邻居之前，否则「一次越界上报」
	// 就能给整圈邻居灌进远超真实阅读量的预热。
	over := int64(types.MasteryMaxPageViewSeconds + 600)
	svc.RecordPageView(ctx, "kb-1", "entity/acme", over, []string{
		"entity/other",
		"entity/other", // 既是出链又是入链 → 只计一次
		"entity/acme",  // 来源页自己 → 不计
		"",             // 空 slug → 忽略
		"concept/hub",
	})

	rows := loadSpreadSeconds(t, db)
	if len(rows) != 2 {
		t.Fatalf("自己/空/重复都应被折叠，应写 2 行，got %d（%v）", len(rows), rows)
	}
	for slug, seconds := range rows {
		if seconds != int64(types.MasteryMaxPageViewSeconds) {
			t.Errorf("%s 的预热秒数应为钳制后的 %d，got %d",
				slug, types.MasteryMaxPageViewSeconds, seconds)
		}
	}

	// 同一天内再读一次：预热按秒数累加，而浏览次数仍按日折叠——两套口径不能互相
	// 带偏（预热的账走秒数，浏览的账走次数）。
	svc.RecordPageView(ctx, "kb-1", "entity/acme", 60, []string{"entity/other"})

	rows = loadSpreadSeconds(t, db)
	if got := rows["entity/other"]; got != int64(types.MasteryMaxPageViewSeconds)+60 {
		t.Errorf("同一天内预热应累加，got %d（want %d）", got, types.MasteryMaxPageViewSeconds+60)
	}
	var views []types.MemoryPageView
	if err := db.Find(&views).Error; err != nil {
		t.Fatalf("load page views: %v", err)
	}
	if len(views) != 1 || views[0].ViewCount != 1 {
		t.Errorf("浏览次数应按日折叠为 1 条 1 次，got %d 条 %+v", len(views), views)
	}
}

func newMasteryServiceDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&types.MemoryPageView{},
		&types.MemoryMasteryDaily{},
		&types.MemorySpreadView{},
		&types.MemoryGuideExposure{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// masteryScopeCtx 造一个带租户与用户的请求上下文。测试必须走 ResolveScope 的
// 同一条推导路径，而不是把 scope 直接塞进服务——服务根本没有接受 scope 的入口，
// 这正是它的隔离模型。
func masteryScopeCtx() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
}

func loadSpreadSeconds(t *testing.T, db *gorm.DB) map[string]int64 {
	t.Helper()
	var rows []types.MemorySpreadView
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load spread rows: %v", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Slug] = r.SpreadSeconds
	}
	return out
}
