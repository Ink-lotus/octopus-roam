"""Exercise CI routing against real, isolated Git histories; no product dependencies."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import ci_changes as ci


class ChangesTests(unittest.TestCase):
    def setUp(self):
        # Retain isolated fixtures, consistent with the release contract tests.
        self.root = Path(tempfile.mkdtemp(prefix='ci-changes-', dir=os.environ.get('CI_TEST_ROOT')))
        self.env = {**os.environ, 'GIT_CONFIG_GLOBAL': os.devnull, 'GIT_CONFIG_NOSYSTEM': '1',
            'GIT_CONFIG_COUNT': '0', 'GIT_AUTHOR_NAME': 'CI fixture', 'GIT_COMMITTER_NAME': 'CI fixture',
            'GIT_AUTHOR_EMAIL': 'ci@example.invalid', 'GIT_COMMITTER_EMAIL': 'ci@example.invalid'}
        for name in ('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_OBJECT_DIRECTORY',
                     'GIT_ALTERNATE_OBJECT_DIRECTORIES'):
            self.env.pop(name, None)
        self.git('init', '--initial-branch=main')
        self.base = self.commit({'README.md': '# Project\n', 'docs/guide.md': '# Guide\n',
            'main.go': 'package main\n', 'scripts/example.py': 'print(1)\n'})
        self.git('update-ref', 'refs/remotes/origin/main', self.base)
        self.git('checkout', '-b', 'topic')

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root),
            '-c', 'core.autocrlf=false', '-c', 'core.excludesFile=',
            '-c', 'core.hooksPath=' + str(self.root / 'disabled-hooks'),
            '-c', 'commit.gpgsign=false', *args], env=self.env, stderr=subprocess.PIPE).decode().strip()

    def commit(self, files=None):
        for name, data in (files or {}).items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data.encode('utf-8') if isinstance(data, str) else data)
        self.git('add', '-A')
        self.git('commit', '--allow-empty', '-m', 'Synthetic CI change')
        return self.git('rev-parse', 'HEAD')

    def push(self, head, before=None, ref='refs/heads/topic'):
        return ci.select(self.root, 'push', {'ref': ref, 'before': before or self.base, 'after': head}, head)

    def test_document_branch_first_push_is_lightweight(self):
        head = self.commit({'README.md': '# 新的说明\n'})
        result = self.push(head, before='0' * 40)
        self.assertTrue(result.docs_only)
        self.assertEqual(result.base, self.base)
        self.assertEqual(ci.check_docs(self.root, result.base, head), 1)

    def test_main_document_push_is_lightweight(self):
        head = self.commit({'docs/guide.md': '# Updated guide\n'})
        self.assertTrue(self.push(head, ref='refs/heads/main').docs_only)

    def test_entire_branch_is_checked_when_last_push_only_changes_docs(self):
        code = self.commit({'main.go': 'package main\nfunc main() {}\n'})
        head = self.commit({'README.md': '# Documentation after untested code\n'})
        self.assertFalse(self.push(head, before=code).docs_only)

    def test_all_commits_in_main_push_are_checked(self):
        self.commit({'scripts/example.py': 'print(2)\n'})
        head = self.commit({'README.md': '# Documentation\n'})
        self.assertFalse(self.push(head, ref='refs/heads/main').docs_only)

    def test_new_branch_with_code_runs_full_suite(self):
        head = self.commit({'main.go': 'package changed\n'})
        self.assertFalse(self.push(head, before='0' * 40).docs_only)

    def test_unknown_or_runtime_paths_run_full_suite_even_if_markdown(self):
        for name in ('scripts/README.md', 'static/out/README.md', '.github/README.md',
                     '.github/workflows/build.yaml', 'web/public/example.md',
                     'docs/config.json', 'docs/archive/old.md', 'go.mod'):
            with self.subTest(path=name):
                base = self.git('rev-parse', 'HEAD')
                head = self.commit({name: 'Changed input\n'})
                self.assertFalse(self.push(head, before=base, ref='refs/heads/main').docs_only)

    def test_code_renamed_to_document_does_not_bypass_tests(self):
        self.git('mv', 'scripts/example.py', 'docs/example.md')
        head = self.commit()
        self.assertFalse(self.push(head).docs_only)

    def test_document_rename_is_lightweight(self):
        self.git('mv', 'docs/guide.md', 'docs/renamed.md')
        head = self.commit()
        self.assertTrue(self.push(head).docs_only)
        self.assertEqual(ci.check_docs(self.root, self.base, head), 2)

    def test_document_mode_change_runs_full_suite(self):
        self.git('update-index', '--chmod=+x', 'README.md')
        self.git('commit', '-m', 'Synthetic executable mode')
        head = self.git('rev-parse', 'HEAD')
        self.assertFalse(self.push(head).docs_only)

    def test_release_and_reusable_calls_force_full_suite(self):
        head = self.commit({'README.md': '# Documentation\n'})
        event = {'ref': 'refs/heads/topic', 'before': self.base, 'after': head}
        for event_name in ('workflow_dispatch', 'workflow_call'):
            with self.subTest(event=event_name):
                self.assertFalse(ci.select(self.root, event_name, event, head).docs_only)
        self.assertFalse(ci.select(self.root, 'push', event, head, force_full=True).docs_only)

    def test_incomplete_or_inconsistent_ranges_fall_back_to_full(self):
        head = self.commit({'README.md': '# Documentation\n'})
        for event in ({}, {'ref': 'refs/tags/v1', 'before': self.base, 'after': head},
                      {'ref': 'refs/heads/main', 'before': 'f' * 40, 'after': head},
                      {'ref': 'refs/heads/main', 'before': '0' * 40, 'after': head},
                      {'ref': 'refs/heads/main', 'before': self.base, 'after': self.base}):
            with self.subTest(event=event):
                self.assertFalse(ci.select(self.root, 'push', event, head).docs_only)
        self.git('update-ref', '-d', 'refs/remotes/origin/main')
        self.assertFalse(self.push(head).docs_only)

    def test_empty_change_range_runs_full_suite(self):
        head = self.commit()
        self.assertFalse(self.push(head).docs_only)

    def test_document_pr_ignores_changes_already_on_target_branch(self):
        self.commit({'README.md': '# Branch documentation\n'})
        self.git('checkout', 'main')
        target = self.commit({'main.go': 'package main\n// Target advanced\n'})
        self.git('checkout', 'topic')
        self.git('merge', '--no-ff', '--no-edit', 'main')
        head = self.git('rev-parse', 'HEAD')
        result = ci.select(self.root, 'pull_request', {'pull_request': {'base': {'sha': target}}}, head)
        self.assertTrue(result.docs_only)
        self.assertEqual(result.base, target)

    def test_code_pr_runs_full_suite(self):
        head = self.commit({'main.go': 'package updated\n', 'README.md': '# Updated\n'})
        result = ci.select(self.root, 'pull_request', {'pull_request': {'base': {'sha': self.base}}}, head)
        self.assertFalse(result.docs_only)

    def test_pr_without_actual_merge_base_runs_full_suite(self):
        head = self.commit({'README.md': '# Topic\n'})
        self.git('checkout', 'main')
        target = self.commit({'main.go': 'package advanced\n'})
        result = ci.select(self.root, 'pull_request', {'pull_request': {'base': {'sha': target}}}, head)
        self.assertFalse(result.docs_only)

    def test_document_check_rejects_code_ranges(self):
        head = self.commit({'main.go': 'package changed\n'})
        with self.assertRaisesRegex(ValueError, 'documentation-only'):
            ci.check_docs(self.root, self.base, head)

    def test_document_links_and_fenced_examples(self):
        head = self.commit({'docs/中文.md': '# Guide\n', 'README.md':
            '# Project\n[Guide](docs/%E4%B8%AD%E6%96%87.md#heading)\n'
            '[Directory](docs/)\n[External](https://example.invalid/)\n'
            '```markdown\n[Example](missing.md)\n```\n'})
        self.assertEqual(ci.check_docs(self.root, self.base, head), 2)

    def test_missing_local_link_fails(self):
        head = self.commit({'README.md': '# Project\n[Missing](docs/missing.md)\n'})
        with self.assertRaisesRegex(ValueError, 'missing local link'):
            ci.check_docs(self.root, self.base, head)

    def test_deleted_document_checks_unchanged_incoming_links(self):
        base = self.commit({'README.md': '# Project\n[Guide](docs/guide.md)\n'})
        self.git('rm', 'docs/guide.md')
        head = self.commit()
        with self.assertRaisesRegex(ValueError, 'missing local link'):
            ci.check_docs(self.root, base, head)

    def test_unclosed_fence_and_merge_conflicts_fail(self):
        for text, message in (('# Project\n```python\nprint(1)\n', 'unclosed'),
                              ('# Project\n<<<<<<< pending\n', 'unresolved')):
            with self.subTest(message=message):
                base = self.git('rev-parse', 'HEAD')
                head = self.commit({'README.md': text})
                with self.assertRaisesRegex(ValueError, message):
                    ci.check_docs(self.root, base, head)

    def test_invalid_utf8_or_nul_fails(self):
        for data in (b'# Invalid\n\xff\n', b'# Invalid\n\0\n'):
            with self.subTest(data=data):
                base = self.git('rev-parse', 'HEAD')
                head = self.commit({'README.md': data})
                with self.assertRaises(ValueError):
                    ci.check_docs(self.root, base, head)

    def test_new_trailing_whitespace_fails(self):
        head = self.commit({'README.md': '# Trailing whitespace  \n'})
        with self.assertRaises(subprocess.CalledProcessError):
            ci.check_docs(self.root, self.base, head)

    def test_cli_publishes_stable_check_selection(self):
        head = self.commit({'README.md': '# Documentation\n'})
        payload = self.root / 'event.json'
        payload.write_text(json.dumps({'ref': 'refs/heads/topic', 'before': '0' * 40, 'after': head}), encoding='utf-8')
        output = self.root / 'outputs.txt'
        summary = self.root / 'summary.md'
        process = subprocess.run([sys.executable, '-B', str(Path(ci.__file__)), 'select'],
            cwd=self.root, env={**self.env, 'GITHUB_EVENT_NAME': 'push', 'GITHUB_EVENT_PATH': str(payload),
                'COMMIT': head, 'GITHUB_OUTPUT': str(output), 'GITHUB_STEP_SUMMARY': str(summary),
                'CI_FORCE_FULL': 'false'}, capture_output=True, text=True, check=True)
        self.assertTrue(json.loads(process.stdout)['docs_only'])
        self.assertEqual(output.read_text(encoding='utf-8'), f'docs_only=true\nbase={self.base}\n')
        self.assertIn('documentation', summary.read_text(encoding='utf-8'))


if __name__ == '__main__':
    unittest.main()
