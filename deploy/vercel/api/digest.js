// Крон Vercel (vercel.json, 06:00 UTC = 09:00 МСК, на Hobby ±59 мин) зовет с заголовком Authorization: Bearer CRON_SECRET.
// Env (Production): KB_HEALTH_URL (https://…/healthz), UPTIMEROBOT_API_KEY (monitor-specific, только монитор kb-сервера),
// TG_TOKEN (@dvkbasebot), TG_CHAT, CRON_SECRET. Токен не печатается: в лог идет имя исключения, а не текст.
import { timingSafeEqual } from "node:crypto";
import { digestText, fetchUptime, probe } from "../lib/digest.js";

export const maxDuration = 30;

// и сырое значение, и обрезанное (перевод строки в секрете); пустой секрет - всегда отказ
function authorized(header) {
  const raw = process.env.CRON_SECRET ?? "";
  const got = Buffer.from(header ?? "");
  return [raw, raw.trim()].some((sec) => {
    if (!sec.trim()) return false;
    const want = Buffer.from("Bearer " + sec);
    return got.length === want.length && timingSafeEqual(got, want);
  });
}

export async function GET(request) {
  if (!authorized(request.headers.get("authorization"))) return Response.json({ error: "auth" }, { status: 401 });
  const bot = (process.env.TG_TOKEN ?? "").trim(), chat = (process.env.TG_CHAT ?? "").trim();
  if (!bot || !chat) return Response.json({ error: "telegram_off" }, { status: 503 });

  const now = Math.floor(Date.now() / 1000);
  const [p, u] = await Promise.all([probe(process.env.KB_HEALTH_URL, fetch), fetchUptime(process.env.UPTIMEROBOT_API_KEY, now, fetch)]);
  const r = await fetch(`https://api.telegram.org/bot${bot}/sendMessage`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ chat_id: chat, text: digestText(p, u), parse_mode: "HTML", disable_web_page_preview: true }),
    signal: AbortSignal.timeout(10000),
  }).catch((e) => {
    console.error("[digest] telegram", e instanceof Error ? e.name : "error");
    return null;
  });
  // сводка ушла без UptimeRobot - запуск красный; лежащий kb-сервер - не сбой крона, о нем и сводка
  if (!r || !r.ok) return Response.json({ error: "telegram", status: r?.status ?? "net" }, { status: 502 });
  if (!u) return Response.json({ error: "uptimerobot", code: p.code, sent: true }, { status: 502 });
  return Response.json({ ok: true, code: p.code });
}
