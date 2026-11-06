// Тесты суточной сводки (digest.js): npm test в deploy/vercel. Ответ UptimeRobot - по форме v2 getMonitors.
import { test } from "node:test";
import assert from "node:assert/strict";
import { digestText, fetchUptime, parseUptime, probe } from "./digest.js";

const NOW = 1762400000; // 06.11.2025 03:33:20 UTC = 06:33:20 МСК
const ok = { code: 200, ms: 312, why: "" };
const monitor = (over = {}) => ({
  stat: "ok",
  monitors: [{ status: 2, custom_uptime_ratio: "100.000-99.981", response_times: [{ datetime: NOW - 60, value: 200 }, { datetime: NOW - 360, value: 283.2 }], logs: [], ...over }],
});

test("probe: код и время ответа", async () => {
  let t = 1000;
  assert.deepEqual(await probe("https://kb.example/healthz", async () => { t += 87; return { status: 200 }; }, () => t), { code: 200, ms: 87, why: "" });
});

test("probe: сбой сети - имя исключения, не текст", async () => {
  const p = await probe("https://kb.example/healthz", async () => { throw Object.assign(new Error("текст с адресом"), { name: "TimeoutError" }); });
  assert.deepEqual(p, { code: null, ms: null, why: "TimeoutError" });
});

test("probe: без https-адреса в сеть не ходит", async () => {
  for (const url of [undefined, "", "  ", "http://kb.example/healthz"]) {
    assert.equal((await probe(url, async () => assert.fail("fetch для " + JSON.stringify(url)))).why, "нет адреса");
  }
});

test("parseUptime: доли, среднее по замерам, сбои только за сутки", () => {
  const logs = [
    { type: 2, datetime: NOW - 3000, duration: 0 }, // восстановление - не сбой
    { type: 1, datetime: NOW - 7200, duration: 45, reason: { detail: "" } },
    { type: 1, datetime: NOW - 3600, duration: "300", reason: { detail: "Connection <Timeout>" } },
    { type: 1, datetime: NOW - 2 * 86400, duration: 9999 }, // старше суток
  ];
  const u = parseUptime(monitor({ logs }), NOW);
  assert.equal(u.d1, 100);
  assert.equal(u.d7, 99.981);
  assert.equal(u.avgMs, 241.6);
  assert.equal(u.paused, false);
  assert.deepEqual(u.incidents.map((i) => i.start), [NOW - 3600, NOW - 7200]); // свежие сверху
  assert.equal(u.incidents[0].durationSec, 300);
});

test("parseUptime: чужой или пустой ответ - null; нет долей - null, не ноль", () => {
  for (const b of [null, {}, { stat: "fail" }, { stat: "ok", monitors: [] }]) assert.equal(parseUptime(b, NOW), null);
  const u = parseUptime(monitor({ custom_uptime_ratio: undefined, response_times: undefined, status: 0 }), NOW);
  assert.deepEqual([u.d1, u.d7, u.avgMs, u.paused], [null, null, null, true]);
});

test("fetchUptime: без ключа в сеть не ходит; ключ уходит в теле, не в адресе", async () => {
  assert.equal(await fetchUptime("  ", NOW, async () => assert.fail("fetch без ключа")), null);
  let seen;
  const u = await fetchUptime("ur123-key", NOW, async (url, init) => { seen = { url, body: init.body }; return { ok: true, json: async () => monitor() }; });
  assert.equal(u.d1, 100);
  assert.doesNotMatch(seen.url, /ur123/);
  assert.match(seen.body, /api_key=ur123-key/);
  assert.match(seen.body, /custom_uptime_ratios=1-7/);
});

test("fetchUptime: не-200 и сеть - null", async () => {
  assert.equal(await fetchUptime("k", NOW, async () => ({ ok: false, status: 401 })), null);
  assert.equal(await fetchUptime("k", NOW, async () => { throw new TypeError("fetch failed"); }), null);
});

test("сводка: все хорошо", () => {
  assert.equal(
    digestText(ok, parseUptime(monitor(), NOW)),
    ["<b>🩺 kb-сервер, сводка за сутки</b>", "Сейчас: ✓ <code>/healthz</code> 200 за 312 мс", "Аптайм: 100 % за 24 ч · 99.98 % за 7 дней", "Отклик в среднем: 242 мс", "Сбоев за сутки: нет"].join("\n"),
  );
});

test("сводка: сбои по Москве, причина экранирована", () => {
  const logs = [
    { type: 1, datetime: NOW - 3600, duration: 300, reason: { detail: "Connection <Timeout>" } },
    { type: 1, datetime: NOW - 7200, duration: 45 },
  ];
  const s = digestText(ok, parseUptime(monitor({ logs, custom_uptime_ratio: "99.45-99.1" }), NOW));
  assert.match(s, /Аптайм: 99\.45 % за 24 ч · 99\.10 % за 7 дней/);
  assert.match(s, /Сбоев за сутки: 2, всего 6 мин/);
  assert.match(s, /· 05:33 МСК, 5 мин - Connection &lt;Timeout&gt;/);
  assert.match(s, /· 04:33 МСК, 45 с$/m);
});

test("сводка: сервер не ответил или ответил не 200", () => {
  const u = parseUptime(monitor(), NOW);
  assert.match(digestText({ code: null, ms: null, why: "TimeoutError" }, u), /Сейчас: ✗ <code>\/healthz<\/code> не ответил: TimeoutError/);
  assert.match(digestText({ code: 503, ms: 40, why: "" }, u), /Сейчас: ✗ <code>\/healthz<\/code> ответил 503/);
});

test("сводка: UptimeRobot не ответил - так и пишем, проверка остается", () => {
  const s = digestText(ok, null);
  assert.match(s, /Сейчас: ✓/);
  assert.match(s, /UptimeRobot: нет данных$/);
  assert.doesNotMatch(s, /Аптайм/);
});

test("сводка: монитор на паузе назван", () => {
  assert.match(digestText(ok, parseUptime(monitor({ status: 0 }), NOW)), /Монитор UptimeRobot на паузе/);
});
