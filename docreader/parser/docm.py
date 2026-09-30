"""Adapt macro-enabled Word packages for the python-docx fallback reader."""

import io
import zipfile

from lxml import etree

_MACRO_MAIN = "application/vnd.ms-word.document.macroEnabled.main+xml"
_DOCX_MAIN = (
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)
_OVERRIDE = "{http://schemas.openxmlformats.org/package/2006/content-types}Override"


def normalize_docm_content_type(content: bytes) -> bytes:
    """Change only the Word main-part type in an in-memory copy.

    python-docx rejects the DOCM main-part content type. The document XML is
    otherwise compatible. Preserve all other ZIP members, including VBA data;
    this reader does not execute macros or save a converted user document.
    """
    source = io.BytesIO(content)
    if not zipfile.is_zipfile(source):
        return content
    with zipfile.ZipFile(source) as archive:
        try:
            raw = archive.read("[Content_Types].xml")
        except KeyError:
            return content
        parser = etree.XMLParser(resolve_entities=False, no_network=True)
        root = etree.fromstring(raw, parser)
        changed = False
        for entry in root.findall(_OVERRIDE):
            if entry.get("ContentType") == _MACRO_MAIN:
                entry.set("ContentType", _DOCX_MAIN)
                changed = True
        if not changed:
            return content
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as rewritten:
            for info in archive.infolist():
                data = (
                    etree.tostring(root)
                    if info.filename == "[Content_Types].xml"
                    else archive.read(info)
                )
                rewritten.writestr(info, data)
        return output.getvalue()
