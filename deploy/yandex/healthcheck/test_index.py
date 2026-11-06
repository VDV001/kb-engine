"""Тесты проверки kb-сервера: локальный HTTP-двойник вместо сервера и Telegram.

Запуск: python3 -m unittest discover -s deploy/yandex/healthcheck
"""

import http.server
import os
import threading
import unittest
from unittest import mock

import index


class _Handler(http.server.BaseHTTPRequestHandler):
    status = 200
    sent = []

    def do_GET(self):  # noqa: N802 — имя задает http.server
        self.send_response(type(self).status)
        self.end_headers()

    def do_POST(self):  # noqa: N802
        n = int(self.headers.get("Content-Length", 0))
        type(self).sent.append((self.path, self.rfile.read(n).decode()))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def log_message(self, *_):
        pass


class HealthcheckTest(unittest.TestCase):
    def setUp(self):
        _Handler.status, _Handler.sent = 200, []
        self.srv = http.server.HTTPServer(("127.0.0.1", 0), _Handler)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{self.srv.server_port}"
        env = {"HEALTH_URL": base + "/healthz", "TG_TOKEN": "123:secret-token", "TG_CHAT": "42", "TG_API": base}
        self.env = mock.patch.dict(os.environ, env)
        self.env.start()

    def tearDown(self):
        self.env.stop()
        self.srv.shutdown()

    def test_up_is_silent(self):
        self.assertEqual(index.handler({}, None)["ok"], True)
        self.assertEqual(_Handler.sent, [])

    def test_down_is_reported_once_per_check(self):
        _Handler.status = 503
        out = index.handler({}, None)
        self.assertFalse(out["ok"])
        self.assertEqual(len(_Handler.sent), 1)
        path, body = _Handler.sent[0]
        self.assertEqual(path, "/bot123:secret-token/sendMessage")
        self.assertIn("chat_id=42", body)
        self.assertIn("503", body)

    def test_telegram_failure_does_not_leak_the_token(self):
        _Handler.status = 503
        os.environ["TG_API"] = "http://127.0.0.1:1"  # закрытый порт
        out = index.handler({}, None)
        self.assertFalse(out["notified"])
        self.assertNotIn("secret-token", repr(out))

    def test_telegram_refusal_is_named(self):
        _Handler.status = 503
        orig = _Handler.do_POST

        def refuse(h):
            body = b'{"ok":false,"error_code":403,"description":"Forbidden: bot can\'t initiate conversation with a user"}'
            h.send_response(403)
            h.send_header("Content-Length", str(len(body)))
            h.end_headers()
            h.wfile.write(body)

        _Handler.do_POST = refuse
        try:
            out = index.handler({}, None)
        finally:
            _Handler.do_POST = orig
        self.assertEqual(out["telegram"], "Telegram 403 Forbidden: bot can't initiate conversation with a user")
        self.assertNotIn("secret-token", repr(out))


if __name__ == "__main__":
    unittest.main()
