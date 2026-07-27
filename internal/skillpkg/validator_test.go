package skillpkg

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type zipFixtureEntry struct {
	name string
	body string
	mode os.FileMode
}

func buildZipFixture(t *testing.T, entries ...zipFixtureEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		writer, err := zw.CreateHeader(header)
		require.NoError(t, err)
		_, err = writer.Write([]byte(entry.body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func validSkillZip(t *testing.T) []byte {
	return buildZipFixture(t,
		zipFixtureEntry{name: "SKILL.md", body: "---\nname: invoice-reader\ndescription: Read invoices\ncategory: data\nscripts:\n  - scripts/run.py\n---\n# Invoice Reader\n"},
		zipFixtureEntry{name: "scripts/run.py", body: "print('ok')\n"},
	)
}

func TestValidatorAcceptsValidSkillPackage(t *testing.T) {
	archive := validSkillZip(t)
	pkg, err := NewValidator(DefaultLimits()).Validate(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)
	require.Equal(t, "invoice-reader", pkg.Manifest.Name)
	require.Equal(t, "data", pkg.Manifest.Category)
	require.True(t, pkg.HasScripts)
	require.Len(t, pkg.Files, 2)
}

func TestValidatorRejectsUnsafePathsAndTypes(t *testing.T) {
	cases := []struct {
		name    string
		archive []byte
		code    string
	}{
		{"traversal", buildZipFixture(t, zipFixtureEntry{name: "../escape", body: "x"}), "path_traversal"},
		{"absolute", buildZipFixture(t, zipFixtureEntry{name: "/etc/passwd", body: "x"}), "absolute_path"},
		{"case duplicate", buildZipFixture(t,
			zipFixtureEntry{name: "SKILL.md", body: "x"},
			zipFixtureEntry{name: "skill.md", body: "y"},
		), "duplicate_path"},
		{"symlink", buildZipFixture(t, zipFixtureEntry{name: "SKILL.md", body: "x", mode: os.ModeSymlink | 0o777}), "unsupported_entry_type"},
		{"unsupported script", buildZipFixture(t,
			zipFixtureEntry{name: "SKILL.md", body: "---\nname: safe\ndescription: safe\n---\n"},
			zipFixtureEntry{name: "scripts/run.rb", body: "puts 'x'"},
		), "unsupported_script"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewValidator(DefaultLimits()).Validate(bytes.NewReader(tc.archive), int64(len(tc.archive)))
			require.Error(t, err)
			require.ErrorContains(t, err, tc.code)
		})
	}
}

func TestValidatorCountsActualExpandedBytes(t *testing.T) {
	archive := buildZipFixture(t,
		zipFixtureEntry{name: "SKILL.md", body: "---\nname: safe\ndescription: safe\n---\n"},
		zipFixtureEntry{name: "large.txt", body: "0123456789"},
	)
	limits := DefaultLimits()
	limits.MaxExpandedBytes = 20

	_, err := NewValidator(limits).Validate(bytes.NewReader(archive), int64(len(archive)))
	require.ErrorContains(t, err, "expanded_size_exceeded")
}
