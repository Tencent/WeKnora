"""Verify that an installed skill's Python sources can be loaded in this image.

Run inside the sandbox, by the interpreter a real skill call would use, as the
ordinary execution user, after the tree has been given its final permissions:

    python3 - <skill-dir> <relative-script> [<relative-script> ...]

Nothing here executes the skill's own code. Everything it reports is decided
from the parse tree and from what the environment can resolve, so a skill that
writes files or calls the network on import cannot do either while being
checked, and a script that never learned --help cannot be failed for it.

Exits 1 and writes one line per problem to stderr; exits 0 otherwise.
"""

import ast
import importlib.util
import os
import re
import sys

root = os.path.abspath(sys.argv[1])
relative_scripts = sys.argv[2:]
problems = []

# Directories that belong to the installed environment rather than the skill's
# own sources, and would drown the scan below in third-party module names.
PRUNED = {".venv", "venv", "node_modules", "__pycache__", ".git"}


def under_root(candidate):
    return candidate == root or candidate.startswith(root + os.sep)


def own_top_level_names():
    """Top-level names that refer to something the skill itself ships.

    Only the skill root is consulted. A nested file such as vendor/requests.py
    must not make `import requests` look first-party — that name is a
    dependency the installer was asked to put in the venv, and skipping it
    would snapshot a broken install.

    An import of one of these is the skill reaching for its own code, and
    whether it resolves depends on how the script arranges sys.path at run
    time — commonly by inserting its parent before importing `scripts.x`.
    That is a runtime decision this checker cannot evaluate without executing
    the file, so such imports are left to the skill. Sibling modules still
    resolve through find_spec, which puts the script's own directory first.
    """
    names = set()
    try:
        entries = os.listdir(root)
    except OSError:
        return names
    for name in entries:
        if name in PRUNED or name.startswith("."):
            continue
        full = os.path.join(root, name)
        if os.path.isdir(full):
            names.add(name)
        elif name.endswith(".py"):
            names.add(name[:-3])
    return names


own_modules = own_top_level_names()


def is_package(directory):
    return os.path.isfile(os.path.join(directory, "__init__.py"))


def module_exists(base, dotted):
    """Whether a dotted name resolves to a file or package under base."""
    target = os.path.join(base, *dotted.split(".")) if dotted else base
    return os.path.isfile(target + ".py") or os.path.isfile(
        os.path.join(target, "__init__.py")
    )


def is_importable(name, script_dir):
    """Whether a top-level name is available to this script at run time.

    find_spec consults the finders without importing, so a missing dependency
    is reported without the side effects of loading a present one. script_dir
    leads the path because that is where the interpreter puts the directory of
    the script it was handed, which is how sibling modules resolve at runtime.
    """
    if name in own_modules:
        return True
    saved = sys.path[:]
    sys.path.insert(0, script_dir)
    try:
        return importlib.util.find_spec(name) is not None
    except Exception:
        return False
    finally:
        sys.path[:] = saved


def check_imports(rel, tree, script_dir):
    """Check the imports that running this file is guaranteed to execute.

    Only statements at module level are checked, and only unconditionally: an
    import nested in try/except, in an `if`, or inside a function is how a
    skill declares an optional or lazily loaded dependency, and failing an
    install over one would reject a working skill.
    """
    for node in tree.body:
        if isinstance(node, ast.Import):
            for alias in node.names:
                top = alias.name.split(".")[0]
                if not is_importable(top, script_dir):
                    problems.append(
                        "%s imports %s, which is not available in this image"
                        % (rel, top)
                    )
        elif isinstance(node, ast.ImportFrom):
            if node.level:
                check_relative_import(rel, node, script_dir)
            elif node.module:
                top = node.module.split(".")[0]
                if not is_importable(top, script_dir):
                    problems.append(
                        "%s imports %s, which is not available in this image"
                        % (rel, top)
                    )


def check_relative_import(rel, node, script_dir):
    spelling = "." * node.level + (node.module or "")
    if not is_package(script_dir):
        problems.append(
            "%s uses the relative import '%s' but its directory has no "
            "__init__.py, so the import can never resolve when the script is "
            "run directly" % (rel, spelling)
        )
        return
    base = script_dir
    for _ in range(node.level - 1):
        base = os.path.dirname(base)
    if not under_root(base):
        problems.append(
            "%s uses the relative import '%s', which reaches outside the "
            "skill directory" % (rel, spelling)
        )
        return
    # A bare `from . import name` may be pulling something the package's
    # __init__ defines rather than a submodule, so only the package itself is
    # checked in that case.
    if node.module and not module_exists(base, node.module):
        problems.append(
            "%s imports '%s', which does not exist in the skill" % (rel, spelling)
        )


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
        for name in poetry:
            if str(name).strip().lower() == "python":
                continue
            yield "pyproject.toml", str(name)


def check_declared_requirements():
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
        except Exception:
            problems.append(
                "%s declares %s but it is not installed in %s"
                % (source, name, sys.prefix)
            )


for relative in relative_scripts:
    script = os.path.join(root, relative)
    try:
        with open(script, "rb") as source:
            code = source.read()
    except OSError as exc:
        problems.append(
            "%s cannot be read by the skill execution user (%s)" % (relative, exc)
        )
        continue
    try:
        parsed = ast.parse(code, filename=relative)
    except SyntaxError as exc:
        problems.append(
            "%s has a syntax error on line %s: %s" % (relative, exc.lineno, exc.msg)
        )
        continue
    check_imports(relative, parsed, os.path.dirname(script))

check_declared_requirements()

if problems:
    sys.stderr.write("\n".join(problems) + "\n")
    sys.exit(1)

print(
    "verified %d python file(s) against %s"
    % (len(relative_scripts), sys.executable or "python3")
)
