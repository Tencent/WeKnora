"""Verify that an installed skill's Python sources are well-formed in this image.

Run inside the sandbox, by the interpreter a real skill call would use, as the
ordinary execution user, after the tree has been given its final permissions:

    python3 - <skill-dir> <script> [<script> ...] [--optional <script> ...]

Nothing here executes the skill's own code, and nothing here decides whether an
import will resolve.

Import resolution used to live in this file, and it is gone on purpose. Whether
`import helper` works depends on what the script does to sys.path before it
runs — an idiom no static evaluator can enumerate: a guarded insert, a path
constant imported from a sibling module, a value read from the environment, a
mutation inside a helper function. Every approximation of that rejected skills
that run perfectly, and a rejected install throws away the dependency work that
succeeded and leaves the previous image serving. The installer agent has a root
shell and the real interpreter, so proving an import resolves is its job: it can
run the import instead of guessing at it.

What remains is what a file, not a runtime, can settle:

    - the execution user can read every source file
    - every source file parses
    - every distribution the manifests name is installed in the venv

Findings are graded, because rejecting an install is expensive.

    stdout  `note: ...` lines - reported, image kept
    stderr  problem lines - the install is refused
    exit 0  the image may be kept
    exit 1  a problem no installer round can fix: a syntax error, or a file the
            execution user cannot read
    exit 2  every problem is a dependency missing from this image, so handing
            these lines back to the installer is worth a round

Files named after --optional are checked identically, but their findings are
notes: nothing the skill offers loads a test or an example, and a bundled
tests/ directory must not decide whether the skill installs.
"""

import ast
import os
import re
import sys

OPTIONAL_FLAG = "--optional"

EXIT_UNREPAIRABLE = 1
EXIT_MISSING_DEPENDENCY = 2

root = os.path.abspath(sys.argv[1])
_argv_scripts = sys.argv[2:]
if OPTIONAL_FLAG in _argv_scripts:
    _cut = _argv_scripts.index(OPTIONAL_FLAG)
    entry_scripts = _argv_scripts[:_cut]
    optional_scripts = _argv_scripts[_cut + 1 :]
else:
    entry_scripts = _argv_scripts
    optional_scripts = []
all_scripts = entry_scripts + optional_scripts
optional_set = set(optional_scripts)

# problems refuse the install; notes are reported and the image is kept.
problems = []
notes = []

# Whether any problem is something installing a package cannot fix. It decides
# the exit code, which is how the caller knows if another installer round could
# help or if the bundle itself has to change.
unrepairable = False


def add_problem(message, repairable=False):
    global unrepairable
    if message not in problems:
        problems.append(message)
    if not repairable:
        unrepairable = True


def add_note(message):
    if message not in notes:
        notes.append(message)


def note_instead_of_problem(message, repairable=False):
    """Reporter for an auxiliary file: the finding is real, the verdict is not.

    repairable is accepted and ignored so this can stand in for add_problem;
    a note never influences the exit code.
    """
    add_note("%s (auxiliary file; this does not fail the install)" % message)


def distribution_name(raw):
    """The distribution a requirement line names, or '' if it cannot be read.

    Options (-r, -e, --index-url), VCS URLs, local paths and archives name no
    distribution of their own, and inventing one would fail an install over a
    requirement that is present.
    """
    line = raw.split("#")[0].strip()
    if not line or line.startswith("-"):
        return ""
    name = re.split(r"[\s<>=!~;\[@]", line)[0].strip()
    if not re.match(r"^[A-Za-z0-9][A-Za-z0-9._-]*$", name):
        return ""
    return name


def marker_of(raw):
    """The PEP 508 environment marker on a requirement line, or ''."""
    _, _, marker = raw.split("#")[0].partition(";")
    return marker.strip()


def marker_applies(marker):
    """Whether pip would install a line carrying this marker in this image.

    None means the answer cannot be established, and callers must read that as
    "not enforceable" rather than "absent". Markers compare versions with
    version semantics, so they are evaluated by `packaging` or not at all: a
    hand-rolled string comparison reads "3.10" as lower than "3.9" and would
    fail installs whose requirements pip resolved correctly. A bare venv has
    no `packaging`, so None is the common answer.
    """
    if re.search(r"\bextra\b", marker):
        # Gated on an extra, which pip does not install unless it is requested.
        return False
    try:
        from packaging.markers import Marker
    except Exception:
        return None
    try:
        return bool(Marker(marker).evaluate())
    except Exception:
        return None


def load_pyproject(path):
    try:
        import tomllib
    except ImportError:
        return None
    try:
        with open(path, "rb") as handle:
            return tomllib.load(handle)
    except Exception:
        return None


def declared_requirement_lines():
    """(source, requirement-line) pairs from requirements.txt and pyproject.toml."""
    requirements = os.path.join(root, "requirements.txt")
    if os.path.isfile(requirements):
        with open(requirements, encoding="utf-8", errors="replace") as handle:
            for line in handle:
                yield "requirements.txt", line
    pyproject = os.path.join(root, "pyproject.toml")
    if not os.path.isfile(pyproject):
        return
    data = load_pyproject(pyproject)
    if not data:
        return
    for item in data.get("project", {}).get("dependencies") or []:
        if isinstance(item, str):
            yield "pyproject.toml", item
    poetry = ((data.get("tool") or {}).get("poetry") or {}).get("dependencies") or {}
    if isinstance(poetry, dict):
        for name, spec in poetry.items():
            if str(name).strip().lower() == "python":
                continue
            # A table-valued dependency carrying optional, markers or a python
            # constraint is conditional, and poetry would not have installed it
            # here unconditionally.
            if isinstance(spec, dict) and (
                spec.get("optional") or spec.get("markers") or spec.get("python")
            ):
                continue
            yield "pyproject.toml", str(name)


def check_declared_requirements():
    """Check the manifests name nothing the venv is missing.

    This is the installer's literal instruction - "install requirements.txt" -
    so a distribution it names that pip did not land is a failed install. A
    line pip would have skipped is not: an environment marker that is false
    here, or an extras-gated dependency, is reported instead. Refusing an
    install over `pywin32; sys_platform == "win32"` on Linux rejects a skill
    whose requirements are all present.
    """
    try:
        from importlib import metadata
    except ImportError:
        return
    for source, raw in declared_requirement_lines():
        name = distribution_name(raw)
        if not name:
            continue
        try:
            metadata.distribution(name)
            continue
        except Exception:
            pass
        missing = "%s declares %s but it is not installed in %s" % (
            source,
            name,
            sys.prefix,
        )
        marker = marker_of(raw)
        if not marker:
            add_problem(missing, repairable=True)
            continue
        applies = marker_applies(marker)
        if applies:
            add_problem(missing, repairable=True)
        elif applies is None:
            add_note(
                "%s, and its environment marker '%s' cannot be evaluated here, "
                "so it is not enforced" % (missing, marker)
            )


for relative in all_scripts:
    report = note_instead_of_problem if relative in optional_set else add_problem
    script = os.path.join(root, relative)
    try:
        with open(script, "rb") as source:
            code = source.read()
    except OSError as exc:
        report("%s cannot be read by the skill execution user (%s)" % (relative, exc))
        continue
    try:
        ast.parse(code, filename=relative)
    except SyntaxError as exc:
        report("%s has a syntax error on line %s: %s" % (relative, exc.lineno, exc.msg))

check_declared_requirements()

for note in notes:
    sys.stdout.write("note: %s\n" % note)

if problems:
    sys.stderr.write("\n".join(problems) + "\n")
    sys.exit(EXIT_UNREPAIRABLE if unrepairable else EXIT_MISSING_DEPENDENCY)

print(
    "verified %d python file(s) against %s"
    % (len(all_scripts), sys.executable or "python3")
)
