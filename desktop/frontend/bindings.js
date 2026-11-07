// Привязки Go и ошибки окна (#505): в одной сборке «Сохранить» в «Чат и
// модель» молчало — ни «Сохранено», ни ошибки, и причину не нашли. Этот файл
// грузится первым (index.html): его перехват видит падение любого следующего
// скрипта. Без DOM: проверяется node (desktop/bindings_test.go).

// Set…, не ответивший за срок, сообщает об этом один раз, но не отменяется и не
// подменяется отказом: запись в Go не отменить, и окно сказало бы «не
// сохранено» о том, что сохранится позже, — с устаревшим экраном, открытой
// во время переиндексации панелью и вставшей очередью (ревью #515). Срок —
// только у Set…: чтение, чат, диктовка и выбор папки законно ждут дольше.
const SLOW_SAVE_MS = 10000;
const slowNoticeCache = new WeakMap();

// withSlowNotice wraps the Wails bindings: onSlow(name) fires once when a Set…
// has not settled in ms; the call's own answer or refusal passes through as is.
function withSlowNotice(app, onSlow, ms = SLOW_SAVE_MS) {
  if (slowNoticeCache.has(app)) return slowNoticeCache.get(app);
  const wrapped = new Proxy(app, {
    get(target, prop) {
      const v = target[prop];
      if (typeof v !== "function" || typeof prop !== "string" || !/^Set[A-Z]/.test(prop)) return v;
      return (...args) => {
        const t = setTimeout(() => onSlow(prop), ms);
        const done = () => clearTimeout(t);
        const p = Promise.resolve(v.apply(target, args));
        p.then(done, done);
        return p;
      };
    },
  });
  slowNoticeCache.set(app, wrapped);
  return wrapped;
}

// Ошибка JS и отказ без catch не глотают нажатие молча. До готовности
// подсказок (toast в app.js) они ждут в очереди: app.js ставит show и
// разбирает ее. Ошибки рантайма Wails (/wails/) и чужих скриптов (без файла)
// — не наша беда и не повод для подсказки при каждом открытии окна. Путь, а
// не origin: у схемы wails:// он может быть «null» (ревью #515).
const windowErrors = { queue: [], show: null };
function reportWindowError(head, text) {
  if (windowErrors.show) windowErrors.show(head, text);
  else windowErrors.queue.push([head, text]);
}
const fromWails = (s) => typeof s === "string" && s.includes("/wails/");
if (typeof addEventListener === "function") {
  addEventListener("error", (e) => {
    if (!e.filename || fromWails(e.filename)) return;
    reportWindowError("Ошибка окна", e.message || "без текста");
  });
  addEventListener("unhandledrejection", (e) => {
    const r = e.reason;
    if (r && fromWails(r.stack)) return;
    reportWindowError("Действие не выполнено", r instanceof Error ? r.message || r.name : r === undefined ? "без текста" : String(r));
  });
}
