"""Actual Windows candidate menu/helper/state checks in new isolated runtimes.

This does not sign out, register a login task, or report actual logon success.
The separate real-logon plan must be executed in the approved interactive session.
"""
import argparse
import ctypes
from ctypes import wintypes
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import threading
import time
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "backup"))
import product_v016 as product

require, sha, until = product.require, product.sha, product.until


def windows():
    library = ctypes.WinDLL("kernel32", use_last_error=True)
    library.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    library.OpenProcess.restype = wintypes.HANDLE
    library.CloseHandle.argtypes = [wintypes.HANDLE]
    library.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
    library.GetExitCodeProcess.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.DWORD)]
    library.QueryFullProcessImageNameW.argtypes = [wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)]
    library.GetProcessTimes.argtypes = [wintypes.HANDLE] + [ctypes.POINTER(wintypes.FILETIME)] * 4
    library.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, ctypes.c_void_p, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
    library.CreateFileW.restype = wintypes.HANDLE
    return library


def read_state(runtime):
    try:
        return json.loads((runtime.root / ".octopus-roam-process.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None


def owned_process(runtime, state):
    library = windows()
    handle = library.OpenProcess(0x00100000 | 0x1000, False, state["pid"])
    if not handle:
        return None
    path = ctypes.create_unicode_buffer(32768)
    length = wintypes.DWORD(len(path))
    created, exited, kernel, user = (wintypes.FILETIME() for _ in range(4))
    try:
        require(library.QueryFullProcessImageNameW(handle, 0, path, ctypes.byref(length)), "cannot verify the owned process image")
        require(Path(path.value).resolve() == runtime.executable.resolve(), "PID does not belong to the isolated runtime")
        require(library.GetProcessTimes(handle, ctypes.byref(created), ctypes.byref(exited), ctypes.byref(kernel), ctypes.byref(user)), "cannot verify process creation identity")
        require((created.dwHighDateTime << 32 | created.dwLowDateTime) == state["created"], "PID creation identity changed")
        return handle
    except Exception:
        library.CloseHandle(handle)
        raise


class Runtime(product.Runtime):
    def stop(self):
        state = read_state(self)
        if not state:
            require(self.process is None or self.process.poll() is not None, "live process has no verifiable state")
            return
        handle = owned_process(self, state)
        if handle is None:
            return
        try:
            if windows().WaitForSingleObject(handle, 0) == 0:
                return
            self.suite.command([str(self.executable), "desktop", "request-stop", "--runtime", str(self.root)])
            require(windows().WaitForSingleObject(handle, 25000) == 0, "owned native process did not stop normally")
            code = wintypes.DWORD()
            require(windows().GetExitCodeProcess(handle, ctypes.byref(code)) and code.value == 0, "owned process exit was not zero")
        finally:
            windows().CloseHandle(handle)


def configure(runtime):
    path = runtime.data / "config.json"
    value = json.loads(path.read_text(encoding="utf-8-sig")) if path.is_file() else {}
    value["server"] = {"host": "127.0.0.1", "port": runtime.port}
    value["database"] = {"type": "sqlite", "path": str(runtime.db)}
    value["codex_catalog"] = {"enabled": False, "use_proxy": False}
    value["log"] = {"level": "error"}
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    return path


def helper(suite, snapshot, name, current_helper, fail_start):
    runtime = Runtime(suite.previous_view, name, data_source=snapshot / "data")
    config = configure(runtime)
    runtime.start()
    before = product.base.database_state(runtime.db)
    stage = suite.output / (name + "-stage")
    stage.mkdir()
    with zipfile.ZipFile(suite.archive) as archive:
        for entry in archive.infolist():
            relative = Path(entry.filename)
            require(not relative.is_absolute() and ".." not in relative.parts and not entry.is_dir(), "unsafe candidate stage ZIP entry")
        archive.extractall(stage)
    require(sha(stage / "octopus-roam.exe") == suite.binary_sha, "helper replacement is not the actual candidate")
    helper_path = stage / ".update-helper.exe"
    shutil.copyfile(stage / "octopus-roam.exe" if current_helper else runtime.executable, helper_path)
    state = read_state(runtime)
    require(state and state["ready"], "authentic helper parent is not ready")
    files = ["octopus-roam.exe", "octopus-roam.cmd"]
    require(all((stage / name).is_file() for name in files), "reviewed native ZIP layout required")
    plan = {"schema": 1, "parent_pid": state["pid"], "parent_created": state["created"], "runtime": str(runtime.root), "stage": str(stage), "directory": str(runtime.root), "arguments": ["start", "--config", str(config)], "files": [{"path": name, "sha256": sha(stage / name)} for name in files]}
    product.base.write_json(stage / ".update-plan.json", plan)
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith("OCTOPUS_ROAM_")}
    env.update(OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE="1", OCTOPUS_ROAM_CODEX_CATALOG_ENABLED="false")
    port_blocked, blocker_done = threading.Event(), threading.Event()
    blocker_errors = []

    def block_then_release():
        listener = socket.socket()
        try:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
            deadline = time.monotonic() + 40
            while time.monotonic() < deadline:
                try:
                    listener.bind(("127.0.0.1", runtime.port))
                    listener.listen(1)
                    port_blocked.set()
                    break
                except OSError:
                    time.sleep(0.002)
            require(port_blocked.is_set(), "controlled candidate port conflict was not established")
            path = runtime.root / ".octopus-roam-update.json"
            until(lambda: path.is_file() and json.loads(path.read_text()).get("failed") is True, "actual helper rollback begins", 45)
        except Exception as error:
            blocker_errors.append(type(error).__name__)
        finally:
            listener.close()
            blocker_done.set()

    watcher = threading.Thread(target=block_then_release, daemon=True) if fail_start else None
    with (stage / ".update-helper.log").open("xb") as log:
        process = subprocess.Popen([str(helper_path), "__update-apply", str(stage / ".update-plan.json")], cwd=stage, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log, creationflags=subprocess.CREATE_NO_WINDOW)
    suite.helpers.append(process)
    until(lambda: (stage / ".update-ready.json").is_file(), "authentic helper readiness")
    require(json.loads((stage / ".update-ready.json").read_text()).get("ready") is True, "authentic helper rejected the parent/stage")
    if watcher:
        watcher.start()
    suite.command([str(runtime.executable), "desktop", "request-stop", "--runtime", str(runtime.root)])
    code = process.wait(timeout=100)
    if watcher:
        watcher.join(timeout=10)
        require(port_blocked.is_set() and blocker_done.is_set() and not blocker_errors, "controlled failure/recovery watcher did not complete")
    expected_version = "v0.1.5" if fail_start else "v0.1.6"
    expected_binary = suite.previous_view.binary_sha if fail_start else suite.binary_sha
    until(lambda: read_state(runtime) and read_state(runtime)["ready"], "helper restart state")
    runtime.api("/api/v1/user/login", "POST", {"username": "admin", "password": "admin", "expire": -1})
    require(runtime.api("/api/v1/update/now-version") == expected_version and sha(runtime.executable) == expected_binary, "helper restarted a different version/program")
    require((code != 0) == fail_start, "authentic helper exit did not match the controlled scenario")
    require(product.base.database_state(runtime.db)["business_sha256"] == before["business_sha256"], "helper changed prior business data")
    runtime.stop()
    return {"parent_version": "v0.1.5", "replacement_version": "v0.1.6", "helper_version": "v0.1.6" if current_helper else "v0.1.5", "helper_sha256": sha(helper_path), "replacement_sha256": sha(stage / "octopus-roam.exe"), "helper_exit_code": code, "restarted_version": expected_version, "actual_candidate_bytes": True, "failure_injection": "loopback port occupied after old shutdown; released before rollback restart" if fail_start else None, "data_unchanged": True}


def menus(suite, snapshot):
    runtime = Runtime(suite, "actual-menu", data_source=snapshot / "data")
    configure(runtime)
    launcher = runtime.root / "octopus-roam.cmd"
    require(launcher.is_file(), "actual candidate menu launcher missing")
    before = product.base.database_state(runtime.db)

    def menu(choice, ready):
        with (runtime.root / ("menu-" + choice + ".log")).open("xb") as log:
            process = subprocess.Popen([os.path.join(os.environ["SystemRoot"], "System32", "cmd.exe"), "/d", "/c", str(launcher)], cwd=runtime.root, stdin=subprocess.PIPE, stdout=log, stderr=log, creationflags=subprocess.CREATE_NO_WINDOW)
        process.stdin.write(choice.encode())
        process.stdin.flush()
        if ready:
            until(lambda: read_state(runtime) and read_state(runtime)["ready"], "actual menu startup")
        else:
            until(lambda: (not read_state(runtime)) or (read_state(runtime) and owned_process_exited(runtime)), "actual menu normal stop")
        process.stdin.write(b"0")
        process.stdin.flush()
        process.stdin.close()
        require(process.wait(timeout=25) == 0, "actual packaged menu failed")
    menu("1", True)
    runtime.api("/api/v1/user/login", "POST", {"username": "admin", "password": "admin", "expire": -1})
    require(runtime.api("/api/v1/update/now-version") == "v0.1.6", "menu did not start actual candidate")
    menu("3", False)
    require(product.base.database_state(runtime.db)["business_sha256"] == before["business_sha256"], "menu stop lost prior data")
    return {"actual_packaged_choices": [1, 3, 0], "version": "v0.1.6", "login_task_actions": "not_run; require separate actual-logon permission", "data_unchanged": True}


def owned_process_exited(runtime):
    state = read_state(runtime)
    if not state:
        return True
    handle = owned_process(runtime, state)
    if handle is None:
        return True
    try:
        return windows().WaitForSingleObject(handle, 0) == 0
    finally:
        windows().CloseHandle(handle)


def state_writes(suite, snapshot):
    runtime = Runtime(suite, "actual-state-writer", data_source=snapshot / "data")
    configure(runtime)
    runtime.start()
    runtime.stop()
    path = runtime.root / ".octopus-roam-process.json"

    def deny_delete():
        handle = windows().CreateFileW(str(path), 0x80000000, 3, None, 3, 0x80, None)
        require(handle not in (None, ctypes.c_void_p(-1).value), "isolated read handle was not opened")
        return handle

    handle = deny_delete()
    release = threading.Thread(target=lambda: (time.sleep(0.5), windows().CloseHandle(handle)), daemon=True)
    release.start()
    runtime.start()
    release.join()
    until(lambda: read_state(runtime)["ready"], "initial state transient-reader recovery")
    runtime.stop()
    before = sha(path)
    previous_created = read_state(runtime)["created"]
    ready_reader = {"observed": False, "errors": []}

    def hold_ready_record():
        handle = None
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                state = read_state(runtime)
                if state and state["created"] != previous_created and not state["ready"]:
                    handle = deny_delete()
                    protected = read_state(runtime)
                    if protected and protected["created"] == state["created"] and not protected["ready"]:
                        time.sleep(0.5)
                        require(not read_state(runtime)["ready"], "Ready publication was not held by the real reader")
                        ready_reader["observed"] = True
                        return
                    windows().CloseHandle(handle)
                    handle = None
                time.sleep(0.002)
            raise AssertionError("actual initial-to-Ready transition was not observed")
        except Exception as error:
            ready_reader["errors"].append(type(error).__name__)
        finally:
            if handle is not None:
                windows().CloseHandle(handle)

    reader = threading.Thread(target=hold_ready_record, daemon=True)
    reader.start()
    runtime.start()
    reader.join(timeout=12)
    require(ready_reader["observed"] and not ready_reader["errors"], "Ready-phase reader scenario did not execute")
    until(lambda: read_state(runtime)["ready"], "actual Ready publication after the reader released")
    runtime.stop()
    before = sha(path)
    handle = deny_delete()
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith("OCTOPUS_ROAM_")}
    env.update(OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE="1", OCTOPUS_ROAM_CODEX_CATALOG_ENABLED="false")
    with (runtime.root / "persistent-state-denial.log").open("xb") as log:
        process = subprocess.Popen([str(runtime.executable), "start"], cwd=runtime.root, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log, creationflags=subprocess.CREATE_NO_WINDOW)
    try:
        require(process.wait(timeout=10) != 0 and sha(path) == before, "persistent denial changed the prior state or was silently accepted")
    finally:
        windows().CloseHandle(handle)
    runtime.start()
    until(lambda: read_state(runtime)["ready"], "actual startup after persistent denial released")
    runtime.stop()
    return {"initial_transient_reader_recovered": True, "ready_phase_transient_reader_recovered": True, "persistent_denial_rejected": True, "prior_state_preserved": True, "normal_start_after_release": True}


def main():
    parser = product.parser()
    parser.description = __doc__
    parser.add_argument("--product-evidence", type=Path, required=True)
    args = parser.parse_args()
    require(sys.platform == "win32" and args.platform == "windows", "native Windows required")
    require(args.allow_isolated_write, "explicit isolated native update/file/config permission required")
    for name in ("repository", "candidate", "previous_artifact", "fixtures", "output", "product_evidence"):
        setattr(args, name, getattr(args, name).resolve())
    require(args.output != args.product_evidence and args.product_evidence not in args.output.parents and args.output not in args.product_evidence.parents, "native output overlaps protected product evidence")
    arguments = [sys.executable, "-B", str(args.repository / "scripts/acceptance/backup/product_v016.py"), "--repository", str(args.repository), "--candidate", str(args.candidate), "--commit", args.commit, "--artifact-sha", args.artifact_sha, "--materials-sha", args.materials_sha, "--previous-artifact", str(args.previous_artifact), "--previous-manifest-sha", args.previous_manifest_sha, "--platform", "windows", "--fixtures", str(args.fixtures), "--output", str(args.output), "--validate-only"]
    subprocess.run(arguments, cwd=args.repository, check=True)
    proof = json.loads((args.product_evidence / "summary.json").read_text(encoding="utf-8"))
    require(proof.get("product_scenarios_passed") is True and proof.get("commit") == args.commit and proof.get("artifact_sha256") == args.artifact_sha, "completed actual product evidence for this exact candidate required")
    snapshot = args.product_evidence / "complete-v015-preupgrade"
    require(product.base.data_manifest(snapshot) == json.loads((args.product_evidence / "preupgrade-manifest.json").read_text()), "complete real-v0.1.5 snapshot differs")
    manifest, fixtures = product.fixture_inputs(args.fixtures)
    metadata = json.loads((args.candidate / "build-info.json").read_text())
    previous = json.loads((args.previous_artifact / "artifact.json").read_text())
    args.hold_for_browser = False
    suite = product.Suite(args, metadata, manifest, fixtures, previous)
    suite.helpers = []
    passed = False
    try:
        suite.check("actual-candidate-menu-start-stop", lambda: menus(suite, snapshot))
        suite.check("real-v015-helper-upgrade-to-actual-candidate", lambda: helper(suite, snapshot, "old-helper-success", False, False))
        suite.check("actual-candidate-helper-success", lambda: helper(suite, snapshot, "current-helper-success", True, False))
        suite.check("actual-candidate-helper-start-failure-and-v015-recovery", lambda: helper(suite, snapshot, "current-helper-failure", True, True))
        suite.check("actual-candidate-state-reader-contention", lambda: state_writes(suite, snapshot))
        passed = True
    finally:
        try:
            for runtime in reversed(suite.runtimes):
                runtime.stop()
            for process in suite.helpers:
                process.wait(timeout=100)
            suite.cleanup()
        except Exception:
            passed = False
            raise
        finally:
            product.base.write_json(suite.output / "NATIVE-RESULT.json", {"schema": 1, "native_scenarios_passed": passed, "commit": args.commit, "artifact_sha256": args.artifact_sha, "materials_sha256": args.materials_sha, "candidate_binary_sha256": suite.binary_sha, "previous_commit": product.PREVIOUS_COMMIT, "checks": suite.checks, "actual_logon": "not_run; mandatory due to startup-state changes", "login_tasks_created": 0, "ready_phase_reader": "passed" if passed else "failed_or_not_completed", "live_helper_count": sum(process.poll() is None for process in suite.helpers), "acceptance_script_sha256": sha(__file__)})


if __name__ == "__main__":
    main()
