# Суточная сводка kb-сервера (Vercel)

Раз в сутки (06:00 UTC = 09:00 МСК, на Hobby ±59 мин) бот @dvkbasebot присылает владельцу в
Telegram сводку: ответ `/healthz` в момент крона, аптайм за 24 ч и 7 дней, средний отклик и сбои
за сутки по монитору UptimeRobot «kb-сервер (Yandex Cloud) /healthz».

Почему Vercel: из Yandex Cloud Functions `api.telegram.org` недоступен (замер 06.11.2026), поэтому
функция `kb-healthcheck` в облаке стоит на паузе. Это отдельный проект Vercel из этой папки, без
связи с git: выкатка руками, пуши в kb-engine его не пересобирают. Зависимостей нет.

## Выкатка

```sh
cd deploy/vercel
npx vercel@65.2.0 link --project kb-digest   # первый раз: создать проект
npx vercel@65.2.0 deploy --prod
```

Env (Production), значения в терминал не выводятся:

| Имя | Что |
|---|---|
| `KB_HEALTH_URL` | `https://<контейнер>/healthz` |
| `UPTIMEROBOT_API_KEY` | monitor-specific ключ монитора kb-сервера (только чтение одного монитора) |
| `TG_TOKEN` | токен @dvkbasebot, тот же, что в Lockbox `kb-alert-bot` |
| `TG_CHAT` | чат владельца |
| `CRON_SECRET` | Vercel шлет его кроном в `Authorization: Bearer …` |

Проверка без ожидания крона: `curl -H "Authorization: Bearer $CRON_SECRET" https://<проект>.vercel.app/api/digest`
отвечает `{"ok":true,...}` и присылает сводку. 502 с `uptimerobot` — сводка ушла, но без аптайма.

Тесты: `npm test` (встроенный раннер Node).
