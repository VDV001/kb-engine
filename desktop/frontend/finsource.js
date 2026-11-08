// Откуда «Финансы» берут данные — одна чистая функция для строки над витриной
// и для выбора в настройках (ревью #510). Без DOM: проверяется node
// (desktop/finsource_test.go). Правила те же, что у appUpstream в Go: пусто —
// первый включенный kb-сервер; id — сервер из «Источников»; адрес — только
// этого Mac (как loopback.Host: localhost, 127.x.x.x, ::1).
const finIsLocal = (v) => v.includes("://");
function finLoopback(addr) {
  let host;
  try { host = new URL(addr).hostname; } catch { return false; }
  return host === "localhost" || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host) || host === "[::1]";
}
// Опрос serve, пока открыты «Финансы»: витрина грузится в iframe один раз, и
// без него ошибка «не отвечает» жила бы до нового «Сохранить», а выключенный
// serve оставлял бы прежние данные. 502/503 окно отвечает само: сервер не достали
// или его нет; любой другой ответ (даже 401) значит, что на том конце кто-то есть.
const finServeAlive = (code) => code > 0 && code !== 502 && code !== 503;
// Перечитывать витрину только при смене состояния: первый опрос (prev === null)
// — это замер, а не перемена, и без перемены iframe не мигает.
const finServeFlipped = (prev, now) => prev !== null && prev !== now;
// finSource returns {kind, text, server}: kind — local | bad | server | off | gone | first | none.
function finSource(cur, servers, label) {
  if (finIsLocal(cur)) {
    return finLoopback(cur) ? { kind: "local", text: `этот Mac · ${cur}` } : { kind: "bad", text: "адрес в config.json не подходит — выберите заново" };
  }
  if (cur) {
    const s = servers.find((x) => x.id === cur);
    if (!s) return { kind: "gone", text: "сервер, которого нет в «Источниках»" };
    return s.enabled ? { kind: "server", text: `сервер «${label(s)}»`, server: s } : { kind: "off", text: `сервер «${label(s)}» — выключен`, server: s };
  }
  const first = servers.find((x) => x.enabled);
  if (first) return { kind: "first", text: `первый kb-сервер «${label(first)}»`, server: first };
  const any = servers[0];
  return any ? { kind: "off", text: `kb-сервер «${label(any)}» выключен`, server: any } : { kind: "none", text: "kb-сервер не подключен" };
}
