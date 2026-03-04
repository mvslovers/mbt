"""Tests for mbt/output.py."""

import sys
import json
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.output import format_shell, format_json, format_doctor
from mbt.config import ConfigSource


class TestFormatShell(unittest.TestCase):

    def test_simple_key_value(self):
        result = format_shell({"PROJECT_NAME": "httpd"})
        self.assertIn("PROJECT_NAME=httpd", result)

    def test_multiple_values(self):
        result = format_shell({
            "PROJECT_NAME": "httpd",
            "PROJECT_VERSION": "3.3.1-dev",
        })
        self.assertIn("PROJECT_NAME=httpd", result)
        self.assertIn("PROJECT_VERSION=3.3.1-dev", result)

    def test_one_per_line(self):
        result = format_shell({"A": "1", "B": "2"})
        lines = [l for l in result.split("\n") if l]
        self.assertEqual(len(lines), 2)

    def test_empty_value(self):
        result = format_shell({"EMPTY": ""})
        self.assertIn("EMPTY=", result)

    def test_list_value_space_joined(self):
        result = format_shell({"MACLIBS": ["DS1", "DS2", "DS3"]})
        self.assertIn("MACLIBS=DS1 DS2 DS3", result)

    def test_no_quoting(self):
        # Make $(eval ...) works without quotes
        result = format_shell({"KEY": "value"})
        self.assertNotIn('"', result)
        self.assertNotIn("'", result)

    def test_dsn_value(self):
        result = format_shell({"BUILD_DS_NCALIB": "IBMUSER.HTTPD.V3R3M1.NCALIB"})
        self.assertIn("BUILD_DS_NCALIB=IBMUSER.HTTPD.V3R3M1.NCALIB", result)


class TestFormatJson(unittest.TestCase):

    def test_valid_json(self):
        result = format_json({"foo": "bar"})
        parsed = json.loads(result)
        self.assertEqual(parsed["foo"], "bar")

    def test_nested_types(self):
        result = format_json({"n": 42, "b": True, "s": "hello"})
        parsed = json.loads(result)
        self.assertEqual(parsed["n"], 42)

    def test_indented(self):
        result = format_json({"a": "b"})
        # Should be pretty-printed (indented)
        self.assertIn("\n", result)

    def test_empty_dict(self):
        result = format_json({})
        parsed = json.loads(result)
        self.assertEqual(parsed, {})


class TestFormatDoctor(unittest.TestCase):

    def test_header_present(self):
        result = format_doctor({"MVS_HOST": ConfigSource("localhost", "default")})
        self.assertIn("[mbt] Configuration:", result)

    def test_value_present(self):
        result = format_doctor({"MVS_HOST": ConfigSource("myhost", "env")})
        self.assertIn("myhost", result)

    def test_source_present(self):
        result = format_doctor({"MVS_HOST": ConfigSource("myhost", "env")})
        self.assertIn("[env]", result)

    def test_multiple_entries(self):
        sourced = {
            "MVS_HOST": ConfigSource("localhost", "default"),
            "MVS_PORT": ConfigSource("1080", "~/.mbt/config.toml"),
            "MVS_HLQ": ConfigSource("CIUSER", "env"),
        }
        result = format_doctor(sourced)
        self.assertIn("localhost", result)
        self.assertIn("1080", result)
        self.assertIn("CIUSER", result)
        self.assertIn("default", result)
        self.assertIn("~/.mbt/config.toml", result)
        self.assertIn("[env]", result)

    def test_empty_dict(self):
        result = format_doctor({})
        self.assertIn("[mbt] Configuration:", result)


if __name__ == "__main__":
    unittest.main()
