"""Verify that an installed skill's Python sources can be loaded in this image.

Run inside the sandbox, by the interpreter a real skill call would use, as the
ordinary execution user, after the tree has been given its final permissions:

    python3 - <skill-dir> <script> [<script> ...] [--optional <script> ...]

Nothing here executes the skill's own code. Everything it reports is decided
from the parse tree and from what the environment can resolve, so a skill that
writes files or calls the network on import cannot do either while being
checked, and a script that never learned --help cannot be failed for it.

Loadability follows Python's import rules, not "this file as isolated
__main__". A file's import prefixes are its own directory plus every ancestor
up to the skill root, because any of those can be sys.path[0]: either the
bundle ships a script there (the runtime hands `python <file>` a directory),
or the file put it there itself. A sibling subtree is never an ancestor, so
vendor/requests.py still cannot make `import requests` look satisfied to a
script under scripts/.

Findings are graded, because rejecting an install is expensive: it throws away
minutes of dependency work and leaves the previous image serving.

    stdout  `note: ...` lines - reported, image kept
    stderr  problem lines - the install is refused
    exit 0  the image may be kept
    exit 1  a problem no installer round can fix: a syntax error, a file the
            execution user cannot read, a relative import that can never
            resolve
    exit 2  every problem is a dependency missing from this image, so handing
            these lines back to the installer is worth a round

Files named after --optional are checked identically, but their findings are
notes: nothing the skill offers loads a test or an example, and a bundled
tests/ directory must not decide whether the skill installs.
"""

import ast
import importlib.util
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

# The directories the bundle actually ships a script in. A prefix that is one
# of these is reachable on its own, because the runtime can be asked to execute
# a file there; any other prefix is only on sys.path if the skill puts it there.
script_dirs = {os.path.dirname(os.path.join(root, rel)) for rel in all_scripts}


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


def under_root(candidate):
    return candidate == root or candidate.startswith(root + os.sep)


def is_package(directory):
    """Whether directory is a regular Python package.

    Only __init__.py counts, and only the relative-import check needs it: a
    `from . import x` in a directory without one can never resolve when the
    file is run directly. Absolute imports do not consult this, because a
    PEP 420 namespace package has no __init__.py and is still importable.
    """
    return os.path.isfile(os.path.join(directory, "__init__.py"))


def module_exists(base, dotted):
    """Whether a dotted name resolves to a file or package under base."""
    target = os.path.join(base, *dotted.split(".")) if dotted else base
    return os.path.isfile(target + ".py") or os.path.isfile(
        os.path.join(target, "__init__.py")
    )


def import_search_dirs(script_path):
    """Every directory that could be sys.path[0] when this file is loaded.

    Its own directory comes first: execute_skill_script runs `python <file>`.
    Each ancestor up to the skill root follows, because that is what makes a
    neighbouring entry script's imports resolve - Python puts the directory of
    the script it was handed on the path, and a package below that directory,
    regular or namespace, is importable from there. It is also where the
    `sys.path.insert(0, parent)` idiom lands, which cannot be seen without
    executing the file.

    Over-approximating here rejects nothing that works. Under-approximating
    rejected the official office toolkit, whose library modules import their
    sibling packages by short name.
    """
    current = os.path.dirname(os.path.abspath(script_path))
    dirs = []
    while under_root(current):
        if current not in dirs:
            dirs.append(current)
        parent = os.path.dirname(current)
        if parent == current:
            break
        current = parent
    return dirs


def find_spec_with(name, prefixes):
    """Whether find_spec resolves name with prefixes ahead of sys.path.

    find_spec consults the finders without importing, so a missing dependency
    is reported without the side effects of loading a present one. Only
    top-level names are ever queried: find_spec on a dotted name imports the
    parent package, which would run the skill's own module body.
    """
    saved = sys.path[:]
    try:
        for directory in reversed(prefixes):
            sys.path.insert(0, directory)
        return importlib.util.find_spec(name) is not None
    except Exception:
        return False
    finally:
        sys.path[:] = saved


def resolve_top_level(name, script_path):
    """How a top-level name resolves for this file.

    "image"      the venv, the standard library, or a directory the bundle
                 ships a script in - something the runtime reaches on its own.
    "unattested" only a directory that ships no script of its own can provide
                 it, so it is on sys.path only if the file puts it there. That
                 is unprovable without executing, so it is reported rather
                 than trusted or refused.
    ""           nothing in this image can provide it.
    """
    dirs = import_search_dirs(script_path)
    attested = [directory for directory in dirs if directory in script_dirs]
    if find_spec_with(name, attested):
        return "image"
    if len(attested) != len(dirs) and find_spec_with(name, dirs):
        return "unattested"
    return ""


def check_imports(rel, tree, script_path, report):
    """Check the imports that loading this file is guaranteed to execute.

    Only statements at module level are checked, and only unconditionally: an
    import nested in try/except, in an `if`, or inside a function is how a
    skill declares an optional or lazily loaded dependency, and failing an
    install over one would reject a working skill.
    """
    script_dir = os.path.dirname(script_path)
    for node in tree.body:
        if isinstance(node, ast.Import):
            for alias in node.names:
                check_absolute_import(rel, alias.name, script_path, report)
        elif isinstance(node, ast.ImportFrom):
            if node.level:
                check_relative_import(rel, node, script_dir, report)
            elif node.module:
                check_absolute_import(rel, node.module, script_path, report)


def check_absolute_import(rel, dotted, script_path, report):
    name = dotted.split(".")[0]
    origin = resolve_top_level(name, script_path)
    if origin == "image":
        return
    if origin == "unattested":
        add_note(
            "%s imports %s, which the skill ships but no directory it executes "
            "scripts from can reach; it resolves only if the file puts that "
            "directory on sys.path itself" % (rel, name)
        )
        return
    report(
        "%s imports %s, which is not available in this image" % (rel, name),
        repairable=True,
    )


def check_relative_import(rel, node, script_dir, report):
    spelling = "." * node.level + (node.module or "")
    if not is_package(script_dir):
        report(
            "%s uses the relative import '%s' but its directory has no "
            "__init__.py, so the import can never resolve when the script is "
            "run directly" % (rel, spelling)
        )
        return
    base = script_dir
    for _ in range(node.level - 1):
        base = os.path.dirname(base)
    if not under_root(base):
        report(
            "%s uses the relative import '%s', which reaches outside the "
            "skill directory" % (rel, spelling)
        )
        return
    # A bare `from . import name` may be pulling something the package's
    # __init__ defines rather than a submodule, so only the package itself is
    # checked in that case.
    if node.module and not module_exists(base, node.module):
        report("%s imports '%s', which does not exist in the skill" % (rel, spelling))


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
        parsed = ast.parse(code, filename=relative)
    except SyntaxError as exc:
        report(
            "%s has a syntax error on line %s: %s" % (relative, exc.lineno, exc.msg)
        )
        continue
    check_imports(relative, parsed, script, report)

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
