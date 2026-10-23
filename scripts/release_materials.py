"""Assemble, stage and audit reviewed release materials; no product startup."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
import zipfile

import release_contract as contract

REPOSITORY = 'Ink-lotus/octopus-roam'
API = f'https://api.github.com/repos/{REPOSITORY}'
MAX_BUNDLE_BYTES = 2_000_000_000
INBOX_TAG = r'release-materials-inbox-[0-9]{8}-[0-9]{6}-[a-z0-9]{8}'
ASSET_HOSTS = {'release-assets.githubusercontent.com', 'objects.githubusercontent.com'}
TOOL_VERSIONS = {'go': '1.27.3', 'node': '24.18.0', 'pnpm': '11.15.1',
                 'python': '3.14.6', 'go-licenses': 'v2.0.1', 'ndk': 'r28c'}
RUNTIME_TOOLS = ('go', 'node', 'pnpm', 'python')
PRODUCTION_RECORD = 'material-production.json'


def require_hash(value):
    contract.require(isinstance(value, str) and re.fullmatch('[0-9a-f]{64}', value),
                     'A reviewed lowercase SHA-256 is required')


def required_files(version):
    return {'materials.json', 'THIRD_PARTY_LICENSES.csv',
            f'{contract.APP}-source-{version}.zip', *contract.DOCS,
            *(f'alpine/{arch}.zip' for arch in contract.LINUX)}


def target(version, commit, exact_checkout=True):
    contract.identity(version, commit)
    contract.require(contract.run('git', 'rev-parse', commit + '^{commit}') == commit,
                     'Material commit differs')
    if exact_checkout:
        contract.require(contract.run('git', 'rev-parse', 'HEAD') == commit,
                         'Materials need the exact reviewed checkout')
        contract.require(not contract.run('git', 'status', '--porcelain=v1',
                                           '--untracked-files=normal'),
                         'Material checkout has unreviewed tracked or untracked files')
        subprocess.run(['git', 'diff', '--quiet'], check=True)
        subprocess.run(['git', 'diff', '--cached', '--quiet'], check=True)
    main = contract.run('git', 'show', commit + ':main.go')
    contract.require(re.findall(r'^// Version (\S+)\s*$', main, re.M) == [version],
                     'Material version differs from main.go')
    contract.reviewed_notes(contract.run('git', 'show',
                                         commit + ':plan/release-notes-v0.1.0.md'), version)
    epoch = contract.run('git', 'show', '-s', '--format=%ct', commit)
    contract.require(re.fullmatch('[1-9][0-9]*', epoch), 'Invalid material commit epoch')
    return int(epoch)


def resolve(args):
    contract.require(os.environ.get('GITHUB_REPOSITORY') == REPOSITORY,
                     'Materials workflow is restricted to the target repository')
    contract.require(os.environ.get('GITHUB_REF') == 'refs/heads/main',
                     'Materials dispatch must use main')
    target(args.version, args.commit, exact_checkout=False)
    subprocess.run(['git', 'merge-base', '--is-ancestor', args.commit, 'origin/main'], check=True)
    contract.require(re.fullmatch('[1-9][0-9]*', args.release_id or '')
                     and re.fullmatch('[1-9][0-9]*', args.asset_id or ''), 'Invalid inbox IDs')
    require_hash(args.bundle_sha)
    require_hash(args.materials_sha)
    with Path(os.environ['GITHUB_OUTPUT']).open('a', encoding='utf-8') as output:
        output.write(f'version={args.version}\ncommit={args.commit}\n')


def input_file(root, record, extra_keys=()):
    contract.require(isinstance(record, dict)
                     and set(record) == {'path', 'sha256', *extra_keys},
                     'Reviewed input record differs')
    name = contract.safe(record['path'])
    path = root / name
    contract.require(path.resolve().is_relative_to(root.resolve())
                     and path.is_file() and not path.is_symlink(),
                     'Material inputs must be ordinary files inside the reviewed input directory')
    require_hash(record['sha256'])
    contract.require(contract.digest(path) == record['sha256'], 'Reviewed material input differs')
    return path


def new_directory(value):
    path = Path(value).resolve()
    contract.require(not path.exists(), 'Material output must be new')
    path.mkdir(parents=True, exist_ok=False)
    return path


def copy_new(source, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    with Path(source).open('rb') as src, destination.open('xb') as dst:
        shutil.copyfileobj(src, dst)


def application_files(root):
    """The same committed file selection used by release_contract.source_matches."""
    tracked = subprocess.check_output(['git', '-C', str(root), 'ls-files', '-z']).decode().split('\0')
    selected = {name for name in tracked if name and
                (name in contract.ROOT_INPUTS or name.split('/')[0] in
                 {'.github', 'cmd', 'internal', 'scripts', 'static', 'web'})
                and not name.startswith('static/out/')}
    contract.require(selected, 'Committed application inputs are missing')
    return {contract.safe(name): contract.digest(root / name) for name in sorted(selected)}


def source_application(source, expected):
    """Verify committed source without pretending an old frontend is current."""
    with zipfile.ZipFile(source) as archive:
        names = contract.zip_checksums(archive)
        actual = {name.removeprefix('application/'): contract.sha(archive.read(name))
                  for name in names if name.startswith('application/')
                  and not name.startswith('application/static/out/')}
    contract.require(actual == expected, 'Corresponding committed application differs')


def tool_inputs(data):
    contract.require(isinstance(data, dict) and set(data) == {'schema', 'tools'}
                     and data['schema'] == 1 and set(data['tools']) == set(TOOL_VERSIONS),
                     'Reviewed tool input set differs')
    for name, version in TOOL_VERSIONS.items():
        row = data['tools'][name]
        contract.require(isinstance(row, dict) and set(row) == {'version', 'source', 'sha256'}
                         and row['version'] == version, 'Reviewed tool version differs')
        require_hash(row['sha256'])
        url = urllib.parse.urlsplit(row['source'])
        contract.require(url.scheme == 'https' and url.hostname and not url.username
                         and not url.password and not url.query and not url.fragment,
                         'Tool source must be a public HTTPS location without credentials')
    return data


def observed_tools():
    contract.require_go_toolchain()
    versions = {'go': contract.go_run('env', 'GOVERSION').removeprefix('go'),
                'node': contract.run('node', '--version').removeprefix('v'),
                'pnpm': contract.run('bash', '-c', 'exec pnpm --version'),
                'python': platform.python_version()}
    contract.require(versions == {name: TOOL_VERSIONS[name] for name in RUNTIME_TOOLS},
                     'Actual material tool versions differ')
    host_os = contract.go_run('env', 'GOHOSTOS')
    host_arch = contract.go_run('env', 'GOHOSTARCH')
    goroot = Path(contract.go_run('env', 'GOROOT'))
    suffix = '.exe' if host_os == 'windows' else ''
    binaries = {'go': Path(shutil.which('go')),
                'compile': goroot / f'pkg/tool/{host_os}_{host_arch}/compile{suffix}',
                'link': goroot / f'pkg/tool/{host_os}_{host_arch}/link{suffix}',
                'node': Path(shutil.which('node')), 'python': Path(sys.executable)}
    return {'versions': versions, 'host': host_os + '/' + host_arch,
            'executables': {name: contract.digest(path) for name, path in binaries.items()},
            'go_license_sha256': contract.digest(goroot / 'LICENSE')}


def frontend_files(root):
    files = contract.tree(root / 'static/out')
    contract.require('index.html' in files and any(name.endswith('.js') for name in files),
                     'Actual frontend output is missing')
    return {'static/out/' + name: value for name, value in files.items()}


def source_production(source, version, commit, epoch):
    """Check the production record against actual bytes, without running bundled code."""
    with zipfile.ZipFile(source) as archive:
        names = contract.zip_checksums(archive)
        record = json.loads(archive.read(PRODUCTION_RECORD))
        contract.require(set(record) == {'schema', 'version', 'commit', 'source_date_epoch',
                         'inputs_sha256', 'inputs', 'tools', 'frontend', 'scripts'}
                         and record['schema'] == 1 and record['version'] == version
                         and record['commit'] == commit and record['source_date_epoch'] == epoch,
                         'Material production identity differs')
        require_hash(record['inputs_sha256'])
        required = {'source', 'license_report', 'tool_inputs',
                    *('documents/' + name for name in contract.DOCS),
                    *('alpine/' + arch for arch in contract.LINUX)}
        contract.require(set(record['inputs']) == required, 'Material production inputs differ')
        for checksum in record['inputs'].values():
            require_hash(checksum)
        tools = record['tools']
        contract.require(set(tools) == {'versions', 'host', 'executables', 'go_license_sha256'}
                         and tools['versions'] == {name: TOOL_VERSIONS[name] for name in RUNTIME_TOOLS}
                         and re.fullmatch(r'[a-z0-9]+/[a-z0-9]+', tools['host'])
                         and set(tools['executables']) == {'go', 'compile', 'link', 'node', 'python'},
                         'Material production tool evidence differs')
        for checksum in [tools['go_license_sha256'], *tools['executables'].values()]:
            require_hash(checksum)
        tool_raw = archive.read('tool-inputs.json')
        tool_inputs(json.loads(tool_raw))
        contract.require(contract.sha(tool_raw) == record['inputs']['tool_inputs'],
                         'Reviewed tool input hash differs')
        frontend = {name.removeprefix('application/'): contract.sha(archive.read(name))
                    for name in names if name.startswith('application/static/out/')}
        contract.require(frontend == record['frontend'] and 'static/out/index.html' in frontend
                         and any(name.endswith('.js') for name in frontend),
                         'Material frontend evidence differs')
        scripts = {name: contract.sha(archive.read('application/' + name)) for name in
                   ('scripts/release_materials.py', 'scripts/release_contract.py')}
        contract.require(record['scripts'] == scripts, 'Material producer source differs')
        for name in contract.DOCS:
            contract.require(contract.sha(archive.read(name)) == record['inputs']['documents/' + name],
                             'Material document input hash differs')
        contract.require(contract.sha(archive.read('licenses/THIRD_PARTY_LICENSES.csv')) ==
                         record['inputs']['license_report'], 'Material license input hash differs')
    return record


def produce(args):
    epoch = target(args.version, args.commit)
    spec_path = Path(args.inputs_json).resolve()
    require_hash(args.inputs_sha)
    contract.require(contract.digest(spec_path) == args.inputs_sha, 'Reviewed input manifest differs')
    spec = contract.read_json(spec_path)
    contract.require(set(spec) == {'schema', 'version', 'commit', 'source',
                                   'license_report', 'documents', 'alpine', 'tool_inputs'}
                     and spec['schema'] == 1 and spec['version'] == args.version
                     and spec['commit'] == args.commit, 'Reviewed input identity differs')
    contract.require(set(spec['documents']) == set(contract.DOCS)
                     and set(spec['alpine']) == set(contract.LINUX), 'Reviewed input matrix differs')
    inputs = {f'{contract.APP}-source-{args.version}.zip': input_file(spec_path.parent, spec['source']),
              'THIRD_PARTY_LICENSES.csv': input_file(spec_path.parent, spec['license_report'])}
    tools_path = input_file(spec_path.parent, spec['tool_inputs'])
    tool_inputs(contract.read_json(tools_path))
    for name, row in spec['documents'].items():
        inputs[name] = input_file(spec_path.parent, row)
    for arch, row in spec['alpine'].items():
        contract.require(re.fullmatch(r'alpine@sha256:[0-9a-f]{64}', row['base_image']),
                         'Reviewed Alpine digest is required')
        inputs[f'alpine/{arch}.zip'] = input_file(spec_path.parent, row, ('base_image',))
    output = Path(args.output).resolve()
    contract.require(not output.exists(), 'Material output must be new')
    contract.require(not output.is_relative_to(spec_path.parent.resolve()),
                     'Material output must be separate from reviewed inputs')
    root = Path.cwd()
    committed = application_files(root)
    template = inputs[f'{contract.APP}-source-{args.version}.zip']
    source_application(template, committed)
    with zipfile.ZipFile(template) as archive:
        names = contract.zip_checksums(archive)
        contract.require(PRODUCTION_RECORD not in names and 'tool-inputs.json' not in names,
                         'Source template must not contain a previous production record')
        contract.require(json.loads(archive.read('release-source.json')) ==
                         {'schema': 1, 'version': args.version, 'commit': args.commit,
                          'platforms': list(contract.TARGETS)}, 'Source template identity differs')
        for name in contract.DOCS:
            contract.require(archive.read(name) == inputs[name].read_bytes(), 'Template document differs')
        contract.require(archive.read('licenses/THIRD_PARTY_LICENSES.csv') ==
                         inputs['THIRD_PARTY_LICENSES.csv'].read_bytes(), 'Template license report differs')
    actual_tools = observed_tools()
    # This invokes only the committed frontend builder, never scripts from an input ZIP.
    contract.frontend(argparse.Namespace(version=args.version, commit=args.commit))
    contract.require(target(args.version, args.commit) == epoch and application_files(root) == committed,
                     'Source changed during frontend production')
    frontend = frontend_files(root)
    reviewed = {'source': spec['source']['sha256'], 'license_report': spec['license_report']['sha256'],
                'tool_inputs': spec['tool_inputs']['sha256'],
                **{'documents/' + name: row['sha256'] for name, row in spec['documents'].items()},
                **{'alpine/' + arch: row['sha256'] for arch, row in spec['alpine'].items()}}
    production = {'schema': 1, 'version': args.version, 'commit': args.commit,
                  'source_date_epoch': epoch, 'inputs_sha256': args.inputs_sha, 'inputs': reviewed,
                  'tools': actual_tools, 'frontend': frontend,
                  'scripts': {name: committed[name] for name in
                              ('scripts/release_materials.py', 'scripts/release_contract.py')}}
    output = new_directory(output)
    for name, source in inputs.items():
        if source != template:
            copy_new(source, output / name)
    replacements = {'application/' + name: (root / name).read_bytes() for name in frontend}
    replacements[PRODUCTION_RECORD] = (json.dumps(production, ensure_ascii=False, indent=2) + '\n').encode()
    replacements['tool-inputs.json'] = tools_path.read_bytes()
    source_output = output / template_name(args.version)
    with zipfile.ZipFile(template) as source, zipfile.ZipFile(source_output, 'x', compression=zipfile.ZIP_DEFLATED) as archive:
        names = {name for name in contract.zip_files(source)
                 if name != 'SHA256SUMS' and not name.startswith('application/static/out/')}
        checksums = {}
        for name in sorted(names | set(replacements)):
            info = zipfile.ZipInfo(contract.safe(name), date_time=(2026, 1, 1, 0, 0, 0))
            info.create_system = 3
            mode = 0o644 if name in replacements else (source.getinfo(name).external_attr >> 16) & 0o777
            info.external_attr = (stat.S_IFREG | (mode or 0o644)) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            hasher = hashlib.sha256()
            with archive.open(info, 'w', force_zip64=True) as destination:
                if name in replacements:
                    content = replacements[name]
                    destination.write(content)
                    hasher.update(content)
                else:
                    with source.open(name) as original:
                        while chunk := original.read(1024 * 1024):
                            destination.write(chunk)
                            hasher.update(chunk)
            checksums[name] = hasher.hexdigest()
        info = zipfile.ZipInfo('SHA256SUMS', date_time=(2026, 1, 1, 0, 0, 0))
        info.create_system = 3
        info.external_attr = (stat.S_IFREG | 0o644) << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        archive.writestr(info, ''.join(f'{value}  {name}\n' for name, value in sorted(checksums.items())))
    data = {'schema': 1, 'version': args.version, 'commit': args.commit,
            'source_date_epoch': epoch, 'platforms': list(contract.TARGETS),
            'alpine': {arch: {'base_image': row['base_image']} for arch, row in spec['alpine'].items()},
            'files': contract.tree(output)}
    with (output / 'materials.json').open('x', encoding='utf-8') as stream:
        json.dump(data, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
    material_sha = contract.digest(output / 'materials.json')
    contract.materials(output, args.version, args.commit, material_sha)
    contract.source_matches(root, source_output)
    source_production(source_output, args.version, args.commit, epoch)
    contract.require(contract.digest(spec_path) == args.inputs_sha, 'Input manifest changed during production')
    for record in [spec['source'], spec['license_report'], spec['tool_inputs'], *spec['documents'].values()]:
        input_file(spec_path.parent, record)
    for record in spec['alpine'].values():
        input_file(spec_path.parent, record, ('base_image',))
    print(json.dumps({'materials_sha256': material_sha, 'files': len(data['files']),
                      'source_date_epoch': epoch}))


def template_name(version):
    return f'{contract.APP}-source-{version}.zip'


def audit(args):
    require_hash(args.materials_sha)
    epoch = target(args.version, args.commit)
    data = contract.materials(args.materials, args.version, args.commit, args.materials_sha)
    contract.require(data['source_date_epoch'] == epoch, 'Material commit epoch differs')
    observed_tools()
    production = source_production(Path(args.materials) / template_name(args.version),
                                   args.version, args.commit, epoch)
    for arch in contract.LINUX:
        contract.require(contract.digest(Path(args.materials) / f'alpine/{arch}.zip') ==
                         production['inputs']['alpine/' + arch], 'Reviewed Alpine input differs')
    contract.source_matches(Path.cwd(),
                            Path(args.materials) / f'{contract.APP}-source-{args.version}.zip')
    contract.require(frontend_files(Path.cwd()) == production['frontend'],
                     'Actual rebuilt frontend differs from the material production record')
    print(json.dumps({'version': args.version, 'commit': args.commit,
                      'materials_sha256': args.materials_sha, 'source_date_epoch': epoch,
                      'production_tools': production['tools']['versions'],
                      'frontend_files': len(production['frontend'])}))


def pack(args):
    audit(args)
    root = Path(args.materials).resolve()
    output = Path(args.output).resolve()
    contract.require(not output.exists() and not output.is_relative_to(root),
                     'Material bundle must be new and outside the material directory')
    output.parent.mkdir(parents=True, exist_ok=True)
    # Nested source ZIPs are already compressed. Preserve every reviewed byte.
    with zipfile.ZipFile(output, 'x', compression=zipfile.ZIP_STORED) as bundle:
        for name in sorted(required_files(args.version)):
            info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = 0o100644 << 16
            with (root / name).open('rb') as src, bundle.open(info, 'w', force_zip64=True) as dst:
                shutil.copyfileobj(src, dst)
    contract.require(output.stat().st_size <= MAX_BUNDLE_BYTES, 'Material bundle exceeds staging limit')
    print(json.dumps({'bundle_sha256': contract.digest(output), 'bundle_bytes': output.stat().st_size}))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        return None


def asset_location(value):
    parsed = urllib.parse.urlsplit(value)
    contract.require(parsed.scheme == 'https' and parsed.hostname in ASSET_HOSTS
                     and parsed.port in (None, 443) and not parsed.username
                     and not parsed.password and not parsed.fragment,
                     'Unexpected material asset redirect')
    return value


class AssetRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        asset_location(url)
        redirected = super().redirect_request(request, fp, code, message, headers, url)
        contract.require(not any(key.lower() == 'authorization'
                                 for key in redirected.headers), 'Credential-bearing asset redirect')
        return redirected


def api_request(path, token, binary=False):
    return urllib.request.Request(API + path, headers={
        'Authorization': 'Bearer ' + token, 'User-Agent': contract.APP,
        'Accept': 'application/octet-stream' if binary else 'application/vnd.github+json',
        'X-GitHub-Api-Version': '2022-11-28'})


def download(args):
    contract.identity(args.version, args.commit)
    require_hash(args.bundle_sha)
    contract.require(os.environ.get('GITHUB_REPOSITORY') == REPOSITORY,
                     'Material download is restricted to the target repository')
    contract.require(re.fullmatch('[1-9][0-9]*', args.release_id or '')
                     and re.fullmatch('[1-9][0-9]*', args.asset_id or ''), 'Invalid inbox IDs')
    token = os.environ.get('GH_TOKEN')
    contract.require(bool(token), 'A repository-scoped read token is required')
    output = Path(args.output).resolve()
    contract.require(not output.exists(), 'Material download output must be new')
    api = urllib.request.build_opener(NoRedirect)
    with api.open(api_request('/releases/' + args.release_id, token), timeout=60) as response:
        release = json.load(response)
    contract.require(release['draft'] is True
                     and re.fullmatch(INBOX_TAG, release['tag_name'])
                     and release['target_commitish'] == args.commit
                     and release.get('published_at') is None, 'Unreviewed material inbox')
    contract.require(any(str(row['id']) == args.asset_id for row in release['assets']),
                     'Material asset is not in the reviewed inbox')
    endpoint = '/releases/assets/' + args.asset_id
    with api.open(api_request(endpoint, token), timeout=60) as response:
        metadata = json.load(response)
    expected_name = f'release-materials-{args.version}-{args.commit}.zip'
    contract.require(metadata['url'] == API + endpoint
                     and str(metadata['id']) == args.asset_id
                     and metadata['name'] == expected_name and metadata['state'] == 'uploaded'
                     and isinstance(metadata['size'], int)
                     and 0 < metadata['size'] <= MAX_BUNDLE_BYTES, 'Material asset metadata differs')
    contract.require(metadata.get('digest') in (None, '', 'sha256:' + args.bundle_sha),
                     'GitHub asset digest differs')
    try:
        response = api.open(api_request(endpoint, token, binary=True), timeout=60)
    except urllib.error.HTTPError as error:
        try:
            contract.require(error.code == 302, 'Material asset download failed')
            location = asset_location(error.headers.get('Location', ''))
        finally:
            error.close()
        # The GitHub token stays on api.github.com. Signed locations are not logged.
        response = urllib.request.build_opener(AssetRedirect).open(
            urllib.request.Request(location, headers={'User-Agent': contract.APP}), timeout=60)
    output.parent.mkdir(parents=True, exist_ok=True)
    hasher, count = hashlib.sha256(), 0
    with response, output.open('xb') as stream:
        while chunk := response.read(1024 * 1024):
            count += len(chunk)
            contract.require(count <= metadata['size'], 'Material download size differs')
            hasher.update(chunk)
            stream.write(chunk)
    contract.require(count == metadata['size'] and hasher.hexdigest() == args.bundle_sha,
                     'Material download checksum differs')
    print(json.dumps({'bundle_sha256': args.bundle_sha, 'bundle_bytes': count,
                      'source_release_id': args.release_id, 'source_asset_id': args.asset_id}))


def unpack(args):
    contract.identity(args.version, args.commit)
    require_hash(args.bundle_sha)
    require_hash(args.materials_sha)
    bundle = Path(args.bundle)
    contract.require(bundle.stat().st_size <= MAX_BUNDLE_BYTES
                     and contract.digest(bundle) == args.bundle_sha, 'Material bundle checksum differs')
    with zipfile.ZipFile(bundle) as archive:
        names = set(contract.zip_files(archive))
        contract.require(names == required_files(args.version), 'Material bundle file set differs')
        contract.require(sum(info.file_size for info in archive.infolist()) <= MAX_BUNDLE_BYTES,
                         'Expanded material bundle exceeds limit')
        output = new_directory(args.output)
        for name in sorted(names):
            destination = output / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            with archive.open(name) as src, destination.open('xb') as dst:
                shutil.copyfileobj(src, dst)
    contract.materials(output, args.version, args.commit, args.materials_sha)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('resolve', 'produce', 'audit', 'pack', 'download', 'unpack'))
    parser.add_argument('--version', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--inputs-json')
    parser.add_argument('--inputs-sha')
    parser.add_argument('--materials')
    parser.add_argument('--materials-sha')
    parser.add_argument('--bundle')
    parser.add_argument('--bundle-sha')
    parser.add_argument('--release-id')
    parser.add_argument('--asset-id')
    parser.add_argument('--output')
    args = parser.parse_args()
    try:
        globals()[args.command](args)
    except (contract.Invalid, KeyError, TypeError, OSError, ValueError,
            subprocess.CalledProcessError, zipfile.BadZipFile) as error:
        print(f'release_materials: failed ({type(error).__name__})')
        if isinstance(error, contract.Invalid):
            print(str(error))
        raise SystemExit(2)
    print(f'release_materials: {args.command} passed')


if __name__ == '__main__':
    main()
