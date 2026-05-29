package im

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripIMCitationTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no tags", "hello world", "hello world"},
		{"kb tag", `text <kb id="1" title="doc"/> more`, "text  more"},
		{"web tag", `see <web url="http://x.com"/> here`, "see  here"},
		{"multiple", `<kb id="1"/><web url="x"/>text`, "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripIMCitationTags(tt.in))
		})
	}
}

func TestStripImageXMLTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"with image_original",
			`<image url="local://1/img.png">
<image_original>![alt](local://1/img.png)</image_original>
<image_caption>a cat</image_caption>
</image>`,
			"![alt](local://1/img.png)",
		},
		{
			"without image_original",
			`<image url="local://1/img.png">
<image_caption>a cat</image_caption>
</image>`,
			"",
		},
		{
			"no image tags",
			"just plain text ![img](http://example.com/img.png)",
			"just plain text ![img](http://example.com/img.png)",
		},
		{
			"mixed content",
			`before <image url="x"><image_original>![a](b)</image_original></image> after`,
			"before ![a](b) after",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripImageXMLTags(tt.in))
		})
	}
}

func TestFindIncompleteStorageURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected return; -1 means no match expected
	}{
		{
			"complete URL terminated by )",
			"![img](local://1/abc/img.png)",
			// The URL `local://1/abc/img.png` ends with `)` which is a terminator,
			// but the regex [^\s)\]>"]* matches up to `)` — the `)` is NOT included.
			// So the URL portion is `local://1/abc/img.png` and `)` terminates it.
			// The match does NOT reach end of string → should return -1.
			-1,
		},
		{
			"complete URL terminated by space",
			"text local://1/abc/img.png more text",
			-1,
		},
		{
			"truncated URL at end",
			"text ![img](local://1/abc/im",
			12, // starts at `l` in `local://`
		},
		{
			"just scheme at end",
			"text minio://",
			5,
		},
		{
			"no storage URL",
			"just plain text http://example.com",
			-1,
		},
		{
			"URL at very end",
			"local://1/img.png",
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findIncompleteStorageURL(tt.in)
			assert.Equal(t, tt.want, got, "findIncompleteStorageURL(%q)", tt.in)
		})
	}
}

func TestFindIncompleteXMLTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{
			"complete tag",
			`text <kb id="1"/> more`,
			-1,
		},
		{
			"incomplete kb tag",
			`text <kb id="1`,
			5,
		},
		{
			"incomplete image tag",
			`text <image url="local://1/img`,
			5,
		},
		{
			"incomplete image_original",
			`<image_original>![alt](local://1/`,
			-1, // the tag itself is complete (has >), the content is truncated
		},
		{
			"incomplete opening image_original",
			`text <image_original`,
			5,
		},
		{
			"no XML",
			"just plain text",
			-1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findIncompleteXMLTag(tt.in)
			assert.Equal(t, tt.want, got, "findIncompleteXMLTag(%q)", tt.in)
		})
	}
}

func TestResolveIMFileServiceForPath_LocalSchemeDespiteCOSDefault(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example.com")

	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}
	svc := resolveIMFileServiceForPath(tenant, "local://10000/exports/img.png", nil)
	require.NotNil(t, svc)
	got, err := svc.GetFileURL(context.Background(), "local://10000/exports/img.png")
	require.NoError(t, err)
	assert.Contains(t, got, "/api/v1/files/presigned")
}

func TestRewriteStorageURLs_LocalUsesPresignedAPI(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example.com")

	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}

	in := "![img](local://10000/exports/abc.png)"
	out := rewriteStorageURLs(context.Background(), in, newIMFileServiceResolver(tenant, nil))
	assert.Contains(t, out, "/api/v1/files/presigned")
	assert.NotContains(t, out, "myqcloud.com")
}

func TestRewriteStorageURLs_COSPathNotSignedAsLocalKey(t *testing.T) {
	// Without real COS credentials, GetFileURL may fail; ensure we never embed
	// local:// as a COS object key when rewriting fails.
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "test-bucket",
				Region:     "ap-shanghai",
				PathPrefix: "weknora",
			},
		},
	}
	path := "cos://test-bucket/ap-shanghai/weknora/10000/exports/abc.png"
	svc := resolveIMFileServiceForPath(tenant, path, nil)
	require.NotNil(t, svc)

	in := "![img](" + path + ")"
	out := rewriteStorageURLs(context.Background(), in, newIMFileServiceResolver(tenant, nil))
	if out != in {
		assert.False(t, strings.Contains(out, "local%3A"), "COS URL must not treat local:// as object key")
	}
}

func TestFindIncompleteMarkdownImage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"complete image", "![img](local://1/a.png)", -1},
		{"complete then text", "![img](local://1/a.png) trailing", -1},
		{"truncated provider URL in image", `![知识助理"知识库"管理视图界面](minio://wizard-test/10000/exports/c91cf852`, 0},
		{"open paren only", "text ![alt](", 5},
		{"bare provider suffix without markdown", "text minio://wizard-test/10000/exp", -1},
		{"two images complete", "![a](local://1/a.png) ![b](local://1/b.png)", -1},
		{"first complete second incomplete", "![a](local://1/a.png) ![b](minio://part", 22},
		{"bracket inside alt text", "![a[b]](minio://wizard-test/10000/part", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findIncompleteMarkdownImage(tt.in)
			assert.Equal(t, tt.want, got, "findIncompleteMarkdownImage(%q)", tt.in)
		})
	}
}

func TestHoldbackCutoff(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected cutoff (len(in) means no holdback)
	}{
		{
			"no holdback needed",
			"plain text with complete ![img](local://1/img.png) content",
			-1,
		},
		{
			"truncated URL inside markdown image",
			"text ![img](local://1/abc/im",
			5, // hold back from ![ — not from local://
		},
		{
			"truncated markdown image with quotes in alt",
			"### 2\n![知识助理\"知识库\"管理视图界面](minio://wizard-test/10000/exports/c91cf852",
			-2, // computed via strings.Index below
		},
		{
			"truncated XML",
			"text <kb id=",
			5,
		},
		{
			"both truncated, XML earlier",
			"<image url=\"local://1/im",
			0,
		},
		{
			"bare truncated provider URL without markdown wrapper",
			"prefix minio://wizard-test/10000/exp",
			7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			if want == -2 {
				want = strings.Index(tt.in, "![知识助理")
				require.GreaterOrEqual(t, want, 0)
			}
			got := holdbackCutoff(tt.in)
			if want == -1 {
				assert.Equal(t, len(tt.in), got, "holdbackCutoff(%q) should be len(in)", tt.in)
			} else {
				assert.Equal(t, want, got, "holdbackCutoff(%q)", tt.in)
			}
		})
	}
}

// simulateIMStreamFlushStep mirrors one handleMessageStream flush (see flush() in service.go).
func simulateIMStreamFlushStep(holdback, incoming string, final bool) (sent, newHoldback string) {
	chunk := holdback + incoming
	holdback = ""
	if chunk == "" {
		return "", ""
	}
	if !final {
		if cut := holdbackCutoff(chunk); cut < len(chunk) {
			holdback = chunk[cut:]
			chunk = chunk[:cut]
		}
	}
	return chunk, holdback
}

// simulateIMStreamFlush mirrors handleMessageStream's flush/holdback logic (non-final
// flushes apply holdbackCutoff; final flush sends everything).
func simulateIMStreamFlush(parts []string) (sent []string, remainder string) {
	var pending string
	for i, part := range parts {
		final := i == len(parts)-1
		var chunk string
		chunk, pending = simulateIMStreamFlushStep(pending, part, final)
		if chunk != "" {
			sent = append(sent, chunk)
		}
	}
	return sent, pending
}

// orphanMarkdownImageOpenRe detects a flush chunk that ends with an opened Markdown
// image (](...) without the URL — the bug we fixed.
var orphanMarkdownImageOpenRe = regexp.MustCompile(`!\[[^\]]*\]\(\s*$`)

func TestSimulateIMStreamFlush_MiddleImageNotSplit(t *testing.T) {
	// Reproduces the user report: three images; the middle one was split at minio://.
	part1 := "### 1️⃣ 智能交互入口\n\n" +
		"![知识助理AI对话界面](minio://wizard-test/10000/exports/bb524693-a3f7-48bd-88ca-0c3957cb56e7.png)\n\n" +
		"### 2️⃣ 知识管理\n- 支持创建\n\n"
	part2 := `![知识助理"知识库"管理视图界面](minio://wizard-test/10000/exports/c91cf852`
	part3 := `-9c72-f549da25619a.png)\n\n### 3️⃣ API\n\n` +
		"![API](minio://wizard-test/10000/exports/a0423e91-95c9-46bf-ab4c-9586b43d0a61.png)\n"

	sent, remainder := simulateIMStreamFlush([]string{part1, part2, part3})
	require.Empty(t, remainder, "final flush should drain holdback")

	for i, chunk := range sent {
		assert.False(t, orphanMarkdownImageOpenRe.MatchString(chunk),
			"chunk %d must not end with orphan ![alt]( : %q", i, chunk)
	}

	joined := strings.Join(sent, "")
	assert.Contains(t, joined, "bb524693-a3f7-48bd-88ca-0c3957cb56e7.png)")
	assert.Contains(t, joined, "c91cf852-9c72-f549da25619a.png)")
	assert.Contains(t, joined, "a0423e91-95c9-46bf-ab4c-9586b43d0a61.png)")

	// Middle image must appear intact in some single sent chunk (after merge for assertion).
	middleImg := `![知识助理"知识库"管理视图界面](minio://wizard-test/10000/exports/c91cf852-9c72-f549da25619a.png)`
	assert.Contains(t, joined, middleImg)

	foundMiddleIntact := false
	for _, chunk := range sent {
		if strings.Contains(chunk, middleImg) {
			foundMiddleIntact = true
			break
		}
	}
	assert.True(t, foundMiddleIntact, "middle markdown image should ship in one chunk, got %d chunks", len(sent))
}

func TestSimulateIMStreamFlush_FirstChunkHoldsMiddleImagePrefix(t *testing.T) {
	part1 := "### 1\n![ok](minio://wizard-test/10000/exports/ok.png)\n\n### 2\n"
	part2 := `![知识助理"知识库"管理视图界面](minio://wizard-test/10000/exports/c91cf852`

	s1, hb1 := simulateIMStreamFlushStep("", part1, false)
	assert.Equal(t, part1, s1)
	assert.Empty(t, hb1)

	s2, hb2 := simulateIMStreamFlushStep(hb1, part2, false)
	assert.Empty(t, s2, "incomplete middle image must not be sent on non-final flush")
	require.NotEmpty(t, hb2)
	assert.True(t, strings.HasPrefix(hb2, "![知识助理"),
		"holdback should include full markdown image prefix, got %q", hb2)
	assert.False(t, orphanMarkdownImageOpenRe.MatchString(s1))
}

func TestSimulateIMStreamFlush_CompleteImageInOnePart(t *testing.T) {
	one := `![x](minio://wizard-test/10000/exports/uuid.png) tail`
	sent, rem := simulateIMStreamFlush([]string{one})
	require.Empty(t, rem)
	require.Len(t, sent, 1)
	assert.Equal(t, one, sent[0])
}

func TestSimulateIMStreamFlush_BareProviderURLStillHeld(t *testing.T) {
	s1, hb1 := simulateIMStreamFlushStep("", "intro ", false)
	assert.Equal(t, "intro ", s1)

	s2, hb2 := simulateIMStreamFlushStep(hb1, "minio://wizard-test/10000/exp", false)
	assert.Empty(t, s2)
	require.NotEmpty(t, hb2)
	assert.True(t, strings.HasPrefix(hb2, "minio://wizard-test/10000/exp"))
}

func TestCleanIMContent_AfterStreamReassembly(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example.com")

	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}

	parts := []string{
		`![知识助理"知识库"管理视图界面](local://10000/exports/c91cf852`,
		`-9c72-f549da25619a.png)`,
	}
	sent, rem := simulateIMStreamFlush(parts)
	require.Empty(t, rem)
	joined := strings.Join(sent, "")
	out := cleanIMContent(context.Background(), joined, tenant, nil)
	assert.Contains(t, out, "/api/v1/files/presigned")
	assert.NotContains(t, out, "local://10000")
}

func TestHoldbackCutoff_BracketInAlt(t *testing.T) {
	in := "text ![a[b]](minio://wizard-test/10000/exp"
	want := strings.Index(in, "![a[b]]")
	require.GreaterOrEqual(t, want, 0)
	assert.Equal(t, want, holdbackCutoff(in))
}

func TestSimulateIMStreamFlush_BracketInAltMiddleImage(t *testing.T) {
	part1 := "### 1\n![ok](minio://wizard-test/10000/exports/ok.png)\n\n### 2\n"
	part2 := "![a[b]](minio://wizard-test/10000/exports/c91cf852"
	part3 := "-suffix.png)\n"

	sent, rem := simulateIMStreamFlush([]string{part1, part2, part3})
	require.Empty(t, rem)
	joined := strings.Join(sent, "")
	assert.Contains(t, joined, "![a[b]](minio://wizard-test/10000/exports/c91cf852-suffix.png)")
	for _, chunk := range sent {
		assert.False(t, orphanMarkdownImageOpenRe.MatchString(chunk))
	}
}

func TestRewriteStorageURLs_MultipleImagesInOneChunk(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example.com")

	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{DefaultProvider: "cos"},
	}

	doc := "### 1\n\n![a](local://10000/exports/bb524693.png)\n\n### 2\n\n" +
		`![知识助理"知识库"管理视图界面](local://10000/exports/c91cf852.png)` + "\n\n### 3\n\n" +
		"![c](local://10000/exports/a0423e91.png)\n"

	out := rewriteStorageURLs(context.Background(), doc, newIMFileServiceResolver(tenant, nil))
	assert.NotContains(t, out, "local://")
	assert.Equal(t, 3, strings.Count(out, "/api/v1/files/presigned"))
}
