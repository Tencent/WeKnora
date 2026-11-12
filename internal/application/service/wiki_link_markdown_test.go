package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWikiOutLinksRespectMarkdown(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          types.StringArray
	}{
		{"table alias", `| [[CONCEPT/Foo\|显示名称]] |`, types.StringArray{"concept/foo"}},
		{"escaped bracket", `[[concept/a\]b|名称]]`, types.StringArray{"concept/a]b"}},
		{"inline code", "`[[concept/example]]` and [[concept/real]]", types.StringArray{"concept/real"}},
		{
			"fenced code", "```json\n[[1, 2]]\n[[concept/example]]\n```\n[[concept/real]]",
			types.StringArray{"concept/real"},
		},
		{
			"nested fence", "> ~~~md\r\n> [[concept/example]]\r\n> ~~~\r\n\r\n[[concept/real]]",
			types.StringArray{"concept/real"},
		},
		{"indented code", "    [[concept/example]]\n\n[[concept/real]]", types.StringArray{"concept/real"}},
		{"numeric slug", `[[123]] and [[concept/real]]`, types.StringArray{"123", "concept/real"}},
		{"escaped opener", `\[[concept/example]] and [[concept/real]]`, types.StringArray{"concept/real"}},
		{"unclosed inline delimiter", "`[[concept/real]]", types.StringArray{"concept/real"}},
		{"deduplicate aliases", `[[Concept/Foo\|one]] [[concept/foo|two]]`, types.StringArray{"concept/foo"}},
		{"even escaped opener", `\\[[concept/real]]`, types.StringArray{"concept/real"}},
		{"fence info", "```[[concept/example]]\nexample\n```\n[[concept/real]]", types.StringArray{"concept/real"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, wikiOutLinks(markdownWikiLinks(tc.content), nil))
		})
	}
}

func TestRewriteWikiLinksLeavesCodeExamplesIntact(t *testing.T) {
	content := "`[[concept/old|Name]]`\n\n```md\n[[concept/old|Name]]\n```\n\n" +
		`| [[concept/old\|Name]] |` + "\n\n[[concept/old|Name]]"
	want := "`[[concept/old|Name]]`\n\n```md\n[[concept/old|Name]]\n```\n\n" +
		`| [[concept/new\|Name]] |` + "\n\n[[concept/new|Name]]"
	calls := 0
	got, changed := rewriteDeadWikiLinks(content, func(slug, display string) (string, bool) {
		calls++
		require.Equal(t, "concept/old", slug)
		require.Equal(t, "Name", display)
		return "concept/new", true
	})
	require.True(t, changed)
	require.Equal(t, 2, calls)
	require.Equal(t, want, got)
}

type markdownLintKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s markdownLintKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func TestWikiLintUsesCurrentMarkdownInsteadOfStaleOutLinks(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}, &types.WikiPageIssue{},
	))
	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil, nil)
	const kbID = "kb-markdown-links"
	content := "A sufficiently long explanation of this topic and its references.\n\n" +
		`| [[CONCEPT/FOO\|显示名称]] |` + "\n\n`[[concept/example]]`\n\n```json\n[[1, 2]]\n```"
	for _, page := range []*types.WikiPage{
		{
			TenantID: 1, KnowledgeBaseID: kbID, Slug: "concept/foo", Title: "Foo",
			PageType: types.WikiPageTypeConcept,
			Content:  "An existing target page with enough explanatory text to avoid an empty-page finding.",
		},
		{
			TenantID: 1, KnowledgeBaseID: kbID, Slug: "summary/main", Title: "Main",
			PageType: types.WikiPageTypeSummary, Content: content,
		},
	} {
		_, err := svc.CreatePage(ctx, page)
		require.NoError(t, err)
	}
	// Old persisted derivations must not force an unnecessary rebuild/write
	// before a read-only lint can recognize the valid current Markdown.
	require.NoError(t, db.Model(&types.WikiPage{}).Where("slug = ?", "summary/main").
		Update("out_links", types.StringArray{`concept/foo\`, "CONCEPT/FOO", "concept/example", "1,-2"}).Error)
	kb := &types.KnowledgeBase{ID: kbID}
	kb.IndexingStrategy.WikiEnabled = true
	lint := NewWikiLintService(svc, markdownLintKBService{kb: kb}, nil)
	report, err := lint.RunLint(ctx, kbID)
	require.NoError(t, err)
	for _, issue := range report.Issues {
		require.NotEqual(t, LintIssueBrokenLink, issue.Type, "%+v", issue)
		assert.NotEqual(t, LintIssueMissingCrossRef, issue.Type, "%+v", issue)
	}
	before, err := svc.GetPageBySlug(ctx, kbID, "summary/main")
	require.NoError(t, err)
	fixed, err := lint.AutoFix(ctx, kbID)
	require.NoError(t, err)
	require.Zero(t, fixed)
	after, err := svc.GetPageBySlug(ctx, kbID, "summary/main")
	require.NoError(t, err)
	require.Equal(t, content, after.Content)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt)

	// The same real dead target can occur more than once. Lint must report
	// it once, and AutoFix must remove only prose links, retaining examples.
	withDead := content + "\n\n[[concept/missing]] [[concept/missing|Broken label]]\n" +
		`| [[concept/missing\|Table label]] |` + "\n\n`[[concept/missing]]`\n\n" +
		"```md\n[[concept/missing]]\n```\n\n" + `\[[concept/missing]]`
	after.Content = withDead
	require.NoError(t, svc.UpdateAutoLinkedContent(ctx, after))
	report, err = lint.RunLint(ctx, kbID)
	require.NoError(t, err)
	broken := 0
	for _, issue := range report.Issues {
		if issue.Type == LintIssueBrokenLink {
			broken++
			assert.Equal(t, "concept/missing", issue.TargetSlug)
		}
	}
	assert.Equal(t, 1, broken)
	fixed, err = lint.AutoFix(ctx, kbID)
	require.NoError(t, err)
	assert.Equal(t, 1, fixed)
	after, err = svc.GetPageBySlug(ctx, kbID, "summary/main")
	require.NoError(t, err)
	want := content + "\n\nconcept/missing Broken label\n| Table label |\n\n" +
		"`[[concept/missing]]`\n\n```md\n[[concept/missing]]\n```\n\n" + `\[[concept/missing]]`
	assert.Equal(t, want, after.Content)
}
