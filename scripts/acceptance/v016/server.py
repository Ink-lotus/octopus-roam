"""Run final-SHA MySQL 8.4/PostgreSQL 18 hourly tests in disposable containers.

Credentials exist only in child-process environments/stdin. Go output is
redacted before it is written. Missing, skipped and cached tests are rejected.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import time

MIGRATION = {
    "TestServerMigration18UpgradePreservesNativeEvidence": ("completed17-false", "completed17-true"),
    "TestServerMigration18FailureRollsBackAndRetries": ("archive-write", "archive-readback", "quarantine", "success-record"),
    "TestServerMigration18RejectsUnreviewedSchemas": ("column", "generated", "reference", "trigger", "engine-or-rls", "type"),
    "TestServerMigration18NormalDatabaseRemainsUnchanged": (),
}
RESTORE = {"TestServerHourlyArchivesEncryptedRestoreAndReplay": ()}
TASK_LABEL = "v016-server"


def require(value, reason):
    if not value:
        raise AssertionError(reason)


def sha(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_new(path, value):
    with Path(path).open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write("\n")


def audit_go(raw, expected, exit_code):
    events = []
    for line in raw.splitlines():
        try:
            value = json.loads(line)
        except ValueError:
            continue
        if isinstance(value, dict):
            events.append(value)
    required = {name for name in expected}
    required.update(name + "/" + child for name, children in expected.items() for child in children)
    passed = {event.get("Test") for event in events if event.get("Action") == "pass" and event.get("Test")}
    skipped = [event.get("Test", "package") for event in events if event.get("Action") == "skip"]
    nested_skips = any("--- SKIP:" in event.get("Output", "") for event in events)
    failures = [event.get("Test", "package") for event in events if event.get("Action") == "fail"]
    require(exit_code == 0 and not failures, "server Go test failed; see redacted log")
    require(not skipped and not nested_skips, "server test contained a skip")
    require(required <= passed, "server test inventory is missing explicit PASS results")
    require(not any("(cached)" in event.get("Output", "") for event in events), "cached server result rejected")
    return {"exit_code": exit_code, "passed_tests": sorted(passed), "required_tests": sorted(required), "skipped": [], "explicit_pass_count": len(required)}


def clean_environment():
    env = {k: v for k, v in os.environ.items() if not k.startswith(("ROAM_", "OCTOPUS_ROAM_")) and k != "OCTOPUS_W03_TEST_CASE" and k.upper() not in ("GOOS", "GOARCH", "CGO_ENABLED", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY")}
    env.update(GOTOOLCHAIN="local", GOPROXY="off", GOFLAGS="-mod=readonly")
    return env


class Runner:
    def __init__(self, args):
        self.args = args
        self.output = args.output.resolve()
        require(not self.output.exists(), "server evidence output must be new")
        require(args.source not in self.output.parents and self.output != args.source, "evidence must be outside the source checkout")
        self.output.mkdir(parents=True)
        self.run_id = hashlib.sha256(str(self.output).encode()).hexdigest()[:10]
        self.commands, self.results, self.cleanup = [], [], []
        self.owned = None
        self.redactions = []
        self.env = clean_environment()

    def command(self, args, *, input_text=None, env=None, timeout=90, expect_zero=True):
        start = time.monotonic()
        process = subprocess.run(args, input=input_text, encoding="utf-8", errors="replace", stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env or self.env, cwd=self.args.source, timeout=timeout)
        self.commands.append({"command": args, "exit_code": process.returncode, "elapsed_seconds": round(time.monotonic() - start, 3)})
        with (self.output / "commands.jsonl").open("a", encoding="utf-8") as stream:
            stream.write(json.dumps(self.commands[-1]) + "\n")
        require(not expect_zero or process.returncode == 0, "isolated command failed: " + Path(args[0]).name)
        return process

    def inspect_owned(self, name):
        info = self.command(["docker", "inspect", "--format", '{{json .Id}} {{json .Config.Labels}}', name], expect_zero=False)
        if info.returncode:
            return None
        identifier, labels_raw = info.stdout.strip().split(" ", 1)
        identifier, labels = json.loads(identifier), json.loads(labels_raw)
        require(labels.get("roam.task") == TASK_LABEL and labels.get("roam.run") == self.run_id, "container ownership does not match this run")
        return identifier

    def stop_owned(self):
        if self.owned is None:
            return
        name, identifier = self.owned
        require(self.inspect_owned(name) == identifier, "container identity changed before cleanup")
        stopped = self.command(["docker", "stop", "--time", "20", identifier], expect_zero=False)
        removed = self.command(["docker", "rm", identifier], expect_zero=False)
        exists = self.command(["docker", "inspect", "--format", "{{.Id}}", identifier], expect_zero=False)
        row = {"name": name, "container_id": identifier, "stop_exit_code": stopped.returncode, "remove_exit_code": removed.returncode, "absent_after": exists.returncode != 0, "tmpfs_only": True}
        self.cleanup.append(row)
        require(removed.returncode == 0 and row["absent_after"], "owned container cleanup failed")
        self.owned = None

    def one(self, dialect, tag, image):
        name = "roam-v016-s02-" + self.run_id + "-" + dialect + "-" + tag
        exists = self.command(["docker", "inspect", "--format", "{{.Id}}", name], expect_zero=False)
        require(exists.returncode != 0, "server runner refuses to adopt any existing container")
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        password = secrets.token_hex(24)
        self.redactions.append(password)
        if dialect == "mysql":
            container_env = "MYSQL_ROOT_PASSWORD=" + password + "\nMYSQL_ROOT_HOST=%\nMYSQL_DATABASE=roam_hourly18\n"
            data_path, container_port = "/var/lib/mysql", 3306
            query = 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --protocol=socket -uroot -Nse "SELECT VERSION()"'
            dsn = f"root:{password}@tcp(127.0.0.1:{port})/roam_hourly18?parseTime=true&charset=utf8mb4"
        else:
            container_env = "POSTGRES_PASSWORD=" + password + "\nPOSTGRES_DB=roam_hourly18\n"
            data_path, container_port = "/var/lib/postgresql", 5432
            query = 'PGPASSWORD="$POSTGRES_PASSWORD" exec psql -U postgres -d roam_hourly18 -Atc "SHOW server_version"'
            dsn = f"host=127.0.0.1 port={port} user=postgres password={password} dbname=roam_hourly18 sslmode=disable"
        self.redactions.append(dsn)
        args = ["docker", "run", "--detach", "--pull", "never", "--name", name, "--label", "roam.task=" + TASK_LABEL, "--label", "roam.run=" + self.run_id, "--publish", f"127.0.0.1:{port}:{container_port}", "--tmpfs", data_path + ":rw,size=384m", "--memory", "768m", "--env-file", "/dev/stdin", image]
        process = self.command(args, input_text=container_env, expect_zero=False)
        identifier = self.inspect_owned(name)
        if identifier:
            self.owned = name, identifier
        require(process.returncode == 0 and identifier is not None, "disposable database did not start")
        try:
            deadline = time.monotonic() + 90
            version = None
            while time.monotonic() < deadline:
                result = self.command(["docker", "exec", name, "sh", "-c", query], expect_zero=False, timeout=10)
                if result.returncode == 0:
                    version = result.stdout.strip()
                    break
                time.sleep(1)
            require(version is not None and version.startswith("8.4." if dialect == "mysql" else "18."), "actual server version does not satisfy MySQL 8.4/PostgreSQL 18")
            info = self.command(["docker", "image", "inspect", "--format", '{{json .Id}} {{json .Os}} {{json .Architecture}}', image]).stdout.strip().split(" ")
            require(json.loads(info[1]) == "linux" and json.loads(info[2]) == "amd64", "server image is not Linux amd64")
            row = {"dialect": dialect, "build": tag, "server_version": version, "image_reference": image, "image_id": json.loads(info[0]), "container_id": identifier, "loopback_publish": True, "credential_injection": "stdin and child environment; values not retained", "tests": []}
            for label, package, inventory in (("migration18", "./internal/db/migrate", MIGRATION), ("encrypted-restore", "./internal/op", RESTORE)):
                pattern = "^(" + "|".join(inventory) + ")$"
                # Preserve the existing isolated restore helper's two-minute
                # test deadline when executing its assertions directly.
                args = ["go", "test", "-json", "-count=1", "-timeout=2m" if inventory == RESTORE else "-timeout=6m"]
                if tag == "jsoniter":
                    args.append("-tags=jsoniter")
                args += ["-run", pattern, package]
                env = {**self.env, "ROAM_HOURLY18_DIALECT": dialect, "ROAM_HOURLY18_DSN": dsn, "ROAM_HOURLY18_ALLOW_ISOLATED_WRITE": "1"}
                if inventory == RESTORE:
                    # This go test invocation already owns an isolated process.
                    # Execute the actual assertions here, instead of accepting
                    # a parent's PASS while its child output is suppressed.
                    env["OCTOPUS_W03_TEST_CASE"] = "TestServerHourlyArchivesEncryptedRestoreAndReplay"
                result = self.command(args, env=env, timeout=400, expect_zero=False)
                raw = result.stdout + result.stderr
                for secret in sorted(self.redactions, key=len, reverse=True):
                    raw = raw.replace(secret, "[redacted]")
                log = self.output / (dialect + "-" + tag + "-" + label + ".jsonl")
                with log.open("x", encoding="utf-8", newline="\n") as stream:
                    stream.write(raw)
                check = audit_go(raw, inventory, result.returncode)
                check.update({"entry": label, "command": args, "log": log.name, "log_sha256": sha(log), "direct_isolated_assertions": True})
                row["tests"].append(check)
            self.results.append(row)
            write_new(self.output / (dialect + "-" + tag + ".json"), row)
            print(json.dumps({"dialect": dialect, "build": tag, "server_version": version, "status": "passed", "explicit_tests": 17, "skipped": 0}), flush=True)
        finally:
            self.stop_owned()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--mysql-image", required=True)
    parser.add_argument("--postgres-image", required=True)
    parser.add_argument("--allow-isolated-write", action="store_true")
    parser.add_argument("--cleanup-owned-containers", action="store_true")
    args = parser.parse_args()
    args.source = args.source.resolve()
    require(sys.platform.startswith("linux"), "Linux amd64 Docker Engine host required")
    require(re.fullmatch(r"[0-9a-f]{40}", args.commit), "full final source SHA required")
    require(args.allow_isolated_write and args.cleanup_owned_containers, "explicit database and owned-container cleanup permission required")
    for image in (args.mysql_image, args.postgres_image):
        require(re.fullmatch(r"(?:mysql|postgres)@sha256:[0-9a-f]{64}|sha256:[0-9a-f]{64}", image), "reviewed immutable server image required")
    require(subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=args.source, text=True).strip() == args.commit, "source checkout is not the final SHA")
    subprocess.run(["git", "diff", "--exit-code"], cwd=args.source, check=True)
    subprocess.run(["git", "diff", "--cached", "--exit-code"], cwd=args.source, check=True)
    require(subprocess.check_output(["go", "env", "GOVERSION"], env=clean_environment(), text=True).strip() == "go1.27.3", "Go 1.27.3 required")
    source_files = ["internal/db/migrate/018_server_test.go", "internal/op/hourly_server_test.go", "scripts/acceptance/v016/server.py", "go.mod", "go.sum"]
    before = {name: sha(args.source / name) for name in source_files}
    for name in source_files:
        raw = subprocess.check_output(["git", "cat-file", "blob", args.commit + ":" + name], cwd=args.source)
        require(hashlib.sha256(raw).hexdigest() == before[name], "server input differs from final Git blob")
    require(Path(__file__).read_bytes() == (args.source / "scripts/acceptance/v016/server.py").read_bytes(), "executed runner differs from frozen source")
    runner = Runner(args)
    passed = False
    try:
        for dialect, image in (("mysql", args.mysql_image), ("postgres", args.postgres_image)):
            for tag in ("default", "jsoniter"):
                runner.one(dialect, tag, image)
        require(before == {name: sha(args.source / name) for name in source_files}, "source/locks changed during server tests")
        require(len(runner.results) == 4 and len(runner.cleanup) == 4, "server matrix or cleanup is incomplete")
        passed = True
    finally:
        try:
            runner.stop_owned()
        except Exception:
            passed = False
            raise
        finally:
            write_new(runner.output / "RESULT.json", {"schema": 1, "status": "passed" if passed else "failed", "commit": args.commit, "completed_at_utc": datetime.now(timezone.utc).isoformat(), "source_hashes": before, "results": runner.results, "cleanup": runner.cleanup, "skips_accepted": False, "dsn_retained": False, "actual_candidate_product_acceptance": "not_run"})


if __name__ == "__main__":
    main()
