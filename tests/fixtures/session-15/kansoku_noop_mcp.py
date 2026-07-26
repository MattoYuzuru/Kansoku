#!/usr/bin/env python3
"""Bounded stdio MCP canary. JSON-RPC metadata only; no network/filesystem access."""
import json
import sys
import time

PROTOCOL = "2025-11-25"

def send(value):
    sys.stdout.write(json.dumps(value, separators=(",", ":")) + "\n")
    sys.stdout.flush()

def result(request_id, value):
    send({"jsonrpc": "2.0", "id": request_id, "result": value})

for line in sys.stdin:
    try:
        request = json.loads(line)
    except json.JSONDecodeError:
        continue
    method = request.get("method")
    request_id = request.get("id")
    if method == "initialize":
        result(request_id, {
            "protocolVersion": PROTOCOL,
            "capabilities": {"tools": {"listChanged": True}},
            "serverInfo": {"name": "kansoku-noop-mcp", "version": "1.0.0"},
        })
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        cursor = request.get("params", {}).get("cursor")
        if not cursor:
            result(request_id, {
                "tools": [{
                    "name": "nothing.success",
                    "description": "Deterministic metadata-only no-op.",
                    "inputSchema": {"type": "object", "additionalProperties": True},
                }],
                "nextCursor": "page-2",
            })
        else:
            result(request_id, {
                "tools": [{
                    "name": "nothing.error",
                    "description": "Deterministic protocol-level execution error.",
                    "inputSchema": {"type": "object", "additionalProperties": True},
                }]
            })
            send({"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
    elif method == "tools/call":
        name = request.get("params", {}).get("name")
        if name == "nothing.success":
            result(request_id, {"content": [{"type": "text", "text": "kansoku-noop-mcp: observed"}], "isError": False})
        elif name == "nothing.error":
            result(request_id, {"content": [{"type": "text", "text": "deterministic canary error"}], "isError": True})
        elif name == "nothing.delay":
            time.sleep(2)
            result(request_id, {"content": [], "isError": False})
        else:
            send({"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": "method unavailable"}})
    elif request_id is not None:
        send({"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": "method unavailable"}})
