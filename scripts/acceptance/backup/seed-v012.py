"""Create the legacy physical schema with the unmodified v0.1.2 executable.

The caller supplies only a disposable loopback database via environment. Do not
print configurations, command environments, login data, or raw application logs.
"""

import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time
import urllib.request


def main():
    executable = Path(sys.argv[1]).resolve(strict=True)
    target = Path(sys.argv[2]).resolve()
    if target.exists() or not target.name.startswith("v012-"):
        raise RuntimeError("new legacy fixture working directory required")
    dialect = os.environ.get("ROAM_LEGACY_DB_DIALECT")
    dsn = os.environ.get("ROAM_BACKUP_TEST_DSN", "")
    if dialect not in ("sqlite", "mysql", "postgres"):
        raise RuntimeError("unsupported fixture database")
    if dialect != "sqlite" and ("127.0.0.1" not in dsn or "roam_backup_b02_upgrade" not in dsn):
        raise RuntimeError("dedicated loopback legacy fixture required")
    if dialect == "sqlite":
        path = Path(dsn).resolve()
        if path.exists() or path.name != "legacy-statistics.sqlite" or path.parent != target.parent:
            raise RuntimeError("new dedicated legacy SQLite fixture required")
    target.mkdir(mode=0o700)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    config = target / "config.json"
    config.write_text(json.dumps({"server": {"host": "127.0.0.1", "port": port}, "database": {"type": dialect, "path": "environment-only"}, "log": {"level": "error"}}))
    env = os.environ.copy()
    env["OCTOPUS_ROAM_DATABASE_TYPE"] = dialect
    env["OCTOPUS_ROAM_DATABASE_PATH"] = dsn
    env["OCTOPUS_ROAM_SERVER_HOST"] = "127.0.0.1"
    env["OCTOPUS_ROAM_SERVER_PORT"] = str(port)
    env["OCTOPUS_ROAM_LOG_LEVEL"] = "error"
    process = subprocess.Popen([str(executable), "start", "--config", str(config)], cwd=target, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    ready = False
    try:
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline and process.poll() is None:
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=1) as response:
                    ready = response.status == 200
            except (OSError, TimeoutError):
                pass
            if ready:
                break
            time.sleep(0.2)
        if not ready:
            raise RuntimeError("legacy application did not become ready")
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
        try:
            result = process.wait(timeout=30)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise RuntimeError("legacy shutdown timed out") from None
    if result != 0:
        raise RuntimeError("legacy application exited unsuccessfully")
    print(json.dumps({"legacy_release": "v0.1.2", "dialect": dialect, "ready": ready, "normal_stop_exit": result}))


if __name__ == "__main__":
    main()
