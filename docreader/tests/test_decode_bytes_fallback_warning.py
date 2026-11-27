"""The latin-1 fallback must be the path that reports an undetectable file."""

import unittest

from docreader.utils.endecode import decode_bytes

# No encoding in the default candidate list can read these bytes, so the
# decoder has to reach the fallback -- the branch that warns about the result.
UNDECODABLE = bytes((0xFF, 0xFF))


class DecodeBytesFallbackWarningTests(unittest.TestCase):
    def test_fallback_reports_the_encoding_it_could_not_detect(self):
        with self.assertLogs("docreader.utils.endecode", level="WARNING") as logs:
            text = decode_bytes(UNDECODABLE)
        self.assertEqual(text, UNDECODABLE.decode("latin-1", errors="replace"))
        self.assertTrue(
            any("latin-1 as fallback" in line for line in logs.output),
            logs.output,
        )


if __name__ == "__main__":
    unittest.main()
