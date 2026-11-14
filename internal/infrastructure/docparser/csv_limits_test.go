package docparser

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCSVMarkdownExactBudget(t *testing.T) {
	for _, data := range []string{
		"name,qty,note\npen,2\nink,1,blue\n",
		"name,qty\npen,2,spare\n",
		"expr,note\na\\|b,literal\n",
		"name,address\nAnn,\"line1\r\nline2\"\n",
		"\xef\xbb\xbfname,value\n中文,\"a|b\\c\rd\"\n",
	} {
		t.Run(data, func(t *testing.T) {
			want, err := csvToMarkdown([]byte(data))
			require.NoError(t, err)
			got, err := csvToMarkdownContext(context.Background(), []byte(data), len(want))
			require.NoError(t, err)
			require.Equal(t, want, got)
			got, err = csvToMarkdownContext(context.Background(), []byte(data), len(want)-1)
			require.ErrorIs(t, err, ErrCSVMarkdownTooLarge)
			require.Empty(t, got, "never return a partial table")
		})
	}
}

func TestCSVRejectsPaddingAmplification(t *testing.T) {
	wide := strings.Repeat(",", 9999) + "x\n"
	narrow := strings.Repeat("x\n", 10000)
	for _, data := range []string{wide + narrow, narrow + wide} {
		// About 30 KB would previously generate 300 MB of Markdown.
		require.Less(t, len(data), 31000)
		result, err := (&SimpleFormatReader{}).Read(context.Background(), &types.ReadRequest{
			FileType: "csv", FileContent: []byte(data),
		})
		require.ErrorIs(t, err, ErrCSVMarkdownTooLarge)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "reduce the number of rows or columns")
	}
}

func TestCSVEscapedContentExceedsBudget(t *testing.T) {
	got, err := csvToMarkdownContext(context.Background(), []byte(strings.Repeat("|", 20)+"\n"), 16)
	require.ErrorIs(t, err, ErrCSVMarkdownTooLarge)
	require.Contains(t, err.Error(), "16 bytes")
	require.Empty(t, got)
}

func TestCSVSizeBudgetCannotOverflow(t *testing.T) {
	for _, dimensions := range [][3]int{
		{math.MaxInt, 1, 0}, {1, math.MaxInt, 0}, {1, 1, math.MaxInt},
	} {
		_, err := csvMarkdownSize(dimensions[0], dimensions[1], dimensions[2], maxCSVMarkdownBytes)
		require.ErrorIs(t, err, ErrCSVMarkdownTooLarge)
	}
}

func TestCSVConversionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (&SimpleFormatReader{}).Read(ctx, &types.ReadRequest{
		FileType: "csv", FileContent: []byte("a,b\n1,2\n"),
	})
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, errors.Is(err, ErrCSVMarkdownTooLarge))
	require.Nil(t, result)
}

func TestCSVEmptyInputAndZeroBudget(t *testing.T) {
	got, err := csvToMarkdownContext(context.Background(), []byte("\xef\xbb\xbf\n"), 0)
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = csvToMarkdownContext(context.Background(), []byte("a,b\n1,2\n"), 0)
	require.ErrorIs(t, err, ErrCSVMarkdownTooLarge)
	require.Empty(t, got)
}

func BenchmarkCSVToMarkdownRagged(b *testing.B) {
	data := []byte(strings.Repeat("x\n", 1000) + strings.Repeat(",", 999) + "x\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := csvToMarkdown(data); err != nil {
			b.Fatal(err)
		}
	}
}
