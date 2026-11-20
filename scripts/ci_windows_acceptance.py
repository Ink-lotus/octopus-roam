"""Run every Windows-only release entry check, rejecting skipped or missing tests."""
from pathlib import Path
import sys
import unittest

TESTS = (
    'test_release_acceptance.EntryPointTests',
    'test_release_inputs.EntryVersionTests.test_powershell_entry_passes_valid_versions_to_artifact_verification_only',
)
EXPECTED_TESTS = 5


def main():
    if sys.platform != 'win32':
        raise SystemExit('Windows acceptance checks require native Windows.')
    sys.path.insert(0, str(Path(__file__).resolve().parent / 'tests'))
    suite = unittest.defaultTestLoader.loadTestsFromNames(TESTS)
    if suite.countTestCases() != EXPECTED_TESTS:
        raise SystemExit('The Windows acceptance test inventory changed; review the CI coverage.')
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    if not result.wasSuccessful() or result.skipped or result.testsRun != EXPECTED_TESTS:
        raise SystemExit('All five Windows acceptance checks must pass without skips.')


if __name__ == '__main__':
    main()
