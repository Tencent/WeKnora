package dingtalk

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func testConfig(overrides map[string]interface{}) *types.DataSourceConfig {
	creds := map[string]interface{}{
		"app_key":     "dingabcdefghij",
		"app_secret":  "s3cret-value-should-never-leak",
		"operator_id": "OPERATOR_UNION_ID",
	}
	for k, v := range overrides {
		if v == nil {
			delete(creds, k)
			continue
		}
		creds[k] = v
	}
	return &types.DataSourceConfig{Type: types.ConnectorTypeDingTalk, Credentials: creds}
}

func TestParseConfigRequiresCredentials(t *testing.T) {
	if _, err := parseConfig(nil); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("nil config error = %v, want ErrInvalidConfig", err)
	}

	for _, tc := range []struct {
		name    string
		missing string
		want    string
	}{
		{"no app key", "app_key", "app_key is required"},
		{"no app secret", "app_secret", "app_secret is required"},
		{"no operator", "operator_id", "operator_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfig(testConfig(map[string]interface{}{tc.missing: nil}))
			if err == nil {
				t.Fatalf("expected error when %s is missing", tc.missing)
			}
			if !errors.Is(err, datasource.ErrInvalidCredentials) {
				t.Fatalf("want ErrInvalidCredentials, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseConfigErrorsNeverLeakSecret(t *testing.T) {
	const secret = "s3cret-value-should-never-leak"
	_, err := parseConfig(testConfig(map[string]interface{}{"operator_id": nil}))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("app_secret leaked into error: %q", err.Error())
	}
}

func TestParseConfigRejectsEndpointOverride(t *testing.T) {
	_, err := parseConfig(testConfig(map[string]interface{}{"base_url": "https://attacker.example"}))
	if err == nil {
		t.Fatal("expected rejection for tenant-controlled DingTalk endpoint")
	}
	if !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
	if strings.Contains(err.Error(), "attacker.example") {
		t.Fatalf("endpoint value leaked into error: %q", err.Error())
	}
}

func TestParseConfigUsesFixedOfficialEndpoint(t *testing.T) {
	cfg, err := parseConfig(testConfig(nil))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got, want := cfg.BaseURL, officialAPIBaseURL; got != want {
		t.Fatalf("BaseURL = %q, want %q", got, want)
	}
}

// D3: an access token minted for one tenant/data source must never be served to
// another, even when both configure the identical DingTalk application.
func TestTokenCacheKeyIsolatesTenantsSharingOneApp(t *testing.T) {
	build := func(tenant, dataSource string) *config {
		cfg := testConfig(nil)
		cfg.Settings = map[string]interface{}{"tenant_id": tenant, "data_source_id": dataSource}
		parsed, err := parseConfig(cfg)
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		return parsed
	}

	tenantA := build("tenant-a", "ds-1")
	tenantB := build("tenant-b", "ds-1")
	sameTenantOtherSource := build("tenant-a", "ds-2")
	repeat := build("tenant-a", "ds-1")

	if tenantA.tokenCacheKey() == tenantB.tokenCacheKey() {
		t.Fatal("different tenants sharing one app must not share a token cache key")
	}
	if tenantA.tokenCacheKey() == sameTenantOtherSource.tokenCacheKey() {
		t.Fatal("different data sources must not share a token cache key")
	}
	if tenantA.tokenCacheKey() != repeat.tokenCacheKey() {
		t.Fatal("identical scope and credentials must reuse one cache key")
	}
}

func TestTokenCacheKeyChangesWithSecretAndCarriesNoSecret(t *testing.T) {
	base, err := parseConfig(testConfig(nil))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	rotated, err := parseConfig(testConfig(map[string]interface{}{"app_secret": "rotated-secret"}))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if base.tokenCacheKey() == rotated.tokenCacheKey() {
		t.Fatal("rotating app_secret must invalidate the cached token")
	}
	if strings.Contains(base.tokenCacheKey(), base.AppSecret) {
		t.Fatal("token cache key must not embed the raw app_secret")
	}

	otherIssuer := *base
	otherIssuer.BaseURL = "https://gateway.example.test"
	if base.tokenCacheKey() == otherIssuer.tokenCacheKey() {
		t.Fatal("changing the token issuer base URL must invalidate the cached token")
	}
	if strings.Contains(otherIssuer.tokenCacheKey(), otherIssuer.BaseURL) {
		t.Fatal("token cache key must not embed the raw base URL")
	}
}

// D2: node classification must distinguish container, document and unsupported.
// An unfamiliar leaf is unsupported — never an empty document, never a deletion.
func TestClassifyNodeKinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    node
		want classification
	}{
		{"folder", node{Type: "FOLDER"}, classContainer},
		{"online doc", node{Type: "FILE", DocKey: "adoc123"}, classDocument},
		{"online doc with children", node{Type: "FILE", Category: "ALIDOC", HasChildren: true}, classDocument},
		{"file with children", node{Type: "FILE", HasChildren: true}, classContainer},
		{"unknown type with children", node{HasChildren: true}, classContainer},
		{"file without docKey", node{Type: "FILE"}, classUnsupported},
		{"unknown type leaf", node{}, classUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.n.classify(); got != tc.want {
				t.Fatalf("classify() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNodeUsesOfficialALIDocShape(t *testing.T) {
	var n node
	err := json.Unmarshal([]byte(`{
		"nodeId":"node-1",
		"workspaceId":"ws-1",
		"name":"Roadmap",
		"type":"FILE",
		"category":"ALIDOC",
		"extension":"adoc",
		"createTime":"2026-07-27T10:00:00Z",
		"modifiedTime":"2026-07-28T11:12:13Z",
		"hasChildren":false
	}`), &n)
	if err != nil {
		t.Fatalf("unmarshal official node: %v", err)
	}
	if got := n.classify(); got != classDocument {
		t.Fatalf("classify() = %v, want ALIDOC document", got)
	}
	if got := n.documentKey(); got != "node-1" {
		t.Fatalf("documentKey() = %q, want node ID fallback accepted by blocks API", got)
	}
	if got := n.revision(); got != "2026-07-28T11:12:13Z" {
		t.Fatalf("revision() = %q", got)
	}
	wantTime := time.Date(2026, 7, 28, 11, 12, 13, 0, time.UTC)
	if got := n.lastModified(); !got.Equal(wantTime) {
		t.Fatalf("lastModified() = %v, want %v", got, wantTime)
	}
}

func TestDocumentKeyPrefersDocumentID(t *testing.T) {
	withDocID := node{NodeID: "node-1", DocumentID: "doc-key-1"}
	if got := withDocID.documentKey(); got != "doc-key-1" {
		t.Fatalf("documentKey() = %q, want doc-key-1", got)
	}
	withoutDocID := node{NodeID: "node-1"}
	if got := withoutDocID.documentKey(); got != "node-1" {
		t.Fatalf("documentKey() = %q, want node-1 fallback", got)
	}
}

func TestRevisionPrefersModifiedTimeAndFlagsUnknown(t *testing.T) {
	if got := (&node{ModifiedTime: "1753000000000"}).revision(); got != "1753000000000" {
		t.Fatalf("revision() = %q", got)
	}
	tsNode := node{CreateTime: "1753000000000"}
	if got := tsNode.revision(); got != "1753000000000" {
		t.Fatalf("revision() fallback = %q, want epoch millis", got)
	}
	if got := (&node{}).revision(); got != "" {
		t.Fatalf("unknown revision should be empty to force re-fetch, got %q", got)
	}
}

func TestMetadataRevisionChangesOnRenameButNotContentTimestamp(t *testing.T) {
	original := &node{
		Name:         "Original",
		URL:          "https://alidocs.dingtalk.com/i/node",
		WorkspaceID:  "ws-1",
		DocKey:       "doc-1",
		ModifiedTime: "1000",
	}
	renamed := *original
	renamed.Name = "Renamed"

	if original.revision() != renamed.revision() {
		t.Fatal("content revision unexpectedly changed for a metadata-only rename")
	}
	if original.metadataRevision() == renamed.metadataRevision() {
		t.Fatal("metadata revision did not change for rename")
	}
}

func TestMetadataRevisionIgnoresPrivateURLQueryButDetectsStablePathChange(t *testing.T) {
	original := &node{
		Name:        "Document",
		URL:         "https://alidocs.dingtalk.com/i/node-a?signature=old#first",
		WorkspaceID: "ws-1",
		DocKey:      "doc-1",
	}
	rotatedSignature := *original
	rotatedSignature.URL = "https://alidocs.dingtalk.com/i/node-a?signature=new#second"
	movedPath := *original
	movedPath.URL = "https://alidocs.dingtalk.com/i/node-b?signature=old"

	if original.metadataRevision() != rotatedSignature.metadataRevision() {
		t.Fatal("private query/fragment rotation changed the stable metadata revision")
	}
	if original.metadataRevision() == movedPath.metadataRevision() {
		t.Fatal("stable source path change did not change the metadata revision")
	}
}

func TestNodeUsesOfficialNumericTimestamps(t *testing.T) {
	var modified node
	err := json.Unmarshal([]byte(`{
		"nodeId":"node-1",
		"createTimestamp":1753000000000,
		"modifiedTimestamp":1753087654321
	}`), &modified)
	if err != nil {
		t.Fatalf("unmarshal numeric timestamps: %v", err)
	}
	if got := modified.revision(); got != "1753087654321" {
		t.Fatalf("revision() = %q, want official modifiedTimestamp", got)
	}
	wantModified := time.UnixMilli(1753087654321)
	if got := modified.lastModified(); !got.Equal(wantModified) {
		t.Fatalf("lastModified() = %v, want %v", got, wantModified)
	}

	var created node
	err = json.Unmarshal([]byte(`{"createTimestamp":1753000000}`), &created)
	if err != nil {
		t.Fatalf("unmarshal create timestamp: %v", err)
	}
	if got := created.revision(); got != "1753000000" {
		t.Fatalf("revision() fallback = %q, want official createTimestamp", got)
	}
	wantCreated := time.Unix(1753000000, 0)
	if got := created.lastModified(); !got.Equal(wantCreated) {
		t.Fatalf("lastModified() fallback = %v, want %v", got, wantCreated)
	}
}

func TestRedactIdentifierKeepsShortPrefixOnly(t *testing.T) {
	if got := redactIdentifier("dingabcdefghij"); got != "ding***" {
		t.Fatalf("redactIdentifier() = %q", got)
	}
	if got := redactIdentifier("abc"); got != "***" {
		t.Fatalf("short identifier must be fully masked, got %q", got)
	}
	if got := redactIdentifier(""); got != "" {
		t.Fatalf("empty stays empty, got %q", got)
	}
}
