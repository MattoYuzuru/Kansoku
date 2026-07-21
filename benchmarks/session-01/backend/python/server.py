#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import queue
import signal
import threading
import time
from dataclasses import asdict, dataclass
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


MAX_BODY_BYTES = 1 << 20
MAX_LOG_RECORDS = 10_000
BATCH_SIZE = 64


@dataclass(frozen=True)
class SafeRow:
    received_at: str
    route: str
    record_count: int
    body_bytes: int
    schema_fingerprint: str


class WorkItem:
    def __init__(self, row: SafeRow) -> None:
        self.row = row
        self.done = threading.Event()
        self.error: str | None = None


class BatchSink:
    def __init__(self, path: Path) -> None:
        self.path = path
        self.queue: queue.Queue[WorkItem | None] = queue.Queue(maxsize=1024)
        self.accepted = 0
        self.persisted = 0
        self.last_error_class = ""
        self.lock = threading.Lock()
        self.thread = threading.Thread(target=self._writer, name="batch-writer", daemon=True)
        self.thread.start()

    def submit(self, row: SafeRow) -> bool:
        item = WorkItem(row)
        try:
            self.queue.put(item, timeout=5)
        except queue.Full:
            return False
        with self.lock:
            self.accepted += 1
        if not item.done.wait(timeout=5):
            return False
        return item.error is None

    def _writer(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with self.path.open("a", encoding="utf-8", buffering=64 * 1024) as output:
            stopping = False
            while not stopping:
                first = self.queue.get()
                if first is None:
                    break
                batch = [first]
                deadline = time.monotonic() + 0.025
                while len(batch) < BATCH_SIZE:
                    timeout = deadline - time.monotonic()
                    if timeout <= 0:
                        break
                    try:
                        item = self.queue.get(timeout=timeout)
                    except queue.Empty:
                        break
                    if item is None:
                        stopping = True
                        break
                    batch.append(item)
                error: str | None = None
                try:
                    for item in batch:
                        output.write(json.dumps(asdict(item.row), separators=(",", ":")) + "\n")
                    output.flush()
                    os.fsync(output.fileno())
                except OSError:
                    error = "batch_write_failed"
                    with self.lock:
                        self.last_error_class = error
                else:
                    with self.lock:
                        self.persisted += len(batch)
                for item in batch:
                    item.error = error
                    item.done.set()

    def health(self) -> dict[str, Any]:
        with self.lock:
            return {
                "status": "ok",
                "accepted_batches": self.accepted,
                "persisted_batches": self.persisted,
                "queue_depth": self.queue.qsize(),
                "last_error_class": self.last_error_class,
            }

    def close(self) -> None:
        self.queue.put(None)
        self.thread.join(timeout=10)


def count_records(payload: Any) -> int:
    if not isinstance(payload, dict) or set(payload) != {"resourceLogs"}:
        raise ValueError("unknown top-level schema")
    resources = payload["resourceLogs"]
    if not isinstance(resources, list):
        raise ValueError("resourceLogs must be a list")
    total = 0
    for resource in resources:
        if not isinstance(resource, dict) or set(resource) != {"scopeLogs"}:
            raise ValueError("unknown resourceLogs schema")
        scopes = resource["scopeLogs"]
        if not isinstance(scopes, list):
            raise ValueError("scopeLogs must be a list")
        for scope in scopes:
            if not isinstance(scope, dict) or set(scope) != {"logRecords"}:
                raise ValueError("unknown scopeLogs schema")
            records = scope["logRecords"]
            if not isinstance(records, list):
                raise ValueError("logRecords must be a list")
            for record in records:
                if not isinstance(record, dict) or not set(record).issubset({"timeUnixNano"}):
                    raise ValueError("unknown log record schema")
            total += len(records)
    if not 1 <= total <= MAX_LOG_RECORDS:
        raise ValueError("invalid record count")
    return total


class Handler(BaseHTTPRequestHandler):
    server_version = "KansokuSpike/1"

    @property
    def sink(self) -> BatchSink:
        return self.server.sink  # type: ignore[attr-defined]

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def send_json(self, status: HTTPStatus, body: dict[str, Any]) -> None:
        encoded = json.dumps(body, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_GET(self) -> None:
        if self.path != "/healthz":
            self.send_error(HTTPStatus.NOT_FOUND)
            return
        self.send_json(HTTPStatus.OK, self.sink.health())

    def do_POST(self) -> None:
        if self.path != "/v1/logs":
            self.send_error(HTTPStatus.NOT_FOUND)
            return
        if not self.headers.get("Content-Type", "").startswith("application/json"):
            self.send_error(HTTPStatus.UNSUPPORTED_MEDIA_TYPE, "unsupported_content_type")
            return
        try:
            size = int(self.headers.get("Content-Length", "-1"))
        except ValueError:
            size = -1
        if size < 0 or size > MAX_BODY_BYTES:
            self.send_error(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, "payload_too_large")
            return
        body = self.rfile.read(size)
        try:
            payload = json.loads(body)
            count = count_records(payload)
        except (UnicodeDecodeError, json.JSONDecodeError, ValueError):
            self.send_error(HTTPStatus.BAD_REQUEST, "invalid_or_unknown_schema")
            return
        row = SafeRow(
            received_at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            route="otlp_http_json_logs",
            record_count=count,
            body_bytes=size,
            schema_fingerprint="spike.otlp-json-safe-counts/1",
        )
        if not self.sink.submit(row):
            self.send_error(HTTPStatus.SERVICE_UNAVAILABLE, "durable_batch_failed")
            return
        self.send_json(HTTPStatus.ACCEPTED, {"accepted": True})


def main() -> None:
    sink = BatchSink(Path(os.environ.get("SINK_PATH", "/data/batches.jsonl")))
    port = int(os.environ.get("PORT", "8080"))
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    server.sink = sink  # type: ignore[attr-defined]

    def stop(_signum: int, _frame: object) -> None:
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        server.serve_forever(poll_interval=0.1)
    finally:
        server.server_close()
        sink.close()


if __name__ == "__main__":
    main()
