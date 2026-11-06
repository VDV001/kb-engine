"""Проверка kb-сервера по таймеру (решение владельца 06.11): раз в 5 минут
/healthz; не ответил 200 и со второй попытки — письмо через Postbox и
сообщение в Telegram ботом владельца. Только стандартная библиотека: функцию
нечем «ставить».

Окружение: HEALTH_URL, TG_CHAT; TG_TOKEN — из Lockbox (секрет функции), в
код и логи не попадает. TG_API — адрес API Telegram (тест подменяет его).
MAIL_TO, MAIL_FROM — письмо (адрес отправителя на домене, подтвержденном в
Postbox); вход — IAM-токен сервисного аккаунта функции из context, ключей нет.
POSTBOX_API — адрес API Postbox (тест подменяет его). Письмо уходит первым: из
Cloud Functions api.telegram.org недоступен (06.11), а Postbox — свой сервис.

ponytail: состояния нет, поэтому сбой сообщается на каждой проверке, пока
длится, а о восстановлении — никак. Потолок: 12 сообщений в час при долгом
сбое. Путь: хранить последний исход (объект в бакете) и писать только смену.
"""

import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request

ATTEMPTS = 2  # холодный старт контейнера не должен будить человека
TIMEOUT = 15


def check(url):
    """Returns (ok, why): why — код ответа или вид сбоя, без тела."""
    why = ""
    for i in range(ATTEMPTS):
        try:
            with urllib.request.urlopen(url, timeout=TIMEOUT) as r:
                if r.status == 200:
                    return True, "HTTP 200"
                why = f"HTTP {r.status}"
        except urllib.error.HTTPError as e:
            why = f"HTTP {e.code}"
        except Exception as e:  # сеть, TLS, таймаут — назвать вид, не текст
            why = type(e).__name__
        if i + 1 < ATTEMPTS:
            time.sleep(5)
    return False, why


def notify(text):
    """Sends text to the owner's chat: ("", True) или (причина, False). Текст
    исключения не возвращается — в нем бывает адрес с токеном бота; отказ
    Telegram назван его полем description («chat not found» и т. п.), где
    токена нет."""
    api = os.environ.get("TG_API", "https://api.telegram.org").rstrip("/")
    data = urllib.parse.urlencode({"chat_id": os.environ["TG_CHAT"], "text": text}).encode()
    try:
        url = f"{api}/bot{os.environ['TG_TOKEN']}/sendMessage"
        with urllib.request.urlopen(url, data=data, timeout=TIMEOUT) as r:
            return "", r.status == 200
    except urllib.error.HTTPError as e:
        try:
            desc = json.loads(e.read().decode())["description"]
        except Exception:
            desc = ""
        return f"Telegram {e.code} {desc}".strip(), False
    except Exception as e:
        return type(e).__name__, False


def mail(text, context):
    """Sends text by Postbox (API Amazon SES v2): ("", True) или (причина,
    False). Токен — заголовком X-YaCloud-SubjectToken, подпись не нужна; в
    причину попадают код и поле message ответа, токена там нет."""
    token = (getattr(context, "token", None) or {}).get("access_token")
    if not token:
        return "нет IAM-токена: у функции нет сервисного аккаунта", False
    api = os.environ.get("POSTBOX_API", "https://postbox.cloud.yandex.net").rstrip("/")
    subject, _, _ = text.partition("\n")
    msg = {
        "FromEmailAddress": os.environ["MAIL_FROM"],
        "Destination": {"ToAddresses": [os.environ["MAIL_TO"]]},
        "Content": {"Simple": {"Subject": {"Data": subject}, "Body": {"Text": {"Data": text}}}},
    }
    req = urllib.request.Request(
        f"{api}/v2/email/outbound-emails",
        data=json.dumps(msg).encode(),
        headers={"Content-Type": "application/json", "X-YaCloud-SubjectToken": token},
    )
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
            return "", r.status == 200
    except urllib.error.HTTPError as e:
        try:
            desc = str(json.loads(e.read().decode()).get("message", ""))[:120]
        except Exception:
            desc = ""
        return f"Postbox {e.code} {desc}".strip(), False
    except Exception as e:
        return type(e).__name__, False


def handler(event, context):
    url = os.environ["HEALTH_URL"]
    ok, why = check(url)
    sent, tg, ml = False, "", ""
    if not ok:
        text = f"kb-сервер не отвечает: {why}\n{url}"
        if os.environ.get("MAIL_TO"):
            ml, m_ok = mail(text, context)
            sent = sent or m_ok
        if os.environ.get("TG_TOKEN"):
            tg, t_ok = notify(text)
            sent = sent or t_ok
    print(f"healthcheck ok={ok} why={why} notified={sent} mail={ml or '-'} telegram={tg or '-'}")
    return {"ok": ok, "why": why, "notified": sent, "mail": ml, "telegram": tg}
