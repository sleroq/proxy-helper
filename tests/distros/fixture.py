#!/usr/bin/env python3
"""Local subscription and HTTP target; failure flag makes every fetch fail."""
import base64
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path


class Handler(BaseHTTPRequestHandler):
    def do_HEAD(self):
        self.send_response(200)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self):
        if self.path == "/subscription":
            if Path("/tmp/fail-source").exists():
                self.send_error(503)
                return
            credentials = base64.b64encode(b"aes-128-gcm:fixture-secret").decode()
            body = f"ss://{credentials}@127.0.0.1:18388#fixture\n".encode()
        else:
            body = b"fixture-ok\n"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


HTTPServer(("127.0.0.1", 18080), Handler).serve_forever()
