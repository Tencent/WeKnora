package service

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Go tests above pin which commands an install issues. These run the
// embedded checker itself, because its judgement is what decides whether a
// working skill installs — and a check that is too strict fails good skills
// just as surely as one that is too loose passes broken ones. Every case here
// is a shape a real skill ships.
func TestSkillPythonVerifier(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		// wantProblem is a substring of the expected stderr. Empty means the
		// tree must verify cleanly.
		wantProblem string
	}{{
		name: "a package whose __init__ imports its own submodules",
		files: map[string]string{
			"scripts/__init__.py":        "from .chart_generator import ChartGenerator\n",
			"scripts/chart_generator.py": "class ChartGenerator:\n    pass\n",
		},
	}, {
		// The shape that failed a real install: sys.path is rearranged at run
		// time, so the absolute import of the skill's own package resolves
		// only once the file executes. Deciding it statically is not possible,
		// and guessing wrong rejects a working skill.
		name: "a script that puts the skill root on sys.path and imports its own package",
		files: map[string]string{
			"scripts/__init__.py": "",
			"scripts/helper.py":   "def go():\n    pass\n",
			"scripts/ux_regression_check.py": "import sys\n" +
				"from pathlib import Path\n" +
				"sys.path.insert(0, str(Path(__file__).resolve().parent.parent))\n" +
				"from scripts.helper import go\n",
		},
	}, {
		name: "a script importing a sibling module directly",
		files: map[string]string{
			"scripts/run.py":     "import helper\n",
			"scripts/helper.py":  "x = 1\n",
			"scripts/README.txt": "not python\n",
		},
	}, {
		name: "an optional dependency behind try/except",
		files: map[string]string{
			"scripts/run.py": "try:\n    import matplotlib\nexcept ImportError:\n" +
				"    matplotlib = None\n",
		},
	}, {
		name: "a dependency imported lazily inside a function",
		files: map[string]string{
			"scripts/run.py": "def render():\n    import matplotlib\n    return matplotlib\n",
		},
	}, {
		name: "a dependency the installer never installed",
		files: map[string]string{
			"scripts/run.py": "import totally_absent_package\n",
		},
		wantProblem: "scripts/run.py imports totally_absent_package, " +
			"which is not available in this image",
	}, {
		name: "a syntax error",
		files: map[string]string{
			"scripts/run.py": "def broken(:\n    pass\n",
		},
		wantProblem: "scripts/run.py has a syntax error on line 1",
	}, {
		name: "a relative import in a directory that is not a package",
		files: map[string]string{
			"scripts/run.py":     "from .helper import go\n",
			"scripts/helper.py":  "def go():\n    pass\n",
			"scripts/notinit.py": "",
		},
		wantProblem: "has no __init__.py",
	}, {
		name: "a relative import of a module the skill does not ship",
		files: map[string]string{
			"pkg/__init__.py":     "",
			"pkg/sub/__init__.py": "",
			"pkg/sub/run.py":      "from ..missing import go\n",
		},
		wantProblem: "pkg/sub/run.py imports '..missing', which does not exist in the skill",
	}, {
		name: "a requirement the venv does not carry",
		files: map[string]string{
			"requirements.txt": "# pinned\npandas==3.0.1\n-r other.txt\n" +
				"totally_absent_package>=1.0 ; python_version>=\"3.9\"\n",
			"scripts/run.py": "x = 1\n",
		},
		wantProblem: "requirements.txt declares totally_absent_package but it is not installed",
	}, {
		name: "a nested file name is not treated as a first-party import",
		files: map[string]string{
			"vendor/totally_absent_package.py": "x = 1\n",
			"scripts/run.py":                   "import totally_absent_package\n",
		},
		wantProblem: "scripts/run.py imports totally_absent_package, " +
			"which is not available in this image",
	}, {
		name: "a pyproject.toml dependency the venv does not carry",
		files: map[string]string{
			"pyproject.toml": "[project]\nname = \"demo\"\ndependencies = [\n" +
				"  \"totally_absent_package>=1.0\",\n]\n",
			"scripts/run.py": "x = 1\n",
		},
		wantProblem: "pyproject.toml declares totally_absent_package but it is not installed",
	}, {
		// Lines that name a distribution only indirectly cannot be checked by
		// name, and inventing one from the URL would fail installs whose
		// requirements are all present.
		name: "requirements that point at a VCS, an archive or a local path",
		files: map[string]string{
			"requirements.txt": "git+https://example.com/x/y.git#egg=y\n" +
				"./vendor/local-wheel.whl\n" +
				"https://example.com/pkg-1.0.tar.gz\n" +
				"--index-url https://example.com/simple\n",
			"scripts/run.py": "x = 1\n",
		},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeSkillTree(t, tc.files)
			stdout, stderr, err := runSkillPythonVerifier(t, root, pythonFiles(tc.files))

			if tc.wantProblem == "" {
				require.NoError(t, err, "this skill must install; stderr: %s", stderr)
				require.Contains(t, stdout, "verified")
				return
			}
			require.Error(t, err, "this skill is broken and must not reach a snapshot")
			require.Contains(t, stderr, tc.wantProblem)
		})
	}
}

// Verification must be able to read a skill that writes files or opens sockets
// the moment it is imported, without doing either.
func TestSkillPythonVerifierNeverExecutesTheSkill(t *testing.T) {
	root := writeSkillTree(t, map[string]string{
		"scripts/run.py": "import os\n" +
			"open(os.path.join(os.path.dirname(__file__), 'SIDE_EFFECT'), 'w').close()\n",
	})

	_, stderr, err := runSkillPythonVerifier(t, root, []string{"scripts/run.py"})

	require.NoError(t, err, stderr)
	_, statErr := os.Stat(filepath.Join(root, "scripts", "SIDE_EFFECT"))
	require.True(t, os.IsNotExist(statErr),
		"the checker ran the skill's module body instead of reading it")
}

// The skill tree is owned by root and readable by everyone; a file the
// execution user cannot open is an install that would fail on first use.
func TestSkillPythonVerifierReportsAnUnreadableScript(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 000 file, so this states nothing when tests run as root")
	}
	root := writeSkillTree(t, map[string]string{"scripts/run.py": "x = 1\n"})
	require.NoError(t, os.Chmod(filepath.Join(root, "scripts", "run.py"), 0o000))

	_, stderr, err := runSkillPythonVerifier(t, root, []string{"scripts/run.py"})

	require.Error(t, err)
	require.Contains(t, stderr, "cannot be read by the skill execution user")
}

func writeSkillTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return root
}

func pythonFiles(files map[string]string) []string {
	var scripts []string
	for rel := range files {
		if strings.HasSuffix(rel, ".py") {
			scripts = append(scripts, rel)
		}
	}
	sort.Strings(scripts)
	return scripts
}

// runSkillPythonVerifier feeds the embedded checker to a real interpreter the
// same way the sandbox command does: on stdin, with the tree and the files to
// check as argv.
func runSkillPythonVerifier(t *testing.T, root string, scripts []string) (string, string, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	cmd := exec.Command(python, append([]string{"-", root}, scripts...)...)
	cmd.Stdin = strings.NewReader(skillPythonVerifier)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	return stdout.String(), stderr.String(), runErr
}
