"""Exercise sealed-artifact acceptance adapters with synthetic bytes; never start a DB/product."""
import argparse
from contextlib import ExitStack
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import unittest
from unittest.mock import patch
import uuid
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import release_acceptance as acceptance
import release_contract as contract
import release_materials as materials
import test_release_contract as fixtures
from test_release_materials import fixture_application, production_source_files

VERSION, COMMIT = fixtures.VERSION, fixtures.COMMIT
REPOSITORY = Path(__file__).resolve().parents[2]


def rewrite_fixture_zip(path, entries):
    # Only freshly created synthetic test bytes are rewritten in this helper.
    with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
        for name, data in sorted(entries.items()):
            archive.writestr(name, data)


def refresh_layout_fixture(layout):
    files = contract.tree(layout / 'source')
    record = contract.read_json(layout / 'build-info.json')
    record['source_files'] = files
    contract.write_json(layout / 'build-info.json', record)
    (layout / 'source-input-sha256sums.txt').write_text(
        ''.join(f'{value}  {name}\n' for name, value in files.items()), encoding='utf-8')
    files = contract.tree(layout)
    files.pop('file-sha256sums.txt')
    (layout / 'file-sha256sums.txt').write_text(
        ''.join(f'{value}  {name}\n' for name, value in files.items()), encoding='utf-8')


class AcceptanceTests(unittest.TestCase):
    def setUp(self):
        self.root = fixtures.new_directory()
        self.checkout = self.root / 'checkout'
        self.checkout.mkdir()
        self.application = fixture_application()
        for name, data in self.application.items():
            path = self.checkout / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        (self.checkout / 'plan').mkdir()
        (self.checkout / 'plan/release-notes-v0.1.0.md').write_text(f'# Octopus Roam {VERSION}\nSynthetic reviewed notes\n')
        stack = self.enterContext(ExitStack())
        previous = Path.cwd()
        os.chdir(self.checkout)
        self.addCleanup(os.chdir, previous)
        stack.enter_context(patch.object(acceptance, 'REPOSITORY', self.checkout))
        stack.enter_context(patch.dict(os.environ, {'GITHUB_REPOSITORY': materials.REPOSITORY}))
        stack.enter_context(patch.object(materials, 'target', return_value=1))
        stack.enter_context(patch.object(contract.subprocess, 'check_output',
            return_value=('\0'.join(sorted(self.application)) + '\0').encode()))
        def go(*args):
            value = fixtures.fixture_go(*args)
            if args[:2] == ('version', '-m'):
                value += f'\tbuild\tvcs.revision={COMMIT}\n\tbuild\tvcs.modified=false\n'
            return value
        stack.enter_context(patch.object(contract, 'go_run', side_effect=go))
        original_materials = fixtures.material_fixture
        def production_fixture(root):
            _, packages = original_materials(root)
            source = root / materials.template_name(VERSION)
            with zipfile.ZipFile(source) as archive:
                entries = {name: archive.read(name) for name in contract.zip_files(archive)}
            rewrite_fixture_zip(source, production_source_files(root, entries, self.application))
            record = contract.read_json(root / 'materials.json')
            record['files'][source.name] = contract.digest(source)
            contract.write_json(root / 'materials.json', record)
            return contract.digest(root / 'materials.json'), packages
        self.artifact = self.root / 'artifact'
        with patch.object(fixtures, 'material_fixture', side_effect=production_fixture):
            seal = fixtures.release_fixture(self.artifact)
        contract.seal_or_verify(seal)
        self.layout = self.checkout / 'build/release-local/derived'
        self.args = argparse.Namespace(artifact=str(self.artifact), output=str(self.layout),
            candidate=str(self.layout), version=VERSION, commit=COMMIT,
            artifact_sha=contract.digest(self.artifact / 'artifact.json'), materials_sha=seal.materials_sha)

    def test_full_verify_prepare_reverify_preserves_source_and_product_binding(self):
        before = contract.tree(self.artifact)
        acceptance.prepare(self.args)
        acceptance.verify_layout(self.args)
        self.assertEqual(contract.tree(self.artifact), before)
        record = contract.read_json(self.layout / 'build-info.json')
        self.assertEqual(record['source_artifact_manifest_sha256'], self.args.artifact_sha)
        self.assertEqual(record['source_materials_sha256'], self.args.materials_sha)
        self.assertEqual(record['go_versions'], {target: 'go1.27.3' for target in contract.TARGETS})
        self.assertFalse(record['database_connected'])
        self.assertFalse(record['product_started'])

    def test_wrong_external_artifact_hash_fails_before_output_creation(self):
        self.args.artifact_sha = '0' * 64
        with self.assertRaisesRegex(contract.Invalid, 'manifest hash'):
            acceptance.prepare(self.args)
        self.assertFalse(self.layout.exists())

    def test_wrong_version_commit_or_material_hash_cannot_derive_layout(self):
        for field, bad in [('version', 'v0.1.0-rc.2'), ('commit', 'b' * 40), ('materials_sha', 'c' * 64)]:
            args = argparse.Namespace(**{**vars(self.args), field: bad})
            with self.subTest(field=field), self.assertRaises(contract.Invalid):
                acceptance.prepare(args)
        self.assertFalse(self.layout.exists())

    def test_changed_checkout_source_fails_before_layout_creation(self):
        (self.checkout / 'internal/fixture.go').write_bytes(b'changed source')
        with self.assertRaisesRegex(contract.Invalid, 'committed application'):
            acceptance.prepare(self.args)
        self.assertFalse(self.layout.exists())

    def test_old_toolchain_and_wrong_vcs_identity_are_rejected(self):
        original = contract.go_run
        for old in (True, False):
            def changed(*args):
                result = original(*args)
                if old and args == ('env', 'GOVERSION'):
                    return 'go1.27.0'
                if not old and args[:2] == ('version', '-m'):
                    return result.replace('vcs.modified=false', 'vcs.modified=true')
                return result
            with self.subTest(old=old), patch.object(contract, 'go_run', side_effect=changed), \
                 self.assertRaises(contract.Invalid):
                acceptance.prepare(self.args)

    def test_source_tampering_cannot_be_hidden_by_rehashing_derived_manifests(self):
        acceptance.prepare(self.args)
        (self.layout / 'source/internal/fixture.go').write_bytes(b'changed source')
        refresh_layout_fixture(self.layout)
        with self.assertRaisesRegex(contract.Invalid, 'approved source ZIP'):
            acceptance.verify_layout(self.args)

    def test_program_tampering_cannot_be_hidden_by_rehashing_derived_manifest(self):
        acceptance.prepare(self.args)
        (self.layout / 'bin/octopus-roam-windows-amd64.exe').write_bytes(b'changed program')
        refresh_layout_fixture(self.layout)
        with self.assertRaisesRegex(contract.Invalid, 'approved artifact'):
            acceptance.verify_layout(self.args)

    def test_unexpected_layout_file_is_rejected_even_with_updated_manifest(self):
        acceptance.prepare(self.args)
        (self.layout / 'unexpected.txt').write_bytes(b'fixture')
        refresh_layout_fixture(self.layout)
        with self.assertRaisesRegex(contract.Invalid, 'Unexpected derived'):
            acceptance.verify_layout(self.args)

    def test_existing_or_overlapping_output_is_rejected(self):
        for output in (self.artifact, self.artifact / 'derived', self.checkout, self.root / 'elsewhere'):
            args = argparse.Namespace(**{**vars(self.args), 'output': str(output)})
            with self.subTest(output=output.name), self.assertRaises(contract.Invalid):
                acceptance.prepare(args)

    def test_portable_paths_reject_aliases_and_case_collisions(self):
        for paths in (['../outside'], ['internal/Name.go', 'internal/name.go'], ['internal/name.'], ['internal/NUL.txt']):
            with self.subTest(paths=paths), self.assertRaises(contract.Invalid):
                acceptance.portable_files(paths)


class EntryPointTests(unittest.TestCase):
    def setUp(self):
        self.root = fixtures.new_directory()
        self.output = REPOSITORY / 'build/release-local' / ('entry-test-' + uuid.uuid4().hex)
        self.pwsh = shutil.which('pwsh')
        self.assertIsNotNone(self.pwsh, 'PowerShell 7 is required to check the acceptance entry')
        self.script = REPOSITORY / 'scripts/prepare-database-acceptance.ps1'

    def test_powershell_entry_parses_without_execution(self):
        command = "$tokens = $null; $errors = $null; [void][System.Management.Automation.Language.Parser]::ParseFile($args[0], [ref]$tokens, [ref]$errors); if ($errors.Count) { $errors | ForEach-Object { $_.Message }; exit 1 }"
        # -CommandWithArgs passes a literal path instead of interpolating shell text.
        result = subprocess.run([self.pwsh, '-NoProfile', '-CommandWithArgs', command, str(self.script)],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)

    def test_legacy_input_validation_does_not_build_or_start_a_program(self):
        source = self.root / 'source/scripts/windows'
        source.mkdir(parents=True)
        helper = (REPOSITORY / 'scripts/windows/send-stop-signal.ps1').read_bytes()
        (source / 'send-stop-signal.ps1').write_bytes(helper)
        (self.root / 'source-input-sha256sums.txt').write_text(
            contract.sha(helper) + '  scripts/windows/send-stop-signal.ps1\n')
        (self.root / 'bin').mkdir()
        binary = self.root / 'bin/octopus-roam-windows-amd64.exe'
        binary.write_bytes(b'synthetic non-executable input')
        (self.root / 'file-sha256sums.txt').write_text(contract.digest(binary) + '  bin/octopus-roam-windows-amd64.exe\n')
        contract.write_json(self.root / 'build-info.json', {'version': 'v0.1.0-local.fixture'})
        result = subprocess.run([self.pwsh, '-NoProfile', '-File', str(self.script),
            '-Candidate', str(self.root), '-OutputRoot', str(self.output), '-ValidateOnly'],
            capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        record = json.loads(result.stdout)
        self.assertEqual(record['status'], 'inputs_verified')
        self.assertFalse(record['driver_built'])
        self.assertFalse(record['product_started'])
        self.assertFalse(self.output.exists())

    def test_derived_powershell_entry_requires_explicit_identity_before_startup(self):
        contract.write_json(self.root / 'build-info.json', {'role': acceptance.ROLE, 'schema': 2, 'version': VERSION})
        result = subprocess.run([self.pwsh, '-NoProfile', '-File', str(self.script),
            '-Candidate', str(self.root), '-OutputRoot', str(self.output), '-ValidateOnly'],
            capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('explicitly reviewed', result.stderr)
        self.assertFalse(self.output.exists())

    def test_raw_actions_layout_is_rejected_with_adapter_instruction(self):
        contract.write_json(self.root / 'build.json', {'schema': 2})
        result = subprocess.run([self.pwsh, '-NoProfile', '-File', str(self.script),
            '-Candidate', str(self.root), '-OutputRoot', str(self.output), '-ValidateOnly'],
            capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('release_acceptance.py prepare', result.stderr)
        self.assertFalse(self.output.exists())

    def test_update_entry_requires_identity_before_opening_fixture_server(self):
        contract.write_json(self.root / 'build-info.json', {'role': acceptance.ROLE, 'schema': 2})
        result = subprocess.run(['node', str(REPOSITORY / 'scripts/test-release-acceptance.mjs'),
            str(self.root), str(self.output)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('explicit reviewed identity', result.stderr)
        self.assertFalse(self.output.exists())


if __name__ == '__main__':
    unittest.main()
