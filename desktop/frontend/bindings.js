// Сохранение настроек отвечает за срок или отказывает словами (#505): в одной
// сборке «Сохранить» в «Чат и модель» молчало — ни «Сохранено», ни ошибки, и
// причину не нашли. Все вызовы Set… уже показывают отказ (catch → showError или
// заметка), не хватало отказа у вызова, который не вернулся. Срок — только у
// Set…: чтение, чат, диктовка и выбор папки законно ждут дольше. Без DOM:
// проверяется node (desktop/savedeadline_test.go).
const SAVE_DEADLINE_MS = 10000;
const SAVE_HUNG_TEXT =
  "окно не ответило за 10 с. Если macOS спрашивает доступ к Связке ключей — ответьте и сохраните снова; " +
  "иначе перезапустите окно (⌘Q). Повторится — правый клик в любом поле ввода → Inspect Element → Console";
const saveDeadlineCache = new WeakMap();

// withSaveDeadline wraps the Wails bindings: Set… rejects after ms with
// SAVE_HUNG_TEXT; its own answer or refusal passes through as is.
function withSaveDeadline(app, ms = SAVE_DEADLINE_MS) {
  if (saveDeadlineCache.has(app)) return saveDeadlineCache.get(app);
  const wrapped = new Proxy(app, {
    get(target, prop) {
      const v = target[prop];
      if (typeof v !== "function" || typeof prop !== "string" || !/^Set[A-Z]/.test(prop)) return v;
      return (...args) => new Promise((ok, fail) => {
        const t = setTimeout(() => fail(new Error(SAVE_HUNG_TEXT)), ms);
        Promise.resolve(v.apply(target, args)).then(
          (r) => { clearTimeout(t); ok(r); },
          (e) => { clearTimeout(t); fail(e); },
        );
      });
    },
  });
  saveDeadlineCache.set(app, wrapped);
  return wrapped;
}
