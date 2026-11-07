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
