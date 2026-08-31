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
	// A false environment marker is silent when packaging can evaluate it, and
	// a note when it cannot. The install must succeed in both environments;
	// only the note is conditional.
	unevaluableMarkerNote := ""
	if !pythonCanEvaluateMarkers(t) {
		unevaluableMarkerNote = "requirements.txt declares pywin32 but it is not installed"
	}

	cases := []struct {
		name  string
		files map[string]string
		// optional names the files whose findings must be reported instead of
		// refusing the install. The install path fills this from the naming
		// conventions the ecosystems share; here it is explicit.
		optional []string
		// wantProblem is a substring of the expected stderr. Empty means the
		// tree must verify cleanly.
		wantProblem string
		// wantExit is the code a failing tree must exit with: 2 when installing
		// a package would satisfy everything found, 1 when it would not. The
		// install flow reads it to decide whether another installer round is
		// worth its minutes.
		wantExit int
		// wantNote is a substring of a stdout note - something the checker
		// reported without refusing the install.
		wantNote string
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
		// and guessing wrong rejects a working skill. It is reported, because
		// the only directory that can satisfy it ships no script of its own.
		name: "a script that puts the skill root on sys.path and imports its own package",
		files: map[string]string{
			"scripts/__init__.py": "",
			"scripts/helper.py":   "def go():\n    pass\n",
			"scripts/ux_regression_check.py": "import sys\n" +
				"from pathlib import Path\n" +
				"sys.path.insert(0, str(Path(__file__).resolve().parent.parent))\n" +
				"from scripts.helper import go\n",
		},
		wantNote: "imports scripts, which the skill ships but no directory it " +
			"executes scripts from can reach",
	}, {
		name: "a script importing a sibling module directly",
		files: map[string]string{
			"scripts/run.py":     "import helper\n",
			"scripts/helper.py":  "x = 1\n",
			"scripts/README.txt": "not python\n",
		},
	}, {
		// scripts/ is both a package (relative imports in __init__) and a
		// directory of files the runtime executes with python file.py.
		// Loadability is the union of those prefixes, so a sibling import
		// still resolves when the package marker is present.
		name: "a packaged scripts directory whose files also import siblings",
		files: map[string]string{
			"scripts/__init__.py": "",
			"scripts/run.py":      "import helper\n",
			"scripts/helper.py":   "x = 1\n",
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
		wantExit: 2,
	}, {
		name: "a syntax error",
		files: map[string]string{
			"scripts/run.py": "def broken(:\n    pass\n",
		},
		wantProblem: "scripts/run.py has a syntax error on line 1",
		wantExit:    1,
	}, {
		// The exit code is the whole verdict, so one unfixable finding among
		// fixable ones has to sink the batch: sending the installer back for a
		// package it can install would only delay a failure it cannot.
		name: "a syntax error alongside a missing dependency",
		files: map[string]string{
			"scripts/bad.py": "def broken(:\n",
			"scripts/run.py": "import totally_absent_package\n",
		},
		wantProblem: "scripts/bad.py has a syntax error",
		wantExit:    1,
	}, {
		name: "a relative import in a directory that is not a package",
		files: map[string]string{
			"scripts/run.py":     "from .helper import go\n",
			"scripts/helper.py":  "def go():\n    pass\n",
			"scripts/notinit.py": "",
		},
		wantProblem: "has no __init__.py",
		wantExit:    1,
	}, {
		name: "a relative import of a module the skill does not ship",
		files: map[string]string{
			"pkg/__init__.py":     "",
			"pkg/sub/__init__.py": "",
			"pkg/sub/run.py":      "from ..missing import go\n",
		},
		wantProblem: "pkg/sub/run.py imports '..missing', which does not exist in the skill",
		wantExit:    1,
	}, {
		name: "a requirement the venv does not carry",
		files: map[string]string{
			"requirements.txt": "# pinned\npandas==3.0.1\n-r other.txt\n",
			"scripts/run.py":   "x = 1\n",
		},
		wantProblem: "requirements.txt declares pandas but it is not installed",
		wantExit:    2,
	}, {
		// pip skips a line whose marker is false here, so refusing the install
		// over it rejects a skill whose requirements are all present. Markers
		// compare versions with version semantics, so they are evaluated by
		// `packaging` or not at all. CI's system Python usually has it (the
		// marker is false, the line is silent); a bare skill venv does not
		// (the line is a note, not a failure). Either way the install proceeds.
		name: "a requirement gated by an environment marker",
		files: map[string]string{
			"requirements.txt": "pywin32; sys_platform == \"win32\"\n" +
				"totally_absent_package; extra == \"dev\"\n",
			"scripts/run.py": "x = 1\n",
		},
		wantNote: unevaluableMarkerNote,
	}, {
		// An extras-gated dependency is never installed unless the extra is
		// requested, so it is not even worth a note.
		name: "an extras-gated requirement is not reported at all",
		files: map[string]string{
			"requirements.txt": "totally_absent_package; extra == \"dev\"\n",
			"scripts/run.py":   "x = 1\n",
		},
	}, {
		// poetry tables carrying optional, markers or a python constraint are
		// conditional, and poetry would not have installed them here either.
		name: "a poetry dependency marked optional",
		files: map[string]string{
			"pyproject.toml": "[tool.poetry.dependencies]\npython = \"^3.11\"\n" +
				"totally_absent_package = { version = \"^1.0\", optional = true }\n",
			"scripts/run.py": "x = 1\n",
		},
	}, {
		// The vendor guarantee: a sibling module sharing an import's name does
		// not make the import resolve. The wording names the shipped file so a
		// repair round knows no package install will help.
		name: "a nested file name is not treated as a first-party import",
		files: map[string]string{
			"vendor/totally_absent_package.py": "x = 1\n",
			"scripts/run.py":                   "import totally_absent_package\n",
		},
		wantProblem: "scripts/run.py imports totally_absent_package, " +
			"which the skill ships at vendor/totally_absent_package.py",
		wantExit: 1,
	}, {
		// The official Anthropic office toolkit (xlsx/docx/pptx): a non-package
		// directory holds entry scripts plus sibling packages. Library modules
		// import those siblings by short name (`from helpers import ...`).
		// Treating every .py as isolated __main__ rejects this layout; Python's
		// package path root is scripts/office/, which is where helpers lives.
		name: "a library package importing a sibling package by short name",
		files: map[string]string{
			"scripts/recalc.py":         "from office.soffice import run_soffice\n",
			"scripts/office/soffice.py": "def run_soffice():\n    pass\n",
			"scripts/office/validate.py": "from helpers import safe_extract\n" +
				"from validators import BaseSchemaValidator\n",
			"scripts/office/helpers/__init__.py":    "def safe_extract():\n    pass\n",
			"scripts/office/validators/__init__.py": "from .base import BaseSchemaValidator\n",
			"scripts/office/validators/base.py": "from helpers import safe_extract\n" +
				"class BaseSchemaValidator:\n    pass\n",
			"scripts/office/validators/docx.py": "from helpers import safe_extract\n",
		},
	}, {
		name: "a library package importing a dependency the installer never installed",
		files: map[string]string{
			"scripts/office/validate.py":            "x = 1\n",
			"scripts/office/helpers/__init__.py":    "x = 1\n",
			"scripts/office/validators/__init__.py": "",
			"scripts/office/validators/base.py":     "import totally_absent_package\n",
		},
		wantProblem: "scripts/office/validators/base.py imports totally_absent_package, " +
			"which is not available in this image",
		wantExit: 2,
	}, {
		// The same layout without __init__.py anywhere. PEP 420 makes those
		// directories namespace packages, so a neighbouring entry script still
		// resolves the short-name import - which is why prefixes are every
		// ancestor and not only the ones a package marker chains together.
		name: "a namespace package importing a sibling by short name",
		files: map[string]string{
			"scripts/office/validate.py":         "from helpers import safe_extract\n",
			"scripts/office/helpers/__init__.py": "def safe_extract():\n    pass\n",
			"scripts/office/validators/base.py":  "from helpers import safe_extract\n",
			"scripts/pkg/sub/mod.py":             "from util import go\n",
			"scripts/pkg/util.py":                "def go():\n    pass\n",
		},
	}, {
		name: "a pyproject.toml dependency the venv does not carry",
		files: map[string]string{
			"pyproject.toml": "[project]\nname = \"demo\"\ndependencies = [\n" +
				"  \"totally_absent_package>=1.0\",\n]\n",
			"scripts/run.py": "x = 1\n",
		},
		wantProblem: "pyproject.toml declares totally_absent_package but it is not installed",
		wantExit:    2,
	}, {
		// Skills ship their tests. Nothing the skill offers loads them, so a
		// bundled tests/ directory must not decide whether the skill installs -
		// and refusing over one throws away the dependency work that succeeded.
		name: "a bundled test file importing a package the image does not carry",
		files: map[string]string{
			"scripts/run.py":    "x = 1\n",
			"tests/test_run.py": "import pytest\nimport totally_absent_package\n",
			"examples/demo.py":  "import totally_absent_package\n",
			"tests/conftest.py": "def broken(:\n",
			"scripts/helper.py": "x = 1\n",
		},
		optional: []string{
			"examples/demo.py", "tests/conftest.py", "tests/test_run.py",
		},
		wantNote: "auxiliary file; this does not fail the install",
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
	}, {
		// The lib/ + sys.path bootstrap layout - the shape that failed a real
		// install. Static resolution cannot see the bridge, but the script's
		// own bootstrap provably raises the directory that ships the module,
		// so the install passes with a note instead of a missing dependency.
		name: "a script that bootstraps a sibling lib directory and imports from it",
		files: map[string]string{
			"lib/image_video.py":  "def generate_image():\n    pass\n",
			"scripts/generate.py": "import sys\nimport os\n" +
				"script_dir = os.path.dirname(os.path.abspath(__file__))\n" +
				"lib_dir = os.path.join(script_dir, '..', 'lib')\n" +
				"sys.path.insert(0, lib_dir)\n" +
				"from image_video import generate_image\n",
		},
		wantNote: "which the skill ships at lib/image_video.py",
	}, {
		// The pathlib idiom must evaluate to the same bridge.
		name: "a pathlib bootstrap reaching a sibling module",
		files: map[string]string{
			"lib/helper.py":     "x = 1\n",
			"scripts/runner.py": "import sys\n" +
				"from pathlib import Path\n" +
				"sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'lib'))\n" +
				"import helper\n",
		},
		wantNote: "which the skill ships at lib/helper.py",
	}, {
		// The same shape without the bridge cannot resolve at runtime, and no
		// package install provides a skill's own module - the finding names
		// the shipped file instead of pretending a dependency is missing, and
		// fails the install outright: a repair round can neither install a
		// fix nor edit the tree.
		name: "a script importing a lib module without a sys.path bootstrap",
		files: map[string]string{
			"lib/image_video.py":  "def generate_image():\n    pass\n",
			"scripts/generate.py": "from image_video import generate_image\n",
		},
		wantProblem: "which the skill ships at lib/image_video.py",
		wantExit:    1,
	}, {
		// The vendor guarantee holds inside the new tier: a sibling module
		// sharing a dependency's name is still unresolvable unless the
		// script's own bootstrap provably reaches it. (A neutral name keeps
		// the case immune to whatever happens to be importable in the
		// environment running the tests.)
		name: "a vendored module must not satisfy an import without a bootstrap",
		files: map[string]string{
			"vendor/bridge_helper.py": "x = 1\n",
			"scripts/run.py":          "import bridge_helper\n",
		},
		wantProblem: "which the skill ships at vendor/bridge_helper.py",
		wantExit:    1,
	}, {
		// ...and when the bootstrap does reach the vendored copy, the script
		// genuinely works: reported, not refused.
		name: "a bootstrap that reaches the vendored copy is a note",
		files: map[string]string{
			"vendor/bridge_helper.py": "x = 1\n",
			"scripts/run.py": "import sys, os\n" +
				"sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'vendor'))\n" +
				"import bridge_helper\n",
		},
		wantNote: "which the skill ships at vendor/bridge_helper.py",
	}, {
		// A bridge the evaluator cannot prove stays a problem: guessing would
		// be exactly the false pass the vendor rule exists to prevent.
		name: "an unprovable bootstrap stays a problem",
		files: map[string]string{
			"lib/image_video.py":  "def generate_image():\n    pass\n",
			"scripts/generate.py": "import sys, os\n" +
				"sys.path.insert(0, os.environ['LIB_DIR'])\n" +
				"from image_video import generate_image\n",
		},
		wantProblem: "cannot be verified statically",
		wantExit:    1,
	}, {
		// Packages pip placed under .venv are not the skill's own code; the
		// scan must not turn a genuinely missing dependency into a shipped
		// note.
		name: "a module that only exists under .venv is still missing",
		files: map[string]string{
			".venv/lib/python3.11/site-packages/ghost_mod.py": "x = 1\n",
			"scripts/run.py":                                  "import ghost_mod\n",
		},
		wantProblem: "not available in this image",
		wantExit:    2,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeSkillTree(t, tc.files)
			entry := pythonFiles(tc.files)
			if len(tc.optional) > 0 {
				entry = withoutPaths(entry, tc.optional)
			}
			stdout, stderr, err := runSkillPythonVerifier(t, root, entry, tc.optional)

			if tc.wantProblem == "" {
				require.NoError(t, err, "this skill must install; stderr: %s", stderr)
				require.Contains(t, stdout, "verified")
			} else {
				require.Error(t, err, "this skill is broken and must not reach a snapshot")
				require.Contains(t, stderr, tc.wantProblem)
				require.Equal(t, tc.wantExit, verifierExitCode(t, err),
					"the exit code is what decides whether an installer round can fix this; "+
						"stderr: %s", stderr)
			}
			if tc.wantNote != "" {
				require.Contains(t, stdout, "note: ",
					"a finding that does not refuse the install must still be reported")
				require.Contains(t, stdout, tc.wantNote)
			}
		})
	}
}

func verifierExitCode(t *testing.T, err error) int {
	t.Helper()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "the checker must fail by exiting, not by crashing")
	return exit.ExitCode()
}

func withoutPaths(all, drop []string) []string {
	dropped := make(map[string]struct{}, len(drop))
	for _, rel := range drop {
		dropped[rel] = struct{}{}
	}
	kept := make([]string, 0, len(all))
	for _, rel := range all {
		if _, ok := dropped[rel]; !ok {
			kept = append(kept, rel)
		}
	}
	return kept
}

// Verification must be able to read a skill that writes files or opens sockets
// the moment it is imported, without doing either.
func TestSkillPythonVerifierNeverExecutesTheSkill(t *testing.T) {
	root := writeSkillTree(t, map[string]string{
		"scripts/run.py": "import os\n" +
			"open(os.path.join(os.path.dirname(__file__), 'SIDE_EFFECT'), 'w').close()\n",
	})

	_, stderr, err := runSkillPythonVerifier(t, root, []string{"scripts/run.py"}, nil)

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

	_, stderr, err := runSkillPythonVerifier(t, root, []string{"scripts/run.py"}, nil)

	require.Error(t, err)
	require.Contains(t, stderr, "cannot be read by the skill execution user")
	require.Equal(t, 1, verifierExitCode(t, err),
		"a file the execution user cannot read is not something installing a package fixes")
}

// The layout that started this: the official office toolkit ships entry scripts
// beside sibling packages, and its library modules import those siblings by
// short name. Treating every .py as an isolated __main__ rejected it outright.
// The imports it cannot resolve here are the third-party ones it genuinely
// needs — including two its SKILL.md never mentions — and that is the list the
// installer gets handed back.
func TestSkillPythonVerifierAcceptsTheOfficeToolkitLayout(t *testing.T) {
	files := map[string]string{
		"SKILL.md": "# xlsx\n",
		"scripts/recalc.py": "import json\nimport sys\nfrom pathlib import Path\n" +
			"from office.soffice import run_soffice\n",
		"scripts/office/soffice.py": "import subprocess\nimport tempfile\n" +
			"def run_soffice():\n    pass\n",
		"scripts/office/validate.py": "import argparse\n" +
			"from helpers import safe_extract\n" +
			"from validators import DOCXSchemaValidator\n",
		"scripts/office/helpers/__init__.py": "import zipfile\n" +
			"def safe_extract():\n    pass\n",
		"scripts/office/validators/__init__.py": "from .docx import DOCXSchemaValidator\n",
		"scripts/office/validators/base.py": "import re\n" +
			"from helpers import safe_extract\n" +
			"class BaseSchemaValidator:\n    pass\n",
		"scripts/office/validators/docx.py": "from helpers import safe_extract\n" +
			"from .base import BaseSchemaValidator\n" +
			"class DOCXSchemaValidator(BaseSchemaValidator):\n    pass\n",
		"scripts/office/helpers/pptx_chart.py": "from __future__ import annotations\n" +
			"import re\nfrom . import part_text\n",
	}
	root := writeSkillTree(t, files)

	stdout, stderr, err := runSkillPythonVerifier(t, root, pythonFiles(files), nil)

	require.NoError(t, err, "the toolkit's own layout must not be a failed install; stderr: %s", stderr)
	require.Contains(t, stdout, "verified")
	require.NotContains(t, stderr, "helpers",
		"helpers is a sibling package of validators/, reachable from scripts/office/")
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

// pythonCanEvaluateMarkers reports whether this interpreter has packaging, the
// library the checker uses for PEP 508 markers. Without it a false marker is a
// note rather than a skip, which is the contract a bare venv relies on.
func pythonCanEvaluateMarkers(t *testing.T) bool {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		return false
	}
	return exec.Command(python, "-c", "from packaging.markers import Marker").Run() == nil
}

// runSkillPythonVerifier feeds the embedded checker to a real interpreter the
// same way the sandbox command does: on stdin, with the tree and the files to
// check as argv, auxiliary files last behind the separator.
func runSkillPythonVerifier(
	t *testing.T, root string, scripts, optional []string,
) (string, string, error) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	argv := append([]string{"-", root}, scripts...)
	if len(optional) > 0 {
		argv = append(append(argv, skillVerifyOptionalFlag), optional...)
	}
	cmd := exec.Command(python, argv...)
	cmd.Stdin = strings.NewReader(skillPythonVerifier)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	return stdout.String(), stderr.String(), runErr
}
