"""Select conservative CI checks and validate documentation without dependencies."""
import argparse
from dataclasses import dataclass
import json
import os
from pathlib import Path
import posixpath
import re
import subprocess
from urllib.parse import unquote, urlsplit


DOCUMENTS = {'README.md', 'README_zh.md', 'CHANGELOG.md', 'web/README.md'}
SHA = re.compile(r'[0-9a-f]{40}')
FENCE = re.compile(r'^ {0,3}(`{3,}|~{3,})(.*)$')
LINK = re.compile(r'!?\[[^\]\n]*\]\(\s*(?:<([^>\n]+)>|([^\s)]+))')


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], stderr=subprocess.PIPE)


def document(path):
    if '\\' in path or any(ord(char) < 32 for char in path):
        return False
    return path in DOCUMENTS or (path.startswith('docs/') and path.endswith('.md')
        and not path.startswith('docs/archive/'))


@dataclass(frozen=True)
class Change:
    path: str
    before_mode: str
    after_mode: str


@dataclass(frozen=True)
class Selection:
    docs_only: bool = False
    base: str = ''
    reason: str = 'Full tests required'


def changes(root, base, head):
    if not SHA.fullmatch(base) or not SHA.fullmatch(head):
        raise ValueError('A full commit SHA is required')
    fields = git(root, 'diff', '--raw', '--no-abbrev', '--no-renames',
        '--no-ext-diff', '--no-textconv', '-z', base, head, '--').split(b'\0')
    result = []
    for index in range(0, len(fields) - 1, 2):
        metadata = fields[index].decode('ascii').split()
        if len(metadata) != 5 or not metadata[0].startswith(':'):
            raise ValueError('Unrecognized Git change record')
        result.append(Change(fields[index + 1].decode('utf-8'),
            metadata[0][1:], metadata[1]))
    return result


def select(root, event_name, event, head, force_full=False):
    if force_full or event_name not in {'push', 'pull_request'}:
        return Selection(reason='Release/reusable or other event: full tests required')
    try:
        if not SHA.fullmatch(head):
            raise ValueError('Invalid checked-out commit')
        if event_name == 'pull_request':
            base = event['pull_request']['base']['sha']
            # The checkout is GitHub's proposed merge, including the whole PR.
            git(root, 'merge-base', '--is-ancestor', base, head)
        elif event['after'] != head or not event['ref'].startswith('refs/heads/'):
            return Selection(reason='Tag or inconsistent push: full tests required')
        elif event['ref'] == 'refs/heads/main':
            base = event['before']
            if base == '0' * 40:
                return Selection(reason='Initial main push: full tests required')
            git(root, 'merge-base', '--is-ancestor', base, head)
        else:
            # Include ALL outstanding branch changes, even on its first push or
            # when a docs commit follows a code commit that failed its tests.
            base = git(root, 'merge-base', 'refs/remotes/origin/main', head).decode().strip()
        modified = changes(root, base, head)
        if not modified:
            return Selection(reason='Empty change range: full tests required')
        docs_only = all(document(change.path)
            and {change.before_mode, change.after_mode} <= {'000000', '100644'}
            for change in modified)
        return Selection(docs_only, base,
            'Only maintained Markdown changed' if docs_only else 'Code, configuration or other inputs changed')
    except (AttributeError, KeyError, TypeError, ValueError, OSError, subprocess.CalledProcessError):
        return Selection(reason='Change range unavailable: full tests required')


def markdown_prose(text, path):
    """Ignore fenced examples when finding prose links and conflict markers."""
    prose = []
    opened = None
    for line in text.splitlines():
        match = FENCE.match(line)
        if opened:
            if match and match[1][0] == opened[0] and len(match[1]) >= len(opened) and not match[2].strip():
                opened = None
            continue
        if match:
            opened = match[1]
        else:
            if line.startswith(('<<<<<<< ', '>>>>>>> ')) or line == '=======':
                raise ValueError(f'{path}: unresolved merge conflict')
            prose.append(re.sub(r'(`+).*?\1', '', line))
    if opened:
        raise ValueError(f'{path}: unclosed Markdown code fence')
    return '\n'.join(prose)


def check_docs(root, base, head):
    modified = changes(root, base, head)
    if not modified or not all(document(change.path)
            and {change.before_mode, change.after_mode} <= {'000000', '100644'} for change in modified):
        raise ValueError('Documentation checks require a nonempty, documentation-only range')
    tracked = set(git(root, 'ls-tree', '-r', '--name-only', '-z', head).decode('utf-8').split('\0'))
    directories = {posixpath.dirname(path) for path in tracked}
    for directory in tuple(directories):
        while '/' in directory:
            directory = posixpath.dirname(directory)
            directories.add(directory)
    targets = tracked | directories
    # Check incoming links too when a document was deleted or renamed.
    names = ({path for path in tracked if document(path)}
        if any(change.after_mode == '000000' for change in modified)
        else {change.path for change in modified if change.after_mode != '000000'})
    for name in sorted(names):
        text = git(root, 'show', f'{head}:{name}').decode('utf-8-sig')
        if '\0' in text:
            raise ValueError(f'{name}: NUL byte in Markdown')
        for match in LINK.finditer(markdown_prose(text, name)):
            link = urlsplit(match[1] or match[2])
            if link.scheme or link.netloc or not link.path or link.path.startswith('/'):
                continue
            target = posixpath.normpath(posixpath.join(posixpath.dirname(name), unquote(link.path)))
            if target not in targets:
                raise ValueError(f'{name}: missing local link target {target!r}')
    git(root, 'diff', '--check', base, head, '--')
    return len(names)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    choose = commands.add_parser('select')
    choose.add_argument('--event-name', default=os.environ.get('GITHUB_EVENT_NAME', ''))
    choose.add_argument('--event-path', default=os.environ.get('GITHUB_EVENT_PATH', ''))
    choose.add_argument('--force-full', action='store_true', default=os.environ.get('CI_FORCE_FULL', '').lower() == 'true')
    docs = commands.add_parser('check-docs')
    docs.add_argument('--base', required=True)
    for command in (choose, docs):
        command.add_argument('--head', default=os.environ.get('COMMIT', ''))
    args = parser.parse_args()
    root = Path.cwd()
    if args.command == 'check-docs':
        count = check_docs(root, args.base, args.head)
        print(f'Checked {count} Markdown files: UTF-8, code fences, local links and diff whitespace.')
        return
    try:
        event = json.loads(Path(args.event_path).read_text(encoding='utf-8'))
    except (ValueError, OSError):
        event = {}
    result = select(root, args.event_name, event, args.head, args.force_full)
    print(json.dumps({'docs_only': result.docs_only, 'base': result.base, 'reason': result.reason}))
    if output := os.environ.get('GITHUB_OUTPUT'):
        with Path(output).open('a', encoding='utf-8') as stream:
            stream.write(f'docs_only={str(result.docs_only).lower()}\nbase={result.base}\n')
    if summary := os.environ.get('GITHUB_STEP_SUMMARY'):
        with Path(summary).open('a', encoding='utf-8') as stream:
            stream.write(f'CI checks: **{"documentation" if result.docs_only else "full"}**. {result.reason}.\n')


if __name__ == '__main__':
    main()
