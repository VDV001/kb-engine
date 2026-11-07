// Интерфейс kb-desktop. Чистый JS без сборки: ходит только в HTTP API движка
// (/api/search — база знаний, /api/files/* — файлы), поэтому одинаково работает
// в окне, в браузере и против удаленного сервера.
"use strict";

const $ = (s) => document.querySelector(s);
const api = (path, signal) => fetch(path, { signal }).then(async (r) => ({ status: r.status, body: r.ok ? await r.json() : null }));

// kb — записи общей выдачи источников (у каждой _src — id источника), sources —
// строка «N из M» над выдачей, filter — источник, выбранный в ней кликом, known —
// весь список источников из настроек: по нему цвет совпадает с панелью.
const state = { q: "", tab: "all", kb: [], files: [], items: [], sel: 0, seq: 0, sources: [], filter: null, known: [] };

// --- Окно (Wails) или браузер -------------------------------------------------
const native = () => window.go && window.go.main && window.go.main.App;
// errText is the words of a refused call: Wails отдает отказ Go объектом
// Error, и String(err) показал бы человеку «Error: …».
const errText = (e) => (e instanceof Error ? e.message : String(e));
const openFile = (p) => native() && native().Open(p);
const revealFile = (p) => native() && native().Reveal(p);
const openURL = (u) => (window.runtime ? window.runtime.BrowserOpenURL(u) : window.open(u, "_blank"));

// --- Подсветка форм слова ------------------------------------------------------
// Те же окончания, что срезает движок (internal/usecase/filesearch): подсветка
// обязана находить «договора» по запросу «договор», как и поиск.
const ENDINGS = ["иями","ями","ами","ого","его","ому","ему","ыми","ими","ией","ием","иях","ях","ах","ов","ев","ей","ой","ий","ый","ая","яя","ое","ее","ые","ие","ую","юю","ом","ем","ам","ям","ия","ию","ии","ых","их","а","я","о","е","ы","и","у","ю","ь"];
function stem(w) {
  if (!/[а-яё]/i.test(w)) return w;
  for (const e of ENDINGS) if (w.endsWith(e) && [...w].length - [...e].length >= 3) return w.slice(0, -e.length);
  return w;
}
const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
// --- Подсказки -----------------------------------------------------------------
// Вместо title: системная подсказка WebKit не берет шрифт и цвета окна и ждет
// около секунды. Одна подсказка на окно и делегирование на document: строку
// источников и список в панели перерисовывает каждый ввод, подписка на каждый
// элемент терялась бы. Текст — из data-tip (имя), data-tip-b (пояснение),
// data-tip-k (клавиша), data-tip-bad (беда, красным). Только по наведению: Tab
// в окне переключает вкладки выдачи и до кнопок не доходит. Блок стоит до
// отрисовки, которая зовет dropStaleTip.
const tip = Object.assign(document.createElement("div"), { id: "tip", className: "tip", hidden: true });
tip.setAttribute("role", "tooltip");
document.body.append(tip);
let tipFor = null, tipTimer = 0;
function showTip(el) {
  const d = el.dataset;
  // --c у чипа и строки источника — его цвет; computed отдает уже готовый цвет.
  const c = "tipBad" in d ? "var(--z-bad)" : getComputedStyle(el).getPropertyValue("--c").trim() || "var(--z-accent)";
  tip.style.setProperty("--c", c);
  tip.innerHTML = `<span class="tip-t">${esc(d.tip)}${d.tipK ? `<kbd>${esc(d.tipK)}</kbd>` : ""}</span>${d.tipB ? `<span class="tip-b">${esc(d.tipB)}</span>` : ""}`;
  // Мерить в левом верхнем углу: с прежним left у правого края ширина
  // сжалась бы, и высота вышла бы больше настоящей.
  tip.style.left = tip.style.top = "0px";
  tip.hidden = false;
  // Под элементом, а у нижнего края окна — над ним; по ширине не за край.
  const r = el.getBoundingClientRect(), t = tip.getBoundingClientRect();
  tip.style.left = Math.max(8, Math.min(r.left, innerWidth - t.width - 8)) + "px";
  tip.style.top = (r.bottom + 8 + t.height <= innerHeight - 8 ? r.bottom + 8 : Math.max(8, r.top - 8 - t.height)) + "px";
  // Скрытый от VoiceOver элемент (ручка перетаскивания) описанием не снабжаем.
  if (!el.closest('[aria-hidden="true"]')) el.setAttribute("aria-describedby", "tip");
  tip.classList.remove("enter");
  void tip.offsetWidth; // перезапуск появления при переходе с одной подсказки на другую
  tip.classList.add("enter");
}
function hideTip() {
  clearTimeout(tipTimer);
  tipFor?.removeAttribute("aria-describedby");
  tipFor = null;
  tip.hidden = true;
}
// Перерисовка строки источников или панели заменила элемент под подсказкой:
// числа в ней устарели, а якоря больше нет.
const dropStaleTip = () => { if (tipFor && !tipFor.isConnected) hideTip(); };
document.addEventListener("mouseover", (e) => {
  const el = e.target.closest("[data-tip]");
  if (el === tipFor) return; // движение по детям того же элемента не сбрасывает задержку
  hideTip();
  if (!el) return;
  tipFor = el;
  tipTimer = setTimeout(() => el.isConnected && showTip(el), 400);
});
document.documentElement.addEventListener("mouseleave", hideTip);
// Клик, клавиша, прокрутка и смена размера прячут: после них строка могла
// перерисоваться или уехать, а место подсказки считается один раз.
for (const ev of ["mousedown", "keydown", "wheel", "scroll"]) document.addEventListener(ev, hideTip, true);
addEventListener("resize", hideTip);

const reEsc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
// hits — слова записи, что нашел движок (match): подсвечиваются целиком, по
// границе слова. Граница проверяется кодом, а не lookbehind в регулярке: его
// WebKit понимает только с Safari 16.4, а окно ставится на macOS 12.
// Сопоставление идет по сырому тексту, экранируются куски: по уже
// экранированному тексту короткое слово находилось бы внутри «&lt;».
const isWordChar = (c) => !!c && /[\p{L}\p{N}]/u.test(c);
function highlighter(q, hits = []) {
  const stems = q.toLowerCase().replace(/ё/g, "е").split(/[^\p{L}\p{N}]+/u).filter((w) => [...w].length >= 2).map(stem);
  const stemRe = stems.length ? new RegExp(`(?:${stems.map((w) => `${reEsc(w)}[\\p{L}\\p{N}]*`).join("|")})`, "giu") : null;
  const words = [...new Set(hits.filter(Boolean).map((h) => h.toLowerCase()))];
  if (!stemRe && !words.length) return esc;
  return (text) => {
    const spans = [];
    if (stemRe) for (const m of text.matchAll(stemRe)) if (m[0]) spans.push([m.index, m.index + m[0].length]);
    const low = text.toLowerCase();
    if (low.length === text.length) {
      for (const w of words) {
        for (let at = low.indexOf(w); at >= 0; at = low.indexOf(w, at + 1)) {
          const end = at + w.length;
          if (!isWordChar(text[at - 1]) && !isWordChar(text[end])) spans.push([at, end]);
        }
      }
    }
    spans.sort((a, b) => a[0] - b[0] || b[1] - a[1]);
    let out = "", last = 0;
    for (const [from, to] of spans) {
      if (to <= last) continue; // вложено в уже подсвеченное
      const start = Math.max(from, last);
      out += esc(text.slice(last, start)) + `<mark>${esc(text.slice(start, to))}</mark>`;
      last = to;
    }
    return out + esc(text.slice(last));
  };
}
// Подсветка по набору слов движка — одна на запрос: у строк выдачи наборы
// повторяются, и разбор не собирается заново на каждую строку.
const hlCache = new Map();
function hlFor(hits) {
  const key = state.q + "\u0000" + hits.join("\u0000");
  if (!hlCache.has(key)) hlCache.set(key, highlighter(state.q, hits));
  return hlCache.get(key);
}

// --- Признак находки -----------------------------------------------------------
// Движок говорит, где и как совпало каждое слово (match). Буквальное совпадение
// в заголовке или описании видно подсветкой; все остальное — поле не на виду
// (автор, тег, категория, номер) или способ не буквальный — называется словами.
const FIELD = { title: "заголовок", description: "описание", category: "категория", tag: "тег", author: "автор", id: "номер" };
const FIELD_WITH = { title: "с заголовком", description: "с описанием", category: "с категорией", tag: "с тегом", author: "с автором", id: "с номером" };
const HOW = { exact: "", synonym: "синоним", translit: "транслит", typo: "опечатка" };
const HOW_LONG = { exact: "", synonym: "по синониму", translit: "через транслитерацию", typo: "несмотря на опечатку" };
const VISIBLE = new Set(["title", "description"]);
const worth = (m) => FIELD[m.field] && HOW[m.how] !== undefined && !(VISIBLE.has(m.field) && m.how === "exact");
// Подсветка по признаку — только там, где совпало: в заголовке и описании —
// небуквальные совпадения этих же полей (буквальные и так ловит подсветка
// запроса), в мета-строке превью — автор и категория. Номер и тег не
// подсвечиваются нигде: их называет строка «совпало».
const hitsWhere = (e, fields, how) => (e.match || []).filter((m) => fields.has(m.field) && how(m.how)).map((m) => m.hit).filter(Boolean);
// В заголовке и описании подсвечиваются все совпадения этих полей, и
// буквальные тоже: разбор запроса в окне не повторяет нормы движка (ё и е,
// запрос из одной буквы), и «Ёжик» по «еж» иначе остался бы без подсветки.
const rowHits = (e) => hitsWhere(e, VISIBLE, (h) => HOW[h] !== undefined);
const metaHits = (e) => hitsWhere(e, new Set(["author", "category"]), (h) => HOW[h] !== undefined);
function whyLine(e) {
  const items = (e.match || []).filter(worth);
  if (!items.length) return "";
  const mk = (t) => `<mark>${esc(t)}</mark>`;
  return `<div class="hit-why"><span class="why-lbl">совпало:</span> ${items.map((m) => [
    FIELD[m.field],
    m.how === "exact" ? mk(m.hit) : `${esc(m.word)} → ${mk(m.hit)}`,
    HOW[m.how],
  ].filter(Boolean).join(" · ")).join("; ")}</div>`;
}
function whySentence(e) {
  const items = (e.match || []).filter(worth);
  return items.map((m) => `<div class="doc-why">«${esc(m.word)}» совпало ${FIELD_WITH[m.field]}${HOW_LONG[m.how] ? " " + HOW_LONG[m.how] : ""}: <mark>${esc(m.hit)}</mark></div>`).join("");
}

// --- Индексация при запуске ----------------------------------------------------
async function waitForIndex() {
  const screen = $("#indexing");
  let shown = false;
  for (;;) {
    const r = await api("/api/files/search?q=kb&limit=1").catch(() => ({ status: 0 }));
    if (r.status === 200) {
      if (shown) await countUp($("#indexing-count"), r.body.indexed);
      if (shown) { screen.classList.add("leave"); setTimeout(() => (screen.hidden = true), 500); }
      setIndexed(r.body.indexed, r.body.skipped);
      return;
    }
    if (r.status === 500) {
      // Сборка упала: «еще собирается» тут было бы враньем, показываем причину.
      const why = await fetch("/api/files/search?q=kb&limit=1").then((x) => x.text()).catch(() => "");
      screen.hidden = false;
      $(".indexing-main .eyebrow").textContent = "Индекс не собрался";
      $("#indexing-count").textContent = "—";
      $(".indexing-bar").hidden = true;
      $("#indexing-note").textContent = why.trim() || "Причина неизвестна";
      // Экран закрывает кнопку настроек — даем выход прямо с него.
      $("#indexing-settings").hidden = false;
      return;
    }
    if (!shown && r.status === 503) { screen.hidden = false; shown = true; }
    await sleep(350);
  }
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function countUp(el, to) {
  // Число известно только по окончании сборки — его и показываем, а не
  // выдуманный прогресс по ходу.
  return new Promise((done) => {
    const t0 = performance.now(), dur = 700;
    const tick = (t) => {
      const k = Math.min(1, (t - t0) / dur), e = 1 - Math.pow(1 - k, 4);
      el.textContent = Math.round(to * e).toLocaleString("ru-RU");
      k < 1 ? requestAnimationFrame(tick) : setTimeout(done, 350);
    };
    requestAnimationFrame(tick);
  });
}
// skipped — сколько файлов не прочитано: в браузере (без окна) это весь счет
// проблем, в окне счет берется из Problems — там и серверы, и каталог.
// err — пересборка не удалась: строка называет причину, а кнопка проблем
// остается (ревью #457: прежде ошибка стирала ее вместе с красной точкой).
function setIndexed(n, skipped, rec, err) {
  state.indexed = { n, skipped, rec, err };
  const el = $("#indexed");
  el.textContent = "";
  // Счет файлов — своим элементом: в тесной шапке он прячется первым (fitBar), проблемы — никогда.
  el.append(Object.assign(document.createElement("span"), { className: "idx-n", textContent: err ? `пересборка не удалась: ${err}` : `${n.toLocaleString("ru-RU")} файлов` +
    (rec ? ` · распознавание ${rec.done.toLocaleString("ru-RU")} из ${rec.total.toLocaleString("ru-RU")}` : "") }));
  // «N проблем» — кнопка «подробнее»: что не так у источников, почему и что
  // сделать (панель справа, #456).
  const total = state.problems ? state.problems.total : skipped;
  // Уровень — цвет точки статуса при наведении (CSS): красная — ошибка,
  // оранжевая — предупреждение, синяя — уведомление.
  el.parentElement.dataset.level = state.problems ? state.problems.level || "" : skipped ? "error" : "";
  const dot = $("#settings-dot"); // точка у раздела «Проблемы» в настройках
  dot.hidden = !total;
  dot.dataset.level = el.parentElement.dataset.level;
  if (!$("#chat-view").hidden) renderChatSources(); // точка ошибки у плашки источника
  if (total) {
    const b = document.createElement("button");
    b.className = "problems-link";
    b.textContent = `${fmt(total)} ${plural(total, "проблема", "проблемы", "проблем")}`;
    b.dataset.tip = "Проблемы";
    b.dataset.tipB = "Что не так у источников: серверы, каталог, папки и файлы. Нажмите — почему и что сделать";
    b.onclick = () => openSettings("problems");
    el.append(Object.assign(document.createElement("span"), { className: "idx-sep", textContent: " · " }), b);
  }
  fitBar();
  el.parentElement.dataset.tip = "Индекс файлов";
  el.parentElement.dataset.tipB = "Файлы ваших папок, по которым идет поиск" + (skipped ? `; ${skipped} прочитать не удалось` : "");
}

// Счет растет и после первой сборки: окно пересобирается по таймеру и
// распознает PDF и картинки в фоне. Опрос часто, пока что-то идет, и раз в
// 30 с в покое; спрятанное окно не опрашивается (ревью #381: 28 тыс. вызовов
// в сутки у приложения, которое весь день в меню-баре). Ошибка пересборки
// показывается, а не замораживает строку.
async function pollIndex() {
  let next = 30000;
  if (native() && !document.hidden && !settingsOpen()) {
    const st = await native().IndexStatus().catch(() => null);
    if (st && (st.error || !st.building)) {
      await refreshProblems();
      setIndexed(st.indexed, st.skipped, st.recognizing, st.error);
    }
    if (st && (st.building || st.recognizing)) next = 3000;
  }
  setTimeout(pollIndex, next);
}
if (native()) setTimeout(pollIndex, 3000);
// Список источников — ради цветов строки источников; панель его обновляет сама.
if (native()) native().Sources().then((v) => { state.known = v.sources || []; refreshTabs(); renderChatSources(); }).catch(() => {});
if (native()) native().Tabs().then((t) => { state.tabs = t || []; refreshTabs(); }).catch(() => {});

// --- Поиск ---------------------------------------------------------------------
let timer;
$("#q").addEventListener("input", (e) => {
  clearTimeout(timer);
  timer = setTimeout(() => search(e.target.value.trim()), 110);
});

let inflight; // запросы прошлой буквы: отменяются, и Go перестает ждать серверы
async function search(q) {
  // Новый запрос снимает фильтр источника: иначе выбранный источник, не
  // нашедший ничего по новому слову, прятал бы и файлы, и остальные источники.
  if (q !== state.q) { state.filter = null; hlCache.clear(); }
  state.q = q;
  if (inflight) inflight.abort();
  inflight = new AbortController();
  const signal = inflight.signal;
  const seq = ++state.seq;
  // HEAD на ветке — текущий запрос, как текущий шаг на сайте.
  $("#branch-label").textContent = q ? `find «${q.length > 40 ? q.slice(0, 40) + "…" : q}»` : "поиск";
  if (!q) { $("#app").classList.remove("busy"); state.kb = []; state.files = []; state.sources = []; render(); showEmpty(); return; }
  const enc = encodeURIComponent(q);
  $("#app").classList.add("busy");
  const [kb, files] = await Promise.all([
    searchSources(enc, signal),
    api(`/api/files/search?q=${enc}&limit=60`, signal).catch(() => ({ body: null })),
  ]);
  if (seq !== state.seq) return; // пришел ответ на устаревший запрос
  $("#app").classList.remove("busy");
  // Записей от источника не больше 40: Go склеивает дубли по всему найденному
  // и только потом режет, поэтому фильтр по нижнему источнику показывает его
  // записи, а не пустоту.
  state.kb = kb.hits.map((h) => ({ ...h.entry, _src: h.source }));
  state.sources = kb.sources;
  state.dupes = kb.dupes || 0;
  if (state.filter && !kb.sources.some((s) => s.id === state.filter && !s.error)) state.filter = null;
  // Файл выдачи помечен своей папкой-источником — по ней строка источников
  // считает «показано» и фильтрует. Вложенных папок нет (checkNested), поэтому
  // папка у файла одна.
  const roots = kb.sources.filter((x) => x.kind === "folder" && x.root);
  state.files = ((files.body && files.body.hits) || []).map((f) => ({ ...f, _src: folderOf(f.path, roots) }));
  state.sel = 0;
  render();
}

// folderOf returns the id of the folder source that holds path, "" if none.
// ponytail: граница папки повторяет countUnder из Go (с «/» вместо
// filepath.Separator). Потолок: правка одной стороны разведет «показано» и
// «найдено». Путь: id источника у каждого файла в ответе окна.
function folderOf(path, roots) {
  const r = roots.find((x) => path === x.root || path.startsWith(x.root.replace(/\/$/, "") + "/"));
  return r ? r.id : "";
}

// Общая выдача источников живет в окне (/api/sources/search). Простой kbengine
// serve в браузере ее не знает — тогда поиск по его каталогу, без строки источников.
async function searchSources(enc, signal) {
  const r = await api(`/api/sources/search?q=${enc}`, signal).catch(() => ({ status: 0, body: null }));
  if (r.body) return r.body;
  if (r.status === 404) {
    const old = await api(`/api/search?q=${enc}`, signal).catch(() => ({ body: null }));
    return { hits: (old.body || []).map((entry) => ({ source: "", entry })), sources: [] };
  }
  return { hits: [], sources: [] };
}

// --- Вкладки -------------------------------------------------------------------
// «Все» всегда первая; остальные — из настроек окна (config.json, tabs):
// вкладка по типу (kb — записи базы, doc/code/image — файлы) или по источнику
// (его записи и файлы). Без настройки — DEFAULT_TABS, тот же набор, что
// desktopconfig.DefaultTabs.
const DEFAULT_TABS = [
  { name: "База знаний", kind: "type", value: "kb" },
  { name: "Документы", kind: "type", value: "doc" },
  { name: "Код", kind: "type", value: "code" },
  { name: "Картинки", kind: "type", value: "image" },
];
const TYPE_TIP = {
  kb: "Записи каталога и kb-серверов",
  doc: "Найденные файлы папок с текстом: заметки, PDF, Word, HTML, таблицы",
  code: "Найденные файлы папок с исходным кодом и настройками",
  image: "Найденные картинки папок: текст на них распознается",
};
const TYPE_NAME = { kb: "база", doc: "док", code: "код", image: "картинки" };
state.tabs = DEFAULT_TABS;
// Ключ вкладки — вид и значение (desktopconfig.Tab.Key), а не номер: после
// удаления или перестановки активной иначе стала бы соседняя. Вкладку
// неизвестного вида (ее записала новая сборка) окно хранит, но не показывает.
const tabKey = (t) => t.kind + ":" + t.value;
const visibleTab = (t) => (t.kind === "type" && TYPE_NAME[t.value] !== undefined) || t.kind === "source";
const kbItem = (e) => ({ type: "kb", e });
const fileItem = (f) => ({ type: "file", f });
const tabItems = (t, kb, files) =>
  t.kind === "type"
    ? t.value === "kb" ? kb.map(kbItem) : files.filter((f) => f.kind === t.value).map(fileItem)
    : [...kb.filter((e) => e._src === t.value).map(kbItem), ...files.filter((f) => f._src === t.value).map(fileItem)];
// Модули окна (#435): вкладка модуля — после вкладок поиска, ключ module:<id>.
// Сборка знает только эти; модуль новой сборки в config.json вкладки не дает.
const MODULES = {
  chat: { name: "Чат", what: "вопросы к своим документам — ответ со ссылками на записи и файлы", view: "chat",
    icon: '<path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5v7a2.5 2.5 0 0 1-2.5 2.5H10l-4.5 4v-4H6.5A2.5 2.5 0 0 1 4 13.5z"/>' },
  finances: { name: "Финансы", what: "счета, траты, отчеты — с kb-сервера", src: "/app/?tab=finances", // сразу раздел финансов, а не главная витрины
    icon: '<circle cx="12" cy="12" r="8"/><path d="M10 16.5V7.5h3a2.5 2.5 0 0 1 0 5H8.5M8.5 14.5h5"/>' },
  stats: { name: "Статистика", what: "токены на вход и на выход, вызовы модели, ошибки — по дням, назначениям и моделям", view: "stats",
    icon: '<path d="M4 20h16M7 16.5V12M12 16.5V6.5M17 16.5V9.5"/>' },
  access: { name: "Доступы", what: "доступы к продуктам: карточки видны, пароль — только по Touch ID", view: "access",
    icon: '<rect x="5" y="10.5" width="14" height="9.5" rx="1.5"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>' },
};
// Значок модуля без своего (новая сборка добавила модуль, а значок забыли) — квадрат, а не пустое место.
const MODULE_ICON = '<rect x="5" y="5" width="14" height="14" rx="2"/>';
const SEARCH_ICON = '<circle cx="10.5" cy="10.5" r="6"/><path d="m15 15 5 5"/>';
state.modules = [];
const moduleKeys = () => state.modules.filter((m) => m.enabled && MODULES[m.id]).map((m) => "module:" + m.id);
const moduleOf = (key) => (key.startsWith("module:") ? key.slice("module:".length) : null);
const tabKeys = () => ["all", ...state.tabs.filter(visibleTab).map(tabKey), ...moduleKeys()];
function itemsOf(key, kb, files) {
  // «Все» — объединение вкладок: все записи базы и все файлы, число равно сумме
  // (решение владельца 06.11; до того база резалась до 5 и «Все» показывало
  // меньше «Базы знаний»).
  if (key === "all") return [...kb.map(kbItem), ...files.map(fileItem)];
  const t = state.tabs.find((x) => tabKey(x) === key);
  return t ? tabItems(t, kb, files) : [];
}
const sourceOf = (t) => state.known.find((x) => x.id === t.value);
function tabTip(t) {
  if (t.kind === "type") return TYPE_TIP[t.value] || "";
  const s = sourceOf(t);
  return s ? `Записи и файлы источника ${label(s)}` : "Источника больше нет в списке — вкладка пуста";
}
// Полоса вкладок строится заново при смене набора; подчеркивание остается.
// Полоса и выдача заново: набор вкладок или список источников сменился.
function refreshTabs() { renderTabs(); render(); }
// Шапка — вариант 1 (решение владельца 07.11): наверху Поиск и модули со
// значками, вкладки поиска — чипами под строкой поиска. Кнопка «Поиск» ведет
// на последнюю вкладку поиска, а не на «Все».
state.searchTab = "all";
function renderTabs() {
  const nav = $("#tabs"), ink = nav.querySelector(".tab-ink"), chips = $("#chips");
  nav.querySelectorAll("button").forEach((b) => b.remove());
  chips.textContent = "";
  const top = (key, name, tip, icon, n) => {
    const b = document.createElement("button");
    b.dataset.tab = key;
    b.dataset.tip = name;
    b.dataset.tipB = tip;
    if (n <= 9) b.dataset.tipK = "⌘ " + n;
    b.setAttribute("aria-label", name);
    b.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${icon}</svg><span class="lbl">${esc(name)}</span>`;
    nav.insertBefore(b, ink);
  };
  const chip = (key, name, tip) => {
    const b = document.createElement("button");
    b.dataset.tab = key;
    b.dataset.tip = name;
    b.dataset.tipB = tip;
    b.append(Object.assign(document.createElement("span"), { className: "lbl", textContent: name }), " ", document.createElement("i"));
    chips.append(b);
  };
  top("search", "Поиск", "Записи базы и файлы ваших папок; вкладки — под строкой поиска", SEARCH_ICON, 1);
  moduleKeys().forEach((k, i) => top(k, MODULES[moduleOf(k)].name, MODULES[moduleOf(k)].what, MODULES[moduleOf(k)].icon || MODULE_ICON, i + 2));
  chip("all", "Все", "Записи базы и найденные файлы папок — все вкладки вместе");
  state.tabs.filter(visibleTab).forEach((t) => chip(tabKey(t), t.name, tabTip(t)));
  if (!tabKeys().includes(state.tab)) state.tab = "all";
  if (!tabKeys().includes(state.searchTab) || moduleOf(state.searchTab)) state.searchTab = "all";
  fitBar();
}
const pickTab = (key) => {
  state.tab = key === "search" ? state.searchTab : key;
  state.sel = 0;
  render();
};
$("#tabs").addEventListener("click", (e) => { const b = e.target.closest("button"); if (b) pickTab(b.dataset.tab); });
$("#chips").addEventListener("click", (e) => { const b = e.target.closest("button"); if (b) pickTab(b.dataset.tab); });
// ⌘1 — Поиск, ⌘2… — модули по порядку в шапке (подсказка каждой кнопки их называет).
document.addEventListener("keydown", (e) => {
  if (!e.metaKey || e.altKey || e.ctrlKey || !/^[1-9]$/.test(e.key) || settingsOpen()) return; // под открытыми настройками модуль не сменится незаметно
  const b = document.querySelectorAll("#tabs button")[+e.key - 1];
  if (!b) return;
  e.preventDefault();
  pickTab(b.dataset.tab);
});
function moveInk() {
  if (!moduleOf(state.tab)) state.searchTab = state.tab;
  const b = $(`#tabs button[data-tab="${moduleOf(state.tab) ? state.tab : "search"}"]`), ink = $(".tab-ink");
  document.querySelectorAll("#tabs button").forEach((x) => x.classList.toggle("active", x === b));
  document.querySelectorAll("#chips button").forEach((x) => x.classList.toggle("active", x.dataset.tab === state.tab));
  if (!b) return;
  ink.style.left = b.offsetLeft + "px";
  ink.style.width = b.offsetWidth + "px";
}
// Шапка не прокручивается никогда. Как только место справа от последней кнопки
// меньше отступа слева от первой, подписи модулей прячутся (остаются значки с
// подсказками); тесно и так — короче становится счет файлов, потом «источники»
// значком. «N проблем» видно всегда (решение владельца 07.11).
function fitBar() {
  const BAR_STEPS = ["compact", "tight", "tighter"]; // внутри: setIndexed зовет fitBar раньше, чем дойдет до этой строки
  const bar = $(".bar"), nav = $("#tabs"), btns = nav.querySelectorAll("button");
  if (!btns.length) return;
  bar.classList.remove(...BAR_STEPS);
  const box = () => nav.getBoundingClientRect();
  const left = () => btns[0].getBoundingClientRect().left - box().left;
  const free = () => box().right - btns[btns.length - 1].getBoundingClientRect().right;
  for (const step of BAR_STEPS) {
    if (free() >= left()) break;
    bar.classList.add(step);
  }
  moveInk();
}
new ResizeObserver(() => fitBar()).observe($(".bar"));

// --- Отрисовка -----------------------------------------------------------------
function render() {
  // Фильтр источника оставляет только его записи: у базы знаний — записи,
  // у папки — ее файлы.
  const only = state.filter;
  const kb = only ? state.kb.filter((e) => e._src === only) : state.kb;
  const files = only ? state.files.filter((f) => f._src === only) : state.files;
  // Счетчик — длина того же списка, что покажет вкладка.
  document.querySelectorAll("#chips button").forEach((b) => (b.querySelector("i").textContent = state.q ? itemsOf(b.dataset.tab, kb, files).length : ""));
  moveInk();
  if (showModule(moduleOf(state.tab))) return;

  // «Показано» у источника — сколько его записей в текущей вкладке до фильтра:
  // клик по одному источнику не обнуляет счет остальных.
  state.items = itemsOf(state.tab, kb, files);
  const shown = {};
  const unfiltered = state.filter ? itemsOf(state.tab, state.kb, state.files) : state.items;
  for (const it of unfiltered) {
    const src = it.type === "kb" ? it.e._src : it.f._src;
    if (src) shown[src] = (shown[src] || 0) + 1;
  }
  // Под фильтром у выбранного источника показано столько, сколько в списке.
  if (state.filter) shown[state.filter] = state.items.length;
  const hue = stripHues(); // один раз на отрисовку, а не на каждую строку
  renderStrip(shown, hue);
  $("#count").textContent = state.q ? `${state.items.length}` : "";
  const hl = highlighter(state.q);
  const list = $("#results");
  // Вкладка источника, убранного из списка, пуста всегда: «Ничего не нашлось»
  // звало бы менять запрос, а дело во вкладке (#433).
  const cur = state.tabs.find((t) => tabKey(t) === state.tab);
  if (cur && cur.kind === "source" && !sourceOf(cur)) {
    list.innerHTML = `<li class="nothing">Источника этой вкладки больше нет в списке.<br>Убрать вкладку — в настройках окна (шестеренка).</li>`;
    showEmpty();
    return;
  }
  if (state.q && !state.items.length) {
    list.innerHTML = `<li class="nothing">Ничего не нашлось</li>`;
    showEmpty();
    return;
  }
  list.innerHTML = state.items.map((it, i) => (it.type === "kb" ? kbRow(it.e, i, hl, hue) : fileRow(it.f, i, hl))).join("");
  if (state.items.length) select(state.sel);
}

// Строка журнала, как таблица на сайте: тип своим цветом (как роли «Я» и
// «Агент») · имя · когда, ниже строка совпадения.
// Подпись по настоящему типу: картинка не выдается за документ — вкладка
// «Документы» ее и не покажет.
const TYPE_LABEL = { kb: "база", doc: "док", code: "код", image: "img", other: "файл" };
function kbRow(e, i, hl, hue) {
  const hits = rowHits(e);
  // Свой разбор у строки с признаком: цена — микросекунды на строку выдачи,
  // зато подсвечено то, что нашел движок, а не догадка окна.
  if (hits.length) hl = hlFor(hits);
  const verdict = e.verdict ? `<span class="verdict ${e.verdict === "keep" ? "keep" : ""}">${esc(e.verdict)}</span>` : "";
  // Метка источника — только когда их больше одного: одна и та же подпись у
  // каждой строки ничего не говорит.
  const s = kbSourceCount() > 1 && state.sources.find((x) => x.id === e._src);
  const from = s ? `<span class="hit-src" style="--c:var(${hue.get(s.id)})">${esc(label(s))}</span>` : "";
  return `<li class="hit" data-i="${i}" style="--i:${Math.min(i, 14)}">
    <div class="hit-head"><span class="type kb">${TYPE_LABEL.kb}</span><span class="hit-name">${hl(e.title)}</span><span class="hit-tail">${from}${verdict}</span></div>
    ${whyLine(e)}
    ${e.description ? `<div class="hit-snip">${hl(e.description)}</div>` : ""}
  </li>`;
}
function fileRow(f, i, hl) {
  const t = TYPE_LABEL[f.kind] ? f.kind : "other";
  return `<li class="hit" data-i="${i}" style="--i:${Math.min(i, 14)}">
    <div class="hit-head"><span class="type ${t}">${TYPE_LABEL[t]}</span><span class="hit-name">${hl(f.name)}</span><span class="hit-meta">${ago(f.modified)}</span></div>
    <div class="hit-path">${esc(shortPath(f.path))}</div>
    ${f.snippet ? `<div class="hit-snip">${hl(f.snippet)}</div>` : ""}
  </li>`;
}
const shortPath = (p) => {
  // Домашняя папка — «~», длинный путь — последние три части: имя файла и
  // ближние папки говорят больше, чем начало пути.
  const parts = p.replace(/^\/Users\/[^/]+/, "~").split("/");
  return parts.length > 4 ? "…/" + parts.slice(-4, -1).join("/") : parts.slice(0, -1).join("/");
};
function ago(iso) {
  const d = (Date.now() - new Date(iso)) / 864e5;
  if (d < 1) return "сегодня";
  if (d < 7) return `${Math.floor(d)} дн`;
  if (d < 60) return `${Math.floor(d / 7)} нед`;
  return `${Math.floor(d / 30)} мес`;
}

$("#results").addEventListener("click", (e) => {
  const li = e.target.closest(".hit");
  if (li) select(+li.dataset.i);
});
$("#results").addEventListener("dblclick", (e) => {
  const li = e.target.closest(".hit");
  if (li) activate(state.items[+li.dataset.i]);
});

let previewSeq = 0;
function select(i) {
  state.sel = Math.max(0, Math.min(i, state.items.length - 1));
  document.querySelectorAll(".hit").forEach((el) => el.classList.toggle("sel", +el.dataset.i === state.sel));
  const el = $(`.hit[data-i="${state.sel}"]`);
  if (el) el.scrollIntoView({ block: "nearest" });
  preview(state.items[state.sel]);
}

async function preview(it) {
  if (!it) return;
  const seq = ++previewSeq, hlq = highlighter(state.q), box = $("#preview");
  if (it.type === "kb") {
    const e = it.e;
    const hits = rowHits(e), hl = hits.length ? hlFor(hits) : hlq;
    // Мета-строка: подсвечиваются только автор и категория и только по
    // признаку — источник, вердикт и дату поиск не смотрит.
    const hlMeta = highlighter("", metaHits(e));
    const meta = [[sourceName(e._src), false], [e.category, true], [e.verdict, false], [e.author, true], [e.date_added, false]]
      .filter(([t]) => t).map(([t, searched]) => (searched ? hlMeta(t) : esc(t))).join(" · ");
    box.innerHTML = `<div class="doc">
      <div class="doc-head"><div class="prompt"><span class="dollar">$</span> kb show ${esc(String(e.id))}</div><div class="doc-title">${hl(e.title)}</div>
        <div class="doc-path">${meta}</div>
        ${whySentence(e)}
        <div class="doc-actions">${e.url ? `<button class="btn primary" data-act="url">Открыть источник</button>` : ""}</div></div>
      <div class="doc-desc">${hl(e.description || "Описания нет.")}</div></div>`;
    const b = box.querySelector('[data-act="url"]');
    if (b) b.onclick = () => openURL(e.url);
    return;
  }
  const f = it.f;
  const r = await api(`/api/files/read?path=${encodeURIComponent(f.path)}`).catch(() => ({ body: null }));
  if (seq !== previewSeq) return;
  const text = r.body ? r.body.text : "Не удалось прочитать файл.";
  box.innerHTML = `<div class="doc">
    <div class="doc-head"><div class="prompt"><span class="dollar">$</span> cat ${esc(f.name)}</div><div class="doc-title">${esc(f.name)}</div>
      <div class="doc-path">${esc(f.path.replace(/^\/Users\/[^/]+/, "~"))}</div>
      <div class="doc-actions"><button class="btn primary" data-act="open">Открыть</button><button class="btn" data-act="reveal">Показать в Finder</button></div></div>
    <pre class="doc-text">${hlq(text.slice(0, 200000))}</pre></div>`;
  box.querySelector('[data-act="open"]').onclick = () => openFile(f.path);
  box.querySelector('[data-act="reveal"]').onclick = () => revealFile(f.path);
  box.scrollTop = 0;
  const m = box.querySelector("mark");
  if (m) m.scrollIntoView({ block: "center" });
}

function activate(it, reveal) {
  if (!it) return;
  if (it.type === "kb") return it.e.url && openURL(it.e.url);
  reveal ? revealFile(it.f.path) : openFile(it.f.path);
}

function showEmpty() {
  $("#preview").innerHTML = `<div class="empty"><div class="prompt"><span class="dollar">$</span> cat README</div>
    <p>Поиск по базе знаний и файлам на этом компьютере.</p>
    <p class="muted">Слова ищутся во всех формах: договор, договора, договорами.</p>
    <p class="muted keys-line"><b>↑↓</b> выбор <b>↵</b> открыть <b>⌘↵</b> в Finder <b>⌘,</b> папки <b>esc</b> очистить, еще раз — спрятать <b>⌥space</b> открыть</p></div>`;
}

// --- Клавиатура ----------------------------------------------------------------
document.addEventListener("keydown", (e) => {
  if (settingsOpen()) return; // клавиши поиска не трогают панель источников
  if (e.key === "ArrowDown") { e.preventDefault(); select(state.sel + 1); }
  else if (e.key === "ArrowUp") { e.preventDefault(); select(state.sel - 1); }
  else if (e.key === "Enter") { e.preventDefault(); activate(state.items[state.sel], e.metaKey); }
  else if (e.key === "Escape") {
    // Первый Esc очищает запрос, второй прячет окно: kb живет в меню-баре.
    if ($("#q").value) { $("#q").value = ""; search(""); }
    else if (window.runtime) window.runtime.Hide(); // как красная кнопка: фокус уходит туда, откуда звали
  }
  else if (e.key === "Tab") {
    e.preventDefault();
    const order = tabKeys(), k = order.indexOf(state.tab);
    state.tab = order[(k + (e.shiftKey ? order.length - 1 : 1)) % order.length];
    state.sel = 0;
    render();
  }
  else if (e.key.length === 1 && document.activeElement !== $("#q") && !e.metaKey) $("#q").focus();
});

window.addEventListener("resize", moveInk);

// ⌥Space и «Открыть поиск» в меню-баре: окно появляется с курсором в поиске.
// Знак «просыпается» вместе с окном и при наведении: полушария расходятся,
// D, V, K перерисовываются по очереди (style.css). wake снимается, когда
// дорисована K, а не по таймеру: таймер расходился с CSS и обрывал цикл при
// повторном ⌥Space.
const brand = $(".brand");
// Идущий цикл не перезапускается ни наведением, ни повторным ⌥Space: рывок
// букв назад и есть моргание.
const wakeMark = () => {
  if (matchMedia("(prefers-reduced-motion: reduce)").matches || brand.classList.contains("wake")) return;
  brand.classList.add("wake");
};
// У K две анимации: конец стирания (0.35 с) — не конец цикла. Отмена (снятый
// класс, скрытое окно) тоже снимает wake, иначе знак застрял бы раскрытым.
const markDone = (e) => { if (e.animationName === "mark-draw") brand.classList.remove("wake"); };
$(".mark-k").addEventListener("animationend", markDone);
$(".mark-k").addEventListener("animationcancel", markDone);
brand.addEventListener("mouseenter", wakeMark);
if (window.runtime) window.runtime.EventsOn("kb:show", () => { $("#q").focus(); $("#q").select(); wakeMark(); });
renderTabs();
moveInk();
// Шрифт вшит и грузится после первого замера: подчеркивание мерить заново.
document.fonts.ready.then(moveInk);
showEmpty(); // одна копия пустого экрана — в showEmpty, в разметке его нет
waitForIndex();

// --- Источники -----------------------------------------------------------------
// Один список: папки, каталог базы, kb-серверы. Порядок — приоритет. Правки идут
// в черновик, «Сохранить» отдает его Go: проверка там, а не здесь (папка обязана
// существовать, вложенность, файл каталога, адрес сервера).
const panel = $("#settings"), scrim = $("#scrim");
const KIND = { folder: "папка", catalog: "каталог KB", server: "kb-сервер", gdrive: "папка Google Диска" };
// Подключения Google для формы папки Диска: обновляются при открытии «Источников».
let googleConns = [];
// Ни один цвет источника не совпадает с цветами состояний (--z-bad, --z-ok):
// красный и зеленый спектра заменены своими (style.css).
const HUES = ["--z-sp4", "--z-sp2", "--z-src-teal", "--z-sp1", "--z-sp5", "--z-src-violet"];
// tokens — токены серверов, введенные в формах, до сохранения: id → {url,
// token}. Токен привязан к адресу, для которого его ввели: на другой адрес он
// не уходит ни при «Проверить», ни при сохранении.
// Уходят в Go один раз при сохранении (в Связку ключей) и стираются; обратно
// Go отдает только признак hasToken.
const draft = { sources: [], exclude: [], tokens: {}, saved: "" };
const ui = { editing: null, replacing: null, confirmDel: null, addKind: null, drag: null, verdict: null, checkSeq: 0, adding: false, statesSeq: 0 };
let keySeq = 0;

const target = (s) => (s.kind === "server" || s.kind === "gdrive" ? s.url : s.path) || "";
const tilde = (p) => p.replace(/^\/Users\/[^/]+/, "~");
const label = (s) => s.name || (s.kind === "gdrive" ? "Google Диск" : s.kind === "server" ? hostOf(s.url) : target(s).split("/").filter(Boolean).pop()) || target(s);
const hostOf = (u) => { try { return new URL(u).host; } catch { return u; } };
const fmt = (n) => n.toLocaleString("ru-RU");
function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  return m10 === 1 && m100 !== 11 ? one : m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14) ? few : many;
}
// Цвет держится за путь или адрес, а не за место и не за id: не меняется ни
// при перестановке, ни после первого сохранения нового источника (id выдает
// Go). Совпадения разводятся по кругу, пока цветов хватает; порядок разведения —
// по пути, а не по приоритету, чтобы перестановка их не перетасовывала.
// Панель и строка источников зовут одну функцию над одним списком, поэтому
// источник одного цвета и там, и там.
function hues(list = draft.sources, keyOf = (s) => s._key) {
  const hash = (t) => { let h = 0; for (const c of t) h = (h * 31 + c.charCodeAt(0)) >>> 0; return h; };
  const out = new Map(), used = new Set();
  for (const s of [...list].sort((a, b) => (target(a) + keyOf(a) < target(b) + keyOf(b) ? -1 : 1))) {
    let i = hash(s.kind + target(s)) % HUES.length;
    for (let k = 0; k < HUES.length && used.has(i) && used.size < HUES.length; k++) i = (i + 1) % HUES.length;
    used.add(i);
    out.set(keyOf(s), HUES[i]);
  }
  return out;
}
// Цвета строки источников — по всему списку настроек, если он известен (окно);
// в браузере списка нет, и цвет считается по тем, кто ответил.
const stripHues = () => hues(state.known.length ? state.known : state.sources, (s) => s.id);
// Метка источника у записи — когда баз знаний больше одной; папки не в счет.
const kbSourceCount = () => state.sources.filter((x) => x.kind !== "folder").length;
const sourceName = (id) => { const s = state.sources.find((x) => x.id === id); return s && kbSourceCount() > 1 ? label(s) : ""; };

// Строка источников над выдачей: «имя N из M» — показано из найденного; клик
// оставляет в выдаче только этот источник, повторный — снимает фильтр.
// Источник с бедой назван, а не пропущен: короткое слово в строке, причина целиком
// в подсказке.
function renderStrip(shown, hue) {
  const strip = $("#strip");
  strip.hidden = !state.q || !state.sources.length;
  if (strip.hidden) return;
  // «все» первым: сброс фильтра виден и нажимается, а не прячется в повторном
  // клике по выбранному источнику (просьба владельца 05.11).
  // У «все» тот же счет, что у источников, — их сумма; источник с ошибкой чисел не дает.
  const ok = state.sources.filter((s) => !s.error);
  const sumShown = ok.reduce((n, s) => n + (shown[s.id] || 0), 0), sumFound = ok.reduce((n, s) => n + (s.found || 0), 0);
  const all = `<button class="chip all" data-src="" aria-pressed="${!state.filter}" data-tip="Все источники" data-tip-b="Выдача сразу по всем источникам · показано ${fmt(sumShown)} из ${fmt(sumFound)}">все<span class="cnt"><b>${fmt(sumShown)}</b> из ${fmt(sumFound)}</span></button>`;
  strip.innerHTML = `<span class="strip-lbl">источники</span>` + all + state.sources.map((s) => {
    const name = esc(label(s)), c = `style="--c:var(${hue.get(s.id)})"`;
    if (s.error) return `<span class="chip off" ${c} data-tip="${name}" data-tip-b="${esc(s.error)}" data-tip-bad><span class="chip-dot"></span>${name}<span class="cnt">${esc(s.error.split(":")[0])}</span></span>`;
    return `<button class="chip" ${c} data-src="${esc(s.id)}" aria-pressed="${state.filter === s.id}" data-tip="${name}" data-tip-b="${esc(tilde(target(s)))} · показано ${fmt(shown[s.id] || 0)} из ${fmt(s.found)}"><span class="chip-dot"></span>${name}<span class="cnt"><b>${fmt(shown[s.id] || 0)}</b> из ${fmt(s.found)}</span></button>`;
  }).join("") + (state.dupes > 0 ? `<span class="strip-sum">склеено дублей: ${fmt(state.dupes)}</span>` : "");
  dropStaleTip();
}
$("#strip").addEventListener("click", (e) => {
  const b = e.target.closest("button.chip");
  if (!b) return;
  // «все» (data-src="") снимает фильтр; повторный клик по источнику — тоже.
  state.filter = !b.dataset.src || state.filter === b.dataset.src ? null : b.dataset.src;
  state.sel = 0;
  render();
});
const payload = () => ({
  sources: draft.sources.map(({ id, kind, name, path, url, conn, enabled }) => ({ id, kind, name: name || "", path: path || "", url: url || "", conn: conn || "", enabled })),
  exclude: draft.exclude,
});
// Токены только для серверов, которые еще в списке: убранный или отмененный
// «Вернуть» сервер не должен нести токен в сохранение (Go отклонил бы весь список).
const liveTokens = () => {
  const out = {};
  for (const s of draft.sources) {
    const t = s.kind === "server" && draft.tokens[s.id];
    if (t && t.url === s.url) out[s.id] = t.token;
  }
  return out;
};
// tokenFor — введенный в черновике токен сервера, но только для этого адреса.
const tokenFor = (s, url) => { const t = s && draft.tokens[s.id]; return t && t.url === url ? t.token : ""; };
// Черновик, который еще не загружали (saved пуст), не изменен: вид «Настройки
// окна» источники не читает, и закрытие его звало «Не сохранено» впустую.
const dirty = () => draft.saved !== "" && (JSON.stringify(payload()) !== draft.saved || Object.keys(liveTokens()).length > 0);
const byKey = (k) => draft.sources.find((s) => s._key === k);

function load(v) {
  state.known = v.sources || [];
  refreshTabs(); // подписи вкладок по источникам зависят от списка
  draft.sources = (v.sources || []).map((s) => ({ ...s, _key: "k" + ++keySeq, _orig: target(s), _was: s.enabled }));
  draft.exclude = [...(v.exclude || [])];
  draft.tokens = {};
  draft.saved = JSON.stringify(payload());
}

// Одна панель, два вида: «источники» (кнопка рядом и ⌘,) и настройки окна
// (шестеренка, #438). Вид только прячет чужие разделы: черновик источников
// переживает переход в настройки и обратно.
const VIEWS = {
  sources: { title: "Источники", cmd: "config sources" },
  prefs: { title: "Окно", cmd: "config window" },
  problems: { title: "Проблемы", cmd: "problems" },
  chat: { title: "Чат и модель", cmd: "config chat" },
  look: { title: "Оформление", cmd: "config look" },
  conn: { title: "Подключения", cmd: "config connections" },
  memory: { title: "Что DVK помнит обо мне", cmd: "memory" },
};
async function openSettings(view = "sources") {
  hideTip(); // ⌘, перехватывается раньше общего keydown подсказки
  panel.dataset.view = view;
  $("#settings-title").textContent = VIEWS[view].title;
  $("#settings-cmd").textContent = VIEWS[view].cmd;
  scrim.hidden = false;
  panel.classList.add("open");
  panel.setAttribute("aria-hidden", "false");
  panel.querySelectorAll("[data-go]").forEach((b) => b.setAttribute("aria-current", String(b.dataset.go === view)));
  showError("");
  if (view === "chat") {
    if (native() && native().ChatSettings) await loadChatSettings();
    await renderVoiceSettings();
    await loadPortrait();
    $("#chat-model-note").textContent = native() && native().ChatSettings ? "" : "Модель чата настраивается в окне приложения.";
    $("#chat-name").focus();
    return;
  }
  if (view === "memory") {
    await loadPortrait();
    renderPortrait();
    $("#mem-back").focus();
    return;
  }
  if (view === "problems") {
    renderProblems(); // сразу прежний список, следом свежий: серверы отвечают до 3 с
    $("#close-settings").focus();
    refreshProblems(true).then(() => {
      if (panel.dataset.view === "problems") renderProblems();
      const i = state.indexed;
      if (i) setIndexed(i.n, i.skipped, i.rec, i.err);
    });
    return;
  }
  if (view === "conn") {
    await renderConnections();
    $("#close-settings").focus();
    return;
  }
  if (view === "look") {
    renderAccents();
    $("#close-settings").focus();
    return;
  }
  if (view === "prefs") {
    renderTabSettings();
    renderModules();
    $("#close-settings").focus();
    return;
  }
  const editable = !!native();
  for (const id of ["#add-exclude", "#exclude-input"]) $(id).disabled = !editable;
  if (!editable) {
    load({});
    renderSources();
    $("#settings-note").textContent = "Источники меняются в окне приложения, не в браузере.";
    return;
  }
  // Несохраненный черновик переживает закрытие панели: случайный Esc не стирает
  // полчаса перестановок.
  if (!dirty() || !draft.sources.length) {
    try {
      load(await native().Sources());
    } catch (e) {
      showError("Не удалось прочитать настройки: " + errText(e));
      return;
    }
  }
  if (native().Connections) googleConns = ((await native().Connections().catch(() => [])) || []).filter((c) => c.kind === "google" && c.enabled);
  const list = $("#sources");
  list.classList.add("enter");
  renderSources();
  setTimeout(() => list.classList.remove("enter"), 500);
  $("#close-settings").focus();
  loadServerStates();
}
// renderConnections lists the window's accesses; «Изменить» ведет в раздел,
// где доступ живет (модель — «Чат и модель», kb-сервер — «Источники»).
const CONN_KIND = { model: "модель", kbserver: "kb-сервер", google: "Google Диск", speech: "распознавание речи" };
let connConfirm = null;
async function renderConnections() {
  const list = $("#conn-list");
  if (!native() || !native().Connections) { list.innerHTML = `<li class="muted">Подключения видны в окне приложения.</li>`; return; }
  let rows = [];
  try { rows = (await native().Connections()) || []; } catch (e) { list.innerHTML = `<li class="muted">${esc(errText(e))}</li>`; return; }
  list.innerHTML = rows.map((r) => {
    const acts = r.own
      ? `<button class="btn" data-conn-toggle="${esc(r.id)}" data-on="${!r.enabled}">${r.enabled ? "Выключить" : "Включить"}</button><button class="btn" data-conn-del="${esc(r.id)}">Удалить</button>`
      : `<button class="btn" data-go-sec="${esc(r.section)}">Изменить</button>`;
    const confirm = connConfirm === r.id ? `<span class="conn-confirm">Удалить «${esc(r.name)}»? Вход сотрется из Связки ключей, закрытые ссылки перестанут читаться.
      <button class="btn" data-conn-del-yes="${esc(r.id)}">Удалить</button><button class="btn" data-conn-del-no>Отмена</button></span>` : "";
    return `<li class="${r.own && !r.enabled ? "off" : ""}">
    <span class="n">${esc(r.name)}<small>${esc(CONN_KIND[r.kind] || r.kind)}</small></span>
    <span class="st${r.ok ? " ok" : ""}">${esc(r.status)}</span>
    <span class="d">${esc(r.detail)} · использует: ${esc((r.usedBy || []).join(", "))}</span>
    <span class="conn-acts">${acts}</span>${confirm}</li>`;
  }).join("");
}
$("#conn-list").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-go-sec]");
  if (b) return openSettings(b.dataset.goSec);
  const t = e.target.closest("[data-conn-toggle]");
  const name = e.target.closest("li")?.querySelector(".n")?.firstChild?.textContent || "подключение";
  try {
    if (t) {
      const on = t.dataset.on === "true";
      await native().SetConnectionEnabled(t.dataset.connToggle, on);
      toast(name, on ? " · включено" : " · выключено", null, on ? "info" : "off");
    }
    else if (e.target.closest("[data-conn-del]")) connConfirm = e.target.closest("[data-conn-del]").dataset.connDel;
    else if (e.target.closest("[data-conn-del-no]")) connConfirm = null;
    else if (e.target.closest("[data-conn-del-yes]")) { await native().RemoveConnection(e.target.closest("[data-conn-del-yes]").dataset.connDelYes); connConfirm = null; toast("Удалено", ` · «${name}» и его ключ`, null, "del"); }
    else return;
  } catch (err) { showError(errText(err)); }
  renderConnections();
});
$("#conn-add-speech").addEventListener("click", () => {
  const f = $("#conn-speech");
  f.hidden = !f.hidden;
  $("#conn-add-speech").setAttribute("aria-expanded", String(!f.hidden));
  if (!f.hidden) $("#conn-s-url").focus();
});
$("#conn-speech").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!native() || !native().AddSpeechConnection) return void ($("#conn-s-note").textContent = "Подключения меняются в окне приложения.");
  try {
    const r = await native().AddSpeechConnection($("#conn-s-name").value, $("#conn-s-url").value, $("#conn-s-model").value, $("#conn-s-key").value);
    $("#conn-s-note").textContent = "Добавлено: " + r.name + ". Выберите его в «Чат и модель → Распознавание».";
    toast("Добавлено", ` · «${r.name}»`);
    for (const id of ["#conn-s-name", "#conn-s-url", "#conn-s-model", "#conn-s-key"]) $(id).value = "";
  } catch (err) { $("#conn-s-note").textContent = errText(err); }
  renderConnections();
});
$("#conn-add-google").addEventListener("click", () => {
  const f = $("#conn-google");
  f.hidden = !f.hidden;
  $("#conn-add-google").setAttribute("aria-expanded", String(!f.hidden));
  if (!f.hidden) $("#conn-g-id").focus();
});
$("#conn-google").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!native() || !native().ConnectGoogle) return void ($("#conn-g-note").textContent = "Подключение Google — в окне приложения.");
  const go = $("#conn-g-go");
  go.disabled = true;
  $("#conn-g-note").textContent = "Жду согласия в браузере (до 5 минут)…";
  try {
    const r = await native().ConnectGoogle($("#conn-g-id").value, $("#conn-g-secret").value);
    $("#conn-g-note").textContent = "Подключено: " + r.name;
    toast("Подключено", ` · «${r.name}»`);
    $("#conn-g-secret").value = "";
    $("#conn-google").hidden = true;
    $("#conn-add-google").setAttribute("aria-expanded", "false");
  } catch (err) {
    $("#conn-g-note").textContent = errText(err);
  } finally {
    go.disabled = false;
    renderConnections();
  }
});
// Состояние серверов приходит после списка: панель не ждет сеть. Сливается в
// черновик по id — поля состояния в сохранение не входят и dirty не трогают.
async function loadServerStates() {
  if (!native() || !native().ServerStates) return;
  const seq = ++ui.statesSeq;
  let rows, failed = false;
  try { rows = await native().ServerStates(); } catch { failed = true; }
  if (seq !== ui.statesSeq) return; // поздний ответ прежнего открытия или сохранения
  for (const s of draft.sources) {
    if (s.kind !== "server") continue;
    const r = (rows || []).find((x) => x.id === s.id);
    // Состояние относится к сохраненному адресу: строке с уже другим адресом
    // в черновике оно не подходит.
    if (r && r.url === s.url) Object.assign(s, { state: r.state, count: r.count, note: r.note, hasToken: r.hasToken });
    else if (s.state === "checking") Object.assign(s, { state: "missing", note: failed ? "состояние серверов не получено" : "" });
  }
  if (settingsOpen()) patchStates();
}

function closeSettings() {
  hideTip();
  panel.classList.remove("open");
  panel.setAttribute("aria-hidden", "true");
  scrim.hidden = true;
  ui.editing = ui.replacing = ui.confirmDel = null;
  // Про черновик источников — только при закрытии «Источников»: настройки окна
  // сохраняются сразу, им напоминание о сохранении не к месту.
  if (panel.dataset.view === "sources" && dirty()) toast("Не сохранено", " · черновик ждет в панели, ⌘,", null, "off");
  $("#q").focus();
}
const settingsOpen = () => panel.classList.contains("open");

function stateOf(s) {
  if (!s.enabled) return `<span class="state off">выключен</span>`;
  // Новый, перенацеленный или только что включенный: состояние узнается после сохранения.
  if (!s._orig || s._orig !== target(s) || !s._was) return `<span class="state new">после сохранения</span>`;
  const note = s.note ? ` data-tip="${esc(label(s))}" data-tip-b="${esc(s.note)}"` : "";
  switch (s.state) {
    case "ok":
      if (s.kind === "server")
        return `<span class="state ok"${note}><span class="sdot"></span>${fmt(s.count)} ${plural(s.count, "запись", "записи", "записей")}</span>`;
      return s.kind === "folder"
        ? `<span class="state ok"><span class="sdot"></span>${fmt(s.count)} ${plural(s.count, "файл", "файла", "файлов")}</span>`
        : `<span class="state ok"><span class="sdot"></span>${fmt(s.count)} ${plural(s.count, "запись", "записи", "записей")}</span>`;
    case "indexing":
      return `<span class="state pending"${note}><span class="sdot"></span>индексируется</span>`;
    case "checking":
      return `<span class="state pending"><span class="sdot"></span>проверяется</span>`;
    case "missing":
      return `<span class="state missing"${note}><span class="sdot"></span>нет связи</span>`;
    default:
      return `<span class="state pending"${note}><span class="sdot"></span>ждет</span>`;
  }
}

function formOf(s, i) {
  const what = s.kind === "server" ? "адрес" : s.kind === "gdrive" ? "ссылка на папку" : "путь";
  // Подключение папки Диска меняется здесь же: удаленное подключение иначе
  // не заменить без удаления папки (ревью #467).
  const conns = googleConns.some((c) => c.id === s.conn) ? googleConns : [{ id: s.conn || "", name: "нет подключения — выберите" }, ...googleConns];
  const connField = s.kind === "gdrive"
    ? `<div class="field"><label for="ec-${s._key}">подключение</label><select id="ec-${s._key}">${conns.map((c) => `<option value="${esc(c.id)}"${c.id === s.conn ? " selected" : ""}>${esc(c.name)}</option>`).join("")}</select></div>` : "";
  const pick = s.kind === "folder" && native() ? `<button class="btn" data-pick="${s._key}">Выбрать…</button>` : "";
  if (ui.editing === s._key)
    return `<div class="form">
      <div class="field"><label for="en-${s._key}">имя</label><input id="en-${s._key}" value="${esc(s.name || "")}" placeholder="${esc(label(s))}"></div>
      <div class="field"><label for="ea-${s._key}">${what}</label><input id="ea-${s._key}" value="${esc(target(s))}" spellcheck="false">${pick}</div>
      ${connField}${s.kind === "server" ? tokenField(`et-${s._key}`, s.hasToken || draft.tokens[s.id]) + checkBar(s._key) : ""}
      <div class="formbar"><button class="btn primary" data-save="${s._key}">Готово</button><button class="btn" data-cancel>Отмена</button></div></div>`;
  if (ui.replacing === s._key)
    return `<div class="form">
      <div class="field"><label for="ra-${s._key}">новый ${what}</label><input id="ra-${s._key}" placeholder="${s.kind === "server" ? "https://…" : "/Volumes/…"}" spellcheck="false">${pick}</div>
      <div class="formbar"><button class="btn primary" data-doreplace="${s._key}">Заменить</button><button class="btn" data-cancel>Отмена</button>
      <span class="muted">место ${i + 1}, имя и включенность сохранятся · было <s>${esc(tilde(target(s)))}</s></span></div></div>`;
  if (ui.confirmDel === s._key)
    return `<div class="form"><div class="formbar"><span>Убрать «${esc(label(s))}» из источников? Файлы на диске не трогаются.</span>
      <button class="btn danger" data-dodel="${s._key}">Убрать</button><button class="btn" data-cancel>Оставить</button></div></div>`;
  return "";
}

// Поле токена: сам токен сюда не возвращается никогда — только «сохранен».
// Глаз показывает только то, что набрано в поле, — чтобы человек убедился,
// что вставил тот токен. Сохраненный токен окно из Связки на страницу не
// достает: токен и данные подключений не текут из приложения.
const EYE = `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.6-6.5 10-6.5S22 12 22 12s-3.6 6.5-10 6.5S2 12 2 12z"/><circle cx="12" cy="12" r="3"/><path class="eye-slash" d="M4 4l16 16"/></svg>`;
const tokenField = (id, saved) =>
  `<div class="field"><label for="${id}">токен</label><input id="${id}" type="password" autocomplete="off" spellcheck="false"
    placeholder="${saved ? "сохранен в Связке ключей — пусто, чтобы оставить" : "токен kbengine serve --token-file"}">
    <button type="button" class="reveal" data-eye="${id}" aria-pressed="false" aria-label="Показать набранный токен" data-tip="Показать токен" data-tip-b="Только то, что набрано в поле; сохраненный остается в Связке">${EYE}</button></div>`;
const checkBar = (key) => {
  const v = ui.verdict && ui.verdict.key === key ? ui.verdict : null;
  return `<div class="formbar"><button class="btn" data-check="${key}">Проверить</button>
    <span class="check-verdict ${v ? v.state : ""}" role="status">${v ? esc(v.text) : "адрес и токен проверяются до сохранения"}</span></div>`;
};

function renderSources() {
  const n = draft.sources.length, hue = hues();
  $("#sources").innerHTML = n
    ? draft.sources.map((s, i) => {
        const open = [ui.editing, ui.replacing, ui.confirmDel].includes(s._key);
        return `<li class="src-row${s.enabled ? "" : " off"}${open ? " editing" : ""}" style="--c:var(${hue.get(s._key)})" data-key="${s._key}">
          <span class="handle" draggable="true" aria-hidden="true" data-tip="Перетащите" data-tip-b="чтобы сменить приоритет источника">⠿</span>
          <span class="prio">${i + 1}</span>
          <div class="who"><div class="name"><i class="swatch"></i><strong>${esc(label(s))}</strong><span class="kind">${esc(KIND[s.kind] || s.kind)}</span></div>
            <div class="addr" data-tip="${esc(label(s))}" data-tip-b="${esc(target(s))}">${esc(tilde(target(s)))}</div></div>
          <div class="right">${stateOf(s)}
            <button class="sw" role="switch" aria-checked="${s.enabled}" aria-label="${s.enabled ? "Выключить" : "Включить"} ${esc(label(s))}" data-toggle="${s._key}"></button></div>
          <div class="acts">
            <button data-up="${s._key}" aria-label="Выше по приоритету" ${i ? "" : "disabled"}>↑</button>
            <button data-down="${s._key}" aria-label="Ниже по приоритету" ${i < n - 1 ? "" : "disabled"}>↓</button>
            <button data-edit="${s._key}">изменить</button><button data-replace="${s._key}">заменить</button>
            <button class="danger" data-del="${s._key}">убрать</button></div>
          ${formOf(s, i)}</li>`;
      }).join("")
    : `<li class="src-row"><span></span><span></span><span class="muted">Ни одного источника — добавьте папку ниже</span></li>`;

  const kinds = Object.entries(KIND).map(([k, v]) => `<button data-kind="${k}" aria-pressed="${ui.addKind === k}">${v}</button>`).join("");
  let form = "";
  if (ui.addKind === "folder")
    form = `<div class="row">${native() ? `<button class="btn primary" data-pick="new">Выбрать папку…</button>` : ""}
      <input id="na" placeholder="или путь: ~/Documents, /Volumes/team" spellcheck="false"><button class="btn" data-addok>Добавить</button></div>
      <div class="row"><input id="ns" placeholder="или сетевая: smb://сервер/папка · Яндекс Диск: https://webdav.yandex.ru" spellcheck="false"><button class="btn" data-addshare>Подключить</button></div>
      <span class="hint">сетевая папка подключается сама после сна и перезагрузки; пароль спросит macOS и запомнит в Связке ключей (для Яндекс Диска — пароль приложения из Яндекс ID)</span>`;
  else if (ui.addKind === "catalog")
    form = `<div class="row"><input id="na" placeholder="~/…/knowledge-base/_data/catalog.json" spellcheck="false"><button class="btn" data-addok>Добавить</button></div>
      <span class="hint">читается первый включенный каталог списка</span>`;
  else if (ui.addKind === "server")
    form = `<div class="field"><label for="na">адрес</label><input id="na" placeholder="https://kb.example.ru" spellcheck="false"></div>
      ${tokenField("nt", false)}${checkBar("new")}
      <div class="row"><button class="btn primary" data-addok>Подключить</button>
      <span class="hint">http — только для этого Mac; токен уйдет в Связку ключей macOS, не в config.json</span></div>`;
  else if (ui.addKind === "gdrive")
    form = googleConns.length
      ? `<div class="field"><label for="na">ссылка на папку</label><input id="na" placeholder="https://drive.google.com/drive/folders/…" spellcheck="false"></div>
        <div class="row"><select id="nc" aria-label="Подключение Google">${googleConns.map((c) => `<option value="${esc(c.id)}">${esc(c.name)}</option>`).join("")}</select>
        <input id="nn" placeholder="название, например Тендеры" spellcheck="false"><button class="btn primary" data-addok>Добавить</button></div>
        <span class="hint">ищет сам Google Диск, файлы на Мак не скачиваются; только файлы прямо в этой папке</span>`
      : `<div class="row"><span class="muted">Сначала подключите Google — вход один на все папки Диска.</span><button class="btn" data-go-sec="conn">Подключения</button></div>`;
  dropStaleTip();
  $("#add").innerHTML = `<div class="row"><span class="muted">+ источник</span><span class="seg" role="group" aria-label="Вид источника">${kinds}</span></div>${form}`;

  renderTabSettings();
  $("#excludes").innerHTML = draft.exclude.map((r, i) =>
    `<li><span class="path">${esc(r)}</span><button data-unex="${i}" aria-label="Убрать ${esc(r)}">✕</button></li>`).join("");

  renderFooter();
}

function renderFooter() {
  const on = draft.sources.filter((s) => s.enabled);
  const lost = on.filter((s) => s.state === "missing" && s._orig === target(s)).length;
  $("#settings-note").innerHTML = dirty()
    ? `<span class="dirty">есть несохраненные изменения</span>`
    : esc(`включено ${on.length} из ${draft.sources.length}` + (lost ? ` · без связи: ${lost}` : ""));
  $("#save-settings").disabled = !native() || !dirty();
}

// patchStates обновляет только плашки состояния и подвал: полная перерисовка
// стерла бы то, что человек набирает в формах, пока серверы отвечали.
function patchStates() {
  for (const s of draft.sources) {
    const el = $(`.src-row[data-key="${s._key}"] .right .state`);
    if (el) el.outerHTML = stateOf(s);
  }
  renderFooter();
}

function showError(msg) {
  const el = $("#settings-error");
  el.textContent = msg;
  el.hidden = !msg;
}

// --- Подсказка с «Вернуть» -------------------------------------------------------
let toastT, undoFn = null;
function toast(head, rest, undo, tone = "info") {
  const t = $("#toast");
  undoFn = undo;
  t.dataset.tone = tone;
  t.classList.remove("show"); void t.offsetWidth; // перезапуск полоски
  t.innerHTML = `<span class="glow"></span><span><b>${esc(head)}</b>${esc(rest)}</span>` +
    (undo ? `<button id="undo">Вернуть</button>` : "") + `<span class="life"></span>`;
  t.classList.add("show");
  clearTimeout(toastT);
  toastT = setTimeout(() => t.classList.remove("show"), 4000);
  t.onmouseenter = () => clearTimeout(toastT);
  t.onmouseleave = () => { toastT = setTimeout(() => t.classList.remove("show"), 1200); };
}
$("#toast").addEventListener("click", (e) => {
  if (e.target.id !== "undo" || !undoFn) return;
  undoFn(); undoFn = null;
  $("#toast").classList.remove("show");
  renderSources();
});

// change снимает черновик до правки и отдает отмену для подсказки: и список,
// и введенные токены — «Отменить» не оставляет токен от отмененной правки.
function change(fn) {
  const before = draft.sources.map((s) => ({ ...s })), tokensBefore = { ...draft.tokens };
  fn();
  ui.editing = ui.replacing = ui.confirmDel = null;
  renderSources();
  return () => { draft.sources = before; draft.tokens = tokensBefore; };
}
function move(key, d) {
  const i = draft.sources.findIndex((s) => s._key === key), j = i + d;
  if (i < 0 || j < 0 || j >= draft.sources.length) return;
  const undo = change(() => { [draft.sources[i], draft.sources[j]] = [draft.sources[j], draft.sources[i]]; });
  toast(`${j + 1}-й по приоритету`, ` · «${label(draft.sources[j])}»`, undo);
  $(`[data-${d < 0 ? "up" : "down"}="${key}"]`)?.focus();
}
function setTarget(s, v) {
  if (s.kind === "server" || s.kind === "gdrive") s.url = v; else s.path = v;
}
const focusSoon = (sel) => requestAnimationFrame(() => $(sel)?.focus());

panel.addEventListener("click", async (e) => {
  // data-eye, а не data-reveal: тот же атрибут у «В Finder» в «Проблемах»
  // нес путь файла, и этот обработчик искал элемент с id-путем и падал.
  const eye = e.target.closest("[data-eye]");
  if (eye) {
    const inp = $("#" + eye.dataset.eye), show = inp.type === "password";
    inp.type = show ? "text" : "password";
    eye.setAttribute("aria-pressed", String(show));
    eye.setAttribute("aria-label", show ? "Скрыть токен" : "Показать набранный токен");
    return;
  }
  const b = e.target.closest("button");
  if (!b || !panel.contains(b)) return;
  const d = b.dataset;
  if (d.toggle) {
    const s = byKey(d.toggle);
    const undo = change(() => { s.enabled = !s.enabled; });
    return toast(label(s), s.enabled ? " включен · ищется после сохранения" : " выключен · место и настройки сохранены", undo, s.enabled ? "info" : "off");
  }
  if (d.up) return move(d.up, -1);
  if (d.down) return move(d.down, 1);
  if (d.edit) { ui.editing = d.edit; ui.replacing = ui.confirmDel = null; renderSources(); return focusSoon(`#en-${d.edit}`); }
  if (d.replace) { ui.replacing = d.replace; ui.editing = ui.confirmDel = null; renderSources(); return focusSoon(`#ra-${d.replace}`); }
  if (d.del) { ui.confirmDel = d.del; ui.editing = ui.replacing = null; renderSources(); return focusSoon(`[data-dodel="${d.del}"]`); }
  if ("cancel" in d) { ui.editing = ui.replacing = ui.confirmDel = ui.verdict = null; return renderSources(); }
  if (d.check) {
    const isNew = d.check === "new", s = isNew ? null : byKey(d.check);
    const url = (isNew ? $("#na") : $(`#ea-${s._key}`)).value.trim();
    const tok = (isNew ? $("#nt") : $(`#et-${s._key}`)).value.trim() || tokenFor(s, url);
    const typed = { url, tok, na: isNew ? $("#na").value : null, nt: isNew ? $("#nt").value : null,
      name: isNew ? null : $(`#en-${s._key}`).value };
    const seq = ++ui.checkSeq;
    ui.verdict = { key: d.check, state: "wait", text: "проверяю…" };
    renderAndKeep(d.check, typed);
    let verdict;
    try {
      const info = await native().CheckServer(s ? s.id : "", url, tok);
      verdict = { key: d.check, state: "ok", text: `● kbengine ${info.version} · ${fmt(info.entries)} ${plural(info.entries, "запись", "записи", "записей")}` };
    } catch (err) {
      verdict = { key: d.check, state: "bad", text: errText(err) };
    }
    // Поздний ответ не пишет в закрытую или уже другую форму.
    const open = isNew ? ui.addKind === "server" : ui.editing === d.check;
    if (seq !== ui.checkSeq || !open) return;
    ui.verdict = verdict;
    return renderAndKeep(d.check, typed);
  }
  if (d.save) {
    const s = byKey(d.save), name = $(`#en-${s._key}`).value.trim(), v = $(`#ea-${s._key}`).value.trim();
    if (!v) return focusSoon(`#ea-${s._key}`);
    const tok = s.kind === "server" ? $(`#et-${s._key}`).value.trim() : "";
    const conn = s.kind === "gdrive" ? $(`#ec-${s._key}`).value : s.conn;
    ui.verdict = null;
    const undo = change(() => { s.name = name; setTarget(s, v); if (s.kind === "gdrive") s.conn = conn; if (tok) draft.tokens[s.id] = { url: v, token: tok }; });
    return toast("Изменено", ` · «${label(s)}»`, undo);
  }
  if (d.doreplace) {
    const s = byKey(d.doreplace), v = $(`#ra-${s._key}`).value.trim();
    if (!v) return focusSoon(`#ra-${s._key}`);
    const old = target(s);
    const undo = change(() => setTarget(s, v));
    return toast("Заменено", ` · ${tilde(old)} → ${tilde(v)}`, undo);
  }
  if (d.dodel) {
    const i = draft.sources.findIndex((s) => s._key === d.dodel), s = draft.sources[i];
    const undo = change(() => draft.sources.splice(i, 1));
    return toast("Убран", ` · «${label(s)}», файлы на диске не тронуты`, undo, "del");
  }
  if (d.pick) {
    let p = "";
    try { p = await native().PickFolder(); } catch (err) { return showError(errText(err)); }
    if (!p) return;
    const input = d.pick === "new" ? $("#na") : $(`#ea-${d.pick}`) || $(`#ra-${d.pick}`);
    if (input) input.value = p;
    if (d.pick === "new") $("[data-addok]").click();
    return;
  }
  if (d.goSec) return openSettings(d.goSec);
  if ("addshare" in d) {
    const addr = $("#ns").value.trim();
    if (!addr) return focusSoon("#ns");
    if (!native() || !native().MountShare) return showError("Сетевые папки подключаются в окне приложения.");
    if (ui.adding) return;
    ui.adding = true;
    toast("Подключаю", " · " + addr + " — macOS может спросить пароль", null, "off");
    let vol;
    try { vol = await native().MountShare(addr); }
    catch (err) { ui.adding = false; return showError(errText(err)); }
    ui.adding = false;
    const s = { id: "", kind: "folder", name: "", path: vol, url: addr, enabled: true, _key: "k" + ++keySeq };
    const undo = change(() => draft.sources.push(s));
    ui.addKind = null;
    renderSources();
    return toast("Добавлена сетевая папка", ` · «${label(s)}», ${draft.sources.length}-я по приоритету`, undo);
  }
  if (d.kind) { ui.addKind = ui.addKind === d.kind ? null : d.kind; renderSources(); return focusSoon("#na"); }
  if ("addok" in d) {
    const v = $("#na").value.trim();
    if (!v) return focusSoon("#na");
    if (ui.adding) return; // двойной клик через await добавил бы сервер дважды
    const kind = ui.addKind;
    // Поля читаются до ожидания: за время await форма может перерисоваться.
    const tok = kind === "server" ? $("#nt").value.trim() : "";
    const s = { id: "", kind, name: "", path: "", url: "", enabled: true, _key: "k" + ++keySeq };
    if (kind === "gdrive") { s.conn = $("#nc").value; s.name = $("#nn").value.trim(); }
    setTarget(s, v);
    if (kind === "server") {
      ui.adding = true;
      // id выдает Go сразу: к нему привязан токен в Связке ключей.
      try { s.id = await native().NewSourceID("server"); }
      catch (err) { ui.adding = false; return showError("Не удалось добавить сервер: " + errText(err)); }
      ui.adding = false;
      if (ui.addKind !== "server") return; // форму сменили, пока ждали
      ui.verdict = null;
    }
    const undo = change(() => { draft.sources.push(s); if (tok) draft.tokens[s.id] = { url: v, token: tok }; });
    ui.addKind = null;
    renderSources();
    return toast("Добавлен", ` · «${label(s)}», ${draft.sources.length}-й по приоритету`, undo);
  }
  if (d.unex !== undefined) {
    const before = draft.exclude.slice(), [gone] = draft.exclude.splice(+d.unex, 1);
    toast("Снова заходит", ` · в папки «${gone}» после сохранения`, () => { draft.exclude = before; }, "off");
    return renderSources();
  }
});

// Перерисовка во время проверки сохраняет то, что человек уже набрал в форме.
function renderAndKeep(key, typed) {
  renderSources();
  if (key === "new") {
    if ($("#na")) $("#na").value = typed.na;
    if ($("#nt")) $("#nt").value = typed.nt;
  } else {
    const s = byKey(key);
    if (s && $(`#en-${key}`) && typed.name != null) $(`#en-${key}`).value = typed.name;
    if (s && $(`#ea-${key}`)) $(`#ea-${key}`).value = typed.url;
    if (s && $(`#et-${key}`) && typed.tok !== tokenFor(s, typed.url)) $(`#et-${key}`).value = typed.tok;
  }
}

// Адрес в форме правки сервера изменили — сохраненный токен ему не достанется
// (при сохранении он удалится), подсказка поля токена говорит это сразу.
panel.addEventListener("input", (e) => {
  const m = e.target.id && e.target.id.match(/^ea-(.+)$/);
  const s = m && byKey(m[1]);
  const et = s && s.kind === "server" && $(`#et-${s._key}`);
  if (!et) return;
  et.placeholder = e.target.value.trim() !== s._orig
    ? "новый адрес — введите токен для него (сохраненный удалится)"
    : s.hasToken ? "сохранен в Связке ключей — пусто, чтобы оставить" : "токен kbengine serve --token-file";
});

// Enter в поле формы — главная кнопка этой формы.
panel.addEventListener("keydown", (e) => {
  if (e.key !== "Enter" || e.target.tagName !== "INPUT") return;
  const btn = e.target.closest(".form, .add, .add-row")?.querySelector(".btn.primary:not([data-pick]), [data-addok], #add-exclude");
  if (btn) { e.preventDefault(); btn.click(); }
});

// Перетаскивание ⠿: линия над или под строкой показывает, куда встанет.
const sourcesList = $("#sources");
sourcesList.addEventListener("dragstart", (e) => {
  // Тянется только ⠿: иначе выделение текста в поле формы сдвигало строку.
  const row = e.target.closest?.(".handle") && e.target.closest(".src-row[data-key]");
  if (!row) return;
  ui.drag = row.dataset.key;
  row.classList.add("dragging");
  e.dataTransfer.effectAllowed = "move";
  e.dataTransfer.setData("text/plain", ui.drag);
});
sourcesList.addEventListener("dragover", (e) => {
  const row = e.target.closest(".src-row[data-key]");
  if (!ui.drag || !row) return;
  e.preventDefault();
  const below = e.clientY > row.getBoundingClientRect().top + row.offsetHeight / 2;
  for (const r of sourcesList.children) r.classList.remove("drop-above", "drop-below");
  row.classList.add(below ? "drop-below" : "drop-above");
  row.dataset.below = below;
});
sourcesList.addEventListener("dragend", () => {
  ui.drag = null;
  for (const r of sourcesList.children) r.classList.remove("dragging", "drop-above", "drop-below");
});
sourcesList.addEventListener("drop", (e) => {
  const row = e.target.closest(".src-row[data-key]");
  if (!ui.drag || !row) return;
  e.preventDefault();
  const key = ui.drag, below = row.dataset.below === "true";
  if (key === row.dataset.key) return;
  const undo = change(() => {
    const from = draft.sources.findIndex((s) => s._key === key);
    const [moved] = draft.sources.splice(from, 1);
    let to = draft.sources.findIndex((s) => s._key === row.dataset.key);
    if (below) to++;
    draft.sources.splice(to, 0, moved);
  });
  const at = draft.sources.findIndex((s) => s._key === key);
  toast(`${at + 1}-й по приоритету`, ` · «${label(draft.sources[at])}»`, undo);
});

$("#open-settings").onclick = () => openSettings("sources");
$("#open-prefs").onclick = () => openSettings("prefs");
$("#close-settings").onclick = closeSettings;
scrim.onclick = closeSettings;
$("#add-exclude").onclick = () => {
  const v = $("#exclude-input").value.trim();
  if (v && !draft.exclude.includes(v)) {
    const before = draft.exclude.slice();
    draft.exclude.push(v);
    toast("Не заходить", ` · в папки «${v}» после сохранения`, () => { draft.exclude = before; });
  }
  $("#exclude-input").value = "";
  renderSources();
};

// Пока идут сохранение и переиндексация, панель заперта: правка посреди нее
// пропала бы при перечитывании, а вторая кнопка запустила бы второе сохранение.
const setBusy = (on) => { panel.classList.toggle("busy", on); panel.inert = on; };
$("#save-settings").onclick = async () => {
  showError("");
  const btn = $("#save-settings");
  btn.disabled = true;
  setBusy(true);
  const p = payload();
  try {
    // Проверку делает Go: новая папка обязана существовать, иначе отказ с ее
    // именем и без изменений — ни конфига, ни индекса.
    await native().SetSources(p.sources, p.exclude, liveTokens());
  } catch (e) {
    setBusy(false);
    showError(errText(e));
    btn.disabled = false;
    return;
  }
  $("#settings-note").innerHTML = `<span class="building"></span>Переиндексация…`;
  $("#indexed").innerHTML = `<span class="building"></span>переиндексация`;
  try {
    for (;;) {
      const st = await native().IndexStatus();
      if (!st.building) {
        if (st.error) showError("Индекс не собрался: " + st.error);
        else hideIndexingScreen(); // если первая сборка падала, ее экран больше не нужен
        await refreshProblems(true);
        setIndexed(st.indexed, st.skipped);
        break;
      }
      await sleep(400);
    }
    load(await native().Sources()); // id новых источников и свежий счет
    // Поиск — после обновления списка: у нового источника уже есть цвет.
    if (state.q) search(state.q);
    renderSources();
    loadServerStates();
    toast("Сохранено", " · " + $("#settings-note").textContent);
  } catch (e) {
    showError("Не удалось узнать статус индекса: " + errText(e));
    if (state.q) search(state.q); // сохранение прошло — выдача по новым источникам
    renderSources();
  } finally {
    setBusy(false); // панель не остается запертой ни при каком исходе
  }
};

// Esc в поле правки отменяет правку — и только ее: второй Esc закроет панель
// или спрячет окно. Иначе закрытие сняло бы фокус, и сохранение по уходу из
// поля записало бы ровно то, от чего человек отказался. Стоит раньше Esc
// панели: слушатели перехвата одного узла идут в порядке добавления.
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape" || e.isComposing || !(e.target instanceof Element)) return;
  const tab = e.target.closest("[data-tab-name]"), fact = e.target.closest("[data-mem-edit]");
  const queued = memState.qedit && e.target.closest("[data-mem-qtext]"), sugg = chatState.memNew?.edit && e.target.closest("[data-sugg-text]");
  if (!tab && !fact && !queued && !sugg) return;
  e.preventDefault(); e.stopImmediatePropagation();
  if (tab) { tab.value = state.tabs[Number(tab.dataset.tabName)].name; tab.blur(); }
  if (fact) { const f = factById(fact.dataset.memEdit); if (f) fact.textContent = f.text; fact.blur(); }
  if (queued) { memState.qedit = ""; renderPortrait(); }
  if (sugg) { chatState.memNew.edit = ""; renderChat(); }
}, true);
// Esc закрывает панель раньше, чем очищает поиск; ⌘, открывает ее, как в любом приложении macOS.
document.addEventListener("keydown", (e) => {
  if (settingsOpen() && e.key === "Escape") { e.stopImmediatePropagation(); closeSettings(); }
  else if (e.metaKey && e.key === ",") { e.preventDefault(); e.stopImmediatePropagation(); openSettings("sources"); }
}, true);

// --- Вкладки поиска в панели ----------------------------------------------------
// Правка сохраняется сразу (SetTabs, без переиндексации) и сразу видна в
// полосе. В браузере (dev-режим) раздел только показывает набор.
function renderTabSettings() {
  // Имя печатается в поле — не перерисовывать список под рукой: ввод пропал бы.
  if ($("#tab-list").contains(document.activeElement) && document.activeElement.matches("input")) return;
  const editable = !!native(), n = state.tabs.length;
  const what = (t) => {
    if (!visibleTab(t)) return `<span class="what gone">из новой версии окна</span>`;
    if (t.kind === "type") return `<span class="what">тип · ${esc(TYPE_NAME[t.value])}</span>`;
    const src = sourceOf(t);
    return src ? `<span class="what">источник · ${esc(label(src))}</span>` : `<span class="what gone">источник убран</span>`;
  };
  $("#tab-list").innerHTML = `<li class="fixed"><span></span><span>Все</span><span class="what">всегда первая</span><span></span></li>` +
    state.tabs.map((t, i) => `<li data-tab-i="${i}">
      <span class="handle" draggable="${editable}" aria-hidden="true" data-tip="Перетащите" data-tip-b="чтобы сменить порядок вкладок">⠿</span>
      <input value="${esc(t.name)}" data-tab-name="${i}" aria-label="Имя вкладки" ${editable && visibleTab(t) ? "" : "disabled"}>
      ${what(t)}
      <span class="tab-acts">
        <button data-tab-up="${i}" aria-label="Выше" ${editable && i ? "" : "disabled"}>↑</button>
        <button data-tab-down="${i}" aria-label="Ниже" ${editable && i < n - 1 ? "" : "disabled"}>↓</button>
        <button class="del" data-tab-del="${i}" aria-label="Убрать вкладку ${esc(t.name)}" ${editable ? "" : "disabled"}>✕</button>
      </span></li>`).join("");
  const types = Object.keys(TYPE_NAME).filter((v) => !state.tabs.some((t) => t.kind === "type" && t.value === v));
  $("#tab-add-type").innerHTML = `<option value="">по типу…</option>` + types.map((v) => `<option value="${v}">${esc(TYPE_NAME[v])}</option>`).join("");
  const srcs = state.known.filter((x) => !state.tabs.some((t) => t.kind === "source" && t.value === x.id));
  $("#tab-add-source").innerHTML = `<option value="">по источнику…</option>` + srcs.map((x) => `<option value="${esc(x.id)}">${esc(label(x))}</option>`).join("");
  $("#tab-add-type").disabled = !editable || !types.length;
  $("#tab-add-source").disabled = !editable || !srcs.length;
  $("#tab-reset").disabled = !editable || JSON.stringify(state.tabs) === JSON.stringify(DEFAULT_TABS);
  dropStaleTip();
}
// Сохранения идут строго по очереди: Wails зовет привязки параллельно, и
// быстрые ↓↓ могли бы прийти в Go в обратном порядке. При отказе набор
// перечитывается из окна — откат к «прежнему» стер бы уже сохраненную правку.
let tabSaves = Promise.resolve();
function saveTabs(next) {
  state.tabs = next;
  refreshTabs();
  renderTabSettings();
  tabSaves = tabSaves.then(() => native().SetTabs(next)).then(() => showError("")).catch(async (e) => {
    showError("Вкладки не сохранены: " + errText(e));
    try { state.tabs = (await native().Tabs()) || []; } catch { /* остается то, что на экране */ }
    refreshTabs();
    renderTabSettings();
  });
}
// Имена «+ по типу» — из набора по умолчанию: одна копия, а не вторая.
const TYPE_TITLE = Object.fromEntries(DEFAULT_TABS.map((t) => [t.value, t.name]));
$("#tab-list").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  const next = state.tabs.slice(), before = state.tabs.slice();
  let head, rest, tone;
  if (b.dataset.tabDel !== undefined) { const [gone] = next.splice(Number(b.dataset.tabDel), 1); head = "Вкладка убрана"; rest = ` · «${gone.name}»`; tone = "del"; }
  else if (b.dataset.tabUp !== undefined) { const i = Number(b.dataset.tabUp); [next[i - 1], next[i]] = [next[i], next[i - 1]]; head = `${i + 1}-я вкладка`; rest = ` · «${next[i - 1].name}»`; }
  else if (b.dataset.tabDown !== undefined) { const i = Number(b.dataset.tabDown); [next[i], next[i + 1]] = [next[i + 1], next[i]]; head = `${i + 3}-я вкладка`; rest = ` · «${next[i + 1].name}»`; }
  else return;
  saveTabs(next);
  toast(head, rest, () => saveTabs(before), tone);
});
// Имя — по уходу из поля или Enter; пустое имя не сохраняется, поле
// возвращает прежнее.
$("#tab-list").addEventListener("change", (e) => {
  const inp = e.target.closest("[data-tab-name]");
  if (!inp) return;
  const i = Number(inp.dataset.tabName), name = inp.value.trim();
  if (!name) { inp.value = state.tabs[i].name; return; }
  if (name === state.tabs[i].name) return;
  const before = state.tabs.slice(), old = state.tabs[i].name;
  saveTabs(state.tabs.map((t, k) => (k === i ? { ...t, name } : t)));
  toast("Переименовано", ` · «${old}» → «${name}»`, () => saveTabs(before));
});
$("#tab-list").addEventListener("keydown", (e) => { if (e.key === "Enter" && e.target.matches("[data-tab-name]")) e.target.blur(); });
// «По умолчанию» — набор DEFAULT_TABS; прежний возвращается из подсказки.
$("#tab-reset").addEventListener("click", () => {
  const before = state.tabs;
  saveTabs(DEFAULT_TABS.map((t) => ({ ...t })));
  toast("Вкладки по умолчанию", " · прежний набор можно вернуть", () => saveTabs(before));
});
// Перетаскивание ⠿, как у источников: линия над или под строкой показывает,
// куда встанет вкладка; «Вернуть» в подсказке — прежний порядок.
const tabList = $("#tab-list");
let tabDrag = null;
tabList.addEventListener("dragstart", (e) => {
  const row = e.target.closest?.(".handle") && e.target.closest("li[data-tab-i]");
  if (!row || !native()) return;
  tabDrag = Number(row.dataset.tabI);
  row.classList.add("dragging");
  e.dataTransfer.effectAllowed = "move";
  e.dataTransfer.setData("text/plain", String(tabDrag));
});
tabList.addEventListener("dragover", (e) => {
  const row = e.target.closest("li[data-tab-i]");
  if (tabDrag === null || !row) return;
  e.preventDefault();
  const below = e.clientY > row.getBoundingClientRect().top + row.offsetHeight / 2;
  for (const r of tabList.children) r.classList.remove("drop-above", "drop-below");
  row.classList.add(below ? "drop-below" : "drop-above");
  row.dataset.below = below;
});
tabList.addEventListener("dragend", () => {
  tabDrag = null;
  for (const r of tabList.children) r.classList.remove("dragging", "drop-above", "drop-below");
});
tabList.addEventListener("drop", (e) => {
  const row = e.target.closest("li[data-tab-i]");
  if (tabDrag === null || !row) return;
  e.preventDefault();
  const from = tabDrag, over = Number(row.dataset.tabI), below = row.dataset.below === "true";
  if (from === over) return;
  const before = state.tabs, next = before.slice();
  const [moved] = next.splice(from, 1);
  let to = next.indexOf(before[over]);
  if (below) to++;
  next.splice(to, 0, moved);
  saveTabs(next);
  toast(`${to + 2}-я вкладка`, ` · «${moved.name}»`, () => saveTabs(before));
});
$("#tab-add-type").addEventListener("change", (e) => {
  const v = e.target.value;
  if (!v) return;
  const before = state.tabs.slice();
  saveTabs([...state.tabs, { name: TYPE_TITLE[v], kind: "type", value: v }]);
  toast("Вкладка добавлена", ` · «${TYPE_TITLE[v]}»`, () => saveTabs(before));
});
$("#tab-add-source").addEventListener("change", (e) => {
  const s = state.known.find((x) => x.id === e.target.value);
  if (!s) return;
  const before = state.tabs.slice();
  saveTabs([...state.tabs, { name: label(s), kind: "source", value: s.id }]);
  toast("Вкладка добавлена", ` · «${label(s)}»`, () => saveTabs(before));
});

function hideIndexingScreen() {
  const screen = $("#indexing");
  if (screen.hidden) return;
  screen.classList.add("leave");
  setTimeout(() => { screen.hidden = true; screen.classList.remove("leave"); }, 500);
}
$("#indexing-settings").onclick = () => openSettings("sources");

// --- Оформление: акценты спектра ----------------------------------------------
// Порядок и id — те же, что в прототипе (outputs/2026-11-06_dvk-gradients.html)
// и в блоке «Спектр акцентами» style.css: выключенный акцент — класс no-<id> на
// body. Go хранит режим и свой набор (desktopconfig.Accents), списка не знает.
const ACCENTS = [
  ["vault-edge", "Сейф: кант", "кант двери и корпуса хранилища градиентом"],
  ["vault-dial", "Сейф: диск", "кольцо кодового диска градиентом"],
  ["vault-plate", "Сейф: табличка", "название на табличке градиентом"],
  ["vault-glow", "Сейф: свет", "свет внутри, когда дверь открывается"],
  ["tab-ink", "Черта вкладки", "линия под активной кнопкой шапки и рамка активной вкладки поиска"],
  ["search", "Поле поиска в фокусе", "рамка строки dvk find и курсор"],
  ["row", "Выбранная строка", "рамка по контуру вместо полосы слева"],
  ["head", "Слово HEAD", "метка над поиском"],
  ["buttons", "Главные кнопки", "Открыть источник, Подключить, Сохранить"],
  ["chip", "Нажатый чип источника", "рамка от цвета источника"],
  ["indexing", "Индексация", "бегущая полоса и большое число"],
  ["panel-title", "Заголовок панели", "слово и верхний край панели"],
  ["marks", "Найденные слова", "подчеркивание совпадений"],
  ["tab-name", "Имя активной вкладки", "текст активной кнопки шапки и вкладки поиска"],
  ["dollar", "Знак $", "в строке поиска и командах"],
  ["bar-edge", "Нижний край шапки", "тонкая линия под шапкой"],
  ["branch", "Линия ветки", "от готового узла к HEAD"],
  ["pulse", "Точка статуса", "вращается у счета файлов"],
  ["count", "Счет в поиске", "число найденного в поле"],
  ["preview-line", "Черта шапки превью", "под заголовком документа"],
  ["icons", "Иконки в шапке", "папка и шестеренка"],
  ["scrollbar", "Ползунок прокрутки", "в выдаче и превью"],
  ["switch", "Переключатель", "включенный тумблер"],
  ["tip", "Подсказка", "рамка всплывающей подсказки"],
  ["hover", "Наведение на строку", "мягкий перелив слева"],
  ["chat-send", "Кнопка «Спросить»", "главная кнопка чата"],
  ["chat-field", "Поле вопроса в фокусе", "рамка поля чата"],
  ["chat-active", "Открытый чат", "перелив в колонке чатов"],
  ["chat-grip", "Край колонки чата", "линия, за которую тянут ширину"],
  ["chat-user", "Ваш вопрос", "рамка сообщения в чате"],
  ["chat-pane", "Значки колонок", "нажатые значки чатов и документов"],
  ["chat-new", "Новый чат", "плюс в колонке чатов"],
  ["chat-docs", "Счет документов", "число документов чата"],
  ["chat-thinking", "Ожидание ответа", "бегущая полоса, пока модель думает"],
  ["chat-title", "Заголовок чата", "название в шапке разговора"],
  ["st-money", "Шкала расхода", "полоса «потрачено» в Деньгах статистики"],
];
const ACCENT_IDS = ACCENTS.map((a) => a[0]);
state.accents = { mode: "", off: [] }; // режим "" — твой выбор: все, кроме off
// offNow — какие акценты выключены сейчас, с учетом режима.
const offNow = (a) => (a.mode === "all" ? [] : a.mode === "none" ? ACCENT_IDS : (a.off || []));
function applyAccents() {
  const off = offNow(state.accents);
  for (const id of ACCENT_IDS) document.body.classList.toggle("no-" + id, off.includes(id));
}
function renderAccents() {
  const a = state.accents, off = offNow(a), editable = !!native();
  $("#accent-mine-n").textContent = ACCENT_IDS.filter((id) => !(a.off || []).includes(id)).length;
  $("#accent-all-n").textContent = ACCENT_IDS.length;
  for (const b of document.querySelectorAll("#accent-presets [data-preset]")) {
    b.setAttribute("aria-pressed", (b.dataset.preset === "mine" ? "" : b.dataset.preset) === (a.mode || ""));
    b.disabled = !editable;
  }
  $("#accent-list").innerHTML = ACCENTS.map(([id, t, w]) => {
    const on = !off.includes(id);
    return `<li><span class="t">${esc(t)}</span><span class="w">${esc(w)}</span>
      <button class="sw" role="switch" aria-checked="${on}" aria-label="${on ? "Выключить" : "Включить"}: ${esc(t)}" data-accent="${id}" ${editable ? "" : "disabled"}></button></li>`;
  }).join("");
  renderLookAccess();
}
// Сохранения по очереди, как у вкладок; при отказе — перечитать из окна.
let accentSaves = Promise.resolve();
function saveAccents(next) {
  state.accents = next;
  applyAccents();
  renderAccents();
  accentSaves = accentSaves.then(() => native().SetAccents(next)).then(() => showError("")).catch(async (e) => {
    showError("Оформление не сохранено: " + errText(e));
    try { state.accents = await native().Accents(); } catch { /* остается то, что на экране */ }
    applyAccents();
    renderAccents();
  });
}
$("#accent-presets").addEventListener("click", (e) => {
  const b = e.target.closest("[data-preset]");
  if (!b || !native()) return;
  const mode = b.dataset.preset === "mine" ? "" : b.dataset.preset;
  saveAccents({ ...state.accents, mode, off: state.accents.off || [] }); // look (выбор значений) не теряется
  // Кнопки «Сохранить» у оформления нет: правка сохраняется сразу — об этом и плашка.
  toast("Сохранено", " · оформление: " + b.textContent.trim());
});
// Щелчок по переключателю правит твой выбор от того, что видно сейчас, и
// включает его: человек меняет то, что видит.
$("#accent-list").addEventListener("click", (e) => {
  const b = e.target.closest("[data-accent]");
  if (!b || !native()) return;
  const id = b.dataset.accent, off = offNow(state.accents), before = { ...state.accents };
  const on = off.includes(id), what = (ACCENTS.find(([x]) => x === id) || [, id])[1];
  saveAccents({ ...state.accents, mode: "", off: on ? off.filter((x) => x !== id) : [...off, id] });
  // Отмена возвращает только переключатели: выбор значений (look) мог поменяться после.
  toast(on ? "Включено" : "Выключено", ` · ${what}`, () => saveAccents({ ...state.accents, mode: before.mode, off: before.off }), on ? "info" : "off");
});
if (native()) native().Accents().then((a) => { state.accents = a || { mode: "", off: [] }; applyAccents(); }).catch(() => {});

// --- Модули окна (#435, G4) -----------------------------------------------------
// showModule opens a module in place of the search (id) or returns to the search
// (null), and reports whether a module is open. Фрейм грузится при первом
// открытии и дальше живет: вкладка туда-обратно не перезагружает витрину.
function showModule(id) {
  const mod = id && MODULES[id];
  // Подсказка графика не переживает уход с вкладки; stTipHide объявлен ниже и при первом вызове еще недоступен.
  const stTipNow = document.querySelector(".st-tip"); if (stTipNow) stTipNow.hidden = true;
  $("#app").classList.toggle("module-open", !!mod);
  $("#module-view").hidden = !mod || !!mod.view;
  $("#chat-view").hidden = !mod || mod.view !== "chat";
  $("#stats-view").hidden = !mod || mod.view !== "stats";
  $("#access-view").hidden = !mod || mod.view !== "access";
  if (!mod || mod.view !== "access") acLeave();
  if (mod && mod.view === "chat") openChatView();
  else if (mod && mod.view === "stats") openStats();
  else if (mod && mod.view === "access") openAccess();
  else if (mod) {
    const frame = $("#module-frame");
    if (frame.dataset.src !== mod.src) { frame.src = mod.src; frame.dataset.src = mod.src; frame.title = mod.name; }
  }
  return !!mod;
}
function renderModules() {
  const editable = !!native();
  $("#module-list").innerHTML = Object.entries(MODULES).map(([id, m]) => {
    const on = state.modules.some((x) => x.id === id && x.enabled);
    return `<li><span class="t">${esc(m.name)}</span><span class="w">${esc(m.what)}</span>
      <button class="sw" role="switch" aria-checked="${on}" aria-label="${on ? "Выключить" : "Включить"}: ${esc(m.name)}" data-module="${id}" ${editable ? "" : "disabled"}></button></li>`;
  }).join("");
}
// Сохранения по очереди, как у вкладок; при отказе — перечитать из окна.
let moduleSaves = Promise.resolve();
$("#module-list").addEventListener("click", (e) => {
  const b = e.target.closest("[data-module]");
  if (!b || !native()) return;
  const id = b.dataset.module, on = !state.modules.some((x) => x.id === id && x.enabled);
  state.modules = [...state.modules.filter((x) => x.id !== id), { id, enabled: on }];
  if (!on && moduleOf(state.tab) === id) state.tab = "all"; // выключенный модуль не остается открытым
  renderModules();
  refreshTabs();
  toast(MODULES[id].name, on ? " · вкладка рядом с поиском" : " · выключен, данные на сервере не тронуты", null, on ? "info" : "off");
  moduleSaves = moduleSaves.then(() => native().SetModules([{ id, enabled: on }])).then(() => showError("")).catch(async (err) => {
    showError("Модуль не сохранен: " + errText(err));
    try { state.modules = (await native().Modules()) || []; } catch { /* остается то, что на экране */ }
    renderModules();
    refreshTabs();
  });
});
if (native()) native().Modules().then((m) => { state.modules = m || []; refreshTabs(); }).catch(() => {});

// --- Проблемы (кнопка «подробнее» в строке статуса, #456) --------------------
state.problems = null; // null — окно еще не ответило (или это браузер без окна)
let problemsAt = 0, problemsSeq = 0;
// refreshProblems updates state.problems; строку рисует вызывающий. Без force
// — не чаще раза в 30 с и без опроса серверов: окно берет их последний ответ,
// в сеть и Связку ключей не ходит. force (открытие панели, сохранение)
// опрашивает серверы. Поздний ответ прежнего вызова не затирает новый.
async function refreshProblems(force) {
  if (!native() || !native().Problems || (!force && Date.now() - problemsAt < 30000)) return;
  problemsAt = Date.now();
  const seq = ++problemsSeq;
  try {
    const v = await native().Problems(!!force);
    if (seq === problemsSeq) state.problems = v;
  } catch { /* прежний список остается */ }
}
// midTrim обрезает длинное имя посередине: видны начало и расширение, полное
// имя — в подсказке.
const midTrim = (s, max = 44) => (s.length <= max ? s : s.slice(0, Math.ceil((max - 1) * 0.6)) + "…" + s.slice(-Math.floor((max - 1) * 0.4)));
// renderProblems groups by source, then by reason: у шести одинаковых файлов
// одна причина и один совет, а не шесть.
function renderProblems() {
  const view = $("#problems-view");
  if (!state.problems) {
    view.innerHTML = `<p class="muted">${native() ? "Проверяю источники…" : "Проблемы источников видны в окне приложения."}</p>`;
    return;
  }
  const { total, problems } = state.problems;
  if (!total) {
    view.innerHTML = `<p class="muted">Проблем нет: все включенные источники читаются.</p>`;
    return;
  }
  if (!problems.length) {
    view.innerHTML = `<p class="muted">${fmt(total)} ${plural(total, "файл не прочитан", "файла не прочитаны", "файлов не прочитаны")}. Их имена появятся после следующей сборки индекса.</p>`;
    return;
  }
  const bySource = new Map();
  for (const p of problems) {
    const key = p.source + "\0" + p.name;
    if (!bySource.has(key)) bySource.set(key, { name: p.name, reasons: new Map() });
    const reasons = bySource.get(key).reasons;
    reasons.set(p.what, [...(reasons.get(p.what) || []), p]);
  }
  const files = problems.filter((p) => p.path).length;
  const unnamed = total - problems.length; // файлы сверх первых 100
  view.innerHTML = `<p class="muted">${fmt(total)} ${plural(total, "проблема", "проблемы", "проблем")}${unnamed > 0 ? ` · названо ${fmt(files)} файлов из ${fmt(files + unnamed)}` : ""}${files ? ". Нажмите файл — исходная ошибка" : ""}.</p>` +
    [...bySource.values()].map(({ name, reasons }) => `<section class="problem-source"><h3 class="problem-source-name">${esc(name)}</h3>` +
      [...reasons].map(([what, list]) => {
        const todo = list[0].todo;
        const head = `<h4><span class="lvl" data-level="${esc(list[0].level)}"></span>${esc(what.charAt(0).toUpperCase() + what.slice(1))}${list[0].path ? ` · ${list.length}` : ""}</h4>${todo ? `<p class="problem-todo">Что сделать: ${esc(todo)}</p>` : ""}`;
        // Беда источника целиком (сервер, каталог, папка, индекс): под советом
        // сразу исходная ошибка, без списка.
        if (!list[0].path) return `<div class="skip-group">${head}${list.map((p) => (p.detail ? `<code class="problem-detail">${esc(p.detail)}</code>` : "")).join("")}</div>`;
        return `<div class="skip-group">${head}<ul class="skip-list">${list.map((f) => {
          const fname = f.path.split("/").pop(), dir = f.path.slice(0, -fname.length - 1);
          return `<li><details><summary><span class="skip-name" data-tip="${esc(fname)}" data-tip-b="${esc(tilde(f.path))}">${esc(midTrim(fname))}</span>
            <span class="skip-dir">${esc(tilde(dir))}</span>
            <button class="btn" data-reveal="${esc(f.path)}" ${native() ? "" : "disabled"}>В Finder</button></summary>
            <code class="skip-detail">${esc(f.detail)}</code></details></li>`;
        }).join("")}</ul></div>`;
      }).join("") + `</section>`).join("");
  dropStaleTip();
}
$("#problems-view").addEventListener("click", (e) => {
  const b = e.target.closest("[data-reveal]");
  if (!b) return;
  e.preventDefault(); // кнопка внутри <summary> не раскрывает строку
  revealFile(b.dataset.reveal);
});

// --- Чат над документами (#436) -------------------------------------------------
// Как в Claude Desktop (решение владельца 06.11): значок панели в шапке
// показывает и прячет колонку чатов и колонку документов целиком; край
// колонки тянется и прилипает к удобным ширинам. Ответ ждется до 3 минут —
// режим «модель ищет» делает несколько запросов к модели подряд.
const chatState = { id: "", chat: null, busy: false, partial: "", settings: null, opened: false, list: [], attached: [], only: null, moreOpen: false, voices: [], portraitMode: {}, over: null, memNew: null };
// memState — портрет «Что DVK помнит обо мне» (#479), как его отдало окно.
const memState = { p: null, qedit: "", where: "", err: "" };
async function openChatView() {
  if (!native() || !native().Ask) {
    $("#chat-log").innerHTML = `<p class="muted">Чат работает в окне приложения, не в браузере.</p>`;
    return;
  }
  if (!chatState.opened) {
    chatState.opened = true;
    await loadChatSettings();
  }
  await loadPortrait(); // размер портрета — для предупреждения в начале нового чата
  await renderChatList();
  renderChat();
  renderChatSources();
  $("#chat-q").focus();
}

// --- Ряд источников над полем (решение владельца 06.11) ---------------------------
// В чем ищет этот чат: включенные источники окна плашками их цвета. Щелчок
// убирает источник из этого чата или возвращает; общие настройки не меняются.
// Плашка пунктиром с красной точкой — у источника ошибка (сервер не отвечает,
// нет токена, папка пропала): видно, что подключено, а что нет.
// chatOnly null — все; список хранится в чате и уходит с вопросом.
// Функции, а не const: строка статуса зовет renderChatSources раньше, чем
// исполнится этот кусок файла.
function chatHues() { return hues(state.known, (s) => s.id); }
function chatSources() { return state.known.filter((s) => s.enabled); }
function chatSrcOn(id) { return !chatState.only || chatState.only.includes(id); }
function chatSrcBad(id) {
  const p = state.problems && (state.problems.problems || []).find((x) => x.source === id && !x.path && x.level === "error");
  return p ? p.what + (p.todo ? " · " + p.todo : "") : "";
}
function renderChatSources() {
  const row = $("#chat-srcs");
  const list = chatSources();
  row.hidden = !list.length;
  if (!list.length) return;
  const hue = chatHues();
  const chip = (s) => {
    const bad = chatSrcBad(s.id), on = chatSrcOn(s.id);
    return `<button type="button" class="chat-src${bad ? " bad" : ""}" style="--c:var(${hue.get(s.id)})" data-chat-src="${esc(s.id)}" aria-pressed="${on}"
      data-tip="${esc(label(s))}" data-tip-b="${esc(bad || (on ? "этот чат ищет здесь · щелчок — убрать" : "этот чат здесь не ищет · щелчок — вернуть"))}"${bad ? " data-tip-bad" : ""}><i></i><span class="t">${esc(label(s))}</span></button>`;
  };
  const max = 6; // больше — в «ещё N»: ряд не переносится
  const shown = list.slice(0, max), rest = list.slice(max);
  const restOn = rest.filter((s) => chatSrcOn(s.id)).length;
  row.innerHTML = `<span class="lbl">ищет в</span>` + shown.map(chip).join("") +
    (rest.length ? `<button type="button" class="chat-src" data-chat-more aria-expanded="${chatState.moreOpen}">ещё ${rest.length}${chatState.only ? ` · ${restOn} вкл` : ""}</button>` : "") +
    (chatState.only ? `<button type="button" class="chat-srcs-reset" data-chat-src-all>все</button>` : "") +
    (chatState.moreOpen && rest.length ? `<div class="chat-srcs-more">${rest.map((s) => `<label style="--c:var(${hue.get(s.id)})"><input type="checkbox" data-chat-src-cb="${esc(s.id)}"${chatSrcOn(s.id) ? " checked" : ""}><i></i>${esc(label(s))}${chatSrcBad(s.id) ? ' <span class="muted">· ошибка</span>' : ""}</label>`).join("")}</div>` : "");
  dropStaleTip();
}
function toggleChatSrc(id) {
  const all = chatSources().map((s) => s.id);
  let only = chatState.only ? [...chatState.only] : [...all];
  only = only.includes(id) ? only.filter((x) => x !== id) : [...only, id];
  if (!only.length) return showChatNote("Хотя бы один источник: иначе чату негде искать.");
  chatState.only = all.every((x) => only.includes(x)) ? null : only;
  renderChatSources();
}
$("#chat-srcs").addEventListener("click", (e) => {
  const b = e.target.closest("[data-chat-src]");
  if (b) return toggleChatSrc(b.dataset.chatSrc);
  if (e.target.closest("[data-chat-src-all]")) { chatState.only = null; return renderChatSources(); }
  if (e.target.closest("[data-chat-more]")) { chatState.moreOpen = !chatState.moreOpen; renderChatSources(); }
});
$("#chat-srcs").addEventListener("change", (e) => {
  const cb = e.target.closest("[data-chat-src-cb]");
  if (cb) { chatState.moreOpen = true; toggleChatSrc(cb.dataset.chatSrcCb); }
});
document.addEventListener("click", (e) => {
  // isConnected: щелчок внутри ряда перерисовал его, цель уже вне документа.
  if (chatState.moreOpen && e.target.isConnected && !e.target.closest("#chat-srcs")) { chatState.moreOpen = false; renderChatSources(); }
});
async function loadChatSettings() {
  try { chatState.settings = await native().ChatSettings(); } catch (e) { chatState.settings = null; showChatNote("Настройки чата не прочитаны: " + errText(e)); return; }
  const s = chatState.settings;
  $("#chat-url").value = s.url || "";
  $("#chat-name").value = s.model || "";
  $("#chat-mode").value = s.mode || "";
  $("#chat-mic-mode").value = s.mic || "";
  renderMicChoices();
  setMic(dict.on);
  $("#chat-key").value = "";
  $("#chat-key").placeholder = s.hasKey ? "ключ в Связке ключей — пусто, чтобы оставить" : s.local ? "для модели на этом Маке не нужен" : "ключ провайдера";
  renderChatNote();
  $("#chat-model-chip").textContent = (s.model || "").replace(/^[^/]+\//, "");
  renderMemSettings();
  renderAccValues();
}
// renderVoiceSettings: кто слушает (Apple Speech, без сети ли) и каким голосом
// читать ответ.
async function renderVoiceSettings() {
  if (!native() || !native().ListenStatus) { $("#chat-listen-note").textContent = "Голос настраивается в окне приложения."; return; }
  chatState.listenStatus = await native().ListenStatus().catch(() => ({}));
  chatState.speechConns = ((await native().Connections().catch(() => [])) || []).filter((c) => c.kind === "speech");
  $("#chat-listen").value = (chatState.settings && chatState.settings.listen) || "";
  renderListenChoices();
  chatState.voices = (await native().Voices().catch(() => [])) || [];
  chatState.cloudVoices = (native().CloudVoices && (await native().CloudVoices().catch(() => []))) || [];
  $("#chat-accent").value = (chatState.settings && chatState.settings.accent) || "";
  $("#chat-voice-name").value = (chatState.settings && chatState.settings.voice) || "";
  renderVoiceChoices();
  renderAccValues();
}
// voiceTag names the quality from the macOS voice name: Premium и Enhanced
// (и их русские подписи) звучат живее обычных.
const voiceTag = (v) => (/premium|преми/i.test(v) ? "премиум" : /enhanced|улучш/i.test(v) ? "улучшенный" : "");
// renderVoiceChoices: кто читает (Mac или облако) и голос плашками, как в
// english-hub (#478); выбранное — в скрытом поле формы, сохраняется сразу.
const ENGINE_NOTE = {
  mac: "Голоса macOS, без сети и без денег.",
  cloud: "Gemini через Vercel AI Gateway — около 0,2 цента за реплику, «дороже» — OpenAI, около 0,8 цента. Ключ — ключ шлюза из «Модель».",
};
const isCloud = (v) => /^(gemini|openai):/.test(v || "");
function renderVoiceChoices() {
  const cur = $("#chat-voice-name").value, cloud = isCloud(cur);
  $("#chat-engines").querySelectorAll("[data-engine]").forEach((b) => b.setAttribute("aria-pressed", String((b.dataset.engine === "cloud") === cloud)));
  let all;
  if (cloud) {
    all = (chatState.cloudVoices || []).map((v) => [v.id, v.label, v.pricey ? "дороже" : v.tag]);
  } else {
    const list = chatState.voices || [];
    all = [["", "голос системы", ""], ...list.map((v) => [v, v.replace(/\s*\(.*\)$/, ""), voiceTag(v)])];
    if (cur && !list.includes(cur)) all.push([cur, cur + " — нет на этом Маке", ""]);
  }
  $("#chat-voices").innerHTML = all.map(([v, name, tag]) =>
    `<button type="button" class="btn" data-voice="${esc(v)}" aria-pressed="${v === cur}">${esc(name)}${tag ? `<small>${esc(tag)}</small>` : ""}</button>`).join("");
  $("#chat-voice-note").textContent = ENGINE_NOTE[cloud ? "cloud" : "mac"];
  $("#voice-add-row").hidden = cloud;
  const acc = $("#chat-accent").value;
  $("#chat-accents").querySelectorAll("[data-accent-v]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.accentV === acc)));
  $("#chat-accent-note").textContent = acc
    ? `Английский ответ читается с ${acc === "en-GB" ? "британским" : "американским"} произношением${cloud ? "" : " — английским голосом Mac"}; русский — как обычно.`
    : "Русский ответ читает выбранный голос, английский — тоже, без подсказки произношения.";
}
// renderListenChoices: распознавание плашками, как в english-hub (#478):
// Mac, облако через шлюз (быстрее и дешевле или точнее) и свои серверы.
const CLOUD_LISTEN = { "cloud:mini": ["Облако", "gpt-4o-mini-transcribe через Vercel AI Gateway: около 0,3 цента за минуту речи, лучше с терминами и английскими словами."], "cloud:best": ["Облако: gpt-4o (дороже)", "gpt-4o-transcribe через Vercel AI Gateway: около 0,6 цента за минуту, точнее на шуме и быстрой речи."] };
function renderListenChoices() {
  const cur = $("#chat-listen").value, st = chatState.listenStatus || {}, conns = chatState.speechConns || [];
  const all = [["", "Mac (бесплатно)", true], ...Object.entries(CLOUD_LISTEN).map(([v, [n]]) => [v, n, true]), ...conns.map((c) => [c.id, c.name, c.enabled])];
  if (cur && !all.some(([v]) => v === cur)) all.push([cur, "удаленный сервер", false]);
  $("#chat-listens").innerHTML = all.map(([v, n, on]) => `<button type="button" class="btn" data-listen="${esc(v)}" aria-pressed="${v === cur}" ${on ? "" : "disabled"}>${esc(n)}${on ? "" : "<small>выключено</small>"}</button>`).join("");
  const AUTH = { denied: "macOS запретил распознавание речи: Системные настройки → Конфиденциальность → Распознавание речи → DVK", restricted: "распознавание речи закрыто политикой Мака", notDetermined: "macOS спросит разрешение на микрофон и распознавание при первой диктовке" };
  const conn = conns.find((c) => c.id === cur);
  $("#chat-listen-note").textContent = CLOUD_LISTEN[cur] ? CLOUD_LISTEN[cur][1]
    : conn ? conn.name + ": запись диктовки уходит этому серверу."
    : !st.helper ? "Диктовка работает в собранном окне: нет помощника kb-listen."
    : `Apple Speech на этом Маке${st.onDevice ? ", без сети: речь не уходит в Apple" : ", через сервер Apple"}.` + (AUTH[st.auth] ? " " + AUTH[st.auth] + "." : "");
}
const MIC_MODES = {
  hold: ["Удерживать", "Держите кнопку микрофона (или пробел на ней) и говорите, отпустили — фраза ушла."],
  "": ["Нажал - нажал", "Нажали — слушает, нажали еще раз — фраза ушла. Удобно для длинных мыслей."],
  auto: ["Слушает сам", "Нажали один раз — микрофон слушает весь разговор, пауза отправляет фразу. Еще раз или Esc — стоп."],
};
// renderMicChoices: режим микрофона плашками; хранится в скрытом поле формы.
function renderMicChoices() {
  const cur = $("#chat-mic-mode").value;
  $("#chat-mics").querySelectorAll("[data-mic]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.mic === cur)));
  $("#chat-mic-note").textContent = (MIC_MODES[cur] || MIC_MODES[""])[1];
}
// renderAccValues: в заголовке раздела — выбранное, чтобы видеть все, не раскрывая.
function renderAccValues() {
  $("#acc-v-model").textContent = ($("#chat-name").value || $("#chat-name").placeholder).replace(/^[^/]+\//, "");
  const v = $("#chat-voice-name").value;
  $("#acc-v-voice").textContent = isCloud(v) ? v.split(":")[1].replace(/^./, (c) => c.toUpperCase()) + " · облако" : (v ? v.replace(/\s*\(.*\)$/, "") : "голос системы") + " · Mac";
  $("#acc-v-mic").textContent = (MIC_MODES[$("#chat-mic-mode").value] || MIC_MODES[""])[0];
  const lb = $("#chat-listens").querySelector('[aria-pressed="true"]');
  $("#acc-v-listen").textContent = lb ? lb.firstChild.textContent : "Mac (бесплатно)";
  const p = memState.p;
  $("#acc-v-mem").textContent = !memOn(p) ? "не включено" : `${approvedFacts().length}` + (pendingFacts().length ? ` · ${pendingFacts().length} ждут` : "");
}
// renderChatNote says aloud where the documents go (ADR-0006).
function renderChatNote() {
  const s = chatState.settings;
  if (!s) return;
  const who = s.effectiveMode === "tools" ? "модель сама ищет и читает документы" : "окно находит документы и отдает их модели";
  // Портрет идет в каждый чат — человек видит, что он уходит туда же, куда документы.
  const what = memOn(memState.p) ? "документы и портрет" : "документы";
  showChatNote(s.local
    ? `${s.model} на этом Маке: ${what} никуда не уходят · ${who}`
    : `${s.model}: найденные ${what} уходят провайдеру модели · ${who}`);
}
const showChatNote = (t) => ($("#chat-note").textContent = t);
// chatWhen is a short date for the tooltip: сегодня — время, иначе день.
function chatWhen(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toDateString() === new Date().toDateString()
    ? d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleDateString("ru-RU", { day: "numeric", month: "short" });
}
async function renderChatList() {
  try { chatState.list = (await native().Chats()) || []; } catch { chatState.list = []; }
  paintChatList();
}
function paintChatList() {
  const q = ($("#chat-find").hidden ? "" : $("#chat-find").value).trim().toLowerCase();
  const list = chatState.list.filter((c) => !q || c.title.toLowerCase().includes(q));
  // Удаление стирает историю с диска насовсем — сначала вопрос в строке, как у
  // подключений: «×» рядом с названием легко задеть.
  $("#chat-list").innerHTML = list.map((c) => c.id === chatState.confirmDel
    ? `<li class="chat-confirm"><span>Удалить «${esc(c.title)}»? История сотрется.</span>
      <button class="btn danger" data-del-yes="${esc(c.id)}">Удалить</button><button class="btn" data-del-no>Отмена</button></li>`
    : `<li><button class="chat-item${c.id === chatState.id ? " on" : ""}" data-chat="${esc(c.id)}" data-tip="${esc(c.title)}" data-tip-b="${esc(chatWhen(c.updated))}">${esc(c.title)}</button>
      <button class="chat-del" data-del="${esc(c.id)}" aria-label="Удалить чат: ${esc(c.title)}" data-tip="Удалить чат">×</button></li>`).join("") ||
    `<li class="muted chat-empty" style="padding:7px 10px">${q ? "Чатов с таким названием нет" : "Чатов пока нет"}</li>`;
  dropStaleTip();
}
// answerHTML shows the model's text: абзацы, **жирный**, `код` — без HTML модели.
function answerHTML(t) {
  return esc(t)
    .replace(/\*\*(.+?)\*\*/g, "<b>$1</b>")
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\n/g, "<br>");
}
function renderChat() {
  const c = chatState.chat;
  const log = $("#chat-log");
  $("#chat-title").textContent = (c && c.title) || "Новый чат";
  if (!c) {
    // Портрет не помещается — сказать в начале нового чата, до вопроса
    // (прототип 06.11, вариант A): иначе первый ответ уйдет без портрета.
    const p = memState.p;
    if (!chatState.busy && memOn(p) && p.size > p.budget && !chatState.portraitMode[""] && !(chatState.over && chatState.over.chat === ""))
      chatState.over = { chat: "", size: p.size, budget: p.budget, props: null, start: true };
    log.innerHTML = memInChat() + `<div class="chat-hello"><p>Спросите о своих записях и файлах.</p><p class="muted">Ответ — со ссылками на документы; к чату можно вернуться в колонке чатов.</p></div>`;
  } else {
    log.innerHTML = c.messages.map((m) => m.role === "user"
      ? `<div class="msg user">${esc(m.text)}</div>`
      : `<div class="msg bot">${answerHTML(m.text)}</div>`).join("");
  }
  if (c && !chatState.busy) log.insertAdjacentHTML("beforeend", memInChat());
  if (chatState.busy) {
    log.insertAdjacentHTML("beforeend", `<div class="msg bot thinking" id="chat-partial"></div>`);
    paintPartial();
  }
  log.scrollTop = log.scrollHeight;
  const docs = (c && c.docs) || [];
  $("#chat-docs-n").textContent = docs.length || "";
  $("#chat-docs").innerHTML = docs.map((d) => {
    const file = d.ref.startsWith("file:") ? d.ref.slice(5) : "";
    const link = d.ref.startsWith("url:") ? d.ref.slice(4) : "";
    const hue = d.src && chatHues().get(d.src);
    return `<li><span class="chat-doc-title" data-tip="${esc(d.title)}" data-tip-b="${esc(file ? tilde(file) : d.source || "")}">${hue ? `<span class="chat-doc-dot" style="--c:var(${hue})"></span>` : ""}${esc(d.title)}</span>
      <span class="muted chat-doc-src">${esc(file ? tilde(d.source || "") : d.source || "")}</span>
      ${file ? `<span class="chat-doc-acts"><button class="btn" data-open="${esc(file)}">Открыть</button><button class="btn" data-reveal-file="${esc(file)}">В Finder</button></span>` : ""}
      ${link ? `<span class="chat-doc-acts"><button class="btn" data-open-url="${esc(link)}">Открыть</button></span>` : ""}</li>`;
  }).join("") || `<li class="muted">Документы появятся после первого ответа.</li>`;
  dropStaleTip();
}
// Ответ печатается по мере генерации: окно шлет текст, накопленный к куску
// (chat:delta), пустой — показанное оказалось не ответом («сейчас поищу»).
function paintPartial() {
  const el = $("#chat-partial");
  if (!el) return;
  const t = chatState.partial;
  // Внизу ли человек — до обновления: длинный кусок иначе сам «уводит» вверх.
  const log = $("#chat-log");
  const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
  el.classList.toggle("streaming", !!t);
  el.innerHTML = t ? answerHTML(t) : `<span class="building"></span>ищу в документах и думаю…`;
  if (atBottom) log.scrollTop = log.scrollHeight; // не уводить, если человек листает вверх
}
if (window.runtime) window.runtime.EventsOn("chat:delta", (t) => {
  if (!chatState.busy) return;
  chatState.partial = t || "";
  paintPartial();
});
async function askChat() {
  const q = $("#chat-q").value.trim();
  if (!q || chatState.busy) return;
  chatState.busy = true;
  chatState.partial = "";
  $("#chat-send").disabled = true;
  // Вопрос виден сразу, до ответа.
  chatState.chat = chatState.chat ? { ...chatState.chat, messages: [...chatState.chat.messages, { role: "user", text: q }] } : { title: q, messages: [{ role: "user", text: q }], docs: [] };
  $("#chat-q").value = "";
  fitChatQ();
  renderChat();
  try {
    const sent = chatState.attached.map((a) => a.ref);
    const mode = chatState.portraitMode[chatState.id] || "";
    const c = await native().Ask({ id: chatState.id, question: q, refs: sent, sources: chatState.only || [], portrait: mode });
    if (mode) chatState.portraitMode[c.id] = mode; // новый чат получил id
    if (!chatState.id) delete chatState.portraitMode[""]; // выбор до вопроса — только для этого чата
    const pn = c.portrait;
    chatState.over = pn && pn.over && !pn.used ? { chat: c.id, size: pn.size, budget: pn.budget, props: null } : null;
    // Убрать только отправленное: приложенное, пока модель думала, — к следующему вопросу.
    chatState.attached = chatState.attached.filter((a) => !sent.includes(a.ref));
    renderAttached();
    const last = c.messages[c.messages.length - 1];
    if (voice.on && last) speakAnswer(last.text);
    if (c.notes && c.notes.length) setTimeout(() => showChatNote("Не прочитано: " + c.notes.join(" · ")), 0);
    chatState.id = c.id;
    chatState.chat = c;
    await renderChatList();
  } catch (e) {
    chatState.chat.messages.pop();
    if (!chatState.chat.messages.length) chatState.chat = null;
    $("#chat-q").value = q; // вопрос не теряется
    micLoop.on = false;
    stopVoice(); // до заметки: остановка возвращает обычную подпись под полем
    showChatNote("Нет ответа: " + errText(e));
  } finally {
    chatState.busy = false;
    fitChatQ();
    renderChat();
  }
  // «Слушает сам»: ответ пришел — снова слушать (с голосом это делает разговор голосом).
  if (micLoop.on && !voice.on) startMic(VOICE_PAUSE, true);
}
function newChat() {
  if (chatState.busy) return;
  chatState.id = "";
  chatState.chat = null;
  chatState.over = null;
  loadPortrait().then(() => { if (!chatState.chat && !chatState.busy) renderChat(); });
  chatState.attached = [];
  chatState.only = null;
  renderAttached();
  renderChatSources();
  paintChatList();
  renderChat();
  renderChatNote();
  $("#chat-q").focus();
}
$("#chat-ask").addEventListener("submit", (e) => { e.preventDefault(); askChat(); });
// Поле растет по тексту до 40% высоты; кнопка «Спросить» — только с вопросом.
function fitChatQ() {
  const q = $("#chat-q");
  q.style.height = "auto";
  q.style.height = q.scrollHeight + "px";
  $("#chat-send").disabled = chatState.busy || !q.value.trim();
  q.closest(".chat-box").classList.toggle("has-text", !!q.value.trim() || chatState.busy);
}
$("#chat-q").addEventListener("input", () => { fitChatQ(); renderAttached(); });

// --- Диктовка (#436) ----------------------------------------------------------------
// Помощник kb-listen (Apple Speech, на этом Маке) шлет текст сказанного целиком
// на сейчас; он встает после того, что было в поле до начала диктовки.
const dict = { on: false, base: "", send: false };
// Режим микрофона из настроек (#478, как в english-hub): "" — нажал-нажал,
// второе нажатие отправляет; hold — держите кнопку, отпустили — ушло; auto —
// слушает сам: пауза отправляет, после ответа слушает снова.
const micMode = () => (chatState.settings && chatState.settings.mic) || "";
const micLoop = { on: false };
const MIC_TIP = { "": "нажмите и говорите, еще раз — фраза уйдет · Esc — стоп", hold: "держите и говорите, отпустили — фраза ушла", auto: "слушает сам: пауза отправляет фразу · еще раз или Esc — стоп" };
function setMic(on) {
  dict.on = on;
  $("#chat-mic").setAttribute("aria-pressed", String(on || micLoop.on));
  $("#chat-mic").dataset.tipB = on ? "слушаю · " + MIC_TIP[micMode()] : MIC_TIP[micMode()];
}
// startMic listens; send — договоренное уходит вопросом само.
async function startMic(pause, send) {
  if (!native() || !native().StartDictation) return showChatNote("Диктовка работает в окне приложения.");
  if (dict.on || chatState.busy) return;
  dict.base = $("#chat-q").value.replace(/\s+$/, "");
  dict.send = send;
  // pause 0 — без паузы: Wails требует ровно столько аргументов, сколько у метода Go (ревью #465).
  try { await native().StartDictation(pause); setMic(true); } catch (e) { micLoop.on = false; setMic(false); showChatNote("Диктовка: " + errText(e)); }
}
function stopMic(send) {
  if (!dict.on) return;
  dict.send = send;
  native().StopDictation();
}
$("#chat-mic").addEventListener("click", () => {
  hideTip();
  if (voice.on) stopVoice();
  const m = micMode();
  if (m === "hold") return; // держат — нажатие и отпускание ниже
  if (m === "auto") {
    if (micLoop.on || dict.on) { micLoop.on = false; stopMic(false); setMic(dict.on); return; }
    micLoop.on = true;
    startMic(VOICE_PAUSE, true);
    return;
  }
  dict.on ? stopMic(true) : startMic(0, false);
});
// Удерживать: мышью или пробелом/Enter на кнопке; отпустили — фраза ушла.
// holding: отпустили раньше, чем диктовка успела начаться, — остановить сразу.
let holding = false;
async function holdStart() {
  if (micMode() !== "hold" || holding) return;
  holding = true;
  if (voice.on) stopVoice();
  await startMic(0, false);
  if (!holding) stopMic(true);
}
function holdEnd() {
  if (micMode() !== "hold" || !holding) return;
  holding = false;
  stopMic(true);
}
$("#chat-mic").addEventListener("pointerdown", (e) => { if (e.button === 0) holdStart(); });
["pointerup", "pointerleave", "pointercancel"].forEach((t) => $("#chat-mic").addEventListener(t, holdEnd));
$("#chat-mic").addEventListener("keydown", (e) => { if ((e.key === " " || e.key === "Enter") && micMode() === "hold") { e.preventDefault(); if (!e.repeat) holdStart(); } });
$("#chat-mic").addEventListener("keyup", (e) => { if (e.key === " " || e.key === "Enter") holdEnd(); });
if (window.runtime) window.runtime.EventsOn("dictation", (ev) => {
  if (ev.error) { micLoop.on = false; if (voice.on) stopVoice(); showChatNote("Диктовка: " + ev.error); }
  if (ev.busy) showChatNote("Диктовка: распознаю запись…");
  if (ev.text !== undefined && ev.text !== "") {
    $("#chat-q").value = (dict.base ? dict.base + " " : "") + ev.text;
    fitChatQ();
    renderAttached();
  }
  if (ev.final) {
    const send = dict.send && !ev.error;
    dict.send = false;
    setMic(false);
    $("#chat-q").focus();
    // Разговор без рук: вопрос договорен — уходит сам; тишина без слов — слушать снова.
    if (voice.on && !ev.error) $("#chat-q").value.trim() ? askChat() : listenVoice();
    else if (send && $("#chat-q").value.trim()) askChat();
    else if (micLoop.on && !ev.error) startMic(VOICE_PAUSE, true);
  }
});

// --- Разговор голосом без рук (#436) ---------------------------------------------
// Слушает с паузой: тишина 1,2 с после речи — вопрос договорен и уходит сам;
// ответ читается вслух голосом из настроек; дочитал — снова слушает. Кнопка
// или Esc останавливают все сразу.
const voice = { on: false };
const VOICE_PAUSE = 1.2;
function setVoice(on) {
  voice.on = on;
  $("#chat-voice").setAttribute("aria-pressed", String(on));
  $("#chat-voice").closest(".chat-box").classList.toggle("voice-on", on);
}
async function listenVoice(keepDraft) {
  if (!voice.on || chatState.busy) return;
  // Первый запуск сохраняет набранное — голос его дописывает; дальше поле чистое.
  dict.base = keepDraft ? $("#chat-q").value.replace(/\s+$/, "") : "";
  if (!keepDraft) $("#chat-q").value = "";
  fitChatQ();
  try { await native().StartDictation(VOICE_PAUSE); setMic(true); showChatNote("Разговор голосом: говорите — после паузы вопрос уйдет сам · Esc — стоп"); }
  catch (e) { showChatNote("Разговор голосом: " + errText(e)); stopVoice(); }
}
function stopVoice() {
  if (!voice.on) return;
  setVoice(false);
  if (dict.on) native().StopDictation();
  native().StopSpeaking();
  renderChatNote();
}
$("#chat-voice").addEventListener("click", () => {
  hideTip();
  if (!native() || !native().StartDictation) return showChatNote("Разговор голосом работает в окне приложения.");
  if (voice.on) return stopVoice();
  micLoop.on = false;
  setVoice(true);
  listenVoice(true);
});
// Ответ готов — прочитать; дочитал — слушать снова.
function speakAnswer(text) {
  if (!voice.on) return;
  showChatNote("Разговор голосом: читаю ответ · Esc — стоп");
  native().Speak(text).catch((e) => { showChatNote("Голос: " + errText(e)); stopVoice(); });
}
if (window.runtime) window.runtime.EventsOn("speech", () => { if (voice.on) listenVoice(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && voice.on) { e.preventDefault(); stopVoice(); } }, true);

// --- Приложенное к вопросу (#436) -------------------------------------------------
// «+»: файл с диска, найденное, ссылка. Ссылки из текста вопроса окно читает
// само — их плашки пунктиром, убрать их можно только из текста.
const LINK_RE = /https?:\/\/[^\s<>"«»]+/g;
function questionLinks() {
  const out = [];
  for (const m of $("#chat-q").value.match(LINK_RE) || []) {
    const l = m.replace(/[.,;:!?)»'"]+$/, "");
    if (!out.includes(l) && !chatState.attached.some((a) => a.ref === "url:" + l)) out.push(l);
  }
  return out;
}
function linkTitle(l) {
  try { const u = new URL(l); return u.host + (u.pathname.length > 1 ? u.pathname.slice(0, 28) + (u.pathname.length > 28 ? "…" : "") : ""); } catch { return l; }
}
function renderAttached() {
  const box = $("#chat-attached");
  const auto = questionLinks().map((l) => `<span class="chat-att auto" data-tip="Окно прочтет ссылку из вопроса" data-tip-b="${esc(l)}"><span class="t">${esc(linkTitle(l))}</span><span class="w">ссылка из вопроса</span></span>`);
  const mine = chatState.attached.map((a, i) => `<span class="chat-att" data-tip="${esc(a.title)}" data-tip-b="${esc(a.hint || "")}"><span class="t">${esc(a.title)}</span><span class="w">${esc(a.via)}</span><button type="button" data-unattach="${i}" aria-label="Убрать: ${esc(a.title)}">×</button></span>`);
  box.innerHTML = [...mine, ...auto].join("");
  box.hidden = !box.innerHTML;
}
function addAttached(a) {
  if (!chatState.attached.some((x) => x.ref === a.ref)) chatState.attached.push(a);
  renderAttached();
  closeAttachMenu();
  $("#chat-q").focus();
}
function closeAttachMenu() {
  $("#chat-attach-menu").hidden = true;
  $("#chat-attach-pick").hidden = true;
  $("#chat-attach").setAttribute("aria-expanded", "false");
}
let attachMode = "";
$("#chat-attach").addEventListener("click", () => {
  const m = $("#chat-attach-menu");
  if (!m.hidden) return closeAttachMenu();
  m.hidden = false;
  $("#chat-attach-pick").hidden = true;
  $("#chat-attach").setAttribute("aria-expanded", "true");
  hideTip();
  m.querySelector("button").focus();
});
$("#chat-attach-menu").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-attach]");
  if (b) {
    attachMode = b.dataset.attach;
    if (attachMode === "file") {
      closeAttachMenu();
      try {
        const p = await native().PickChatFile();
        if (p) addAttached({ ref: "file:" + p, title: p.split("/").pop(), via: "с диска", hint: tilde(p) });
      } catch (err) { showChatNote("Файл не выбран: " + errText(err)); }
      return;
    }
    const inp = $("#chat-attach-input");
    $("#chat-attach-pick").hidden = false;
    $("#chat-attach-found").innerHTML = attachMode === "link" ? `<li class="muted" style="padding:4px 8px">Enter — приложить. Google-документ открыт по ссылке или приложите файл.</li>` : "";
    inp.value = "";
    inp.placeholder = attachMode === "link" ? "https://…" : "найти в своих источниках…";
    inp.focus();
    return;
  }
  const f = e.target.closest("[data-found]");
  if (f) addAttached(JSON.parse(f.dataset.found));
});
let findTimer = 0;
$("#chat-attach-input").addEventListener("input", (e) => {
  if (attachMode !== "found") return;
  clearTimeout(findTimer);
  const q = e.target.value.trim();
  findTimer = setTimeout(async () => {
    let docs = [];
    try { docs = (await native().ChatFind(q)) || []; } catch (err) { $("#chat-attach-found").innerHTML = `<li class="muted" style="padding:4px 8px">${esc(errText(err))}</li>`; return; }
    if (q !== $("#chat-attach-input").value.trim()) return; // пока искали, запрос сменился
    $("#chat-attach-found").innerHTML = docs.map((d) => {
      const a = { ref: d.ref, title: d.title, via: d.source || "найдено", hint: d.ref.startsWith("file:") ? tilde(d.ref.slice(5)) : d.source };
      return `<li><button type="button" data-found="${esc(JSON.stringify(a))}"><span>${esc(d.title)}</span><span class="s">${esc(d.source || "")}</span></button></li>`;
    }).join("") || (q ? `<li class="muted" style="padding:4px 8px">Ничего не нашлось</li>` : "");
  }, 200);
});
$("#chat-attach-input").addEventListener("keydown", (e) => {
  if (e.key === "Escape") { e.preventDefault(); closeAttachMenu(); $("#chat-attach").focus(); return; }
  if (e.key !== "Enter") return;
  e.preventDefault();
  if (attachMode === "found") { const first = $("#chat-attach-found [data-found]"); if (first) first.click(); return; }
  const v = e.target.value.trim();
  if (!/^https?:\/\/\S+$/.test(v)) { $("#chat-attach-found").innerHTML = `<li class="muted" style="padding:4px 8px">Нужна ссылка, начинающаяся с http:// или https://</li>`; return; }
  addAttached({ ref: "url:" + v, title: linkTitle(v), via: "ссылка", hint: v });
});
$("#chat-attached").addEventListener("click", (e) => {
  const b = e.target.closest("[data-unattach]");
  if (!b) return;
  chatState.attached.splice(+b.dataset.unattach, 1);
  renderAttached();
});
document.addEventListener("click", (e) => {
  if (!$("#chat-attach-menu").hidden && !e.target.closest("#chat-attach-menu, #chat-attach")) closeAttachMenu();
  const u = e.target.closest("[data-open-url]");
  if (u) openURL(u.dataset.openUrl);
});
$("#chat-model-chip").addEventListener("click", () => openSettings("chat"));
$("#chat-q").addEventListener("keydown", (e) => {
  if (e.key === "Escape" && (dict.on || micLoop.on)) { e.preventDefault(); micLoop.on = false; stopMic(false); setMic(dict.on); return; }
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); askChat(); }
});
$("#chat-new").addEventListener("click", newChat);
$("#chat-find-btn").addEventListener("click", () => {
  const f = $("#chat-find");
  f.hidden = !f.hidden;
  $("#chat-find-btn").setAttribute("aria-expanded", String(!f.hidden));
  if (!f.hidden) f.focus(); else { f.value = ""; paintChatList(); }
});
$("#chat-find").addEventListener("input", paintChatList);
$("#chat-sources-btn").addEventListener("click", () => openSettings("sources"));
document.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n" && !$("#chat-view").hidden) { e.preventDefault(); newChat(); }
});
$("#chat-list").addEventListener("click", async (e) => {
  const ask = e.target.closest("[data-del]");
  if (ask && !chatState.busy) {
    chatState.confirmDel = ask.dataset.del;
    paintChatList();
    $("[data-del-no]")?.focus(); // случайный Enter не стирает историю
    return;
  }
  if (e.target.closest("[data-del-no]")) {
    chatState.confirmDel = null;
    return paintChatList();
  }
  const yes = e.target.closest("[data-del-yes]");
  if (yes && !chatState.busy) {
    const id = yes.dataset.delYes;
    chatState.confirmDel = null;
    const title = (chatState.list.find((c) => c.id === id) || {}).title || "чат";
    try { await native().DeleteChat(id); } catch (err) { paintChatList(); showChatNote("Чат не удален: " + errText(err)); return; }
    toast("Чат удален", ` · «${title}»`, null, "del");
    if (id === chatState.id) { chatState.id = ""; chatState.chat = null; }
    await renderChatList();
    renderChat();
    return;
  }
  const b = e.target.closest("[data-chat]");
  if (!b || chatState.busy) return;
  try {
    chatState.chat = await native().Chat(b.dataset.chat);
    chatState.id = b.dataset.chat;
    // Источники чата — только те, что есть сейчас: удаленный или выключенный
    // оставил бы чат искать нигде (ревью #465).
    const alive = (chatState.chat.sources || []).filter((id) => chatSources().some((s) => s.id === id));
    chatState.only = alive.length ? alive : null;
    renderChatSources();
  } catch (err) { showChatNote("Чат не открылся: " + errText(err)); return; }
  paintChatList();
  renderChat();
  renderChatNote();
});
$("#chat-docs").addEventListener("click", (e) => {
  const o = e.target.closest("[data-open]");
  if (o) return void openFile(o.dataset.open);
  const r = e.target.closest("[data-reveal-file]");
  if (r) revealFile(r.dataset.revealFile);
});
$("#chat-model-btn").addEventListener("click", () => openSettings("chat"));
$(".settings-nav").addEventListener("click", (e) => {
  const b = e.target.closest("[data-go]");
  if (b && b.dataset.go !== panel.dataset.view) openSettings(b.dataset.go);
});
// saveChatPart saves one choice right away (голос, микрофон, распознавание) —
// как в english-hub, без кнопки; правки модели и ключа в полях не трогает.
async function saveChatPart(patch, head, rest) {
  const s = chatState.settings;
  if (!s || !native() || !native().SetChatSettings) return;
  const { hasKey, local, effectiveMode, ...cfg } = s; // в сохранение — только настройки, без признаков
  const before = { ...cfg }, next = { ...cfg, ...patch };
  try {
    await native().SetChatSettings(next, "");
    chatState.settings = { ...s, ...patch };
    toast(head, rest, () => saveChatPart(before, "Вернул", " · как было"));
    // Где лежит портрет поменялось (и при «Вернуть» тоже) — портрет с нового места.
    if ("memoryDir" in patch || "memoryServer" in patch) await loadPortrait();
  } catch (err) {
    $("#chat-model-note").textContent = errText(err);
  }
  $("#chat-voice-name").value = chatState.settings.voice || "";
  $("#chat-accent").value = chatState.settings.accent || "";
  $("#chat-mic-mode").value = chatState.settings.mic || "";
  $("#chat-listen").value = chatState.settings.listen || "";
  renderListenChoices();
  renderVoiceChoices();
  renderMicChoices();
  renderMemSettings();
  renderAccValues();
  setMic(dict.on);
}
$("#chat-voices").addEventListener("click", (e) => {
  const b = e.target.closest("[data-voice]");
  if (!b) return;
  const v = b.dataset.voice;
  if (native() && native().PreviewVoice) native().PreviewVoice(v).catch((err) => toast("Голос не прозвучал", " · " + errText(err), null, "del"));
  saveChatPart({ voice: v }, "Голос", " · " + (isCloud(v) ? v.split(":")[1] + ", облако" : v || "голос системы"));
});
$("#chat-engines").addEventListener("click", (e) => {
  const b = e.target.closest("[data-engine]");
  if (!b || (b.dataset.engine === "cloud") === isCloud($("#chat-voice-name").value)) return;
  const v = b.dataset.engine === "cloud" ? ((chatState.cloudVoices || [])[0] || {}).id || "" : "";
  if (native() && native().PreviewVoice) native().PreviewVoice(v).catch((err) => toast("Голос не прозвучал", " · " + errText(err), null, "del"));
  saveChatPart({ voice: v }, "Кто читает", b.dataset.engine === "cloud" ? " · облако, голос " + v.split(":")[1] : " · Mac, голос системы");
});
$("#chat-accents").addEventListener("click", (e) => {
  const b = e.target.closest("[data-accent-v]");
  if (!b) return;
  saveChatPart({ accent: b.dataset.accentV }, "Акцент", " · " + b.textContent);
});
$("#chat-mics").addEventListener("click", (e) => {
  const b = e.target.closest("[data-mic]");
  if (!b) return;
  saveChatPart({ mic: b.dataset.mic }, "Микрофон", " · " + MIC_MODES[b.dataset.mic][0]);
});
$("#voice-add").addEventListener("click", () => {
  if (native() && native().OpenVoiceSettings) native().OpenVoiceSettings().catch((err) => { $("#chat-model-note").textContent = errText(err); });
});
$("#chat-name").addEventListener("input", renderAccValues);
$("#chat-listens").addEventListener("click", (e) => {
  const b = e.target.closest("[data-listen]");
  if (!b || b.disabled) return;
  saveChatPart({ listen: b.dataset.listen }, "Распознавание", " · " + b.firstChild.textContent);
});
$("#chat-model").addEventListener("submit", async (e) => {
  e.preventDefault();
  // Прочие настройки (акцент, портрет) — как сохранены: SetChatSettings
  // заменяет настройки чата целиком.
  const { hasKey, local, effectiveMode, ...saved } = chatState.settings || {};
  const c = { ...saved, url: $("#chat-url").value.trim(), model: $("#chat-name").value.trim(), mode: $("#chat-mode").value, voice: $("#chat-voice-name").value, listen: $("#chat-listen").value, mic: $("#chat-mic-mode").value };
  try {
    await native().SetChatSettings(c, $("#chat-key").value);
    $("#chat-model-note").textContent = "";
    toast("Сохранено", " · модель " + (c.model || $("#chat-name").placeholder).replace(/^[^/]+\//, ""));
    await loadChatSettings();
  } catch (err) {
    $("#chat-model-note").textContent = errText(err);
  }
});

// --- Портрет «Что DVK помнит обо мне» (#479, прототип 06.11, везде A) -------------
// Окно хранит портрет в папке человека; новое из чатов ждет одобрения; каждое
// изменение — подсказкой, где можно — с «Вернуть».
const memOn = (p) => !!(p && (p.dir || p.server));
const memServers = () => state.known.filter((s) => s.kind === "server");
const approvedFacts = () => ((memState.p && memState.p.facts) || []).filter((f) => !f.pending);
const pendingFacts = () => ((memState.p && memState.p.facts) || []).filter((f) => f.pending);
async function loadPortrait() {
  try { return await loadPortraitOnly(); } finally { renderChatNote(); } // подпись под чатом знает, ведется ли портрет
}
async function loadPortraitOnly() {
  if (!native() || !native().Portrait) return;
  if (native().Sources) state.known = ((await native().Sources().catch(() => null)) || { sources: state.known }).sources || [];
  memState.err = "";
  try { memState.p = await native().Portrait(); } catch (e) { memState.p = null; memState.err = errText(e); }
  renderMemSettings();
  renderAccValues();
}
// renderMemSettings — где лежит портрет: папка на этом Маке или kb-сервер из
// «Источников» (тем же токеном); выбранный сервер главнее папки.
function renderMemSettings() {
  const s = chatState.settings || {};
  const dir = s.memoryDir || "", srv = s.memoryServer || "";
  const where = memState.where || (srv ? "server" : "local");
  $("#mem-where").querySelectorAll("[data-mem-where]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.memWhere === where)));
  $("#mem-local").hidden = where !== "local";
  $("#mem-servers").hidden = where !== "server";
  $("#mem-dir").textContent = dir ? tilde(dir) + "/portrait.json" : "папка не выбрана";
  $("#mem-pick").textContent = dir ? "Другая папка…" : "Выбрать папку…";
  const servers = memServers();
  $("#mem-servers").innerHTML = servers.map((x) => `<button type="button" class="btn" data-mem-server="${esc(x.id)}" aria-pressed="${x.id === srv}">${esc(label(x))}</button>`).join("");
  const gone = srv && !servers.some((x) => x.id === srv);
  $("#mem-dir-note").textContent = memState.err || (where === "server"
    ? !servers.length ? "kb-серверов в «Источниках» нет — добавьте свой, и портрет будет на нем."
      : gone ? "kb-сервера портрета больше нет в «Источниках» — выберите другой."
      : srv ? "Рядом с Вашей базой, вход Вашим токеном. На любом устройстве с этим подключением портрет один и тот же."
      : "Выберите kb-сервер — портрет переедет туда не сам: факты из папки останутся в ней."
    : dir ? "Файл с правами 0600: читать его может только Ваш пользователь macOS. Папка в iCloud Drive — и портрет будет на всех Ваших Маках."
      : "Выберите папку — и DVK начнет помнить о Вас. Пока папки нет, портрет не ведется.");
  $("#mem-privacy").textContent = s.local
    ? "Модель на этом Маке: портрет никуда не уходит."
    : "С облачной моделью одобренный портрет уходит ее провайдеру в каждом чате, вместе с вопросом. Разбор ответов отправляет туда же вопрос, ответ и портрет.";
  $("#mem-suggest").checked = !s.memoryQuiet;
  $("#mem-extract").checked = !s.memoryOff;
  for (const id of ["#mem-suggest", "#mem-extract"]) $(id).disabled = !(dir || srv);
  renderMemCard();
}
$("#mem-where").addEventListener("click", (e) => {
  const b = e.target.closest("[data-mem-where]");
  if (!b) return;
  memState.where = b.dataset.memWhere;
  const s = chatState.settings || {};
  if (memState.where === "local" && s.memoryServer) {
    memState.where = "";
    return void saveChatPart({ memoryServer: "" }, "Портрет", s.memoryDir ? " · в папке " + tilde(s.memoryDir) : " · на этом Маке, выберите папку").then(loadPortrait);
  }
  renderMemSettings();
});
$("#mem-servers").addEventListener("click", (e) => {
  const b = e.target.closest("[data-mem-server]");
  if (!b) return;
  memState.where = "";
  saveChatPart({ memoryServer: b.dataset.memServer }, "Портрет", " · на kb-сервере «" + b.textContent + "»").then(loadPortrait);
});
// renderMemCard — карточка как в english-hub: последний факт, разделы, новое.
function renderMemCard() {
  const el = $("#mem-card"), p = memState.p;
  el.hidden = !memOn(p);
  if (el.hidden) return;
  const facts = approvedFacts(), queue = pendingFacts(), last = facts[facts.length - 1];
  const bySec = p.sections.map((sec) => ({ ...sec, n: facts.filter((f) => f.section === sec.id).length })).filter((x) => x.n);
  el.innerHTML = `<p class="lbl">Что DVK помнит обо мне <b>${facts.length}</b></p>
    <p class="last">${last ? esc(last.text) : "Пока ничего"}</p>
    ${bySec.length ? `<div class="mem-acts">${bySec.map((x) => `<button type="button" class="mem-soft" data-mem-open>${esc(x.title)} ${x.n}</button>`).join("")}</div>` : ""}
    ${queue.length ? `<p class="new"><b>новое:</b> ${esc(queue[0].text)}${queue.length > 1 ? ` и еще ${queue.length - 1}` : ""}</p>` : ""}
    <button type="button" class="mem-go" data-mem-open>◎ Погрузиться →</button>`;
}
// renderPortrait — экран портрета: «Ждут одобрения», ниже разделы с правкой в строке.
function renderPortrait() {
  const p = memState.p;
  if (!memOn(p)) {
    $("#mem-queue").innerHTML = `<p class="muted">Не выбрано, где лежит портрет: «Чат и модель → 05 Что DVK помнит обо мне».</p>`;
    $("#mem-facts").innerHTML = "";
    return;
  }
  const queue = pendingFacts(), facts = approvedFacts();
  $("#mem-queue").innerHTML = queue.length
    ? `<p class="label-sm mem-q-head">Ждут одобрения · ${queue.length}</p>` + queue.map((f) => `<div class="mem-q">
        <div><span class="t" data-mem-qtext="${esc(f.id)}"${memState.qedit === f.id ? ' contenteditable="true"' : ""} spellcheck="false">${esc(f.text)}</span><span class="mem-meta">${esc(f.title)} · ${esc(f.source)}</span></div>
        <div class="mem-acts"><button type="button" class="btn primary" data-mem-ok="${esc(f.id)}">Запомнить</button><button type="button" class="btn" data-mem-qedit="${esc(f.id)}">Изменить</button><button type="button" class="btn" data-mem-no="${esc(f.id)}">Не запоминать</button></div></div>`).join("")
    : `<p class="muted">Новых фактов нет. Все, что DVK узнает из чатов, сначала появится здесь.</p>`;
  $("#mem-facts").innerHTML = p.sections.map((sec) => `<div class="mem-sec"><h4>${esc(sec.title)}</h4>
    ${facts.filter((f) => f.section === sec.id).map((f) => `<div class="mem-fact"><div><span class="t" contenteditable="true" spellcheck="false" data-mem-edit="${esc(f.id)}">${esc(f.text)}</span><span class="mem-meta">${esc(f.source)}</span></div>
      <button type="button" class="mem-x" data-mem-del="${esc(f.id)}" aria-label="Забыть: ${esc(f.text)}" data-tip="Забыть">✕</button></div>`).join("") || '<p class="muted">Пусто</p>'}
    <form class="mem-add add" data-mem-add="${esc(sec.id)}"><input aria-label="Добавить в «${esc(sec.title)}»" placeholder="Добавить факт…" maxlength="300" autocomplete="off"><button class="btn">+</button></form></div>`).join("");
  if (memState.qedit) {
    const t = document.querySelector(`[data-mem-qtext="${CSS.escape(memState.qedit)}"]`);
    if (t) { t.focus(); getSelection().selectAllChildren(t); getSelection().collapseToEnd(); }
  }
  dropStaleTip();
}
// factById ищет и в предложенном в чате: портрет страницы мог еще не перечитаться.
const factById = (id) => [...((memState.p && memState.p.facts) || []), ...((chatState.memNew && chatState.memNew.facts) || [])].find((f) => f.id === id);
// memDo runs one portrait change, then reloads the portrait everywhere it is shown.
async function memDo(fn, head, rest, undo, tone) {
  try { await fn(); } catch (e) { toast("Портрет не изменен", " · " + errText(e), null, "del"); await loadPortrait(); renderPortrait(); renderChat(); return false; }
  await loadPortrait();
  if (panel.dataset.view === "memory") renderPortrait();
  renderChat();
  if (head) toast(head, rest, undo, tone);
  return true;
}
function memForget(id) {
  const f = factById(id);
  if (!f) return;
  return memDo(() => native().PortraitRemove(id), "Забыто", ` · «${f.text}»`, () => memDo(() => native().PortraitRestore(f), "Вернул", " · «" + f.text + "»"), "del");
}
function memApprove(id, text) {
  const f = factById(id) || { title: "портрет" };
  memState.qedit = "";
  if (chatState.memNew) chatState.memNew.facts = chatState.memNew.facts.filter((x) => x.id !== id);
  return memDo(() => native().PortraitApprove(id, text || ""), "Запомнено", ` · в «${f.title}»`, () => memDo(() => native().PortraitRemove(id), "Вернул", " · не запоминаю"));
}
function memReject(id) {
  if (chatState.memNew) chatState.memNew.facts = chatState.memNew.facts.filter((x) => x.id !== id);
  memState.qedit = "";
  return memDo(() => native().PortraitRemove(id), "Не запоминаю", " · и в портрет не кладу", null, "off");
}
$("#mem-pick").addEventListener("click", async () => {
  if (!native() || !native().PickPortraitDir) return;
  let dir;
  try { dir = await native().PickPortraitDir(); } catch (e) { $("#mem-dir-note").textContent = errText(e); return; }
  if (!dir) return;
  await saveChatPart({ memoryDir: dir, memoryServer: "" }, "Папка портрета", " · " + tilde(dir));
  await loadPortrait();
});
$("#mem-suggest").addEventListener("change", (e) => saveChatPart({ memoryQuiet: !e.target.checked }, "Память", e.target.checked ? " · предлагаю запомнить в чате" : " · новое ждет только в портрете"));
$("#mem-extract").addEventListener("change", (e) => saveChatPart({ memoryOff: !e.target.checked }, "Память", e.target.checked ? " · разбираю ответы на новое о Вас" : " · ответы не разбираю"));
$("#mem-card").addEventListener("click", (e) => { if (e.target.closest("[data-mem-open]")) openSettings("memory"); });
$("#mem-back").addEventListener("click", () => openSettings("chat"));
$("#memory").addEventListener("click", (e) => {
  const d = e.target.closest("[data-mem-del]");
  if (d) return void memForget(d.dataset.memDel);
  const ok = e.target.closest("[data-mem-ok]");
  if (ok) {
    const t = document.querySelector(`[data-mem-qtext="${CSS.escape(ok.dataset.memOk)}"]`);
    return void memApprove(ok.dataset.memOk, t ? t.textContent.trim() : "");
  }
  const no = e.target.closest("[data-mem-no]");
  if (no) return void memReject(no.dataset.memNo);
  const qe = e.target.closest("[data-mem-qedit]");
  if (qe) { memState.qedit = qe.dataset.memQedit; renderPortrait(); }
});
$("#memory").addEventListener("submit", (e) => {
  const f = e.target.closest("[data-mem-add]");
  if (!f) return;
  e.preventDefault();
  const inp = f.querySelector("input"), text = inp.value.trim(), sec = f.dataset.memAdd;
  if (!text) return;
  memDo(() => native().PortraitAdd(sec, text), "Добавлено", ` · «${text}»`).then((ok) => {
    if (!ok) return;
    const again = document.querySelector(`[data-mem-add="${CSS.escape(sec)}"] input`);
    if (again) again.focus();
  });
});
// Правка в строке сохраняется, когда строка теряет фокус; пусто — забыть.
$("#memory").addEventListener("focusout", (e) => {
  const t = e.target.closest("[data-mem-edit]");
  if (!t) return;
  const f = factById(t.dataset.memEdit), text = t.textContent.trim();
  if (!f || text === f.text) return;
  if (!text) return void memForget(f.id);
  const old = f.text;
  memDo(() => native().PortraitEdit(f.id, text), "Исправлено", ` · «${text}»`, () => memDo(() => native().PortraitEdit(f.id, old), "Вернул", " · как было"));
});
$("#memory").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && e.target.closest("[data-mem-edit]")) { e.preventDefault(); e.target.blur(); }
  const q = e.key === "Enter" && e.target.closest("[data-mem-qtext]");
  if (q) { e.preventDefault(); const b = document.querySelector(`[data-mem-ok="${CSS.escape(q.dataset.memQtext)}"]`); if (b) b.click(); }
});
// Новое из разбора ответа (portrait:new): в портрет уже легло «ждет
// одобрения»; если человек не выключил — спросить прямо в ленте этого чата.
// Новое на одобрение приходит со своим чатом (portrait:new {chat, facts}):
// карточка «Запомнить?» встает в тот чат, где факт найден, даже если человек
// уже открыл другой или чат был новым и id получил во время ответа.
function memOffer(chat, facts) {
  const n = chatState.memNew && chatState.memNew.chat === chat ? chatState.memNew : { chat, facts: [], edit: "" };
  n.facts = [...n.facts, ...facts.filter((f) => !n.facts.some((x) => x.id === f.id))];
  chatState.memNew = n;
}
if (window.runtime) window.runtime.EventsOn("portrait:new", (ev) => {
  if (!(chatState.settings || {}).memoryQuiet && ev && ev.facts && ev.facts.length) memOffer(ev.chat, ev.facts);
  loadPortrait().then(() => { if (!chatState.busy) renderChat(); });
});
// memInChat — карточки после ответа: «запомнить?» и «портрет не поместился».
function memInChat() {
  let h = "";
  const n = chatState.memNew;
  if (n && n.chat === chatState.id) h += n.facts.map((f) => `<div class="mem-sugg">
    <span class="muted">Запомнить о Вас? Раздел «${esc(f.title)}»</span>
    <span class="t" data-sugg-text="${esc(f.id)}"${n.edit === f.id ? ' contenteditable="true"' : ""} spellcheck="false">${esc(f.text)}</span>
    <div class="mem-acts">${n.edit === f.id
      ? `<button type="button" class="btn primary" data-sugg-yes="${esc(f.id)}">Сохранить и запомнить</button><button type="button" class="btn" data-sugg-cancel>Отмена</button>`
      : `<button type="button" class="btn primary" data-sugg-yes="${esc(f.id)}">Да</button><button type="button" class="btn" data-sugg-no="${esc(f.id)}">Нет</button><button type="button" class="btn" data-sugg-edit="${esc(f.id)}">Изменить</button>`}</div></div>`).join("");
  const o = chatState.over;
  if (o && o.chat === chatState.id) {
    h += `<div class="mem-over"><b>Портрет ${o.size.toLocaleString("ru-RU")} знаков, а в начало чата у этой модели влезает ${o.budget.toLocaleString("ru-RU")}. ${o.start ? "Решите до вопроса — иначе ответ пойдет без портрета." : "В этот ответ он не пошел."}</b>`;
    if (o.props) {
      // Размер считает окно — тем же текстом, что уйдет в чат, с разделами и знаками.
      const text = o.props.proposals.map((x) => `${x.title}: ${x.text}`).join("\n"), size = o.props.size;
      h += `<p class="label-sm">Предложение: ${o.size.toLocaleString("ru-RU")} → ${size.toLocaleString("ru-RU")} знаков${size > o.budget ? " — все еще не влезает, лишнее лучше удалить в портрете" : ""}</p>
        <textarea readonly aria-label="Сжатый портрет">${esc(text)}</textarea>
        <div class="mem-acts"><button type="button" class="btn primary" data-over="apply">Принять сжатие</button><button type="button" class="btn" data-over="close">Не сейчас</button></div>
        <p class="muted">Каждый раздел станет короче. Сжатые факты заменятся предложением; одобренное после «Сжать» и ждущее одобрения останутся.</p>`;
    } else {
      h += `<div class="mem-acts"><button type="button" class="btn primary" data-over="compress"${o.busy ? " disabled" : ""}>${o.busy ? "Сжимаю…" : "Сжать портрет"}</button><button type="button" class="btn" data-over="relevant">Только нужное в этот чат</button><button type="button" class="btn" data-over="full">Целиком все равно</button></div>
        <p class="muted">«Только нужное» — кто Вы, предпочтения и факты по словам вопроса. «Целиком» — модель раньше забудет начало разговора.</p>`;
    }
    h += `</div>`;
  }
  return h;
}
$("#chat-log").addEventListener("click", async (e) => {
  const n = chatState.memNew;
  const yes = e.target.closest("[data-sugg-yes]");
  if (yes && n) {
    const t = document.querySelector(`[data-sugg-text="${CSS.escape(yes.dataset.suggYes)}"]`);
    n.edit = "";
    return void memApprove(yes.dataset.suggYes, t ? t.textContent.trim() : "");
  }
  const no = e.target.closest("[data-sugg-no]");
  if (no) return void memReject(no.dataset.suggNo);
  const ed = e.target.closest("[data-sugg-edit]");
  if (ed && n) {
    n.edit = ed.dataset.suggEdit;
    renderChat();
    const t = document.querySelector(`[data-sugg-text="${CSS.escape(n.edit)}"]`);
    if (t) { t.focus(); getSelection().selectAllChildren(t); getSelection().collapseToEnd(); }
    return;
  }
  if (e.target.closest("[data-sugg-cancel]") && n) { n.edit = ""; return void renderChat(); }
  const ov = e.target.closest("[data-over]"), o = chatState.over;
  if (!ov || !o) return;
  const act = ov.dataset.over;
  if (act === "relevant" || act === "full") {
    chatState.portraitMode[o.chat] = act;
    chatState.over = null;
    renderChat();
    return void toast("Портрет", act === "full" ? " · целиком в этот чат, со следующего вопроса" : " · только нужное в этот чат, со следующего вопроса", () => { delete chatState.portraitMode[o.chat]; }, "info");
  }
  if (act === "close") { o.props = null; return void renderChat(); }
  if (act === "compress") {
    o.busy = true; renderChat();
    try { o.props = await native().PortraitCompress(); } catch (err) { toast("Не сжато", " · " + errText(err), null, "del"); }
    o.busy = false; renderChat();
    return;
  }
  if (act === "apply" && o.props) {
    const props = o.props;
    const n = props.proposals.length;
    if (await memDo(() => native().PortraitApplyCompressed(props), "Портрет сжат", ` · ${n} ${plural(n, "факт", "факта", "фактов")} вместо ${props.basis.length}`, null)) {
      chatState.over = null;
      renderChat();
    }
  }
});
$("#chat-log").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && e.target.closest("[data-sugg-text]")) { e.preventDefault(); const b = document.querySelector(`[data-sugg-yes="${CSS.escape(e.target.dataset.suggText)}"]`); if (b) b.click(); }
});

// Колонки чата: видимость (значок в шапке) и ширина (край тянется) —
// удобство одного Мака, в localStorage. Ширина прилипает к SNAP в пределах
// 14 px; двойной щелчок по краю — обычная ширина.
const CHAT_PANES = { l: { def: 248, min: 180, max: 440, snap: [200, 248, 320, 400] }, r: { def: 280, min: 220, max: 520, snap: [240, 280, 360, 480] } };
const chatPanes = (() => { try { return JSON.parse(localStorage.getItem("chat-panes") || "{}"); } catch { return {}; } })();
const saveChatPanes = () => { try { localStorage.setItem("chat-panes", JSON.stringify(chatPanes)); } catch { /* только на сеанс */ } };
function applyChatPanes() {
  const view = $("#chat-view");
  for (const side of ["l", "r"]) {
    const p = chatPanes[side] || {};
    view.classList.toggle("hide-" + side, !!p.hidden);
    view.style.setProperty("--chat-" + side, (p.w || CHAT_PANES[side].def) + "px");
    $("#chat-pane-" + side).setAttribute("aria-pressed", String(!p.hidden));
  }
}
function toggleChatPane(side) {
  chatPanes[side] = { ...(chatPanes[side] || {}), hidden: !(chatPanes[side] || {}).hidden };
  saveChatPanes();
  applyChatPanes();
}
document.querySelectorAll("#chat-view [data-pane]").forEach((b) => b.addEventListener("click", () => toggleChatPane(b.dataset.pane)));
document.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key === "\\" && !$("#chat-view").hidden) { e.preventDefault(); toggleChatPane("l"); }
});
function snapWidth(side, w) {
  const c = CHAT_PANES[side];
  w = Math.min(c.max, Math.max(c.min, Math.round(w)));
  const near = c.snap.find((x) => Math.abs(x - w) <= 14);
  return near ?? w;
}
document.querySelectorAll(".chat-grip").forEach((g) => {
  const side = g.dataset.side;
  g.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    hideTip();
    g.setPointerCapture(e.pointerId);
    const view = $("#chat-view"), x0 = e.clientX, w0 = (chatPanes[side] || {}).w || CHAT_PANES[side].def;
    view.classList.add("dragging");
    g.classList.add("on");
    const move = (ev) => {
      const w = snapWidth(side, side === "l" ? w0 + ev.clientX - x0 : w0 - (ev.clientX - x0));
      chatPanes[side] = { ...(chatPanes[side] || {}), w };
      view.style.setProperty("--chat-" + side, w + "px");
    };
    const up = () => {
      g.removeEventListener("pointermove", move);
      view.classList.remove("dragging");
      g.classList.remove("on");
      saveChatPanes();
    };
    g.addEventListener("pointermove", move);
    g.addEventListener("pointerup", up, { once: true });
    g.addEventListener("pointercancel", up, { once: true });
  });
  g.addEventListener("dblclick", () => { chatPanes[side] = { ...(chatPanes[side] || {}), w: CHAT_PANES[side].def }; saveChatPanes(); applyChatPanes(); });
  // Клавиатура: стрелки двигают край по SNAP.
  g.addEventListener("keydown", (e) => {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const c = CHAT_PANES[side], cur = (chatPanes[side] || {}).w || c.def;
    const grow = (e.key === "ArrowRight") === (side === "l");
    const next = grow ? c.snap.find((x) => x > cur) ?? c.max : [...c.snap].reverse().find((x) => x < cur) ?? c.min;
    chatPanes[side] = { ...(chatPanes[side] || {}), w: next };
    saveChatPanes();
    applyChatPanes();
  });
});
applyChatPanes();

// --- Тема окна --------------------------------------------------------------------
// Кнопка в шапке: как в системе → светлая → темная. Выбор — удобство этого
// Мака (localStorage); theme.js применяет его до первой отрисовки.
const THEMES = { system: "как в системе", light: "светлая", dark: "темная" };
function currentTheme() { return document.documentElement.dataset.theme || "system"; }
function paintThemeBtn() {
  const t = currentTheme();
  // У <svg> нет свойства hidden — только атрибут.
  document.querySelectorAll("[data-theme-icon]").forEach((svg) => svg.toggleAttribute("hidden", svg.dataset.themeIcon !== t));
  $("#theme-btn").dataset.tipB = THEMES[t] + " · нажмите, чтобы сменить";
}
$("#theme-btn").addEventListener("click", () => {
  const order = ["system", "light", "dark"];
  const next = order[(order.indexOf(currentTheme()) + 1) % order.length];
  setTheme(next);
  hideTip();
});
function setTheme(t) {
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
  else delete document.documentElement.dataset.theme;
  rememberTheme(t);
  paintThemeBtn();
}
// Тему переключили кнопкой витрины в рамке «Финансов»: окно берет ее себе,
// чтобы в одном окне не жили две темы.
window.addEventListener("storage", (e) => {
  // Только свои значения: чужая запись в ключ (не light/dark) не стирает выбор окна.
  if (e.key === FRAME_THEME_KEY && (e.newValue === "light" || e.newValue === "dark") && e.newValue !== currentTheme()) setTheme(e.newValue);
});
paintThemeBtn();

// --- Статистика (решение владельца 07.11) ----------------------------------------
// Пульт и Журнал на журнале вызовов модели. Правило переходов одно: день или
// назначение, модель, ошибки → Журнал с фильтром; сверху — откуда пришли.
const st = { screen: "pulse", days: 30, data: null, seq: 0, f: {}, open: null, from: null, hist: [], modes: { tokens: "area", share: "donut", feed: "bars", heat: "calls" }, feedSel: null, cal: null, calDay: null, money: null };
const ST_MON = ["янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"];
const ST_COLORS = { answer: "--z-sp4", search: "--z-sp5", extract: "--z-sp1", compress: "--z-sp2" };
const stCss = (v) => getComputedStyle(document.documentElement).getPropertyValue(v).trim();
const stNum = (n) => Math.round(n).toLocaleString("ru-RU");
const stTok = (n) => (n >= 1e6 ? (n / 1e6).toLocaleString("ru-RU", { maximumFractionDigits: 2 }) + " млн" : n >= 1e3 ? (n / 1e3).toLocaleString("ru-RU", { maximumFractionDigits: 1 }) + " тыс" : stNum(n));
const stDay = (iso) => { const [, m, d] = iso.split("-").map(Number); return d + " " + ST_MON[m - 1]; };
const stSec = (ms) => (ms / 1000).toLocaleString("ru-RU", { minimumFractionDigits: 1, maximumFractionDigits: 1 }) + " с";
const stSw = (c) => `<i class="st-sw" style="background:${stCss(c)}"></i>`;
const stNS = "http://www.w3.org/2000/svg";
const stEl = (tag, a = {}, p) => { const e = document.createElementNS(stNS, tag); for (const k in a) e.setAttribute(k, a[k]); if (p) p.appendChild(e); return e; };
const stReduce = () => matchMedia("(prefers-reduced-motion: reduce)").matches;
let stTipEl = null;
function stTip(x, y, html) {
  if (!stTipEl) { stTipEl = document.createElement("div"); stTipEl.className = "st-tip"; document.body.appendChild(stTipEl); }
  stTipEl.innerHTML = html; stTipEl.hidden = false;
  const w = stTipEl.offsetWidth, h = stTipEl.offsetHeight;
  stTipEl.style.left = Math.max(8, x + 14 + w > innerWidth - 8 ? x - w - 14 : x + 14) + "px";
  stTipEl.style.top = Math.max(8, y + 14 + h > innerHeight - 8 ? y - h - 14 : y + 14) + "px";
}
const stTipHide = () => { if (stTipEl) stTipEl.hidden = true; };
const stRow = (c, n, v) => `<div class="r"><span>${stSw(c)}${esc(n)}</span><span>${v}</span></div>`;

async function openStats() {
  st.money = null; st.cal = null; // «Деньги» и календарь перечитываются при каждом открытии
  $("#st-range").querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", +b.dataset.d === st.days));
  await stLoad();
}
async function stLoad() {
  if (!native() || !native().Stats) { $("#st-body").innerHTML = `<div class="st-empty">Статистика работает в окне приложения.</div>`; return; }
  // Ответ на прежний период, пришедший позже нового, не рисуется: побеждает последний запрос.
  const seq = ++st.seq;
  let data;
  try { data = await native().Stats(st.days); }
  catch (e) { if (seq === st.seq) $("#st-body").innerHTML = `<div class="st-empty">Не прочитать журнал вызовов: ${esc(errText(e))}</div>`; return; }
  if (seq !== st.seq) return;
  st.data = data;
  stRender();
}
// stGo moves to a screen with a filter and says where from (why).
function stGo(screen, f, why) {
  st.hist.push({ screen: st.screen, f: st.f, open: st.open, from: st.from });
  Object.assign(st, { screen, f: f || {}, open: null, from: why || null });
  stRender();
}
$("#st-back").addEventListener("click", () => { const h = st.hist.pop(); if (h) { Object.assign(st, h); stRender(); } });
$("#st-reset").addEventListener("click", () => { Object.assign(st, { f: {}, open: null, from: null }); stRender(); });
document.querySelectorAll("#stats-view [data-st]").forEach((b) => b.addEventListener("click", () => { st.hist = []; Object.assign(st, { screen: b.dataset.st, f: {}, open: null, from: null }); stRender(); }));
$("#st-range").addEventListener("click", (e) => { const b = e.target.closest("[data-d]"); if (!b) return; st.days = +b.dataset.d; st.open = null; st.hist = []; st.feedSel = null; if (st.f.day) Object.assign(st, { f: { ...st.f, day: undefined }, from: null }); openStats(); }); // «Назад» не вернет строку и день прежнего периода

function stDescribe() {
  const f = st.f, p = [];
  if (f.day) p.push(`день <b>${stDay(f.day)}</b>`);
  if (f.from) p.push(`период <b>${stDay(f.from)} — ${stDay(f.to)}</b>`);
  if (f.wd != null) p.push(`<b>${ST_WD_FULL[f.wd]}, ${f.hour}:00–${f.hour + 1}:00</b>`);
  if (f.purpose) p.push(`назначение <b>${esc(st.data.purposes.find((x) => x.id === f.purpose)?.title || f.purpose)}</b>`);
  if (f.model) p.push(`модель <b>${esc(f.model)}</b>`);
  if (f.failed) p.push("<b>только с ошибкой</b>");
  if (f.uncounted) p.push("<b>только без счета</b>");
  return p.join(", ");
}
function stRender() {
  stTipHide();
  if (!st.data) return;
  document.querySelectorAll("#stats-view [data-st]").forEach((b) => { if (b.dataset.st === st.screen) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current"); });
  const f = st.f, d = stDescribe();
  $("#st-cmd").textContent = "dvk stats" + (st.screen === "pulse" ? "" : " " + st.screen) + (st.screen === "money" ? " --month" : f.day ? ` --day ${f.day}` : f.from ? ` --from ${f.from} --to ${f.to}` : ` --since ${st.days}d`) + (f.wd != null ? ` --weekday ${f.wd + 1} --hour ${f.hour}` : "") + (f.purpose ? ` --purpose ${f.purpose}` : "") + (f.model ? ` --model ${f.model}` : "");
  $("#st-ctx").hidden = !(st.from || d);
  $("#st-ctx-text").innerHTML = (st.from ? `Вы пришли сюда, нажав ${esc(st.from)}. ` : "") + (d ? `Показано: ${d}.` : "");
  $("#st-back").hidden = !st.hist.length; $("#st-reset").hidden = !d;
  if (!st.data.total.calls && st.screen !== "money") {
    $("#st-body").innerHTML = `<div class="st-empty"><b>За ${st.days} дн вызовов модели нет.</b><span>Окно считает каждый вызов с этой версии: ответы в «Чате», поиск моделью, разбор портрета. Задайте вопрос в чате — он появится здесь.</span>${st.data.bad ? `<span>Нечитаемых строк в журнале: ${st.data.bad}.</span>` : ""}</div>`;
    return;
  }
  (ST_SCREENS[st.screen] || stPulse)($("#st-body"));
  if (!stReduce()) stCount($("#st-body"));
}
function stCount(root) {
  root.querySelectorAll("[data-n]").forEach((b) => {
    const v = +b.dataset.n, fmt = b.dataset.f === "tok" ? stTok : b.dataset.f === "usd" ? stUsd : stNum, t0 = performance.now();
    const step = (t) => { const k = Math.min(1, (t - t0) / 800); b.textContent = fmt(k === 1 ? v : v * (1 - Math.pow(1 - k, 3))); if (k < 1 && b.isConnected) requestAnimationFrame(step); };
    requestAnimationFrame(step);
  });
}
const stSeg = (id, opts, cur) => `<div class="st-seg" id="${id}">${opts.map(([v, n]) => `<button data-m="${v}" aria-pressed="${v === cur}">${n}</button>`).join("")}</div>`;
const stBindSeg = (id, key) => document.querySelectorAll(`#${id} button`).forEach((b) => b.addEventListener("click", () => { st.modes[key] = b.dataset.m; stRender(); }));

function stPulse(host) {
  const v = st.data, t = v.total, p = v.prev;
  const delta = (a, b) => { if (!b) return "<em>&nbsp;</em>"; const x = Math.round((a - b) / b * 100); return `<em class="${x >= 0 ? "st-up" : "st-down"}">${x >= 0 ? "+" : ""}${x}% к прошлым ${st.days} дн</em>`; };
  const k = (key, name, val, f, dl, to) => `<button data-k="${key}"><span>${name}</span><b data-n="${val}" data-f="${f}">${f === "tok" ? stTok(val) : stNum(val)}</b>${dl}<span class="to">${to} →</span></button>`;
  host.innerHTML = `<div class="st-kpis">${k("in", "Токены на вход", t.in, "tok", delta(t.in, p.in), "Журнал")}${k("out", "Токены на выход", t.out, "tok", delta(t.out, p.out), "Журнал")}${k("calls", "Вызовов модели", t.calls, "n", delta(t.calls, p.calls), "Журнал")}${k("failed", "С ошибкой", t.failed, "n", "<em>&nbsp;</em>", "Журнал")}${t.uncounted ? k("uncounted", "Без счета", t.uncounted, "n", "<em>сервер не прислал токены</em>", "Журнал") : ""}</div>
    <div class="st-grid">
      <div class="st-panel"><div class="st-hd"><div><h3>Токены по дням</h3><div class="st-sub">нажмите на день — Журнал этого дня</div></div>${stSeg("st-m1", [["area", "площадь"], ["line", "линии"], ["bars", "столбцы"]], st.modes.tokens)}</div><div id="st-c1"></div>
        <div class="st-legend"><span>${stSw("--z-accent")}Вход</span><span>${stSw("--z-src-teal")}Выход</span></div></div>
      <div class="st-panel"><div class="st-hd"><div><h3>На что ушли токены</h3><div class="st-sub">нажмите — Журнал с этим назначением</div></div>${stSeg("st-m2", [["donut", "кольцо"], ["bars", "полоски"]], st.modes.share)}</div><div id="st-c2"></div></div>
    </div>
    <div class="st-panel"><h3>Модели</h3><div class="st-sub">нажмите на строку — Журнал по этой модели</div>
      <div style="overflow-x:auto"><table class="st-table"><thead><tr><th>Модель</th><th class="n">Вызовов</th><th class="n">Вход</th><th class="n">Выход</th><th class="n">С ошибкой</th><th class="n">Без счета</th></tr></thead><tbody>${v.models.map((m) => `<tr class="row" tabindex="0" data-model="${esc(m.id)}"><td>${esc(m.title)}</td><td class="n">${stNum(m.calls)}</td><td class="n">${stTok(m.in)}</td><td class="n">${stTok(m.out)}</td><td class="n">${m.failed}</td><td class="n">${m.uncounted}</td></tr>`).join("")}</tbody></table></div></div>
    ${v.bad ? `<div class="st-sub">Нечитаемых строк в журнале: ${v.bad} — они пропущены.</div>` : ""}`;
  host.querySelectorAll(".st-kpis button").forEach((b) => b.addEventListener("click", () => {
    const key = b.dataset.k, name = b.querySelector("span").textContent;
    stGo("log", key === "failed" ? { failed: true } : key === "uncounted" ? { uncounted: true } : {}, `«${name}» на Пульте`);
  }));
  host.querySelectorAll("tr[data-model]").forEach((tr) => { const go = () => stGo("log", { model: tr.dataset.model }, `модель ${tr.dataset.model} на Пульте`); tr.addEventListener("click", go); tr.addEventListener("keydown", (e) => e.key === "Enter" && go()); });
  stBindSeg("st-m1", "tokens"); stBindSeg("st-m2", "share");
  stTimeChart($("#st-c1"), v.days, [{ name: "Вход", color: "--z-accent", get: (x) => x.in }, { name: "Выход", color: "--z-src-teal", get: (x) => x.out }], st.modes.tokens,
    (x) => stGo("log", { day: x.date }, `день ${stDay(x.date)} на графике токенов`));
  // Доли — только токены по счету: вызовы без счета (токены неизвестны) — строкой ниже, а не долей.
  const parts = v.purposes.filter((x) => x.in + x.out > 0).map((x) => ({ id: x.id, name: x.title, color: ST_COLORS[x.id] || "--z-faint", v: x.in + x.out }));
  const c2 = $("#st-c2"), unc = v.purposes.filter((x) => x.uncounted);
  if (parts.length) stShare(c2, parts, st.modes.share, (x) => stGo("log", { purpose: x.id }, `долю «${x.name}» на Пульте`));
  else c2.innerHTML = `<div class="st-sub">Токенов по счету за период нет.</div>`;
  if (unc.length) {
    c2.insertAdjacentHTML("beforeend", `<div class="st-sub">Без счета, токены неизвестны: ${unc.map((x) => `<button class="st-chip" data-unc="${esc(x.id)}">${esc(x.title)} · ${stNum(x.uncounted)}</button>`).join(" ")}</div>`);
    c2.querySelectorAll("[data-unc]").forEach((b) => b.addEventListener("click", () => stGo("log", { purpose: b.dataset.unc, uncounted: true }, `«без счета» под долями на Пульте`)));
  }
}

function stTimeChart(host, days, series, mode, onPick) {
  const W = 640, H = 210, L = 46, R = 8, T = 10, B = 24, n = days.length, bw = (W - L - R) / n;
  const svg = stEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "токены по дням" }); host.appendChild(svg);
  const stacks = days.map((d) => { let a = 0; return series.map((s) => (a += s.get(d))); });
  const max = Math.max(1, ...stacks.map((s) => s[s.length - 1])) * 1.08;
  const x = (i) => (mode === "bars" ? L + bw * (i + 0.5) : L + (W - L - R) * (n === 1 ? 0.5 : i / (n - 1)));
  const y = (v) => T + (H - T - B) * (1 - v / max);
  for (let k = 0; k <= 3; k++) { const v = (max / 1.08) * k / 3; stEl("line", { x1: L, x2: W - R, y1: y(v), y2: y(v), stroke: stCss("--z-rule") }, svg); stEl("text", { x: L - 6, y: y(v) + 3, "text-anchor": "end" }, svg).textContent = stTok(v); }
  const step = Math.max(1, Math.round(n / 6));
  days.forEach((d, i) => { if (i % step === 0 || i === n - 1) stEl("text", { x: x(i), y: H - 6, "text-anchor": "middle" }, svg).textContent = stDay(d.date); });
  const g = stEl("g", { class: mode === "bars" ? "" : "st-draw" }, svg);
  if (mode === "bars") {
    days.forEach((d, i) => { let prev = 0; series.forEach((s, si) => { const v = stacks[i][si]; const r = stEl("rect", { class: "st-bar-r", x: x(i) - bw * 0.36, width: bw * 0.72, y: y(v), height: Math.max(0.5, y(prev) - y(v)), fill: stCss(s.color), "fill-opacity": 0.8 }, g); r.style.animationDelay = Math.min(0.6, i * 0.012) + "s"; prev = v; }); });
  } else {
    for (let si = series.length - 1; si >= 0; si--) {
      const top = days.map((d, i) => [x(i), y(mode === "line" ? series[si].get(d) : stacks[i][si])]);
      if (mode === "area") {
        const bot = days.map((_, i) => [x(i), y(si ? stacks[i][si - 1] : 0)]).reverse();
        stEl("path", { d: "M" + top.map((q) => q.join(",")).join("L") + "L" + bot.map((q) => q.join(",")).join("L") + "Z", fill: stCss(series[si].color), "fill-opacity": 0.28 }, g);
      }
      stEl("path", { d: "M" + top.map((q) => q.join(",")).join("L"), fill: "none", stroke: stCss(series[si].color), "stroke-width": mode === "line" ? 2 : 1.6 }, g);
    }
    requestAnimationFrame(() => requestAnimationFrame(() => g.classList.add("in")));
  }
  const cross = stEl("line", { y1: T, y2: H - B, stroke: stCss("--z-rule-2"), "stroke-dasharray": "3 3", visibility: "hidden" }, svg);
  const at = (e) => { const r = svg.getBoundingClientRect(), px = ((e.clientX - r.left) / r.width) * W; return Math.max(0, Math.min(n - 1, mode === "bars" ? Math.floor((px - L) / bw) : Math.round(((px - L) / (W - L - R)) * (n - 1)))); };
  svg.style.cursor = "pointer";
  svg.addEventListener("pointermove", (e) => {
    const i = at(e), d = days[i];
    cross.setAttribute("x1", x(i)); cross.setAttribute("x2", x(i)); cross.setAttribute("visibility", "visible");
    stTip(e.clientX, e.clientY, `<div class="t">${stDay(d.date)}</div>` + series.map((s) => stRow(s.color, s.name, stTok(s.get(d)))).join("") + stRow("--z-faint", "Вызовов", stNum(d.calls)) + (d.uncounted ? stRow("--z-lvl-warn", "Без счета", d.uncounted) : "") + `<div class="st-hint">Нажмите — Журнал этого дня</div>`);
  });
  svg.addEventListener("pointerleave", () => { cross.setAttribute("visibility", "hidden"); stTipHide(); });
  svg.addEventListener("click", (e) => onPick(days[at(e)]));
}

function stShare(host, parts, mode, onPick) {
  const total = parts.reduce((a, p) => a + p.v, 0) || 1;
  const tip = (e, p) => stTip(e.clientX, e.clientY, `<div class="t">${esc(p.name)}</div>` + stRow(p.color, "Доля", Math.round((p.v / total) * 100) + "%") + `<div class="st-hint">Нажмите — Журнал с этим назначением</div>`);
  if (mode === "bars") {
    const max = Math.max(...parts.map((p) => p.v));
    host.innerHTML = `<div class="st-hbars">${parts.map((p, i) => `<button data-i="${i}"><span>${stSw(p.color)}${esc(p.name)}</span><i style="width:${Math.max(2, (p.v / max) * 100)}%;background:${stCss(p.color)}"></i><span>${Math.round((p.v / total) * 100)}%</span></button>`).join("")}</div>`;
    host.querySelectorAll("button").forEach((b) => { const p = parts[+b.dataset.i]; b.addEventListener("click", () => onPick(p)); b.addEventListener("pointermove", (e) => tip(e, p)); b.addEventListener("pointerleave", stTipHide); });
    return;
  }
  const S = 240, R = 100, r = 62, c = S / 2;
  const svg = stEl("svg", { viewBox: `0 0 ${S} ${S}`, role: "img", "aria-label": "доли назначений", class: "st-donut" }); host.appendChild(svg);
  let a0 = -Math.PI / 2;
  const segs = parts.map((p) => {
    const a1 = a0 + (p.v / total) * Math.PI * 2, big = a1 - a0 > Math.PI ? 1 : 0, pt = (a, rr) => [c + rr * Math.cos(a), c + rr * Math.sin(a)];
    const [x0, y0] = pt(a0, R), [x1, y1] = pt(a1 - 0.004, R), [x2, y2] = pt(a1 - 0.004, r), [x3, y3] = pt(a0, r);
    const s = stEl("path", { d: `M${x0},${y0}A${R},${R} 0 ${big} 1 ${x1},${y1}L${x2},${y2}A${r},${r} 0 ${big} 0 ${x3},${y3}Z`, fill: stCss(p.color), stroke: stCss("--z-bg-2"), "stroke-width": 2, tabindex: 0, role: "button", "aria-label": p.name + ": открыть в журнале" }, svg);
    s.style.cursor = "pointer"; a0 = a1;
    const hl = (e) => { segs.forEach((q) => q.setAttribute("fill-opacity", q === s ? 1 : 0.5)); bigT.textContent = Math.round((p.v / total) * 100) + "%"; small.textContent = p.name; if (e && e.clientX) tip(e, p); };
    s.addEventListener("pointermove", hl); s.addEventListener("focus", hl); s.addEventListener("pointerleave", stTipHide);
    s.addEventListener("click", () => onPick(p)); s.addEventListener("keydown", (e) => e.key === "Enter" && onPick(p));
    return s;
  });
  const bigT = stEl("text", { x: c, y: c + 2, "text-anchor": "middle" }, svg); bigT.style.cssText = "font-size:26px;font-weight:700;fill:var(--z-ink)";
  const small = stEl("text", { x: c, y: c + 22, "text-anchor": "middle" }, svg); small.style.cssText = "font-size:11px;fill:var(--z-muted)";
  const top = parts.reduce((a, p) => (p.v > a.v ? p : a)); bigT.textContent = Math.round((top.v / total) * 100) + "%"; small.textContent = top.name;
}

function stLog(host) {
  const v = st.data, f = st.f;
  const rows = v.calls.filter((c) => (!f.day || c.at.slice(0, 10) === f.day) && stInRange(c.at, f) && (f.wd == null || (stWd(c.at) === f.wd && +c.at.slice(11, 13) === f.hour)) && (!f.purpose || c.purpose === f.purpose) && (!f.model || c.model === f.model) && (!f.failed || c.failed) && (!f.uncounted || !c.counted));
  const shown = rows.slice(0, 200);
  const time = (iso) => stDay(iso.slice(0, 10)) + " " + iso.slice(11, 16);
  host.innerHTML = `<div class="st-hd"><div class="st-chips">${v.purposes.map((p) => `<button class="st-chip" data-p="${p.id}" aria-pressed="${f.purpose === p.id}">${esc(p.title)}</button>`).join("")}</div>
      <div class="st-chips">${v.models.map((m) => `<button class="st-chip" data-m="${esc(m.id)}" aria-pressed="${f.model === m.id}">${esc(m.title)}</button>`).join("")}</div></div>
    <div class="st-sub">${rows.length ? `<b>${stNum(rows.length)}</b> вызовов: вход ${stTok(rows.reduce((a, c) => a + c.in, 0))}, выход ${stTok(rows.reduce((a, c) => a + c.out, 0))}. Нажмите строку — подробности.${rows.length > shown.length ? ` Показаны последние ${shown.length}.` : ""}` : "Под этот фильтр вызовов нет."}</div>
    <div class="st-scroll"><table class="st-table"><thead><tr><th>Когда</th><th>Назначение</th><th>Модель</th><th class="n">Вход</th><th class="n">Выход</th><th class="n">До слова</th><th class="n">Всего</th></tr></thead><tbody>${shown.map((c, i) => `<tr class="row" tabindex="0" data-i="${i}" aria-expanded="${st.open === i}"><td>${time(c.at)}</td><td>${stSw(ST_COLORS[c.purpose] || "--z-faint")}${esc(c.title)}${c.failed ? ' <span class="st-pill bad">ошибка</span>' : ""}</td><td>${esc(c.model)}</td><td class="n">${c.counted ? stNum(c.in) : "—"}</td><td class="n">${c.counted ? stNum(c.out) : "—"}</td><td class="n">${stSec(c.firstMs)}</td><td class="n">${stSec(c.tookMs)}</td></tr>` + (st.open === i ? `<tr class="det"><td colspan="7" id="st-card"></td></tr>` : "")).join("")}</tbody></table></div>`;
  host.querySelectorAll("[data-p]").forEach((b) => b.addEventListener("click", () => { st.f = { ...st.f, purpose: st.f.purpose === b.dataset.p ? undefined : b.dataset.p }; st.open = null; stRender(); }));
  host.querySelectorAll("[data-m]").forEach((b) => b.addEventListener("click", () => { st.f = { ...st.f, model: st.f.model === b.dataset.m ? undefined : b.dataset.m }; st.open = null; stRender(); }));
  host.querySelectorAll("tr.row").forEach((tr) => { const go = () => { st.open = st.open === +tr.dataset.i ? null : +tr.dataset.i; stRender(); }; tr.addEventListener("click", go); tr.addEventListener("keydown", (e) => e.key === "Enter" && go()); });
  if (st.open != null && shown[st.open]) {
    const c = shown[st.open], day = c.at.slice(0, 10), same = v.calls.filter((x) => x.at.slice(0, 10) === day), h0 = +c.at.slice(11, 13);
    const byH = Array.from({ length: 24 }, (_, h) => same.filter((x) => +x.at.slice(11, 13) === h).length), mx = Math.max(1, ...byH);
    $("#st-card").innerHTML = `<div class="st-card">
      <div><b>${esc(c.title)}</b> · ${esc(c.model)} · ${time(c.at)}</div>
      <div class="st-sub">${c.counted ? `Токенов: ${stNum(c.in)} на вход, ${stNum(c.out)} на выход.` : "Сервер не прислал счет токенов."} До первого слова ${stSec(c.firstMs)}, всего ${stSec(c.tookMs)}.${c.failed ? " Вызов закончился ошибкой." : ""} Текст вопроса в журнал не пишется.</div>
      <div class="st-sub">${stDay(day)}: ${same.length} вызовов по часам; синий — час этого вызова</div>
      <div class="st-hrs">${byH.map((n, h) => `<i class="${h === h0 ? "me" : ""}" style="height:${Math.max(4, (n / mx) * 100)}%" title="${h}:00 — ${n}"></i>`).join("")}</div>
      <div><button class="st-go" id="st-card-day">Все ${same.length} вызовов этого дня</button></div></div>`;
    $("#st-card-day").addEventListener("click", (e) => { e.stopPropagation(); stGo("log", { day }, `вызов ${time(c.at)} в Журнале`); });
  }
}

// --- Лента, Карта активности, Деньги (#493) ---------------------------------------
// То же правило переходов: время → Лента, что именно → Журнал, один вызов →
// его день на Карте, цена и лимит → Деньги. Лента и Карта считаются здесь из
// вызовов периода; Деньги — из окна (StatsMoney): цены и лимит живут там.
const ST_WD = ["пн", "вт", "ср", "чт", "пт", "сб", "вс"];
const ST_WD_FULL = ["понедельник", "вторник", "среда", "четверг", "пятница", "суббота", "воскресенье"];
const ST_MON_FULL = ["январе", "феврале", "марте", "апреле", "мае", "июне", "июле", "августе", "сентябре", "октябре", "ноябре", "декабре"];
const stWd = (iso) => { const [y, m, d] = iso.slice(0, 10).split("-").map(Number); return (new Date(y, m - 1, d).getDay() + 6) % 7; };
const stUsd = (v) => "$" + v.toLocaleString("ru-RU", { minimumFractionDigits: 2, maximumFractionDigits: v > 0 && v < 1 ? 4 : 2 });
const stInRange = (iso, f) => (!f.from || iso.slice(0, 10) >= f.from) && (!f.to || iso.slice(0, 10) <= f.to);
const stTitle = (id) => st.data.purposes.find((p) => p.id === id)?.title || id;

// stEnsure reloads a longer period when a link points before the current one
// (день календаря 90 дней при периоде 7 дн): Журнал читает вызовы периода.
async function stEnsure(day) {
  if (st.data.days.some((d) => d.date === day)) return;
  st.days = 90;
  $("#st-range").querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", +b.dataset.d === st.days));
  st.data = await native().Stats(st.days);
}

function stFeed(host) {
  const v = st.data, days = v.days, n = days.length, ids = v.purposes.map((p) => p.id);
  const at = Object.fromEntries(days.map((d, i) => [d.date, i]));
  const grid = days.map(() => Object.fromEntries(ids.map((id) => [id, 0])));
  for (const c of v.calls) { const i = at[c.at.slice(0, 10)]; if (i != null) grid[i][c.purpose] += c.in + c.out; }
  const sel = st.feedSel && st.feedSel.b < n ? st.feedSel : { a: 0, b: n - 1 };
  const whole = sel.a === 0 && sel.b === n - 1, from = days[sel.a].date, to = days[sel.b].date;
  const calls = v.calls.filter((c) => stInRange(c.at, { from, to }));
  const sum = (k) => calls.reduce((a, c) => a + c[k], 0), failed = calls.filter((c) => c.failed).length;
  const parts = ids.map((id) => ({ id, name: stTitle(id), color: ST_COLORS[id] || "--z-faint", v: calls.filter((c) => c.purpose === id).reduce((a, c) => a + c.in + c.out, 0) })).filter((p) => p.v > 0);
  const models = Object.entries(calls.reduce((a, c) => ((a[c.model] = (a[c.model] || 0) + 1), a), {})).sort((a, b) => b[1] - a[1]);
  const range = { from, to }, why = whole ? `Ленту за ${n} дн` : `период ${stDay(from)} — ${stDay(to)} на Ленте`;
  host.innerHTML = `<div class="st-panel"><div class="st-hd"><div><h3>Лента токенов по назначениям</h3><div class="st-sub">протяните по графику — цифры ниже пересчитаются под период; нажмите — один день</div></div>${stSeg("st-m3", [["bars", "столбцы"], ["area", "площадь"]], st.modes.feed)}</div><div id="st-c3"></div>
      <div class="st-legend">${ids.map((id) => `<span>${stSw(ST_COLORS[id] || "--z-faint")}${esc(stTitle(id))}</span>`).join("")}</div></div>
    <div class="st-line"><b>${whole ? `Весь период, ${n} дн` : `${stDay(from)} — ${stDay(to)}, ${sel.b - sel.a + 1} дн`}</b>${whole ? "" : ` <button class="st-chip" id="st-feed-all">весь период</button>`}
      <span class="st-gap"></span><button class="st-go" id="st-feed-log"${calls.length ? "" : " disabled"}>Открыть ${stNum(calls.length)} ${plural(calls.length, "вызов", "вызова", "вызовов")} периода в Журнале</button></div>
    <div class="st-kpis"><button data-fk="in"><span>Вход</span><b data-n="${sum("in")}" data-f="tok">${stTok(sum("in"))}</b><span class="to">Журнал →</span></button><button data-fk="out"><span>Выход</span><b data-n="${sum("out")}" data-f="tok">${stTok(sum("out"))}</b><span class="to">Журнал →</span></button><button data-fk="calls"><span>Вызовов</span><b data-n="${calls.length}">${stNum(calls.length)}</b><span class="to">Журнал →</span></button><button data-fk="failed"><span>С ошибкой</span><b data-n="${failed}">${stNum(failed)}</b><span class="to">Журнал →</span></button></div>
    <div class="st-grid"><div class="st-panel"><h3>Доли за период</h3><div class="st-sub">нажмите — Журнал периода с этим назначением</div><div id="st-c4"></div></div>
      <div class="st-panel"><h3>Модели за период</h3><div class="st-sub">нажмите — Журнал периода по модели</div>${models.length ? `<div class="st-hbars">${models.map(([m, k]) => `<button data-fm="${esc(m)}"><span>${esc(m)}</span><i style="width:${Math.max(2, (k / models[0][1]) * 100)}%;background:${stCss("--z-accent")}"></i><span>${stNum(k)}</span></button>`).join("")}</div>` : `<div class="st-sub">Вызовов нет.</div>`}</div></div>`;
  stBindSeg("st-m3", "feed");
  stFeedChart($("#st-c3"), days, ids, grid, st.modes.feed, whole ? null : sel, (a, b) => { st.feedSel = { a, b }; stRender(); });
  if ($("#st-feed-all")) $("#st-feed-all").addEventListener("click", () => { st.feedSel = null; stRender(); });
  $("#st-feed-log").addEventListener("click", () => stGo("log", whole ? {} : range, why));
  host.querySelectorAll("[data-fk]").forEach((b) => b.addEventListener("click", () => stGo("log", { ...(whole ? {} : range), ...(b.dataset.fk === "failed" ? { failed: true } : {}) }, `«${b.querySelector("span").textContent}» за ${why}`)));
  host.querySelectorAll("[data-fm]").forEach((b) => b.addEventListener("click", () => stGo("log", { ...(whole ? {} : range), model: b.dataset.fm }, `модель ${b.dataset.fm} за ${why}`)));
  if (parts.length) stShare($("#st-c4"), parts, st.modes.share, (p) => stGo("log", { ...(whole ? {} : range), purpose: p.id }, `долю «${p.name}» за ${why}`));
  else $("#st-c4").innerHTML = `<div class="st-sub">Токенов по счету за период нет.</div>`;
}

// stFeedChart draws tokens by day stacked by purpose; протяжка выделяет период.
function stFeedChart(host, days, ids, grid, mode, sel, onSel) {
  const W = 1040, H = 230, L = 46, R = 8, T = 10, B = 24, n = days.length, bw = (W - L - R) / n;
  const svg = stEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "лента токенов по назначениям" }); host.appendChild(svg);
  const stacks = grid.map((g) => { let a = 0; return ids.map((id) => (a += g[id])); });
  const max = Math.max(1, ...stacks.map((s) => s[s.length - 1] || 0)) * 1.08;
  const x = (i) => L + bw * (i + 0.5), y = (v) => T + (H - T - B) * (1 - v / max);
  for (let k = 0; k <= 3; k++) { const v = (max / 1.08) * k / 3; stEl("line", { x1: L, x2: W - R, y1: y(v), y2: y(v), stroke: stCss("--z-rule") }, svg); stEl("text", { x: L - 6, y: y(v) + 3, "text-anchor": "end" }, svg).textContent = stTok(v); }
  const step = Math.max(1, Math.round(n / 6));
  days.forEach((d, i) => { if (i % step === 0 || i === n - 1) stEl("text", { x: x(i), y: H - 6, "text-anchor": "middle" }, svg).textContent = stDay(d.date); });
  const selR = stEl("rect", { class: "st-selr", y: T, height: H - T - B, x: 0, width: 0, visibility: "hidden" }, svg);
  const showSel = (a, b) => { const lo = Math.min(a, b), hi = Math.max(a, b); selR.setAttribute("x", L + bw * lo); selR.setAttribute("width", bw * (hi - lo + 1)); selR.setAttribute("visibility", "visible"); };
  if (sel) showSel(sel.a, sel.b);
  const g = stEl("g", { class: mode === "bars" ? "" : "st-draw" }, svg);
  if (mode === "bars") {
    grid.forEach((_, i) => { let prev = 0; ids.forEach((id, k) => { const v = stacks[i][k]; if (v === prev) return; const r = stEl("rect", { class: "st-bar-r", x: x(i) - bw * 0.36, width: bw * 0.72, y: y(v), height: Math.max(0.5, y(prev) - y(v)), fill: stCss(ST_COLORS[id] || "--z-faint"), "fill-opacity": 0.85 }, g); r.style.animationDelay = Math.min(0.6, i * 0.012) + "s"; prev = v; }); });
  } else {
    for (let k = ids.length - 1; k >= 0; k--) {
      const top = grid.map((_, i) => [x(i), y(stacks[i][k])]), bot = grid.map((_, i) => [x(i), y(k ? stacks[i][k - 1] : 0)]).reverse();
      stEl("path", { d: "M" + top.map((q) => q.join(",")).join("L") + "L" + bot.map((q) => q.join(",")).join("L") + "Z", fill: stCss(ST_COLORS[ids[k]] || "--z-faint"), "fill-opacity": 0.3 }, g);
      stEl("path", { d: "M" + top.map((q) => q.join(",")).join("L"), fill: "none", stroke: stCss(ST_COLORS[ids[k]] || "--z-faint"), "stroke-width": 1.6 }, g);
    }
    requestAnimationFrame(() => requestAnimationFrame(() => g.classList.add("in")));
  }
  const pos = (e) => { const r = svg.getBoundingClientRect(), px = ((e.clientX - r.left) / r.width) * W; return Math.max(0, Math.min(n - 1, Math.floor((px - L) / bw))); };
  let drag = null;
  svg.style.cursor = "crosshair";
  svg.addEventListener("pointerdown", (e) => { drag = pos(e); svg.setPointerCapture(e.pointerId); showSel(drag, drag); stTipHide(); });
  svg.addEventListener("pointermove", (e) => {
    const i = pos(e);
    if (drag != null) return showSel(drag, i);
    const d = days[i];
    stTip(e.clientX, e.clientY, `<div class="t">${stDay(d.date)}</div>` + ids.filter((id) => grid[i][id]).map((id) => stRow(ST_COLORS[id] || "--z-faint", stTitle(id), stTok(grid[i][id]))).join("") + stRow("--z-faint", "Вызовов", stNum(d.calls)) + `<div class="st-hint">Протяните — период, нажмите — день</div>`);
  });
  svg.addEventListener("pointerleave", () => { if (drag == null) stTipHide(); });
  svg.addEventListener("pointerup", (e) => { if (drag == null) return; const a = drag, b = pos(e); drag = null; onSel(Math.min(a, b), Math.max(a, b)); });
  svg.addEventListener("pointercancel", () => { drag = null; });
}

function stHeat(host) {
  const v = st.data, byTok = st.modes.heat === "tokens";
  const cells = Array.from({ length: 7 }, () => Array(24).fill(0));
  for (const c of v.calls) cells[stWd(c.at)][+c.at.slice(11, 13)] += byTok ? c.in + c.out : 1;
  const max = Math.max(1, ...cells.flat());
  const shade = (n) => (n ? `color-mix(in srgb, var(--z-accent) ${Math.round(18 + (n / max) * 82)}%, var(--z-bg-3))` : "var(--z-bg-3)");
  host.innerHTML = `<div class="st-panel"><div class="st-hd"><div><h3>Когда зовете модель</h3><div class="st-sub">дни недели × часы за ${st.days} дн; нажмите клетку — Журнал этого часа</div></div>${stSeg("st-m5", [["calls", "вызовы"], ["tokens", "токены"]], st.modes.heat)}</div>
      <div class="st-heat"><span></span>${Array.from({ length: 24 }, (_, h) => `<span class="hh">${h % 3 ? "" : h}</span>`).join("")}
      ${cells.map((row, wd) => `<span class="wd">${ST_WD[wd]}</span>` + row.map((n, h) => `<button data-wd="${wd}" data-h="${h}" style="background:${shade(n)}" aria-label="${ST_WD_FULL[wd]}, ${h}:00 — ${byTok ? stTok(n) + " токенов" : n + " " + plural(n, "вызов", "вызова", "вызовов")}"></button>`).join("")).join("")}</div></div>
    <div class="st-panel"><div class="st-hd"><div><h3>Календарь 90 дней</h3><div class="st-sub">нажмите день — карточка дня</div></div></div><div id="st-cal">${st.cal ? "" : '<div class="st-sub">читаю 90 дней…</div>'}</div><div id="st-calday"></div></div>`;
  stBindSeg("st-m5", "heat");
  host.querySelectorAll(".st-heat button").forEach((b) => {
    const wd = +b.dataset.wd, h = +b.dataset.h;
    b.addEventListener("pointermove", (e) => stTip(e.clientX, e.clientY, `<div class="t">${ST_WD_FULL[wd]}, ${h}:00–${h + 1}:00</div>` + stRow("--z-accent", byTok ? "Токенов" : "Вызовов", byTok ? stTok(cells[wd][h]) : stNum(cells[wd][h])) + `<div class="st-hint">Нажмите — Журнал этого часа</div>`));
    b.addEventListener("pointerleave", stTipHide);
    b.addEventListener("click", () => stGo("log", { wd, hour: h }, `клетку «${ST_WD[wd]}, ${h}:00» на Карте`));
  });
  if (st.cal) stCalendar();
  else native().Stats(90).then((d) => { st.cal = d; if (st.screen === "heat") stCalendar(); }).catch((e) => { $("#st-cal").innerHTML = `<div class="st-sub">Календарь не прочитан: ${esc(errText(e))}</div>`; });
}
function stCalendar() {
  const days = st.cal.days, pad = stWd(days[0].date), max = Math.max(1, ...days.map((d) => d.calls));
  const shade = (n) => (n ? `color-mix(in srgb, var(--z-accent) ${Math.round(18 + (n / max) * 82)}%, var(--z-bg-3))` : "var(--z-bg-3)");
  $("#st-cal").innerHTML = `<div class="st-cal">${ST_WD.map((w) => `<span class="wd">${w}</span>`).join("")}${'<span></span>'.repeat(pad)}${days.map((d) => `<button data-day="${d.date}" aria-pressed="${st.calDay === d.date}" style="background:${shade(d.calls)}" aria-label="${stDay(d.date)}: ${d.calls} ${plural(d.calls, "вызов", "вызова", "вызовов")}"></button>`).join("")}</div>`;
  $("#st-cal").querySelectorAll("button").forEach((b) => {
    const d = days.find((x) => x.date === b.dataset.day);
    b.addEventListener("pointermove", (e) => stTip(e.clientX, e.clientY, `<div class="t">${stDay(d.date)}, ${ST_WD[stWd(d.date)]}</div>` + stRow("--z-accent", "Вызовов", stNum(d.calls)) + stRow("--z-faint", "Токенов", stTok(d.in + d.out)) + `<div class="st-hint">Нажмите — карточка дня</div>`));
    b.addEventListener("pointerleave", stTipHide);
    b.addEventListener("click", () => { st.calDay = d.date; stCalendar(); });
  });
  stCalDay();
}
function stCalDay() {
  const host = $("#st-calday"), d = st.cal && st.cal.days.find((x) => x.date === st.calDay);
  if (!d) { host.innerHTML = ""; return; }
  const calls = st.cal.calls.filter((c) => c.at.slice(0, 10) === d.date);
  const by = Object.entries(calls.reduce((a, c) => ((a[c.title] = (a[c.title] || 0) + 1), a), {}));
  host.innerHTML = `<div class="st-card"><div><b>${stDay(d.date)}, ${ST_WD_FULL[stWd(d.date)]}</b></div>
    <div class="st-sub">${d.calls ? `${stNum(d.calls)} ${plural(d.calls, "вызов", "вызова", "вызовов")}: вход ${stTok(d.in)}, выход ${stTok(d.out)}${d.failed ? `, с ошибкой ${d.failed}` : ""}${d.uncounted ? `, без счета ${d.uncounted}` : ""}.` : "В этот день модель не звали."}</div>
    ${by.length ? `<div class="st-sub">${by.map(([t, k]) => `${esc(t)} — ${k}`).join(" · ")}</div>` : ""}
    ${d.calls ? `<div><button class="st-go" id="st-calday-log">Все ${d.calls} ${plural(d.calls, "вызов", "вызова", "вызовов")} этого дня в Журнале</button></div>` : ""}</div>`;
  if ($("#st-calday-log")) $("#st-calday-log").addEventListener("click", async () => { await stEnsure(d.date); stGo("log", { day: d.date }, `день ${stDay(d.date)} в календаре Карты`); });
}

async function stMoney(host) {
  if (!native().StatsMoney) { host.innerHTML = `<div class="st-empty">Деньги считает окно приложения.</div>`; return; }
  if (!st.money) {
    host.innerHTML = `<div class="st-empty">Считаю расход месяца…</div>`;
    try { st.money = await native().StatsMoney(); } catch (e) { host.innerHTML = `<div class="st-empty">Не посчитать: ${esc(errText(e))}</div>`; return; }
    if (st.screen !== "money") return;
  }
  const m = st.money, lim = parseFloat((m.limit || "").replace(",", ".")) || 0, mon = ST_MON_FULL[+m.month.slice(5, 7) - 1];
  const share = (x) => (m.spent ? Math.round((x / m.spent) * 100) : 0);
  const warn = lim && m.spent >= lim ? `Лимит месяца превышен: ${stUsd(m.spent)} из ${stUsd(lim)}.` : lim && m.spent >= lim * 0.8 ? `Потрачено ${Math.round((m.spent / lim) * 100)}% лимита месяца.` : lim && m.forecast > lim ? `По прогнозу месяц выйдет за лимит на ${stUsd(m.forecast - lim)}.` : "";
  const tips = [];
  const topP = m.purposes.find((p) => p.cost > 0);
  if (topP && share(topP.cost) >= 40) tips.push([`«${topP.title}» — ${share(topP.cost)}% расхода месяца (${stUsd(topP.cost)})`, { purpose: topP.id }, `совет про «${topP.title}» в Деньгах`]);
  const search = m.purposes.find((p) => p.id === "search" && p.cost > 0);
  if (search) tips.push([`Шаги поиска моделью стоили ${stUsd(search.cost)}: в режиме «Кто ищет документы → окно» этих шагов нет`, { purpose: "search" }, "совет про поиск моделью в Деньгах"]);
  const topM = m.models.find((x) => x.cost > 0);
  if (topM && m.models.filter((x) => x.cost > 0).length > 1) tips.push([`${topM.id} — ${share(topM.cost)}% расхода месяца (${stUsd(topM.cost)}); модель меняется в «Чат и модель»`, { model: topM.id }, `совет про ${topM.id} в Деньгах`]);
  if (m.unpriced) tips.push([`${m.unpriced} ${plural(m.unpriced, "вызов", "вызова", "вызовов")} без цены модели — не вошли в расход; задайте цену в таблице ниже`, null]);
  host.innerHTML = `<div class="st-kpis"><button data-mk="spent"><span>Потрачено в ${mon}</span><b data-n="${m.spent}" data-f="usd">${stUsd(m.spent)}</b><em>за ${m.daysPassed} ${plural(m.daysPassed, "день", "дня", "дней")}</em><span class="to">Журнал →</span></button>
      <button data-mk="forecast"><span>Прогноз на месяц</span><b data-n="${m.forecast}" data-f="usd">${stUsd(m.forecast)}</b><em>по среднему прошедших дней</em></button>
      <button data-mk="limit"><span>Лимит месяца</span><b>${lim ? stUsd(lim) : "не задан"}</b><em>${lim ? `осталось ${stUsd(Math.max(0, lim - m.spent))}` : "потяните шкалу ниже"}</em></button>
      ${m.unpriced || m.uncounted ? `<button data-mk="unknown"><span>Не вошло в расход</span><b>${stNum(m.unpriced + m.uncounted)}</b><em>${m.unpriced ? `без цены ${m.unpriced}` : ""}${m.unpriced && m.uncounted ? " · " : ""}${m.uncounted ? `без счета ${m.uncounted}` : ""}</em></button>` : ""}</div>
    ${warn ? `<div class="st-warn" role="status">${esc(warn)}</div>` : ""}
    <div class="st-panel"><div class="st-hd"><div><h3>Расход против лимита</h3><div class="st-sub">потяните метку лимита по шкале или впишите число; метка 80% — порог предупреждения</div></div>
      <div class="st-limit-in"><label>лимит, $ <input id="st-lim" inputmode="decimal" value="${esc(m.limit || "")}" placeholder="нет"></label><button class="st-chip" id="st-lim-save">Сохранить</button>${lim ? '<button class="st-chip" id="st-lim-off">Снять</button>' : ""}</div></div>
      <div class="st-scale" id="st-scale"></div><div id="st-c6"></div></div>
    <div class="st-grid"><div class="st-panel"><h3>Расход по назначениям</h3><div class="st-sub">нажмите — Журнал с этим назначением</div>${m.purposes.length ? `<div class="st-hbars">${m.purposes.map((p, i) => `<button data-mp="${i}"><span>${stSw(ST_COLORS[p.id] || "--z-faint")}${esc(p.title)}</span><i style="width:${Math.max(2, m.spent ? (p.cost / m.spent) * 100 : 0)}%;background:${stCss(ST_COLORS[p.id] || "--z-faint")}"></i><span>${stUsd(p.cost)}</span></button>`).join("")}</div>` : `<div class="st-sub">В ${mon} вызовов нет.</div>`}</div>
      <div class="st-panel"><h3>Где сэкономить</h3>${tips.length ? `<div class="st-tips">${tips.map(([t, f], i) => `<div>${esc(t)}${f ? ` <button class="st-chip" data-tip-i="${i}">Журнал →</button>` : ""}</div>`).join("")}</div>` : `<div class="st-sub">Подсказок нет: расход ровный.</div>`}</div></div>
    <div class="st-panel"><h3>Цены моделей</h3><div class="st-sub">долларов за 1 млн токенов; «шлюз» — из списка моделей Vercel AI Gateway, раз в сутки; своя цена главнее, пустые поля — снова цена шлюза${m.priceNote ? `. <b>${esc(m.priceNote)}</b>` : ""}</div>
      <div style="overflow-x:auto"><table class="st-table"><thead><tr><th>Модель</th><th class="n">Вызовов</th><th class="n">Расход</th><th class="n">Вход, $</th><th class="n">Выход, $</th><th>Цена</th><th></th></tr></thead><tbody>${m.models.map((x, i) => `<tr data-mi="${i}"><td>${esc(x.id)}</td><td class="n">${stNum(x.calls)}</td><td class="n">${x.priced ? stUsd(x.cost) : "—"}</td>
        <td class="n"><input class="st-price" data-pin="${i}" value="${esc(x.source === "своя" ? x.in : "")}" placeholder="${esc(x.in || "нет")}" inputmode="decimal"></td><td class="n"><input class="st-price" data-pout="${i}" value="${esc(x.source === "своя" ? x.out : "")}" placeholder="${esc(x.out || "нет")}" inputmode="decimal"></td>
        <td>${x.source || '<span class="st-pill bad">нет цены</span>'}</td><td><button class="st-chip" data-psave="${i}">Сохранить</button></td></tr>`).join("")}</tbody></table></div></div>`;
  const reload = async () => { st.money = null; await stMoney(host); if (!stReduce()) stCount(host); };
  const setLimit = async (v) => { try { await native().SetSpendLimit(v); showError(""); } catch (e) { showError("Лимит не сохранен: " + errText(e)); } await reload(); };
  $("#st-lim-save").addEventListener("click", () => setLimit($("#st-lim").value.trim()));
  $("#st-lim").addEventListener("keydown", (e) => e.key === "Enter" && setLimit($("#st-lim").value.trim()));
  if ($("#st-lim-off")) $("#st-lim-off").addEventListener("click", () => setLimit(""));
  host.querySelectorAll('[data-mk="forecast"], [data-mk="limit"]').forEach((b) => b.addEventListener("click", () => $("#st-lim").focus()));
  host.querySelector('[data-mk="spent"]').addEventListener("click", () => stGo("log", {}, `«Потрачено в ${mon}» в Деньгах`));
  host.querySelectorAll("[data-mp]").forEach((b) => b.addEventListener("click", () => { const p = m.purposes[+b.dataset.mp]; stGo("log", { purpose: p.id }, `«${p.title}» в Деньгах`); }));
  host.querySelectorAll("[data-tip-i]").forEach((b) => b.addEventListener("click", () => { const [, f, why] = tips[+b.dataset.tipI]; stGo("log", f, why); }));
  host.querySelectorAll("[data-psave]").forEach((b) => b.addEventListener("click", async () => {
    const i = +b.dataset.psave, x = m.models[i];
    try { await native().SetModelPrice(x.id, host.querySelector(`[data-pin="${i}"]`).value.trim(), host.querySelector(`[data-pout="${i}"]`).value.trim()); showError(""); toast("Сохранено", ` · цена ${x.id}`); }
    catch (e) { showError("Цена не сохранена: " + errText(e)); return; }
    await reload();
  }));
  stScale($("#st-scale"), m, lim, setLimit);
  stMonthChart($("#st-c6"), m, lim);
}

// stScale is the month's spend against the limit; метку лимита тянут мышью.
function stScale(host, m, lim, setLimit) {
  const top = Math.max(lim, m.forecast, m.spent, 1) * 1.25, pct = (v) => Math.min(100, (v / top) * 100);
  const round = (v) => (top > 20 ? String(Math.round(v)) : (Math.round(v * 10) / 10).toString());
  host.innerHTML = `<div class="st-track"><i class="fill${lim && m.spent >= lim * 0.8 ? " hot" : ""}" style="width:${pct(m.spent)}%"></i><i class="fc" style="left:${pct(m.forecast)}%" title="прогноз ${stUsd(m.forecast)}"></i>
      ${lim ? `<i class="p80" style="left:${pct(lim * 0.8)}%"></i>` : ""}<button class="lim" id="st-lim-h" style="left:${pct(lim)}%" aria-label="Лимит месяца ${lim ? stUsd(lim) : "не задан"}; потяните"><span>${lim ? stUsd(lim) : "лимит"}</span></button></div>
    <div class="st-legend"><span>${stSw("--z-accent")}потрачено ${stUsd(m.spent)}</span><span><i class="st-sw st-fc"></i>прогноз ${stUsd(m.forecast)}</span>${lim ? `<span><i class="st-sw st-p80"></i>80% лимита — ${stUsd(lim * 0.8)}</span>` : ""}<span>шкала до ${stUsd(top)}</span></div>`;
  const h = $("#st-lim-h"), track = host.querySelector(".st-track");
  let val = lim;
  h.addEventListener("pointerdown", (e) => { h.setPointerCapture(e.pointerId); h.dataset.drag = "1"; });
  h.addEventListener("pointermove", (e) => {
    if (!h.dataset.drag) return;
    const r = track.getBoundingClientRect(), k = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    val = +round(k * top);
    h.style.left = k * 100 + "%";
    h.querySelector("span").textContent = stUsd(val);
  });
  h.addEventListener("pointerup", () => { if (!h.dataset.drag) return; delete h.dataset.drag; setLimit(val > 0 ? String(val) : ""); });
}

// stMonthChart is the cumulative spend by day with the forecast and the limit.
function stMonthChart(host, m, lim) {
  const W = 1040, H = 170, L = 46, R = 8, T = 10, B = 22, n = m.daysInMonth;
  const svg = stEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "расход месяца нарастающим итогом" }); host.appendChild(svg);
  let a = 0; const cum = m.days.map((v) => (a += v));
  const max = Math.max(lim, m.forecast, a, 0.01) * 1.1, x = (i) => L + ((W - L - R) * i) / Math.max(1, n - 1), y = (v) => T + (H - T - B) * (1 - v / max);
  for (let k = 0; k <= 3; k++) { const v = (max / 1.1) * k / 3; stEl("line", { x1: L, x2: W - R, y1: y(v), y2: y(v), stroke: stCss("--z-rule") }, svg); stEl("text", { x: L - 6, y: y(v) + 3, "text-anchor": "end" }, svg).textContent = stUsd(v); }
  [1, 10, 20, n].forEach((d) => { stEl("text", { x: x(d - 1), y: H - 5, "text-anchor": "middle" }, svg).textContent = d; });
  if (lim) { stEl("line", { x1: L, x2: W - R, y1: y(lim), y2: y(lim), stroke: stCss("--z-lvl-warn") || stCss("--z-bad"), "stroke-dasharray": "5 4" }, svg); stEl("text", { x: W - R, y: y(lim) - 4, "text-anchor": "end" }, svg).textContent = "лимит " + stUsd(lim); }
  const g = stEl("g", { class: "st-draw" }, svg);
  if (cum.length) {
    stEl("path", { d: "M" + cum.map((v, i) => `${x(i)},${y(v)}`).join("L") + `L${x(cum.length - 1)},${y(0)}L${x(0)},${y(0)}Z`, fill: stCss("--z-accent"), "fill-opacity": 0.2 }, g);
    stEl("path", { d: "M" + cum.map((v, i) => `${x(i)},${y(v)}`).join("L"), fill: "none", stroke: stCss("--z-accent"), "stroke-width": 2 }, g);
    stEl("path", { d: `M${x(cum.length - 1)},${y(a)}L${x(n - 1)},${y(m.forecast)}`, fill: "none", stroke: stCss("--z-accent"), "stroke-width": 1.4, "stroke-dasharray": "3 4" }, g);
  }
  requestAnimationFrame(() => requestAnimationFrame(() => g.classList.add("in")));
  svg.addEventListener("pointermove", (e) => {
    const r = svg.getBoundingClientRect(), i = Math.max(0, Math.min(n - 1, Math.round((((e.clientX - r.left) / r.width) * W - L) / ((W - L - R) / Math.max(1, n - 1)))));
    stTip(e.clientX, e.clientY, `<div class="t">${i + 1} число</div>` + (i < cum.length ? stRow("--z-accent", "За день", stUsd(m.days[i])) + stRow("--z-accent", "С начала месяца", stUsd(cum[i])) : stRow("--z-faint", "Прогноз к этому дню", stUsd(a + ((m.forecast - a) * (i + 1 - cum.length)) / Math.max(1, n - cum.length)))));
  });
  svg.addEventListener("pointerleave", stTipHide);
}

const ST_SCREENS = { pulse: stPulse, feed: stFeed, heat: stHeat, money: stMoney, log: stLog };

// --- Доступы (#494) -------------------------------------------------------------
// Закрытое хранилище — черный сейф (решение владельца 07.11); Touch ID
// открывает (помощник kb-auth), внутри список и карточка, продукт своим цветом.
// Пароль — только по Touch ID, каждый раз: показать на 30 с или скопировать
// (буфер со скрытой пометкой, стирается через 30 с). Само закрывается без дела —
// время в «Оформление → Доступы». Состояние — в state.ac: showModule зовет
// acLeave раньше, чем дойдет до этих строк.
state.ac = { open: false, busy: false, confirmDel: "", list: [], q: "", env: "", sel: "", shown: {}, adding: false, left: 0, idle: 0, timers: {} };
const AC_HUES = ["--z-sp4", "--z-sp2", "--z-src-violet", "--z-sp6", "--z-sp5", "--z-sp1", "--z-sp3"];
const AC_GRADS = { spectrum: "Спектр DVK", gold: "Золото", ice: "Лед", dusk: "Закат", mono: "Без цвета" };
const AC_NAMES = ["Хранилище", "Шифр", "Сейф", "Ключи"];
const AC_LOCKS = [["1", "1 мин"], ["3", "3 мин"], ["5", "5 мин"], ["never", "никогда"]];
const acLook = () => {
  const l = state.accents.look || {};
  return { name: l["vault-name"] || "Хранилище", grad: l["vault-grad"] || "spectrum", anim: l["vault-anim"] || "full", lock: l["vault-lock"] || "5" };
};
const acLockSec = () => { const v = acLook().lock; return v === "never" ? 0 : Math.max(1, Math.min(1440, +v || 5)) * 60; };
const acWait = (ms) => new Promise((r) => setTimeout(r, matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : ms));
const acFmt = (s) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;

async function openAccess() {
  const ac = state.ac;
  if (ac.open) await acLoad();
  acRender();
}
// Уход с вкладки запирает хранилище и стирает показанный пароль из разметки:
// скрытая секция иначе держала бы его, пока вкладку не откроют снова.
function acLeave() {
  const ac = state.ac;
  if (!ac || !ac.open) return;
  acClose();
}
async function acLoad() {
  const ac = state.ac;
  try { ac.list = (await native().AccessList(ac.q)) || []; }
  catch (e) { ac.list = []; showError("Доступы не прочитать: " + errText(e)); }
}
function acVaultHTML() {
  const L = acLook();
  const ticks = Array.from({ length: 36 }, (_, i) => { const a = (i * 10 * Math.PI) / 180, r1 = i % 3 ? 55 : 51; return `<line x1="${66 + Math.cos(a) * r1}" y1="${66 + Math.sin(a) * r1}" x2="${66 + Math.cos(a) * 58}" y2="${66 + Math.sin(a) * 58}"/>`; }).join("");
  return `<div class="vault-stage">
    <div class="vault g-${esc(L.grad)} anim-${esc(L.anim)}" id="vault" aria-label="Хранилище «${esc(L.name)}», заперто">
      <div class="shell"></div><div class="inside"></div>
      <div class="door"><div class="face"></div>
        <i class="hinge" style="top:22%"></i><i class="hinge" style="bottom:22%"></i>
        <span class="plate"><span>${esc(L.name)}</span></span>
        <div class="dial"><svg viewBox="0 0 132 132" aria-hidden="true"><g class="ticks">${ticks}</g>
          <g class="grip"><line class="spoke" x1="66" y1="30" x2="66" y2="46"/><line class="spoke" x1="66" y1="86" x2="66" y2="102"/><line class="spoke" x1="30" y1="66" x2="46" y2="66"/><line class="spoke" x1="86" y1="66" x2="102" y2="66"/><circle class="hub" cx="66" cy="66" r="15"/><circle cx="66" cy="66" r="4" fill="#08080a"/></g></svg></div>
        <i class="handle"></i>
        <span class="lamp"><i class="ac-led" id="ac-led"></i><span id="ac-lamp">заперто</span></span>
      </div>
    </div>
    <div class="vault-cap"><h3>${esc(L.name)}</h3>
      <span class="muted">Карточки доступов видны поиску и агенту; пароли — только по Touch ID.</span>
      <div class="btns"><button class="ac-btn pri" data-ac="open">Открыть · Touch ID</button></div></div>
  </div>`;
}
function acCardHTML(c) {
  const ac = state.ac, open = ac.shown[c.id];
  const row = (t, v) => (v ? `<dt>${t}</dt><dd>${esc(v)}</dd>` : "");
  return `<div class="ac-card" style="--c:var(${AC_HUES[acProducts().indexOf(c.product) % AC_HUES.length]})">
    <div class="crumb"><i class="ac-sw"></i>${esc(c.product)}</div>
    <h3>${esc(c.login)} <span class="ac-env ${esc(c.env)}">${esc(c.envName)}</span></h3>
    <dl class="ac-kv">${row("адрес", c.url)}${row("хост", c.host)}${row("роль", c.role)}${row("права", c.rights)}${row("выдал", [c.issuedBy, c.issuedAt].filter(Boolean).join(", "))}${row("заметка", c.note)}
      <dt>пароль</dt><dd><div class="ac-secret"><code class="${open ? "open" : ""}">${open ? esc(open.pw) : "••••••••••••"}</code>
        <button class="ac-btn" data-ac="${open ? "hide" : "show"}" data-id="${esc(c.id)}">${open ? "Скрыть" : "Показать · Touch ID"}</button>
        <button class="ac-btn pri" data-ac="copy" data-id="${esc(c.id)}">Скопировать на 30 с</button>
        ${open ? `<span class="ac-cd" data-cd="${esc(c.id)}">скроется через ${open.left} с</span>` : ""}</div></dd></dl>
    <div class="ac-note">Пароль — в Связке ключей этого Мака; агенту через MCP уходит только карточка, без пароля и заметки.
      ${ac.confirmDel === c.id ? `<span class="ac-del">Снять доступ ${esc(c.login)}? Пароль уйдет из Связки, это не отменить. <button class="ac-btn" data-ac="del-yes" data-id="${esc(c.id)}">Да, снять · Touch ID</button> <button class="ac-btn" data-ac="del-no">Нет</button></span>`
        : `<button class="ac-btn ac-del-btn" data-ac="del" data-id="${esc(c.id)}">Удалить</button>`}</div></div>`;
}
const acProducts = () => [...new Set(state.ac.list.map((c) => c.product))];
function acAddHTML() {
  const f = (k, t, type = "text") => `<label>${t}<input name="${k}" type="${type}" autocomplete="off" spellcheck="false"></label>`;
  return `<form class="ac-add" id="ac-add">
    ${f("product", "продукт")}<label>среда<select name="env"><option value="prod">прод</option><option value="stage">стенд</option><option value="test">тест</option><option value="dev">dev</option></select></label>
    ${f("login", "логин")}${f("url", "адрес")}${f("host", "хост")}${f("role", "роль")}${f("rights", "права")}${f("issuedBy", "кто выдал")}${f("issuedAt", "когда выдан", "date")}${f("note", "заметка (видна только в окне)")}
    ${f("password", "пароль", "password")}
    <div class="acts"><button type="button" class="ac-btn" data-ac="add-cancel">Отмена</button><button class="ac-btn pri" type="submit">Сохранить</button></div></form>`;
}
function acInsideHTML() {
  const ac = state.ac, L = acLook(), sec = acLockSec();
  const list = ac.list.filter((c) => !ac.env || c.env === ac.env);
  const cur = list.find((c) => c.id === ac.sel) || list[0];
  const prods = acProducts();
  const groups = [...new Set(list.map((c) => c.product))].map((p) => {
    const items = list.filter((c) => c.product === p), n = items.length;
    return `<div class="ac-grp" style="--c:var(${AC_HUES[prods.indexOf(p) % AC_HUES.length]})"><div class="ac-gh"><b>${esc(p)}</b><span class="n">${n} ${plural(n, "доступ", "доступа", "доступов")}</span></div>
      ${items.map((c) => `<button class="ac-acc" data-ac="sel" data-id="${esc(c.id)}" aria-selected="${cur && c.id === cur.id}"><span>${esc(c.login)}</span><span class="ac-env ${esc(c.env)}">${esc(c.envName)}</span><small>${esc(c.role || c.url || c.host)}</small></button>`).join("")}</div>`;
  }).join("");
  return `<div class="ac-inside">
    <div class="ac-meta"><span class="mini"><i class="ac-led on"></i>«${esc(L.name)}» открыто</span>
      <span class="mini">${sec ? `закроется через <b id="ac-idle">${acFmt(ac.left)}</b> без дела` : "само не закрывается"} <button class="ac-btn" data-ac="close">Закрыть</button></span></div>
    <div class="ac-find"><input id="ac-q" type="search" placeholder="найти доступ: продукт, среда, логин, роль, адрес" value="${esc(ac.q)}" autocomplete="off" spellcheck="false">
      ${["", "prod", "stage", "test", "dev"].map((e) => `<button class="look-chip" data-ac="env" data-env="${e}" aria-pressed="${ac.env === e}">${e ? { prod: "прод", stage: "стенд", test: "тест", dev: "dev" }[e] : "все"}</button>`).join("")}
      <button class="ac-btn pri" data-ac="add">Добавить доступ</button></div>
    ${ac.adding ? acAddHTML() : ""}
    ${list.length ? `<div class="ac-grid"><div class="ac-list" role="listbox" aria-label="Доступы">${groups}</div>${acCardHTML(cur)}</div>` : `<p class="muted">${ac.list.length ? "Под этот фильтр доступов нет." : "Доступов пока нет: «Добавить доступ» или kbengine creds add в терминале."}</p>`}
  </div>`;
}
function acRender() {
  const ac = state.ac;
  $("#ac-body").innerHTML = ac.open ? acInsideHTML() : acVaultHTML();
  if (ac.open && document.activeElement && document.activeElement.id !== "ac-q") { /* фокус не трогаем */ }
}
async function acOpen() {
  const ac = state.ac;
  if (ac.busy) return;
  if (!native() || !native().AccessOpen) { showError("Доступы работают в окне приложения."); return; }
  ac.busy = true;
  try { await native().AccessOpen(); }
  catch (e) {
    ac.busy = false;
    const v = $("#vault"); v.classList.remove("shake"); void v.offsetWidth; v.classList.add("shake");
    showError(errText(e));
    return;
  }
  const L = acLook(), k = L.anim === "none" ? 0 : L.anim === "short" ? 0.4 : 1, v = $("#vault");
  $("#ac-lamp").textContent = "открываю"; v.classList.add("spin"); await acWait(750 * k);
  v.classList.add("unbolt"); $("#ac-led").classList.add("on"); $("#ac-lamp").textContent = "открыто"; await acWait(420 * k);
  v.classList.add("open"); await acWait(900 * k);
  await acLoad();
  Object.assign(ac, { open: true, busy: false, left: acLockSec() });
  acIdleStart();
  acRender();
}
function acClose() {
  const ac = state.ac;
  for (const id of Object.keys(ac.shown)) { clearInterval(ac.timers[id]); delete ac.shown[id]; }
  clearInterval(ac.idle);
  Object.assign(ac, { open: false, adding: false, confirmDel: "", list: [] });
  if (native() && native().AccessClose) native().AccessClose().catch(() => {});
  acRender();
}
function acIdleStart() {
  const ac = state.ac;
  clearInterval(ac.idle);
  if (!acLockSec()) return;
  ac.idle = setInterval(() => {
    ac.left--;
    const el = $("#ac-idle"); if (el) el.textContent = acFmt(ac.left);
    if (ac.left <= 0) acClose();
  }, 1000);
}
// Любое действие на экране доступов откладывает автозакрытие.
for (const ev of ["click", "keydown", "input"]) $("#access-view").addEventListener(ev, () => { if (state.ac.open) state.ac.left = acLockSec(); }, true);
async function acShow(id) {
  const ac = state.ac;
  try {
    const pw = await native().AccessReveal(id);
    ac.shown[id] = { pw, left: 30 };
    clearInterval(ac.timers[id]);
    ac.timers[id] = setInterval(() => {
      const s = ac.shown[id]; if (!s) return;
      if (--s.left <= 0) { acHide(id); return; }
      document.querySelectorAll(`[data-cd="${CSS.escape(id)}"]`).forEach((e) => (e.textContent = `скроется через ${s.left} с`));
    }, 1000);
    acRender();
  } catch (e) { showError(errText(e)); }
}
function acHide(id) { const ac = state.ac; clearInterval(ac.timers[id]); delete ac.shown[id]; acRender(); }
async function acCopy(id) {
  try { await native().AccessCopy(id); toast("Скопировано", " · пароль в буфере на 30 с, менеджеры буфера его не сохранят"); }
  catch (e) { showError(errText(e)); }
}
$("#access-view").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-ac]"); if (!b) return;
  const ac = state.ac, id = b.dataset.id;
  switch (b.dataset.ac) {
    case "open": acOpen(); break;
    case "close": acClose(); break;
    case "sel": ac.sel = id; ac.confirmDel = ""; acRender(); break;
    case "env": ac.env = b.dataset.env; ac.confirmDel = ""; acRender(); break;
    case "show": acShow(id); break;
    case "hide": acHide(id); break;
    case "copy": acCopy(id); break;
    case "add": ac.adding = !ac.adding; acRender(); break;
    case "add-cancel": ac.adding = false; acRender(); break;
    case "del": ac.confirmDel = id; acRender(); break;
    case "del-no": ac.confirmDel = ""; acRender(); break;
    case "del-yes":
      if (ac.deleting) break; // второй «Да», пока идет первый, — не второй Touch ID и не ложная ошибка
      ac.deleting = true; b.disabled = true;
      try {
        await native().AccessDelete(id);
        ac.confirmDel = ""; delete ac.shown[id]; clearInterval(ac.timers[id]);
        await acLoad(); acRender();
        toast("Снято", " · доступ удален: карточка и пароль из Связки");
      } catch (err) { showError("Доступ не снят: " + errText(err)); }
      finally { ac.deleting = false; }
      break;
  }
});
let acQT = 0;
$("#access-view").addEventListener("input", (e) => {
  if (e.target.id !== "ac-q") return;
  state.ac.q = e.target.value;
  clearTimeout(acQT);
  acQT = setTimeout(async () => { await acLoad(); acRender(); const q = $("#ac-q"); q.focus(); q.setSelectionRange(q.value.length, q.value.length); }, 200);
});
$("#access-view").addEventListener("submit", async (e) => {
  if (e.target.id !== "ac-add") return;
  e.preventDefault();
  const fd = Object.fromEntries(new FormData(e.target)), pw = fd.password;
  delete fd.password;
  e.target.elements.password.value = ""; // пароль не остается в поле
  try {
    await native().AccessAdd(fd, pw);
    state.ac.adding = false;
    await acLoad();
    acRender();
    toast("Сохранено", " · доступ добавлен, пароль — в Связке ключей");
  } catch (err) { showError("Доступ не сохранен: " + errText(err)); }
});

// «Оформление → Доступы»: выбор значений сохраняется в config.json (accents.look).
function renderLookAccess() {
  const host = $("#look-access"); if (!host) return;
  const L = acLook(), editable = !!native(), dis = editable ? "" : " disabled";
  const chip = (k, v, label, pressed, extra = "") => `<button class="look-chip" data-look="${k}" data-v="${esc(v)}" aria-pressed="${pressed}"${dis}>${extra}${esc(label)}</button>`;
  const SW = { spectrum: "var(--z-sp1),var(--z-sp3),var(--z-sp4),var(--z-sp6)", gold: "var(--z-sp1),var(--z-sp2)", ice: "var(--z-sp4),var(--z-sp5),var(--z-sp6)", dusk: "var(--z-sp2),var(--z-sp3),var(--z-src-violet)", mono: "#55555d,#9a9aa4" };
  const customLock = !AC_LOCKS.some(([v]) => v === L.lock);
  host.innerHTML = `
    <div class="row"><span>Название на табличке</span><div class="opts">${AC_NAMES.map((n) => chip("vault-name", n, n, L.name === n)).join("")}
      <label class="muted">свое <input id="look-name" type="text" maxlength="40" value="${AC_NAMES.includes(L.name) ? "" : esc(L.name)}" style="width:140px"${dis}></label></div></div>
    <div class="row"><span>Градиент сейфа</span><div class="opts">${Object.entries(AC_GRADS).map(([g, n]) => chip("vault-grad", g, n, L.grad === g, `<i class="gs" style="background:linear-gradient(90deg,${SW[g]})"></i>`)).join("")}</div></div>
    <div class="row"><span>Открытие</span><div class="opts">${[["full", "полное, 2 с"], ["short", "короткое, 0,8 с"], ["none", "без анимации"]].map(([v, n]) => chip("vault-anim", v, n, L.anim === v)).join("")}</div></div>
    <div class="row"><span>Закрывать без дела через</span><div class="opts">${AC_LOCKS.map(([v, n]) => chip("vault-lock", v, n, L.lock === v)).join("")}
      <label class="muted">свое, мин <input id="look-lock" type="number" min="1" max="1440" value="${customLock ? esc(L.lock) : ""}"${dis}></label></div></div>`;
}
function saveLook(k, v) {
  const look = { ...(state.accents.look || {}), [k]: v };
  saveAccents({ ...state.accents, look });
  if (!state.ac.open) acRender(); else if (k === "vault-lock") { state.ac.left = acLockSec(); acIdleStart(); acRender(); }
}
$("#look-access").addEventListener("click", (e) => {
  const b = e.target.closest("[data-look]"); if (!b || !native()) return;
  saveLook(b.dataset.look, b.dataset.v);
});
$("#look-access").addEventListener("change", (e) => {
  if (!native()) return;
  if (e.target.id === "look-name" && e.target.value.trim()) saveLook("vault-name", e.target.value.trim().slice(0, 40));
  if (e.target.id !== "look-lock") return;
  const n = Math.round(+e.target.value);
  if (n >= 1 && n <= 1440) saveLook("vault-lock", String(n));
});
