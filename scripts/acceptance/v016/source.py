"""Check one exact source SHA on Windows/Linux using local nonsecret settings.

The runner is tracked; cache/tool paths are supplied separately. It preserves
the existing complete CI and prescribed race checks, and starts no candidate.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import tarfile
import time

FIELDS = {"gomodcache", "gocache", "pnpm_store", "bash", "tool_path", "python3_adapter_dir", "python3_adapter_sha256", "actionlint"}


def require(value, reason):
    if not value:
        raise AssertionError(reason)


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def settings_contract(settings):
    require(isinstance(settings, dict) and set(settings) <= FIELDS, "unknown local source settings")
    require({"gomodcache", "gocache", "pnpm_store"} <= set(settings), "three explicit existing caches are required")
    require(all(isinstance(value, str) and value and "\n" not in value for value in settings.values()), "source settings must be nonempty strings")
    return settings


def write_new(path, value):
    with Path(path).open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write("\n")


def exact_inputs(source, commit):
    raw = subprocess.check_output(["git", "-c", "core.autocrlf=false", "archive", "--format=tar", commit], cwd=source)
    expected = {}
    with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
        for member in archive.getmembers():
            if member.isdir():
                continue
            require(member.isfile(), "unreviewed source file type")
            content = archive.extractfile(member).read()
            require((source / member.name).read_bytes() == content, "source differs from the exact commit blob: " + member.name)
            expected[member.name] = {"sha256": sha(content), "bytes": len(content), "mode": member.mode}
    return expected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--settings", type=Path, required=True)
    args = parser.parse_args()
    source, output = args.source.resolve(), args.output.resolve()
    require(platform.system() in ("Windows", "Linux") and platform.machine().lower() in ("amd64", "x86_64"), "native Windows/Linux amd64 required")
    require(len(args.commit) == 40 and all(c in "0123456789abcdef" for c in args.commit), "full exact source SHA required")
    require(subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip() == args.commit, "source HEAD differs")
    require(not output.exists() and output != source and source not in output.parents and output not in source.parents, "new evidence outside the source checkout required")
    settings = settings_contract(json.loads(args.settings.read_text(encoding="utf-8-sig")))
    for field in ("gomodcache", "gocache", "pnpm_store"):
        require(Path(settings[field]).is_dir(), "required existing cache is unavailable")
    if sys.platform != "win32":
        require("python3_adapter_dir" not in settings and "python3_adapter_sha256" not in settings, "Linux must use real Python and must not install aliases")
        require("actionlint" in settings and Path(settings["actionlint"]).is_file(), "existing actionlint v1.7.7 required")
    before = exact_inputs(source, args.commit)
    executed = source / "scripts/acceptance/v016/source.py"
    require(Path(__file__).read_bytes() == executed.read_bytes(), "executed source runner differs from frozen source")
    output.mkdir(parents=True)
    (output / "logs").mkdir()
    (output / "tmp").mkdir()
    write_new(output / "INPUTS-BEFORE.json", {"commit": args.commit, "files": before})
    env = {k: v for k, v in os.environ.items() if not k.startswith(("ROAM_", "OCTOPUS_ROAM_")) and k != "OCTOPUS_W03_TEST_CASE" and k.upper() not in ("GOOS", "GOARCH", "CGO_ENABLED")}
    env.update(GOTOOLCHAIN="local", GOPROXY="off", GOFLAGS="-mod=readonly", VERSION="v0.1.6", COMMIT=args.commit,
               GOMODCACHE=settings["gomodcache"], GOCACHE=settings["gocache"], ROAM_V016_PNPM_STORE=settings["pnpm_store"],
               TEMP=str(output / "tmp"), TMP=str(output / "tmp"), TMPDIR=str(output / "tmp"))
    paths = []
    if "tool_path" in settings:
        paths.extend(settings["tool_path"].split(os.pathsep))
    if "python3_adapter_dir" in settings:
        adapter = Path(settings["python3_adapter_dir"]) / "python3"
        require(adapter.is_file() and sha(adapter.read_bytes()) == settings.get("python3_adapter_sha256"), "existing Windows Python adapter differs")
        paths.append(str(adapter.parent))
    env["PATH"] = os.pathsep.join(paths + [env.get("PATH", "")])
    bash = settings.get("bash", "bash")
    rows = []

    def run(name, command):
        print(json.dumps({"step": name, "status": "running"}), flush=True)
        log = output / "logs" / (name + ".log")
        started = time.monotonic()
        with log.open("xb") as stream:
            process = subprocess.run(command, cwd=source, env=env, stdout=stream, stderr=subprocess.STDOUT)
        raw = log.read_bytes()
        row = {"step": name, "command": command, "exit_code": process.returncode, "elapsed_seconds": round(time.monotonic() - started, 3), "log": log.relative_to(output).as_posix(), "log_sha256": sha(raw), "log_bytes": len(raw)}
        rows.append(row)
        write_new(output / (name + ".json"), row)
        print(json.dumps(row), flush=True)
        require(process.returncode == 0, "source check failed: " + name + "; see isolated log")
        return raw.decode("utf-8", errors="replace")

    passed = False
    try:
        tools = run("01-tools", [bash, "-c", 'set -e; test "$(go env GOVERSION)" = go1.27.3; go version; test "$(node --version)" = v24.18.0; node --version; test "$(pnpm --version)" = 11.15.1; pnpm --version; test "$(python3 --version)" = "Python 3.14.6"; python3 --version; gcc --version'])
        run("02-go-cache", ["go", "mod", "download"])
        run("03-locked-node-cache", [bash, "-c", 'pnpm --dir web install --offline --frozen-lockfile --ignore-scripts --store-dir "$ROAM_V016_PNPM_STORE"'])
        if sys.platform != "win32":
            version = run("04-actionlint-version", [settings["actionlint"], "-version"])
            require(version.splitlines()[0] == "1.7.7", "actionlint version differs")
            run("05-actionlint", [settings["actionlint"], ".github/workflows/build.yaml", ".github/workflows/release.yaml", ".github/workflows/release-materials.yaml"])
        run("06-full-ci", [bash, "-x", "scripts/ci-test.sh"])
        run("07-model-filter", ["node", "--test", "scripts/tests/test_model_filter.cjs"])
        env["CGO_ENABLED"] = "1"
        race = ["go", "test", "-race", "./internal/webhook/..."]
        if sys.platform == "win32":
            race.append("./internal/desktop")
        run("08-additional-race", race)
        if sys.platform == "win32":
            run("09-windows-acceptance", [sys.executable, "-B", "scripts/ci_windows_acceptance.py"])
        run("10-generate-exact-fixtures", ["go", "run", "./scripts/acceptance/v016", "--output", str(output / "fixtures")])
        run("11-verify-exact-fixtures", ["go", "run", "./scripts/acceptance/v016", "--check", "--output", str(output / "fixtures")])
        run("12-source-diff", ["git", "diff", "--exit-code"])
        run("13-index-diff", ["git", "diff", "--cached", "--exit-code"])
        after = exact_inputs(source, args.commit)
        require(after == before, "source or lock files changed")
        write_new(output / "INPUTS-AFTER.json", {"commit": args.commit, "files": after})
        passed = True
    finally:
        write_new(output / "RESULT.json", {"schema": 1, "status": "passed" if passed else "failed", "commit": args.commit,
                  "platform": platform.platform(), "architecture": platform.machine(), "completed_at_utc": datetime.now(timezone.utc).isoformat(),
                  "checks": rows, "source_file_count": len(before), "source_blobs_preserved": passed,
                  "notes_sha256": before["docs/releases/v0.1.6.md"]["sha256"],
                  "fixture_manifest_sha256": sha((output / "fixtures/manifest.json").read_bytes()) if (output / "fixtures/manifest.json").is_file() else None,
                  "settings_file_sha256": sha(args.settings.read_bytes()), "runner_sha256": sha(executed.read_bytes()),
                  "actual_candidate_application": "not_run", "mysql_postgres_opt_in": "not_run; requires explicit server.py evidence"})


if __name__ == "__main__":
    main()
