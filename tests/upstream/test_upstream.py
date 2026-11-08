"""Verify update preparation preserves local patches and reports real conflicts."""

import contextlib
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.dont_write_bytecode = True
SCRIPT = Path(__file__).resolve().parents[2] / "scripts/upstream.py"
spec = importlib.util.spec_from_file_location("http_upstream", SCRIPT)
upstream = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upstream)


class PrepareTests(unittest.TestCase):
    def prepare(self, conflicting=False, addition=False):
        with tempfile.TemporaryDirectory() as temporary:
            temp = Path(temporary)
            root = temp / "http"
            root.mkdir()
            (root / "go.mod").write_text("module example.invalid/http\n\ngo 1.27\n")
            # Separate changes far enough to be independent merge hunks.
            base = (
                "package http\nconst Version = 1\n"
                + "\n".join(f"// stable line {i}" for i in range(12))
                + '\nconst Browser = "stock"\n'
            )
            local = base.replace('Browser = "stock"', 'Browser = "fork"')
            new = base.replace("Version = 1", "Version = 2")
            if conflicting:
                new = new.replace('Browser = "stock"', 'Browser = "new-stock"')
            (root / "client.go").write_text(local)
            sources = {}
            for name, text in [("old", base), ("new", new)]:
                source = temp / name
                path = source / "src/net/http/client.go"
                path.parent.mkdir(parents=True)
                path.write_text(text)
                sources[name] = source
            if addition:
                (sources["new"] / "src/net/http/extra.go").write_text(
                    "package http\nconst Extra = true\n"
                )
            empty = temp / "modules"
            empty.mkdir()
            lock = {
                "go": {"version": "go1.27.3", "commit": "old"},
                "x_net": {"version": "v0.62.0", "commit": "net"},
                "quic_go": {"version": "v0.65.0", "commit": "quic"},
                "own_files": [],
                "omitted_go_files": [],
                "omitted_httputil_files": [],
            }
            (root / "upstream.json").write_text(json.dumps(lock))
            output = temp / "output"

            def source(kind, version, supplied):
                if kind == "go":
                    return sources["new" if version == "go1.27.4" else "old"]
                return empty

            args = [str(SCRIPT), "prepare", "--go", "go1.27.4", "--output", str(output)]
            with patch.object(upstream, "ROOT", root), patch.object(
                upstream, "LOCK", lock
            ), patch.object(upstream, "source", side_effect=source), patch.object(
                upstream, "run", return_value="new-commit"
            ), patch.object(
                sys, "argv", args
            ), contextlib.redirect_stdout(
                io.StringIO()
            ):
                if conflicting:
                    with self.assertRaises(SystemExit) as raised:
                        upstream.main()
                    self.assertEqual(raised.exception.code, 2)
                else:
                    upstream.main()
            self.assertEqual(
                (root / "client.go").read_text(), local, "working tree was changed"
            )
            candidate = (output / "candidate/client.go").read_text()
            report = json.loads((output / "summary.json").read_text())
            if conflicting:
                self.assertEqual(report["conflicts"], ["client.go"])
                self.assertIn("<<<<<<< fork", candidate)
                self.assertIn('Browser = "fork"', candidate)
                self.assertIn('Browser = "new-stock"', candidate)
            else:
                self.assertEqual(report["conflicts"], [])
                self.assertIn("Version = 2", candidate)
                self.assertIn('Browser = "fork"', candidate)
            if addition:
                self.assertEqual(report["new_files"], ["extra.go"])
                self.assertTrue((output / "candidate/extra.go").is_file())
                self.assertIn("/dev/null", (output / "changes.diff").read_text())
            updated = json.loads((output / "candidate/upstream.json").read_text())
            self.assertEqual(
                updated["go"], {"version": "go1.27.4", "commit": "new-commit"}
            )

    def test_keeps_local_changes_and_accepts_independent_upstream_changes(self):
        self.prepare()

    def test_reports_conflicting_changes_without_overwriting_the_checkout(self):
        self.prepare(conflicting=True)

    def test_discovers_new_upstream_files(self):
        self.prepare(addition=True)


if __name__ == "__main__":
    unittest.main()
