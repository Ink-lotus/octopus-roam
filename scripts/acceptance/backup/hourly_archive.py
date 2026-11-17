"""Synthetic real-artifact SQLite upgrade evidence; never used on production."""
import base64
import hashlib
import json
import sqlite3
import struct

COLUMNS = ("hour", "date", "input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed")


def compact(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def seed_legacy_hours(path):
    # The caller supplies its stopped, owned real-v0.1.2 runtime's database.
    with sqlite3.connect(path) as conn:
        ddl = conn.execute("SELECT sql FROM sqlite_schema WHERE name='stats_hourlies'").fetchone()[0]
        if "AUTOINCREMENT" not in ddl.upper():
            raise AssertionError("legacy hourly table lacks original autoincrement")
        conn.execute("INSERT INTO stats_hourlies (hour,date,input_token,output_token,input_cost,output_cost,wait_time,request_success,request_failed) VALUES (23,'20261117',0,0,0,0,0,0,0) ON CONFLICT(hour) DO NOTHING")
        for i in range(1, 33):
            # Emulate the old ORM's omission of hour=0, independently of the
            # Go regression that executes the historical model/upsert itself.
            conn.execute("INSERT INTO stats_hourlies (date,input_token,output_token,input_cost,output_cost,wait_time,request_success,request_failed) VALUES ('20261117',?,0,0.125,0,0,0,0)", (i * 3,))


def expected_archives(path):
    with sqlite3.connect(path.resolve().as_uri()+"?mode=ro", uri=True) as conn:
        conn.execute("PRAGMA query_only=ON")
        query = "SELECT " + ",".join(COLUMNS) + "," + ",".join("typeof("+c+")" for c in COLUMNS) + " FROM stats_hourlies WHERE hour >= 24 ORDER BY hour"
        result = []
        for row in conn.execute(query):
            cells = []
            for column, value, kind in zip(COLUMNS, row[:9], row[9:]):
                if kind == "null": encoded = ""
                elif kind == "integer": encoded = str(value)
                elif kind == "real": encoded = struct.pack(">d", value).hex()
                elif kind == "text": encoded = base64.b64encode(value.encode()).decode()
                elif kind == "blob": encoded = base64.b64encode(value).decode()
                else: raise AssertionError("unexpected storage class")
                cells.append({"column": column, "type": kind, "value": encoded})
            payload = compact(cells)
            identity = hashlib.sha256(compact([1, "sqlite-migration-18", row[0], payload]).encode()).hexdigest()
            result.append((identity, 1, "sqlite-migration-18", row[0], payload))
        return sorted(result)


def verify_archives(path, expected):
    with sqlite3.connect(path.resolve().as_uri()+"?mode=ro", uri=True) as conn:
        conn.execute("PRAGMA query_only=ON")
        actual = list(conn.execute("SELECT id,format,source,original_hour,payload FROM stats_hourly_archives ORDER BY id"))
        remaining = conn.execute("SELECT COUNT(*) FROM stats_hourlies WHERE hour >= 24").fetchone()[0]
        if remaining != 0 or actual != expected:
            raise AssertionError("legacy hourly archive differs from original SQLite values")
