"""The LibreOffice probe must not depend on a POSIX `which` being installed."""

import os
import sys
import unittest

from docreader.parser.excel_convert import find_soffice, normalize_excel_bytes


class SofficeProbeTests(unittest.TestCase):
    def _without_posix_tools(self, func):
        """Run func() with a PATH that has no `which` executable, like a bare Windows host."""
        saved = os.environ.get("PATH", "")
        os.environ["PATH"] = (
            os.path.dirname(sys.executable) if os.name == "nt" else "/nonexistent-bin"
        )
        try:
            return func()
        finally:
            os.environ["PATH"] = saved

    def test_find_soffice_returns_none_instead_of_raising(self):
        self.assertIsNone(self._without_posix_tools(find_soffice))

    def test_unrecognized_spreadsheet_reports_the_documented_error(self):
        with self.assertRaises(ValueError):
            self._without_posix_tools(
                lambda: normalize_excel_bytes(b"not a spreadsheet at all")
            )


if __name__ == "__main__":
    unittest.main()
