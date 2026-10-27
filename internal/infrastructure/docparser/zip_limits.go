package docparser

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
)

// A MinerU result package arrives over the network from a third-party parse API
// and is buffered before anything about it is validated, so both the download
// and what the archive expands to need a ceiling. The V1 client already caps the
// download (mineruV1MaxZipBytes); the Cloud converter did not, and neither path
// capped decompression.
const (
	// maxResultPackageBytes caps a downloaded result package. Same ceiling the
	// V1 client applies to the same artifact.
	maxResultPackageBytes = mineruV1MaxZipBytes

	// maxZipEntryBytes caps one decompressed entry. A markdown page or a single
	// extracted image is far below this; an entry that reaches it is a
	// compression bomb rather than document content.
	maxZipEntryBytes = 64 << 20

	// maxZipImagesTotalBytes caps the images extracted from one package. Every
	// image is held in memory at once, so the total is what actually decides the
	// footprint - bounding each entry alone does not bound a package holding
	// thousands of small ones.
	maxZipImagesTotalBytes = 512 << 20
)

// errResultPackageTooLarge marks a result package over one of the caps above.
var errResultPackageTooLarge = errors.New("result package exceeds maximum size")

// readResultPackage reads a downloaded result package, refusing anything larger
// than limit instead of buffering it. A non-positive limit falls back to
// maxResultPackageBytes.
//
// Mirrors readCapped in the datasource connectors and internal/infrastructure
// /web_search: read one byte past the cap so an oversized body is distinguishable
// from one that merely failed to read.
func readResultPackage(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = maxResultPackageBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", errResultPackageTooLarge, limit)
	}
	return data, nil
}

// readZipEntryCapped reads one archive entry under limit.
//
// It checks UncompressedSize64 first so an obviously oversized entry is never
// decompressed at all, then caps the read anyway: the declared size is attacker
// or vendor controlled and is allowed to understate what the entry expands to.
func readZipEntryCapped(f *zip.File, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = maxZipEntryBytes
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%w: entry %q declares %d bytes", errResultPackageTooLarge, f.Name, f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: entry %q expands past %d bytes", errResultPackageTooLarge, f.Name, limit)
	}
	return data, nil
}
