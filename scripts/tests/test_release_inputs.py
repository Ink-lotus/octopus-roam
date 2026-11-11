"""Reviewed Git blobs and release handoffs; isolated fixtures, no network/product/DB."""
from contextlib import chdir
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
import release_contract as contract
import release_materials as materials
import test_release_contract as fixtures
from test_release_go_metadata import refresh_delivery_and_manifest

REPOSITORY = Path(__file__).resolve().parents[2]
LEGACY_NOTES = 'plan/release-notes-v0.1.0.md'


class CommittedInputsTests(unittest.TestCase):
    def setUp(self):
        self.root = fixtures.new_directory()
        self.enterContext(patch.dict(os.environ, {
            'GIT_CONFIG_GLOBAL': os.devnull, 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_COUNT': '0',
            'GIT_AUTHOR_NAME': 'W10 Fixture', 'GIT_COMMITTER_NAME': 'W10 Fixture',
            'GIT_AUTHOR_EMAIL': 'w10-fixture@example.invalid',
            'GIT_COMMITTER_EMAIL': 'w10-fixture@example.invalid',
        }))
        for name in ('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_OBJECT_DIRECTORY',
                     'GIT_ALTERNATE_OBJECT_DIRECTORIES', 'RELEASE_NOTES_PATH', 'RELEASE_NOTES_SHA256'):
            os.environ.pop(name, None)

    def git(self, root, *args):
        return subprocess.check_output(['git', '-C', str(root), '-c', 'core.autocrlf=false',
            '-c', 'core.excludesFile=', '-c', 'commit.gpgsign=false',
            '-c', 'core.hooksPath=' + str(root / 'disabled-hooks'), *args],
            stderr=subprocess.PIPE, encoding='utf-8').strip()

    def commit_files(self, root, files):
        for name, content in files.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
        self.git(root, 'add', '--', *files)
        self.git(root, 'commit', '-m', 'W10 合成发布输入')
        return self.git(root, 'rev-parse', 'HEAD')

    def repository(self, name, version='v0.1.2', main_version=None, frontend_version=None,
                   notes=None, notes_path=contract.RELEASE_NOTES_PATH):
        root = self.root / name
        root.mkdir()
        self.git(root, 'init', '-b', 'main')
        raw = notes if notes is not None else f'# Octopus Roam {version}\r\n合成已审查说明。\r\n\r\n'.encode()
        files = {'main.go': f'package main\n// Version {main_version or version}\nfunc main() {{}}\n'.encode(),
                 'web/package.json': json.dumps({'version': frontend_version or version.removeprefix('v')}).encode()}
        if notes_path:
            files[notes_path] = raw
        return root, self.commit_files(root, files), raw

    def test_stable_and_rc_read_exact_blob_bytes(self):
        for index, version in enumerate(('v0.1.2', 'v0.1.2-rc.1', 'v0.1.2-rc.123')):
            root, commit, raw = self.repository(str(index), version)
            with self.subTest(version=version), chdir(root):
                self.assertEqual(contract.read_release_notes(version, commit), raw)
                self.assertEqual(materials.target(version, commit), int(self.git(root, 'show', '-s', '--format=%ct', commit)))
                self.assertEqual(contract.notes_binding(raw), {
                    'release_notes_path': contract.RELEASE_NOTES_PATH,
                    'release_notes_sha256': contract.sha(raw)})

    def test_worktree_and_legacy_notes_cannot_replace_the_committed_blob(self):
        root, commit, raw = self.repository('dirty')
        (root / contract.RELEASE_NOTES_PATH).write_bytes(b'# Unreviewed workspace replacement\n')
        legacy = root / LEGACY_NOTES
        legacy.parent.mkdir()
        legacy.write_bytes(b'# Unreviewed legacy replacement\n')
        with chdir(root):
            self.assertEqual(contract.read_release_notes('v0.1.2', commit), raw)
            with self.assertRaisesRegex(contract.Invalid, 'unreviewed'):
                materials.target('v0.1.2', commit)

    def test_legacy_only_or_absent_notes_cannot_be_used(self):
        for index, location in enumerate((LEGACY_NOTES, None)):
            root, commit, _ = self.repository(str(index), notes_path=location)
            with self.subTest(location=location), chdir(root), self.assertRaises(subprocess.CalledProcessError):
                contract.read_release_notes('v0.1.2', commit)

    def test_main_frontend_and_title_must_name_the_exact_version(self):
        cases = [({'main_version': 'v0.1.0'}, 'main.go version'),
                 ({'frontend_version': '0.1.0'}, 'Frontend package version'),
                 ({'frontend_version': 'v0.1.2'}, 'Frontend package version'),
                 ({'notes': b'# Octopus Roam v0.1.2-rc.1\nReviewed\n'}, 'title'),
                 ({'notes': b''}, 'empty'),
                 ({'notes': '# Octopus Roam v0.1.2\n待发布\n'.encode()}, 'need review')]
        for index, (options, message) in enumerate(cases):
            root, commit, _ = self.repository(str(index), **options)
            with self.subTest(options=options), chdir(root), self.assertRaisesRegex(contract.Invalid, message):
                contract.read_release_notes('v0.1.2', commit)

    def test_unknown_sha_tree_object_and_short_sha_are_rejected(self):
        root, commit, _ = self.repository('identity')
        tree = self.git(root, 'rev-parse', commit + '^{tree}')
        for value in ('0' * 40, tree, commit[:7]):
            with self.subTest(commit=value), chdir(root), self.assertRaises((contract.Invalid, subprocess.CalledProcessError)):
                contract.read_release_notes('v0.1.2', value)

    def test_rc_to_stable_requires_new_commit_with_all_three_inputs(self):
        root, candidate, _ = self.repository('promotion', 'v0.1.2-rc.1')
        with chdir(root):
            with self.assertRaisesRegex(contract.Invalid, 'main.go version'):
                contract.read_release_notes('v0.1.2', candidate)
            metadata_only = self.commit_files(root, {
                'main.go': b'package main\n// Version v0.1.2\nfunc main() {}\n',
                'web/package.json': b'{"version":"0.1.2"}\n'})
            with self.assertRaisesRegex(contract.Invalid, 'title'):
                contract.read_release_notes('v0.1.2', metadata_only)
            notes = b'# Octopus Roam v0.1.2\nReviewed stable fixture\n'
            stable = self.commit_files(root, {contract.RELEASE_NOTES_PATH: notes})
            self.assertNotEqual(stable, candidate)
            self.assertEqual(contract.read_release_notes('v0.1.2', stable), notes)
            with self.assertRaisesRegex(contract.Invalid, 'main.go version'):
                contract.read_release_notes('v0.1.2-rc.1', stable)

    def test_prepared_path_and_hash_are_checked_against_the_blob(self):
        root, commit, raw = self.repository('prepared')
        good = {'RELEASE_NOTES_PATH': contract.RELEASE_NOTES_PATH, 'RELEASE_NOTES_SHA256': contract.sha(raw)}
        with chdir(root), patch.dict(os.environ, good):
            self.assertEqual(contract.read_release_notes('v0.1.2', commit), raw)
            for name, value in (('RELEASE_NOTES_PATH', LEGACY_NOTES), ('RELEASE_NOTES_SHA256', '0' * 64)):
                with self.subTest(name=name), patch.dict(os.environ, {name: value}), \
                     self.assertRaisesRegex(contract.Invalid, 'Prepared Release Notes binding'):
                    contract.read_release_notes('v0.1.2', commit)


class NotesHandoffTests(unittest.TestCase):
    def setUp(self):
        self.root = fixtures.new_directory()
        self.enterContext(patch.dict(os.environ, {'GITHUB_REPOSITORY': materials.REPOSITORY}))
        self.enterContext(patch.object(contract, 'read_release_notes', side_effect=fixtures.fixture_notes))
        self.enterContext(patch.object(contract, 'go_run', side_effect=fixtures.fixture_go))

    def test_all_manifests_and_delivery_use_the_same_notes(self):
        args = fixtures.release_fixture(self.root / 'artifact')
        contract.seal_or_verify(args)
        root = Path(args.output)
        for name in ('materials/materials.json', 'build.json', 'artifact.json'):
            record = contract.read_json(root / name)
            for key, value in contract.notes_binding(fixtures.NOTES).items():
                self.assertEqual(record[key], value, name)
        self.assertEqual((root / 'delivery/RELEASE-NOTES.md').read_bytes(), fixtures.NOTES)
        args.command = 'verify'
        contract.seal_or_verify(args)

    def test_missing_old_path_or_wrong_hash_is_rejected_in_each_manifest(self):
        mutations = [('release_notes_path', LEGACY_NOTES), ('release_notes_sha256', '0' * 64),
                     ('release_notes_path', None), ('release_notes_sha256', None)]
        for phase in ('materials', 'build', 'artifact'):
            for index, (key, value) in enumerate(mutations):
                args = fixtures.release_fixture(self.root / (phase + str(index)))
                root = Path(args.output)
                if phase == 'artifact':
                    contract.seal_or_verify(args)
                    args.command = 'verify'
                path = root / {'materials': 'materials/materials.json', 'build': 'build.json', 'artifact': 'artifact.json'}[phase]
                record = contract.read_json(path)
                if value is None:
                    record.pop(key)
                else:
                    record[key] = value
                contract.write_json(path, record)
                if phase == 'materials':
                    args.materials_sha = contract.digest(path)
                with self.subTest(phase=phase, key=key, value=value), self.assertRaises(contract.Invalid):
                    contract.seal_or_verify(args)

    def test_rehashing_public_notes_or_materials_does_not_authorize_replacement(self):
        for kind in ('notes', 'materials'):
            args = fixtures.release_fixture(self.root / kind)
            contract.seal_or_verify(args)
            root = Path(args.output)
            if kind == 'notes':
                (root / 'delivery/RELEASE-NOTES.md').write_bytes(fixtures.NOTES + b'unreviewed text\n')
                message = 'Reviewed Release Notes differ'
            else:
                path = root / 'delivery/MATERIALS.json'
                record = contract.read_json(path)
                record['release_notes_sha256'] = '0' * 64
                contract.write_json(path, record)
                message = 'Published material record differs'
            refresh_delivery_and_manifest(root)
            args.command = 'verify'
            with self.subTest(kind=kind), self.assertRaisesRegex(contract.Invalid, message):
                contract.seal_or_verify(args)

    def test_corresponding_source_cannot_hide_changed_notes_behind_new_hashes(self):
        root = self.root / 'materials'
        fixtures.material_fixture(root)
        source = root / materials.template_name(fixtures.VERSION)
        with zipfile.ZipFile(source) as archive:
            entries = {name: archive.read(name) for name in contract.zip_files(archive) if name != 'SHA256SUMS'}
        entries['application/' + contract.RELEASE_NOTES_PATH] = fixtures.NOTES + b'unreviewed source text\n'
        entries['SHA256SUMS'] = ''.join(f'{contract.sha(raw)}  {name}\n' for name, raw in sorted(entries.items())).encode()
        with zipfile.ZipFile(source, 'w') as archive:
            for name, raw in entries.items():
                archive.writestr(name, raw)
        manifest = contract.read_json(root / 'materials.json')
        manifest['files'][source.name] = contract.digest(source)
        contract.write_json(root / 'materials.json', manifest)
        with self.assertRaisesRegex(contract.Invalid, 'Corresponding Release Notes'):
            contract.materials(root, fixtures.VERSION, fixtures.COMMIT, contract.digest(root / 'materials.json'))


class EntryVersionTests(unittest.TestCase):
    def setUp(self):
        self.root = fixtures.new_directory()
        self.output = REPOSITORY / 'build/release-local' / ('w10-version-' + uuid.uuid4().hex)
        contract.write_json(self.root / 'build-info.json', {'schema': 2, 'role': 'derived-acceptance-layout'})
        self.versions = [('v0.1.2', True), ('v0.1.2-rc.12', True), ('v0.1.0', False), ('v0.1.1', False),
                         ('v0.1.2-rc.0', False), ('v0.1.2-rc.01', False), ('v0.1.3', False),
                         ('V0.1.2', False), ('v0.1.2\n', False)]

    def test_image_entry_accepts_only_target_versions_before_any_docker_action(self):
        environment = {**os.environ, 'COMMIT': '0' * 40}
        environment.pop('GITHUB_REPOSITORY', None)
        for version, accepted in self.versions:
            result = subprocess.run(['bash', str(REPOSITORY / 'scripts/release-images.sh'), 'build', str(self.output)],
                env={**environment, 'VERSION': version}, capture_output=True, text=True)
            with self.subTest(version=version):
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual('GITHUB_REPOSITORY is required' in result.stderr, accepted, result.stderr)
                self.assertFalse(self.output.exists())

    def test_node_entry_passes_valid_versions_to_artifact_verification_only(self):
        for version, accepted in self.versions:
            result = subprocess.run(['node', str(REPOSITORY / 'scripts/test-release-acceptance.mjs'),
                str(self.root), str(self.output), version, '0' * 40, 'a' * 64, 'b' * 64],
                cwd=REPOSITORY, capture_output=True, text=True)
            with self.subTest(version=version):
                self.assertNotEqual(result.returncode, 0)
                message = 'Artifact layout verification failed' if accepted else 'explicit reviewed identity'
                self.assertIn(message, result.stderr)
                self.assertFalse(self.output.exists())

    def test_powershell_entry_passes_valid_versions_to_artifact_verification_only(self):
        for version, accepted in self.versions:
            result = subprocess.run([shutil.which('pwsh'), '-NoProfile', '-File',
                str(REPOSITORY / 'scripts/prepare-database-acceptance.ps1'),
                '-Candidate', str(self.root), '-OutputRoot', str(self.output), '-ExpectedVersion', version,
                '-ExpectedCommit', '0' * 40, '-ArtifactSha256', 'a' * 64, '-MaterialsSha256', 'b' * 64,
                '-ValidateOnly'], cwd=REPOSITORY, capture_output=True, text=True)
            with self.subTest(version=version):
                self.assertNotEqual(result.returncode, 0)
                message = 'Artifact-derived candidate verification failed' if accepted else 'explicitly reviewed'
                self.assertIn(message, result.stderr)
                self.assertFalse(self.output.exists())


if __name__ == '__main__':
    unittest.main()
