#!/usr/bin/env python3
"""Copyright (c) OpenAI. All rights reserved.

Detect missing fonts for PPTX rendering by converting to ODP and inspecting the resolved font
families per slide.

Overview
========
PowerPoint files (PPTX) declare requested font families in runs and theme defaults, but the actual
font used at render time depends on the renderer (LibreOffice in our pipeline), platform
availability, and style inheritance. To make detection stable and renderer-accurate, this module:

- Extracts requested families from PPTX per slide (reads a:r/a:rPr plus document defaults, grouped
  by script: latin/ea/cs/sym). Analysis is done per run: we infer the script from run text and
  select the matching a:rPr child (e.g., latin/ea/cs). Fonts declared for other scripts in the same
  run are not counted as used.
- Converts the PPTX to ODP using headless LibreOffice and parses ODP content.xml and styles.xml to
  discover which families LibreOffice actually resolved for each slide (including master pages and
  defaults).
- Classifies each requested family on each slide into two buckets:
  - font_missing: the family is not installed on the system (per fontconfig synonyms), so resolution
    cannot possibly match the request.
  - font_substituted: the family is installed but was resolved to another family in ODP for the
    slide (theme/style inheritance or glyph coverage), i.e., installed but substituted.

Key Design
-----------------------
1) Inspect the renderer's decision, not only the author's request. Reading PPTX alone tells you what
   was requested, not what LibreOffice will choose after applying styles and availability checks.
   Converting to ODP and reading the resolved fo:font-family/style:font-name* values yields a
   faithful view of what the renderer actually used for each slide.

2) Robust style resolution across ODP structures. Fonts can be specified under multiple layers. We
   parse office:automatic-styles (both content.xml and styles.xml), office:styles and
   style:default-style, draw:master-page references used by slides, nested style:text-properties
   under paragraph-properties, and parent style chains (style:parent-style-name). A text-based
   fallback parser supplements XML namespace lookups when vendor XML variations occur.

3) Scalable aliasing via fontconfig synonyms, not ad hoc maps. PostScript names, full names, and
   family names often differ. We build a synonym map from fc-list that unifies those identifiers. We
   deliberately do NOT use fc-match -s fallback chains for matching, because fallback families
   (e.g., DejaVu Sans) would mask missing/substitution cases and produce false passes.

4) Clear classification: missing vs substituted.
   - Missing: no synonym of the requested base family is present in the installed font set (per
     fontconfig). These require installation.
   - Substituted: the family is installed, but ODP does not reference it on the slide (LibreOffice
     chose another family), which is useful for diagnosing style/theme issues or glyph-coverage
     driven substitutions.

Not Chosen (and why)
--------------------
- PDF inspection (e.g., pdffonts): PostScript names don't reliably map back to authoring families;
  PDFs often reflect subsetted fonts and fallback choices, making robust detection noisy.
- Ad hoc alias tables: unscalable for large-scale fonts and platform variants; the fontconfig
  synonym corpus covers family/fullname/PostScript consistently.
- Treating fallback families as matches (fc-match -s): causes false negatives by accepting generic
  fallbacks when the requested family is missing.
- Hardcoding checks in the renderer: we keep detection separate from render_slides to avoid
  coupling and allow standalone checking.

CLI
---
- JSON output exposes two categories by default (and text mode mirrors them): font_missing_overall/
  font_missing_by_slide and font_substituted_overall/font_substituted_by_slide.
- Flags include_missing/include_substituted control which categories are emitted (default True/True).
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET
from functools import lru_cache
from os.path import abspath, basename, exists, expanduser, join, splitext
from zipfile import ZipFile

STYLE_TOKENS = [
    "regular",
    "condensed",
    "compressed",
    "narrow",
    "italic",
    "oblique",
    "semibold",
    "demibold",
    "bold",
    "black",
    "extra light",
    "ultra light",
    "extralight",
    "ultralight",
    "light",
    "thin",
    "medium",
]


def normalize_font_family_name(name: str) -> str:
    s = name.casefold()
    s = re.sub(r"\([^)]*\)", " ", s)
    s = re.sub(r"[\s\-\_\.,/\'\"]+", " ", s)
    return s.strip()


def _or_dummy(node: ET.Element | None) -> ET.Element:
    """Return the element if not None, otherwise a harmless dummy element.

    Avoids deprecated truthiness checks on Element instances (`elem or dummy`).
    """
    return node if node is not None else ET.Element("dummy")


@lru_cache(maxsize=1)
def _build_fc_synonym_map() -> dict[str, set[str]]:
    """Build synonym map from fontconfig; raise on failures; memoized (size=1)."""
    proc = subprocess.run(
        [
            "fc-list",
            "--format",
            "%{family}\t%{fullname}\t%{postscriptname}\n",
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    syn: dict[str, set[str]] = {}
    for line in (proc.stdout or "").splitlines():
        parts = line.split("\t")
        if len(parts) != 3:
            continue
        fam_field, full_field, ps_field = parts
        names: set[str] = set()
        for field in (fam_field, full_field, ps_field):
            for item in field.split(","):
                norm = normalize_font_family_name(item)
                if norm:
                    names.add(norm)
                    names.add(norm.replace(" ", ""))
        for name in list(names):
            bucket = syn.setdefault(name, set())
            bucket.update(names)
    return syn


def _expand_via_fontconfig(family_base_norm: str) -> set[str]:
    # Accept only true aliases/synonyms (family/fullname/PostScript) — not fallback replacements
    acceptable: set[str] = {family_base_norm, family_base_norm.replace(" ", "")}
    syn = _build_fc_synonym_map()
    if family_base_norm in syn:
        acceptable.update(syn[family_base_norm])
    no_space = family_base_norm.replace(" ", "")
    if no_space in syn:
        acceptable.update(syn[no_space])
    return acceptable


def parse_font_family_base_and_styles(name_norm: str) -> tuple[str, set[str]]:
    tokens = name_norm.split()
    required: set[str] = set()
    weight_code_map = {
        "25": "ultra light",
        "35": "thin",
        "45": "light",
        "55": "regular",
        "65": "medium",
        "75": "bold",
        "85": "black",
        "95": "black",
    }
    if tokens and tokens[0].isdigit() and tokens[0] in weight_code_map:
        required.add(weight_code_map[tokens[0]])
        tokens = tokens[1:]
    if len(tokens) == 1:
        t = tokens[0]
        fused_map = [
            ("extralight", "extra light"),
            ("ultralight", "ultra light"),
            ("semibold", "semibold"),
            ("demibold", "semibold"),
            ("condensed", "condensed"),
            ("compressed", "condensed"),
            ("narrow", "condensed"),
            ("italic", "italic"),
            ("oblique", "italic"),
            ("bold", "bold"),
            ("black", "black"),
            ("light", "light"),
            ("thin", "thin"),
            ("medium", "medium"),
            ("regular", "regular"),
        ]
        changed = True
        while changed:
            changed = False
            for suf, tok in fused_map:
                if t.endswith(suf) and len(t) > len(suf):
                    t = t[: -len(suf)]
                    required.add(tok)
                    changed = True
                    break
        return (t.strip(), required)

    while tokens:
        tail = " ".join(tokens[-2:]) if len(tokens) >= 2 else tokens[-1]
        matched = None
        for style in STYLE_TOKENS:
            if tail == style:
                matched = style
                break
        if matched is None and tokens[-1] in STYLE_TOKENS:
            matched = tokens[-1]
        if matched is None:
            break
        if matched in ("compressed", "narrow"):
            required.add("condensed")
        elif matched == "roman":
            required.add("regular")
        elif matched == "demibold":
            required.add("semibold")
        else:
            required.add(matched)
        if " " in matched:
            tokens = tokens[:-2]
        else:
            tokens = tokens[:-1]
    return (" ".join(tokens).strip(), required)


def _split_odf_family_list(value: str) -> list[str]:
    out: list[str] = []
    for part in value.split(","):
        p = part.strip().strip("\"' ")
        if p:
            out.append(normalize_font_family_name(p))
    return out


def extract_used_fonts_from_pptx(pptx_path: str) -> dict[int, set[str]]:
    by_slide: dict[int, set[str]] = {}
    with ZipFile(pptx_path, "r") as zf:
        for name in zf.namelist():
            if not (name.startswith("ppt/slides/slide") and name.endswith(".xml")):
                continue
            base = os.path.basename(name)
            m = re.search(r"(?i)slide(\d+)\.xml$", base)
            slide_num = int(m.group(1)) if m else None
            with zf.open(name) as f:
                tree = ET.parse(f)
            root = tree.getroot()
            ns = {"a": "http://schemas.openxmlformats.org/drawingml/2006/main"}
            defaults = _collect_default_font_faces(root)
            for r in root.findall(".//a:r", ns):
                parts: list[str] = []
                for t in r.findall("a:t", ns):
                    if t.text:
                        parts.append(t.text)
                text = "".join(parts)
                if not text:
                    continue
                script = _detect_script_tag(text)
                rpr = r.find("a:rPr", ns)
                face_norm: str | None = None
                if rpr is not None:
                    child = rpr.find(f"a:{script}", ns)
                    if child is not None:
                        face = child.get("typeface")
                        if face and not face.startswith("+"):
                            face_norm = normalize_font_family_name(face)
                bucket = by_slide.setdefault(slide_num or -1, set())
                if face_norm is None:
                    for f in defaults.get(script, set()):
                        bucket.add(f)
                else:
                    bucket.add(face_norm)
    return {k: v for k, v in by_slide.items() if k is not None and k != -1}


def _detect_script_tag(text: str) -> str:
    for ch in text:
        cp = ord(ch)
        if (
            0x4E00 <= cp <= 0x9FFF
            or 0x3400 <= cp <= 0x4DBF
            or 0xF900 <= cp <= 0xFAFF
            or 0x3040 <= cp <= 0x309F
            or 0x30A0 <= cp <= 0x30FF
            or 0x31F0 <= cp <= 0x31FF
            or 0xAC00 <= cp <= 0xD7AF
            or 0x3100 <= cp <= 0x312F
            or 0x3000 <= cp <= 0x303F
        ):
            return "ea"
    for ch in text:
        cp = ord(ch)
        if (
            0x0590 <= cp <= 0x05FF
            or 0x0600 <= cp <= 0x06FF
            or 0x0700 <= cp <= 0x077F
            or 0x0780 <= cp <= 0x07BF
            or 0x0900 <= cp <= 0x0D7F
            or 0x0E00 <= cp <= 0x0E7F
            or 0x0E80 <= cp <= 0x0EFF
            or 0xFB50 <= cp <= 0xFDFF
            or 0xFE70 <= cp <= 0xFEFF
        ):
            return "cs"
    for ch in text:
        cp = ord(ch)
        if (
            (0x0041 <= cp <= 0x005A)
            or (0x0061 <= cp <= 0x007A)
            or (0x0030 <= cp <= 0x0039)
            or (0x00C0 <= cp <= 0x024F)
            or (0x1E00 <= cp <= 0x1EFF)
        ):
            return "latin"
    return "latin"


def _collect_default_font_faces(root: ET.Element) -> dict[str, set[str]]:
    ns = {"a": "http://schemas.openxmlformats.org/drawingml/2006/main"}
    defaults: dict[str, set[str]] = {"latin": set(), "ea": set(), "cs": set(), "sym": set()}
    for defrpr in root.findall(".//a:defRPr", ns):
        for tag in ("latin", "ea", "cs", "sym"):
            child = defrpr.find(f"a:{tag}", ns)
            if child is not None:
                face = child.get("typeface")
                if face and not face.startswith("+"):
                    defaults[tag].add(normalize_font_family_name(face))
    return defaults


def _run_soffice_convert(cmd: list[str]) -> None:
    subprocess.run(
        cmd,
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        env=os.environ.copy(),
    )


def _export_to_odp(pptx_path: str, user_profile: str, out_dir: str, stem: str) -> str:
    bin_path = shutil.which("soffice") or shutil.which("libreoffice") or "/usr/bin/libreoffice"
    cmd_odp = [
        bin_path,
        "-env:UserInstallation=file://" + user_profile,
        "--invisible",
        "--headless",
        "--norestore",
        "--convert-to",
        "odp",
        "--outdir",
        out_dir,
        pptx_path,
    ]
    _run_soffice_convert(cmd_odp)
    odp_path = join(out_dir, f"{stem}.odp")
    return odp_path if exists(odp_path) else ""


def _collect_face_map(root: ET.Element, ns: dict[str, str]) -> dict[str, str]:
    face_map: dict[str, str] = {}
    decls = root.find("office:font-face-decls", ns)
    if decls is None:
        return face_map
    for ff in decls.findall("style:font-face", ns):
        name_attr = ff.get("{urn:oasis:names:tc:opendocument:xmlns:style:1.0}name") or ff.get(
            "style:name"
        )
        fam_attr = ff.get("{urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0}font-family")
        if not name_attr or not fam_attr:
            continue
        face_map[normalize_font_family_name(name_attr)] = normalize_font_family_name(fam_attr)
    return face_map


def _families_from_text_properties(
    tp: ET.Element, ns: dict[str, str], face_map: dict[str, str]
) -> set[str]:
    fams: set[str] = set()
    # Inspect current node for direct font-family
    fam_attr = tp.get("{urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0}font-family")
    if fam_attr:
        fams.update(_split_odf_family_list(fam_attr))
    # Inspect font-name aliases on current node
    for key in (
        "{urn:oasis:names:tc:opendocument:xmlns:style:1.0}font-name",
        "style:font-name",
        "style:font-name-asian",
        "style:font-name-complex",
    ):
        val = tp.get(key)
        if val:
            norm_val = normalize_font_family_name(val)
            mapped = face_map.get(norm_val)
            if mapped:
                fams.add(normalize_font_family_name(mapped))
            else:
                fams.add(norm_val)
    # Some styles nest text-properties under paragraph-properties or default-style blocks
    if not fams:
        nested = None
        # paragraph-properties/text-properties
        pp = tp.find("style:paragraph-properties", ns)
        if pp is not None:
            nested = pp.find("style:text-properties", ns)
        if nested is None:
            # When tp is actually the style:style node, try finding child text-properties directly
            nested = tp.find("style:text-properties", ns)
        if nested is not None and nested is not tp:
            fams.update(_families_from_text_properties(nested, ns, face_map))
    return fams


def _extract_styles_from_container(
    container: ET.Element | None, ns: dict[str, str], face_map: dict[str, str]
) -> tuple[dict[str, set[str]], set[str]]:
    styles: dict[str, set[str]] = {}
    defaults: set[str] = set()
    if container is None:
        return styles, defaults
    for st in container.findall("style:style", ns):
        name = st.get("{urn:oasis:names:tc:opendocument:xmlns:style:1.0}name") or st.get(
            "style:name"
        )
        if not name:
            continue
        fams = _families_from_text_properties(
            _or_dummy(st.find("style:text-properties", ns)), ns, face_map
        )
        if fams:
            styles[name] = fams
    for ds in container.findall("style:default-style", ns):
        defaults.update(
            _families_from_text_properties(
                _or_dummy(ds.find("style:text-properties", ns)), ns, face_map
            )
        )
    return styles, defaults


def _build_style_map(
    content: ET.Element,
    styles_root: ET.Element | None,
    ns: dict[str, str],
    face_map: dict[str, str],
) -> tuple[dict[str, set[str]], set[str]]:
    style_map: dict[str, set[str]] = {}
    default_fams: set[str] = set()
    auto_styles = content.find("office:automatic-styles", ns)
    styles_part, defaults_part = _extract_styles_from_container(auto_styles, ns, face_map)
    style_map.update(styles_part)
    default_fams.update(defaults_part)
    if styles_root is not None:
        # Also parse automatic-styles within styles.xml (document-styles)
        styles_auto = styles_root.find("office:automatic-styles", ns)
        styles_part, defaults_part = _extract_styles_from_container(styles_auto, ns, face_map)
        for k, v in styles_part.items():
            if k not in style_map:
                style_map[k] = v
        default_fams.update(defaults_part)
        common_styles = styles_root.find("office:styles", ns)
        styles_part, defaults_part = _extract_styles_from_container(common_styles, ns, face_map)
        for k, v in styles_part.items():
            if k not in style_map:
                style_map[k] = v
        default_fams.update(defaults_part)
        # top-level default-style under styles_root
        for ds in styles_root.findall("style:default-style", ns):
            default_fams.update(
                _families_from_text_properties(
                    _or_dummy(ds.find("style:text-properties", ns)), ns, face_map
                )
            )
        # Fallback: include any remaining style:style definitions anywhere in styles.xml
        for st in styles_root.findall(".//style:style", ns):
            name = st.get("{urn:oasis:names:tc:opendocument:xmlns:style:1.0}name") or st.get(
                "style:name"
            )
            if not name or name in style_map:
                continue
            fams = _families_from_text_properties(
                _or_dummy(st.find("style:text-properties", ns)), ns, face_map
            )
            if fams:
                style_map[name] = fams
    # also check top-level default-style in content root
    for ds in content.findall("style:default-style", ns):
        default_fams.update(
            _families_from_text_properties(
                _or_dummy(ds.find("style:text-properties", ns)), ns, face_map
            )
        )
    # Fallback: include any remaining style:style definitions anywhere in content.xml
    for st in content.findall(".//style:style", ns):
        name = st.get("{urn:oasis:names:tc:opendocument:xmlns:style:1.0}name") or st.get(
            "style:name"
        )
        if not name or name in style_map:
            continue
        fams = _families_from_text_properties(
            _or_dummy(st.find("style:text-properties", ns)), ns, face_map
        )
        if fams:
            style_map[name] = fams
    return style_map, default_fams


def _lookup_style_families(
    style_name: str, ns: dict[str, str], face_map: dict[str, str], roots: list[ET.Element | None]
) -> set[str]:
    fams: set[str] = set()
    if not style_name:
        return fams
    visited: set[str] = set()

    def _resolve(name: str) -> None:
        if not name or name in visited:
            return
        visited.add(name)
        for root in roots:
            if root is None:
                continue
            node = root.find(f".//style:style[@style:name='{name}']", ns)
            if node is None:
                node = root.find(f".//style:style[@{{{ns['style']}}}name='{name}']", ns)
            if node is None:
                continue
            fams.update(
                _families_from_text_properties(
                    _or_dummy(node.find("style:text-properties", ns)), ns, face_map
                )
            )
            # Follow parent style chain if present
            parent = node.get(
                "{urn:oasis:names:tc:opendocument:xmlns:style:1.0}parent-style-name"
            ) or node.get("style:parent-style-name")
            if parent:
                _resolve(parent)

    _resolve(style_name)
    return fams


def _collect_slide_families(
    page: ET.Element,
    ns: dict[str, str],
    style_map: dict[str, set[str]],
    face_map: dict[str, str],
    roots: list[ET.Element | None],
    text_style_map: dict[str, set[str]] | None = None,
) -> set[str]:
    slide_fams: set[str] = set()
    for el in page.iter():
        fam_attr = el.get(
            "{urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0}font-family"
        )
        if fam_attr:
            slide_fams.update(_split_odf_family_list(fam_attr))
        for attr in (
            "{urn:oasis:names:tc:opendocument:xmlns:text:1.0}style-name",
            "text:style-name",
            "{urn:oasis:names:tc:opendocument:xmlns:drawing:1.0}text-style-name",
            "draw:text-style-name",
            "draw:style-name",
            "presentation:style-name",
        ):
            style_name = el.get(attr)
            if not style_name:
                continue
            resolved_fams: set[str] = set()
            if style_name in style_map:
                resolved_fams.update(style_map[style_name])
            if not resolved_fams:
                # Fallback: resolve on the fly from XML if not present in prebuilt style_map
                resolved_fams.update(_lookup_style_families(style_name, ns, face_map, roots))
            if not resolved_fams and text_style_map and style_name in text_style_map:
                resolved_fams.update(text_style_map[style_name])
            if resolved_fams:
                slide_fams.update(resolved_fams)
    return slide_fams


def _build_style_map_text(xml_text: str) -> dict[str, set[str]]:
    # Best-effort textual extraction for cases missed by XML namespace lookups
    # Finds style:style name="X" blocks and extracts fo:font-family and style:font-name attributes
    style_map: dict[str, set[str]] = {}
    # Non-greedy match of a style:style block
    for m in re.finditer(
        r"<style:style[^>]*?\bstyle:name=\"([^\"]+)\"[\s\S]*?(?:</style:style>)",
        xml_text,
        flags=re.IGNORECASE,
    ):
        name = m.group(1).strip()
        block = m.group(0)
        fams: set[str] = set()
        # fo:font-family may be a comma list
        mff = re.search(r"fo:font-family=\"([^\"]+)\"", block, flags=re.IGNORECASE)
        if mff:
            for f in _split_odf_family_list(mff.group(1)):
                fams.add(f)
        # style:font-name may be a face alias; treat as family directly if present
        mfn = re.search(r"style:font-name=\"([^\"]+)\"", block, flags=re.IGNORECASE)
        if mfn:
            fams.add(normalize_font_family_name(mfn.group(1)))
        if fams:
            style_map[name] = fams
    return style_map


def _extract_slide_families_from_odp(odp_path: str) -> dict[int, set[str]]:
    ns = {
        "office": "urn:oasis:names:tc:opendocument:xmlns:office:1.0",
        "style": "urn:oasis:names:tc:opendocument:xmlns:style:1.0",
        "fo": "urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0",
        "draw": "urn:oasis:names:tc:opendocument:xmlns:drawing:1.0",
        "text": "urn:oasis:names:tc:opendocument:xmlns:text:1.0",
    }
    by_slide: dict[int, set[str]] = {}
    with ZipFile(odp_path, "r") as zf:
        content_bytes = zf.read("content.xml")
        styles_bytes = zf.read("styles.xml") if "styles.xml" in zf.namelist() else None
        content = ET.fromstring(content_bytes)
        styles_root = ET.fromstring(styles_bytes) if styles_bytes is not None else None
        styles_text = (
            styles_bytes.decode("utf-8", errors="ignore") if styles_bytes is not None else ""
        )

        face_map: dict[str, str] = {}
        face_map.update(_collect_face_map(content, ns))
        if styles_root is not None:
            face_map.update(_collect_face_map(styles_root, ns))

        style_map, default_fams = _build_style_map(content, styles_root, ns, face_map)
        # Augment style_map with textual parsing fallback (helps with tricky namespace emissions)
        text_style_map: dict[str, set[str]] = {}
        if styles_text:
            text_style_map = _build_style_map_text(styles_text)
            for k, v in text_style_map.items():
                if k not in style_map:
                    style_map[k] = v

        master_map: dict[str, set[str]] = _build_master_page_map(styles_root, ns, style_map)

        pres = content.find("office:body", ns)
        if pres is not None:
            pres = pres.find("office:presentation", ns)
        if pres is None:
            return {}
        pages = pres.findall("draw:page", ns)
        global_fams: set[str] = set()
        for idx, page in enumerate(pages, start=1):
            slide_fams = _collect_slide_families(
                page, ns, style_map, face_map, [content, styles_root], text_style_map
            )
            mp_name = page.get(
                "{urn:oasis:names:tc:opendocument:xmlns:drawing:1.0}master-page-name"
            ) or page.get("draw:master-page-name")
            if mp_name and mp_name in master_map:
                slide_fams.update(master_map[mp_name])
            # If theme placeholders like +mn lt are present, augment with defaults
            if any(f.startswith("+") for f in slide_fams) and default_fams:
                slide_fams.update(default_fams)
            if not slide_fams and default_fams:
                slide_fams.update(default_fams)
            expanded: set[str] = set()
            for f in slide_fams:
                base, _ = parse_font_family_base_and_styles(f)
                expanded.add(f)
                expanded.add(base)
                expanded.add(base.replace(" ", ""))
            by_slide[idx] = expanded
            global_fams.update(expanded)
        # As a last resort, use global families
        if global_fams:
            for idx in list(by_slide.keys()):
                if not by_slide[idx]:
                    by_slide[idx] = set(global_fams)
                elif all(f.startswith("+") for f in by_slide[idx]):
                    by_slide[idx].update(global_fams)
    return by_slide


def _build_master_page_map(
    styles_root: ET.Element | None, ns: dict[str, str], style_map: dict[str, set[str]]
) -> dict[str, set[str]]:
    master_map: dict[str, set[str]] = {}
    if styles_root is None:
        return master_map
    master_styles = styles_root.find("office:master-styles", ns)
    if master_styles is None:
        return master_map
    for mp in master_styles.findall("draw:master-page", ns):
        mname = mp.get("{urn:oasis:names:tc:opendocument:xmlns:drawing:1.0}name") or mp.get(
            "draw:name"
        )
        if not mname:
            continue
        fams: set[str] = set()
        for el in mp.iter():
            fam_attr = el.get(
                "{urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0}font-family"
            )
            if fam_attr:
                fams.update(_split_odf_family_list(fam_attr))
            for attr in (
                "{urn:oasis:names:tc:opendocument:xmlns:text:1.0}style-name",
                "text:style-name",
                "{urn:oasis:names:tc:opendocument:xmlns:drawing:1.0}text-style-name",
                "draw:text-style-name",
                "draw:style-name",
                "presentation:style-name",
            ):
                sname = el.get(attr)
                if sname and sname in style_map:
                    fams.update(style_map[sname])
        if fams:
            expanded: set[str] = set()
            for f in fams:
                base, _ = parse_font_family_base_and_styles(f)
                expanded.add(f)
                expanded.add(base)
                expanded.add(base.replace(" ", ""))
            master_map[mname] = expanded
    return master_map


def detect_missing_fonts_odp(pptx_path: str) -> tuple[set[str], dict[int, list[str]]]:
    pptx_path = abspath(pptx_path)
    used = extract_used_fonts_from_pptx(pptx_path)
    with tempfile.TemporaryDirectory(prefix="soffice_profile_") as prof:
        with tempfile.TemporaryDirectory(prefix="soffice_convert_") as out:
            stem = splitext(basename(pptx_path))[0]
            odp_path = _export_to_odp(pptx_path, prof, out, stem)
            if not odp_path:
                return set(), {}
            slide_fams = _extract_slide_families_from_odp(odp_path)

    missing_overall: set[str] = set()
    missing_by_slide: dict[int, list[str]] = {}
    syn_map = _build_fc_synonym_map()
    for slide_num, req_fams in used.items():
        odp_fams = slide_fams.get(slide_num, set())
        slide_missing: list[str] = []
        for req in req_fams:
            fam_base, _ = parse_font_family_base_and_styles(req)
            # Accept fontconfig-resolved aliases and no-space variants for the requested base family
            acceptable: set[str] = _expand_via_fontconfig(fam_base)
            # Determine if any acceptable alias is actually installed on system
            installed = any(alias in syn_map for alias in acceptable)
            # Missing if not installed at all, or if installed but not resolved in ODP families
            if (not installed) or ((req not in odp_fams) and not (acceptable & odp_fams)):
                slide_missing.append(req)
                missing_overall.add(req)
        if slide_missing:
            missing_by_slide[slide_num] = sorted(slide_missing)
    return missing_overall, missing_by_slide


def main() -> None:
    parser = argparse.ArgumentParser(
        description=(
            "Detect missing/substituted fonts for a PPTX by converting to ODP and inspecting resolved families."
        )
    )
    parser.add_argument("pptx_path", help="Path to .pptx file")
    parser.add_argument(
        "--json", dest="output_json", action="store_true", default=False, help="Emit JSON output"
    )
    parser.add_argument(
        "--include-missing",
        dest="include_missing",
        action="store_true",
        default=True,
        help="Include missing category",
    )
    parser.add_argument(
        "--include-substituted",
        dest="include_substituted",
        action="store_true",
        default=True,
        help="Include substituted category",
    )
    args = parser.parse_args()

    pptx_path = abspath(expanduser(args.pptx_path))
    used = extract_used_fonts_from_pptx(pptx_path)
    # Only build ODP families if we need to report substitutions
    slide_fams: dict[int, set[str]] = {}
    odp_available = False
    if args.include_substituted:
        with tempfile.TemporaryDirectory(prefix="soffice_profile_") as prof:
            with tempfile.TemporaryDirectory(prefix="soffice_convert_") as out:
                stem = splitext(basename(pptx_path))[0]
                odp_path = _export_to_odp(pptx_path, prof, out, stem)
                if odp_path:
                    slide_fams = _extract_slide_families_from_odp(odp_path)
                    odp_available = True

    syn_map = _build_fc_synonym_map()
    font_missing_by_slide: dict[int, list[str]] = {}
    font_substituted_by_slide: dict[int, list[str]] = {}
    for slide_num, req_fams in used.items():
        if args.include_substituted and odp_available:
            odp_fams = slide_fams.get(slide_num, set())
        else:
            odp_fams = set()
        miss_missing: list[str] = []
        miss_sub: list[str] = []
        for req in req_fams:
            fam_base, _ = parse_font_family_base_and_styles(req)
            acceptable: set[str] = _expand_via_fontconfig(fam_base)
            installed = any(alias in syn_map for alias in acceptable)
            if args.include_missing and not installed:
                miss_missing.append(req)
            if (
                args.include_substituted
                and odp_available
                and installed
                and (req not in odp_fams)
                and not (acceptable & odp_fams)
            ):
                miss_sub.append(req)
        if miss_missing:
            font_missing_by_slide[slide_num] = sorted(miss_missing)
        if miss_sub:
            font_substituted_by_slide[slide_num] = sorted(miss_sub)

    font_missing_overall: set[str] = (
        set().union(*font_missing_by_slide.values()) if font_missing_by_slide else set()
    )
    font_substituted_overall: set[str] = (
        set().union(*font_substituted_by_slide.values()) if font_substituted_by_slide else set()
    )

    if args.output_json:
        payload: dict[str, object] = {}
        if args.include_missing:
            payload["font_missing_overall"] = sorted(font_missing_overall)
            payload["font_missing_by_slide"] = {str(k): v for k, v in font_missing_by_slide.items()}
        if args.include_substituted:
            payload["font_substituted_overall"] = sorted(font_substituted_overall)
            payload["font_substituted_by_slide"] = {
                str(k): v for k, v in font_substituted_by_slide.items()
            }
        print(json.dumps(payload))
    else:
        any_missing = args.include_missing and bool(font_missing_overall)
        any_sub = args.include_substituted and bool(font_substituted_overall)
        if any_missing or any_sub:
            if any_missing:
                print("Fonts missing (not installed):")
                print(", ".join(sorted(font_missing_overall)))
                for slide_num in sorted(font_missing_by_slide.keys()):
                    print(f"Slide {slide_num} missing: ", end="")
                    print(", ".join(font_missing_by_slide[slide_num]))
            if any_sub:
                print("Fonts substituted (installed but substituted during rendering):")
                print(", ".join(sorted(font_substituted_overall)))
                for slide_num in sorted(font_substituted_by_slide.keys()):
                    print(f"Slide {slide_num} substituted: ", end="")
                    print(", ".join(font_substituted_by_slide[slide_num]))
        else:
            print("No font issues detected.")


if __name__ == "__main__":
    main()
