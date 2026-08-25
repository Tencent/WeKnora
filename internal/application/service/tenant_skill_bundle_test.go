package service

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func zipBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func zipBundleWithSymlink(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f, err := w.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte(validSkillMD))
	require.NoError(t, err)

	header := &zip.FileHeader{Name: "scripts/link"}
	header.SetMode(0o777 | os.ModeSymlink)
	link, err := w.CreateHeader(header)
	require.NoError(t, err)
	_, err = link.Write([]byte("/etc/passwd"))
	require.NoError(t, err)

	require.NoError(t, w.Close())
	return buf.Bytes()
}

func zipBundleWithNFiles(t *testing.T, count int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	f, err := w.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte(validSkillMD))
	require.NoError(t, err)

	for i := range count {
		f, err := w.Create(fmt.Sprintf("scripts/%04d.txt", i))
		require.NoError(t, err)
		_, err = f.Write([]byte("x"))
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())
	return buf.Bytes()
}

func zipBundleWithDeclaredSize(t *testing.T, name string, size uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	header := &zip.FileHeader{
		Name:               name,
		Method:             zip.Store,
		UncompressedSize64: size,
		CompressedSize64:   0,
		CRC32:              0,
	}
	_, err := w.CreateRaw(header)
	require.NoError(t, err)

	f, err := w.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte(validSkillMD))
	require.NoError(t, err)

	require.NoError(t, w.Close())
	return buf.Bytes()
}

type repeatedByteReader byte

func (r repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func deflatedRepeatedBytes(t *testing.T, size int64) ([]byte, uint32) {
	t.Helper()
	var compressed bytes.Buffer
	checksum := crc32.NewIEEE()
	zw, err := flate.NewWriter(&compressed, flate.BestSpeed)
	require.NoError(t, err)

	_, err = io.Copy(io.MultiWriter(zw, checksum), io.LimitReader(repeatedByteReader('x'), size))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return compressed.Bytes(), checksum.Sum32()
}

func zipBundleExceedingDeclaredTotal(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	payload, checksum := deflatedRepeatedBytes(t, int64(maxSkillBundleFileBytes))

	for i := 0; i < maxSkillBundleTotalBytes/maxSkillBundleFileBytes+1; i++ {
		header := &zip.FileHeader{
			Name:               fmt.Sprintf("scripts/chunk-%d.bin", i),
			Method:             zip.Deflate,
			UncompressedSize64: uint64(maxSkillBundleFileBytes),
			CompressedSize64:   uint64(len(payload)),
			CRC32:              checksum,
		}
		f, err := w.CreateRaw(header)
		require.NoError(t, err)
		_, err = f.Write(payload)
		require.NoError(t, err)
	}

	f, err := w.Create("SKILL.md")
	require.NoError(t, err)
	_, err = f.Write([]byte(validSkillMD))
	require.NoError(t, err)

	require.NoError(t, w.Close())
	return buf.Bytes()
}

const validSkillMD = `---
name: pdf-tools
description: Extract text from PDF files
---

Use scripts/extract.py to pull text out of a PDF.
`

func TestParseSkillBundle(t *testing.T) {
	t.Run("rejects malformed zip input", func(t *testing.T) {
		_, err := ParseSkillBundle([]byte("not a zip"))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
		require.ErrorContains(t, err, "not a readable zip archive")
	})

	t.Run("reads frontmatter, body and files", func(t *testing.T) {
		data := zipBundle(t, map[string]string{
			"SKILL.md":           validSkillMD,
			"scripts/extract.py": "print('hi')\n",
			"requirements.txt":   "pypdf==4.0.0\n",
		})

		bundle, err := ParseSkillBundle(data)

		require.NoError(t, err)
		require.Equal(t, "pdf-tools", bundle.Name)
		require.Equal(t, "Extract text from PDF files", bundle.Description)
		require.Contains(t, bundle.Instructions, "scripts/extract.py")
		require.Len(t, bundle.SHA256, 64)
		require.Contains(t, bundle.Files, "scripts/extract.py")
	})

	t.Run("reads version frontmatter", func(t *testing.T) {
		data := zipBundle(t, map[string]string{
			"SKILL.md": `---
name: pdf-tools
version: 1.2.3
description: Extract text from PDF files
---

Use scripts/extract.py to pull text out of a PDF.
`,
		})

		bundle, err := ParseSkillBundle(data)

		require.NoError(t, err)
		require.Equal(t, "1.2.3", bundle.Version)
	})

	t.Run("tolerates a single top-level directory", func(t *testing.T) {
		data := zipBundle(t, map[string]string{
			"pdf-tools/SKILL.md":           validSkillMD,
			"pdf-tools/scripts/extract.py": "print('hi')\n",
		})

		bundle, err := ParseSkillBundle(data)

		require.NoError(t, err)
		require.Equal(t, "pdf-tools", bundle.Name)
		require.Contains(t, bundle.Files, "scripts/extract.py",
			"paths must be relative to the skill root, not to the archive root")
	})

	t.Run("remote options re-root a nested unique skill and drop extras", func(t *testing.T) {
		data := zipBundle(t, map[string]string{
			"repo-main/README.md":           "noise",
			"repo-main/skills/pdf/SKILL.md": validSkillMD,
			"repo-main/skills/pdf/run.py":   "print(1)\n",
		})
		bundle, err := ParseSkillBundleWithOptions(data, SkillBundleParseOptions{
			Subdir:           "skills/pdf",
			AllowExtraFiles:  true,
			AllowNestedSkill: true,
		})
		require.NoError(t, err)
		require.Equal(t, "pdf-tools", bundle.Name)
		require.Contains(t, bundle.Files, "run.py")
		require.NotContains(t, bundle.Files, "README.md")
	})

	t.Run("rejects files outside the wrapped skill directory", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundle(t, map[string]string{
			"pdf-tools/SKILL.md": validSkillMD,
			"README.md":          "left over from the zip root",
		}))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
		require.ErrorContains(t, err, "outside the skill directory")
	})

	t.Run("rejects an archive without SKILL.md", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundle(t, map[string]string{"a.txt": "x"}))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
	})

	t.Run("rejects too many entries", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundleWithNFiles(t, maxSkillBundleFiles))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
		require.ErrorContains(t, err, "archive holds more than")
	})

	t.Run("rejects per-entry oversize", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundleWithDeclaredSize(
			t, "scripts/large.bin", uint64(maxSkillBundleFileBytes+1),
		))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
		require.ErrorContains(t, err, `entry "scripts/large.bin" is too large`)
	})

	t.Run("rejects aggregate decompressed oversize", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundleExceedingDeclaredTotal(t))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
		require.ErrorContains(t, err, "archive is too large")
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundle(t, map[string]string{
			"SKILL.md":     validSkillMD,
			"../escape.py": "x",
		}))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
	})

	t.Run("rejects absolute paths", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundle(t, map[string]string{
			"SKILL.md": validSkillMD,
			"/tmp/x":   "x",
		}))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
	})

	t.Run("rejects symlinks", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundleWithSymlink(t))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
	})

	t.Run("rejects control characters in entry names", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundle(t, map[string]string{
			"SKILL.md":        validSkillMD,
			"scripts/a\nb.py": "x",
		}))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
		require.ErrorContains(t, err, "unsupported character")
	})

	t.Run("rejects NUL in entry names", func(t *testing.T) {
		_, err := ParseSkillBundle(zipBundle(t, map[string]string{
			"SKILL.md":          validSkillMD,
			"scripts/a\x00b.py": "x",
		}))
		require.ErrorIs(t, err, ErrSkillBundleInvalid)
		require.ErrorContains(t, err, "unsupported character")
	})

	t.Run("accepts the punctuation vendored dependencies actually use", func(t *testing.T) {
		bundle, err := ParseSkillBundle(zipBundle(t, map[string]string{
			"SKILL.md":                         validSkillMD,
			"node_modules/@scope/pkg/index.js": "x",
			"wheels/numpy-1.26.4+cpu.whl":      "x",
			"scripts/x$(id).py":                "x",
		}))

		require.NoError(t, err,
			"sandbox.ShellQuote makes any name inert, so refusing @ or + only broke real bundles")
		require.Contains(t, bundle.Files, "node_modules/@scope/pkg/index.js")
		require.Contains(t, bundle.Files, "wheels/numpy-1.26.4+cpu.whl")
		require.Contains(t, bundle.Files, "scripts/x$(id).py")
	})

	t.Run("accepts non-ASCII entry names", func(t *testing.T) {
		bundle, err := ParseSkillBundle(zipBundle(t, map[string]string{
			"SKILL.md":            validSkillMD,
			"references/参考 手册.md": "x",
		}))

		require.NoError(t, err)
		require.Contains(t, bundle.Files, "references/参考 手册.md",
			"real skills ship documents named in the operator's own language")
	})

	t.Run("same bytes hash the same", func(t *testing.T) {
		data := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})
		a, err := ParseSkillBundle(data)
		require.NoError(t, err)
		b, err := ParseSkillBundle(data)
		require.NoError(t, err)
		require.Equal(t, a.SHA256, b.SHA256)
	})
}

func TestListSkillZipFilesDoesNotInflateBodies(t *testing.T) {
	data := zipBundle(t, map[string]string{
		"pdf-tools/SKILL.md":           validSkillMD,
		"pdf-tools/scripts/extract.py": "print('hi')\n",
	})

	files, err := listSkillZipFiles(data)
	require.NoError(t, err)
	require.Equal(t, []SkillFileEntry{
		{Path: "SKILL.md", Size: int64(len(validSkillMD))},
		{Path: "scripts/extract.py", Size: int64(len("print('hi')\n"))},
	}, files)

	body, err := readSkillZipFile(data, "scripts/extract.py")
	require.NoError(t, err)
	require.Equal(t, []byte("print('hi')\n"), body)

	_, err = readSkillZipFile(data, "missing.txt")
	require.ErrorIs(t, err, errSkillFileMissing)
}
