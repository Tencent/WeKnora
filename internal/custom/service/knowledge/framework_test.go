package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const canonicalFrameworkSHA256 = "02c226b9541a0cecfe70106085a6da64fadb99fdec3083b77ee1d4e2ef5e7f5a"

func TestFrameworkDigestContainsFiveTypesAndSixEntitySubtypes(t *testing.T) {
	digest, err := LoadDefaultFrameworkDigest()
	if err != nil {
		t.Fatalf("load framework digest: %v", err)
	}
	if len(digest.Types) != 5 {
		t.Fatalf("primary type count = %d, want 5", len(digest.Types))
	}
	if len(digest.Entries) != 10 {
		t.Fatalf("framework entry count = %d, want 10 (six entity subtypes plus four knowledge types)", len(digest.Entries))
	}

	wantTypes := []KnowledgeType{TypeEntity, TypeMethodology, TypeCase, TypeConcept, TypeInsight}
	for index, want := range wantTypes {
		if digest.Types[index].PrimaryType != want || strings.TrimSpace(digest.Types[index].Label) == "" {
			t.Fatalf("type[%d] = %#v, want %q with Chinese label", index, digest.Types[index], want)
		}
	}

	entitySubtypes := make(map[string]bool)
	for _, entry := range digest.Entries {
		if entry.PrimaryType == TypeEntity {
			entitySubtypes[entry.EntitySubType] = true
		}
	}
	if len(entitySubtypes) != 6 {
		t.Fatalf("entity subtype count = %d, want 6", len(entitySubtypes))
	}
}

func TestFrameworkDigestUsesCanonicalSourceHash(t *testing.T) {
	digest, err := LoadDefaultFrameworkDigest()
	if err != nil {
		t.Fatalf("load framework digest: %v", err)
	}
	if digest.SourceSHA256 != canonicalFrameworkSHA256 {
		t.Fatalf("type-frameworks.md changed: got sha256 %s, want %s; review and update the contract deliberately", digest.SourceSHA256, canonicalFrameworkSHA256)
	}
}

func TestBundledFrameworkCopyMatchesCanonicalSource(t *testing.T) {
	canonical, err := LoadDefaultFrameworkDigest()
	if err != nil {
		t.Fatalf("load canonical framework digest: %v", err)
	}
	bundledPath, err := resolveBundledFrameworkPath()
	if err != nil {
		t.Fatalf("resolve bundled framework path: %v", err)
	}
	bundled, err := LoadFrameworkDigest(bundledPath)
	if err != nil {
		t.Fatalf("load bundled framework digest: %v", err)
	}
	if canonical.SourceSHA256 != bundled.SourceSHA256 {
		t.Fatalf("bundled framework %q is stale: got sha256 %s, want %s", filepath.Clean(bundledPath), bundled.SourceSHA256, canonical.SourceSHA256)
	}
}

func TestFrameworkFieldsAndLabelsFollowSourceOrder(t *testing.T) {
	tests := []struct {
		primaryType   KnowledgeType
		entitySubType string
		keys          []string
		labels        []string
	}{
		{TypeEntity, "person", []string{"identity", "background", "expertise", "standpoint"}, []string{"职业身份", "教育背景与经历", "擅长领域", "代表性观点"}},
		{TypeEntity, "organization", []string{"org_type", "industry", "stage", "core_business", "key_people"}, []string{"机构类型", "所在行业", "发展阶段", "核心业务", "关键人物"}},
		{TypeEntity, "product", []string{"product_type", "target_users", "core_function", "tech_basis", "differentiation"}, []string{"产品类别", "目标用户", "核心功能", "技术基础", "差异化特点"}},
		{TypeEntity, "technology", []string{"tech_category", "application_area", "maturity"}, []string{"技术分类", "应用领域", "发展阶段"}},
		{TypeEntity, "industry", []string{"scope", "stage", "key_trends"}, []string{"行业范围", "发展阶段", "关键趋势"}},
		{TypeEntity, "place", []string{"place_type", "associated_activity"}, []string{"地点类型", "关联活动"}},
		{TypeMethodology, "", []string{"input", "steps", "criteria", "output", "applicability"}, []string{"输入", "步骤", "判断标准", "输出", "适用条件"}},
		{TypeCase, "", []string{"context", "actors", "choices", "actions", "outcome", "retrospective"}, []string{"背景", "参与对象", "选择", "行动", "结果", "复盘判断"}},
		{TypeConcept, "", []string{"definition", "components", "mechanism", "distinction"}, []string{"定义", "构成要素", "运行机制", "相邻区别"}},
		{TypeInsight, "", []string{"claim", "reasoning", "qualifications", "implications"}, []string{"核心判断", "推导依据", "限定条件", "影响建议"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.primaryType)+"/"+tt.entitySubType, func(t *testing.T) {
			entry, err := FrameworkFor(tt.primaryType, tt.entitySubType)
			if err != nil {
				t.Fatalf("framework lookup: %v", err)
			}
			if len(entry.Fields) != len(tt.keys) {
				t.Fatalf("field count = %d, want %d", len(entry.Fields), len(tt.keys))
			}
			for index, field := range entry.Fields {
				if field.Key != tt.keys[index] || field.Label != tt.labels[index] {
					t.Fatalf("field[%d] = %#v, want key=%q label=%q", index, field, tt.keys[index], tt.labels[index])
				}
			}
		})
	}
}

func TestFrameworkParserDetectsChangedSourceBytes(t *testing.T) {
	path, err := resolveFrameworkPath()
	if err != nil {
		t.Fatalf("resolve framework path: %v", err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read framework source: %v", err)
	}
	mutated := strings.Replace(string(source), "核心判断", "核心判断（已变更）", 1)
	if mutated == string(source) {
		t.Fatal("fixture mutation did not change the source")
	}
	digest, err := parseFrameworkSource([]byte(mutated), path)
	if err != nil {
		t.Fatalf("mutated framework should remain structurally parseable: %v", err)
	}
	if digest.SourceSHA256 == canonicalFrameworkSHA256 {
		t.Fatal("source hash did not change after framework rule mutation")
	}
}
