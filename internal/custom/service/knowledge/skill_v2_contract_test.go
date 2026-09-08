package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const canonicalKnowledgeV2SkillSHA256 = "664124c4005b1a40653ff98222ade96a49427abc36235f27cf1725b1900fb81f"

var knowledgeV2SkillFiles = []string{
	"SKILL.md",
	"agents/openai.yaml",
	"references/audit-rules.md",
	"references/output-examples.md",
	"references/type-frameworks.md",
	"references/wiki-schema.md",
}

func TestKnowledgeV2RuntimeSkillDigestAndContract(t *testing.T) {
	runtimeDir := findSkillDir(t, filepath.Join("skills", "preloaded", "extract-video-knowledge"))
	if got := skillTreeDigest(t, runtimeDir); got != canonicalKnowledgeV2SkillSHA256 {
		t.Fatalf("V2 runtime skill changed: got sha256 %s, want %s; update the V2 source and synchronize deliberately", got, canonicalKnowledgeV2SkillSHA256)
	}

	assertSkillContains(t, runtimeDir, "SKILL.md",
		"服务端负责确定规范对象和规范 Wiki 页面",
		"Skill 只提出规范标题和别名建议",
		"一组当前视频当前代次的 `evidence_contribution`",
	)
	assertSkillContains(t, runtimeDir, "agents/openai.yaml",
		"完整读取 SKILL.md",
		"references/output-examples.md",
		"只提交候选和证据贡献",
	)
	assertSkillContains(t, runtimeDir, "references/type-frameworks.md",
		"身份判定必须比较",
		"语义等价不是传递关系",
		"工具返回的规范对象 ID、规范 Wiki 页面 ID",
	)
	assertSkillContains(t, runtimeDir, "references/wiki-schema.md",
		"一对象一规范页",
		"evidence_contribution",
		"created",
		"reused",
		"review_required",
	)
	assertSkillContains(t, runtimeDir, "references/audit-rules.md",
		"生产任务、V2 Skill、`wiki_write_page` 工具调用和源文档",
	)
	assertSkillContains(t, runtimeDir, "references/output-examples.md",
		"Codex（实体）",
		"AI Agent 第二大脑（概念）",
		"个人知识库五关标准",
		"个人知识库五大条件",
		"different_object",
	)
}

func TestKnowledgeV2RuntimeCopyMatchesSource(t *testing.T) {
	runtimeDir := findSkillDir(t, filepath.Join("skills", "preloaded", "extract-video-knowledge"))
	repoDir := filepath.Clean(filepath.Join(runtimeDir, "..", "..", ".."))
	sourceDir := filepath.Join(filepath.Dir(repoDir), "skill", "extract-video-knowledge-v2")
	if _, err := os.Stat(filepath.Join(sourceDir, "SKILL.md")); err != nil {
		t.Skip("workspace V2 source is unavailable; runtime digest test still protects the deployed copy")
	}

	for _, relativePath := range knowledgeV2SkillFiles {
		source := readSkillFile(t, sourceDir, relativePath)
		if relativePath == "SKILL.md" {
			source = bytes.Replace(source, []byte("name: extract-video-knowledge-v2"), []byte("name: extract-video-knowledge"), 1)
		}
		if relativePath == "agents/openai.yaml" {
			source = bytes.ReplaceAll(source, []byte("$extract-video-knowledge-v2"), []byte("$extract-video-knowledge"))
		}
		runtime := readSkillFile(t, runtimeDir, relativePath)
		if !bytes.Equal(source, runtime) {
			t.Fatalf("runtime file %s is not generated from the V2 source", relativePath)
		}
	}
}

func findSkillDir(t *testing.T, relativePath string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, relativePath)
		if _, err := os.Stat(filepath.Join(candidate, "SKILL.md")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	t.Fatalf("skill directory not found: %s", relativePath)
	return ""
}

func skillTreeDigest(t *testing.T, dir string) string {
	t.Helper()
	entries := make([]string, 0, len(knowledgeV2SkillFiles))
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relativePath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(relativePath))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	if strings.Join(entries, "\n") != strings.Join(knowledgeV2SkillFiles, "\n") {
		t.Fatalf("unexpected V2 runtime skill files: %v", entries)
	}

	hash := sha256.New()
	for _, relativePath := range entries {
		hash.Write([]byte(relativePath))
		hash.Write([]byte{0})
		hash.Write(readSkillFile(t, dir, relativePath))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func assertSkillContains(t *testing.T, dir, relativePath string, expected ...string) {
	t.Helper()
	content := string(readSkillFile(t, dir, relativePath))
	for _, value := range expected {
		if !strings.Contains(content, value) {
			t.Errorf("%s does not contain required V2 contract text %q", relativePath, value)
		}
	}
}

func readSkillFile(t *testing.T, dir, relativePath string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatal(err)
	}
	return content
}
