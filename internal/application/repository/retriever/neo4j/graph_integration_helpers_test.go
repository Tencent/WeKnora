// Shared assertions for the live graph-database tests. Both engines are checked
// the same way: whatever the deletion path does internally, the namespace has
// to end up empty.
package neo4j

import (
	"context"
	"testing"

	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// assertNamespaceEmpty counts what is left under a label after a deletion.
// Relationships are counted without a label filter on the far endpoint so an
// edge left dangling to another namespace still shows up.
func assertNamespaceEmpty(ctx context.Context, t *testing.T, db driver.Driver, label string) {
	t.Helper()
	for _, probe := range []struct {
		what  string
		query string
	}{
		{what: "nodes", query: "MATCH (n:" + label + ") RETURN count(n) AS remaining"},
		{what: "relationships", query: "MATCH (n:" + label + ")-[r]-() RETURN count(r) AS remaining"},
	} {
		result, err := driver.ExecuteQuery(ctx, db, probe.query, nil, driver.EagerResultTransformer)
		if err != nil {
			t.Fatalf("count remaining %s: %v", probe.what, err)
		}
		if len(result.Records) != 1 {
			t.Fatalf("counting %s returned %d records, want 1", probe.what, len(result.Records))
		}
		remaining, _, err := driver.GetRecordValue[int64](result.Records[0], "remaining")
		if err != nil {
			t.Fatalf("decode remaining %s: %v", probe.what, err)
		}
		if remaining != 0 {
			t.Errorf("%d %s survived DelGraph", remaining, probe.what)
		}
	}
}

// countUnderLabel counts nodes or relationships attached to a label.
func countUnderLabel(ctx context.Context, t *testing.T, db driver.Driver, label, what string) int64 {
	t.Helper()
	query := "MATCH (n:" + label + ") RETURN count(n) AS remaining"
	if what == "relationships" {
		query = "MATCH (n:" + label + ")-[r]-() RETURN count(DISTINCT r) AS remaining"
	}
	result, err := driver.ExecuteQuery(ctx, db, query, nil, driver.EagerResultTransformer)
	if err != nil {
		t.Fatalf("count %s: %v", what, err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("counting %s returned %d records, want 1", what, len(result.Records))
	}
	count, _, err := driver.GetRecordValue[int64](result.Records[0], "remaining")
	if err != nil {
		t.Fatalf("decode %s count: %v", what, err)
	}
	return count
}

// assertNamespaceCount counts nodes or relationships under a label.
func assertNamespaceCount(
	ctx context.Context, t *testing.T, db driver.Driver, label, what string, want int64,
) {
	t.Helper()
	if got := countUnderLabel(ctx, t, db, label, what); got != want {
		t.Errorf("%s under %s = %d, want %d", what, label, got, want)
	}
}
