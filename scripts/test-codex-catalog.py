"""Windows C01 catalog acceptance against a built development executable.

Creates one isolated runtime, uses only synthetic database credentials, and reads
the separately authorized upstream test key from stdin. No dependency installs.
Retains evidence and requests normal shutdown of only the owned native process.
"""
import argparse
import getpass
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import secrets
import select
import socket
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from http.cookies import SimpleCookie

MAX_BODY = 1048576
MAIN = "gpt-6.1-sol"
LUNA = "gpt-5.6-luna"
GROUPS = ["gpt-6-astra", MAIN, LUNA]


def utc():
    return datetime.now(timezone.utc).isoformat()


def sha(data):
    return hashlib.sha256(data).hexdigest()


def write_new(path, data):
    with path.open("xb") as output:
        output.write(data if isinstance(data, bytes) else data.encode("utf-8"))


def free_port():
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


def request(port, method, path, body=None, headers=None, timeout=15):
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
    try:
        data = None if body is None else json.dumps(body).encode()
        outgoing = dict(headers or {})
        if data is not None:
            outgoing["Content-Type"] = "application/json"
        connection.request(method, path, body=data, headers=outgoing)
        response = connection.getresponse()
        raw = response.read(8 * MAX_BODY + 1)
        if len(raw) > 8 * MAX_BODY:
            raise RuntimeError("Acceptance response exceeded limit")
        return response.status, dict(response.getheaders()), raw
    finally:
        connection.close()


def original_entries(raw):
    text = raw.decode("utf-8")
    decoder = json.JSONDecoder()

    def space(index):
        while index < len(text) and text[index].isspace():
            index += 1
        return index

    position = space(0) + 1
    while True:
        position = space(position)
        key, position = decoder.raw_decode(text, position)
        position = space(position)
        assert text[position] == ":"
        position = space(position + 1)
        if key == "models":
            assert text[position] == "["
            position = space(position + 1)
            result = {}
            while text[position] != "]":
                start = position
                model, position = decoder.raw_decode(text, position)
                result[model["slug"]] = text[start:position].encode("utf-8")
                position = space(position)
                if text[position] == ",":
                    position = space(position + 1)
            return result
        _, position = decoder.raw_decode(text, position)
        position = space(position)
        assert text[position] == ","
        position += 1


class QuietServer(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def handle_error(self, *_args):
        # Closed test sockets are diagnostic categories, not raw client addresses.
        self.failures.append(type(sys.exception()).__name__)


def start_server(handler):
    server = QuietServer(("127.0.0.1", 0), handler)
    server.failures = []
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, thread


class QuietHandler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        pass

    def empty(self, status):
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--snapshot", action="append", required=True, metavar="VERSION=FILE")
    parser.add_argument("--expected-binary-sha256", required=True)
    args = parser.parse_args()
    if os.name != "nt":
        raise SystemExit("This lifecycle entry validates Windows native; Linux container acceptance is separate.")
    repository = Path(__file__).resolve().parents[1]
    output = args.output.resolve()
    if not output.is_relative_to(repository) or output == repository:
        raise SystemExit("Output must be a new isolated directory inside the workspace")
    executable = args.executable.resolve(strict=True)
    binary = executable.read_bytes()
    if sha(binary) != args.expected_binary_sha256:
        raise SystemExit("The supplied binary identity does not match")
    references = {}
    for specification in args.snapshot:
        version, filename = specification.split("=", 1)
        if len(version) > 32 or not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
            raise SystemExit("Invalid reference version")
        path = Path(filename).resolve(strict=True)
        raw = path.read_bytes()
        value = json.loads(raw)
        references[version] = {"path": path, "raw": raw, "value": value, "entries": original_entries(raw)}
    if "0.169.0" not in references or len(references) < 2:
        raise SystemExit("0.169.0 and at least one other raw snapshot are required")
    protected = [Path(r"D:\tool\AI\test\model_catalog.json"), Path(os.environ["USERPROFILE"]) / ".codex" / "config.toml"]
    before = [sha(path.read_bytes()) for path in protected]
    output.mkdir(parents=True, exist_ok=False)
    runtime = output / "runtime"
    runtime.mkdir()
    data = runtime / "data"
    data.mkdir()
    copied_executable = runtime / "octopus-roam.exe"
    write_new(copied_executable, binary)
    port = free_port()
    config = data / "config.json"
    write_new(config, json.dumps({
        "server": {"host": "127.0.0.1", "port": port},
        "database": {"type": "sqlite", "path": str(data / "data.db")},
        "log": {"level": "info"},
        "codex_catalog": {"enabled": False, "use_proxy": False},
    }, indent=2) + "\n")
    report = {
        "schema": 1, "started_utc": utc(), "finished_utc": None,
        "script_sha256": sha(Path(__file__).read_bytes()), "binary_sha256": sha(binary),
        "role": "Windows native C01 development runtime, uncommitted working tree",
        "snapshot_inputs": {version: {"sha256": sha(item["raw"]), "bytes": len(item["raw"]),
            "tag": "rust-v" + version, "url": "https://raw.githubusercontent.com/openai/codex/rust-v" + version + "/codex-rs/models-manager/models.json"}
            for version, item in references.items()},
        "checks": [], "catalog_responses": [], "inference_requests": [], "source_logs": [],
        "processes": [], "proxy_connections": [], "max_real_inference_requests": 4,
        "outcome": "not-completed",
    }
    print("READY_FOR_TEST_GATEWAY_KEY_STDIN", flush=True)
    gateway_key = (getpass.getpass("") if sys.stdin.isatty() else sys.stdin.readline()).strip()
    if not gateway_key or len(gateway_key) > 1024:
        raise SystemExit("Missing stdin credential")
    bridge_key = "synthetic-" + secrets.token_urlsafe(32)
    lock = threading.Lock()

    class InferenceBridge(QuietHandler):
        def do_POST(self):
            if self.path != "/v1/responses" or self.headers.get("Authorization") != "Bearer " + bridge_key:
                self.empty(403)
                return
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 < size <= MAX_BODY:
                self.empty(413)
                return
            body = self.rfile.read(size)
            try:
                payload = json.loads(body)
            except ValueError:
                self.empty(400)
                return
            if payload.get("model") not in {MAIN, LUNA}:
                self.empty(403)
                return
            row = {"model": payload["model"], "stream": payload.get("stream", False),
                   "started_utc": utc(), "status": None}
            with lock:
                if len(report["inference_requests"]) >= 4:
                    self.empty(429)
                    return
                report["inference_requests"].append(row)
            connection = http.client.HTTPConnection("localhost", 8080, timeout=90)
            start = time.monotonic()
            sent = False
            try:
                headers = {name: self.headers[name] for name in
                    ("Content-Type", "Accept", "User-Agent", "OpenAI-Beta", "Originator", "Version")
                    if self.headers.get(name)}
                headers["Authorization"] = "Bearer " + gateway_key
                connection.request("POST", "/v1/responses", body, headers)
                response = connection.getresponse()
                row["status"] = response.status
                self.send_response(response.status)
                self.send_header("Content-Type", response.getheader("Content-Type", "application/json"))
                self.send_header("Connection", "close")
                self.end_headers()
                sent = True
                self.close_connection = True
                count = 0
                while chunk := response.read1(32768):
                    count += len(chunk)
                    if count > 8 * MAX_BODY:
                        raise RuntimeError("Inference observation limit")
                    self.wfile.write(chunk)
                    self.wfile.flush()
                row["bytes"] = count
            except Exception as error:
                row["exception_type"] = type(error).__name__
                self.close_connection = True
                if not sent:
                    try:
                        self.empty(502)
                    except OSError:
                        pass
            finally:
                connection.close()
                row["elapsed_ms"] = round((time.monotonic() - start) * 1000)

    class RawTunnel(QuietHandler):
        def do_CONNECT(self):
            if self.path != "raw.githubusercontent.com:443":
                self.empty(403)
                return
            report["proxy_connections"].append({"target": self.path, "started_utc": utc()})
            remote = None
            try:
                remote = socket.create_connection(("raw.githubusercontent.com", 443), timeout=5)
                self.send_response(200, "Connection established")
                self.end_headers()
                self.wfile.flush()
                sockets = [self.connection, remote]
                deadline = time.monotonic() + 30
                while time.monotonic() < deadline:
                    ready, _, _ = select.select(sockets, [], [], 0.5)
                    for incoming in ready:
                        block = incoming.recv(32768)
                        if not block:
                            return
                        (remote if incoming is self.connection else self.connection).sendall(block)
            except OSError:
                pass
            finally:
                self.close_connection = True
                if remote is not None:
                    remote.close()

    bridge, bridge_thread = start_server(InferenceBridge)
    tunnel, tunnel_thread = start_server(RawTunnel)
    process = None
    process_log = None
    cookie = None
    phase = None
    base_environment = {name: value for name, value in os.environ.items() if not name.upper().startswith("OCTOPUS_ROAM_")}
    base_environment.update({"OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE": "1", "NO_COLOR": "1"})

    def check(name, condition):
        report["checks"].append({"name": name, "passed": bool(condition)})
        print(json.dumps({"check": name, "passed": bool(condition)}), flush=True)
        if not condition:
            raise AssertionError(name)

    def start(name, enabled=None, proxy=None):
        nonlocal process, process_log, cookie, phase
        phase = name
        try:
            previous_created = json.loads((runtime / ".octopus-roam-process.json").read_bytes()).get("created")
        except (OSError, ValueError):
            previous_created = None
        environment = dict(base_environment)
        if enabled is not None:
            environment["OCTOPUS_ROAM_CODEX_CATALOG_ENABLED"] = str(enabled).lower()
        if proxy is not None:
            environment["OCTOPUS_ROAM_CODEX_CATALOG_USE_PROXY"] = str(proxy).lower()
        process_log = (output / ("runtime-" + name + ".log")).open("xb")
        process = subprocess.Popen([str(copied_executable), "start", "--config", str(config)], cwd=runtime,
            env=environment, stdin=subprocess.DEVNULL, stdout=process_log, stderr=subprocess.STDOUT,
            creationflags=subprocess.CREATE_NO_WINDOW)
        report["processes"].append({"phase": name, "enabled_env": enabled, "proxy_env": proxy, "normal_exit": False})
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("Owned native runtime exited before readiness")
            try:
                state = json.loads((runtime / ".octopus-roam-process.json").read_bytes())
                if state["pid"] == process.pid and state["ready"] and state.get("created") != previous_created:
                    break
            except (OSError, ValueError, KeyError):
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError("Owned native runtime did not become ready")
        status, headers, body = request(port, "POST", "/api/v1/user/login",
                                       {"username": "admin", "password": "admin", "expire": 60})
        check(name + "-login", status == 200)
        cookies = SimpleCookie()
        cookies.load(next(value for key, value in headers.items() if key.lower() == "set-cookie"))
        cookie = "auth=" + cookies["auth"].value

    def admin(method, path, body=None):
        status, _, raw = request(port, method, path, body, {"Cookie": cookie})
        if status != 200:
            raise RuntimeError("Isolated management API failed: " + method + " " + path + " " + str(status))
        return json.loads(raw).get("data")

    def stop():
        nonlocal process, process_log, cookie
        if process is None:
            return
        if process.poll() is None:
            result = subprocess.run([str(copied_executable), "desktop", "request-stop", "--runtime", str(runtime)],
                cwd=runtime, env=base_environment, capture_output=True, timeout=20, creationflags=subprocess.CREATE_NO_WINDOW)
            report["processes"][-1]["stop_command_exit"] = result.returncode
            if result.returncode != 0:
                raise RuntimeError("Owned runtime normal stop request failed")
            process.wait(timeout=20)
        report["processes"][-1]["exit_code"] = process.returncode
        report["processes"][-1]["normal_exit"] = process.returncode == 0
        process_log.close()
        process_log = None
        process = None
        cookie = None
        check(phase + "-normal-stop", report["processes"][-1]["normal_exit"])

    def new_key(name, allowed=None, enabled=True, expire=0):
        return admin("POST", "/api/v1/apikey/create", {"name": name,
            "api_key": "synthetic-" + secrets.token_urlsafe(24), "enabled": enabled,
            "expire_at": expire, "supported_models": allowed or []})

    def models(key, anthropic=False):
        headers = {"x-api-key" if anthropic else "Authorization": key["api_key"] if anthropic else "Bearer " + key["api_key"]}
        status, _, raw = request(port, "GET", "/v1/models", headers=headers)
        value = json.loads(raw)
        check(phase + "-existing-model-list-" + str(anthropic), status == 200)
        return [model["id"] for model in value["data"]]

    def catalog(label, version, key, expected=200, allowed=None):
        headers = {"X-Client-Private": "synthetic-do-not-forward", "Cookie": "synthetic=do-not-forward"}
        if key is not None:
            headers["Authorization"] = "Bearer " + key["api_key"]
        started = time.monotonic()
        status, response_headers, raw = request(port, "GET", "/codex/models?client_version=" + version, headers=headers)
        row = {"label": label, "phase": phase, "version": version, "status": status,
               "elapsed_ms": round((time.monotonic() - started) * 1000)}
        report["catalog_responses"].append(row)
        check(label + "-status", status == expected)
        if status != 200:
            row["error_code"] = json.loads(raw).get("error", {}).get("code")
            return None
        reference = references[version]
        value = json.loads(raw)
        expected_names = [item["slug"] for item in reference["value"]["models"] if item["slug"] in (allowed if allowed is not None else GROUPS)]
        names = [item["slug"] for item in value["models"]]
        check(label + "-permissions", names == expected_names)
        expected_value = dict(reference["value"])
        expected_value["models"] = [item for item in reference["value"]["models"] if item["slug"] in expected_names]
        check(label + "-metadata-values", value == expected_value)
        check(label + "-model-original-bytes", all(reference["entries"][name] in raw for name in names))
        normalized = {name.lower(): value for name, value in response_headers.items()}
        check(label + "-downstream-headers", normalized.get("cache-control") == "private, no-store"
              and normalized.get("content-type") == "application/json" and "etag" not in normalized and len(raw) <= MAX_BODY)
        write_new(output / (label + ".json"), raw)
        row.update({"sha256": sha(raw), "bytes": len(raw), "models": names, "raw_source_sha256": sha(reference["raw"])})
        return value

    def infer(key, stream, label):
        payload = {"model": MAIN, "input": "Reply with a short confirmation for this connectivity check.",
                   "stream": stream, "reasoning": {"effort": "low"}}
        status, _, raw = request(port, "POST", "/v1/responses", payload,
            {"Authorization": "Bearer " + key["api_key"], "Accept": "text/event-stream" if stream else "application/json"}, timeout=100)
        check(label + "-status", status == 200)
        if stream:
            events = []
            for line in raw.splitlines():
                if line.startswith(b"data:"):
                    try:
                        events.append(json.loads(line[5:].strip()))
                    except ValueError:
                        pass
            check(label + "-completed", any(event.get("type") == "response.completed" for event in events))
        else:
            check(label + "-completed", json.loads(raw).get("status") == "completed")

    try:
        start("default")
        channel = admin("POST", "/api/v1/channel/create", {
            "name": "c01-isolated-inference", "base_url": "http://127.0.0.1:" + str(bridge.server_port),
            "enabled": True, "allowed_protocols": 4, "follow_global_auto_update": False,
            "keys": [{"name": "synthetic-bridge", "key": bridge_key, "enabled": True}],
            "models": GROUPS, "grants": [{"model_name": name, "key_name": "synthetic-bridge", "protocols": 4} for name in GROUPS],
        })
        grants = {item["model_name"]: item["id"] for item in admin("GET", "/api/v1/channel/grants")
                  if item["channel_id"] == channel["id"]}

        def make_group(name):
            value = admin("POST", "/api/v1/group/create", {
                "name": name, "mode": "manual", "model_match_mode": "exact",
                "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 0,
                    "member_non_stream_response_timeout_seconds": 90, "member_stream_first_event_timeout_seconds": 90,
                    "member_cooldown_seconds": 1, "member_affinity_seconds": 0,
                    "retry_mode": "fixed", "retry_initial_interval_seconds": 1, "retry_max_interval_seconds": 1,
                    "manual_max_attempts": 1},
                "items": [{"channel_grant_id": grants[name]}]})
            admin("POST", "/api/v1/group/update/" + str(value["id"]), {"active_item_id": value["items"][0]["id"]})
            return value

        groups = {name: make_group(name) for name in GROUPS}
        a = new_key("c01-full")
        b = new_key("c01-main-only", [MAIN])
        disabled = new_key("c01-disabled")
        disabled["enabled"] = False
        admin("POST", "/api/v1/apikey/update", disabled)
        expired = new_key("c01-expired", expire=int(time.time()) - 60)
        catalog("disabled-default", "0.169.0", a, expected=503)
        check("default-original-models", set(models(a)) == set(GROUPS))
        infer(a, False, "default-normal-inference")
        stop()

        start("direct", enabled=True, proxy=False)
        status, _, stats_raw = request(port, "GET", "/api/v1/apikey/stats", headers={"Authorization": "Bearer " + a["api_key"]})
        check("stats-before-status", status == 200)
        stats_before = json.loads(stats_raw)["data"]["stats"]
        for version in references:
            catalog("direct-" + version, version, a)
        catalog("cache-hit", "0.169.0", a)
        catalog("key-b", "0.169.0", b, allowed=[MAIN])
        for label, credential in [("no-key", None), ("invalid-key", {"api_key": "synthetic-invalid"}),
                                  ("disabled-key", disabled), ("expired-key", expired)]:
            catalog(label, "0.169.0", credential, expected=401)
        for index, version in enumerate(["", "0.169", "01.2.3", "0.169.0&client_version=0.168.0"]):
            catalog("invalid-version-" + str(index), version, a, expected=400)
        catalog("missing-version", "0.0.0", a, expected=404)
        a["supported_models"] = [LUNA]
        admin("POST", "/api/v1/apikey/update", a)
        catalog("permission-tightened", "0.169.0", a, allowed=[LUNA])
        admin("DELETE", "/api/v1/group/delete/" + str(groups[LUNA]["id"]))
        catalog("group-deleted", "0.169.0", a, allowed=[])
        groups[LUNA] = make_group(LUNA)
        catalog("group-restored", "0.169.0", a, allowed=[LUNA])
        status, _, stats_raw = request(port, "GET", "/api/v1/apikey/stats", headers={"Authorization": "Bearer " + a["api_key"]})
        check("catalog-not-inference-accounting", status == 200 and json.loads(stats_raw)["data"]["stats"] == stats_before)
        count_before = len(report["inference_requests"])
        status, _, rejected = request(port, "POST", "/v1/responses", {"model": MAIN, "input": "permission test", "stream": False},
                                      {"Authorization": "Bearer " + a["api_key"]})
        check("inference-permission-independent", status == 400
              and json.loads(rejected).get("error", {}).get("message") == "model not supported by this api key"
              and len(report["inference_requests"]) == count_before)
        a["supported_models"] = []
        admin("POST", "/api/v1/apikey/update", a)
        check("original-openai-list", set(models(a)) == set(GROUPS))
        check("original-anthropic-list", models(b, anthropic=True) == [MAIN])
        stop()

        start("proxy", enabled=True, proxy=True)
        catalog("proxy-missing", "0.169.0", a, expected=503)
        admin("POST", "/api/v1/setting/set", {"key": "proxy_url", "value": "http://127.0.0.1:" + str(tunnel.server_port)})
        catalog("proxy-enabled", "0.169.0", a)
        check("proxy-connect-used", bool(report["proxy_connections"]))
        admin("POST", "/api/v1/setting/set", {"key": "proxy_url", "value": ""})
        catalog("proxy-removed-cached", "0.169.0", a, expected=503)
        infer(a, True, "proxy-unavailable-stream-inference")
        stop()

        start("restart", enabled=True, proxy=False)
        catalog("restart-refetch", "0.169.0", a)
        check("configuration-file-preserved", json.loads(config.read_bytes())["codex_catalog"] == {"enabled": False, "use_proxy": False})
        stop()

        for log in output.glob("runtime-*.log"):
            for line in log.read_text(encoding="utf-8", errors="replace").splitlines():
                if "Codex catalog" not in line or "sha256=" not in line:
                    continue
                def field(name):
                    match = re.search(r"\b" + re.escape(name) + r"=(\S+)", line)
                    return match[1].strip('"') if match else None
                row = {"phase": log.stem.removeprefix("runtime-"), "version": field("client_version"),
                       "tag": field("tag"), "url": field("source"), "sha256": field("sha256"),
                       "cache": field("cache"), "model_count": field("models"), "visible_count": field("visible_models")}
                report["source_logs"].append(row)
        for version, reference in references.items():
            matching = [row for row in report["source_logs"] if row["phase"] == "direct" and row["version"] == version and row["cache"] == "miss"]
            expected_url = report["snapshot_inputs"][version]["url"]
            check("actual-source-path-" + version, bool(matching) and all(row["url"] == expected_url and row["sha256"] == sha(reference["raw"]) for row in matching))
        check("real-cache-hit-observed", any(row["phase"] == "direct" and row["cache"] == "hit" for row in report["source_logs"]))
        check("restart-cache-cleared", any(row["phase"] == "restart" and row["cache"] == "miss" for row in report["source_logs"]))
        check("proxy-source-identity", any(row["phase"] == "proxy" and row["sha256"] == report["snapshot_inputs"]["0.169.0"]["sha256"] for row in report["source_logs"]))
        report["outcome"] = "ok"
    except Exception as error:
        report["outcome"] = "failed"
        report["exception_type"] = type(error).__name__
        # Harness exceptions have fixed operation names; no upstream bodies are included.
        report["failed_check"] = next((row["name"] for row in reversed(report["checks"]) if not row["passed"]), None)
    finally:
        try:
            stop()
        except Exception as error:
            report["cleanup_exception_type"] = type(error).__name__
            report["outcome"] = "failed"
        bridge.shutdown()
        tunnel.shutdown()
        bridge.server_close()
        tunnel.server_close()
        bridge_thread.join(timeout=5)
        tunnel_thread.join(timeout=5)
        gateway_key = ""
        report["protected_files_unchanged"] = before == [sha(path.read_bytes()) for path in protected]
        report["snapshot_files_unchanged"] = all(sha(item["path"].read_bytes()) == sha(item["raw"]) for item in references.values())
        report["binary_unchanged"] = sha(copied_executable.read_bytes()) == args.expected_binary_sha256
        report["finished_utc"] = utc()
        report["all_owned_processes_stopped"] = process is None
        report["server_error_types"] = bridge.failures + tunnel.failures
        if not all([report["protected_files_unchanged"], report["snapshot_files_unchanged"], report["binary_unchanged"], report["all_owned_processes_stopped"]]):
            report["outcome"] = "failed"
        write_new(output / "result.json", json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"outcome": report["outcome"], "checks": len(report["checks"]),
                      "failed_check": report.get("failed_check"), "exception_type": report.get("exception_type"),
                      "all_owned_processes_stopped": report["all_owned_processes_stopped"]}, ensure_ascii=False), flush=True)
    return 0 if report["outcome"] == "ok" else 1


if __name__ == "__main__":
    sys.exit(main())
