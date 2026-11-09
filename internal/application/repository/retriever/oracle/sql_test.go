package oracle

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestBuildTextQuery(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"   ":            "",
		"WeKnora deploy": "{WeKnora} ACCUM {deploy}",
		"WeKnoraのデプロイ方法を教えて": "{WeKnora} ACCUM {デプロイ} ACCUM {方法} ACCUM {教}",
		"如何部署":   "{如何} ACCUM {何部} ACCUM {部署}",
		"東京":     "{東京}",
		"ひらがなだけ": "",
		// Oracle Text operators in user input stay inside braces.
		"a AND b; DROP} x": "{a} ACCUM {AND} ACCUM {b} ACCUM {DROP} ACCUM {x}",
		"dup dup":          "{dup}",
	}
	for in, want := range cases {
		assert.Equal(t, want, buildTextQuery(in), "query %q", in)
	}
	long := "x"
	for i := 0; i < 200; i++ {
		long += fmt.Sprintf(" t%d", i)
	}
	assert.Equal(t, maxQueryTerms, strings.Count(buildTextQuery(long), "{"))
}

func TestSanitizeTableBase(t *testing.T) {
	assert.Equal(t, "WEKNORA_EMBEDDINGS", sanitizeTableBase("weknora_embeddings", "x"))
	assert.Equal(t, "MY_KB", sanitizeTableBase("my-kb", "x"))
	assert.Equal(t, "DEF", sanitizeTableBase("a;DROP TABLE t", "def"))
	assert.Equal(t, "DEF", sanitizeTableBase("1abc", "def"))
	assert.Equal(t, "DEF", sanitizeTableBase(strings.Repeat("a", 101), "def"))
}

func TestWhereFilter(t *testing.T) {
	b := &binds{}
	b.add("vec") // a preceding bind shifts the numbering
	where := whereFilter(b, types.RetrieveParams{
		KnowledgeBaseIDs: []string{"kb1", "kb2"},
		TagIDs:           []string{"t1"},
		ExcludeChunkIDs:  []string{"c1"},
	})
	assert.Equal(t, "is_enabled = 1 AND (knowledge_base_id IN (:2, :3)) AND (tag_id IN (:4)) "+
		"AND (chunk_id NOT IN (:5))", where)
	assert.Equal(t, []any{"vec", "kb1", "kb2", "t1", "c1"}, b.args)

	// Lists above the Oracle IN-list limit are split.
	ids := make([]string, maxInListSize+1)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
	}
	where = whereFilter(&binds{}, types.RetrieveParams{KnowledgeIDs: ids, ExcludeKnowledgeIDs: ids})
	assert.Contains(t, where, ") OR knowledge_id IN (")
	assert.Contains(t, where, ") AND knowledge_id NOT IN (")
}

func TestBindsText(t *testing.T) {
	b := &binds{}
	assert.Equal(t, ":1", b.text("short"))

	// 3-byte runes: 4000 is not a multiple of 3, so a naive split would cut one.
	long := strings.Repeat("日", 3000) // 9000 bytes
	b = &binds{}
	expr := b.text(long)
	assert.Equal(t, "TO_CLOB(:1) || :2 || :3", expr)
	var joined string
	for _, a := range b.args {
		part := a.(string)
		assert.LessOrEqual(t, len(part), maxVarcharBind)
		assert.True(t, utf8.ValidString(part))
		joined += part
	}
	assert.Equal(t, long, joined)
}

func TestTranslateSourceID(t *testing.T) {
	assert.Equal(t, "t", translateSourceID("c", "c", "t"))
	assert.Equal(t, "t-q1", translateSourceID("c-q1", "c", "t"))
	assert.Len(t, translateSourceID("other", "c", "t"), 36)
}
