package linkcmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
)

// mkLinkFile seeds .weknora/project.yaml in dir so the unlink path has
// something to remove. Returns the absolute file path.
func mkLinkFile(t *testing.T, dir string) string {
	t.Helper()
	full := filepath.Join(dir, projectlink.DirName, projectlink.FileName)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := projectlink.Save(full, &projectlink.Project{Profile: "default", KBID: "kb_abc"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	return full
}

func TestUnlink_RemovesLinkInCwd(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	tmp := t.TempDir()
	linkPath := mkLinkFile(t, tmp)
	t.Chdir(tmp)

	if err := runUnlink(&UnlinkOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}); err != nil {
		t.Fatalf("runUnlink: %v", err)
	}
	if _, err := os.Stat(linkPath); !os.IsNotExist(err) {
		t.Errorf("link file should be gone, got err=%v", err)
	}
}

func TestUnlink_WalksUpFromSubdir(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	root := t.TempDir()
	linkPath := mkLinkFile(t, root)
	sub := filepath.Join(root, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	t.Chdir(sub)

	if err := runUnlink(&UnlinkOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}); err != nil {
		t.Fatalf("runUnlink: %v", err)
	}
	if _, err := os.Stat(linkPath); !os.IsNotExist(err) {
		t.Errorf("parent link file should be gone, got err=%v", err)
	}
}

func TestUnlink_NoLink_Errors(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	tmp := t.TempDir()
	t.Chdir(tmp)

	err := runUnlink(&UnlinkOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText})
	if err == nil {
		t.Fatal("expected error when no link present")
	}
	var typed *cmdutil.Error
	if !errors.As(err, &typed) {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if typed.Code != cmdutil.CodeInputInvalidArgument {
		t.Errorf("expected CodeInputInvalidArgument, got %s", typed.Code)
	}
	if !strings.Contains(typed.Message, projectlink.DirName) {
		t.Errorf("error message should name the missing path; got %q", typed.Message)
	}
}

func TestUnlink_JSON_BareObject(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	tmp := t.TempDir()
	mkLinkFile(t, tmp)
	t.Chdir(tmp)

	if err := runUnlink(&UnlinkOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}); err != nil {
		t.Fatalf("runUnlink: %v", err)
	}
	got := out.String()
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, got)
	}
	if !env.OK {
		t.Errorf("envelope.ok must be true, got %q", got)
	}
	for _, want := range []string{`"project_link_path"`, projectlink.DirName} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}
