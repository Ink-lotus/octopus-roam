"""Acceptance contracts: skips/missing tests and unsafe inputs cannot pass."""
import argparse
import copy
import http.server
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import types
import unittest
import urllib.request
from unittest.mock import Mock, patch

ROOT = Path(__file__).resolve().parents[2]


def module(name, relative):
    location = ROOT / relative
    spec = importlib.util.spec_from_file_location(name, location)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


server = module("v016_server_contract", "scripts/acceptance/v016/server.py")
source = module("v016_source_contract", "scripts/acceptance/v016/source.py")
sys.path.insert(0, str(ROOT / "scripts/acceptance/backup"))
product = module("v016_product_contract", "scripts/acceptance/backup/product_v016.py")
sys.path.pop(0)


class ExplicitServerEvidenceTests(unittest.TestCase):
    def evidence(self, inventory):
        rows = []
        for name, children in inventory.items():
            rows.extend({"Action": "pass", "Test": name + "/" + child} for child in children)
            rows.append({"Action": "pass", "Test": name})
        rows.append({"Action": "pass", "Package": "isolated"})
        return "\n".join(json.dumps(row) for row in rows)

    def test_requires_all_migration_subcases_and_actual_restore(self):
        self.assertEqual(server.audit_go(self.evidence(server.MIGRATION), server.MIGRATION, 0)["explicit_pass_count"], 16)
        self.assertEqual(server.audit_go(self.evidence(server.RESTORE), server.RESTORE, 0)["explicit_pass_count"], 1)

    def test_rejects_missing_subcase_even_when_package_passed(self):
        raw = self.evidence(server.MIGRATION)
        raw = "\n".join(line for line in raw.splitlines() if "/generated" not in line)
        with self.assertRaisesRegex(AssertionError, "inventory"):
            server.audit_go(raw, server.MIGRATION, 0)

    def test_rejects_direct_and_hidden_child_skips(self):
        for extra in ({"Action": "skip", "Test": next(iter(server.RESTORE))}, {"Action": "output", "Output": "--- SKIP: child"}):
            with self.subTest(extra=extra), self.assertRaisesRegex(AssertionError, "skip"):
                server.audit_go(self.evidence(server.RESTORE) + "\n" + json.dumps(extra), server.RESTORE, 0)

    def test_rejects_failure_exit_and_cached_results(self):
        with self.assertRaisesRegex(AssertionError, "failed"):
            server.audit_go(self.evidence(server.RESTORE), server.RESTORE, 1)
        with self.assertRaisesRegex(AssertionError, "cached"):
            server.audit_go(self.evidence(server.RESTORE) + '\n{"Action":"output","Output":"ok package (cached)"}', server.RESTORE, 0)

    def test_does_not_inherit_application_or_old_database_targets(self):
        values = {"ROAM_HOURLY18_DSN": "synthetic-forbidden", "OCTOPUS_ROAM_DATABASE_PATH": "synthetic-forbidden", "OCTOPUS_W03_TEST_CASE": "synthetic-old-case"}
        with patch.dict(os.environ, values):
            env = server.clean_environment()
        self.assertTrue(all(name not in env for name in values))
        self.assertEqual(env["GOPROXY"], "off")
        self.assertEqual(env["GOFLAGS"], "-mod=readonly")


class ProductInputTests(unittest.TestCase):
    def args(self, root):
        return argparse.Namespace(commit="a" * 40, artifact_sha="b" * 64, materials_sha="c" * 64,
            previous_manifest_sha=product.PREVIOUS_MANIFEST, output=root / "new-output",
            candidate=root / "candidate", previous_artifact=root / "old", fixtures=root / "fixtures",
            platform="windows" if sys.platform == "win32" else "docker", hold_for_browser=True,
            validate_only=True, allow_isolated_write=False, image="sha256:" + "d" * 64,
            previous_image="sha256:" + "e" * 64)

    def test_dry_validation_does_not_create_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            args = self.args(Path(temporary))
            product.validate_parameters(args)
            self.assertFalse(args.output.exists())

    def test_rejects_relabelled_old_artifact_and_short_sha(self):
        with tempfile.TemporaryDirectory() as temporary:
            args = self.args(Path(temporary))
            args.previous_manifest_sha = "f" * 64
            with self.assertRaisesRegex(AssertionError, "published"):
                product.validate_parameters(args)
            args.previous_manifest_sha = product.PREVIOUS_MANIFEST
            args.commit = "01f205e"
            with self.assertRaisesRegex(AssertionError, "full source"):
                product.validate_parameters(args)

    def test_rejects_existing_output_overlap_and_unapproved_write(self):
        with tempfile.TemporaryDirectory() as temporary:
            args = self.args(Path(temporary))
            args.output = args.candidate / "new"
            with self.assertRaisesRegex(AssertionError, "overlaps"):
                product.validate_parameters(args)
            args.output = Path(temporary)
            with self.assertRaisesRegex(AssertionError, "must be new"):
                product.validate_parameters(args)
            args.output = Path(temporary) / "new"
            args.validate_only = False
            with self.assertRaisesRegex(AssertionError, "permission"):
                product.validate_parameters(args)

    def test_rejects_floating_images(self):
        with self.assertRaisesRegex(AssertionError, "immutable"):
            product.immutable_image("ghcr.io/ink-lotus/octopus-roam:v0.1.5-amd64")


class ExportInspectionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.requests = []
        self.ciphertext = b'{"product":"octopus-roam","format":"encrypted-backup","version":1,"ciphertext":"synthetic-test-envelope"}'
        test = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_GET(self):
                self.send_response(405)
                self.send_header("Allow", "POST")
                self.end_headers()

            def do_POST(self):
                data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                test.requests.append((self.command, self.path, data))
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(test.ciphertext)))
                self.end_headers()
                self.wfile.write(test.ciphertext)

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()
        self.addCleanup(self.stop_server)
        self.runtime = object.__new__(product.Runtime)
        self.runtime.root = Path(self.temporary.name)
        self.runtime.url = f"http://127.0.0.1:{self.server.server_port}"
        self.runtime.client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.runtime.export_sequence = 0
        self.runtime.suite = types.SimpleNamespace(version="v0.1.5", inspect_backup=Mock())

    def stop_server(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def test_real_http_contract_uses_password_post_and_checks_inner_version(self):
        for version in (1005, 1006):
            inner = {"product": "octopus-roam", "version": version, "exported_at": "synthetic-time", "stats_hourly_archives": [{"format": 2, "payload": "native-bits"}]}
            self.runtime.suite.inspect_backup.return_value = copy.deepcopy(inner)
            actual = product.exported(self.runtime)
            self.assertEqual(actual["version"], version)
            self.assertEqual(actual["stats_hourly_archives"], inner["stats_hourly_archives"])
            self.assertNotIn("exported_at", actual)
            self.runtime.suite.inspect_backup.assert_called_with(self.ciphertext)
        self.assertEqual(self.requests, [("POST", "/api/v1/setting/export", {"password": product.BACKUP_PASSWORD})] * 2)
        for number in (1, 2):
            self.assertEqual((self.runtime.root / f"export-{number:03d}.encrypted.json").read_bytes(), self.ciphertext)
        records = [json.loads(line) for line in (self.runtime.root / "http.jsonl").read_text().splitlines()]
        self.assertTrue(all(row["status"] == 200 and row["method"] == "POST" for row in records))
        self.assertNotIn(product.BACKUP_PASSWORD, (self.runtime.root / "http.jsonl").read_text())

    def test_failed_inspection_retains_original_ciphertext(self):
        self.runtime.suite.inspect_backup.side_effect = AssertionError("authenticated inspection failed")
        with self.assertRaisesRegex(AssertionError, "authenticated"):
            product.exported(self.runtime)
        self.assertEqual((self.runtime.root / "export-001.encrypted.json").read_bytes(), self.ciphertext)
        self.assertTrue((self.runtime.root / "exports.jsonl").is_file())

    def test_rejected_get_records_actual_status(self):
        with self.assertRaisesRegex(AssertionError, "status 405"):
            self.runtime.api("/api/v1/setting/export", raw=True)
        record = json.loads((self.runtime.root / "http.jsonl").read_text())
        self.assertEqual((record["method"], record["status"]), ("GET", 405))
        self.assertFalse(list(self.runtime.root.glob("export-*.encrypted.json")))


class SourceSettingsTests(unittest.TestCase):
    def test_local_parameters_cannot_inject_database_or_arbitrary_environment(self):
        caches = {"gomodcache": "existing-go-modules", "gocache": "existing-go-build", "pnpm_store": "existing-pnpm-store"}
        self.assertEqual(source.settings_contract(caches), caches)
        for extra in ({"dsn": "synthetic-forbidden"}, {"GOTOOLCHAIN": "auto"}):
            with self.subTest(extra=extra), self.assertRaisesRegex(AssertionError, "unknown"):
                source.settings_contract({**caches, **extra})
        with self.assertRaisesRegex(AssertionError, "three"):
            source.settings_contract({"gomodcache": "one-cache"})


if __name__ == "__main__":
    unittest.main()
