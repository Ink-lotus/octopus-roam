"""Local, fail-closed contracts for a manually approved release (stdlib only)."""
import argparse
import base64
import csv
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import tarfile
import urllib.error
import urllib.request
import zipfile

APP = 'octopus-roam'
MODULE = 'github.com/Ink-lotus/octopus-roam'
TARGETS = ('linux-amd64', 'linux-arm64', 'linux-arm', 'linux-386',
           'windows-amd64', 'darwin-arm64', 'darwin-amd64',
           'android-amd64', 'android-arm64', 'android-arm', 'android-386')
LINUX = {'amd64': 'x86_64', 'arm64': 'aarch64', 'arm': 'armv7', '386': 'x86'}
DOCS = ('THIRD-PARTY-NOTICES.md', 'SOURCE-OFFER.md', 'source-offer.html', 'REBUILD.md', 'INSTALL.md')
ROOT_INPUTS = {'main.go', 'go.mod', 'go.sum', 'LICENSE', 'README.md', 'README_zh.md',
               'docker-compose.yml', '.dockerignore'}


class Invalid(ValueError):
    pass


def require(value, reason):
    if not value:
        raise Invalid(reason)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def digest(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def safe(name):
    require(isinstance(name, str) and name and '\\' not in name, 'Invalid relative path')
    p = PurePosixPath(name)
    require(not p.is_absolute() and str(p) == name and '..' not in p.parts
            and all(':' not in part for part in p.parts), 'Unsafe relative path')
    return name


def read_json(path):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, 'Duplicate JSON key')
            result[key] = value
        return result
    return json.loads(Path(path).read_text(encoding='utf-8'), object_pairs_hook=unique)


def write_json(path, data):
    Path(path).write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def identity(version, commit):
    require(re.fullmatch(r'v0\.1\.0(?:-rc\.[1-9][0-9]*)?', version),
            'This first-release contract accepts v0.1.0 or v0.1.0-rc.N only')
    require(re.fullmatch('[0-9a-f]{40}', commit), 'A full lowercase commit SHA is required')


def reviewed_notes(notes, version):
    require(notes.strip(), 'Release Notes are empty')
    versions = re.findall(r'v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?', notes.splitlines()[0])
    require(versions == [version], 'Release Notes title must name the exact version')
    require(not any(marker in notes for marker in ('草稿', '待发布', '尚未发布')), 'Release Notes need review')


def prepare(args):
    identity(args.version, args.commit)
    require(os.environ.get('GITHUB_REF') == 'refs/heads/main', 'Dispatch must use main')
    require(run('git', 'rev-parse', args.commit + '^{commit}') == args.commit, 'Commit differs')
    subprocess.run(['git', 'merge-base', '--is-ancestor', args.commit, 'origin/main'], check=True)
    text = run('git', 'show', args.commit + ':main.go')
    require(re.findall(r'^// Version (\S+)\s*$', text, re.M) == [args.version], 'main.go version differs')
    notes = run('git', 'show', args.commit + ':plan/release-notes-v0.1.0.md')
    reviewed_notes(notes, args.version)
    tags = run('git', 'tag', '--list', args.version)
    require(not tags, 'Tag already exists; do not overwrite')
    require(re.fullmatch('[1-9][0-9]*', args.materials_run), 'Invalid materials run ID')
    require(re.fullmatch('[0-9a-f]{64}', args.materials_sha), 'Invalid materials manifest SHA-256')
    outputs = {'version': args.version, 'commit': args.commit,
               'prerelease': str('-rc.' in args.version).lower(),
               'artifact': f'release-{args.version}-{args.commit}',
               'materials_run': args.materials_run, 'materials_sha': args.materials_sha}
    with Path(os.environ['GITHUB_OUTPUT']).open('a', encoding='utf-8') as output:
        for key, value in outputs.items():
            output.write(f'{key}={value}\n')


def tree(root):
    result = {}
    for path in sorted(Path(root).rglob('*')):
        require(not path.is_symlink(), 'Symlink in artifact')
        if path.is_file():
            name = safe(path.relative_to(root).as_posix())
            result[name] = digest(path)
    return result


def zip_files(archive):
    names = set()
    for info in archive.infolist():
        name = info.filename.rstrip('/') if info.is_dir() else info.filename
        safe(name)
        require(name not in names, 'Duplicate ZIP entry')
        names.add(name)
        mode = info.external_attr >> 16
        require(stat.S_IFMT(mode) in (0, stat.S_IFREG, stat.S_IFDIR), 'Non-regular ZIP entry')
        if not info.is_dir():
            yield info.filename


def zip_checksums(archive, manifest='SHA256SUMS'):
    names = set(zip_files(archive))
    require(manifest in names, 'Source checksum manifest missing')
    expected = {}
    for line in archive.read(manifest).decode('utf-8-sig').splitlines():
        checksum, name = line.split(maxsplit=1)
        name = safe(name.removeprefix('*').removeprefix('./'))
        require(name not in expected and re.fullmatch('[0-9a-f]{64}', checksum), 'Invalid checksum row')
        expected[name] = checksum
    require(set(expected) == names - {manifest}, 'Source file set differs from manifest')
    for name, checksum in expected.items():
        with archive.open(name) as stream:
            require(hashlib.file_digest(stream, 'sha256').hexdigest() == checksum, 'Source checksum differs')
    return names


def apk_inventory(raw):
    result = []
    for paragraph in raw.decode('utf-8').strip().split('\n\n'):
        fields = {}
        # Header keys are case-sensitive; stop before F/R/a file metadata.
        for line in paragraph.splitlines():
            if line.startswith('F:'):
                break
            key, sep, value = line.partition(':')
            if sep:
                require(key not in fields, 'Duplicate APK header')
                fields[key] = value
        keys = {'name': 'P', 'version': 'V', 'architecture': 'A', 'origin': 'o',
                'aports_commit': 'c', 'license': 'L'}
        require(all(fields.get(key) for key in keys.values()), 'Incomplete APK header')
        result.append({name: fields[key] for name, key in keys.items()})
    require(result and len({p['name'] for p in result}) == len(result), 'Empty/duplicate APK inventory')
    return sorted(result, key=lambda p: p['name'])


def alpine_source(path, arch):
    with zipfile.ZipFile(path) as archive:
        names = set(zip_files(archive))
        records = json.loads(archive.read('FILE-SHA256SUMS.json'))
        require(names == {r['path'] for r in records} | {'FILE-SHA256SUMS.json'}, 'OS source set differs')
        require(len({r['path'] for r in records}) == len(records), 'Duplicate OS source record')
        for record in records:
            data = archive.read(safe(record['path']))
            require(len(data) == record['bytes'] and sha(data) == record['sha256'], 'OS source checksum differs')
        inventory = json.loads(archive.read('ALPINE-PACKAGES.json'))['packages']
        keys = ('name', 'version', 'architecture', 'origin', 'aports_commit', 'license')
        packages = sorted(({key: row[key] for key in keys} for row in inventory), key=lambda p: p['name'])
        require(packages and all(p['architecture'] == LINUX[arch] for p in packages), 'OS architecture differs')
        bindings = json.loads(archive.read('SOURCE-BINDINGS.json'))
        require({p['origin'] for p in packages} == {b['origin'] for b in bindings}, 'Missing source origin')
        for binding in bindings:
            require(binding['recipe_path'] in names, 'APKBUILD missing')
            for p in packages:
                if p['origin'] == binding['origin']:
                    require(p['aports_commit'] == binding['aports_commit'], 'aports commit differs')
            for source in binding['inputs']:
                require(hashlib.sha512(archive.read(safe(source['path']))).hexdigest() == source['sha512'],
                        'APKBUILD source input differs')
        for record in json.loads(archive.read('GITMODES.json')):
            data = archive.read(safe(record['path']))
            require(hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest()
                    == record['git_blob_sha1'], 'aports Git blob differs')
        notice_index = json.loads(archive.read('NOTICE-INDEX.json'))
        require(notice_index['original_notices'], 'Original OS notices missing')
        for notice in notice_index['original_notices']:
            require(sha(archive.read(safe(notice['path']))) == notice['sha256'], 'OS notice differs')
        require({'README.md', 'REBUILD.md', 'NOTICES.md', 'SOURCE-ARCHIVE-FILES.json'} <= names, 'OS instructions missing')
        for source, records in json.loads(archive.read('SOURCE-ARCHIVE-FILES.json')).items():
            with tarfile.open(fileobj=io.BytesIO(archive.read(safe(source)))) as upstream:
                members = upstream.getmembers()
                require(len(members) == len(records), 'Upstream source member count differs')
                for member, record in zip(members, records):
                    require(member.name == record['path'], 'Upstream source member differs')
                    if member.isfile():
                        require(sha(upstream.extractfile(member).read()) == record['sha256'], 'Upstream source file differs')
                    elif member.issym() or member.islnk():
                        require(member.linkname == record['link_target'], 'Upstream source link differs')
        return packages


def materials(root, version, commit, expected_sha):
    identity(version, commit)
    root = Path(root)
    require(digest(root / 'materials.json') == expected_sha, 'Materials root hash differs')
    data = read_json(root / 'materials.json')
    require(data['schema'] == 1 and data['version'] == version and data['commit'] == commit, 'Stale materials')
    require(data['platforms'] == list(TARGETS), 'Materials must cover all eleven targets')
    require(isinstance(data['source_date_epoch'], int) and data['source_date_epoch'] > 0, 'Invalid build epoch')
    actual = tree(root)
    actual.pop('materials.json')
    require(actual == data['files'], 'Materials file set/hash differs')
    required = {'THIRD_PARTY_LICENSES.csv', f'{APP}-source-{version}.zip', *DOCS,
                *(f'alpine/{arch}.zip' for arch in LINUX)}
    require(set(actual) == required, 'Unexpected/missing material files')
    with (root / 'THIRD_PARTY_LICENSES.csv').open(encoding='utf-8-sig', newline='') as stream:
        require(len(list(csv.reader(stream))) > 1, 'Empty license report')
    for name in DOCS:
        require((root / name).stat().st_size > 0, 'Empty material document')
    require(set(data['alpine']) == set(LINUX), 'OS platform coverage differs')
    for arch, row in data['alpine'].items():
        require(re.fullmatch(r'alpine@sha256:[0-9a-f]{64}', row['base_image']), 'Alpine base must be digest pinned')
        alpine_source(root / f'alpine/{arch}.zip', arch)
    source = root / f'{APP}-source-{version}.zip'
    with zipfile.ZipFile(source) as archive:
        names = zip_checksums(archive)
        binding = json.loads(archive.read('release-source.json'))
        require(binding == {'schema': 1, 'version': version, 'commit': commit, 'platforms': list(TARGETS)},
                'Application source binding differs')
        require({'REBUILD.md', 'INSTALL.md', 'licenses/AGPL-3.0.txt', 'licenses/LGPL-3.0.txt',
                 'licenses/GPL-3.0.txt', 'licenses/MPL-2.0.txt'} <= names, 'Source/license instructions missing')
        require(any(n.startswith('go-cache/') and n.endswith('.zip') for n in names), 'Go corresponding source missing')
        require(any(n.startswith('npm-packages/') for n in names), 'Frontend corresponding source missing')
        require(archive.read('licenses/THIRD_PARTY_LICENSES.csv') == (root / 'THIRD_PARTY_LICENSES.csv').read_bytes(),
                'License report differs from corresponding source')
        for name in DOCS:
            require(archive.read(name) == (root / name).read_bytes(), 'Source document differs')
    return data


def source_matches(root, source):
    tracked = subprocess.check_output(['git', '-C', str(root), 'ls-files', '-z']).decode().split('\0')
    selected = {n for n in tracked if n and (n in ROOT_INPUTS or n.split('/')[0] in
                {'.github', 'cmd', 'internal', 'scripts', 'static', 'web'}) and not n.startswith('static/out/')}
    selected |= {p.relative_to(root).as_posix() for p in (root / 'static/out').rglob('*') if p.is_file()}
    with zipfile.ZipFile(source) as archive:
        actual = {n.removeprefix('application/') for n in zip_files(archive) if n.startswith('application/')}
        require(actual == selected, 'Corresponding application file set differs')
        for name in selected:
            require(sha(archive.read('application/' + name)) == digest(root / name), 'Corresponding application input differs')


def archive_write(path, entries):
    with zipfile.ZipFile(path, 'x', compression=zipfile.ZIP_DEFLATED) as archive:
        for name, (data, mode) in sorted(entries.items()):
            info = zipfile.ZipInfo(safe(name), date_time=(2026, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = (stat.S_IFREG | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, data)


def frontend(args):
    # Vite emptyOutDir removes the tracked sentinel too. Restore its exact bytes
    # before Go reads VCS state, without hiding any source/lock-file changes.
    sentinel = Path('static/out/README.md')
    original = sentinel.read_bytes() if sentinel.is_file() else None
    try:
        subprocess.run(['bash', '-c', 'exec pnpm --dir web run build'], check=True,
                       env={**os.environ, 'VITE_APP_VERSION': args.version,
                            'pnpm_config_verify_deps_before_run': 'error'})
    finally:
        if original is not None:
            sentinel.parent.mkdir(parents=True, exist_ok=True)
            sentinel.write_bytes(original)


def module_hash(data, module, version):
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        names = sorted(zip_files(archive))
        require(names and all(n.startswith(f'{module}@{version}/') for n in names), 'Go module ZIP prefix differs')
        hashed = hashlib.sha256()
        for name in names:
            hashed.update(f'{sha(archive.read(name))}  {name}\n'.encode())
        return 'h1:' + base64.b64encode(hashed.digest()).decode()


def dependency_sources(info, source_path, cache):
    dependencies = []
    for line in info.splitlines():
        fields = line.split()
        if fields and fields[0] == 'dep':
            dependencies.append(fields[1:])
        elif fields and fields[0] == '=>':
            require(dependencies, 'Replacement without dependency')
            dependencies[-1] = fields[1:]
    with zipfile.ZipFile(source_path) as source:
        sums = set(source.read('application/go.sum').decode().splitlines())
        for fields in dependencies:
            require(len(fields) == 3 and fields[2].startswith('h1:'), 'Unversioned/local replacement needs source review')
            module, version, expected = fields
            key = (module, version, expected)
            require(f'{module} {version} {expected}' in sums, 'Actual binary dependency differs from source go.sum')
            if key not in cache:
                escaped = ''.join('!' + c.lower() if c.isupper() else c for c in module)
                name = f'go-cache/cache/download/{escaped}/@v/{version}.zip'
                require(module_hash(source.read(safe(name)), module, version) == expected, 'Actual binary dependency source h1 differs')
                cache[key] = True
    return len(dependencies)


def package(args):
    root, out = Path.cwd(), Path(args.output).resolve()
    delivery = out / 'archives'
    require(not any(delivery.iterdir()), 'Archive output must be empty')
    data = None
    if args.materials:
        data = materials(args.materials, args.version, args.commit, args.materials_sha)
        require(run('git', 'rev-parse', 'HEAD') == args.commit, 'Build checkout differs')
        require(str(data['source_date_epoch']) == run('git', 'show', '-s', '--format=%ct', args.commit)
                == os.environ.get('SOURCE_DATE_EPOCH'), 'Reviewed build epoch differs')
        source_matches(root, Path(args.materials) / f'{APP}-source-{args.version}.zip')
    targets = TARGETS[:7] if args.standard_only else TARGETS
    binaries = {}
    module_cache = {}
    for target in targets:
        suffix = '.exe' if target.startswith('windows-') else ''
        binary = out / 'bin' / f'{APP}-{target}{suffix}'
        info = run('go', 'version', '-m', str(binary))
        # -trimpath omits ldflags from Go build metadata. Use an explicit build ID,
        # and keep the native runtime version check in the build-contract fixture.
        require(run('go', 'tool', 'buildid', str(binary)) == f'{APP}/{args.version}/{args.commit}',
                'Binary build identity differs')
        os_name, goarch = target.split('-')
        require(f'GOOS={os_name}' in info and f'GOARCH={goarch}' in info, 'Binary target differs')
        if data:
            require(f'vcs.revision={args.commit}' in info and 'vcs.modified=false' in info,
                    'Binary source revision differs or checkout was dirty')
            dependency_sources(info, Path(args.materials) / f'{APP}-source-{args.version}.zip', module_cache)
        executable = APP + suffix
        entries = {executable: (binary.read_bytes(), 0o755)}
        for name in ('README.md', 'README_zh.md', 'LICENSE'):
            content = (root / name).read_bytes()
            if data and name.startswith('README'):
                content += b'\n\n' + b'\n\n'.join((Path(args.materials) / n).read_bytes() for n in DOCS[:2])
            entries[name] = (content, 0o644)
        entries['THIRD_PARTY_LICENSES.csv'] = ((out / 'THIRD_PARTY_LICENSES.csv').read_bytes(), 0o644)
        if suffix:
            entries[f'{APP}.cmd'] = ((root / 'scripts/windows' / f'{APP}.cmd').read_bytes(), 0o644)
            for name in (f'{APP}-menu.ps1', f'{APP}-start.ps1', f'{APP}-autostart.ps1', 'send-stop-signal.ps1'):
                entries['windows/' + name] = ((root / 'scripts/windows' / name).read_bytes(), 0o644)
        archive_write(delivery / f'{APP}-{target}.zip', entries)
        binaries[target] = digest(binary)
    sums = ''.join(f'{digest(path)}  {path.name}\n' for path in sorted(delivery.glob('*.zip')))
    (delivery / 'SHA256SUMS').write_text(sums, encoding='utf-8')
    write_json(out / 'build.json', {'schema': 1, 'version': args.version, 'commit': args.commit,
                                  'platforms': list(targets), 'binaries': binaries})
    if data:
        context = out / 'docker-context'
        context.mkdir()
        shutil.copytree(root / 'scripts/dockerfile', context / 'scripts/dockerfile')
        shutil.copyfile(root / 'LICENSE', context / 'LICENSE')
        shutil.copyfile(root / 'docker-compose.yml', context / 'docker-compose.yml')
        shutil.copytree(out / 'docker', context / 'build/docker')
        shutil.copyfile(out / 'THIRD_PARTY_LICENSES.csv', context / 'build/THIRD_PARTY_LICENSES.csv')
        notices = context / 'build/release-notices'
        notices.mkdir()
        with zipfile.ZipFile(Path(args.materials) / f'{APP}-source-{args.version}.zip') as archive:
            for name in zip_files(archive):
                if name.startswith('licenses/'):
                    dest = notices / safe(name)
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    dest.write_bytes(archive.read(name))
        for name in DOCS:
            shutil.copyfile(Path(args.materials) / name, notices / name)
        for arch in LINUX:
            platform = 'linux/arm/v7' if arch == 'arm' else f'linux/{arch}'
            with zipfile.ZipFile(Path(args.materials) / f'alpine/{arch}.zip') as archive:
                for name in zip_files(archive):
                    if name.startswith('notices/') or name in ('ALPINE-PACKAGES.json', 'NOTICES.md', 'NOTICE-INDEX.json'):
                        dest = context / 'build/alpine' / platform / safe(name)
                        dest.parent.mkdir(parents=True, exist_ok=True)
                        dest.write_bytes(archive.read(name))


def image_files(path, wanted, arch, version, commit):
    # Inspect layers without extracting or starting a container.
    result = {}
    with tarfile.open(path) as outer:
        manifests = json.load(outer.extractfile('manifest.json'))
        require(len(manifests) == 1, 'Expected exactly one Docker image')
        entry = manifests[0]
        config_raw = outer.extractfile(safe(entry['Config'])).read()
        config_id = sha(config_raw)
        require(entry['Config'] in (config_id + '.json', 'blobs/sha256/' + config_id), 'Docker configuration digest differs')
        config = json.loads(config_raw)
        require(config['os'] == 'linux' and config['architecture'] == arch, 'Docker image architecture differs')
        if arch == 'arm':
            require(config.get('variant') == 'v7', 'Docker ARM variant differs')
        labels = config['config'].get('Labels', {})
        require(labels.get('org.opencontainers.image.version') == version and
                labels.get('org.opencontainers.image.revision') == commit, 'Docker labels differ')
        repository = os.environ['GITHUB_REPOSITORY'].lower()
        require(entry['RepoTags'] == [f'ghcr.io/{repository}:{version}-{arch}'], 'Docker image tag differs')
        diff_ids = config['rootfs']['diff_ids']
        require(len(diff_ids) == len(entry['Layers']), 'Docker layer count differs')
        for name, expected in zip(entry['Layers'], diff_ids):
            raw = outer.extractfile(safe(name)).read()
            if raw.startswith(b'\x1f\x8b'):
                raw = gzip.decompress(raw)
            require('sha256:' + sha(raw) == expected, 'Docker layer digest differs')
            with tarfile.open(fileobj=io.BytesIO(raw)) as layer:
                members = layer.getmembers()
                # OCI whiteouts apply to the preceding layers, not same-layer additions.
                for member in members:
                    normalized = member.name.removeprefix('./').lstrip('/')
                    parent, _, leaf = normalized.rpartition('/')
                    if leaf.startswith('.wh.'):
                        for key in list(result):
                            target = (parent + '/' if parent else '') + leaf[4:]
                            if key == target or key.startswith(target + '/') or (leaf == '.wh..wh..opq' and (not parent or key.startswith(parent + '/'))):
                                del result[key]
                for member in members:
                    name = member.name.removeprefix('./').lstrip('/')
                    require(member.isdir() or not any(n.startswith(name + '/') for n in wanted),
                            'Required image parent is not a directory')
                    if name in wanted:
                        require(member.isfile(), 'Expected image file is not regular')
                        if name in (f'app/{APP}', 'entrypoint.sh'):
                            require(member.mode & 0o111, 'Image executable mode missing')
                        result[name] = layer.extractfile(member).read()
    require(set(result) == set(wanted), 'Required image file missing')
    return result


def verify_images(root, material_root, version, commit):
    result = {}
    for arch in LINUX:
        context = root / 'docker-context'
        platform = 'linux/arm/v7' if arch == 'arm' else f'linux/{arch}'
        wanted = {f'app/{APP}': (root / 'bin' / f'{APP}-linux-{arch}').read_bytes(),
                  'app/LICENSE': (context / 'LICENSE').read_bytes(),
                  'app/THIRD_PARTY_LICENSES.csv': (root / 'THIRD_PARTY_LICENSES.csv').read_bytes(),
                  'entrypoint.sh': (context / 'scripts/dockerfile/entrypoint.sh').read_bytes()}
        for directory, prefix in ((context / 'build/release-notices', 'app/'),
                                  (context / 'build/alpine' / platform, 'app/alpine/')):
            for path in directory.rglob('*'):
                if path.is_file():
                    wanted[prefix + path.relative_to(directory).as_posix()] = path.read_bytes()
        actual = image_files(root / 'images' / f'{arch}.tar', [*wanted, 'lib/apk/db/installed'], arch, version, commit)
        require(all(actual[n] == data for n, data in wanted.items()), 'Image payload differs')
        packages = alpine_source(material_root / f'alpine/{arch}.zip', arch)
        require(apk_inventory(actual['lib/apk/db/installed']) == packages, 'Actual installed OS packages differ from source')
        image_path = root / 'images' / f'{arch}.tar'
        with tarfile.open(image_path) as archive:
            entry = json.load(archive.extractfile('manifest.json'))[0]
            config_id = 'sha256:' + sha(archive.extractfile(entry['Config']).read())
        result[arch] = {'image_id': config_id, 'image_tar_sha256': digest(image_path),
                        'alpine_source_sha256': digest(material_root / f'alpine/{arch}.zip'),
                        'installed_db_sha256': sha(actual['lib/apk/db/installed']), 'packages': packages}
    return result


def verify_archives(root, version, commit):
    build = read_json(root / 'build.json')
    require(build['schema'] == 1 and build['version'] == version and build['commit'] == commit,
            'Artifact version/commit differs')
    require(build['platforms'] == list(TARGETS) and set(build['binaries']) == set(TARGETS), 'Incomplete release matrix')
    archives = root / 'archives'
    require({p.name for p in archives.iterdir()} == {f'{APP}-{t}.zip' for t in TARGETS} | {'SHA256SUMS'},
            'Archive allowlist differs')
    sums = ''.join(f'{digest(p)}  {p.name}\n' for p in sorted(archives.glob('*.zip')))
    require((archives / 'SHA256SUMS').read_text() == sums, 'Archive checksums differ')
    for target in TARGETS:
        executable = APP + ('.exe' if target.startswith('windows-') else '')
        expected = {executable, 'README.md', 'README_zh.md', 'LICENSE', 'THIRD_PARTY_LICENSES.csv'}
        if target.startswith('windows-'):
            expected |= {f'{APP}.cmd', *(f'windows/{n}' for n in
                         (f'{APP}-menu.ps1', f'{APP}-start.ps1', f'{APP}-autostart.ps1', 'send-stop-signal.ps1'))}
        with zipfile.ZipFile(archives / f'{APP}-{target}.zip') as archive:
            require(set(zip_files(archive)) == expected, 'Updater archive allowlist differs')
            require(sha(archive.read(executable)) == build['binaries'][target], 'Archive binary differs')
            require(archive.read('THIRD_PARTY_LICENSES.csv') == (root / 'THIRD_PARTY_LICENSES.csv').read_bytes(),
                    'Archive license report differs')
            for info in archive.infolist():
                mode = 0o755 if info.filename == executable else 0o644
                require(info.create_system == 3 and info.external_attr >> 16 == (stat.S_IFREG | mode),
                        'Archive Unix mode differs')


def seal_or_verify(args):
    root = Path(args.output).resolve()
    identity(args.version, args.commit)
    material_root = root / 'materials'
    materials(material_root, args.version, args.commit, args.materials_sha)
    verify_archives(root, args.version, args.commit)
    images = verify_images(root, material_root, args.version, args.commit)
    if args.command == 'seal':
        require(not (root / 'artifact.json').exists(), 'Artifact already sealed')
        delivery = root / 'delivery'
        shutil.copytree(root / 'archives', delivery)
        for path in material_root.iterdir():
            if path.is_file() and path.name != 'materials.json':
                shutil.copyfile(path, delivery / path.name)
        for arch in LINUX:
            shutil.copyfile(material_root / f'alpine/{arch}.zip', delivery / f'{APP}-alpine-source-{arch}-{args.version}.zip')
        shutil.copyfile('plan/release-notes-v0.1.0.md', delivery / 'RELEASE-NOTES.md')
        shutil.copyfile(root / 'build.json', delivery / 'BUILD.json')
        shutil.copyfile(material_root / 'materials.json', delivery / 'MATERIALS.json')
        write_json(delivery / 'IMAGE-MANIFEST.json', images)
        (delivery / 'SHA256SUMS').write_text(''.join(f'{digest(p)}  {p.name}\n' for p in sorted(delivery.iterdir())
                                                  if p.name != 'SHA256SUMS'), encoding='utf-8')
        write_json(root / 'artifact.json', {'schema': 1, 'version': args.version, 'commit': args.commit,
                                          'materials_sha256': args.materials_sha, 'files': tree(root)})
    else:
        manifest = read_json(root / 'artifact.json')
        actual = tree(root)
        actual.pop('artifact.json')
        require(manifest == {'schema': 1, 'version': args.version, 'commit': args.commit,
                             'materials_sha256': args.materials_sha, 'files': actual}, 'Artifact file set/hash differs')
        require((root / 'delivery/RELEASE-NOTES.md').read_bytes() == Path('plan/release-notes-v0.1.0.md').read_bytes(),
                'Reviewed Release Notes differ')
        require(read_json(root / 'delivery/IMAGE-MANIFEST.json') == images, 'Published image/source mapping differs')


def remote_check(args):
    identity(args.version, args.commit)
    repository = os.environ['GITHUB_REPOSITORY']
    require(re.fullmatch('[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository), 'Invalid repository')
    for resource in (f'git/ref/tags/{args.version}', f'releases/tags/{args.version}'):
        request = urllib.request.Request(f'https://api.github.com/repos/{repository}/{resource}',
                                         headers={'Authorization': 'Bearer ' + os.environ['GH_TOKEN'],
                                                  'Accept': 'application/vnd.github+json', 'User-Agent': APP})
        try:
            with urllib.request.urlopen(request, timeout=30):
                raise Invalid('Tag/Release already exists; do not overwrite')
        except urllib.error.HTTPError as error:
            try:
                require(error.code == 404, f'Remote preflight HTTP {error.code}')
            finally:
                error.close()
    # A previous partial publish may have images without a Git tag. Never overwrite these.
    credentials = base64.b64encode((os.environ['GITHUB_ACTOR'] + ':' + os.environ['GH_TOKEN']).encode()).decode()
    image = repository.lower()
    request = urllib.request.Request(f'https://ghcr.io/token?service=ghcr.io&scope=repository:{image}:pull',
                                     headers={'Authorization': 'Basic ' + credentials, 'User-Agent': APP})
    with urllib.request.urlopen(request, timeout=30) as response:
        token = json.load(response)['token']
    for tag in [args.version, *(args.version + '-' + arch for arch in LINUX)]:
        request = urllib.request.Request(f'https://ghcr.io/v2/{image}/manifests/{tag}',
                                         headers={'Authorization': 'Bearer ' + token,
                                                  'Accept': 'application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.v2+json'})
        try:
            with urllib.request.urlopen(request, timeout=30):
                raise Invalid('Version image exists; do not overwrite')
        except urllib.error.HTTPError as error:
            try:
                require(error.code == 404, f'Registry preflight HTTP {error.code}')
            finally:
                error.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('prepare', 'materials', 'frontend', 'package', 'seal', 'verify', 'remote-check'))
    parser.add_argument('--version', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--output')
    parser.add_argument('--materials')
    parser.add_argument('--materials-sha')
    parser.add_argument('--materials-run')
    parser.add_argument('--standard-only', action='store_true')
    args = parser.parse_args()
    try:
        if args.command == 'prepare':
            prepare(args)
        elif args.command == 'materials':
            materials(args.materials, args.version, args.commit, args.materials_sha)
        elif args.command == 'package':
            package(args)
        elif args.command == 'frontend':
            frontend(args)
        elif args.command in ('seal', 'verify'):
            seal_or_verify(args)
        else:
            remote_check(args)
    except (Invalid, KeyError, OSError, ValueError, subprocess.CalledProcessError, zipfile.BadZipFile, tarfile.TarError) as error:
        # Avoid credential-bearing HTTP errors / untrusted path text in logs.
        print(f'release_contract: failed ({type(error).__name__})')
        if isinstance(error, Invalid):
            print(str(error))
        raise SystemExit(2)
    print(f'release_contract: {args.command} passed')


if __name__ == '__main__':
    main()
