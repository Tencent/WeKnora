package docparser

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCSVReaderSourceRows(t *testing.T) {
	tests := []struct {
		name string
		csv  string
		rows []int
		text []string
	}{
		{
			name: "blank lines retain their sheet positions",
			csv:  "\nname,qty\n\npen,2\n\n\nink,1",
			rows: []int{2, 4, 7},
			text: []string{"| name | qty |\n| --- | --- |\n", "| pen | 2 |\n", "| ink | 1 |\n"},
		},
		{
			name: "consecutive multiline cells occupy one sheet row",
			csv:  "id,note,remark\n001,\"line1\nline2\",\"line3\nline4\"\n\n002,normal,done\n",
			rows: []int{1, 2, 4},
			text: []string{
				"| id | note | remark |\n| --- | --- | --- |\n",
				"| 001 | line1<br />line2 | line3<br />line4 |\n",
				"| 002 | normal | done |\n",
			},
		},
		{
			name: "BOM before a quoted multiline first field",
			csv:  "\xef\xbb\xbf\"head\ncontinued\",qty\n\npen,2\n",
			rows: []int{1, 3},
			text: []string{"| head<br />continued | qty |\n| --- | --- |\n", "| pen | 2 |\n"},
		},
		{
			name: "duplicate short and punctuation rows need no text alignment",
			csv:  "item,placeholder,qty\nItem,--,3\nItem,--,3\nx\n",
			rows: []int{1, 2, 3, 4},
			text: []string{
				"| item | placeholder | qty |\n| --- | --- | --- |\n", "| Item | -- | 3 |\n",
				"| Item | -- | 3 |\n", "| x |  |  |\n",
			},
		},
		{
			name: "Unicode and escaped cells use Markdown rune offsets",
			csv:  "名称,备注\n苹果🍎,\"a|b\\c\"\n香蕉,\"引号\"\"及换行\n末尾\"",
			rows: []int{1, 2, 3},
			text: []string{
				"| 名称 | 备注 |\n| --- | --- |\n", "| 苹果🍎 | a\\|b\\\\c |\n",
				"| 香蕉 | 引号\"及换行<br />末尾 |\n",
			},
		},
	}
	for _, tt := range tests {
		for _, ending := range []string{"LF", "CRLF"} {
			t.Run(tt.name+"/"+ending, func(t *testing.T) {
				input := tt.csv
				if ending == "CRLF" {
					input = strings.ReplaceAll(input, "\n", "\r\n")
				}
				result, err := (&SimpleFormatReader{}).Read(context.Background(), &types.ReadRequest{
					FileType: "csv", FileContent: []byte(input),
				})
				require.NoError(t, err)
				require.Len(t, result.SourceBlocks, len(tt.rows))
				runes := []rune(result.MarkdownContent)
				index := sourceloc.NewIndex(result.MarkdownContent, result.SourceBlocks)
				previousEnd := 0
				for i, block := range result.SourceBlocks {
					require.GreaterOrEqual(t, block.Start, previousEnd)
					require.Equal(t, tt.text[i], string(runes[block.Start:block.End]))
					require.Equal(t, types.SourceLocator{
						Type: types.SourceLocatorSheet, Mapping: "exact",
						RowStart: tt.rows[i], RowEnd: tt.rows[i],
					}, block.Locator)
					locators := index.Locators(block.Start, block.End)
					require.Len(t, locators, 1)
					require.Equal(t, tt.rows[i], locators[0].RowStart)
					require.False(t, locators[0].Partial)
					previousEnd = block.End
				}
			})
		}
	}
}

func TestCSVSourceMetadataLimit(t *testing.T) {
	result, err := (&SimpleFormatReader{}).Read(context.Background(), &types.ReadRequest{
		FileType: "csv", FileContent: []byte(strings.Repeat("x\n", maxCSVSourceBlocks+1)),
	})
	require.NoError(t, err)
	require.Len(t, result.SourceBlocks, maxCSVSourceBlocks)
	require.Equal(t, maxCSVSourceBlocks+2, strings.Count(result.MarkdownContent, "\n"))
	last := result.SourceBlocks[len(result.SourceBlocks)-1]
	require.Equal(t, maxCSVSourceBlocks, last.Locator.RowEnd)
	index := sourceloc.NewIndex(result.MarkdownContent, result.SourceBlocks)
	locators := index.Locators(last.Start, len([]rune(result.MarkdownContent)))
	require.Len(t, locators, 1)
	require.True(t, locators[0].Partial, "unmapped rows must not inherit the preceding row number")
}

func TestCSVSourceResultFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := csvToMarkdownResult(ctx, []byte("a,b\n1,2\n"), maxCSVMarkdownBytes)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
	result, err = csvToMarkdownResult(context.Background(), []byte("a,b\n1,2\n"), 1)
	require.ErrorIs(t, err, ErrCSVMarkdownTooLarge)
	require.Nil(t, result, "failed conversion must not expose partial source metadata")
	result, err = csvToMarkdownResult(context.Background(), []byte("\xef\xbb\xbf\n\n"), 0)
	require.NoError(t, err)
	require.Empty(t, result.MarkdownContent)
	require.Empty(t, result.SourceBlocks)
}
