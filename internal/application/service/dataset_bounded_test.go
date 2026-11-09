package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/require"
)

type boundedDatasetFixture struct {
	queries, corpus, answers []TextInfo
	qrels                    []RelsInfo
	qas                      []QaInfo
}

func validBoundedFixture() boundedDatasetFixture {
	return boundedDatasetFixture{
		queries: []TextInfo{{2, "question two"}, {0, "question zero"}},
		corpus:  []TextInfo{{4, "passage four"}, {1, "passage one"}, {2, "passage two"}},
		answers: []TextInfo{{2, "answer two"}, {0, "answer zero"}},
		qrels:   []RelsInfo{{2, 4}, {2, 1}, {0, 2}}, qas: []QaInfo{{2, 2}, {0, 0}},
	}
}
func writeBoundedFixture(t *testing.T, fixture boundedDatasetFixture) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "queries.parquet"), fixture.queries))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "corpus.parquet"), fixture.corpus))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "answers.parquet"), fixture.answers))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "qrels.parquet"), fixture.qrels))
	require.NoError(t, parquet.WriteFile(filepath.Join(dir, "qas.parquet"), fixture.qas))
	return dir
}
func reverseBoundedRows[T any](rows []T) {
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
}

func TestBoundedDatasetCanonicalAliasesAndRowOrder(t *testing.T) {
	a := validBoundedFixture()
	b := validBoundedFixture()
	reverseBoundedRows(b.queries)
	reverseBoundedRows(b.corpus)
	reverseBoundedRows(b.answers)
	reverseBoundedRows(b.qrels)
	reverseBoundedRows(b.qas)
	sa := &DatasetService{sampleDir: writeBoundedFixture(t, a)}
	sb := &DatasetService{sampleDir: writeBoundedFixture(t, b)}
	first, err := sa.GetDatasetByID(context.Background(), "default")
	require.NoError(t, err)
	second, err := sb.GetDatasetByID(context.Background(), "sample")
	require.NoError(t, err)
	require.Equal(t, 0, first[0].QID)
	require.Equal(t, 2, first[1].QID)
	require.Equal(t, []int{1, 4}, first[1].PIDs)
	require.Equal(t, []string{"passage one", "passage four"}, first[1].Passages)
	canonical, err := json.Marshal(first)
	require.NoError(t, err)
	reordered, err := json.Marshal(second)
	require.NoError(t, err)
	require.Equal(t, string(canonical), string(reordered))
	for i := 0; i < 50; i++ {
		got, e := sa.GetDatasetByID(context.Background(), "default")
		require.NoError(t, e)
		raw, e := json.Marshal(got)
		require.NoError(t, e)
		require.Equal(t, string(canonical), string(raw))
	}
}

func TestBoundedDatasetUnknownBeforeFilesystemAndMissingErrors(t *testing.T) {
	svc := &DatasetService{sampleDir: filepath.Join(t.TempDir(), "missing")}
	for _, id := range []string{"", "business-golden", "hidden", "../samples", "DEFAULT", "default "} {
		got, err := svc.GetDatasetByID(context.Background(), id)
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrDatasetUnknown)
	}
	got, err := svc.GetDatasetByID(context.Background(), "default")
	require.Nil(t, got)
	require.ErrorIs(t, err, os.ErrNotExist)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.GetDatasetByID(ctx, "default")
	require.ErrorIs(t, err, context.Canceled)
	for _, name := range []string{"queries", "corpus", "answers", "qrels", "qas"} {
		t.Run(name, func(t *testing.T) {
			dir := writeBoundedFixture(t, validBoundedFixture())
			require.NoError(t, os.Remove(filepath.Join(dir, name+".parquet")))
			_, e := loadDataset(dir, defaultDatasetLimits)
			require.ErrorIs(t, e, os.ErrNotExist)
		})
	}
}

func TestBoundedDatasetMalformedRelationsAndSparseIDs(t *testing.T) {
	cases := []struct {
		name   string
		modify func(*boundedDatasetFixture)
		want   error
	}{
		{"duplicate query", func(f *boundedDatasetFixture) { f.queries = append(f.queries, f.queries[0]) }, ErrDatasetInvalid},
		{"duplicate corpus", func(f *boundedDatasetFixture) { f.corpus = append(f.corpus, f.corpus[0]) }, ErrDatasetInvalid},
		{"duplicate answer", func(f *boundedDatasetFixture) { f.answers = append(f.answers, f.answers[0]) }, ErrDatasetInvalid},
		{"negative ID", func(f *boundedDatasetFixture) { f.queries[0].ID = -1 }, ErrDatasetInvalid},
		{"huge sparse PID", func(f *boundedDatasetFixture) { f.corpus[0].ID = 1 << 50; f.qrels[0].PID = 1 << 50 }, ErrDatasetResourceLimit},
		{"qrel missing query", func(f *boundedDatasetFixture) { f.qrels[0].QID = 9 }, ErrDatasetInvalid},
		{"qrel missing passage", func(f *boundedDatasetFixture) { f.qrels[0].PID = 9 }, ErrDatasetInvalid},
		{"duplicate qrel", func(f *boundedDatasetFixture) { f.qrels = append(f.qrels, f.qrels[0]) }, ErrDatasetInvalid},
		{"qa missing query", func(f *boundedDatasetFixture) { f.qas[0].QID = 9 }, ErrDatasetInvalid},
		{"qa missing answer", func(f *boundedDatasetFixture) { f.qas[0].AID = 9 }, ErrDatasetInvalid},
		{"multiple answer mappings", func(f *boundedDatasetFixture) { f.qas = append(f.qas, f.qas[0]) }, ErrDatasetInvalid},
		{"query without answer", func(f *boundedDatasetFixture) { f.qas = f.qas[:1] }, ErrDatasetInvalid},
		{"query without passage", func(f *boundedDatasetFixture) { f.qrels = f.qrels[:2] }, ErrDatasetInvalid},
		{"empty text", func(f *boundedDatasetFixture) { f.answers[0].Text = " \n " }, ErrDatasetInvalid},
		{"empty queries", func(f *boundedDatasetFixture) { f.queries = nil }, ErrDatasetInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validBoundedFixture()
			tc.modify(&f)
			got, e := loadDataset(writeBoundedFixture(t, f), defaultDatasetLimits)
			require.ErrorIs(t, e, tc.want)
			require.Nil(t, got.queries)
		})
	}
}

func TestBoundedDatasetResourceBudgetsOnRealParquet(t *testing.T) {
	cases := []struct {
		name   string
		change func(*datasetLimits)
	}{
		{"file bytes", func(l *datasetLimits) { l.maxFileBytes = 1 }},
		{"declared uncompressed bytes", func(l *datasetLimits) { l.maxDecodedBytes = 1 }},
		{"row count", func(l *datasetLimits) { l.maxRows = 1 }},
		{"text size", func(l *datasetLimits) { l.maxTextBytes = 3 }},
		{"aggregate text", func(l *datasetLimits) { l.maxTotalTextBytes = 10 }},
		{"ID budget", func(l *datasetLimits) { l.maxID = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := defaultDatasetLimits
			tc.change(&l)
			_, e := loadDataset(writeBoundedFixture(t, validBoundedFixture()), l)
			require.ErrorIs(t, e, ErrDatasetResourceLimit)
		})
	}
	// Exact largest allowed PID remains valid; it must not be renumbered silently.
	f := validBoundedFixture()
	f.corpus[0].ID = defaultDatasetLimits.maxID
	f.qrels[0].PID = defaultDatasetLimits.maxID
	d, e := loadDataset(writeBoundedFixture(t, f), defaultDatasetLimits)
	require.NoError(t, e)
	require.Equal(t, int(defaultDatasetLimits.maxID), d.Iterate()[1].PIDs[1])
}

func TestBoundedDatasetCorruptSchemaAndSymlinkReturnErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.parquet")
	require.NoError(t, os.WriteFile(bad, []byte("not a parquet footer"), 0600))
	_, err := loadParquet[TextInfo](bad)
	require.ErrorIs(t, err, ErrDatasetInvalid)
	type wrongSchema struct {
		Other int64 `parquet:"other"`
	}
	require.NoError(t, parquet.WriteFile(bad, []wrongSchema{{4}}))
	_, err = loadParquet[TextInfo](bad)
	require.ErrorIs(t, err, ErrDatasetInvalid)
	valid := filepath.Join(writeBoundedFixture(t, validBoundedFixture()), "queries.parquet")
	link := filepath.Join(dir, "link.parquet")
	require.NoError(t, os.Symlink(valid, link))
	_, err = loadParquet[TextInfo](link)
	require.ErrorIs(t, err, ErrDatasetInvalid)
}

func TestBoundedDatasetBundledSampleCompatibility(t *testing.T) {
	for _, name := range []string{"queries", "corpus", "answers", "qrels", "qas"} {
		f, e := os.Open("../../../dataset/samples/" + name + ".parquet")
		require.NoError(t, e)
		st, e := f.Stat()
		require.NoError(t, e)
		pf, e := parquet.OpenFile(f, st.Size())
		require.NoError(t, e)
		t.Logf("sample %s schema: %s", name, pf.Schema())
		f.Close()
	}
	dir := filepath.Join("..", "..", "..", "dataset", "samples")
	d, err := loadDataset(dir, defaultDatasetLimits)
	require.NoError(t, err)
	pairs := d.Iterate()
	require.NotEmpty(t, pairs)
	for i, p := range pairs {
		require.Equal(t, len(p.PIDs), len(p.Passages))
		require.NotEmpty(t, p.Question)
		require.NotEmpty(t, p.Answer)
		if i > 0 {
			require.Less(t, pairs[i-1].QID, p.QID)
		}
	}
	again, err := loadDataset(dir, defaultDatasetLimits)
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(pairs, again.Iterate()))
	_, err = d.GetContextForQID(-1)
	require.EqualError(t, err, "question ID not found")
	svc := &DatasetService{sampleDir: dir}
	fromService, err := svc.GetDatasetByID(context.Background(), "default")
	require.NoError(t, err)
	require.Equal(t, pairs, fromService)
}

func TestBoundedDatasetNumericStringIDsRejectMalformed(t *testing.T) {
	type stringText struct {
		ID   *string `parquet:"id,optional"`
		Text string  `parquet:"text"`
	}
	for _, value := range []string{"abc", "", "1x", "+42", "-1", " 42", "42 ", "0x2a", "1.0", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			dir := writeBoundedFixture(t, validBoundedFixture())
			rows := []stringText{{ID: &value, Text: "question"}}
			require.NoError(t, parquet.WriteFile(filepath.Join(dir, "queries.parquet"), rows))
			_, err := loadDataset(dir, defaultDatasetLimits)
			require.Error(t, err)
		})
	}
	t.Run("null", func(t *testing.T) {
		dir := writeBoundedFixture(t, validBoundedFixture())
		require.NoError(t, parquet.WriteFile(filepath.Join(dir, "queries.parquet"), []stringText{{Text: "question"}}))
		_, err := loadDataset(dir, defaultDatasetLimits)
		require.Error(t, err)
	})
}

func TestBoundedDatasetConcurrentLoadsAreIndependent(t *testing.T) {
	svc := &DatasetService{sampleDir: writeBoundedFixture(t, validBoundedFixture())}
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			pairs, err := svc.GetDatasetByID(context.Background(), "sample")
			if err == nil && (len(pairs) != 2 || pairs[0].QID != 0 || pairs[1].PIDs[0] != 1 || pairs[1].Passages[0] != "passage one") {
				err = fmt.Errorf("noncanonical concurrent load")
			}
			if err == nil {
				pairs[0].Question = "caller mutation"
				pairs[1].PIDs[0] = 999
			}
			results <- err
		}()
	}
	for i := 0; i < 8; i++ {
		require.NoError(t, <-results)
	}
	pairs, err := svc.GetDatasetByID(context.Background(), "default")
	require.NoError(t, err)
	require.Equal(t, "question zero", pairs[0].Question)
	require.Equal(t, []int{1, 4}, pairs[1].PIDs)
}
func TestBoundedDatasetDecimalBytesAndExtraColumns(t *testing.T) {
	type decimalRow struct {
		ID    string `parquet:"id"`
		Text  string `parquet:"text"`
		Extra string `parquet:"extra"`
	}
	p := filepath.Join(t.TempDir(), "decimal.parquet")
	require.NoError(t, parquet.WriteFile(p, []decimalRow{{"0", "zero", "ignored"}, {"42", "forty two", "ignored"}, {"100000", "largest", "ignored"}}))
	rows, err := loadParquet[TextInfo](p)
	require.NoError(t, err)
	require.Equal(t, []TextInfo{{0, "zero"}, {42, "forty two"}, {100000, "largest"}}, rows)
}

func TestBoundedDatasetPrintRandomAllowsZeroID(t *testing.T) {
	d := dataset{
		queries: map[int64]string{0: "zero query"}, answers: map[int64]string{0: "zero answer"},
		corpus: map[int64]string{0: "zero passage"}, qas: map[int64]int64{0: 0}, qrels: map[int64][]int64{0: {0}},
	}
	require.NoError(t, d.PrintRandomQA())
	empty := dataset{}
	require.EqualError(t, empty.PrintRandomQA(), "no questions available")
}
