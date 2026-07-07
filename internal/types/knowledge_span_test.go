package types

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

func TestKnowledgeProcessingSpanNameFitsWikiSummarySpan(t *testing.T) {
	parsed, err := schema.Parse(&KnowledgeProcessingSpan{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)

	nameField := parsed.LookUpField("Name")
	require.NotNil(t, nameField)

	summarySpanName := "postprocess.wiki.page[summary/00000000-0000-0000-0000-000000000000]"
	assert.GreaterOrEqual(t, nameField.Size, len(summarySpanName))
}
