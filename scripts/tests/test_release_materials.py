"""Material handoff fixtures; no real HTTP, checkout mutation, product or DB."""
import argparse
from contextlib import contextmanager
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import urllib.error
import urllib.request
import warnings
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import release_materials as materials
import release_contract as contract
from test_release_contract import VERSION, COMMIT, material_fixture


def fixture_tools():
    return {'versions': {name: materials.TOOL_VERSIONS[name] for name in materials.RUNTIME_TOOLS},
            'host': 'windows/amd64', 'executables': {name: 'd' * 64 for name in
                ('go', 'compile', 'link', 'node', 'python')}, 'go_license_sha256': 'e' * 64}


def fixture_tool_inputs():
    return {'schema': 1, 'tools': {name: {'version': version,
            'source': 'https://example.invalid/reviewed/' + name, 'sha256': 'f' * 64}
            for name, version in materials.TOOL_VERSIONS.items()}}


def fixture_application():
    return {'main.go': f'package main\n// Version {VERSION}\nfunc main() {{}}\n'.encode(),
            'go.mod': b'module example.invalid/fixture\n\ngo 1.27.3\n', 'go.sum': b'',
            'scripts/release_materials.py': b'# synthetic material producer\n',
            'scripts/release_contract.py': b'# synthetic artifact contract\n',
            'scripts/windows/send-stop-signal.ps1': b'# synthetic stop helper\n',
            'scripts/acceptance/database/main.go': b'package main\nfunc main() {}\n',
            'internal/fixture.go': b'package internal\n'}


def production_source_files(root, original, application):
    """Synthetic metadata with real ZIP/hash binding, for consumer rejection tests."""
    tools = (json.dumps(fixture_tool_inputs()) + '\n').encode()
    frontend = {'static/out/index.html': b'<script src="app.js"></script>',
                'static/out/app.js': f'const version = "{VERSION}";'.encode()}
    entries = {**original, **{'application/' + name: data for name, data in {**application, **frontend}.items()}}
    entries.pop('SHA256SUMS', None)
    inputs = {'source': 'a' * 64, 'license_report': contract.sha(original['licenses/THIRD_PARTY_LICENSES.csv']),
              'tool_inputs': contract.sha(tools),
              **{'documents/' + name: contract.sha(original[name]) for name in contract.DOCS},
              **{'alpine/' + arch: contract.digest(root / f'alpine/{arch}.zip') for arch in contract.LINUX}}
    production = {'schema': 1, 'version': VERSION, 'commit': COMMIT, 'source_date_epoch': 1,
                  'inputs_sha256': 'c' * 64, 'inputs': inputs, 'tools': fixture_tools(),
                  'frontend': {name: contract.sha(data) for name, data in frontend.items()},
                  'scripts': {name: contract.sha(application[name]) for name in
                              ('scripts/release_materials.py', 'scripts/release_contract.py')}}
    entries[materials.PRODUCTION_RECORD] = (json.dumps(production) + '\n').encode()
    entries['tool-inputs.json'] = tools
    entries['SHA256SUMS'] = ''.join(f'{contract.sha(data)}  {name}\n' for name, data in sorted(entries.items())).encode()
    return entries


class Response(io.BytesIO):
    pass


class MaterialsTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix='materials-entry-',
                                        dir=os.environ.get('RELEASE_TEST_ROOT')))
        self.inputs = self.root / 'inputs'
        self.inputs.mkdir()
        self.source = self.inputs / 'reviewed'
        self.materials_sha, _ = material_fixture(self.source)
        self.checkout = self.root / 'checkout'
        self.checkout.mkdir()
        self.application = fixture_application()
        for name, data in self.application.items():
            path = self.checkout / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        source_name = f'{contract.APP}-source-{VERSION}.zip'
        with zipfile.ZipFile(self.source / source_name) as archive:
            entries = {name: archive.read(name) for name in contract.zip_files(archive) if name != 'SHA256SUMS'}
        entries.update({'application/' + name: data for name, data in self.application.items()})
        entries['application/static/out/obsolete.js'] = b'synthetic old generated output'
        entries['SHA256SUMS'] = ''.join(f'{contract.sha(data)}  {name}\n' for name, data in sorted(entries.items())).encode()
        template = self.inputs / 'source-template.zip'
        contract.archive_write(template, {name: (data, 0o644) for name, data in entries.items()})
        tools_path = self.inputs / 'tool-inputs.json'
        contract.write_json(tools_path, fixture_tool_inputs())
        docs = {name: {'path': 'reviewed/' + name,
                       'sha256': contract.digest(self.source / name)} for name in contract.DOCS}
        alpine = {arch: {'path': f'reviewed/alpine/{arch}.zip',
                         'sha256': contract.digest(self.source / f'alpine/{arch}.zip'),
                         'base_image': 'alpine@sha256:' + 'c' * 64} for arch in contract.LINUX}
        self.spec = {'schema': 1, 'version': VERSION, 'commit': COMMIT,
                     'source': {'path': 'source-template.zip', 'sha256': contract.digest(template)},
                     'tool_inputs': {'path': 'tool-inputs.json', 'sha256': contract.digest(tools_path)},
                     'license_report': {'path': 'reviewed/THIRD_PARTY_LICENSES.csv',
                                        'sha256': contract.digest(self.source / 'THIRD_PARTY_LICENSES.csv')},
                     'documents': docs, 'alpine': alpine}
        self.spec_path = self.inputs / 'inputs.json'
        contract.write_json(self.spec_path, self.spec)
        self.output = self.root / 'assembled'
        self.args = argparse.Namespace(version=VERSION, commit=COMMIT,
                                      inputs_json=str(self.spec_path), output=str(self.output),
                                      inputs_sha=contract.digest(self.spec_path),
                                      materials=str(self.source), materials_sha=self.materials_sha)

    @contextmanager
    def production_environment(self):
        previous = Path.cwd()
        os.chdir(self.checkout)
        try:
            with patch.object(materials, 'target', return_value=1), \
                 patch.object(materials, 'observed_tools', side_effect=fixture_tools), \
                 patch.object(contract.subprocess, 'check_output',
                              return_value=('\0'.join(sorted(self.application)) + '\0').encode()):
                yield
        finally:
            os.chdir(previous)

    def generated_frontend(self, args):
        self.assertEqual((args.version, args.commit), (VERSION, COMMIT))
        directory = self.checkout / 'static/out'
        directory.mkdir(parents=True, exist_ok=True)
        (directory / 'index.html').write_bytes(b'<script src="app.js"></script>')
        (directory / 'app.js').write_bytes(f'const version = "{VERSION}";'.encode())

    def assemble(self):
        with self.production_environment(), patch.object(contract, 'frontend', side_effect=self.generated_frontend):
            materials.produce(self.args)

    def produced_args(self):
        return argparse.Namespace(**{**vars(self.args), 'materials': str(self.output),
            'materials_sha': contract.digest(self.output / 'materials.json')})

    def test_assemble_binds_exact_inputs_and_does_not_change_source(self):
        before = contract.tree(self.inputs)
        self.assemble()
        sha = contract.digest(self.output / 'materials.json')
        data = contract.materials(self.output, VERSION, COMMIT, sha)
        self.assertEqual(data['source_date_epoch'], 1)
        self.assertEqual(contract.tree(self.inputs), before)

    def test_assemble_refuses_changed_reviewed_input(self):
        (self.source / 'INSTALL.md').write_bytes(b'changed after review')
        with self.assertRaises(contract.Invalid):
            self.assemble()
        self.assertFalse(self.output.exists())

    def test_assemble_refuses_output_inside_reviewed_inputs(self):
        self.args.output = str(self.inputs / 'output')
        with self.assertRaises(contract.Invalid):
            self.assemble()

    def test_assemble_refuses_existing_output(self):
        self.output.mkdir()
        (self.output / 'keep.txt').write_bytes(b'preserved')
        with self.assertRaises(contract.Invalid):
            self.assemble()
        self.assertEqual((self.output / 'keep.txt').read_bytes(), b'preserved')

    def test_audit_refuses_wrong_epoch(self):
        with patch.object(materials, 'target', return_value=2), self.assertRaises(contract.Invalid):
            materials.audit(self.args)

    def test_audit_checks_actual_application_and_generated_frontend(self):
        self.assemble()
        (self.checkout / 'static/out/app.js').write_bytes(b'changed generated result')
        with self.production_environment(), self.assertRaises(contract.Invalid):
            materials.audit(self.produced_args())

    def bundle(self):
        self.assemble()
        output = self.root / 'bundle.zip'
        args = argparse.Namespace(**{**vars(self.produced_args()), 'output': str(output)})
        with self.production_environment():
            materials.pack(args)
        return output

    def test_pack_unpack_round_trip_preserves_every_byte(self):
        bundle = self.bundle()
        output = self.root / 'downloaded'
        args = argparse.Namespace(version=VERSION, commit=COMMIT, bundle=str(bundle),
                                  bundle_sha=contract.digest(bundle), materials_sha=self.produced_args().materials_sha,
                                  output=str(output))
        materials.unpack(args)
        self.assertEqual(contract.tree(output), contract.tree(self.output))
        with self.production_environment():
            materials.audit(argparse.Namespace(**{**vars(self.produced_args()), 'materials': str(output)}))

    def test_production_replaces_old_frontend_and_binds_tools_and_scripts(self):
        self.assemble()
        source = self.output / materials.template_name(VERSION)
        production = materials.source_production(source, VERSION, COMMIT, 1)
        self.assertEqual(production['tools']['versions']['go'], '1.27.3')
        self.assertEqual(production['inputs_sha256'], self.args.inputs_sha)
        with zipfile.ZipFile(source) as archive:
            self.assertNotIn('application/static/out/obsolete.js', contract.zip_checksums(archive))
            self.assertEqual(archive.read('application/static/out/app.js'),
                             (self.checkout / 'static/out/app.js').read_bytes())

    def test_production_rejects_stale_manifest_and_committed_source(self):
        self.args.inputs_sha = '0' * 64
        with self.assertRaisesRegex(contract.Invalid, 'input manifest'):
            self.assemble()
        self.args.inputs_sha = contract.digest(self.spec_path)
        (self.checkout / 'main.go').write_bytes(b'changed committed input')
        with self.assertRaisesRegex(contract.Invalid, 'committed application'):
            self.assemble()
        self.assertFalse(self.output.exists())

    def test_production_rejects_tool_baseline_drift(self):
        data = fixture_tool_inputs()
        for name in materials.TOOL_VERSIONS:
            changed = json.loads(json.dumps(data))
            changed['tools'][name]['version'] = 'old-tool'
            with self.subTest(tool=name), self.assertRaises(contract.Invalid):
                materials.tool_inputs(changed)

    def test_production_rejects_failed_frontend_before_creating_materials(self):
        with self.production_environment(), \
             patch.object(contract, 'frontend', side_effect=RuntimeError('synthetic frontend failure')), \
             self.assertRaises(RuntimeError):
            materials.produce(self.args)
        self.assertFalse(self.output.exists())

    def test_observed_tools_require_the_real_go_baseline_first(self):
        with patch.object(contract, 'go_run', return_value='go1.27.0'), self.assertRaises(contract.Invalid):
            materials.observed_tools()

    def test_unpack_refuses_wrong_outer_hash(self):
        bundle = self.bundle()
        args = argparse.Namespace(version=VERSION, commit=COMMIT, bundle=str(bundle),
                                  bundle_sha='0' * 64, materials_sha=self.materials_sha,
                                  output=str(self.root / 'downloaded'))
        with self.assertRaises(contract.Invalid):
            materials.unpack(args)
        self.assertFalse(Path(args.output).exists())

    def test_unpack_refuses_unexpected_outer_file_before_extracting(self):
        bundle = self.bundle()
        with zipfile.ZipFile(bundle, 'a') as archive:
            archive.writestr('unexpected.txt', 'fixture')
        args = argparse.Namespace(version=VERSION, commit=COMMIT, bundle=str(bundle),
                                  bundle_sha=contract.digest(bundle), materials_sha=self.materials_sha,
                                  output=str(self.root / 'downloaded'))
        with self.assertRaises(contract.Invalid):
            materials.unpack(args)
        self.assertFalse(Path(args.output).exists())

    def test_unpack_refuses_duplicate_and_parent_paths_before_extracting(self):
        for index, name in enumerate(('../fixture.txt', 'materials.json')):
            bundle = self.root / ('unsafe-' + str(index) + '.zip')
            with zipfile.ZipFile(bundle, 'x') as archive:
                archive.writestr(name, b'fixture')
                with warnings.catch_warnings():
                    warnings.simplefilter('ignore', UserWarning)
                    archive.writestr(name, b'duplicate')
            args = argparse.Namespace(version=VERSION, commit=COMMIT, bundle=str(bundle),
                                      bundle_sha=contract.digest(bundle), materials_sha=self.materials_sha,
                                      output=str(self.root / ('unsafe-output-' + str(index))))
            with self.assertRaises(contract.Invalid):
                materials.unpack(args)
            self.assertFalse(Path(args.output).exists())

    def test_asset_redirect_is_https_and_exact_host_only(self):
        good = 'https://release-assets.githubusercontent.com/fixture'
        self.assertEqual(materials.asset_location(good), good)
        for value in ('http://release-assets.githubusercontent.com/fixture',
                      'https://example.invalid/fixture', 'https://objects.githubusercontent.com:444/fixture'):
            with self.assertRaises(contract.Invalid):
                materials.asset_location(value)

    def test_signed_asset_redirect_has_no_github_credential(self):
        request = urllib.request.Request('https://release-assets.githubusercontent.com/fixture',
                                         headers={'User-Agent': 'fixture'})
        response = materials.AssetRedirect().redirect_request(
            request, None, 302, 'fixture', {}, 'https://objects.githubusercontent.com/next')
        self.assertFalse(any(key.lower() == 'authorization' for key in response.headers))
        request = urllib.request.Request('https://release-assets.githubusercontent.com/fixture',
                                         headers={'Authorization': 'synthetic'})
        with self.assertRaises(contract.Invalid):
            materials.AssetRedirect().redirect_request(
                request, None, 302, 'fixture', {}, 'https://objects.githubusercontent.com/next')

    def download_fixture(self, *, draft=True, mismatch=False, redirect=False, auth_error=None):
        content = b'synthetic bundle'
        release = {'draft': draft, 'tag_name': 'release-materials-inbox-20261022-000000-abcdefgh',
                   'target_commitish': COMMIT, 'published_at': None, 'assets': [{'id': 2}]}
        asset = {'url': materials.API + '/releases/assets/2', 'id': 2,
                 'name': f'release-materials-{VERSION}-{COMMIT}.zip',
                 'state': 'uploaded', 'size': len(content), 'digest': 'sha256:' + contract.sha(content)}
        requests = []
        class Opener:
            def open(self, request, timeout):
                requests.append(request)
                if auth_error:
                    raise urllib.error.HTTPError(request.full_url, auth_error, 'fixture', {}, None)
                if request.full_url.endswith('/releases/1'):
                    return Response(json.dumps(release).encode())
                if request.get_header('Accept') != 'application/octet-stream':
                    return Response(json.dumps(asset).encode())
                if redirect:
                    raise urllib.error.HTTPError(request.full_url, 302, 'fixture',
                                                 {'Location': 'https://release-assets.githubusercontent.com/fixture'},
                                                 None)
                return Response(content)
        class PublicOpener:
            def open(self, request, timeout):
                requests.append(request)
                self_outer.assertIsNone(request.get_header('Authorization'))
                return Response(content)
        self_outer = self
        args = argparse.Namespace(version=VERSION, commit=COMMIT, release_id='1', asset_id='2',
                                  bundle_sha='0' * 64 if mismatch else contract.sha(content),
                                  output=str(self.root / 'download.zip'))
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': materials.REPOSITORY, 'GH_TOKEN': 'synthetic'}), \
             patch.object(materials.urllib.request, 'build_opener',
                          side_effect=[Opener(), PublicOpener()] if redirect else [Opener()]):
            materials.download(args)
        return requests

    def test_exact_draft_asset_can_be_downloaded(self):
        requests = self.download_fixture()
        self.assertEqual(len(requests), 3)

    def test_redirect_credential_stays_on_github_api(self):
        requests = self.download_fixture(redirect=True)
        self.assertEqual(len(requests), 4)
        self.assertTrue(all(r.full_url.startswith(materials.API)
                            for r in requests if r.get_header('Authorization')))

    def test_download_refuses_published_inbox(self):
        with self.assertRaises(contract.Invalid):
            self.download_fixture(draft=False)

    def test_download_refuses_changed_bundle_digest(self):
        with self.assertRaises(contract.Invalid):
            self.download_fixture(mismatch=True)

    def test_download_auth_failure_is_not_absence(self):
        for code in (401, 403, 404, 503):
            with self.assertRaises(urllib.error.HTTPError) as caught:
                self.download_fixture(auth_error=code)
            caught.exception.close()

    def test_resolve_refuses_non_main_dispatch(self):
        args = argparse.Namespace(version=VERSION, commit=COMMIT)
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': materials.REPOSITORY,
                                     'GITHUB_REF': 'refs/heads/fixture'}), \
             self.assertRaises(contract.Invalid):
            materials.resolve(args)

    def test_resolve_refuses_another_repository(self):
        args = argparse.Namespace(version=VERSION, commit=COMMIT)
        with patch.dict(os.environ, {'GITHUB_REPOSITORY': 'fixture/other',
                                     'GITHUB_REF': 'refs/heads/main'}), \
             self.assertRaises(contract.Invalid):
            materials.resolve(args)

    def test_target_refuses_untracked_checkout_input(self):
        with patch.object(contract, 'run', side_effect=[COMMIT, COMMIT, '?? fixture.go']), \
             patch.object(materials.subprocess, 'run') as mutation, \
             self.assertRaises(contract.Invalid):
            materials.target(VERSION, COMMIT)
        mutation.assert_not_called()

    def test_target_refuses_a_different_checkout(self):
        with patch.object(contract, 'run', side_effect=[COMMIT, 'b' * 40]), \
             self.assertRaises(contract.Invalid):
            materials.target(VERSION, COMMIT)

    def test_target_refuses_the_upstream_version_comment(self):
        with patch.object(contract, 'run', side_effect=[COMMIT, COMMIT, '', '// Version v0.13.6']), \
             patch.object(materials.subprocess, 'run'), self.assertRaises(contract.Invalid):
            materials.target(VERSION, COMMIT)


if __name__ == '__main__':
    unittest.main()
