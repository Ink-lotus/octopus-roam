"""Go metadata gates: synthetic full-matrix contracts plus real PE/ELF/Mach-O readers."""
import argparse
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import release_contract as contract
import test_release_contract as fixtures


def package_fixture(root):
    (root / 'archives').mkdir(parents=True)
    (root / 'bin').mkdir()
    (root / 'THIRD_PARTY_LICENSES.csv').write_bytes(b'ecosystem,name\ngo,fixture\n')
    for target in contract.TARGETS:
        suffix = '.exe' if target.startswith('windows-') else ''
        (root / 'bin' / f'{contract.APP}-{target}{suffix}').write_bytes(('synthetic ' + target).encode())
    return argparse.Namespace(output=str(root), version=fixtures.VERSION, commit=fixtures.COMMIT,
                              materials=None, materials_sha=None, standard_only=False)


def refresh_delivery_and_manifest(root):
    delivery = root / 'delivery'
    (delivery / 'SHA256SUMS').write_text(''.join(f'{contract.digest(p)}  {p.name}\n'
        for p in sorted(delivery.iterdir()) if p.name != 'SHA256SUMS'), encoding='utf-8')
    manifest = contract.read_json(root / 'artifact.json')
    manifest['files'] = contract.tree(root)
    manifest['files'].pop('artifact.json')
    contract.write_json(root / 'artifact.json', manifest)


class GoGateTests(unittest.TestCase):
    def setUp(self):
        self.root = fixtures.new_directory()
        self.env = patch.dict(os.environ, {'GITHUB_REPOSITORY': 'Ink-lotus/octopus-roam'})
        self.env.start()
        self.addCleanup(self.env.stop)
        self.go = patch.object(contract, 'go_run', side_effect=fixtures.fixture_go)
        self.mock_go = self.go.start()
        self.addCleanup(self.go.stop)

    def phase(self, command, name):
        root = self.root / name
        if command == 'package':
            return contract.package, package_fixture(root)
        args = fixtures.release_fixture(root)
        if command == 'verify':
            contract.seal_or_verify(args)
            args.command = 'verify'
        return contract.seal_or_verify, args

    def test_package_records_actual_go_for_every_target(self):
        args = package_fixture(self.root / 'package')
        contract.package(args)
        record = contract.read_json(Path(args.output) / 'build.json')
        self.assertEqual(record['schema'], 2)
        self.assertEqual(record['go_versions'], {target: 'go1.27.3' for target in contract.TARGETS})
        contract.verify_archives(Path(args.output), fixtures.VERSION, fixtures.COMMIT)

    def test_every_phase_rejects_old_metadata_for_each_of_eleven_targets(self):
        for command in ('package', 'seal', 'verify'):
            for target in contract.TARGETS:
                with self.subTest(command=command, target=target):
                    operation, args = self.phase(command, command + '-' + target)
                    suffix = '.exe' if target.startswith('windows-') else ''
                    name = f'{contract.APP}-{target}{suffix}'
                    def old_metadata(*arguments):
                        result = fixtures.fixture_go(*arguments)
                        if arguments[:2] == ('version', '-m') and Path(arguments[2]).name == name:
                            return result.replace(': go1.27.3\n', ': go1.27.0\n', 1)
                        return result
                    with patch.object(contract, 'go_run', side_effect=old_metadata), \
                         self.assertRaisesRegex(contract.Invalid, 'Binary Go version differs'):
                        operation(args)

    def test_each_phase_rejects_missing_or_nonexact_go_metadata(self):
        bad_headers = ('', 'not a Go executable', 'go1.27.30', 'go1.27.3-custom', 'go1.27.4', 'devel')
        for command in ('package', 'seal', 'verify'):
            for index, header in enumerate(bad_headers):
                with self.subTest(command=command, header=header):
                    operation, args = self.phase(command, f'{command}-{index}')
                    def malformed(*arguments):
                        result = fixtures.fixture_go(*arguments)
                        if arguments[:2] == ('version', '-m'):
                            return result.replace(': go1.27.3\n', ': ' + header + '\n', 1)
                        return result
                    with patch.object(contract, 'go_run', side_effect=malformed), self.assertRaises(contract.Invalid):
                        operation(args)

    def test_each_phase_requires_the_pinned_inspection_tool(self):
        for command in ('package', 'seal', 'verify'):
            operation, args = self.phase(command, command)
            def old_tool(*arguments):
                return 'go1.27.0' if arguments == ('env', 'GOVERSION') else fixtures.fixture_go(*arguments)
            with self.subTest(command=command), patch.object(contract, 'go_run', side_effect=old_tool), \
                 self.assertRaisesRegex(contract.Invalid, 'Go inspection tool must be go1.27.3'):
                operation(args)

    def test_each_phase_rejects_missing_inspection_executable(self):
        for command in ('package', 'seal', 'verify'):
            operation, args = self.phase(command, command)
            with self.subTest(command=command), patch.object(contract, 'go_run', side_effect=FileNotFoundError), \
                 self.assertRaises(FileNotFoundError):
                operation(args)

    def test_seal_and_verify_reject_stale_incomplete_or_false_records(self):
        changes = (
            lambda data: data.update(schema=1),
            lambda data: data.pop('go_versions'),
            lambda data: data['go_versions'].pop('android-386'),
            lambda data: data['go_versions'].update({'unexpected': 'go1.27.3'}),
            lambda data: data['go_versions'].update({'android-386': 'go1.27.0'}),
        )
        for command in ('seal', 'verify'):
            for index, modify in enumerate(changes):
                with self.subTest(command=command, index=index):
                    operation, args = self.phase(command, f'{command}-{index}')
                    root = Path(args.output)
                    record = contract.read_json(root / 'build.json')
                    modify(record)
                    contract.write_json(root / 'build.json', record)
                    if command == 'verify':
                        refresh_delivery_and_manifest(root)
                    with self.assertRaises(contract.Invalid):
                        operation(args)

    def test_bin_zip_and_record_cannot_disagree(self):
        for command in ('seal', 'verify'):
            operation, args = self.phase(command, command)
            binary = Path(args.output) / 'bin' / f'{contract.APP}-linux-amd64'
            binary.write_bytes(b'synthetic linux-arm64')
            with self.subTest(command=command), self.assertRaisesRegex(contract.Invalid, 'Actual binary hash differs'):
                operation(args)

    def test_wrong_target_and_build_identity_are_rechecked(self):
        for command in ('package', 'seal', 'verify'):
            for index in range(3):
                operation, args = self.phase(command, f'{command}-{index}')
                def mismatch(*arguments):
                    result = fixtures.fixture_go(*arguments)
                    if arguments[:2] == ('version', '-m'):
                        if index == 0:
                            return result.replace('GOARCH=', 'otherGOARCH=')
                        if index == 1:
                            return result + '\tbuild\tGOOS=linux\n'
                    if arguments[:2] == ('tool', 'buildid') and index == 2:
                        return result + '-different'
                    return result
                with self.subTest(command=command, index=index), patch.object(contract, 'go_run', side_effect=mismatch), \
                     self.assertRaises(contract.Invalid):
                    operation(args)

    def test_verify_binds_public_build_record_and_program_copies(self):
        for kind in ('build', 'program'):
            operation, args = self.phase('verify', kind)
            root = Path(args.output)
            if kind == 'build':
                record = contract.read_json(root / 'delivery/BUILD.json')
                record['go_versions']['android-386'] = 'go1.27.0'
                contract.write_json(root / 'delivery/BUILD.json', record)
                message = 'Published build record differs'
            else:
                public = root / 'delivery' / f'{contract.APP}-linux-amd64.zip'
                public.write_bytes((root / 'archives' / f'{contract.APP}-linux-arm64.zip').read_bytes())
                message = 'Published program archive differs'
            refresh_delivery_and_manifest(root)
            with self.subTest(kind=kind), self.assertRaisesRegex(contract.Invalid, message):
                operation(args)

    def test_all_four_images_remain_bound_to_the_inspected_program_bytes(self):
        for arch in contract.LINUX:
            operation, args = self.phase('seal', arch)
            root = Path(args.output)
            binary = root / 'bin' / f'{contract.APP}-linux-{arch}'
            original_reader = contract.image_files
            def changed_image(path, wanted, image_arch, version, commit):
                result = original_reader(path, wanted, image_arch, version, commit)
                if image_arch == arch:
                    result[f'app/{contract.APP}'] = binary.read_bytes() + b'changed'
                return result
            with self.subTest(arch=arch), patch.object(contract, 'image_files', side_effect=changed_image), \
                 self.assertRaisesRegex(contract.Invalid, 'Image payload differs'):
                operation(args)


class RealGoMetadataTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # Tiny standard-library-only programs. This does not build the product
        # matrix, Android/NDK outputs, or real container images.
        contract.require_go_toolchain()
        cls.root = fixtures.new_directory() / 'real Go metadata'
        cls.root.mkdir()
        (cls.root / 'go.mod').write_text('module example.invalid/metadata-fixture\n\ngo 1.27.3\n', encoding='utf-8')
        (cls.root / 'main.go').write_text('package main\nfunc main() {}\n', encoding='utf-8')
        cls.binaries = {}
        for target in ('windows-amd64', 'linux-arm64', 'darwin-amd64'):
            goos, goarch = target.split('-')
            binary = cls.root / (target + ('.exe' if goos == 'windows' else ''))
            subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false',
                f'-ldflags=-s -w -buildid={contract.APP}/{fixtures.VERSION}/{fixtures.COMMIT}',
                '-o', str(binary), '.'], cwd=cls.root, check=True,
                env={**os.environ, 'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOFLAGS': '-mod=readonly',
                     'CGO_ENABLED': '0', 'GOOS': goos, 'GOARCH': goarch, 'GOARM': ''})
            cls.binaries[target] = binary

    def test_reads_actual_pe_elf_and_macho_metadata(self):
        for target, binary in self.binaries.items():
            with self.subTest(target=target):
                info, version = contract.binary_metadata(binary, target, fixtures.VERSION, fixtures.COMMIT)
                self.assertEqual(version, 'go1.27.3')
                self.assertIn('example.invalid/metadata-fixture', info)

    def test_actual_program_cannot_be_relabelled_as_another_target_or_release(self):
        binary = self.binaries['windows-amd64']
        for target, version in (('linux-amd64', fixtures.VERSION), ('windows-amd64', 'v0.1.0-rc.2')):
            with self.subTest(target=target, version=version), self.assertRaises(contract.Invalid):
                contract.binary_metadata(binary, target, version, fixtures.COMMIT)

    def test_non_go_file_fails_with_the_real_reader(self):
        binary = self.root / 'not-go.bin'
        binary.write_bytes(b'synthetic non-Go payload')
        with self.assertRaises((contract.Invalid, subprocess.CalledProcessError)):
            contract.binary_metadata(binary, 'windows-amd64', fixtures.VERSION, fixtures.COMMIT)

    def test_reader_forces_local_offline_tools_without_disabling_checksums(self):
        with patch.dict(os.environ, {'GOTOOLCHAIN': 'auto', 'GOPROXY': 'https://proxy.example.invalid',
                                     'GOSUMDB': 'sum.golang.org'}), \
             patch.object(contract.subprocess, 'check_output', return_value='go1.27.3\n') as command:
            contract.require_go_toolchain()
        environment = command.call_args.kwargs['env']
        self.assertEqual(environment['GOTOOLCHAIN'], 'local')
        self.assertEqual(environment['GOPROXY'], 'off')
        self.assertEqual(environment['GOFLAGS'], '-mod=readonly')
        self.assertEqual(environment['GOSUMDB'], 'sum.golang.org')


if __name__ == '__main__':
    unittest.main()
