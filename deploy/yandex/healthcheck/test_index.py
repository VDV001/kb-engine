"""Тесты проверки kb-сервера: локальный HTTP-двойник вместо сервера и Telegram.

Запуск: python3 -m unittest discover -s deploy/yandex/healthcheck
"""

import http.server
import json
import os
import threading
import unittest
from unittest import mock

import index


class _Handler(http.server.BaseHTTPRequestHandler):
    status = 200
    body = b""  # тело ответа /healthz: «ok» и, при отправке в Monium, «log-dropped: N»
    sent = []
    headers_seen = []

    def do_GET(self):  # noqa: N802 — имя задает http.server
        self.send_response(type(self).status)
        self.end_headers()
        self.wfile.write(type(self).body)

    def do_POST(self):  # noqa: N802
        n = int(self.headers.get("Content-Length", 0))
        type(self).sent.append((self.path, self.rfile.read(n).decode()))
        type(self).headers_seen.append(self.headers)  # без учета регистра, как в HTTP
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def log_message(self, *_):
        pass


class _Context:
    """Контекст Cloud Functions: IAM-токен сервисного аккаунта функции."""

    token = {"access_token": "iam-secret", "expires_in": 3600, "token_type": "Bearer"}


class HealthcheckTest(unittest.TestCase):
    def setUp(self):
        _Handler.status, _Handler.body, _Handler.sent, _Handler.headers_seen = 200, b"", [], []
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

    def test_up_with_lost_log_lines_is_reported(self):
        """Monium недоступен, сервер жив: «ok» с числом потерь не должно проходить молча (#697)."""
        _Handler.body = b"ok\nlog-dropped: 5\n"
        out = index.handler({}, None)
        self.assertTrue(out["ok"])
        self.assertEqual(out.get("dropped"), 5)
        self.assertTrue(out["notified"])
        self.assertEqual(len(_Handler.sent), 1)
        _, body = _Handler.sent[0]
        self.assertIn("Monium", body)
        self.assertIn("5", body)

    def test_up_with_no_lost_lines_is_silent(self):
        _Handler.body = b"ok\nlog-dropped: 0\n"
        out = index.handler({}, None)
        self.assertEqual(out.get("dropped"), 0)
        self.assertEqual(_Handler.sent, [])

    def test_plain_ok_has_no_dropped_field(self):
        _Handler.body = b"ok\n"  # отправка выключена: ответ прежний
        out = index.handler({}, None)
        self.assertIsNone(out.get("dropped", None))
        self.assertEqual(_Handler.sent, [])

    def test_garbage_in_the_dropped_line_is_ignored(self):
        for body in (b"ok\nlog-dropped: abc\n", b"ok\nlog-dropped: -3\n", b"ok\nlog-dropped: 99999999999999999999\n", b"ok\n" + b"x" * 5000):
            _Handler.body, _Handler.sent = body, []
            out = index.handler({}, None)
            self.assertTrue(out["ok"], body[:30])
            self.assertIsNone(out.get("dropped"), body[:30])
            self.assertEqual(_Handler.sent, [], body[:30])

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


class MailTest(HealthcheckTest):
    """Письмо через Postbox (API Amazon SES v2) по IAM-токену функции."""

    def setUp(self):
        super().setUp()
        base = os.environ["TG_API"]
        os.environ.update({"MAIL_TO": "owner@example.com", "MAIL_FROM": "alerts@example.com", "POSTBOX_API": base})

    def test_down_is_mailed_with_the_function_token(self):
        _Handler.status = 503
        out = index.handler({}, _Context())
        self.assertTrue(out["notified"])
        self.assertEqual(out["mail"], "")
        mails = [(p, b, h) for (p, b), h in zip(_Handler.sent, _Handler.headers_seen) if p == "/v2/email/outbound-emails"]
        self.assertEqual(len(mails), 1)
        _, body, hdr = mails[0]
        self.assertEqual(hdr.get("X-YaCloud-SubjectToken"), "iam-secret")
        msg = json.loads(body)
        self.assertEqual(msg["FromEmailAddress"], "alerts@example.com")
        self.assertEqual(msg["Destination"]["ToAddresses"], ["owner@example.com"])
        self.assertIn("503", msg["Content"]["Simple"]["Body"]["Text"]["Data"])
        self.assertIn("kb-сервер", msg["Content"]["Simple"]["Subject"]["Data"])

    def test_mail_goes_first_and_survives_dead_telegram(self):
        _Handler.status = 503
        os.environ["TG_API"] = "http://127.0.0.1:1"  # Telegram недоступен, как из Cloud Functions
        out = index.handler({}, _Context())
        self.assertTrue(out["notified"])
        self.assertEqual(out["mail"], "")
        self.assertNotEqual(out["telegram"], "")

    def test_up_sends_no_mail(self):
        index.handler({}, _Context())
        self.assertEqual(_Handler.sent, [])

    def test_no_token_in_context_is_named_not_crashed(self):
        _Handler.status = 503
        out = index.handler({}, None)
        self.assertEqual(out["mail"], "нет IAM-токена: у функции нет сервисного аккаунта")

    def test_postbox_refusal_does_not_leak_the_token(self):
        _Handler.status = 503
        os.environ["POSTBOX_API"] = "http://127.0.0.1:1"
        out = index.handler({}, _Context())
        self.assertNotEqual(out["mail"], "")
        self.assertNotIn("iam-secret", repr(out))


if __name__ == "__main__":
    unittest.main()
