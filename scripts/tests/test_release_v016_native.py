"""Process exit races must not bypass live native runtime ownership checks."""
import ctypes
import importlib.util
from pathlib import Path
import types
import unittest
from unittest.mock import Mock, patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("v016_native_contract", ROOT / "scripts/acceptance/v016/native.py")
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)


class NativeProcessOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.executable = (ROOT / "synthetic-owned.exe").resolve()
        self.runtime = types.SimpleNamespace(executable=self.executable)
        self.state = {"pid": 123, "created": 123456}
        self.library = types.SimpleNamespace(
            OpenProcess=Mock(return_value=456), CloseHandle=Mock(return_value=True),
            WaitForSingleObject=Mock(return_value=258),
            QueryFullProcessImageNameW=Mock(side_effect=self.query_image),
            GetProcessTimes=Mock(side_effect=self.query_times),
        )
        self.windows_patch = patch.object(native, "windows", return_value=self.library)
        self.windows_patch.start()
        self.addCleanup(self.windows_patch.stop)

    def query_image(self, handle, flags, buffer, length):
        buffer.value = str(self.executable)
        return True

    def query_times(self, handle, created, *others):
        value = ctypes.cast(created, ctypes.POINTER(native.wintypes.FILETIME)).contents
        value.dwLowDateTime = self.state["created"]
        value.dwHighDateTime = 0
        return True

    def test_live_owned_process_keeps_handle_for_caller(self):
        self.assertEqual(native.owned_process(self.runtime, self.state), 456)
        self.library.GetProcessTimes.assert_called_once()
        self.library.CloseHandle.assert_not_called()

    def test_exited_process_needs_no_image_query_and_closes_handle(self):
        self.library.WaitForSingleObject.return_value = 0
        self.assertIsNone(native.owned_process(self.runtime, self.state))
        self.library.QueryFullProcessImageNameW.assert_not_called()
        self.library.CloseHandle.assert_called_once_with(456)

    def test_exit_during_image_query_is_confirmed_on_same_handle(self):
        self.library.WaitForSingleObject.side_effect = [258, 0]
        self.library.QueryFullProcessImageNameW.side_effect = None
        self.library.QueryFullProcessImageNameW.return_value = False
        self.assertIsNone(native.owned_process(self.runtime, self.state))
        self.assertEqual(self.library.WaitForSingleObject.call_args_list, [((456, 0),), ((456, 0),)])
        self.library.CloseHandle.assert_called_once_with(456)

    def test_live_image_query_failure_still_fails_closed(self):
        self.library.QueryFullProcessImageNameW.side_effect = None
        self.library.QueryFullProcessImageNameW.return_value = False
        with self.assertRaisesRegex(AssertionError, "process image"):
            native.owned_process(self.runtime, self.state)
        self.library.CloseHandle.assert_called_once_with(456)

    def test_unconfirmed_exit_cannot_suppress_image_query_failure(self):
        self.library.WaitForSingleObject.side_effect = [258, 0xFFFFFFFF]
        self.library.QueryFullProcessImageNameW.side_effect = None
        self.library.QueryFullProcessImageNameW.return_value = False
        with self.assertRaisesRegex(AssertionError, "process image"):
            native.owned_process(self.runtime, self.state)
        self.library.CloseHandle.assert_called_once_with(456)

    def test_live_wrong_image_is_rejected(self):
        self.runtime.executable = ROOT / "different-runtime.exe"
        with self.assertRaisesRegex(AssertionError, "does not belong"):
            native.owned_process(self.runtime, self.state)
        self.library.CloseHandle.assert_called_once_with(456)

    def test_live_reused_pid_is_rejected(self):
        recorded = {**self.state, "created": self.state["created"] + 1}
        with self.assertRaisesRegex(AssertionError, "creation identity"):
            native.owned_process(self.runtime, recorded)
        self.library.CloseHandle.assert_called_once_with(456)

    def test_live_creation_query_failure_is_rejected(self):
        self.library.GetProcessTimes.side_effect = None
        self.library.GetProcessTimes.return_value = False
        with self.assertRaisesRegex(AssertionError, "creation identity"):
            native.owned_process(self.runtime, self.state)
        self.library.CloseHandle.assert_called_once_with(456)


if __name__ == "__main__":
    unittest.main()
