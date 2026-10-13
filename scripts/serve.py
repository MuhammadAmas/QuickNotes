#!/usr/bin/env python3
"""Local static server with caching off, for working on web/ or extension/.

    python3 scripts/serve.py web 5599        → http://localhost:5599/?demo
"""
import functools
import http.server
import sys


class NoCache(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        super().end_headers()

    def log_message(self, *args):
        pass


folder, port = sys.argv[1], int(sys.argv[2]) if len(sys.argv) > 2 else 5599
http.server.ThreadingHTTPServer(("127.0.0.1", port), functools.partial(NoCache, directory=folder)).serve_forever()
