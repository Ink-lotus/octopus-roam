"""B04 real ZIP/image SQLite upgrade, restore, routing and browser fixtures.

Only fresh task directories and containers bearing this run's ownership labels
are changed. Application logs, databases and complete pre-upgrade copies remain.
No production connection, release transport or application source is patched.
"""
import argparse
import copy
import hashlib
from hourly_archive import seed_legacy_hours, expected_archives, verify_archives
import http.client
import http.cookiejar
import http.server
import json
import os
import pathlib
import shutil
import socket
import sqlite3
import subprocess
import threading
import time
import urllib.error
import urllib.request
import zipfile


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def sha(path):
    with pathlib.Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path, value):
    pathlib.Path(path).write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def free_port():
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


def until(predicate, label, seconds=45):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            value = predicate()
            if value:
                return value
        except (OSError, urllib.error.URLError, ValueError):
            pass
        time.sleep(0.15)
    raise AssertionError(label + " timed out")


def data_manifest(directory):
    return {p.relative_to(directory).as_posix(): sha(p) for p in sorted(directory.rglob("*")) if p.is_file()}


ENTITY_TABLES = {"channels", "channel_keys", "channel_models", "channel_grants", "groups", "group_items", "group_categories", "api_keys", "webhook_endpoints"}


def database_state(path, entities_only=False):
    with sqlite3.connect(path.resolve().as_uri() + "?mode=ro", uri=True) as conn:
        conn.execute("PRAGMA query_only=ON")
        names = sorted(row[0] for row in conn.execute("SELECT name FROM sqlite_schema WHERE type='table'"))
        result = {}
        for name in names:
            if name.startswith("sqlite_") or name in ("migration_records", "users"):
                continue
            if entities_only and name not in ENTITY_TABLES:
                continue
            require(name.replace("_", "").isalnum(), "unexpected table name")
            rows = [list(row) for row in conn.execute('SELECT * FROM "' + name + '"')]
            rows.sort(key=lambda row: json.dumps(row, sort_keys=True, default=str))
            result[name] = rows
        live = copy.deepcopy(result)
        live.pop("stats_hourly_archives", None)
        if "stats_hourlies" in live:
            hour_index = [row[1] for row in conn.execute("PRAGMA table_info(stats_hourlies)")].index("hour")
            live["stats_hourlies"] = [row for row in live["stats_hourlies"] if row[hour_index] < 24]
        live_encoded = json.dumps(live, sort_keys=True, separators=(",", ":"), default=str).encode()
        if not entities_only:
            result.setdefault("stats_hourly_archives", [])
        encoded = json.dumps(result, sort_keys=True, separators=(",", ":"), default=str).encode()
        migrations = list(conn.execute("SELECT version,status FROM migration_records ORDER BY version"))
        members = list(conn.execute("SELECT id,group_id,channel_grant_id FROM group_items ORDER BY id"))
        return {"live_business_sha256": hashlib.sha256(live_encoded).hexdigest(), "business_sha256": hashlib.sha256(encoded).hexdigest(), "counts": {k: len(v) for k, v in result.items()}, "migrations": migrations, "members": members}


class Fixture(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    calls = []

    def log_message(self, *_args):
        pass

    def do_GET(self):
        body = json.dumps({"object": "list", "data": [{"id": "backup-sequence-repro-model", "object": "model"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.close_connection = True

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        authorization = self.headers.get("Authorization", "")
        key_index = next((i for i in range(1, 4) if authorization == f"Bearer synthetic-b04-upstream-{i}"), 0)
        self.calls.append({"stream": bool(body.get("stream")), "key_index": key_index, "path": self.path})
        require(key_index != 0, "fixture received an unexpected synthetic key")
        usage = {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}
        if body.get("stream"):
            events = []
            for delta in ({"role": "assistant"}, {"content": "b04 "}, {"content": "pong"}):
                events.append("data: " + json.dumps({"id": "b04-local", "object": "chat.completion.chunk", "created": 1, "model": body["model"], "choices": [{"index": 0, "delta": delta, "finish_reason": None}]}) + "\n\n")
            events.append("data: " + json.dumps({"id": "b04-local", "object": "chat.completion.chunk", "created": 1, "model": body["model"], "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": usage}) + "\n\ndata: [DONE]\n\n")
            raw = "".join(events).encode()
            content_type = "text/event-stream"
        else:
            raw = json.dumps({"id": "b04-local", "object": "chat.completion", "created": 1, "model": body["model"], "choices": [{"index": 0, "message": {"role": "assistant", "content": "b04 pong"}, "finish_reason": "stop"}], "usage": usage}).encode()
            content_type = "application/json"
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(raw)
        self.close_connection = True


class Runtime:
    def __init__(self, suite, name, legacy=False, data_source=None, compose=False):
        self.suite, self.name, self.legacy, self.compose = suite, name, legacy, compose
        self.root = suite.output / name
        self.root.mkdir()
        self.data = self.root / "data"
        if data_source:
            shutil.copytree(data_source, self.data)
        else:
            self.data.mkdir()
        if suite.platform == "docker":
            # Keep the application at a distinct non-root UID. The host verifier
            # reads the volume through its own group after entrypoint chown.
            self.data.chmod(0o770)
        self.db = self.data / "data.db"
        self.port = free_port()
        self.url = f"http://127.0.0.1:{self.port}"
        self.client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.process = None
        self.container_created = False
        self.sequence = 0
        self.container = "roam-b04-" + suite.run_id + "-" + name
        if suite.platform == "windows":
            archive = suite.legacy_zip if legacy else suite.archive
            with zipfile.ZipFile(archive) as bundle:
                for entry in bundle.infolist():
                    relative = pathlib.PurePosixPath(entry.filename)
                    require(not relative.is_absolute() and ".." not in relative.parts and not entry.is_dir(), "unsafe product ZIP member")
                    target = self.root / relative
                    require(not target.exists(), "product ZIP would overwrite existing test data")
                bundle.extractall(self.root)
            self.executable = self.root / "octopus-roam.exe"
            if not legacy:
                require(sha(self.executable) == suite.binary_sha, "extracted product differs from built ZIP")
        suite.runtimes.append(self)

    def api(self, endpoint, method="GET", body=None, expected=200, raw=False, headers=None):
        data = None if body is None else json.dumps(body).encode()
        request_headers = {} if headers is None else dict(headers)
        if data is not None:
            request_headers["Content-Type"] = "application/json"
        request = urllib.request.Request(self.url + endpoint, data=data, method=method, headers=request_headers)
        try:
            response = self.client.open(request, timeout=25)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            content = response.read()
            require(response.status == expected, f"{endpoint} status {response.status}, expected {expected}")
        if raw:
            return content
        return json.loads(content).get("data") if content else None

    def import_backup(self, raw, password="", expected=200):
        boundary = "b04-synthetic-backup-boundary"
        body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"synthetic.json\"\r\nContent-Type: application/json\r\n\r\n".encode() + raw + f"\r\n--{boundary}\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\n{password}\r\n--{boundary}--\r\n".encode())
        request = urllib.request.Request(self.url + "/api/v1/setting/import", data=body, headers={"Content-Type": "multipart/form-data; boundary=" + boundary})
        try:
            response = self.client.open(request, timeout=45)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            content = response.read()
            require(response.status == expected, f"import status {response.status}, expected {expected}")
        if expected >= 400:
            require(b"synthetic-b04-upstream" not in content, "import error exposed a credential")
        return content

    def start(self):
        self.sequence += 1
        if self.suite.platform == "windows":
            env = {k: v for k, v in os.environ.items() if not k.startswith("OCTOPUS_ROAM_")}
            env.update(OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE="1", OCTOPUS_ROAM_DATABASE_TYPE="sqlite", OCTOPUS_ROAM_DATABASE_PATH=str(self.db), OCTOPUS_ROAM_SERVER_HOST="127.0.0.1", OCTOPUS_ROAM_SERVER_PORT=str(self.port), OCTOPUS_ROAM_LOG_LEVEL="error")
            with (self.root / f"application-{self.sequence}.log").open("xb") as output:
                self.process = subprocess.Popen([str(self.executable), "start"], cwd=self.root, env=env, stdin=subprocess.DEVNULL, stdout=output, stderr=output, creationflags=subprocess.CREATE_NO_WINDOW)
        elif self.compose:
            self._compose_start()
        elif self.container_created:
            self.suite.command(["docker", "start", self.container])
        else:
            image = self.suite.legacy_image if self.legacy else self.suite.image
            self.suite.command(["docker", "run", "--detach", "--name", self.container, "--label", "roam.task=backup-b04", "--label", "roam.run=" + self.suite.run_id, "--publish", f"127.0.0.1:{self.port}:8080", "--mount", f"type=bind,source={self.data},target=/app/data", "--env", "PUID=10001", "--env", "PGID=" + str(self.suite.data_gid), "--env", "OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1", "--env", "OCTOPUS_ROAM_LOG_LEVEL=error", image])
            self.container_created = True
        until(lambda: self.api("/", raw=True), self.name + " readiness")
        self.api("/api/v1/user/login", "POST", {"username": "admin", "password": "admin", "expire": -1})
        version = self.api("/api/v1/update/now-version")
        require(version == ("v0.1.2" if self.legacy else self.suite.version), "runtime version does not match selected artifact")

    def _compose_start(self):
        if not self.container_created:
            override = self.root / "compose-override.yaml"
            override.write_text("services:\n  octopus-roam:\n    image: " + json.dumps(self.suite.image) + "\n    container_name: " + self.container + "\n    ports: !override\n      - " + json.dumps(f"127.0.0.1:{self.port}:8080") + "\n    volumes: !override\n      - " + json.dumps(str(self.data) + ":/app/data") + "\n    labels:\n      roam.task: backup-b04\n      roam.run: " + self.suite.run_id + "\n    environment:\n      PUID: '10001'\n      PGID: '" + str(self.suite.data_gid) + "'\n      OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE: '1'\n      OCTOPUS_ROAM_LOG_LEVEL: error\n", encoding="utf-8")
            self.compose_args = ["docker", "compose", "--project-name", self.container, "-f", str(self.suite.compose_file), "-f", str(override)]
        self.suite.command(self.compose_args + ["up", "--detach", "--no-build", "--pull", "never"], {"OCTOPUS_ROAM_VERSION": "v0.1.2"})
        self.container_created = True

    def stop(self):
        if self.suite.platform == "windows":
            if self.process is None or self.process.poll() is not None:
                return
            self.suite.command([str(self.executable), "desktop", "request-stop", "--runtime", str(self.root)])
            require(self.process.wait(timeout=25) == 0, "native normal stop failed")
            self.process = None
        elif self.container_created:
            state = json.loads(self.suite.command(["docker", "inspect", "--format", "{{json .State}}", self.container]))
            if state["Running"]:
                self.suite.command(["docker", "stop", "--time", "25", self.container])
                state = json.loads(self.suite.command(["docker", "inspect", "--format", "{{json .State}}", self.container]))
            require(state["ExitCode"] == 0 and not state["OOMKilled"], "container normal stop failed")

    def remove_container(self):
        if not self.container_created:
            return
        self.stop()
        labels = json.loads(self.suite.command(["docker", "inspect", "--format", "{{json .Config.Labels}}", self.container]))
        require(labels.get("roam.task") == "backup-b04" and labels.get("roam.run") == self.suite.run_id, "refusing to remove an unowned container")
        self.suite.command(["docker", "rm", self.container])
        self.container_created = False


class Suite:
    def __init__(self, args):
        self.platform = args.platform
        self.output = args.output.resolve()
        require(not self.output.exists(), "product evidence directory must be new")
        self.output.mkdir(parents=True)
        self.run_id = hashlib.sha256(str(self.output).encode()).hexdigest()[:10]
        self.metadata = json.loads((args.candidate / "build-info.json").read_text(encoding="utf-8-sig"))
        self.version = self.metadata["version"]
        self.archive = args.candidate / "archives/octopus-roam-windows-amd64.zip"
        self.legacy_zip = args.legacy_zip
        self.image, self.legacy_image = args.image, args.legacy_image
        self.compose_file = args.compose_file
        self.data_gid = os.getgid() if self.platform == "docker" else None
        self.binary_sha = next(row["sha256"] for row in self.metadata["binaries"] if row["target"] == "windows/amd64")
        if self.platform == "windows":
            require(sha(self.archive) == next(row["sha256"] for row in self.metadata["archives"] if row["name"] == self.archive.name), "current ZIP hash differs")
            require(sha(self.legacy_zip) == "ec1d9bd3cd999488d55f64e504e2960cf2fca23d735825b2d6217d2febfc0af9", "real v0.1.2 ZIP hash differs")
            bind = "127.0.0.1"
        else:
            # Docker's containerd store identifies this exact saved image by its
            # OCI manifest; the classic store uses the config digest. Both map
            # to the independently verified v0.1.2 config and layer contents.
            legacy_ids = ("sha256:907f1080901cea2eb105a277ff3ea7ee5c52c7dc6334e15a418fd8d26ad446fd", "sha256:9525262a0c093e8fc40ea19ce8607a31659a3bf3f9cbac45e1ade40517086963")
            require(self.image.startswith("sha256:") and self.legacy_image in legacy_ids, "immutable Docker image IDs are required")
            bind = self.command(["docker", "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}"]).strip()
        self.fixture_server = http.server.ThreadingHTTPServer((bind, 0), Fixture)
        self.fixture_server.daemon_threads = True
        self.fixture_thread = threading.Thread(target=self.fixture_server.serve_forever, daemon=True)
        self.fixture_thread.start()
        self.fixture_url = f"http://{bind}:{self.fixture_server.server_port}"
        self.seed = json.loads(args.fixture.read_text(encoding="utf-8-sig"))
        require(self.seed["channels"][0]["name"] == "backup-sequence-repro-channel", "known synthetic fixture required")
        self.seed["channels"][0]["base_url"] = self.fixture_url
        for i, key in enumerate(self.seed["channel_keys"], 1):
            key["key"] = f"synthetic-b04-upstream-{i}"
        self.seed["groups"][0]["active_item_id"] = self.seed["group_items"][0]["id"]
        self.runtimes, self.checks = [], []
        self.hold = args.hold_for_browser

    def command(self, arguments, additions=None):
        result = subprocess.run(arguments, capture_output=True, env={**os.environ, **(additions or {})}, timeout=90, creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        with (self.output / "commands.jsonl").open("a", encoding="utf-8") as output:
            output.write(json.dumps({"command": arguments, "exit_code": result.returncode}) + "\n")
        require(result.returncode == 0, pathlib.Path(arguments[0]).name + " command failed")
        return result.stdout.decode("utf-8", errors="replace")

    def check(self, name, function):
        started = time.monotonic()
        try:
            details = function()
            self.checks.append({"name": name, "passed": True, "details": details, "seconds": round(time.monotonic() - started, 3)})
            print("PASS " + name, flush=True)
            return details
        except Exception as error:
            self.checks.append({"name": name, "passed": False, "error_type": type(error).__name__, "reason": str(error) if isinstance(error, AssertionError) else "see isolated command and application logs"})
            raise
        finally:
            write_json(self.output / "checks.json", self.checks)

    def chat(self, runtime, stream=False, success=True):
        if not success:
            return self.blocked_chat(runtime, stream)
        request = urllib.request.Request(runtime.url + "/v1/chat/completions", data=json.dumps({"model": self.seed["groups"][0]["name"], "stream": stream, "messages": [{"role": "user", "content": "isolated b04 acceptance"}]}).encode(), headers={"Content-Type": "application/json", "Authorization": "Bearer " + self.seed["api_keys"][0]["api_key"]})
        before = len(Fixture.calls)
        try:
            response = runtime.client.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            if success:
                require(response.status == 200, "synthetic relay request failed")
                require((b"[DONE]" in raw and b"pong" in raw) if stream else json.loads(raw)["choices"][0]["message"]["content"] == "b04 pong", "relay response differs")
                require(len(Fixture.calls) == before + 1, "relay emitted unexpected upstream requests")
            else:
                require(response.status >= 400 and len(Fixture.calls) == before, "unavailable route contacted an upstream")

    def blocked_chat(self, runtime, stream):
        # An unavailable route waits for configuration changes by contract.
        # Observe the server's SSE state, then cancel the actual client socket.
        before = len(Fixture.calls)
        connection = http.client.HTTPConnection("127.0.0.1", runtime.port, timeout=10)
        body = json.dumps({"model": self.seed["groups"][0]["name"], "stream": stream, "messages": [{"role": "user", "content": "b04 unavailable route cancellation"}]})
        connection.request("POST", "/v1/chat/completions", body=body, headers={"Content-Type": "application/json", "Authorization": "Bearer " + self.seed["api_keys"][0]["api_key"]})
        try:
            with runtime.client.open(runtime.url + "/api/v1/log/overview/stream", timeout=10) as events:
                def observe(predicate):
                    deadline = time.monotonic() + 15
                    while time.monotonic() < deadline:
                        line = events.readline()
                        require(line, "request state stream ended unexpectedly")
                        if line.startswith(b"data:"):
                            state = json.loads(line[5:])
                            if predicate(state):
                                return state
                    raise AssertionError("expected request state was not published")
                waiting = observe(lambda row: row["status"] == "running" and row.get("retrying") and not row.get("sending"))
                time.sleep(0.5)
                require(len(Fixture.calls) == before, "unavailable route contacted an upstream")
                connection.sock.shutdown(socket.SHUT_RDWR)
                connection.close()
                canceled = observe(lambda row: row["id"] == waiting["id"] and row["status"] == "canceled")
                require(len(Fixture.calls) == before and not canceled.get("sending"), "canceled unavailable request sent upstream data")
                return {"waiting_observed": True, "client_cancel_observed": True, "upstream_calls": 0}
        finally:
            connection.close()

    def run(self):
        old = Runtime(self, "healthy-old", legacy=True)

        def seed_old():
            old.start()
            old.import_backup(json.dumps(self.seed).encode())
            self.chat(old)
            old.stop()
            seed_legacy_hours(old.db)
            self.hourly_expected = expected_archives(old.db)
            require(len(self.hourly_expected) >= 32, "historical midnight fixture was not created")
            self.before = database_state(old.db)
            require(max(v for v, _ in self.before["migrations"]) == 15, "old schema was not produced by v0.1.2")
            self.old_copy = self.output / "healthy-preupgrade-complete"
            shutil.copytree(old.data, self.old_copy)
            manifest = data_manifest(self.old_copy)
            require(manifest == data_manifest(old.data), "complete old data copy differs")
            write_json(self.output / "healthy-preupgrade-manifest.json", manifest)
            return self.before
        self.check("real-v012-seed-normal-stop-complete-copy", seed_old)
        upgraded = Runtime(self, "healthy-upgraded", data_source=self.old_copy)

        def upgrade():
            upgraded.start()
            after = database_state(upgraded.db)
            require(after["live_business_sha256"] == self.before["live_business_sha256"], "upgrade changed prior legitimate business values")
            verify_archives(upgraded.db, self.hourly_expected)
            require({16: 1, 17: 1, 18: 1}.items() <= dict(after["migrations"]).items(), "new migrations are not successful")
            detail = upgraded.api("/api/v1/channel/detail/1")
            detail["max_concurrency"] = 9
            detail["auto_add_groups"] = True
            upgraded.api("/api/v1/channel/update", "POST", detail)
            require(len(upgraded.api("/api/v1/group/list")[0]["items"]) == 3, "first product save did not add exactly one member")
            upgraded.api("/api/v1/channel/update", "POST", detail)
            require(len(upgraded.api("/api/v1/group/list")[0]["items"]) == 3, "repeat product save duplicated a member")
            return {"before_live": self.before["live_business_sha256"], "after_live": after["live_business_sha256"], "archived_hourly_rows": len(self.hourly_expected), "migrations": after["migrations"], "first_save_members": 3}
        self.check("new-artifact-upgrade-first-save-repeat-save", upgrade)

        def routes():
            self.chat(upgraded)
            require(Fixture.calls[-1]["key_index"] == 1, "manual active item did not use its key")
            upgraded.api("/api/v1/group/update/1", "POST", {"active_item_id": 2})
            self.chat(upgraded, stream=True)
            require(Fixture.calls[-1]["key_index"] == 2, "changed active item did not use its key")
            stats = until(lambda: (value if (value := upgraded.api("/api/v1/stats/total"))["request_success"] == 3 else None), "exact route statistics")
            require(stats["input_token"] == 9 and stats["output_token"] == 6, "normal and stream token statistics differ")
            upgraded.api("/api/v1/channel/enable", "POST", {"id": 1, "enabled": False})
            self.chat(upgraded, success=False)
            upgraded.api("/api/v1/channel/enable", "POST", {"id": 1, "enabled": True})
            return {"normal": True, "stream": True, "active_keys": [1, 2], "disabled_upstream_calls": 0, "tokens": [9, 6], "successful_requests": 3}
        self.check("normal-stream-current-member-disabled-route-statistics", routes)

        def restart():
            upgraded.stop()
            before = database_state(upgraded.db)
            upgraded.start()
            after = database_state(upgraded.db)
            require(before["business_sha256"] == after["business_sha256"], "restart lost persistent values")
            return {"before": before["business_sha256"], "after": after["business_sha256"]}
        self.check("new-artifact-normal-stop-restart-persistence", restart)

        def conflict():
            conflicting = copy.deepcopy(self.seed)
            conflicting["channels"][0]["base_url"] = "http://127.0.0.1:2"
            write_json(self.output / "conflicting-backup.json", conflicting)
            before = database_state(upgraded.db, True)
            upgraded.import_backup(json.dumps(conflicting).encode(), expected=409)
            after = database_state(upgraded.db, True)
            require(before["business_sha256"] == after["business_sha256"], "HTTP conflict changed entities")
            return {"status": 409, "entity_digest_unchanged": True}
        self.check("real-http-conflict-409-and-no-partial-write", conflict)
        password = "synthetic-b04-encrypted-backup-only"

        def encrypted_restore():
            raw = upgraded.api("/api/v1/setting/export", "POST", {"password": password}, raw=True)
            require(b'"ciphertext"' in raw and b"synthetic-b04-upstream" not in raw, "export is not encrypted")
            (self.output / "current.encrypted.json").write_bytes(raw)
            restored = Runtime(self, "restored-empty")
            restored.start()
            before = database_state(restored.db, True)
            restored.import_backup(raw, "incorrect-synthetic-password", expected=400)
            require(database_state(restored.db, True)["business_sha256"] == before["business_sha256"], "wrong password changed data")
            restored.import_backup(raw, password)
            restored.stop()
            upgraded.stop()
            require(database_state(restored.db)["business_sha256"] == database_state(upgraded.db)["business_sha256"], "restored actual business rows differ")
            restored.start()
            require(restored.api("/api/v1/channel/detail/1")["max_concurrency"] == 9, "restored cache lost saved configuration")
            restored.stop()
            upgraded.start()
            self.encrypted = raw
            return {"backup_sha256": sha(self.output / "current.encrypted.json"), "wrong_password_status": 400, "actual_rows_match": True, "restored_restart": True}
        self.check("encrypted-export-empty-restore-and-restart", encrypted_restore)

        def rollback():
            fallback = Runtime(self, "old-complete-copy-recovery", legacy=True, data_source=self.old_copy)
            fallback.start()
            require(database_state(fallback.db)["business_sha256"] == self.before["business_sha256"], "old artifact with pre-upgrade copy differs")
            fallback.stop()
            return {"real_old_artifact": True, "preupgrade_complete_copy": True, "normal_exit": True}
        self.check("old-artifact-and-complete-preupgrade-copy-recovery", rollback)

        old_bad = Runtime(self, "mismatch-old", legacy=True)

        def legacy_mismatch():
            old_bad.start()
            old_bad.import_backup(json.dumps(self.seed).encode())
            other = copy.deepcopy(self.seed)
            for table in ("channels", "channel_keys", "channel_models", "channel_grants", "group_categories", "groups", "group_items", "api_keys", "webhook_endpoints"):
                for row in other[table]:
                    row["id"] += 1000
            for table in ("channel_keys", "channel_models"):
                for row in other[table]:
                    row["channel_id"] += 1000
            for row in other["channel_grants"]:
                row["channel_key_id"] += 1000
                row["channel_model_id"] += 1000
            for row in other["group_items"]:
                row["group_id"] += 1000
                row["channel_grant_id"] += 1000
            other["groups"][0]["name"] += "-other"
            other["groups"][0]["active_item_id"] += 1000
            other["groups"][0]["category_id"] += 1000
            other["channels"][0]["name"] += "-other"
            other["group_categories"][0]["name"] += " other"
            other["api_keys"][0]["api_key"] = "synthetic-b04-other-client"
            other["api_keys"][0]["name"] += "-other"
            other["webhook_endpoints"][0]["name"] += "-other"
            other["channel_keys"][0]["id"] = 1
            other["channel_grants"][0]["channel_key_id"] = 1
            old_bad.import_backup(json.dumps(other).encode())
            old_bad.stop()
            with sqlite3.connect(old_bad.db) as conn:
                mismatches = conn.execute("SELECT COUNT(*) FROM channel_grants g JOIN channel_models m ON m.id=g.channel_model_id JOIN channel_keys k ON k.id=g.channel_key_id WHERE m.channel_id<>k.channel_id").fetchone()[0]
            require(mismatches > 0, "real old product did not reproduce C03")
            self.bad_copy = self.output / "mismatch-preupgrade-complete"
            shutil.copytree(old_bad.data, self.bad_copy)
            require(data_manifest(self.bad_copy) == data_manifest(old_bad.data), "old mismatch complete copy differs")
            write_json(self.output / "mismatch-preupgrade-manifest.json", data_manifest(self.bad_copy))
            self.bad_before = database_state(old_bad.db, True)
            return {"real_v012_imports": 2, "mismatched_grants": mismatches, "members": self.bad_before["members"]}
        self.check("real-v012-c03-reproduction-and-complete-copy", legacy_mismatch)
        bad = Runtime(self, "mismatch-upgraded", data_source=self.bad_copy)

        def protected_mismatch():
            bad.start()
            require(database_state(bad.db, True)["business_sha256"] == self.bad_before["business_sha256"], "upgrade guessed or removed mismatched entities")
            bad.api("/api/v1/channel/detail/1", expected=409, raw=True)
            groups = bad.api("/api/v1/group/list")
            require(any(item.get("unavailable_reason") for group in groups if group["id"] == 1 for item in group["items"]), "old invalid member was not visibly unavailable")
            self.chat(bad, success=False)
            self.chat(bad, stream=True, success=False)
            require(database_state(bad.db, True)["business_sha256"] == self.bad_before["business_sha256"], "protected reads removed old members")
            bad.stop()
            bad.start()
            bad.api("/api/v1/channel/detail/1", expected=409, raw=True)
            return {"editing_status": 409, "unavailable_member_visible": True, "normal_and_stream_upstream_calls": 0, "members_preserved": True, "restart_protection": True}
        self.check("upgraded-old-mismatch-read-save-route-restart-protection", protected_mismatch)

        if self.platform == "docker":
            def permissions_recreate():
                uid = self.command(["docker", "exec", upgraded.container, "sh", "-c", "awk '/^Uid:/{print $2}' /proc/1/status"]).strip()
                require(uid == "10001" and upgraded.db.stat().st_uid == 10001, "application or SQLite volume owner differs")
                require(upgraded.db.stat().st_gid == self.data_gid and upgraded.data.stat().st_mode & 0o777 == 0o770, "volume group or directory mode differs")
                upgraded.stop()
                before = database_state(upgraded.db)
                upgraded.remove_container()
                upgraded.start()
                require(database_state(upgraded.db)["business_sha256"] == before["business_sha256"], "same-volume container reconstruction lost data")
                return {"pid1_uid": 10001, "database_owner_uid": 10001, "database_group_gid": self.data_gid, "data_directory_mode": "0770", "host_verifier_group_access": True, "same_volume_recreated": True, "business_sha256": before["business_sha256"]}
            self.check("linux-volume-permissions-and-same-volume-container-recreate", permissions_recreate)

            def compose_restore():
                composed = Runtime(self, "compose-entry", compose=True)
                composed.start()
                composed.import_backup(self.encrypted, password)
                composed.stop()
                before = database_state(composed.db)
                composed.remove_container()
                composed.start()
                require(database_state(composed.db)["business_sha256"] == before["business_sha256"], "Compose reconstruction changed data")
                composed.stop()
                return {"compose_source_sha256": sha(self.compose_file), "encrypted_restore": True, "normal_stop": True, "same_volume_recreated": True}
            self.check("documented-compose-entry-restore-and-recreate", compose_restore)

        if self.hold:
            write_json(self.output / "browser-ready.json", {"healthy": upgraded.url, "mismatch": bad.url, "conflict_fixture": str(self.output / "conflicting-backup.json"), "version": self.version})
            print("READY_FOR_BROWSER " + str(self.output / "browser-ready.json"), flush=True)
            until(lambda: (self.output / "browser-done.json").is_file(), "actual product browser acceptance", 2700)
            def browser_result():
                evidence = json.loads((self.output / "browser-done.json").read_text(encoding="utf-8-sig"))
                require(evidence.get("passed") is True, "browser evidence did not pass")
                require(upgraded.api("/api/v1/channel/detail/1")["max_concurrency"] == 11, "UI save did not persist to the real server")
                require(database_state(bad.db, True)["business_sha256"] == self.bad_before["business_sha256"], "UI editing error removed old members")
                return {"evidence_sha256": sha(self.output / "browser-done.json"), "saved_max_concurrency": 11, "old_members_preserved": True}
            self.check("real-artifact-channel-ui-save-and-error-display", browser_result)

    def cleanup(self):
        result = []
        for runtime in reversed(self.runtimes):
            try:
                runtime.stop()
                if self.platform == "docker":
                    runtime.remove_container()
                result.append({"runtime": runtime.name, "normal_stop": True, "owned_container_removed": self.platform == "docker"})
            except Exception as error:
                result.append({"runtime": runtime.name, "normal_stop": False, "error_type": type(error).__name__})
        self.fixture_server.shutdown()
        self.fixture_server.server_close()
        self.fixture_thread.join(timeout=5)
        write_json(self.output / "cleanup.json", result)
        require(all(row["normal_stop"] for row in result), "one or more owned runtimes did not stop cleanly")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--platform", choices=("windows", "docker"), required=True)
    parser.add_argument("--candidate", type=pathlib.Path, required=True)
    parser.add_argument("--legacy-zip", type=pathlib.Path)
    parser.add_argument("--image", default="")
    parser.add_argument("--legacy-image", default="")
    parser.add_argument("--compose-file", type=pathlib.Path)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--hold-for-browser", action="store_true")
    args = parser.parse_args()
    suite = Suite(args)
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
            write_json(suite.output / "summary.json", {"passed": passed, "platform": args.platform, "version": suite.version, "source_commit": suite.metadata["source_commit"], "source_commit_exact": suite.metadata["source_commit_exact"], "source_tree_sha256": suite.metadata["source_tree_sha256"], "current_image": suite.image or None, "old_image": suite.legacy_image or None, "checks": len(suite.checks), "browser_requested": suite.hold, "upstream_calls": Fixture.calls, "acceptance_script_sha256": sha(__file__)})


if __name__ == "__main__":
    main()
