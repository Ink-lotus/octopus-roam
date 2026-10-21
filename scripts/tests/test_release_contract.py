"""Synthetic release fixtures: no network, application startup or database access."""
import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
import urllib.error
from unittest.mock import patch
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import release_contract as contract

VERSION = 'v0.1.0-rc.1'
COMMIT = 'a' * 40


def new_directory():
    # Keep evidence; no recursive cleanup of local test directories.
    return Path(tempfile.mkdtemp(prefix='release-contract-', dir=os.environ.get('RELEASE_TEST_ROOT')))


def zip_write(path, files):
    path.parent.mkdir(parents=True, exist_ok=True)
    contract.archive_write(path, {n: (data, 0o644) for n, data in files.items()})


def tar_bytes(files):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w') as archive:
        for name, content in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(content)
            info.mode = 0o644
            if name in ('entrypoint.sh', 'app/octopus-roam'):
                info.mode = 0o755
            archive.addfile(info, io.BytesIO(content))
    return output.getvalue()


def os_source(path, arch):
    package = {'name': 'fixture-package', 'version': '1.0-r0', 'architecture': contract.LINUX[arch],
               'origin': 'fixture-origin', 'aports_commit': 'b' * 40, 'license': 'MIT'}
    raw = (f"P:{package['name']}\np:alias-name\nV:{package['version']}\nA:{package['architecture']}\n"
           f"o:{package['origin']}\nc:{package['aports_commit']}\nL:MIT\nF:usr/bin\na:0:0:755\nR:fixture\n\n").encode()
    upstream = tar_bytes({'upstream/LICENSE': b'synthetic notice'})
    recipe = b'# synthetic APKBUILD\n'
    files = {'ALPINE-PACKAGES.json': json.dumps({'packages': [package]}).encode(),
             'SOURCE-BINDINGS.json': json.dumps([{'origin': package['origin'], 'aports_commit': package['aports_commit'],
                 'recipe_path': 'aports/APKBUILD', 'inputs': [{'path': 'distfiles/source.tar',
                 'sha512': hashlib.sha512(upstream).hexdigest()}]}]).encode(),
             'GITMODES.json': json.dumps([{'path': 'aports/APKBUILD', 'git_blob_sha1':
                 hashlib.sha1(b'blob ' + str(len(recipe)).encode() + b'\0' + recipe).hexdigest()}]).encode(),
             'NOTICE-INDEX.json': json.dumps({'original_notices': [{'path': 'notices/LICENSE',
                 'sha256': contract.sha(b'synthetic notice')}]}).encode(),
             'SOURCE-ARCHIVE-FILES.json': json.dumps({'distfiles/source.tar': [
                 {'path': 'upstream/LICENSE', 'sha256': contract.sha(b'synthetic notice')}]}).encode(),
             'aports/APKBUILD': recipe, 'distfiles/source.tar': upstream, 'notices/LICENSE': b'synthetic notice',
             'README.md': b'synthetic fixture only', 'REBUILD.md': b'synthetic build instructions', 'NOTICES.md': b'synthetic notices'}
    files['FILE-SHA256SUMS.json'] = json.dumps([{'path': n, 'bytes': len(b), 'sha256': contract.sha(b)}
                                               for n, b in sorted(files.items())]).encode()
    zip_write(path, files)
    return raw, files


def material_fixture(root):
    root.mkdir(parents=True)
    docs = {name: b'synthetic material document' for name in contract.DOCS}
    report = b'ecosystem,name,version,license\ngo,fixture,1.0,MIT\n'
    source = {**docs, 'release-source.json': json.dumps({'schema': 1, 'version': VERSION, 'commit': COMMIT,
               'platforms': list(contract.TARGETS)}).encode(),
              'go-cache/module.zip': b'synthetic module bytes', 'npm-packages/fixture/source.js': b'synthetic source',
              'licenses/THIRD_PARTY_LICENSES.csv': report}
    for name in ('AGPL-3.0.txt', 'LGPL-3.0.txt', 'GPL-3.0.txt', 'MPL-2.0.txt'):
        source['licenses/' + name] = b'synthetic fixture text'
    source['SHA256SUMS'] = ''.join(f'{contract.sha(b)}  {n}\n' for n, b in sorted(source.items())).encode()
    zip_write(root / f'{contract.APP}-source-{VERSION}.zip', source)
    (root / 'THIRD_PARTY_LICENSES.csv').write_bytes(report)
    for name, content in docs.items():
        (root / name).write_bytes(content)
    raw_packages = {}
    for arch in contract.LINUX:
        raw_packages[arch] = os_source(root / f'alpine/{arch}.zip', arch)
    data = {'schema': 1, 'version': VERSION, 'commit': COMMIT, 'platforms': list(contract.TARGETS),
            'source_date_epoch': 1, 'alpine': {arch: {'base_image': 'alpine@sha256:' + 'c' * 64}
                                             for arch in contract.LINUX}, 'files': contract.tree(root)}
    contract.write_json(root / 'materials.json', data)
    return contract.digest(root / 'materials.json'), raw_packages


def image_fixture(path, arch, files, extra_layers=()):
    layers = [tar_bytes(files), *extra_layers]
    config = {'os': 'linux', 'architecture': arch, 'config': {'Labels': {
        'org.opencontainers.image.version': VERSION, 'org.opencontainers.image.revision': COMMIT}},
        'rootfs': {'diff_ids': ['sha256:' + contract.sha(raw) for raw in layers]}}
    if arch == 'arm':
        config['variant'] = 'v7'
    raw = json.dumps(config).encode()
    name = contract.sha(raw) + '.json'
    manifest = [{'Config': name, 'Layers': [f'{i}/layer.tar' for i in range(len(layers))],
                 'RepoTags': [f'ghcr.io/ink-lotus/octopus-roam:{VERSION}-{arch}']}]
    entries = {name: raw, 'manifest.json': json.dumps(manifest).encode()}
    entries.update({f'{i}/layer.tar': b for i, b in enumerate(layers)})
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(tar_bytes(entries))


def release_fixture(root):
    materials_sha, packages = material_fixture(root / 'materials')
    for name in ('archives', 'bin', 'docker-context/build/release-notices', 'docker-context/scripts/dockerfile'):
        (root / name).mkdir(parents=True, exist_ok=True)
    report = (root / 'materials/THIRD_PARTY_LICENSES.csv').read_bytes()
    (root / 'THIRD_PARTY_LICENSES.csv').write_bytes(report)
    (root / 'docker-context/LICENSE').write_bytes(b'synthetic app license')
    (root / 'docker-context/scripts/dockerfile/entrypoint.sh').write_bytes(b'synthetic entrypoint')
    (root / 'docker-context/build/release-notices/SOURCE-OFFER.md').write_bytes(b'synthetic offer')
    binaries = {}
    for target in contract.TARGETS:
        executable = contract.APP + ('.exe' if target.startswith('windows-') else '')
        raw = ('synthetic ' + target).encode()
        (root / 'bin' / (contract.APP + '-' + target + ('.exe' if target.startswith('windows-') else ''))).write_bytes(raw)
        binaries[target] = contract.sha(raw)
        entries = {executable: (raw, 0o755), 'LICENSE': (b'synthetic app license', 0o644),
                   'README.md': (b'synthetic README', 0o644), 'README_zh.md': (b'synthetic README', 0o644),
                   'THIRD_PARTY_LICENSES.csv': (report, 0o644)}
        if target.startswith('windows-'):
            entries[contract.APP + '.cmd'] = (b'synthetic helper', 0o644)
            for n in (f'{contract.APP}-menu.ps1', f'{contract.APP}-start.ps1', f'{contract.APP}-autostart.ps1', 'send-stop-signal.ps1'):
                entries['windows/' + n] = (b'synthetic helper', 0o644)
        contract.archive_write(root / 'archives' / f'{contract.APP}-{target}.zip', entries)
    (root / 'archives/SHA256SUMS').write_text(''.join(f'{contract.digest(p)}  {p.name}\n'
        for p in sorted((root / 'archives').glob('*.zip'))), encoding='utf-8')
    contract.write_json(root / 'build.json', {'schema': 1, 'version': VERSION, 'commit': COMMIT,
        'platforms': list(contract.TARGETS), 'binaries': binaries})
    for arch, (raw, os_files) in packages.items():
        platform = 'linux/arm/v7' if arch == 'arm' else f'linux/{arch}'
        alpine = root / 'docker-context/build/alpine' / platform
        alpine.mkdir(parents=True)
        (alpine / 'ALPINE-PACKAGES.json').write_bytes(os_files['ALPINE-PACKAGES.json'])
        files = {'lib/apk/db/installed': raw, 'app/' + contract.APP: (root / 'bin' / f'{contract.APP}-linux-{arch}').read_bytes(),
                 'app/LICENSE': b'synthetic app license', 'app/THIRD_PARTY_LICENSES.csv': report,
                 'entrypoint.sh': b'synthetic entrypoint', 'app/SOURCE-OFFER.md': b'synthetic offer',
                 'app/alpine/ALPINE-PACKAGES.json': os_files['ALPINE-PACKAGES.json']}
        image_fixture(root / f'images/{arch}.tar', arch, files)
    return argparse.Namespace(command='seal', output=str(root), version=VERSION, commit=COMMIT, materials_sha=materials_sha)


class ContractTests(unittest.TestCase):
    def setUp(self):
        self.root = new_directory()
        self.env = patch.dict(os.environ, {'GITHUB_REPOSITORY': 'Ink-lotus/octopus-roam'})
        self.env.start()

    def tearDown(self):
        self.env.stop()

    def test_identity_accepts_only_full_sha_and_first_release(self):
        contract.identity(VERSION, COMMIT)
        contract.identity('v0.1.0', COMMIT)
        for version, commit in [('dev', COMMIT), ('v0.13.6', COMMIT), ('v0.1.0-local.x', COMMIT),
                                ('v0.1.0-rc.0', COMMIT), (VERSION, 'abcdef1')]:
            with self.subTest(version=version, commit=commit), self.assertRaises(contract.Invalid):
                contract.identity(version, commit)

    def test_notes_version_must_match_exactly(self):
        for title, version in [('# Release v0.1.0-rc.10', VERSION), ('# Release v0.1.0-rc.1', 'v0.1.0')]:
            with self.assertRaises(contract.Invalid):
                contract.reviewed_notes(title, version)

    def test_remote_preflight_accepts_only_absent_objects(self):
        args = argparse.Namespace(version=VERSION, commit=COMMIT)
        def missing():
            return urllib.error.HTTPError('https://fixture.invalid', 404, 'synthetic absent', {}, None)
        responses = [missing(), missing(), io.BytesIO(b'{"token":"synthetic-registry-token"}'),
                     *(missing() for _ in range(5))]
        with patch.dict(os.environ, {'GITHUB_ACTOR': 'fixture', 'GH_TOKEN': 'synthetic-credential'}), \
             patch.object(contract.urllib.request, 'urlopen', side_effect=responses) as request:
            contract.remote_check(args)
            self.assertEqual(request.call_count, 8)

    def test_remote_preflight_rejects_existing_tag(self):
        args = argparse.Namespace(version=VERSION, commit=COMMIT)
        with patch.dict(os.environ, {'GH_TOKEN': 'synthetic-credential'}), \
             patch.object(contract.urllib.request, 'urlopen', return_value=io.BytesIO(b'')), \
             self.assertRaises(contract.Invalid):
            contract.remote_check(args)

    def test_remote_preflight_rejects_auth_or_network_failure(self):
        args = argparse.Namespace(version=VERSION, commit=COMMIT)
        for status in (401, 403, 503):
            error = urllib.error.HTTPError('https://fixture.invalid', status, 'synthetic failure', {}, None)
            with patch.dict(os.environ, {'GH_TOKEN': 'synthetic-credential'}), \
                 patch.object(contract.urllib.request, 'urlopen', side_effect=error), self.assertRaises(contract.Invalid):
                contract.remote_check(args)

    def test_frontend_preserves_tracked_readme_on_build_failure(self):
        (self.root / 'static/out').mkdir(parents=True)
        sentinel = self.root / 'static/out/README.md'
        sentinel.write_bytes(b'original sentinel\r\n')
        previous = Path.cwd()
        os.chdir(self.root)
        try:
            def failed_build(*args, **kwargs):
                sentinel.write_bytes(b'synthetic build replacement')
                self.assertEqual(kwargs['env']['VITE_APP_VERSION'], VERSION)
                raise subprocess.CalledProcessError(1, 'pnpm')
            with patch.object(contract.subprocess, 'run', side_effect=failed_build), self.assertRaises(subprocess.CalledProcessError):
                contract.frontend(argparse.Namespace(version=VERSION))
            self.assertEqual(sentinel.read_bytes(), b'original sentinel\r\n')
        finally:
            os.chdir(previous)

    def test_dependency_replacement_source_and_h1(self):
        module, version = 'example.org/Fixture', 'v1.0.0'
        name = f'{module}@{version}/LICENSE'
        data = b'synthetic module source'
        output = io.BytesIO()
        with zipfile.ZipFile(output, 'w') as archive:
            archive.writestr(name, data)
        expected = 'h1:' + base64.b64encode(hashlib.sha256(f'{contract.sha(data)}  {name}\n'.encode()).digest()).decode()
        source = self.root / 'source.zip'
        zip_write(source, {'application/go.sum': f'{module} {version} {expected}\n'.encode(),
                           'go-cache/cache/download/example.org/!fixture/@v/v1.0.0.zip': output.getvalue()})
        info = f'\tdep\texample.org/original\tv0.1.0\n\t=>\t{module}\t{version}\t{expected}\n'
        self.assertEqual(contract.dependency_sources(info, source, {}), 1)
        with self.assertRaises(contract.Invalid):
            contract.dependency_sources(info.replace(expected, 'h1:unexpected'), source, {})

    def test_apk_keys_are_case_sensitive(self):
        raw, _ = os_source(self.root / 'alpine.zip', 'amd64')
        packages = contract.apk_inventory(raw)
        self.assertEqual(packages[0]['name'], 'fixture-package')
        self.assertEqual(packages[0]['architecture'], 'x86_64')

    def test_apk_missing_or_duplicate_headers_fail(self):
        for raw in (b'', b'P:one\nP:two\n', b'P:one\np:alias\n'):
            with self.assertRaises(contract.Invalid):
                contract.apk_inventory(raw)

    def test_materials_valid_all_platforms(self):
        expected, _ = material_fixture(self.root / 'materials')
        contract.materials(self.root / 'materials', VERSION, COMMIT, expected)

    def test_stale_materials_fail(self):
        expected, _ = material_fixture(self.root / 'materials')
        with self.assertRaises(contract.Invalid):
            contract.materials(self.root / 'materials', 'v0.1.0-rc.2', COMMIT, expected)

    def test_material_root_hash_fail(self):
        material_fixture(self.root / 'materials')
        with self.assertRaises(contract.Invalid):
            contract.materials(self.root / 'materials', VERSION, COMMIT, 'd' * 64)

    def test_material_extra_file_fail(self):
        expected, _ = material_fixture(self.root / 'materials')
        (self.root / 'materials/extra.txt').write_text('unexpected')
        with self.assertRaises(contract.Invalid):
            contract.materials(self.root / 'materials', VERSION, COMMIT, expected)

    def test_material_changed_report_fail(self):
        expected, _ = material_fixture(self.root / 'materials')
        (self.root / 'materials/THIRD_PARTY_LICENSES.csv').write_bytes(b'')
        with self.assertRaises(contract.Invalid):
            contract.materials(self.root / 'materials', VERSION, COMMIT, expected)

    def test_os_architecture_cannot_be_reused(self):
        os_source(self.root / 'alpine.zip', 'amd64')
        with self.assertRaises(contract.Invalid):
            contract.alpine_source(self.root / 'alpine.zip', 'arm64')

    def test_os_source_tampering_fails(self):
        _, files = os_source(self.root / 'original.zip', 'amd64')
        files['distfiles/source.tar'] = b'changed'
        zip_write(self.root / 'changed.zip', files)
        with self.assertRaises(contract.Invalid):
            contract.alpine_source(self.root / 'changed.zip', 'amd64')

    def test_zip_path_and_duplicate_entries_fail(self):
        for name in ('../outside', '/absolute', 'C:/outside', 'one\\two'):
            with self.assertRaises(contract.Invalid):
                contract.safe(name)
        path = self.root / 'duplicate.zip'
        with zipfile.ZipFile(path, 'w') as archive:
            archive.writestr('one', b'1')
            with self.assertWarns(UserWarning):
                archive.writestr('one', b'2')
        with zipfile.ZipFile(path) as archive, self.assertRaises(contract.Invalid):
            list(contract.zip_files(archive))

    def test_zip_symlink_fails(self):
        info = zipfile.ZipInfo('link')
        info.create_system = 3
        info.external_attr = (stat.S_IFLNK | 0o777) << 16
        path = self.root / 'link.zip'
        with zipfile.ZipFile(path, 'w') as archive:
            archive.writestr(info, b'fixture-target')
        with zipfile.ZipFile(path) as archive, self.assertRaises(contract.Invalid):
            list(contract.zip_files(archive))

    def test_image_payload_and_hashes(self):
        path = self.root / 'image.tar'
        image_fixture(path, 'amd64', {'app/payload': b'fixture'})
        self.assertEqual(contract.image_files(path, ['app/payload'], 'amd64', VERSION, COMMIT), {'app/payload': b'fixture'})
        raw = path.read_bytes().replace(b'fixture', b'changed', 1)
        (self.root / 'changed.tar').write_bytes(raw)
        with self.assertRaises(contract.Invalid):
            contract.image_files(self.root / 'changed.tar', ['app/payload'], 'amd64', VERSION, COMMIT)

    def test_image_whiteout_removes_required_file(self):
        path = self.root / 'image.tar'
        image_fixture(path, 'amd64', {'app/payload': b'fixture'}, [tar_bytes({'app/.wh.payload': b''})])
        with self.assertRaises(contract.Invalid):
            contract.image_files(path, ['app/payload'], 'amd64', VERSION, COMMIT)

    def test_wrong_image_version_and_arch_fail(self):
        path = self.root / 'image.tar'
        image_fixture(path, 'amd64', {'app/payload': b'fixture'})
        for arch, version in [('arm64', VERSION), ('amd64', 'v0.1.0-rc.2')]:
            with self.assertRaises(contract.Invalid):
                contract.image_files(path, ['app/payload'], arch, version, COMMIT)

    def test_full_release_can_be_sealed_and_reverified(self):
        args = release_fixture(self.root / 'artifact')
        with patch.object(contract.shutil, 'copyfile', wraps=contract.shutil.copyfile):
            contract.seal_or_verify(args)
        args.command = 'verify'
        contract.seal_or_verify(args)

    def test_extra_archive_fails_before_seal(self):
        args = release_fixture(self.root / 'artifact')
        (self.root / 'artifact/archives/unexpected.zip').write_bytes(b'fixture')
        with self.assertRaises(contract.Invalid):
            contract.seal_or_verify(args)
        self.assertFalse((self.root / 'artifact/artifact.json').exists())

    def test_seven_platform_subset_cannot_publish(self):
        args = release_fixture(self.root / 'artifact')
        build = contract.read_json(self.root / 'artifact/build.json')
        build['platforms'] = list(contract.TARGETS[:7])
        contract.write_json(self.root / 'artifact/build.json', build)
        with self.assertRaises(contract.Invalid):
            contract.seal_or_verify(args)

    def test_changed_actual_os_package_blocks_seal(self):
        args = release_fixture(self.root / 'artifact')
        with patch.object(contract, 'apk_inventory', return_value=[]), self.assertRaises(contract.Invalid):
            contract.seal_or_verify(args)

    def test_downloaded_extra_file_and_changed_payload_fail(self):
        args = release_fixture(self.root / 'artifact')
        contract.seal_or_verify(args)
        args.command = 'verify'
        (self.root / 'artifact/unexpected.txt').write_text('synthetic extra')
        with self.assertRaises(contract.Invalid):
            contract.seal_or_verify(args)

    def test_prepare_requires_main_and_reviewed_notes(self):
        args = argparse.Namespace(version=VERSION, commit=COMMIT, materials_run='123', materials_sha='d' * 64)
        values = [COMMIT, f'// Version {VERSION}', f'# Release {VERSION}\nReviewed notes', '']
        output = self.root / 'outputs'
        with patch.dict(os.environ, {'GITHUB_REF': 'refs/heads/main', 'GITHUB_OUTPUT': str(output)}), \
             patch.object(contract, 'run', side_effect=values), patch.object(contract.subprocess, 'run'):
            contract.prepare(args)
        self.assertIn('prerelease=true', output.read_text())
        with patch.dict(os.environ, {'GITHUB_REF': 'refs/heads/test'}), self.assertRaises(contract.Invalid):
            contract.prepare(args)
        for note in (f'# Release {VERSION}\n草稿', '# Release v0.1.0\nReviewed'):
            with patch.dict(os.environ, {'GITHUB_REF': 'refs/heads/main'}), \
                 patch.object(contract, 'run', side_effect=[COMMIT, f'// Version {VERSION}', note]), \
                 patch.object(contract.subprocess, 'run'), self.assertRaises(contract.Invalid):
                contract.prepare(args)


if __name__ == '__main__':
    unittest.main()
