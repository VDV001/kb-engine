// Суточная сводка kb-сервера владельцу в Telegram ботом @dvkbasebot (решение владельца 06.11.2026):
// проверка /healthz в момент крона плюс аптайм и сбои за сутки из UptimeRobot (монитор kb-сервера).
// Живет на Vercel, а не в Yandex Cloud: из Cloud Functions api.telegram.org недоступен (замер 06.11).
// Только стандартный рантайм Node: ставить нечего. Тесты: digest.test.js (npm test).
// ponytail: форма ответа UptimeRobot v2 getMonitors взята из разбора, который у freelance-hub
// (lib/uptime.ts) кормит страницу /status с 30.10; живой ответ монитора kb-сервера в фикстуру не снят.
// Потолок: поле, которого нет, показывается как «нет данных». Апгрейд: снять живой ответ в тест.

const UPTIME_API = "https://api.uptimerobot.com/v2/getMonitors";
const TIMEOUT_MS = 10000;
const DAY_S = 86400;
const MSK_S = 3 * 3600;
const INCIDENTS = 5;

// один GET /healthz -> { code, ms, why }: code null - ответа не было, why - имя исключения, не его текст
export async function probe(url, fetchFn, now = Date.now) {
  url = (url ?? "").trim();
  if (!url.startsWith("https://")) return { code: null, ms: null, why: "нет адреса" };
  const t0 = now();
  try {
    const r = await fetchFn(url, { cache: "no-store", signal: AbortSignal.timeout(TIMEOUT_MS) });
    return { code: r.status, ms: Math.round(now() - t0), why: "" };
  } catch (e) {
    return { code: null, ms: null, why: e instanceof Error ? e.name : "error" };
  }
}

const num = (v) => {
  const n = typeof v === "number" ? v : typeof v === "string" && v.trim() !== "" ? Number(v) : NaN;
  return Number.isFinite(n) ? n : null;
};

// ответ getMonitors -> { paused, d1, d7, avgMs, incidents } или null, если ответ не похож на ответ с монитором
export function parseUptime(body, nowSec) {
  if (!body || body.stat !== "ok" || !Array.isArray(body.monitors) || body.monitors.length === 0) return null;
  const m = body.monitors[0];
  const [d1 = null, d7 = null] = typeof m.custom_uptime_ratio === "string" ? m.custom_uptime_ratio.split("-").map(num) : [];
  // среднее считаем сами по замерам за сутки: average_response_time у API не опора (замер freelance-hub 30.10)
  const ms = (Array.isArray(m.response_times) ? m.response_times : []).map((r) => num(r?.value)).filter((v) => v !== null && v >= 0);
  const incidents = (Array.isArray(m.logs) ? m.logs : [])
    .filter((l) => num(l?.type) === 1 && (num(l?.datetime) ?? 0) >= nowSec - DAY_S) // 1 - сбой; 2 - восстановление
    .map((l) => ({
      start: num(l.datetime),
      durationSec: Math.max(0, num(l.duration) ?? 0),
      reason: typeof l.reason?.detail === "string" ? l.reason.detail.slice(0, 80) : "",
    }))
    .sort((a, b) => b.start - a.start);
  return { paused: num(m.status) === 0, d1, d7, avgMs: ms.length ? ms.reduce((a, v) => a + v, 0) / ms.length : null, incidents };
}

// -> разбор или null: нет ключа, сеть, не-200, отказ API. Причина - в лог, без ключа
export async function fetchUptime(key, nowSec, fetchFn) {
  key = (key ?? "").trim();
  if (!key) return null;
  const form = new URLSearchParams({
    api_key: key,
    format: "json",
    custom_uptime_ratios: "1-7",
    response_times: "1",
    response_times_start_date: String(nowSec - DAY_S),
    response_times_end_date: String(nowSec),
    logs: "1",
    logs_limit: "50",
  });
  try {
    const r = await fetchFn(UPTIME_API, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded", "cache-control": "no-cache" },
      body: form.toString(),
      signal: AbortSignal.timeout(TIMEOUT_MS),
    });
    if (!r.ok) {
      console.error("[digest] uptimerobot http", r.status);
      return null;
    }
    const body = await r.json();
    if (body?.stat !== "ok") console.error("[digest] uptimerobot api", String(body?.error?.type ?? body?.stat ?? "unknown").slice(0, 40));
    return parseUptime(body, nowSec);
  } catch (e) {
    console.error("[digest] uptimerobot", e instanceof Error ? e.name : "error");
    return null;
  }
}

const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const pct = (x) => (x === null ? "нет данных" : (x >= 99.995 ? "100" : x.toFixed(2)) + " %");
const hhmm = (sec) => new Date((sec + MSK_S) * 1000).toISOString().slice(11, 16);
const dur = (sec) => (sec < 60 ? Math.round(sec) + " с" : sec < 3600 ? Math.round(sec / 60) + " мин" : (sec / 3600).toFixed(1) + " ч");

// HTML для Telegram (parse_mode HTML); u = null - UptimeRobot не ответил, так и пишем
export function digestText(p, u) {
  const now = p.code === 200 ? `✓ <code>/healthz</code> 200 за ${p.ms} мс` : `✗ <code>/healthz</code> ${p.code === null ? "не ответил: " + esc(p.why) : "ответил " + p.code}`;
  const lines = ["<b>🩺 kb-сервер, сводка за сутки</b>", "Сейчас: " + now];
  if (!u) return [...lines, "UptimeRobot: нет данных"].join("\n");
  lines.push(`Аптайм: ${pct(u.d1)} за 24 ч · ${pct(u.d7)} за 7 дней`);
  if (u.avgMs !== null) lines.push(`Отклик в среднем: ${Math.round(u.avgMs)} мс`);
  const down = u.incidents.reduce((a, i) => a + i.durationSec, 0);
  lines.push(u.incidents.length ? `Сбоев за сутки: ${u.incidents.length}, всего ${dur(down)}` : "Сбоев за сутки: нет");
  for (const i of u.incidents.slice(0, INCIDENTS)) lines.push(`· ${hhmm(i.start)} МСК, ${dur(i.durationSec)}${i.reason ? " - " + esc(i.reason) : ""}`);
  if (u.paused) lines.push("Монитор UptimeRobot на паузе");
  return lines.join("\n");
}
