"""Real v0.1.2 PostgreSQL HTTP import, sequence failure and artifact upgrade."""
import argparse
import copy
import hashlib
import http.cookiejar
import json
import os
import pathlib
import re
import secrets
import shutil
import signal
import subprocess
import sys
import time
import urllib.request

from product import Runtime, data_manifest, free_port, require, sha, until, write_json

TABLES = ("channels", "channel_keys", "channel_models", "channel_grants", "group_categories", "groups", "group_items", "api_keys", "webhook_endpoints")
PG_IMAGE = "postgres@sha256:e923a22ac074261dd73970c57edf659475bf0497cb9456f17d98ed7cdd3ca913"


class Process:
    api = Runtime.api
    import_backup = Runtime.import_backup

    def __init__(self, suite, name, old=False, database=None, source=None):
        self.suite, self.old = suite, old
        self.database = database or suite.database
        self.root = suite.output / name
        self.root.mkdir()
        if source:
            shutil.copytree(source, self.root / "data")
        self.port = free_port()
        self.url = f"http://127.0.0.1:{self.port}"
        self.client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.process, self.generation = None, 0
        suite.processes.append(self)

    def start(self):
        executable = self.suite.root / ("v012-linux" if self.old else "current-linux")
        env = {k: v for k, v in os.environ.items() if not k.startswith("OCTOPUS_ROAM_")}
        dsn = f"host=127.0.0.1 port={self.suite.port} user=roam_b04 dbname={self.database} password={self.suite.password} sslmode=disable connect_timeout=10"
        env.update(OCTOPUS_ROAM_DATABASE_TYPE="postgres", OCTOPUS_ROAM_DATABASE_PATH=dsn, OCTOPUS_ROAM_SERVER_HOST="127.0.0.1", OCTOPUS_ROAM_SERVER_PORT=str(self.port), OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE="1", OCTOPUS_ROAM_LOG_LEVEL="error")
        self.generation += 1
        with (self.root / f"application-{self.generation}.log").open("xb") as log:
            self.process = subprocess.Popen([str(executable), "start"], cwd=self.root, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        until(lambda: self.api("/", raw=True), "real PostgreSQL product readiness")
        self.api("/api/v1/user/login", "POST", {"username": "admin", "password": "admin", "expire": -1})
        require(self.api("/api/v1/update/now-version") == ("v0.1.2" if self.old else self.suite.metadata["version"]), "PostgreSQL product version differs")

    def stop(self):
        if self.process is not None:
            if self.process.poll() is None:
                self.process.send_signal(signal.SIGTERM)
            require(self.process.wait(timeout=30) == 0, "PostgreSQL product normal stop failed")
            self.process = None


class Suite:
    def __init__(self, root, attempt="01"):
        self.root = root.resolve()
        require(re.fullmatch(r"/data/home/roam-backup-b04-20\d{6}-\d{2}", str(self.root)) and self.root.is_dir(), "isolated B04 Linux root required")
        require(re.fullmatch(r"\d{2}", attempt), "two-digit attempt required")
        self.output = self.root / ("pg-product-" + attempt)
        self.output.mkdir()
        self.metadata = json.loads((root / "build-info.json").read_text(encoding="utf-8-sig"))
        require(sha(root / "v012-linux") == "706ddf9e6147174f1ffb2a7734f64aefe20ced3a828b035840fa541d5e21a7a5", "real old executable identity differs")
        require(sha(root / "current-linux") == next(row["sha256"] for row in self.metadata["binaries"] if row["target"] == "linux/amd64"), "current executable identity differs")
        self.seed = json.loads((root / "synthetic-fixture.json").read_text())
        require(self.seed["channels"][0]["name"] == "backup-sequence-repro-channel", "known synthetic fixture required")
        self.database = "roam_backup_b04_product_pg"
        self.container = "roam-backup-b04-product-pg-" + attempt
        self.port, self.password = free_port(), secrets.token_hex(32)
        self.created, self.processes, self.checks = False, [], []

    def command(self, arguments, input_data=None, env=None):
        result = subprocess.run(arguments, input=input_data, capture_output=True, env={**os.environ, **(env or {})}, timeout=90)
        with (self.output / "commands.jsonl").open("a") as log:
            log.write(json.dumps({"command": arguments, "exit_code": result.returncode}) + "\n")
        if result.returncode != 0:
            with (self.output / "command-failures.jsonl").open("a") as log:
                log.write(json.dumps({"command": arguments, "exit_code": result.returncode, "stderr": result.stderr.decode(errors="replace").replace(self.password, "[REDACTED]")}) + "\n")
        require(result.returncode == 0, "isolated PG command failed: " + pathlib.Path(arguments[0]).name)
        return result.stdout

    def sql(self, statement, database=None):
        return self.command(["docker", "exec", self.container, "psql", "-U", "roam_b04", "-d", database or self.database, "-At", "-v", "ON_ERROR_STOP=1", "-c", statement]).decode().strip()

    def snapshot(self, database=None):
        rows = {}
        for table in TABLES + ("settings", "stats_totals", "stats_dailies", "stats_hourlies", "stats_api_keys"):
            values = [json.loads(line) for line in self.sql(f"SELECT row_to_json(t) FROM {table} t", database).splitlines()]
            rows[table] = sorted(values, key=lambda row: json.dumps(row, sort_keys=True))
        return {"sha256": hashlib.sha256(json.dumps(rows, sort_keys=True).encode()).hexdigest(), "ids": {table: sorted(row["id"] for row in rows[table]) for table in TABLES}, "counts": {table: len(values) for table, values in rows.items()}}

    def check(self, name, action):
        started = time.monotonic()
        try:
            details = action()
            self.checks.append({"name": name, "passed": True, "details": details, "seconds": round(time.monotonic() - started, 3)})
            print("PASS " + name, flush=True)
        except Exception as error:
            self.checks.append({"name": name, "passed": False, "error_type": type(error).__name__, "reason": str(error) if isinstance(error, AssertionError) else "see isolated logs"})
            raise
        finally:
            write_json(self.output / "checks.json", self.checks)

    def run(self):
        probe = subprocess.run(["docker", "container", "inspect", self.container], capture_output=True)
        require(probe.returncode != 0, "refusing to adopt existing PG product container")
        self.command(["docker", "run", "--detach", "--name", self.container, "--label", "roam.task=backup-b04-pg-product", "--publish", f"127.0.0.1:{self.port}:5432", "--tmpfs", "/var/lib/postgresql:rw,nosuid,nodev,size=536870912", "--env", "POSTGRES_PASSWORD", "--env", "POSTGRES_USER=roam_b04", "--env", "POSTGRES_DB=" + self.database, PG_IMAGE], env={"POSTGRES_PASSWORD": self.password})
        self.created = True
        # The image starts a temporary Unix-socket server while initializing.
        # TCP becomes available only after that server stops and final startup completes.
        until(lambda: subprocess.run(["docker", "exec", self.container, "pg_isready", "-h", "127.0.0.1", "-U", "roam_b04", "-d", self.database], capture_output=True).returncode == 0, "PG 18.6 final TCP readiness")
        require(self.sql("SHOW server_version_num") == "180006", "PG version differs")
        write_json(self.output / "environment.json", {"server_version": self.sql("SHOW server_version"), "server_version_num": "180006", "image": PG_IMAGE})
        old = Process(self, "old", old=True)

        def legacy():
            old.start()
            old.import_backup(json.dumps(self.seed).encode())
            sequence = self.sql("SELECT last_value,is_called FROM group_items_id_seq")
            require(sequence == "1|f", "dense old import did not leave original sequence behind")
            require(self.sql("SELECT COUNT(*) FROM group_items") == "2", "old import lost original members")
            detail = old.api("/api/v1/channel/detail/1")
            detail["auto_add_groups"] = True
            old.api("/api/v1/channel/update", "POST", detail, expected=500, raw=True)
            require(self.sql("SELECT COUNT(*) FROM group_items") == "2", "old failed save changed members")
            old.stop()
            self.before = self.snapshot()
            self.complete = self.output / "preupgrade-complete"
            self.complete.mkdir()
            shutil.copytree(old.root / "data", self.complete / "data")
            dump = self.command(["docker", "exec", self.container, "pg_dump", "-U", "roam_b04", "-d", self.database, "--format=custom", "--no-owner"])
            (self.complete / "database.dump").write_bytes(dump)
            write_json(self.output / "preupgrade-manifest.json", data_manifest(self.complete))
            return {"old_import_sequence": sequence, "original_save_http_status": 500, "members_retained": 2, "snapshot": self.before, "complete_dump_sha256": sha(self.complete / "database.dump")}
        self.check("real-v012-http-sequence-bug-and-complete-copy", legacy)
        upgraded = Process(self, "upgraded", source=self.complete / "data")

        def upgrade():
            upgraded.start()
            require(self.snapshot()["sha256"] == self.before["sha256"], "upgrade changed existing business content")
            require(self.sql("SELECT MAX(version) FROM migration_records WHERE status=1") == "18", "upgrade migrations did not finish")
            detail = upgraded.api("/api/v1/channel/detail/1")
            detail["auto_add_groups"] = True
            upgraded.api("/api/v1/channel/update", "POST", detail)
            require(self.sql("SELECT COUNT(*) FROM group_items") == "3", "first upgraded save did not add exactly one member")
            upgraded.api("/api/v1/channel/update", "POST", detail)
            require(self.sql("SELECT COUNT(*) FROM group_items") == "3", "repeated save duplicated a member")
            return {"migrations": [16, 17, 18], "first_save_http_status": 200, "members_after_first_and_repeat": 3}
        self.check("current-artifact-upgrade-first-save-and-repeat", upgrade)

        def insert():
            before = self.snapshot()
            detail = upgraded.api("/api/v1/channel/detail/1")
            detail["id"], detail["name"] = 0, "b04-pg-real-new-channel"
            for key in detail["keys"]:
                key["id"] = 0
            upgraded.api("/api/v1/channel/create", "POST", detail)
            new_channel = int(self.sql("SELECT MAX(id) FROM channels"))
            grant = int(self.sql(f"SELECT g.id FROM channel_grants g JOIN channel_models m ON m.id=g.channel_model_id WHERE m.channel_id={new_channel} ORDER BY g.id LIMIT 1"))
            category = upgraded.api("/api/v1/group-category/create", "POST", {"name": "b04 pg real new category"})
            upgraded.api("/api/v1/group/create", "POST", {"name": "b04-pg-real-new-group", "mode": "manual", "category_id": category["id"], "items": [{"channel_grant_id": grant}]})
            upgraded.api("/api/v1/apikey/create", "POST", {"name": "b04-pg-real-new-apikey", "api_key": "synthetic-b04-pg-new-client", "enabled": True})
            hook = copy.deepcopy(self.seed["webhook_endpoints"][0])
            hook["id"], hook["name"] = 0, "b04-pg-real-new-hook"
            upgraded.api("/api/v1/webhook/create", "POST", hook)
            after = self.snapshot()
            new_ids = {}
            for table in TABLES:
                added = sorted(set(after["ids"][table]) - set(before["ids"][table]))
                require(added and min(added) > max(before["ids"][table], default=0), "real insert did not allocate a fresh ID: " + table)
                require(set(before["ids"][table]) <= set(after["ids"][table]), "real insert removed an existing row: " + table)
                new_ids[table] = added
            upgraded.stop()
            stable = self.snapshot()
            upgraded.start()
            require(self.snapshot()["sha256"] == stable["sha256"], "PG artifact restart lost business content")
            upgraded.stop()
            return {"new_ids": new_ids, "restart_snapshot": stable}
        self.check("nine-real-http-entity-inserts-and-restart", insert)

        def recovery():
            database = "roam_backup_b04_product_pg_recovery"
            self.sql("CREATE DATABASE " + database)
            self.command(["docker", "exec", "-i", self.container, "pg_restore", "-U", "roam_b04", "-d", database, "--no-owner", "--exit-on-error"], input_data=(self.complete / "database.dump").read_bytes())
            recovered = Process(self, "old-copy-recovery", old=True, database=database, source=self.complete / "data")
            recovered.start()
            require(self.snapshot(database)["sha256"] == self.before["sha256"], "old product with complete prior dump differs")
            require(self.sql("SELECT MAX(version) FROM migration_records", database) == "15", "old recovery used upgraded schema")
            recovered.stop()
            return {"real_v012_restarted": True, "complete_dump_restored": True, "snapshot_sha256": self.before["sha256"]}
        self.check("old-artifact-with-complete-preupgrade-pg-copy", recovery)

    def cleanup(self):
        for process in reversed(self.processes):
            process.stop()
        if self.created:
            label = self.command(["docker", "inspect", "--format", '{{index .Config.Labels "roam.task"}}', self.container]).decode().strip()
            require(label == "backup-b04-pg-product", "unowned PG product container")
            logs = self.command(["docker", "logs", self.container]).decode(errors="replace").replace(self.password, "[REDACTED]")
            (self.output / "postgres-startup.log").write_text(logs)
            self.command(["docker", "stop", "--time", "25", self.container])
            self.command(["docker", "rm", "--volumes", self.container])
        write_json(self.output / "cleanup.json", {"normal_process_stops": True, "owned_pg_container_removed": self.created, "complete_copy_retained": True})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=pathlib.Path)
    parser.add_argument("--attempt", default="01")
    args = parser.parse_args()
    suite = Suite(args.root, args.attempt)
    passed = False
    try:
        suite.run()
        passed = True
    finally:
        try:
            suite.cleanup()
        except Exception:
            passed = False
            raise
        finally:
            write_json(suite.output / "summary.json", {"passed": passed, "checks": len(suite.checks), "engine": "PostgreSQL 18.6", "source_tree_sha256": suite.metadata["source_tree_sha256"], "source_commit_exact": suite.metadata["source_commit_exact"], "current_binary_sha256": sha(suite.root / "current-linux"), "old_binary_sha256": sha(suite.root / "v012-linux"), "acceptance_script_sha256": sha(__file__)})


if __name__ == "__main__":
    main()
