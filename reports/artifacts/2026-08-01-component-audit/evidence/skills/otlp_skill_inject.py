#!/usr/bin/env python3
"""Hand-rolled OTLP/HTTP protobuf injector for the 2026-08-01 skills audit.

Emits an ExportLogsServiceRequest shaped exactly like Claude Code's own OTel
exporter does for a `skill_activated` log record, per
internal/claudeadapter/otel.go (resource service.name="claude-code",
per-record attribute "event.name", native attributes "skill.name",
"session.id", "invocation_trigger") and posts it to the appliance's OTLP HTTP
receiver. No third-party protobuf runtime is available on this VM, so the
wire format is encoded directly.

Usage:
  python3 otlp_skill_inject.py <endpoint> <bearer> <session_id> <skill> [<skill> ...]
"""

import struct
import sys
import time
import urllib.request

# ---------------------------------------------------------------- wire codec


def varint(value: int) -> bytes:
    out = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            out.append(byte | 0x80)
        else:
            out.append(byte)
            return bytes(out)


def tag(field: int, wire: int) -> bytes:
    return varint((field << 3) | wire)


def ld(field: int, payload: bytes) -> bytes:
    """length-delimited field (wire type 2)"""
    return tag(field, 2) + varint(len(payload)) + payload


def fixed64(field: int, value: int) -> bytes:
    return tag(field, 1) + struct.pack("<Q", value)


# ------------------------------------------------------------- otlp messages


def any_string(value: str) -> bytes:
    # AnyValue { string_value = 1 }
    return ld(1, value.encode())


def key_value(key: str, value: str) -> bytes:
    # KeyValue { key = 1, value = 2 }
    return ld(1, key.encode()) + ld(2, any_string(value))


def log_record(attributes, time_ns: int) -> bytes:
    # LogRecord { time_unix_nano = 1, severity_number = 2, severity_text = 3,
    #             body = 5, attributes = 6, observed_time_unix_nano = 11 }
    body = fixed64(1, time_ns)
    body += tag(2, 0) + varint(9)  # SEVERITY_NUMBER_INFO
    body += ld(3, b"INFO")
    for key, value in attributes:
        body += ld(6, key_value(key, value))
    body += fixed64(11, time_ns)
    return body


def build_request(service_name: str, scope_name: str, records) -> bytes:
    # Resource { attributes = 1 }
    resource = ld(1, key_value("service.name", service_name))
    # InstrumentationScope { name = 1 }
    scope = ld(1, scope_name.encode())
    # ScopeLogs { scope = 1, log_records = 2 }
    scope_logs = ld(1, scope)
    for record in records:
        scope_logs += ld(2, record)
    # ResourceLogs { resource = 1, scope_logs = 2 }
    resource_logs = ld(1, resource) + ld(2, scope_logs)
    # ExportLogsServiceRequest { resource_logs = 1 }
    return ld(1, resource_logs)


# --------------------------------------------------------------------- main


def main() -> int:
    if len(sys.argv) < 5:
        print(__doc__)
        return 2
    endpoint, bearer, session_id = sys.argv[1], sys.argv[2], sys.argv[3]
    skills = sys.argv[4:]

    now_ns = int(time.time() * 1_000_000_000)
    records = []
    for index, skill in enumerate(skills):
        records.append(
            log_record(
                [
                    # Real Claude Code wire attributes only. Everything Kansoku
                    # needs (kansoku.event.id / kansoku.event.type /
                    # kansoku.component.kind) is synthesized by
                    # internal/observability/otlp.go itself.
                    ("event.name", "skill_activated"),
                    ("session.id", session_id),
                    ("skill.name", skill),
                    ("invocation_trigger", "user-slash"),
                    ("event.sequence", str(index + 1)),
                ],
                now_ns + index * 1_000_000,
            )
        )

    payload = build_request("claude-code", "com.anthropic.claude_code.events", records)
    request = urllib.request.Request(
        endpoint.rstrip("/") + "/v1/logs",
        data=payload,
        headers={
            "Content-Type": "application/x-protobuf",
            "Authorization": "Bearer " + bearer,
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            print("HTTP", response.status, response.read()[:400])
    except urllib.error.HTTPError as error:
        print("HTTP", error.code, error.read()[:400])
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
