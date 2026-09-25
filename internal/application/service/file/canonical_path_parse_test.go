package file

import (
	"strings"
	"testing"
)

// Canonical storage://<backend-id>/... paths are produced by the resource
// catalog and consumed by backendScopedFileService. Providers reached bare
// (process-global env storage, legacy tenants) must tolerate the same form,
// otherwise storageurl outbound rewrites fail with "invalid ... file path" and
// internal handles leak to IM channels (#3151).
func TestStorageBackendInnerPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"canonical s3 path", "storage://be-1/s3://weknora/data/a.jpg", "s3://weknora/data/a.jpg"},
		{"canonical minio path", "storage://be-1/minio://weknora/data/a.jpg", "minio://weknora/data/a.jpg"},
		{"bare provider path unchanged", "s3://weknora/data/a.jpg", "s3://weknora/data/a.jpg"},
		{"legacy URL unchanged", "https://bucket.cos.region.myqcloud.com/a.jpg", "https://bucket.cos.region.myqcloud.com/a.jpg"},
		{"resource handle unchanged", "resource://abc123", "resource://abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storageBackendInnerPath(tc.in); got != tc.want {
				t.Fatalf("storageBackendInnerPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseS3FilePathAcceptsCanonicalBackendPath(t *testing.T) {
	svc := &s3FileService{bucketName: "weknora"}

	got, err := svc.parseS3FilePath("storage://be-1/s3://weknora/data/10000/exports/a.jpg")
	if err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if got != "data/10000/exports/a.jpg" {
		t.Fatalf("object key = %q, want %q", got, "data/10000/exports/a.jpg")
	}

	// Bare provider paths keep working.
	if got, err := svc.parseS3FilePath("s3://weknora/data/a.jpg"); err != nil || got != "data/a.jpg" {
		t.Fatalf("bare path: key=%q err=%v", got, err)
	}

	// A canonical path for another bucket must still be rejected.
	if _, err := svc.parseS3FilePath("storage://be-1/s3://other-bucket/data/a.jpg"); err == nil {
		t.Fatal("expected bucket mismatch error for foreign canonical path")
	}

	// resource:// handles are not provider paths and must stay rejected.
	if _, err := svc.parseS3FilePath("resource://abc123"); err == nil {
		t.Fatal("expected error for resource:// handle")
	}
}

func TestParseMinioFilePathAcceptsCanonicalBackendPath(t *testing.T) {
	svc := &minioFileService{bucketName: "weknora"}

	got, err := svc.parseMinioFilePath("storage://be-1/minio://weknora/data/a.jpg")
	if err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if got != "data/a.jpg" {
		t.Fatalf("object key = %q, want %q", got, "data/a.jpg")
	}

	if _, err := svc.parseMinioFilePath("minio://weknora/data/a.jpg"); err != nil {
		t.Fatalf("bare path rejected: %v", err)
	}
}

func TestParseOssFilePathAcceptsCanonicalBackendPath(t *testing.T) {
	bucket, key, err := parseOssFilePath("storage://be-1/oss://weknora/data/a.jpg")
	if err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if bucket != "weknora" || key != "data/a.jpg" {
		t.Fatalf("bucket=%q key=%q, want weknora/data/a.jpg", bucket, key)
	}

	if _, _, err := parseOssFilePath("oss://weknora/data/a.jpg"); err != nil {
		t.Fatalf("bare path rejected: %v", err)
	}
}

func TestParseKS3FilePathAcceptsCanonicalBackendPath(t *testing.T) {
	bucket, key, err := parseKS3FilePath("storage://be-1/ks3://weknora/data/a.jpg")
	if err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if bucket != "weknora" || key != "data/a.jpg" {
		t.Fatalf("bucket=%q key=%q, want weknora/data/a.jpg", bucket, key)
	}

	if _, _, err := parseKS3FilePath("ks3://weknora/data/a.jpg"); err != nil {
		t.Fatalf("bare path rejected: %v", err)
	}
}

func TestParseTOSFilePathAcceptsCanonicalBackendPath(t *testing.T) {
	bucket, key, err := parseTOSFilePath("storage://be-1/tos://weknora/data/a.jpg")
	if err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if bucket != "weknora" || key != "data/a.jpg" {
		t.Fatalf("bucket=%q key=%q, want weknora/data/a.jpg", bucket, key)
	}

	if _, _, err := parseTOSFilePath("tos://weknora/data/a.jpg"); err != nil {
		t.Fatalf("bare path rejected: %v", err)
	}
}

func TestParseObsFilePathAcceptsCanonicalBackendPath(t *testing.T) {
	svc := &obsFileService{bucketName: "weknora"}

	got, err := svc.parseObsFilePath("storage://be-1/obs://weknora/data/a.jpg")
	if err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if got != "data/a.jpg" {
		t.Fatalf("object key = %q, want %q", got, "data/a.jpg")
	}

	// Without the fix the canonical form silently fell through to the raw
	// passthrough branch and came back as the "object key" verbatim.
	got, err = svc.parseObsFilePath("https://bucket.obs.region.mycloud.com/a.jpg")
	if err != nil || !strings.HasPrefix(got, "https://") {
		t.Fatalf("legacy URL passthrough broken: key=%q err=%v", got, err)
	}
}

func TestParseCosObjectNameAcceptsCanonicalBackendPath(t *testing.T) {
	svc := &cosFileService{}

	got, err := svc.parseCosObjectName("storage://be-1/cos://bucket/ap-shanghai/data/a.jpg")
	if err != nil {
		t.Fatalf("canonical path rejected: %v", err)
	}
	if got != "data/a.jpg" {
		t.Fatalf("object name = %q, want %q", got, "data/a.jpg")
	}

	// Cross-provider canonical paths must still be rejected, not silently
	// passed to the legacy-URL branch.
	if _, err := svc.parseCosObjectName("storage://be-1/s3://weknora/data/a.jpg"); err == nil {
		t.Fatal("expected error for foreign provider path")
	}
}
