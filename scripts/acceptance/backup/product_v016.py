"""Real v0.1.5 -> v0.1.6 product scenarios; run against a sealed candidate.

Source/fixture checks are preparation only. Product execution requires explicit
isolated-write permission and authentic old/current artifact identities.
"""
import argparse
import copy
import datetime
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import threading
import time
import urllib.parse
import urllib.error
import urllib.request

import product as base

PREVIOUS_COMMIT = "1856fd5fcfa52e5d27798dc2aeea549d58dd0fa6"
PREVIOUS_MANIFEST = "832e5a010eb5ce0466bd44f9d302d58578f0b4a38212086a5a7ef171393bdbbb"
FIXTURE_CONTRACT = "v016-product-fixtures-1"
BACKUP_PASSWORD = "synthetic-v016-encrypted-backup-only"
SOURCE_FILES = (
    "scripts/acceptance/backup/product_v016.py", "scripts/acceptance/backup/product.py",
    "scripts/acceptance/backup/hourly_archive.py", "scripts/acceptance/backup/browser.mjs",
    "scripts/acceptance/v016/main.go", "scripts/acceptance/v016/native.py",
)
sha, require, until = base.sha, base.require, base.until


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def exported(runtime):
    raw = runtime.export_backup()
    data = runtime.suite.inspect_backup(raw)
    data.pop("exported_at", None)
    return data


def immutable_image(reference):
    require(re.fullmatch(r"sha256:[0-9a-f]{64}", reference) is not None, "immutable image ID required")


def image_matches(command, reference, archive):
    immutable_image(reference)
    with tarfile.open(archive) as saved:
        manifest = json.load(saved.extractfile("manifest.json"))
        require(len(manifest) == 1, "one reviewed saved image required")
        config_raw = saved.extractfile(manifest[0]["Config"]).read()
        config = json.loads(config_raw)
        config_id = "sha256:" + hashlib.sha256(config_raw).hexdigest()
        identities = {config_id}
        if "index.json" in saved.getnames():
            index = json.load(saved.extractfile("index.json"))
            for descriptor in index["manifests"]:
                value = descriptor["digest"]
                immutable_image(value)
                raw = saved.extractfile("blobs/sha256/" + value.split(":")[1]).read()
                require("sha256:" + hashlib.sha256(raw).hexdigest() == value, "saved image descriptor differs")
                body = json.loads(raw)
                if body.get("config", {}).get("digest") == config_id:
                    identities.add(value)
            # Docker's containerd store can report the digest of the wrapper.
            raw_index = saved.extractfile("index.json").read()
            identities.add("sha256:" + hashlib.sha256(raw_index).hexdigest())
    actual = json.loads(command(["docker", "image", "inspect", reference]))
    require(len(actual) == 1 and actual[0]["Os"] == "linux" and actual[0]["Architecture"] == "amd64", "image platform differs")
    require(actual[0]["RootFS"]["Layers"] == config["rootfs"]["diff_ids"], "loaded image layers differ from the reviewed tar")
    require(reference in identities, "image ID is not in the reviewed saved image")
    return {"reference": reference, "config_id": config_id, "tar_sha256": sha(archive), "layers_verified": True}


def fixture_inputs(directory):
    manifest = json.loads((directory / "manifest.json").read_text(encoding="utf-8"))
    expected = {f"dump-{v}.json" for v in range(1000, 1007)} | {"no-archives-1004.json", "format1-1006.json", "key-only-1006.json", "notify-only-1006.json"}
    require(manifest.get("schema") == 1 and manifest.get("contract") == FIXTURE_CONTRACT and manifest.get("synthetic") is True, "frozen synthetic fixture contract required")
    require(set(manifest["files"]) == expected, "fixture inventory differs")
    result = {}
    for name, expected_sha in manifest["files"].items():
        require(sha(directory / name) == expected_sha, "synthetic fixture bytes differ: " + name)
        result[name] = json.loads((directory / name).read_text(encoding="utf-8"))
    return manifest, result


def validate_parameters(args):
    require(re.fullmatch(r"[0-9a-f]{40}", args.commit) is not None, "full source commit required")
    for value in (args.artifact_sha, args.materials_sha, args.previous_manifest_sha):
        require(re.fullmatch(r"[0-9a-f]{64}", value) is not None, "external reviewed digest required")
    require(args.previous_manifest_sha == PREVIOUS_MANIFEST, "real published v0.1.5 manifest required")
    require(not args.output.exists(), "product output must be new")
    for protected in (args.candidate, args.previous_artifact, args.fixtures):
        require(args.output != protected and protected not in args.output.parents and args.output not in protected.parents, "output overlaps a protected input")
    if args.platform == "windows":
        require(sys.platform == "win32", "native Windows required")
        require(args.hold_for_browser or args.validate_only, "Windows product scenarios require the actual browser stage")
    else:
        require(sys.platform.startswith("linux"), "Linux Docker Engine host required")
        immutable_image(args.image)
        immutable_image(args.previous_image)
    require(args.validate_only or args.allow_isolated_write, "explicit isolated product/database permission required")


class Fixture(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        pass

    def reply(self, status, raw, content_type="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Connection", "close")
        self.end_headers()
        try:
            self.wfile.write(raw)
        except (BrokenPipeError, ConnectionResetError):
            pass
        self.close_connection = True

    def do_GET(self):
        models = ["v016-model-a", "v016-model-b", "v016-model-preview", "v016-model-retired", "other-model"]
        self.reply(200, json.dumps({"object": "list", "data": [{"id": m, "object": "model"} for m in models]}).encode())

    def do_POST(self):
        suite = self.server.suite
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        if self.path.startswith("/hooks/"):
            with suite.lock:
                suite.hooks.append({"path": self.path, "body": body})
            self.reply(200, b"{}")
            return
        key = next((i for i in (1, 2) if self.headers.get("Authorization") == f"Bearer synthetic-v016-upstream-{i}"), None)
        require(key is not None, "unexpected synthetic upstream key")
        with suite.lock:
            suite.calls.append({"key": key, "model": body["model"], "stream": bool(body.get("stream"))})
            held = suite.held.pop(body["model"], None) if key == 1 else None
        if held is not None:
            require(held.wait(15), "in-flight fixture release timed out")
            self.reply(429, b'{"error":{"code":"quota","message":"v016-quota"}}')
            return
        usage = {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}
        if body.get("stream"):
            raw = ("data: " + json.dumps({"id": "v016-local", "object": "chat.completion.chunk", "model": body["model"], "choices": [{"index": 0, "delta": {"content": "v016-pong"}, "finish_reason": None}]}) + "\n\n" + "data: " + json.dumps({"id": "v016-local", "object": "chat.completion.chunk", "model": body["model"], "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": usage}) + "\n\ndata: [DONE]\n\n").encode()
            self.reply(200, raw, "text/event-stream")
        else:
            self.reply(200, json.dumps({"id": "v016-local", "object": "chat.completion", "created": 1, "model": body["model"], "choices": [{"index": 0, "message": {"role": "assistant", "content": "v016-pong"}, "finish_reason": "stop"}], "usage": usage}).encode())


class Runtime(base.Runtime):
    def __init__(self, suite, name, data_source=None, compose=False):
        super().__init__(suite, name, data_source=data_source, compose=compose)
        self.container = "roam-v016-" + suite.run_id + "-" + name
        self.export_sequence = 0

    def api(self, endpoint, method="GET", body=None, expected=200, raw=False, headers=None):
        started = datetime.datetime.now(datetime.timezone.utc).isoformat()
        data = None if body is None else json.dumps(body).encode()
        request_headers = dict(headers or {})
        if data is not None:
            request_headers["Content-Type"] = "application/json"
        request = urllib.request.Request(self.url + endpoint, data=data, method=method, headers=request_headers)
        record = {"started_at_utc": started, "method": method, "path": endpoint, "expected_status": expected, "status": None}
        try:
            try:
                response = self.client.open(request, timeout=25)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                content = response.read()
                record.update(status=response.status, response_bytes=len(content), response_sha256=hashlib.sha256(content).hexdigest())
                require(response.status == expected, f"{endpoint} status {response.status}, expected {expected}")
            return content if raw else json.loads(content).get("data") if content else None
        finally:
            record["finished_at_utc"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
            # Never persist login bodies, cookies, auth headers, or passwords.
            with (self.root / "http.jsonl").open("a", encoding="utf-8") as evidence:
                evidence.write(json.dumps(record) + "\n")

    def export_backup(self):
        raw = self.api("/api/v1/setting/export", "POST", {"password": BACKUP_PASSWORD}, raw=True)
        self.export_sequence += 1
        path = self.root / f"export-{self.export_sequence:03d}.encrypted.json"
        with path.open("xb") as evidence:
            evidence.write(raw)
        with (self.root / "exports.jsonl").open("a", encoding="utf-8") as evidence:
            evidence.write(json.dumps({"path": path.name, "bytes": len(raw), "sha256": sha(path), "version": self.suite.version}) + "\n")
        return raw

    def import_backup(self, raw, password="", expected=200):
        response = super().import_backup(raw, password, expected)
        if expected >= 400:
            require(b"synthetic-v016-upstream" not in response and b"synthetic-v016-client" not in response and (not password or password.encode() not in response), "failed import exposed synthetic credential/password input")
        return response

    def start(self):
        self.sequence += 1
        if self.suite.platform == "windows":
            env = {k: v for k, v in os.environ.items() if not k.upper().startswith("OCTOPUS_ROAM_") and k.upper() not in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY")}
            env.update(OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE="1", OCTOPUS_ROAM_CODEX_CATALOG_ENABLED="false", OCTOPUS_ROAM_DATABASE_TYPE="sqlite", OCTOPUS_ROAM_DATABASE_PATH=str(self.db), OCTOPUS_ROAM_SERVER_HOST="127.0.0.1", OCTOPUS_ROAM_SERVER_PORT=str(self.port), OCTOPUS_ROAM_LOG_LEVEL="error")
            with (self.root / f"application-{self.sequence}.log").open("xb") as output:
                self.process = subprocess.Popen([str(self.executable), "start"], cwd=self.root, env=env, stdin=subprocess.DEVNULL, stdout=output, stderr=output, creationflags=subprocess.CREATE_NO_WINDOW)
        elif self.compose:
            self._compose_start()
        elif self.container_created:
            self.suite.command(["docker", "start", self.container])
        else:
            self.suite.command(["docker", "run", "--detach", "--pull", "never", "--name", self.container, "--network", "host", "--label", "roam.task=v016-product", "--label", "roam.run=" + self.suite.run_id, "--mount", f"type=bind,source={self.data},target=/app/data", "--env", "PUID=10001", "--env", "PGID=" + str(self.suite.data_gid), "--env", "OCTOPUS_ROAM_SERVER_HOST=127.0.0.1", "--env", "OCTOPUS_ROAM_SERVER_PORT=" + str(self.port), "--env", "OCTOPUS_ROAM_CODEX_CATALOG_ENABLED=false", "--env", "OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1", "--env", "OCTOPUS_ROAM_LOG_LEVEL=error", self.suite.image])
            self.container_created = True
        until(lambda: self.api("/", raw=True), self.name + " readiness")
        self.api("/api/v1/user/login", "POST", {"username": "admin", "password": "admin", "expire": -1})
        require(self.api("/api/v1/update/now-version") == self.suite.version, "real runtime version differs from selected artifact")

    def _compose_start(self):
        if not self.container_created:
            override = self.root / "compose-override.yaml"
            override.write_text("services:\n  octopus-roam:\n    image: " + json.dumps(self.suite.image) + "\n    container_name: " + self.container + "\n    network_mode: host\n    restart: 'no'\n    ports: !reset []\n    volumes: !override\n      - " + json.dumps(str(self.data) + ":/app/data") + "\n    labels:\n      roam.task: v016-product\n      roam.run: " + self.suite.run_id + "\n    environment:\n      PUID: '10001'\n      PGID: '" + str(self.suite.data_gid) + "'\n      OCTOPUS_ROAM_SERVER_HOST: 127.0.0.1\n      OCTOPUS_ROAM_SERVER_PORT: '" + str(self.port) + "'\n      OCTOPUS_ROAM_CODEX_CATALOG_ENABLED: 'false'\n      OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE: '1'\n      OCTOPUS_ROAM_LOG_LEVEL: error\n", encoding="utf-8")
            self.compose_args = ["docker", "compose", "--project-name", self.container, "-f", str(self.suite.compose_file), "-f", str(override)]
            config = json.loads(self.suite.command(self.compose_args + ["config", "--format", "json"], {"OCTOPUS_ROAM_VERSION": self.suite.version}))
            service = config["services"]["octopus-roam"]
            require(service["image"] == self.suite.image and service["network_mode"] == "host" and not service.get("ports"), "Compose override did not keep the isolated loopback contract")
        self.suite.command(self.compose_args + ["up", "--detach", "--no-build", "--pull", "never"], {"OCTOPUS_ROAM_VERSION": self.suite.version})
        self.container_created = True

    def remove_container(self):
        if self.container_created:
            self.stop()
            labels = json.loads(self.suite.command(["docker", "inspect", "--format", "{{json .Config.Labels}}", self.container]))
            require(labels.get("roam.task") == "v016-product" and labels.get("roam.run") == self.suite.run_id, "unowned container cleanup refused")
            self.suite.command(["docker", "rm", self.container])
            self.container_created = False


class Suite(base.Suite):
    def __init__(self, args, metadata, manifest, fixtures, previous):
        self.args, self.metadata, self.manifest, self.fixtures = args, metadata, manifest, fixtures
        self.platform, self.output, self.version = args.platform, args.output, "v0.1.6"
        self.output.mkdir(parents=True)
        self.backup_inspector = self.output / ("inspect-backup.exe" if args.platform == "windows" else "inspect-backup")
        self.command(["go", "build", "-o", str(self.backup_inspector), "./scripts/acceptance/v016"], {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOFLAGS": "-mod=readonly"})
        base.write_json(self.output / "backup-inspector.json", {"source_commit": args.commit, "sha256": sha(self.backup_inspector), "database_connected": False})
        self.run_id = hashlib.sha256(str(self.output).encode()).hexdigest()[:10]
        self.archive = args.candidate / "archives/octopus-roam-windows-amd64.zip"
        self.binary_sha = next(row["sha256"] for row in metadata["binaries"] if row["target"] == "windows/amd64")
        self.image, self.data_gid = args.image, os.getgid() if self.platform == "docker" else None
        self.compose_file = args.candidate / "source/docker-compose.yml"
        self.runtimes, self.checks, self.calls, self.hooks, self.held = [], [], [], [], {}
        self.lock = threading.Lock()
        self.hold = args.hold_for_browser
        self.previous_view = copy.copy(self)
        self.previous_view.version = "v0.1.5"
        build = json.loads((args.previous_artifact / "build.json").read_text(encoding="utf-8"))
        self.previous_view.binary_sha = build["binaries"]["windows-amd64"]
        self.previous_view.archive = args.previous_artifact / "archives/octopus-roam-windows-amd64.zip"
        self.previous_view.image = args.previous_image
        if self.platform == "docker":
            self.image_evidence = {
                "previous": image_matches(self.command, args.previous_image, args.previous_artifact / "images/amd64.tar"),
                "current": image_matches(self.command, args.image, Path(metadata["source_artifact"]) / "images/amd64.tar"),
            }
        self.fixture_server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        self.fixture_server.suite = self
        self.fixture_server.daemon_threads = True
        self.fixture_thread = threading.Thread(target=self.fixture_server.serve_forever, daemon=True)
        self.fixture_thread.start()
        self.fixture_url = f"http://127.0.0.1:{self.fixture_server.server_port}"

    def inspect_backup(self, raw):
        request = json.dumps({"password": BACKUP_PASSWORD, "backup": json.loads(raw)}).encode()
        started = datetime.datetime.now(datetime.timezone.utc).isoformat()
        arguments = [str(self.backup_inspector), "--inspect-backup"]
        result = subprocess.run(arguments, input=request, capture_output=True, timeout=30,
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        record = {"command": arguments, "exit_code": result.returncode, "started_at_utc": started,
                  "finished_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  "encrypted_sha256": hashlib.sha256(raw).hexdigest(),
                  "plaintext_sha256": hashlib.sha256(result.stdout).hexdigest(),
                  "stderr_sha256": hashlib.sha256(result.stderr).hexdigest()}
        with (self.output / "backup-inspections.jsonl").open("a", encoding="utf-8") as evidence:
            evidence.write(json.dumps(record) + "\n")
        require(result.returncode == 0, "authenticated backup inspection failed; original encrypted bytes retained")
        data = json.loads(result.stdout)
        require(data.get("product") == "octopus-roam" and data.get("version") in range(1000, 1007), "inspected backup inner identity differs")
        return data

    def check(self, name, function):
        started = datetime.datetime.now(datetime.timezone.utc).isoformat()
        try:
            return super().check(name, function)
        finally:
            self.checks[-1].update(started_at_utc=started, finished_at_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
            base.write_json(self.output / "checks.json", self.checks)

    def cleanup(self):
        super().cleanup()
        for runtime in self.runtimes:
            if runtime.name == "real-v015" and not (self.output / "complete-v015-preupgrade").exists():
                snapshot = self.output / "failure-preserved-v015-preupgrade"
                before = base.data_manifest(runtime.root)
                shutil.copytree(runtime.root, snapshot)
                require(before == base.data_manifest(runtime.root) == base.data_manifest(snapshot), "failed old preparation snapshot differs")
                base.write_json(self.output / "failure-preupgrade-manifest.json", before)

    def seed(self, name):
        value = copy.deepcopy(self.fixtures[name])
        for channel in value["channels"]:
            channel["base_url"] = self.fixture_url
        for endpoint in value.get("webhook_endpoints", []):
            endpoint["url"] = self.fixture_url + "/hooks/legacy"
        return value

    def chat(self, runtime, group="v016-a", stream=False, expected=200):
        connection = self.begin_chat(runtime, group, stream)
        try:
            response = connection.getresponse()
            raw = response.read()
            require(response.status == expected, "actual relay status differs")
            if expected == 200:
                require(b"v016-pong" in raw and (not stream or b"[DONE]" in raw), "normal/stream response differs")
        finally:
            connection.close()

    def begin_chat(self, runtime, group, stream=False):
        connection = http.client.HTTPConnection("127.0.0.1", runtime.port, timeout=20)
        connection.request("POST", "/v1/chat/completions", body=json.dumps({"model": group, "stream": stream, "messages": [{"role": "user", "content": "synthetic-v016"}]}), headers={"Content-Type": "application/json", "Authorization": "Bearer synthetic-v016-client"})
        return connection

    def rule_state(self, runtime, group):
        value = next(row for row in runtime.api("/api/v1/group/list") if row["id"] == group)
        return value["runtime"]["rule_cooldowns"]

    def cooldown_events(self):
        with self.lock:
            return [copy.deepcopy(row["body"]) for row in self.hooks if row["path"] == "/hooks/cooldown" and row["body"].get("event") == "cooldown"]

    def cooldown(self, runtime):
        legacy_before = sum(row["path"] == "/hooks/legacy" for row in self.hooks)
        body = '{"event":{{json .Event}},"scope":{{json .Scope}},"key_id":{{json .KeyID}},"until":{{json .CooldownUntil}}}'
        runtime.api("/api/v1/webhook/create", "POST", {"name": "synthetic-v016-cooldown", "enabled": True, "url": self.fixture_url + "/hooks/cooldown", "body_template": body, "method": "POST", "content_type": "application/json", "headers": {}, "timeout_seconds": 3})
        a, b = threading.Event(), threading.Event()
        with self.lock:
            self.held = {"v016-model-a": a, "v016-model-b": b}
            before_calls = len(self.calls)
        connections = [self.begin_chat(runtime, "v016-a"), self.begin_chat(runtime, "v016-b")]
        try:
            until(lambda: len(self.calls) >= before_calls + 2, "two real in-flight upstream requests")
            a.set()
            first = until(lambda: self.rule_state(runtime, 1), "first Key cooldown")[0]
            until(lambda: len(self.cooldown_events()) == 1, "one first-period notification")
            require(first["scope"] == "key" and first["key_id"] == 1 and not first.get("model_name"), "Key scope identity differs")
            require(self.rule_state(runtime, 2)[0]["key_id"] == 1 and not self.rule_state(runtime, 3), "same-Key model or other-Key isolation differs")
            time.sleep(0.8)
            b.set()
            extended = until(lambda: (hits if (hits := self.rule_state(runtime, 1)) and hits[0]["until"] > first["until"] else None), "actual active-period extension")[0]
            for connection in connections:
                response = connection.getresponse()
                response.read()
                require(response.status >= 400, "synthetic quota response did not fail")
            self.chat(runtime, "v016-other-key")
            require(self.calls[-1]["key"] == 2, "other Key was not actually used")
            time.sleep(0.8)
            require(len(self.cooldown_events()) == 1, "active extension duplicated a notification")
            until(lambda: not self.rule_state(runtime, 1), "real cooldown expiry", 15)
            next_error = threading.Event()
            with self.lock:
                self.held = {"v016-model-a": next_error}
                before_calls = len(self.calls)
            connection = self.begin_chat(runtime, "v016-a")
            connections.append(connection)
            until(lambda: len(self.calls) > before_calls, "new-period real upstream request")
            next_error.set()
            response = connection.getresponse()
            response.read()
            require(response.status >= 400, "new-period request did not fail")
            until(lambda: len(self.cooldown_events()) == 2, "new-period notification")
            require(all(event["scope"] == "key" and event["key_id"] == 1 for event in self.cooldown_events()), "notification target identity differs")
            require(sum(row["path"] == "/hooks/legacy" for row in self.hooks) == legacy_before, "unsupported cooldown branch of the old template reached the HTTP receiver")
            return {"first_until": first["until"], "extended_until": extended["until"], "cooldown_notifications": 2, "other_key_actual_success": True, "clock": "real", "two_inflight_requests": True, "old_template_invalid_cooldown_not_sent": True}
        finally:
            a.set()
            b.set()
            for event in self.held.values():
                event.set()
            for connection in connections:
                connection.close()

    def filters(self, runtime):
        if self.hold:
            base.write_json(self.output / "browser-ready.json", {"commit": self.args.commit, "artifact_sha256": self.args.artifact_sha, "product": runtime.url, "required_actions": ["old-global-raw-cancel", "old-channel-raw-cancel", "global-visual-apply-reopen", "channel-visual-apply-reopen", "preview-height"], "expected_global": self.manifest["visual_global_filter"], "expected_channel": self.manifest["visual_channel_filter"]})
            print("READY_FOR_BROWSER " + str(self.output / "browser-ready.json"), flush=True)
            until(lambda: (self.output / "browser-done.json").is_file(), "actual candidate browser evidence", 2700)
            proof = json.loads((self.output / "browser-done.json").read_text(encoding="utf-8-sig"))
            require(proof.get("commit") == self.args.commit and proof.get("artifact_sha256") == self.args.artifact_sha and proof.get("passed") is True, "browser proof identity differs")
            require(set(proof.get("actions", [])) >= {"old-global-raw-cancel", "old-channel-raw-cancel", "global-visual-apply-reopen", "channel-visual-apply-reopen", "preview-height"}, "actual browser scenario inventory is incomplete")
            require(bool(proof.get("files")), "browser traces/screenshots required")
            for name, checksum in proof["files"].items():
                path = (self.output / name).resolve()
                require(self.output in path.parents and sha(path) == checksum, "browser evidence file differs")
        else:
            runtime.api("/api/v1/setting/set", "POST", {"key": "model_filter", "value": self.manifest["visual_global_filter"]})
            detail = runtime.api("/api/v1/channel/detail/1")
            detail["match_regex"] = self.manifest["visual_channel_filter"]
            runtime.api("/api/v1/channel/update", "POST", detail)
        settings = {row["key"]: row["value"] for row in runtime.api("/api/v1/setting/list")}
        require(settings["model_filter"] == self.manifest["visual_global_filter"] and runtime.api("/api/v1/channel/detail/1")["match_regex"] == self.manifest["visual_channel_filter"], "visual expressions did not persist to the real server")
        runtime.api("/api/v1/channel/refresh-model/1", "POST")
        require(sorted(runtime.api("/api/v1/channel/detail/1")["models"]) == self.manifest["expected_models_after_visual_filters"], "real backend filtering did not apply both expressions")
        return {"expressions_persisted": True, "actual_models": self.manifest["expected_models_after_visual_filters"], "browser": "passed" if self.hold else "not_run; actual API/backend scenario only"}

    def run(self):
        old = Runtime(self.previous_view, "real-v015")
        password = BACKUP_PASSWORD

        def old_snapshot():
            old.start()
            old.import_backup(json.dumps(self.seed("dump-1005.json")).encode())
            self.chat(old)
            self.chat(old, "v016-other-key", stream=True)
            self.old_encrypted = old.export_backup()
            require(exported(old)["version"] == 1005, "real old export did not retain native archives")
            old.stop()
            self.before = base.database_state(old.db)
            self.snapshot = self.output / "complete-v015-preupgrade"
            shutil.copytree(old.root, self.snapshot)
            self.snapshot_manifest = base.data_manifest(self.snapshot)
            require(self.snapshot_manifest == base.data_manifest(old.root), "complete old runtime/config/database snapshot differs")
            base.write_json(self.output / "preupgrade-manifest.json", self.snapshot_manifest)
            (self.output / "real-v015.encrypted.json").write_bytes(self.old_encrypted)
            return {"version": "v0.1.5", "commit": PREVIOUS_COMMIT, "manifest_files": len(self.snapshot_manifest), "business_sha256": self.before["business_sha256"], "actual_old_api_and_stream": True}
        self.check("real-v015-data-normal-stop-complete-snapshot", old_snapshot)
        upgraded = Runtime(self, "v015-to-v016", data_source=self.snapshot / "data")

        def upgrade():
            upgraded.start()
            require(base.database_state(upgraded.db)["business_sha256"] == self.before["business_sha256"], "real upgrade changed prior business values")
            state = exported(upgraded)
            require(state["channels"][0]["match_regex"] == self.manifest["raw_channel_filter"], "old channel raw regex changed")
            require(next(s["value"] for s in state["settings"] if s["key"] == "model_filter") == self.manifest["raw_global_filter"], "old global raw regex changed")
            require(all(not r.get("notify_webhook", False) for r in state["channels"][0]["cooldown_rules"]), "missing old notification flag did not default to false")
            require(len(state["stats_hourly_archives"]) == 3 and state["version"] == 1005, "old archive/export contract changed")
            return {"prior_raw_regexes_preserved": True, "old_missing_notify_false": True, "archives": 3, "content_version": 1005}
        self.check("real-v015-to-candidate-upgrade-raw-filters-and-archives", upgrade)

        def legacy_template():
            endpoint = upgraded.api("/api/v1/webhook/list")[0]
            original = endpoint["body_template"]
            for enabled in (False, True):
                endpoint["enabled"] = enabled
                upgraded.api("/api/v1/webhook/update/1", "POST", endpoint)
            upgraded.api("/api/v1/webhook/test/1", "POST")
            require(upgraded.api("/api/v1/webhook/list")[0]["body_template"] == original, "old template changed during edit/toggle")
            endpoint.pop("id", None)
            endpoint["name"] = "synthetic-v016-legacy-created"
            upgraded.api("/api/v1/webhook/create", "POST", endpoint)
            return {"actual_old_template_edit_disable_enable_test_create": True, "loopback_only": True}
        self.check("actual-legacy-webhook-template-operations", legacy_template)
        self.check("global-channel-visual-filters-and-actual-model-refresh", lambda: self.filters(upgraded))

        for name in sorted(self.fixtures):
            def restore_fixture(name=name):
                restored = Runtime(self, "matrix-" + name.replace(".json", ""))
                restored.start()
                raw = json.dumps(self.seed(name)).encode()
                restored.import_backup(raw)
                first = exported(restored)
                restored.import_backup(raw)
                second = exported(restored)
                require(digest(first) == digest(second), "repeat import changed actual exported rows: " + name)
                source = self.fixtures[name]
                expected_version = 1006 if source["version"] == 1006 else 1005 if any(a["format"] == 2 for a in source.get("stats_hourly_archives", [])) else 1004
                require(second["version"] == expected_version, "real export version contract differs: " + name)
                by_id = lambda rows: sorted(rows, key=lambda row: row["id"])
                require(by_id(second.get("stats_hourly_archives", [])) == by_id(source.get("stats_hourly_archives", [])), "archive evidence changed during actual restore/replay")
                restored.stop()
                before = base.database_state(restored.db)
                restored.start()
                require(base.database_state(restored.db)["business_sha256"] == before["business_sha256"], "matrix restart changed persistent rows")
                restored.stop()
                return {"input_version": source["version"], "export_version": expected_version, "actual_imports": 2, "restart": True, "archives": len(source.get("stats_hourly_archives", []))}
            self.check("actual-empty-restore-repeat-" + name, restore_fixture)

        detail = upgraded.api("/api/v1/channel/detail/1")
        detail["cooldown_rules"] = self.fixtures["dump-1006.json"]["channels"][0]["cooldown_rules"]
        upgraded.api("/api/v1/channel/update", "POST", detail)
        self.check("actual-key-scope-notification-periods-and-extension", lambda: self.cooldown(upgraded))

        def restart_restore_and_failures():
            self.encrypted = upgraded.export_backup()
            current = exported(upgraded)
            require(current["version"] == 1006 and len(current["stats_hourly_archives"]) == 3, "1006 and both archive formats did not coexist in the real export")
            require(b'"ciphertext"' in self.encrypted and b"synthetic-v016-upstream" not in self.encrypted, "current backup is not encrypted")
            (self.output / "real-v016-1006.encrypted.json").write_bytes(self.encrypted)
            upgraded.stop()
            before = base.database_state(upgraded.db)
            upgraded.start()
            require(base.database_state(upgraded.db)["business_sha256"] == before["business_sha256"] and not self.rule_state(upgraded, 1), "restart changed persisted rows or restored runtime cooldown")
            self.chat(upgraded)
            upgraded.stop()
            expected = base.database_state(upgraded.db)
            # Export after the final normal stop via a fresh, unchanged startup.
            upgraded.start()
            self.encrypted = upgraded.export_backup()
            (self.output / "real-v016-after-restart.encrypted.json").write_bytes(self.encrypted)
            upgraded.stop()
            restored = Runtime(self, "encrypted-empty-restore")
            restored.start()
            original = base.database_state(restored.db)
            restored.import_backup(self.encrypted, "wrong-synthetic-password", expected=400)
            require(base.database_state(restored.db)["business_sha256"] == original["business_sha256"], "wrong password changed data")
            restored.import_backup(self.encrypted, password)
            restored.import_backup(self.encrypted, password)
            restored.stop()
            require(base.database_state(restored.db)["business_sha256"] == expected["business_sha256"], "real encrypted restore/replay differs from saved source rows")
            restored.start()
            valid = exported(restored)
            bad = copy.deepcopy(valid)
            bad["channels"][0]["base_url"] = "http://127.0.0.1:2"
            original = base.database_state(restored.db)
            restored.import_backup(json.dumps(bad).encode(), expected=409)
            require(base.database_state(restored.db)["business_sha256"] == original["business_sha256"], "late conflicting import changed entities")
            restored.stop()
            fallback = Runtime(self.previous_view, "v015-failure-recovery", data_source=self.snapshot / "data")
            fallback.start()
            original = base.database_state(fallback.db)
            fallback.import_backup(self.encrypted, password, expected=400)
            require(base.database_state(fallback.db)["business_sha256"] == original["business_sha256"], "real v0.1.5 changed data while rejecting 1006")
            fallback.stop()
            require(base.database_state(fallback.db)["business_sha256"] == self.before["business_sha256"], "old program plus complete snapshot did not recover the original data")
            require(base.data_manifest(self.snapshot) == self.snapshot_manifest, "original complete snapshot was modified")
            self.restored = restored
            return {"content_version": 1006, "formats": [1, 2], "wrong_password_status": 400, "conflict_status": 409, "real_v015_rejection_status": 400, "old_rejected_data_unchanged": True, "runtime_cooldown_cleared_on_restart": True, "snapshot_unchanged": True}
        self.check("1006-encrypted-restore-repeat-restart-and-real-v015-reject-recovery", restart_restore_and_failures)

        if self.platform == "docker":
            def recreate():
                self.restored.start()
                uid = self.command(["docker", "exec", self.restored.container, "sh", "-c", "awk '/^Uid:/{print $2}' /proc/1/status"]).strip()
                require(uid == "10001" and self.restored.db.stat().st_uid == 10001 and self.restored.db.stat().st_gid == self.data_gid and self.restored.data.stat().st_mode & 0o777 == 0o770, "actual Linux volume user/group/permissions differ")
                self.restored.stop()
                before = base.database_state(self.restored.db)
                self.restored.remove_container()
                self.restored.start()
                require(base.database_state(self.restored.db)["business_sha256"] == before["business_sha256"], "same-volume recreation changed restored 1006 data")
                self.restored.stop()
                composed = Runtime(self, "compose-1006", compose=True)
                composed.start()
                composed.import_backup(self.encrypted, password)
                composed.stop()
                before = base.database_state(composed.db)
                composed.remove_container()
                composed.start()
                require(base.database_state(composed.db)["business_sha256"] == before["business_sha256"], "documented Compose recreation changed 1006 data")
                composed.stop()
                return {"uid": 10001, "gid": self.data_gid, "mode": "0770", "same_volume_recreated": True, "compose_source_sha256": sha(self.compose_file), "compose_1006_restore_and_recreate": True}
            self.check("linux-volume-permissions-recreation-and-compose", recreate)


def parser():
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--repository", type=Path, required=True)
    result.add_argument("--candidate", type=Path, required=True)
    result.add_argument("--commit", required=True)
    result.add_argument("--artifact-sha", required=True)
    result.add_argument("--materials-sha", required=True)
    result.add_argument("--previous-artifact", type=Path, required=True)
    result.add_argument("--previous-manifest-sha", required=True)
    result.add_argument("--platform", choices=("windows", "docker"), required=True)
    result.add_argument("--image", default="")
    result.add_argument("--previous-image", default="")
    result.add_argument("--fixtures", type=Path, required=True)
    result.add_argument("--output", type=Path, required=True)
    result.add_argument("--validate-only", action="store_true")
    result.add_argument("--hold-for-browser", action="store_true")
    result.add_argument("--allow-isolated-write", action="store_true")
    return result


def main():
    args = parser().parse_args()
    for name in ("repository", "candidate", "previous_artifact", "fixtures", "output"):
        setattr(args, name, getattr(args, name).resolve())
    validate_parameters(args)
    require(subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=args.repository, text=True).strip() == args.commit, "exact source checkout required")
    sys.path.insert(0, str(args.repository / "scripts"))
    import release_acceptance
    release_acceptance.verify_layout(argparse.Namespace(candidate=str(args.candidate), version="v0.1.6", commit=args.commit, artifact_sha=args.artifact_sha, materials_sha=args.materials_sha))
    for name in SOURCE_FILES:
        raw = subprocess.check_output(["git", "cat-file", "blob", args.commit + ":" + name], cwd=args.repository)
        require((args.repository / name).read_bytes() == raw == (args.candidate / "source" / name).read_bytes(), "acceptance source does not match the exact candidate blob: " + name)
    require(sha(args.previous_artifact / "artifact.json") == PREVIOUS_MANIFEST, "reviewed old artifact manifest differs")
    previous = json.loads((args.previous_artifact / "artifact.json").read_text(encoding="utf-8"))
    require(previous["version"] == "v0.1.5" and previous["commit"] == PREVIOUS_COMMIT, "real published v0.1.5 artifact required")
    names = ["build.json", "archives/octopus-roam-windows-amd64.zip" if args.platform == "windows" else "images/amd64.tar"]
    for name in names:
        require(sha(args.previous_artifact / name) == previous["files"][name], "old artifact file differs: " + name)
    manifest, fixtures = fixture_inputs(args.fixtures)
    env = {**os.environ, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOFLAGS": "-mod=readonly"}
    subprocess.run(["go", "run", "./scripts/acceptance/v016", "--check", "--output", str(args.fixtures)], cwd=args.repository, env=env, check=True)
    if args.validate_only:
        print(json.dumps({"status": "inputs_verified", "commit": args.commit, "fixture_manifest_sha256": sha(args.fixtures / "manifest.json"), "actual_product_acceptance": "not_run"}))
        return
    metadata = json.loads((args.candidate / "build-info.json").read_text(encoding="utf-8"))
    suite = Suite(args, metadata, manifest, fixtures, previous)
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
            base.write_json(suite.output / "summary.json", {"product_scenarios_passed": passed, "platform": args.platform, "commit": args.commit, "version": "v0.1.6", "previous_commit": PREVIOUS_COMMIT, "previous_version": "v0.1.5", "previous_artifact_manifest_sha256": PREVIOUS_MANIFEST, "artifact_sha256": args.artifact_sha, "materials_sha256": args.materials_sha, "fixture_manifest_sha256": sha(args.fixtures / "manifest.json"), "acceptance_source_hashes": {name: sha(args.repository / name) for name in SOURCE_FILES}, "checks": len(suite.checks), "image_evidence": getattr(suite, "image_evidence", None), "loopback_upstream_and_notifications": True, "windows_native_helper_and_login": "separate mandatory native.py evidence; not implied by this result"})


if __name__ == "__main__":
    main()
