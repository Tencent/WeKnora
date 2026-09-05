# Synthetic preview fixtures

`legacy_preview.doc` is a real Word 97-2003 OLE document. `legacy_preview.docx`
is a real OOXML ZIP document. Both were generated locally with macOS textutil
from exactly the following public-safe synthetic text:

    Synthetic legacy Word preview regression fixture.
    Original download must remain unchanged.

They contain no customer data. Tests exercise format validation and contract
routing; actual LibreOffice conversion runs when soffice is available.
