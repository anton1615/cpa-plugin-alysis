"""Local stand-in for the Alysis gateway.

Records every upstream request (method + path + bearer key) so the plugin's
per-model protocol routing can be proven from observed traffic instead of
inferred, and answers with the native envelope each endpoint should return.

Run: python scripts/upstream_stub.py [port] [logfile]
"""
from __future__ import annotations

import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8899
LOG = sys.argv[2] if len(sys.argv) > 2 else "upstream_stub.log"

CHAT_BODY = {
    "id": "chatcmpl-stub",
    "object": "chat.completion",
    "created": 1,
    "model": "stub",
    "choices": [
        {
            "index": 0,
            "message": {"role": "assistant", "content": "OK"},
            "finish_reason": "stop",
        }
    ],
    "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
}

RESPONSES_BODY = {
    "id": "resp_stub",
    "object": "response",
    "created_at": 1,
    "status": "completed",
    "model": "stub",
    "output": [
        {
            "type": "message",
            "id": "msg_stub",
            "role": "assistant",
            "status": "completed",
            "content": [{"type": "output_text", "text": "OK", "annotations": []}],
        }
    ],
    "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
}

MESSAGES_BODY = {
    "id": "msg_stub",
    "type": "message",
    "role": "assistant",
    "model": "stub",
    "content": [{"type": "text", "text": "OK"}],
    "stop_reason": "end_turn",
    "stop_sequence": None,
    "usage": {"input_tokens": 1, "output_tokens": 1},
}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _record(self, path: str) -> None:
        key = self.headers.get("Authorization", "")
        with open(LOG, "a", encoding="utf-8") as handle:
            handle.write(f"{self.command} {path} {key}\n")
            handle.flush()

    def _send(self, status: int, payload: dict) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self) -> None:  # noqa: N802 - stdlib naming
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        path = self.path.split("?", 1)[0]
        self._record(path)

        if self.headers.get("Authorization", "") == "Bearer slk_bogus":
            self._send(
                401,
                {"error": {"type": "invalid_api_key", "message": "Invalid or revoked Alysis key."}},
            )
            return

        if path.endswith("/chat/completions"):
            self._send(200, CHAT_BODY)
        elif path.endswith("/responses"):
            self._send(200, RESPONSES_BODY)
        elif path.endswith("/messages"):
            self._send(200, MESSAGES_BODY)
        else:
            self._send(404, {"error": {"message": f"unexpected path {path}"}})

    def log_message(self, *args) -> None:  # silence stderr chatter
        pass


if __name__ == "__main__":
    open(LOG, "w", encoding="utf-8").close()
    print(f"stub listening on 127.0.0.1:{PORT}, logging to {LOG}", flush=True)
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()