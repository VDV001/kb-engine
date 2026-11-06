"""Проверка kb-сервера по таймеру (решение владельца 06.11): раз в 5 минут
/healthz; не ответил 200 и со второй попытки — сообщение в Telegram ботом
владельца. Только стандартная библиотека: функцию нечем «ставить».

Окружение: HEALTH_URL, TG_CHAT; TG_TOKEN — из Lockbox (секрет функции), в
код и логи не попадает. TG_API — адрес API Telegram (тест подменяет его).

ponytail: состояния нет, поэтому сбой сообщается на каждой проверке, пока
длится, а о восстановлении — никак. Потолок: 12 сообщений в час при долгом
сбое. Путь: хранить последний исход (объект в бакете) и писать только смену.
"""

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
    """Sends text to the owner's chat; False on any failure. Текст ошибки не
    возвращается: в нем бывает адрес с токеном бота."""
    api = os.environ.get("TG_API", "https://api.telegram.org").rstrip("/")
    data = urllib.parse.urlencode({"chat_id": os.environ["TG_CHAT"], "text": text}).encode()
    try:
        url = f"{api}/bot{os.environ['TG_TOKEN']}/sendMessage"
        with urllib.request.urlopen(url, data=data, timeout=TIMEOUT) as r:
            return r.status == 200
    except Exception:
        return False


def handler(event, context):
    url = os.environ["HEALTH_URL"]
    ok, why = check(url)
    notified = False
    if not ok:
        notified = notify(f"kb-сервер не отвечает: {why}\n{url}")
    print(f"healthcheck ok={ok} why={why} notified={notified}")
    return {"ok": ok, "why": why, "notified": notified}
