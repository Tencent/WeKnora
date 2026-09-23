package mastery

import (
	"testing"
	"time"
)

func TestLevelNoEvidenceIsZero(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	if got := Level(cfg, Evidence{}, now); got != 0 {
		t.Fatalf("no evidence should be 0, got %d", got)
	}
}

func TestLevelCitationOnlyCapsLow(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	// Pure citation, even heavily repeated, must not reach familiar/mastered.
	e := Evidence{Citations: 100, LastCitedAt: now}
	got := Level(cfg, e, now)
	if got >= 40 {
		t.Fatalf("citation-only should stay below familiar, got %d", got)
	}
	if got < 10 {
		t.Fatalf("citation should at least reach contact, got %d", got)
	}
}

func TestLevelViewReachesFamiliar(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	// Two views with some duration should reach familiar (40~60).
	e := Evidence{Views: 2, Duration: 60, LastViewedAt: now}
	got := Level(cfg, e, now)
	if got < 40 || got > 60 {
		t.Fatalf("two views should reach familiar, got %d", got)
	}
}

func TestLevelDiverseEvidenceReachesMastered(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	// Citation + view + like together, recent, should reach mastered (70~90).
	e := Evidence{
		Citations: 1, LastCitedAt: now,
		Views: 2, Duration: 60, LastViewedAt: now,
		Likes: 2, LastLikedAt: now,
	}
	got := Level(cfg, e, now)
	if got < 70 || got > 90 {
		t.Fatalf("diverse recent evidence should reach mastered, got %d", got)
	}
}

func TestLevelDecayDropsWithTime(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	ago := now.Add(-200 * 24 * time.Hour)
	fresh := Evidence{
		Citations: 1, LastCitedAt: now,
		Views: 2, Duration: 60, LastViewedAt: now,
		Likes: 2, LastLikedAt: now,
	}
	stale := Evidence{
		Citations: 1, LastCitedAt: ago,
		Views: 2, Duration: 60, LastViewedAt: ago,
		Likes: 2, LastLikedAt: ago,
	}
	if staleLevel, freshLevel := Level(cfg, stale, now), Level(cfg, fresh, now); staleLevel >= freshLevel {
		t.Fatalf("stale evidence should decay below fresh: stale=%d fresh=%d", staleLevel, freshLevel)
	}
}

// 分信号衰减：陈旧的引用不能被「今天的一次浏览」整体保鲜。
// 在旧的「整分衰减」下，两种情形的水位会完全相同——那正是要避免的高估。
func TestLevelDecaysSignalsIndependently(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	monthAgo := now.Add(-30 * 24 * time.Hour)

	staleCite := Evidence{
		Citations: 3, LastCitedAt: monthAgo,
		Views: 1, Duration: 5, LastViewedAt: now,
	}
	freshCite := Evidence{
		Citations: 3, LastCitedAt: now,
		Views: 1, Duration: 5, LastViewedAt: now,
	}
	staleLevel, freshLevel := Level(cfg, staleCite, now), Level(cfg, freshCite, now)
	if staleLevel >= freshLevel {
		t.Fatalf("一个月前的引用必须独自衰减：stale=%d fresh=%d", staleLevel, freshLevel)
	}
}

// 饱和是单向的：证据累积到顶之后不再随时间回落。
func TestLevelSaturationDoesNotDecay(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	longAgo := now.Add(-2000 * 24 * time.Hour)
	e := Evidence{
		Citations: 3, LastCitedAt: longAgo,
		Views: 5, Duration: 300, LastViewedAt: longAgo,
		Likes: 4, LastLikedAt: longAgo,
	}
	// raw = 3 + (5×2 + 300/60) + 4×2 = 26 ≥ SatScore(12)
	if got := Level(cfg, e, now); got != 100 {
		t.Fatalf("已饱和的节点不应回落，got %d", got)
	}
}

// 衰减有下限：久未接触的节点回落到「沉寂」，但不退回「从未接触」。
func TestLevelDecayHasFloor(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	longAgo := now.Add(-2000 * 24 * time.Hour) // 约 5.5 年
	e := Evidence{
		Citations: 3, LastCitedAt: longAgo,
		Views: 2, Duration: 60, LastViewedAt: longAgo,
	}
	// raw = 3 + (2×2 + 1) = 8 < SatScore，因此会走衰减分支。
	// 没有 DecayFloor 时衰减到 0 → 只剩 10%（等同「从未接触」）。
	if got := Level(cfg, e, now); got < 20 {
		t.Fatalf("久未接触应保留学习痕迹（不低于 20），got %d", got)
	}
}

// view 证据按天切片衰减：今天的一次轻微浏览不得把一个月前积累的阅读整体保鲜。
//
// 这是分信号衰减之后剩下的那个洞——跨信号（引用被浏览保鲜）修好了，但 view
// 信号内部仍是「汇总值 + 单一时间戳」：汇总口径下，今天那次 5 秒浏览会把
// LastViewedAt 刷成今天，于是 30 天前那 300 秒也按「刚发生」计入。
//
// 这里刻意不含引用证据，以免触发饱和短路（见下一个测试）——那样两种口径都会
// 返回 100，就看不出差别了。
func TestLevelViewSlicesDecayPerDay(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	d30 := now.Add(-30 * 24 * time.Hour)

	sliced := Evidence{
		Views: 2, Duration: 305, LastViewedAt: now,
		ViewSlices: []ViewSlice{
			{Day: d30, Views: 1, Duration: 300},
			{Day: now, Views: 1, Duration: 5},
		},
	}
	lumped := sliced
	lumped.ViewSlices = nil

	lumpedLevel, slicedLevel := Level(cfg, lumped, now), Level(cfg, sliced, now)
	if lumpedLevel < 70 {
		t.Fatalf("前提变了：汇总口径下这次浏览本应把节点推上掌握档，got %d", lumpedLevel)
	}
	if slicedLevel > 50 {
		t.Fatalf("按天切片后 30 天前的阅读必须自己衰减，got %d", slicedLevel)
	}
	if lumpedLevel-slicedLevel < 30 {
		t.Fatalf("两种口径的差距应显著（≥30 档），got lumped=%d sliced=%d", lumpedLevel, slicedLevel)
	}
}

// 饱和短路用的是未衰减总量，因此已饱和的节点不受切片口径影响：
// 「某段时间里真的读够了」是一次性事实，不随时间流逝或证据切法而改变。
func TestLevelSaturationShortCircuitsSlices(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	longAgo := now.Add(-200 * 24 * time.Hour)
	e := Evidence{
		Views: 3, Duration: 900, LastViewedAt: longAgo,
		ViewSlices: []ViewSlice{
			{Day: longAgo, Views: 1, Duration: 300},
			{Day: longAgo, Views: 2, Duration: 600},
		},
	}
	// raw = 3×2 + 900/60 = 21 ≥ SatScore(12)
	if got := Level(cfg, e, now); got != 100 {
		t.Fatalf("已饱和节点应保持 100，got %d", got)
	}
}

// 连续性：只有今天的活动时，切片口径与汇总口径必须给出同一个水位，
// 否则上线切片会在同一天内造成水位跳变。
func TestLevelViewSlicesMatchLumpForTodayOnly(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	sliced := Evidence{
		Views: 2, Duration: 600, LastViewedAt: now,
		ViewSlices: []ViewSlice{{Day: now, Views: 2, Duration: 600}},
	}
	lumped := sliced
	lumped.ViewSlices = nil
	if a, b := Level(cfg, sliced, now), Level(cfg, lumped, now); a != b {
		t.Fatalf("纯今天活动下两种口径必须一致：sliced=%d lumped=%d", a, b)
	}
}

// 没有日桶时回落到汇总口径（历史数据 / 日桶从未写入的库），水位不能归零。
func TestLevelViewSlicesFallBackToLump(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	e := Evidence{Views: 1, Duration: 300, LastViewedAt: now.Add(-30 * 24 * time.Hour)}
	if got := Level(cfg, e, now); got < 10 || got > 30 {
		t.Fatalf("无切片时应按汇总行衰减（30 天前 5 分钟 → 接触档），got %d", got)
	}
}

// 冷桶折叠的等价性：地平线之外每一天的衰减因子都已经等于 DecayFloor，
// 因此「把多天求和成一条伪切片」与「逐日计算」必须得到同一个水位。
func TestLevelColdFoldIsEquivalentToPerDaySlices(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	horizon := cfg.ColdHorizon()
	if horizon <= 0 {
		t.Fatalf("默认配置应有衰减地平线，got %v", horizon)
	}

	const days = 3
	perDay := Evidence{}
	for i := 0; i < days; i++ {
		perDay.ViewSlices = append(perDay.ViewSlices, ViewSlice{
			Day:      now.Add(-horizon - time.Duration(i+1)*24*time.Hour),
			Views:    1,
			Duration: 30,
		})
	}
	folded := Evidence{ViewSlices: []ViewSlice{
		{Day: now.Add(-horizon), Views: days, Duration: 30 * days},
	}}

	perDayLevel, foldedLevel := Level(cfg, perDay, now), Level(cfg, folded, now)
	if perDayLevel != foldedLevel {
		t.Fatalf("折叠必须与逐日等价：perDay=%d folded=%d", perDayLevel, foldedLevel)
	}
	if perDayLevel >= 100 {
		t.Fatalf("用例应停在未饱和区间，否则等价性不经过衰减分支：got %d", perDayLevel)
	}
}

// 地平线必须落在「衰减已触底」之后：地平线当天及更早的因子等于下限，
// 而地平线之内必须仍高于下限，否则折叠会把本该有差异的衰减拍平。
func TestLevelColdHorizonIsPastTheFloor(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	horizon := cfg.ColdHorizon()
	if horizon <= 0 {
		t.Fatalf("默认配置应有衰减地平线，got %v", horizon)
	}
	if got := decayFactor(cfg, now, now.Add(-horizon)); got != cfg.DecayFloor {
		t.Fatalf("地平线当天的因子应等于下限 %.2f，got %.4f", cfg.DecayFloor, got)
	}
	if got := decayFactor(cfg, now, now.Add(-horizon+2*24*time.Hour)); got <= cfg.DecayFloor {
		t.Fatalf("地平线之内应仍有高于下限的因子，got %.4f", got)
	}
}

// 时间戳缺失（脏数据 / 早期行）不得让证据永久保鲜：按最古老处理，但仍保留
// 下限痕迹，而不是被打回「从未接触」。
func TestLevelZeroTimestampDecaysToFloor(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	fresh := Evidence{Views: 1, Duration: 300, LastViewedAt: now}
	unknown := Evidence{Views: 1, Duration: 300} // 没有时间戳

	freshLevel, unknownLevel := Level(cfg, fresh, now), Level(cfg, unknown, now)
	if unknownLevel >= freshLevel {
		t.Fatalf("缺失时间戳不应被当作最新证据：unknown=%d fresh=%d", unknownLevel, freshLevel)
	}
	if unknownLevel < 10 {
		t.Fatalf("缺失时间戳仍应保留下限痕迹（DecayFloor），got %d", unknownLevel)
	}
}

// 桶在、汇总行缺失时仍要认得出证据，否则会静默算成「从未接触」。
func TestLevelViewSlicesAloneCountAsEvidence(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	e := Evidence{ViewSlices: []ViewSlice{{Day: now, Views: 1, Duration: 300}}}
	if got := Level(cfg, e, now); got < 40 {
		t.Fatalf("只有日桶时也必须算出水位，got %d", got)
	}
}

// 只有邻居预热的节点：能被带动，但永远到不了「饱和」——100 是留给自己证据的。
func TestLevelSpreadOnlyWarmsWithoutSaturating(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()

	modest := Evidence{SpreadSlices: []SpreadSlice{{Day: now, Seconds: 900}}}
	if got := Level(cfg, modest, now); got != 20 {
		t.Fatalf("900 秒预热 = 3 分，应落在接触档中段 20%%，got %d", got)
	}

	huge := Evidence{SpreadSlices: []SpreadSlice{{Day: now, Seconds: 100000}}}
	got := Level(cfg, huge, now)
	if got >= 70 {
		t.Fatalf("预热再多也进不了掌握档（SpreadCap 封在熟悉档），got %d", got)
	}
	if got != 50 {
		t.Fatalf("封顶后应落在 50%%（SpreadCap = 6），got %d", got)
	}
}

// 自己的证据不足以饱和时，预热再热也停在 90；自己的证据够了就是 100。
func TestLevelSpreadCannotCompleteSaturation(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()

	// 自己 raw = 2 + 540/60 = 11 < SatScore(12)
	own := Evidence{Views: 1, Duration: 540, LastViewedAt: now}
	warm := Evidence{
		Views: 1, Duration: 540, LastViewedAt: now,
		SpreadSlices: []SpreadSlice{{Day: now, Seconds: 100000}},
	}
	if got := Level(cfg, own, now); got >= 100 {
		t.Fatalf("前提：自己 11 分不应饱和，got %d", got)
	}
	if got := Level(cfg, warm, now); got != 90 {
		t.Fatalf("预热不应把节点补成饱和，期望 90，got %d", got)
	}

	// 自己 raw = 2 + 600/60 = 12 → 单向饱和，与预热无关。
	enough := Evidence{
		Views: 1, Duration: 600, LastViewedAt: now,
		SpreadSlices: []SpreadSlice{{Day: now, Seconds: 100000}},
	}
	if got := Level(cfg, enough, now); got != 100 {
		t.Fatalf("自己的证据足够时应为 100，got %d", got)
	}
}

// 只算阅读时间：扫一眼（几秒）的 20% 在分数上约等于零。它确实会让邻居从 0% 变成
// 10%（「有证据就有最低档」是档位映射的地板，与权重无关），但不会再往上推。
func TestLevelGlanceSpreadIsNegligible(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	glance := Evidence{SpreadSlices: []SpreadSlice{{Day: now, Seconds: 5}}}
	if got := Level(cfg, glance, now); got != 10 {
		t.Fatalf("5 秒阅读的 20%% 只应停在最低档 10%%，got %d", got)
	}
}

// 量级：邻居当天被认真读 N 次（每次 5 分钟，单次上限 300 秒），该节点被带到哪一档。
// 每次贡献 = 300 × DurationWeight(1/60) × SpreadFactor(0.2) = 1.0 分，SpreadCap 6 分（50%）封顶。
func TestLevelSpreadMagnitudeByNeighbourReads(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	cases := []struct {
		reads int
		want  int
	}{
		{1, 10},
		{2, 20},
		{3, 20},
		{4, 40},
		{6, 50},  // 1800 秒 → 6 分，正好触到 SpreadCap
		{10, 50}, // 再多也封顶
		{40, 50},
	}
	for _, c := range cases {
		e := Evidence{SpreadSlices: []SpreadSlice{{Day: now, Seconds: int64(c.reads) * 300}}}
		if got := Level(cfg, e, now); got != c.want {
			t.Errorf("%d 次邻居 5 分钟阅读：want %d, got %d", c.reads, c.want, got)
		}
	}
}

// 预热同样按天切片衰减，且冷端折叠与逐日等价（折叠依赖 DecayFloor）。
func TestLevelSpreadDecaysAndColdFolds(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	horizon := cfg.ColdHorizon()
	if horizon <= 0 {
		t.Fatalf("默认配置应有衰减地平线，got %v", horizon)
	}

	fresh := Evidence{SpreadSlices: []SpreadSlice{{Day: now, Seconds: 3600}}}
	stale := Evidence{SpreadSlices: []SpreadSlice{{Day: now.Add(-90 * 24 * time.Hour), Seconds: 3600}}}
	freshLevel, staleLevel := Level(cfg, fresh, now), Level(cfg, stale, now)
	if staleLevel >= freshLevel {
		t.Fatalf("超过地平线的预热应衰减到更低：stale=%d fresh=%d", staleLevel, freshLevel)
	}

	perDay := Evidence{}
	for i := 0; i < 3; i++ {
		perDay.SpreadSlices = append(perDay.SpreadSlices,
			SpreadSlice{Day: now.Add(-horizon - time.Duration(i+1)*24*time.Hour), Seconds: 1200})
	}
	folded := Evidence{SpreadSlices: []SpreadSlice{{Day: now.Add(-horizon), Seconds: 3600}}}
	if a, b := Level(cfg, perDay, now), Level(cfg, folded, now); a != b {
		t.Fatalf("冷端折叠必须与逐日等价：perDay=%d folded=%d", a, b)
	}
}

func TestLevelTiersAreTenMultiples(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Now()
	inputs := []Evidence{
		{Citations: 1, LastCitedAt: now},
		{Views: 1, LastViewedAt: now},
		{Views: 2, LastViewedAt: now},
		{Citations: 3, Views: 4, Duration: 300, Likes: 3,
			LastCitedAt: now, LastViewedAt: now, LastLikedAt: now},
	}
	for _, e := range inputs {
		got := Level(cfg, e, now)
		if got%10 != 0 {
			t.Fatalf("level must be a ten multiple, got %d for %+v", got, e)
		}
	}
}
