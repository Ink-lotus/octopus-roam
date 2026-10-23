"""Bind a derived acceptance layout to an exact sealed artifact. No product or DB startup."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import zipfile

import release_contract as contract
import release_materials as materials

REPOSITORY = Path(__file__).resolve().parents[1]
ROLE = 'derived-acceptance-layout'


def write_new(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('xb') as stream:
        stream.write(content)


def copy_new(source, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    with source.open('rb') as src, destination.open('xb') as dst:
        shutil.copyfileobj(src, dst)


def portable_files(names):
    folded = set()
    for name in sorted(names):
        contract.safe(name)
        parts = name.split('/')
        contract.require(not any(ord(char) < 32 for char in name) and
                         all(not part.endswith(('.', ' ')) and
                             not re.fullmatch(r'(?i)(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?', part)
                             for part in parts), 'Non-portable acceptance path')
        contract.require(name.casefold() not in folded, 'Case-colliding acceptance paths')
        folded.add(name.casefold())


def verified_artifact(args, artifact):
    contract.identity(args.version, args.commit)
    materials.require_hash(args.artifact_sha)
    materials.require_hash(args.materials_sha)
    contract.require(Path.cwd().resolve() == REPOSITORY.resolve(), 'Run in the reviewed repository root')
    epoch = materials.target(args.version, args.commit)
    contract.require(contract.digest(artifact / 'artifact.json') == args.artifact_sha,
                     'Reviewed artifact manifest hash differs')
    contract.require(os.environ.get('GITHUB_REPOSITORY', materials.REPOSITORY) == materials.REPOSITORY,
                     'Acceptance artifact belongs to another repository')
    saved = os.environ.get('GITHUB_REPOSITORY')
    try:
        os.environ['GITHUB_REPOSITORY'] = materials.REPOSITORY
        contract.seal_or_verify(argparse.Namespace(command='verify', output=str(artifact),
            version=args.version, commit=args.commit, materials_sha=args.materials_sha))
    finally:
        if saved is None:
            os.environ.pop('GITHUB_REPOSITORY', None)
        else:
            os.environ['GITHUB_REPOSITORY'] = saved
    source = artifact / 'materials' / materials.template_name(args.version)
    materials.source_application(source, materials.application_files(REPOSITORY))
    production = materials.source_production(source, args.version, args.commit, epoch)
    build = contract.read_json(artifact / 'build.json')
    cache = {}
    for target in contract.TARGETS:
        suffix = '.exe' if target.startswith('windows-') else ''
        info, _ = contract.binary_metadata(artifact / 'bin' / f'{contract.APP}-{target}{suffix}',
                                          target, args.version, args.commit)
        contract.require(f'vcs.revision={args.commit}' in info and 'vcs.modified=false' in info,
                         'Acceptance product source revision differs')
        contract.dependency_sources(info, source, cache)
    contract.require(contract.digest(artifact / 'artifact.json') == args.artifact_sha,
                     'Artifact changed during acceptance verification')
    return source, build, production


def metadata(args, artifact, build, source, files):
    return {'schema': 2, 'role': ROLE, 'version': args.version, 'commit': args.commit,
            'source_artifact': str(artifact), 'source_artifact_manifest_sha256': args.artifact_sha,
            'source_build_json_sha256': contract.digest(artifact / 'build.json'),
            'source_materials_sha256': args.materials_sha, 'source_zip_sha256': contract.digest(source),
            'go_versions': build['go_versions'], 'source_files': files,
            'binaries': [{'target': target.replace('-', '/'),
                          'path': f'bin/{contract.APP}-{target}' + ('.exe' if target.startswith('windows-') else ''),
                          'sha256': build['binaries'][target]} for target in contract.TARGETS],
            'archives': [{'name': path.name, 'sha256': contract.digest(path)}
                         for path in sorted((artifact / 'archives').glob('*.zip'))],
            'database_connected': False, 'product_started': False}


def prepare(args):
    artifact, output = Path(args.artifact).resolve(), Path(args.output).resolve()
    allowed = (REPOSITORY / 'build/release-local').resolve()
    contract.require(not output.exists() and output.is_relative_to(allowed) and output != allowed
                     and not output.is_relative_to(artifact) and not artifact.is_relative_to(output),
                     'Acceptance output must be a new isolated directory under build/release-local')
    source, build, _ = verified_artifact(args, artifact)
    sealed = contract.read_json(artifact / 'artifact.json')
    with zipfile.ZipFile(source) as archive:
        names = contract.zip_checksums(archive)
        application = {name.removeprefix('application/'): name
                       for name in names if name.startswith('application/')}
        contract.require(application, 'Application source is missing')
        portable_files(application)
        output.mkdir(parents=True, exist_ok=False)
        for relative, name in sorted(application.items()):
            write_new(output / 'source' / relative, archive.read(name))
    for directory in ('bin', 'archives'):
        for path in sorted((artifact / directory).iterdir()):
            contract.require(path.is_file() and not path.is_symlink(), 'Unexpected artifact program path')
            relative = directory + '/' + path.name
            copy_new(path, output / relative)
            contract.require(contract.digest(output / relative) == sealed['files'][relative],
                             'Copied artifact file differs')
    files = contract.tree(output / 'source')
    write_new(output / 'source-input-sha256sums.txt',
              ''.join(f'{value}  {name}\n' for name, value in sorted(files.items())).encode())
    record = metadata(args, artifact, build, source, files)
    write_new(output / 'build-info.json', (json.dumps(record, ensure_ascii=False, indent=2) + '\n').encode())
    write_new(output / 'file-sha256sums.txt',
              ''.join(f'{value}  {name}\n' for name, value in contract.tree(output).items()).encode())
    contract.require(contract.tree(artifact) == {**sealed['files'], 'artifact.json': args.artifact_sha},
                     'Artifact bytes changed during layout preparation')
    print(json.dumps({'status': 'layout_prepared', 'version': args.version, 'commit': args.commit,
                      'artifact_sha256': args.artifact_sha, 'materials_sha256': args.materials_sha,
                      'source_files': len(files), 'database_connected': False, 'product_started': False}))


def manifest(path):
    result = {}
    for line in path.read_text(encoding='utf-8-sig').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  (.+)', line)
        contract.require(match, 'Invalid acceptance checksum manifest')
        checksum, name = match.groups()
        contract.safe(name)
        contract.require(name not in result, 'Duplicate acceptance checksum row')
        result[name] = checksum
    portable_files(result)
    return result


def verify_layout(args):
    layout = Path(args.candidate).resolve()
    record = contract.read_json(layout / 'build-info.json')
    contract.require(record.get('role') == ROLE and record.get('schema') == 2,
                     'An explicitly derived artifact layout is required')
    contract.require(record['version'] == args.version and record['commit'] == args.commit
                     and record['source_artifact_manifest_sha256'] == args.artifact_sha
                     and record['source_materials_sha256'] == args.materials_sha,
                     'Acceptance identity differs from the approved inputs')
    artifact = Path(record['source_artifact']).resolve()
    source, build, _ = verified_artifact(args, artifact)
    files = contract.tree(layout / 'source')
    contract.require(record == metadata(args, artifact, build, source, files),
                     'Derived acceptance metadata differs')
    contract.require(manifest(layout / 'source-input-sha256sums.txt') == files,
                     'Derived source manifest differs')
    actual = contract.tree(layout)
    actual.pop('file-sha256sums.txt')
    contract.require(manifest(layout / 'file-sha256sums.txt') == actual,
                     'Derived acceptance file set or bytes differ')
    with zipfile.ZipFile(source) as archive:
        expected = {name.removeprefix('application/'): contract.sha(archive.read(name))
                    for name in contract.zip_files(archive) if name.startswith('application/')}
    contract.require(files == expected, 'Derived source differs from the approved source ZIP')
    expected_names = {'build-info.json', 'source-input-sha256sums.txt',
                      *('source/' + name for name in expected)}
    for directory in ('bin', 'archives'):
        contract.require(contract.tree(layout / directory) == contract.tree(artifact / directory),
                         'Derived executable or archive differs from the approved artifact')
        expected_names.update(directory + '/' + name for name in contract.tree(artifact / directory))
    contract.require(set(actual) == expected_names, 'Unexpected derived acceptance file')
    print(json.dumps({'status': 'layout_verified', 'version': args.version, 'commit': args.commit,
                      'artifact_sha256': args.artifact_sha, 'materials_sha256': args.materials_sha,
                      'product_sha256': build['binaries']['windows-amd64'], 'source_files': len(files)}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('prepare', 'verify-layout'))
    parser.add_argument('--artifact')
    parser.add_argument('--candidate')
    parser.add_argument('--output')
    parser.add_argument('--version', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--artifact-sha', required=True)
    parser.add_argument('--materials-sha', required=True)
    args = parser.parse_args()
    try:
        if args.command == 'prepare':
            prepare(args)
        else:
            verify_layout(args)
    except (contract.Invalid, KeyError, TypeError, OSError, ValueError,
            subprocess.CalledProcessError, zipfile.BadZipFile) as error:
        print(f'release_acceptance: failed ({type(error).__name__})')
        if isinstance(error, contract.Invalid):
            print(str(error))
        raise SystemExit(2)


if __name__ == '__main__':
    main()
