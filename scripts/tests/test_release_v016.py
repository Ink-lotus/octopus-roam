"""Acceptance contracts: skips/missing tests and unsafe inputs cannot pass."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

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
