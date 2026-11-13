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
// Привязки Go — через обертку (bindings.js, #505): о сохранении, которое ждет
// ответа дольше 10 с, окно говорит, а не молчит.
const SLOW_SAVE_TEXT = "ответа нет дольше 10 с. Если macOS спрашивает доступ к Связке ключей — ответьте. Если ответа так и нет — перезапустите окно (⌘Q)";
const native = () => window.go && window.go.main && window.go.main.App &&
  withSlowNotice(window.go.main.App, () => toast("Сохранение ждет", " · " + SLOW_SAVE_TEXT, null, "bad"));
// errText is the words of a refused call: Wails отдает отказ Go объектом
// Error, и String(err) показал бы человеку «Error: …».
// Длинное сообщение (решение владельца 09.11) не растягивает экран: одна строка с «…»
// и кнопка «Подробнее», полный текст — окошком поверх, его можно скопировать.
const CLAMP_AT = 90;
function moreDialog(title, text) {
  const ov = document.createElement("div");
  ov.className = "more-ov";
  ov.innerHTML = `<div class="more-card" role="dialog" aria-modal="true" aria-label="${esc(title)}"><div class="more-head"><b>${esc(title)}</b><button class="more-x" type="button" aria-label="Закрыть">✕</button></div><pre class="more-text"></pre><div class="more-acts"><button class="btn" type="button" data-more="copy">Копировать</button><button class="btn primary" type="button" data-more="close">Закрыть</button></div></div>`;
  ov.querySelector(".more-text").textContent = text;
  const close = () => { ov.remove(); document.removeEventListener("keydown", onKey, true); };
  const onKey = (e) => { if (e.key === "Escape") { e.stopPropagation(); close(); } };
  ov.addEventListener("click", (e) => {
    if (e.target === ov || e.target.closest(".more-x, [data-more=close]")) close();
    else if (e.target.closest("[data-more=copy]")) {
      const b = e.target.closest("button");
      (navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject()).then(() => { b.textContent = "Скопировано"; }, () => { b.textContent = "Не вышло: выделите и скопируйте"; });
    }
  });
  document.addEventListener("keydown", onKey, true);
  document.body.append(ov);
  ov.querySelector("[data-more=close]").focus();
}
// setClamped puts text into el: короткий как есть, длинный — строкой с «…» и кнопкой.
function setClamped(el, text, title) {
  el.replaceChildren();
  if (text.length <= CLAMP_AT) { el.textContent = text; el.classList.remove("clamped"); return; }
  const line = document.createElement("span");
  line.className = "clamp-line";
  line.textContent = text;
  const b = document.createElement("button");
  b.type = "button"; b.className = "clamp-more"; b.textContent = "Подробнее";
  b.addEventListener("click", () => {
    // «Нет ответа: …» — начало до двоеточия становится заголовком окошка, остальное телом.
    const m = text.match(/^([^:\n]{3,40}):\s+([\s\S]+)$/);
    moreDialog(m ? m[1] : title || "Сообщение", m ? m[2] : text);
  });
  el.classList.add("clamped");
  el.append(line, b);
}
const errText = (e) => (e instanceof Error ? e.message : String(e));
const openFile = (p) => native() && native().Open(p);
const revealFile = (p) => native() && native().Reveal(p);
const openURL = (u) => (window.runtime ? window.runtime.BrowserOpenURL(u) : window.open(u, "_blank"));

// --- Подсветка форм слова ------------------------------------------------------
// Те же окончания, что срезает движок (internal/usecase/filesearch): подсветка
// обязана находить «договора» по запросу «договор», как и поиск.
const ENDINGS = ["иями","ями","ами","ого","его","ому","ему","ыми","ими","ией","ием","иях","ях","ах","ов","ев","ей","ой","ий","ый","ая","яя","ое","ее","ые","ие","ую","юю","ом","ем","ам","ям","ия","ию","ии","ых","их","а","я","о","е","ы","и","у","ю","ь"];
function stem(w) {
  if (!/[а-яе]/i.test(w)) return w;
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
// буквальные тоже: разбор запроса в окне не повторяет нормы движка (е и е,
// запрос из одной буквы), и «Ежик» по «еж» иначе остался бы без подсветки.
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
  { name: "Артефакты", kind: "view", value: "artifacts" },
];
const TYPE_TIP = {
  kb: "Записи каталога и kb-серверов",
  doc: "Найденные файлы папок с текстом: заметки, PDF, Word, HTML, таблицы",
  code: "Найденные файлы папок с исходным кодом и настройками",
  image: "Найденные картинки папок: текст на них распознается",
};
const TYPE_NAME = { kb: "база", doc: "док", code: "код", image: "картинки" };
// Экраны окна среди вкладок поиска (desktopconfig.TabView): двигаются и убираются,
// как вкладки по типу (решение владельца 13.11). Одно описание на экран (ревью #596).
const SCREEN_TABS = {
  artifacts: { short: "артефакты", tip: "Документы из папок и их версии: у каждой написано, что в нее добавили", title: "#art-title" },
};
state.tabs = DEFAULT_TABS;
// Ключ вкладки — вид и значение (desktopconfig.Tab.Key), а не номер: после
// удаления или перестановки активной иначе стала бы соседняя. Вкладку
// неизвестного вида (ее записала новая сборка) окно хранит, но не показывает.
// Исключение — экран: его ключ голое имя («artifacts»), как до 13.11, на нем
// держится весь код экрана; Go пишет view:artifacts только в config.json.
// Имена экранов не совпадают с all, search и module:* — их набор закрыт (tabViews).
const tabKey = (t) => (t.kind === "view" ? t.value : t.kind + ":" + t.value);
const visibleTab = (t) => (t.kind === "type" && TYPE_NAME[t.value] !== undefined) || (t.kind === "view" && SCREEN_TABS[t.value] !== undefined) || t.kind === "source";
const kbItem = (e) => ({ type: "kb", e });
const fileItem = (f) => ({ type: "file", f });
const tabItems = (t, kb, files) =>
  t.kind === "view" ? [] // экран, а не выдача
  : t.kind === "type"
    ? t.value === "kb" ? kb.map(kbItem) : files.filter((f) => f.kind === t.value).map(fileItem)
    : [...kb.filter((e) => e._src === t.value).map(kbItem), ...files.filter((f) => f._src === t.value).map(fileItem)];
// Модули окна (#435): вкладка модуля — после вкладок поиска, ключ module:<id>.
// Сборка знает только эти; модуль новой сборки в config.json вкладки не дает.
const MODULES = {
  chat: { name: "Чат", what: "вопросы к своим документам — ответ со ссылками на записи и файлы", view: "chat",
    icon: '<path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5v7a2.5 2.5 0 0 1-2.5 2.5H10l-4.5 4v-4H6.5A2.5 2.5 0 0 1 4 13.5z"/>' },
  finances: { name: "Финансы", what: "счета, траты, отчеты — с kb-сервера или своего kbengine serve", src: "/app/?tab=finances", // сразу раздел финансов, а не главная витрины
    icon: '<circle cx="12" cy="12" r="8"/><path d="M10 16.5V7.5h3a2.5 2.5 0 0 1 0 5H8.5M8.5 14.5h5"/>' },
  stats: { name: "Статистика", what: "токены на вход и на выход, вызовы модели, ошибки — по дням, назначениям и моделям", view: "stats",
    icon: '<path d="M4 20h16M7 16.5V12M12 16.5V6.5M17 16.5V9.5"/>' },
  access: { name: "Доступы", what: "доступы к продуктам: карточки видны, пароль — только по Touch ID", view: "access",
    icon: '<rect x="5" y="10.5" width="14" height="9.5" rx="1.5"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>' },
  memory: { name: "Память", what: "правила и факты для агентов: горячий слой с потолком, холодный, журнал и очередь на одобрение", view: "memory",
    icon: '<path d="M12 5a3 3 0 1 0-5.997.125 4 4 0 0 0-2.526 5.77 4 4 0 0 0 .556 6.588A4 4 0 1 0 12 18Z"/><path d="M12 5a3 3 0 1 1 5.997.125 4 4 0 0 1 2.526 5.77 4 4 0 0 1-.556 6.588A4 4 0 1 1 12 18Z"/><path d="M15 13a4.5 4.5 0 0 1-3-4 4.5 4.5 0 0 1-3 4M17.6 6.5a3 3 0 0 0 .4-1.4M6 5.1a3 3 0 0 0 .4 1.4M3.5 10.9a4 4 0 0 1 .6-.4M19.9 10.5a4 4 0 0 1 .6.4M6 18a4 4 0 0 1-2-.5M20 17.5a4 4 0 0 1-2 .5"/>' },
  browser: { name: "Браузер", what: "обычный браузер этого Mac во вкладке: страницу смотрите и кликаете вы; включается кнопкой над страницей (из терминала — kbengine browser serve)", src: "/browser/",
    icon: '<circle cx="12" cy="12" r="8"/><path d="M4 12h16M12 4c2.5 2.3 3.7 5 3.7 8s-1.2 5.7-3.7 8c-2.5-2.3-3.7-5-3.7-8S9.5 6.3 12 4Z"/>' },
  markets: { name: "Маркеты", what: "ниши Wildberries и Ozon из выгрузки сервиса аналитики: продажи, выручка, продавцы, рост", view: "markets",
    // Тележка, а не сумка: сумка в 16 px читалась как мусорная корзина (замечание владельца 13.11).
    icon: '<path d="M3.5 4.5h2.2l2.1 10.3a1.5 1.5 0 0 0 1.5 1.2h8.1a1.5 1.5 0 0 0 1.5-1.1l1.6-6.4H6.6"/><circle cx="9.8" cy="19.6" r="1.3"/><circle cx="16.6" cy="19.6" r="1.3"/>' },
  help: { name: "Справка", what: "команды движка и клавиши окна — поиск по задаче, копирование в один клик", view: "help",
    icon: '<circle cx="12" cy="12" r="8"/><path d="M9.6 9.6a2.5 2.5 0 1 1 3.4 2.3c-.6.3-1 .8-1 1.5v.6M12 16.8v.2"/>' },
};
// Значок модуля без своего (новая сборка добавила модуль, а значок забыли) — квадрат, а не пустое место.
const MODULE_ICON = '<rect x="5" y="5" width="14" height="14" rx="2"/>';
const SEARCH_ICON = '<circle cx="10.5" cy="10.5" r="6"/><path d="m15 15 5 5"/>';
state.modules = [];
state.mm = { data: null, alert: "", sel: "health", busy: false, confirm: "", hv: { view: "pulse", day: null, rule: null, jf: "all" } }; // экран «Память»
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
  if (t.kind === "view") return SCREEN_TABS[t.value]?.tip || "";
  const s = sourceOf(t);
  return s ? `Записи и файлы источника ${label(s)}` : "Источника больше нет в списке — вкладка пуста";
}
// Полоса вкладок строится заново при смене набора; подчеркивание остается.
// Полоса и выдача заново: набор вкладок или список источников сменился.
function refreshTabs() { renderTabs(); render(); }
// Шапка (решение владельца 07.11, прототип outputs/2026-11-07_dvk-header-variants.html):
// по умолчанию вариант 1 — наверху Поиск и модули со значками, вкладки поиска
// чипами под строкой поиска. Остальные четыре варианта, вид подписей и градиент
// подчеркивания выбираются в «Оформление → Шапка» (accents.look). Кнопка «Поиск»
// ведет на последнюю вкладку поиска, а не на «Все».
const HDR_MODES = [
  ["rows", "Два ряда", "модули сверху, вкладки поиска чипами под строкой"],
  ["icons", "Модули значками справа", "вкладки поиска текстом, модули значками у правого края"],
  ["more", "Меню «еще»", "все в одну строку, что не влезло — в «еще ▾»"],
  ["side", "Боковая панель", "модули в полосе слева, шапка — поиску"],
  ["switch", "Переключатель в начале", "кнопка раздела открывает сетку модулей, ⌘1…⌘9"],
];
const HDR_LABELS = [["both", "текст и значок"], ["icon", "только значок"], ["text", "только текст"]];
const HDR_INKS = [["cool", "Холодный"], ["spectrum", "Спектр DVK"], ["gold", "Золото"], ["ice", "Лед"], ["dusk", "Закат"], ["mono", "Без цвета"]];
const hdrLook = () => {
  const l = (state.accents && state.accents.look) || {};
  const pick = (list, k, d) => (list.some(([v]) => v === l[k]) ? l[k] : d);
  return { mode: pick(HDR_MODES, "header-mode", "rows"), labels: pick(HDR_LABELS, "header-labels", "both"), ink: pick(HDR_INKS, "header-ink", "cool") };
};
// navOrder — то, что ⌘1…⌘9 выбирают в любом виде шапки: Поиск, потом модули.
const navOrder = () => ["search", ...moduleKeys()];
state.searchTab = "all";
const closeHdrPop = () => {
  document.querySelectorAll(".hdr-pop").forEach((p) => (p.hidden = true));
  document.querySelectorAll(".hdr-more-btn, .hdr-switch-btn").forEach((b) => b.setAttribute("aria-expanded", "false"));
};
function renderTabs() {
  // Переименованный экран — тем же именем и в заголовке экрана (ревью #596).
  for (const [v, d] of Object.entries(SCREEN_TABS)) {
    const t = state.tabs.find((x) => x.kind === "view" && x.value === v), h = $(d.title);
    if (h) h.textContent = t ? t.name : DEFAULT_TABS.find((x) => x.kind === "view" && x.value === v).name;
  }
  const nav = $("#tabs"), ink = nav.querySelector(".tab-ink"), chips = $("#chips"), bar = $(".bar"), L = hdrLook();
  nav.querySelectorAll(":scope > :not(.tab-ink)").forEach((x) => x.remove());
  bar.querySelectorAll(".hdr-more").forEach((x) => x.remove());
  chips.textContent = "";
  $("#app").dataset.hdr = L.mode;
  bar.dataset.lbl = L.labels;
  document.body.dataset.ink = L.ink;
  if (!tabKeys().includes(state.tab)) state.tab = "all";
  if (!tabKeys().includes(state.searchTab) || moduleOf(state.searchTab)) state.searchTab = "all";
  const btn = (key, name, tip, icon) => {
    const b = document.createElement("button");
    b.dataset.tab = key;
    b.dataset.tip = name;
    b.dataset.tipB = tip;
    const n = navOrder().indexOf(key) + 1;
    if (n > 0 && n <= 9) b.dataset.tipK = "⌘ " + n;
    if (icon) b.setAttribute("aria-label", name); // у вкладки поиска имя — подпись с числом найденного
    b.innerHTML = `${icon ? `<svg viewBox="0 0 24 24" aria-hidden="true">${icon}</svg>` : ""}<span class="lbl">${esc(name)}</span>`;
    if (key === "module:memory") b.insertAdjacentHTML("beforeend", mmDot());
    return b;
  };
  // Вкладка поиска: с числом найденного (его пишет render()).
  const stab = (key, name, tip) => {
    const b = btn(key, name, tip);
    b.dataset.stab = "";
    b.append(" ", document.createElement("i"));
    return b;
  };
  const searchTabs = () => [stab("all", "Все", "Записи базы и найденные файлы папок — все вкладки вместе"),
    // Экран («Артефакты») — без числа найденного: он ищет сам.
    ...state.tabs.filter(visibleTab).map((t) => (t.kind === "view" ? btn : stab)(tabKey(t), t.name, tabTip(t)))];
  const searchBtn = () => btn("search", "Поиск", "Записи базы и файлы ваших папок; вкладки — под строкой поиска", SEARCH_ICON);
  const mods = () => moduleKeys().map((k) => { const m = MODULES[moduleOf(k)]; return btn(k, m.name, m.what, m.icon || MODULE_ICON); });
  const put = (...xs) => xs.forEach((x) => nav.insertBefore(x, ink));
  if (L.mode === "icons") {
    put(...searchTabs());
    const group = Object.assign(document.createElement("span"), { className: "hdr-mods" });
    group.append(...mods());
    put(group);
  } else if (L.mode === "more") {
    put(...searchTabs(), ...mods());
  } else if (L.mode === "switch") {
    const cur = moduleOf(state.tab) ? MODULES[moduleOf(state.tab)] : null;
    const sw = document.createElement("span");
    sw.className = "hdr-switch";
    sw.innerHTML = `<button type="button" class="hdr-switch-btn" aria-haspopup="true" aria-expanded="false" data-tip="Разделы" data-tip-b="Поиск и модули · ⌘1…⌘9"><svg viewBox="0 0 24 24" aria-hidden="true">${cur ? cur.icon || MODULE_ICON : SEARCH_ICON}</svg><span class="nm">${esc(cur ? cur.name : "Поиск")}</span><span class="car">▾</span>${mmDot()}</button><div class="hdr-pop hdr-grid" hidden></div>`;
    sw.querySelector(".hdr-grid").append(...[searchBtn(), ...mods()].map((b, i) => {
      if (i < 9) b.append(Object.assign(document.createElement("kbd"), { textContent: "⌘" + (i + 1) }));
      return b;
    }));
    put(sw);
    if (!cur) put(...searchTabs());
    nav.dataset.sec = moduleOf(state.tab) || "search";
  } else {
    // rows и side: Поиск и модули — кнопками, вкладки поиска — чипами.
    put(searchBtn(), ...mods());
    chips.append(...searchTabs());
  }
  // «еще ▾» есть в любом виде: в «Меню еще» туда уходит все, что не влезло, в
  // остальных — текстовые вкладки, когда даже значки не помогли (прокрутки нет).
  const more = Object.assign(document.createElement("div"), { className: "cell hdr-more", hidden: true });
  more.innerHTML = `<button type="button" class="hdr-more-btn" aria-expanded="false" aria-haspopup="true"></button><div class="hdr-pop" hidden></div>`;
  nav.after(more);
  fitBar();
}
const pickTab = (key) => {
  closeHdrPop();
  state.tab = key === "search" ? state.searchTab : key;
  state.sel = 0;
  render();
};
$(".bar").addEventListener("click", (e) => {
  const opener = e.target.closest(".hdr-more-btn, .hdr-switch-btn");
  if (opener) {
    const pop = opener.nextElementSibling, open = pop.hidden, r = opener.getBoundingClientRect();
    closeHdrPop();
    pop.hidden = !open;
    // Окошко фиксированное (полоса вкладок обрезает свое содержимое) — место ему задается от кнопки.
    pop.style.top = r.bottom + 6 + "px";
    pop.style.left = Math.max(8, Math.min(r.left, innerWidth - pop.offsetWidth - 8)) + "px";
    opener.setAttribute("aria-expanded", String(open));
    return;
  }
  const b = e.target.closest("button[data-tab]");
  if (b) pickTab(b.dataset.tab);
});
$("#chips").addEventListener("click", (e) => { const b = e.target.closest("button"); if (b) pickTab(b.dataset.tab); });
document.addEventListener("click", (e) => { if (!e.target.closest(".hdr-more, .hdr-switch")) closeHdrPop(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && document.querySelector(".hdr-pop:not([hidden])")) { e.stopPropagation(); closeHdrPop(); } }, true);
// ⌘1 — Поиск, ⌘2… — модули по порядку (подсказка каждой кнопки их называет).
document.addEventListener("keydown", (e) => {
  if (!e.metaKey || e.altKey || e.ctrlKey || !/^[1-9]$/.test(e.key) || settingsOpen()) return; // под открытыми настройками модуль не сменится незаметно
  const key = navOrder()[+e.key - 1];
  if (!key) return;
  e.preventDefault();
  pickTab(key);
});
function moveInk() {
  if (!moduleOf(state.tab)) state.searchTab = state.tab;
  const nav = $("#tabs"), ink = $(".tab-ink");
  // Раздел сменился не через pickTab (Tab по вкладкам, выключенный модуль) — у
  // «Переключателя» другое название и другой ряд вкладок.
  if (hdrLook().mode === "switch" && nav.dataset.sec !== (moduleOf(state.tab) || "search")) return renderTabs();
  const row = [...nav.querySelectorAll(":scope > button, .hdr-mods > button")];
  // Выбранная ушла в «еще» — строка пересчитывается, выбранная остается снаружи.
  if (row.some((x) => x.hidden && x.dataset.tab === state.tab)) return fitBar();
  // Подчеркнута кнопка строки (не копия в окошке): вкладка поиска, если она в строке, иначе Поиск или модуль.
  const sel = (k) => row.find((x) => x.dataset.tab === k && !x.hidden);
  const b = sel(state.tab) || (!moduleOf(state.tab) && sel("search"));
  document.querySelectorAll(".bar button[data-tab]").forEach((x) => x.classList.toggle("active", x.dataset.tab === state.tab || (x.dataset.tab === "search" && !moduleOf(state.tab))));
  document.querySelectorAll("#chips button").forEach((x) => x.classList.toggle("active", x.dataset.tab === state.tab));
  const more = $(".hdr-more-btn");
  if (more) more.classList.toggle("has", !!document.querySelector(`.hdr-more .hdr-pop button[data-tab="${state.tab}"]`));
  ink.hidden = !b || hdrLook().mode === "side";
  if (ink.hidden) return;
  ink.style.left = b.offsetLeft + "px";
  ink.style.width = b.offsetWidth + "px";
}
// Шапка не прокручивается никогда (решение владельца 07.11). Порядок сжатия: в
// «Двух рядах» — как только место справа от последней кнопки меньше отступа слева
// от первой, подписи у кнопок со значком прячутся; тесно и так — короче счет
// файлов, потом «источники» значком; «N проблем» видно всегда. «Только текст»
// уступает тому же правилу: тесно — значки возвращаются. Что не влезло и так —
// с конца уходит в «еще ▾»; выбранная кнопка остается в строке. В «Меню еще»
// подписи не прячутся: туда сразу уходит лишнее.
// ponytail: в «еще» уходят кнопки строки, но не кнопка «Переключателя» и не «Все»
// (первая кнопка). Замер 07.11 на 4 модулях: от MinWidth окна 760 px и шире
// прокрутки нет ни в одном виде; на 480 px «Модули справа» и «Переключатель»
// вылезают. Модулей станет больше — поднять MinWidth по замеру или уводить в
// «еще» и первую вкладку.
function fitBar() {
  const BAR_STEPS = ["compact", "tight", "tighter"]; // внутри: setIndexed зовет fitBar раньше, чем дойдет до этой строки
  const bar = $(".bar"), nav = $("#tabs"), mode = hdrLook().mode, more = $(".hdr-more");
  // Открытое окошко «еще» не перестраивается под курсором (опрос индексации — каждые 3 с).
  if (document.querySelector(".hdr-more .hdr-pop:not([hidden])")) return moveInk();
  bar.classList.remove(...BAR_STEPS);
  const row = [...nav.querySelectorAll(":scope > button, .hdr-mods > button")];
  row.forEach((b) => (b.hidden = false));
  const group = nav.querySelector(".hdr-mods");
  const syncGroup = () => group && (group.hidden = !row.some((b) => b.parentElement === group && !b.hidden)); // пустая группа не держит разделитель
  syncGroup();
  if (more) more.hidden = true;
  if (mode !== "side" && row.length) {
    const box = () => nav.getBoundingClientRect();
    const scrolls = () => nav.scrollWidth > nav.clientWidth + 1;
    const spare = () => {
      const all = [...nav.querySelectorAll("button")].filter((b) => !b.closest(".hdr-pop") && !b.hidden);
      return box().right - Math.max(...all.map((b) => b.getBoundingClientRect().right)) >= all[0].getBoundingClientRect().left - box().left;
    };
    const fits = () => !scrolls() && (mode !== "rows" || spare());
    for (const step of mode === "more" ? BAR_STEPS.slice(1) : BAR_STEPS) {
      if (fits()) break;
      bar.classList.add(step);
    }
    if (more && scrolls()) {
      // Кнопка «еще» сама занимает место — мерить с ней и с самой длинной надписью.
      more.hidden = false;
      more.querySelector(".hdr-more-btn").textContent = `еще ${row.length} ▾`;
      const gone = [];
      for (let i = row.length - 1; i > 0 && scrolls(); i--) {
        if (row[i].dataset.tab === state.tab) continue;
        row[i].hidden = true;
        gone.unshift(row[i]);
        syncGroup();
      }
      more.querySelector(".hdr-more-btn").textContent = `еще ${gone.length} ▾`;
      const pop = more.querySelector(".hdr-pop");
      pop.innerHTML = `<small>не поместились</small>`;
      pop.append(...gone.map((b) => Object.assign(b.cloneNode(true), { hidden: false })));
      more.hidden = !gone.length;
    }
  }
  moveInk();
}
new ResizeObserver(() => fitBar()).observe($(".bar"));

// --- Отрисовка -----------------------------------------------------------------
// Отметки и сравнение «Артефактов» (#547, шаг 2): что выбрано на экране; сами
// заметки и значки хранит Go (version-marks.json рядом с config.json).
const verUI = { fams: [], pick: [], cmp: null, noteFor: "", noteDraft: null, painting: false, describe: "", busy: new Set(), drafts: {} }; // busy — версии, по которым модель пишет описание; drafts — ее готовые ответы, не открытые в поле // noteDraft — набранное в поле заметки, пока список перерисовывается
let verSeq = 0, verTimer = 0, verSeen = false, verFresh = false; // «Артефакты»: verSeen — вкладка уже была открыта в прошлой отрисовке; verFresh — вход ждет свежего обхода
function render() {
  // Фильтр источника оставляет только его записи: у базы знаний — записи,
  // у папки — ее файлы.
  const only = state.filter;
  const kb = only ? state.kb.filter((e) => e._src === only) : state.kb;
  const files = only ? state.files.filter((f) => f._src === only) : state.files;
  // Счетчик — длина того же списка, что покажет вкладка.
  document.querySelectorAll("[data-stab]").forEach((b) => (b.querySelector("i").textContent = state.q ? itemsOf(b.dataset.tab, kb, files).length : ""));
  moveInk();
  // «Артефакты» занимают место выдачи: строка поиска и вкладки остаются, запрос идет в них.
  const art = state.tab === "artifacts";
  $("#app").classList.toggle("art-open", art);
  $("#artifacts-view").hidden = !art;
  // Вне «Артефактов» (в том числе на вкладке-модуле) вход сюда снова будет первым,
  // а отложенная загрузка уже никому не нужна.
  if (!art) { verSeen = false; verFresh = false; clearTimeout(verTimer); }
  if (showModule(moduleOf(state.tab))) return;
  // Скрытый список выдачи не должен отвечать на стрелки и Enter.
  if (art) { state.items = []; renderArtifacts(); return; }

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
  // В модуле выдача поиска скрыта: стрелки и Enter открывали бы ее старую строку,
  // буквы уводили бы фокус в скрытое поле (ревью #509). Tab листает вкладки, как
  // везде; Esc прячет окно — скрытый запрос он не трогает.
  if ($("#app").classList.contains("module-open") && e.key !== "Tab") {
    if (e.key === "Escape" && !e.defaultPrevented && window.runtime) window.runtime.Hide();
    return;
  }
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
  renderFinServer(); // серверы в выборе «Финансы берут данные из»
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
  storage: { title: "Хранилище", cmd: "storage" },
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
  if (view === "storage") {
    await renderStorage();
    $("#close-settings").focus();
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
// renderConnections lists the window's accesses; «Изменить» открывает сам доступ
// там, где он живет: kb-сервер — его строку «Источников» в правке, модель — поле
// ключа в «Чат и модель» (#496).
// «Хранилище» (#543): размер того, что окно копит, и очистка по пункту. Подтверждение
// стоит в самой строке и называет, что пропадет; общей кнопки «очистить все» нет.
function sizeText(b) {
  if (b < 1024) return `${b} Б`;
  if (b < 1048576) return `${fmt(Math.round(b / 1024))} КБ`;
  if (b < 1073741824) return `${(b / 1048576).toLocaleString("ru-RU", { maximumFractionDigits: b < 10485760 ? 1 : 0 })} МБ`;
  return `${(b / 1073741824).toLocaleString("ru-RU", { maximumFractionDigits: 1 })} ГБ`;
}
// Вкладка «Артефакты» (#547): список приходит из Go целиком, запрос фильтрует там же;
// запрос — строка поиска окна. Обход папок читает и хеширует документы, поэтому
// свежим он идет только по устаревшему минутному кэшу.
function renderArtifacts() {
  // Вход на вкладку собирает документы заново, набор запроса — из минутного кэша
  // и после паузы в наборе: обход папок читает и хеширует файлы.
  if (!verSeen) verFresh = true; // не теряется, если до таймера случится еще одна отрисовка
  verSeen = true;
  clearTimeout(verTimer);
  verTimer = setTimeout(() => { const fresh = verFresh; verFresh = false; loadArtifacts(fresh); }, verFresh ? 0 : 200);
}
async function loadArtifacts(fresh) {
  if (state.tab !== "artifacts") return; // таймер дожил до ухода с вкладки
  const box = $("#ver-list"), q = state.q || "";
  if (!native() || !native().Versions) { box.innerHTML = `<p class="muted">Артефакты видны в окне приложения.</p>`; return; }
  const seq = ++verSeq;
  if (!box.firstChild) box.innerHTML = `<p class="muted">Собираю документы…</p>`;
  let fams = [];
  const live = () => seq === verSeq && state.tab === "artifacts"; // ответ не перетирает ни новый запрос, ни другой экран
  try { fams = (await native().Versions(q, fresh)) || []; } catch (e) { if (live()) { $("#count").textContent = ""; box.innerHTML = `<p class="muted">${esc(errText(e))}</p>`; } return; }
  if (!live()) return;
  $("#count").textContent = fams.length ? `${fams.length} ${plural(fams.length, "документ", "документа", "документов")}` : "";
  if (!fams.length) { box.innerHTML = `<p class="muted">${q ? "Ничего не нашлось." : "Документов с несколькими версиями нет. Одинокий файл находится по имени в строке поиска."}</p>`; return; }
  verUI.fams = fams;
  if (fresh) verUI.cmp = null; // свежий обход: показанное сравнение могло устареть
  const known = new Set(fams.flatMap((f) => f.versions.map((v) => v.path)));
  verUI.pick = verUI.pick.filter((p) => known.has(p));
  if (verUI.cmp && !(known.has(verUI.cmp.oldPath) && known.has(verUI.cmp.newPath))) verUI.cmp = null;
  verPaint();
}

const verBtn = (act, path, label, title, extra = "") => `<button class="ver-act" data-act="${act}" data-path="${esc(path)}" title="${esc(title)}" ${extra}>${label}</button>`;
// prevOf — с чем сравнивать версию j для описания: ближайшая прежняя в том же формате, иначе просто прежняя.
function prevOf(f, j) {
  const ext = (p) => p.split(".").pop().toLowerCase();
  for (let k = j - 1; k >= 0; k--) if (ext(f.versions[k].path) === ext(f.versions[j].path)) return f.versions[k];
  return j > 0 ? f.versions[j - 1] : null;
}
function verDescribeBtn(f, j, v) {
  if (!prevOf(f, j)) return "";
  if (verUI.busy.has(v.path)) return `<button class="ver-act" disabled>Модель пишет…</button>`;
  if (verUI.describe !== v.path) return verBtn("describe", v.path, "Описать", "Модель предложит заметку о том, что изменилось");
  return `<span class="ver-consent">Строки, которых нет в другой версии, уйдут провайдеру модели чата. Файлы целиком не уходят. ${
    verBtn("describe-go", v.path, "Отправить", "Спросить модель")}${verBtn("describe-no", v.path, "Отмена", "Не отправлять")}</span>`;
}
function verRow(v, fi, j, f) {
  const mark = verUI.noteFor === v.path
    ? `<input class="ver-note-in" data-note-in="${esc(v.path)}" maxlength="500" value="${esc(verUI.noteDraft ?? v.mark ?? "")}" placeholder="Заметка, например: отправлено Алексею" aria-label="Заметка к версии">`
    : v.mark ? `<span class="ver-mark">${esc(v.mark)}</span>` : "";
  return `<div class="ver-row${v.current ? " cur" : ""}"><span class="ver-date">${esc(v.date)}</span><span class="ver-note">${v.current ? `<b class="ver-badge">актуальная</b> ` : ""}${esc(v.note)} · ${sizeText(v.size)}</span><span class="ver-path">${esc(v.path)}</span>${
    (v.dups || []).map((d) => `<span class="ver-dup">${esc(d)}</span>`).join("")}${mark}<span class="ver-acts">${
    verBtn("open", v.path, "Открыть", "Открыть файл")}${verBtn("preview", v.path, "Просмотр", "Быстрый просмотр (Quick Look)")}${verBtn("reveal", v.path, "В Finder", "Показать в Finder")}${
    verBtn("current", v.path, v.current ? "Снять «актуальная»" : "Актуальная", "Один значок на документ", `data-on="${v.current ? 1 : 0}"`)}${verBtn("note", v.path, "Заметка", "Заметка к версии")}${verDescribeBtn(f, j, v)}<label class="ver-pick"><input type="checkbox" data-act="pick" data-path="${esc(v.path)}" data-fam="${fi}" ${verUI.pick.includes(v.path) ? "checked" : ""}> сравнить</label></span></div>`;
}
function verCmpHTML(c) {
  const col = (title, lines, cls) => `<div class="ver-col"><h5>${esc(title)}</h5>${lines.map((l) => `<div class="ver-line${l.c ? " " + cls : ""}">${esc(l.t)}</div>`).join("") || `<p class="muted">Текста нет.</p>`}</div>`;
  return `<div class="ver-cmp"><div class="ver-cmp-bar"><b>Сравнение версий</b>${verBtn("cmp-close", "", "Закрыть", "Закрыть сравнение")}</div>${
    c.view.truncated ? `<p class="muted">Показаны первые строки каждой версии, остальное не влезло.</p>` : ""}<div class="ver-cols">${
    col(c.oldTitle, c.view.old, "del")}${col(c.newTitle, c.view.new, "add")}</div><p class="muted">Подсвечены строки, которых нет в другой версии. Перестановка абзацев не видна.</p></div>`;
}
function verPaint() {
  // Перерисовка не должна отнимать у человека поле, в котором он печатает.
  const was = document.activeElement?.matches?.("[data-note-in]") ? document.activeElement : null;
  const caret = was ? [was.selectionStart, was.selectionEnd] : null;
  verUI.painting = true; // снятие фокуса с удаляемого поля — не «сохранить»
  $("#ver-list").innerHTML = verUI.fams.map((f, fi) => {
    const picked = f.versions.filter((v) => verUI.pick.includes(v.path));
    const cmp = verUI.cmp && f.versions.some((v) => v.path === verUI.cmp.oldPath) ? verCmpHTML(verUI.cmp) : "";
    return `<section class="ver-fam"><h4>${esc(f.name)} · ${f.versions.length} ${plural(f.versions.length, "версия", "версии", "версий")}${
      picked.length === 2 ? ` ${verBtn("compare", "", "Сравнить выбранные", "Две отмеченные версии рядом", `data-fam="${fi}"`)}` : ""}</h4>${f.versions.map((v, j) => verRow(v, fi, j, f)).join("")}${cmp}</section>`;
  }).join("");
  const now = was && $("#ver-list [data-note-in]");
  if (now) { now.focus(); now.setSelectionRange(caret[0], caret[1]); }
  verUI.painting = false;
}
const baseName = (p) => p.split("/").pop();
async function verCompare(fi) {
  const vs = verUI.fams[fi].versions.filter((v) => verUI.pick.includes(v.path)); // в порядке ленты: старая первой
  if (vs.length !== 2) return;
  const view = await native().VersionCompare(vs[0].path, vs[1].path);
  verUI.cmp = { oldPath: vs[0].path, newPath: vs[1].path, oldTitle: `${vs[0].date} · ${baseName(vs[0].path)}`, newTitle: `${vs[1].date} · ${baseName(vs[1].path)}`, view };
  verPaint();
}
function verPick(path, fi, on) {
  const inFam = new Set(verUI.fams[fi].versions.map((v) => v.path));
  verUI.pick = verUI.pick.filter((p) => inFam.has(p) && p !== path); // выбор живет в одном семействе
  if (on) verUI.pick.push(path);
  if (verUI.pick.length > 2) verUI.pick.shift(); // третья вытесняет самую раннюю отметку
  verPaint();
}
// verDescribe asks the model for a note about what changed; ответ идет в поле
// заметки на правку и сам не сохраняется. Согласие человека уже получено кнопкой.
async function verDescribe(path) {
  const f = verUI.fams.find((x) => x.versions.some((v) => v.path === path));
  const j = f.versions.findIndex((v) => v.path === path), prev = prevOf(f, j);
  verUI.describe = ""; verUI.busy.add(path); verPaint();
  let text = "";
  try { text = await native().VersionDescribe(prev.path, path, true); } catch (e) { toast("Описание не получено", errText(e), null, "bad"); }
  verUI.busy.delete(path);
  if (text) {
    // Ответ не затирает то, что человек набирает в другой заметке: он ждет в черновиках до «Заметка».
    if (verUI.noteFor && verUI.noteFor !== path) { verUI.drafts[path] = text; toast("Описание готово", "Откройте «Заметка» у этой версии"); } else { verUI.noteFor = path; verUI.noteDraft = text; }
  }
  verPaint();
  $("#ver-list [data-note-in]")?.focus();
}
async function verSaveNote(input) {
  const path = input.dataset.noteIn;
  if (verUI.noteFor !== path) return; // Esc или повторный вызов
  verUI.noteFor = "";
  verUI.noteDraft = null;
  try { await native().VersionMark(path, input.value); } catch (e) { toast("Заметка не сохранена", errText(e), null, "bad"); }
  loadArtifacts(false);
}
$("#ver-list").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-act]");
  if (!b) return;
  const act = b.dataset.act, path = b.dataset.path;
  try {
    if (act === "open") await openFile(path);
    else if (act === "reveal") await revealFile(path);
    else if (act === "preview") await native().Preview(path);
    else if (act === "current") { await native().VersionCurrent(path, b.dataset.on !== "1"); await loadArtifacts(false); }
    else if (act === "note") { verUI.noteFor = path; verUI.noteDraft = verUI.drafts[path] ?? null; delete verUI.drafts[path]; verPaint(); $("#ver-list [data-note-in]")?.focus(); }
    else if (act === "pick") verPick(path, +b.dataset.fam, b.checked);
    else if (act === "compare") await verCompare(+b.dataset.fam);
    else if (act === "cmp-close") { verUI.cmp = null; verPaint(); }
    else if (act === "describe") { verUI.describe = path; verPaint(); }
    else if (act === "describe-no") { verUI.describe = ""; verPaint(); }
    else if (act === "describe-go") await verDescribe(path);
  } catch (err) { toast("Артефакты", errText(err), null, "bad"); }
});
$("#ver-list").addEventListener("keydown", (e) => {
  const input = e.target.closest("[data-note-in]");
  if (!input) return;
  if (e.key === "Enter") { e.preventDefault(); verSaveNote(input); }
  else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); verUI.noteFor = ""; verUI.noteDraft = null; verPaint(); }
});
$("#ver-list").addEventListener("input", (e) => { if (e.target.matches("[data-note-in]")) verUI.noteDraft = e.target.value; });
$("#ver-list").addEventListener("focusout", (e) => { const input = e.target.closest("[data-note-in]"); if (input && !verUI.painting) verSaveNote(input); });

let storageConfirm = null;
async function renderStorage() {
  const list = $("#storage-list");
  if (!native() || !native().StorageItems) { list.innerHTML = `<li class="muted">Хранилище видно в окне приложения.</li>`; return; }
  let rows = [];
  try { rows = (await native().StorageItems()) || []; } catch (e) { list.innerHTML = `<li class="muted">${esc(errText(e))}</li>`; return; }
  const total = rows.reduce((n, r) => n + r.bytes, 0);
  list.innerHTML = rows.map((r) => {
    const ask = storageConfirm === r.id ? `<span class="conn-confirm">${esc(r.loses)}
      <button class="btn" data-st-yes="${esc(r.id)}">${r.danger ? "Стереть" : "Очистить"}</button><button class="btn" data-st-no>Отмена</button></span>` : "";
    return `<li data-danger="${r.danger}">
    <span class="n">${esc(r.name)}</span>
    <span class="st">${sizeText(r.bytes)}</span>
    <span class="d">${fmt(r.files)} ${plural(r.files, "файл", "файла", "файлов")}</span>
    <span class="conn-acts"><button class="btn" data-st-ask="${esc(r.id)}" ${r.bytes ? "" : "disabled"}>${r.danger ? "Стереть…" : "Очистить…"}</button></span>${ask}</li>`;
  }).join("") + `<li class="storage-total">Всего: ${sizeText(total)}</li>`;
}
$("#storage-list").addEventListener("click", async (e) => {
  const ask = e.target.closest("[data-st-ask]"), yes = e.target.closest("[data-st-yes]");
  try {
    if (ask) storageConfirm = ask.dataset.stAsk;
    else if (e.target.closest("[data-st-no]")) storageConfirm = null;
    else if (yes) {
      const name = yes.closest("li").querySelector(".n").textContent;
      if (yes.dataset.stYes === "chats" && chatState.busy) throw new Error("Модель отвечает: дождитесь конца ответа, иначе чат запишется заново.");
      await native().StorageClear(yes.dataset.stYes);
      storageConfirm = null;
      if (yes.dataset.stYes === "chats") { // открытый чат и список остались бы в окне призраками
        chatState.id = ""; chatState.chat = null;
        await renderChatList();
        renderChat();
      }
      toast("Очищено", ` · «${name}»`, null, "del");
    } else return;
  } catch (err) { storageConfirm = null; showError(errText(err)); }
  renderStorage();
});
const CONN_KIND = { model: "модель", kbserver: "kb-сервер", google: "Google Диск", speech: "распознавание речи", ozon: "кабинет Ozon", mpstats: "MPStats" };
// CHAT_CONN — подключения, которые чат спрашивает (кабинет Ozon, MPStats): у них
// кнопка «Чату» и плашка в ряду «ищет в».
const CHAT_CONN = { ozon: "Ozon", mpstats: "MPStats" };
function connLabel(c) { return `${CHAT_CONN[c.kind] || c.kind} · ${c.name || c.kind}`; }
// Что случится после удаления — у каждого вида свое (ревью #605).
const CONN_DEL_NOTE = {
  ozon: "Ключ сотрется из Связки ключей, «Магазин» перестанет загружать этот кабинет.",
  mpstats: "Ключ сотрется из Связки ключей, «Прогноз → Из MPStats» перестанет работать.",
  speech: "Ключ сотрется из Связки ключей, диктовка вернется к Apple Speech.",
  google: "Вход сотрется из Связки ключей, закрытые ссылки перестанут читаться.",
};
// Итоги «Проверить» по id подключения: строка под подключением до перерисовки окна.
const connChecks = {};
let connConfirm = null;
async function renderConnections() {
  const list = $("#conn-list");
  if (!native() || !native().Connections) { list.innerHTML = `<li class="muted">Подключения видны в окне приложения.</li>`; return; }
  let rows = [];
  try { rows = (await native().Connections()) || []; } catch (e) { list.innerHTML = `<li class="muted">${esc(errText(e))}</li>`; return; }
  setChatConns(rows); // ряд «ищет в» чата видит разрешение сразу
  list.innerHTML = rows.map((r) => {
    const what = r.kind === "mpstats" ? "цифры ниш и рынка из MPStats" : "цифры магазина";
    const share = CHAT_CONN[r.kind] ? `<button class="btn" data-conn-share="${esc(r.id)}" data-on="${!r.share}"${mkTip(r.share ? "Чату разрешено" : "Чату запрещено", r.share ? `модель чата видит ${what}, они уходят ее провайдеру; нажмите — запретить` : `модель чата не видит ${what}; нажмите — разрешить: ${r.kind === "mpstats" ? "категории и продажи рынка" : "названия товаров, заказы и продажи"} уйдут провайдеру модели`)}>${r.share ? "Чату: запретить" : "Чату: разрешить"}</button>` : "";
    const chk = `<button class="btn" data-conn-check="${esc(r.id)}"${mkTip("Проверить", "живой запрос туда, куда ходит подключение: принят ли ключ или вход, отвечает ли сервер")}>Проверить</button>`;
    const acts = r.own
      ? `${chk}${share}<button class="btn" data-conn-toggle="${esc(r.id)}" data-on="${!r.enabled}"${mkTip(r.enabled ? "Выключить" : "Включить", r.enabled ? "окно перестанет ходить сюда; ключ останется в Связке ключей" : "окно снова будет пользоваться подключением")}>${r.enabled ? "Выключить" : "Включить"}</button><button class="btn" data-conn-del="${esc(r.id)}"${mkTip("Удалить", "подключение и его ключ сотрутся из Связки ключей; спросим еще раз")}>Удалить</button>`
      : `${chk}<button class="btn" data-go-sec="${esc(r.section)}" data-go-id="${esc(r.id)}"${mkTip("Изменить", "открыть раздел, где подключение правится")}>Изменить</button>`;
    const ck = connChecks[r.id];
    const ckLine = ck ? `<span class="conn-check${ck.busy ? "" : ck.ok ? " ok" : " bad"}" role="status">${ck.busy ? "проверяю…" : (ck.ok ? "✓ " : "✕ ") + esc(ck.note)}</span>` : "";
    const confirm = connConfirm === r.id ? `<span class="conn-confirm">Удалить «${esc(r.name)}»? ${CONN_DEL_NOTE[r.kind] || CONN_DEL_NOTE.google}
      <button class="btn" data-conn-del-yes="${esc(r.id)}">Удалить</button><button class="btn" data-conn-del-no>Отмена</button></span>` : "";
    return `<li class="${r.own && !r.enabled ? "off" : ""}">
    <span class="n">${esc(r.name)}<small>${esc(CONN_KIND[r.kind] || r.kind)}</small></span>
    <span class="st${r.ok ? " ok" : ""}">${esc(r.status)}</span>
    <span class="d">${esc(r.detail)} · использует: ${esc((r.usedBy || []).join(", "))}</span>
    <span class="conn-acts">${acts}</span>${ckLine}${confirm}</li>`;
  }).join("");
}
async function openConnection(sec, id) {
  await openSettings(sec);
  if (sec === "chat") {
    $("#chat-key").closest("details").open = true; // раздел «Модель» мог быть свернут
    return focusSoon("#chat-key");
  }
  if (sec !== "sources") return;
  const s = draft.sources.find((x) => x.id === id);
  if (s) return startEdit(s._key);
  showError("Этого источника нет в несохраненном списке — сохраните или отмените правки, и строка найдется.");
}
$("#conn-list").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-go-sec]");
  if (b) return openConnection(b.dataset.goSec, b.dataset.goId);
  const t = e.target.closest("[data-conn-toggle]");
  const name = e.target.closest("li")?.querySelector(".n")?.firstChild?.textContent || "подключение";
  const ck = e.target.closest("[data-conn-check]");
  if (ck) {
    const id = ck.dataset.connCheck;
    hideTip();
    connChecks[id] = { busy: true };
    renderConnections();
    let r;
    try { r = await native().CheckConnection(id); } catch (err) { r = { ok: false, note: errText(err) }; }
    connChecks[id] = r;
    toast(r.ok ? "Проверено" : "Не прошла проверка", ` · «${name}»: ${r.note}`, null, r.ok ? "info" : "bad");
    return renderConnections();
  }
  try {
    const sh = e.target.closest("[data-conn-share]");
    if (sh) {
      const on = sh.dataset.on === "true";
      await native().SetOzonShare(sh.dataset.connShare, on);
      toast(name, on ? " · чату разрешено" : " · чату запрещено", null, on ? "info" : "off");
    } else if (t) {
      const on = t.dataset.on === "true";
      await native().SetConnectionEnabled(t.dataset.connToggle, on);
      toast(name, on ? " · включено" : " · выключено", null, on ? "info" : "off");
    }
    else if (e.target.closest("[data-conn-del]")) connConfirm = e.target.closest("[data-conn-del]").dataset.connDel;
    else if (e.target.closest("[data-conn-del-no]")) connConfirm = null;
    else if (e.target.closest("[data-conn-del-yes]")) { await native().RemoveConnection(e.target.closest("[data-conn-del-yes]").dataset.connDelYes); connConfirm = null; toast("Удалено", ` · «${name}» и его ключ`, null, "del"); }
    else return;
  } catch (err) { showError(errText(err)); toast("Не получилось", ` · «${name}»: ${errText(err)}`, null, "bad"); }
  renderConnections();
});
// maskConnKeys прячет набранные ключи подключений: после «Добавить» (удачного
// или нет), при сворачивании формы и закрытии настроек — открытым ключ не остается.
function maskConnKeys() { for (const id of ["conn-s-key", "conn-o-key", "conn-g-secret", "conn-m-key"]) maskKey(id); }
$("#conn-add-mpstats").addEventListener("click", () => {
  maskConnKeys();
  const f = $("#conn-mpstats");
  f.hidden = !f.hidden;
  $("#conn-add-mpstats").setAttribute("aria-expanded", String(!f.hidden));
  if (!f.hidden) $("#conn-m-key").focus();
});
$("#conn-mpstats").addEventListener("submit", async (e) => {
  e.preventDefault();
  maskConnKeys();
  if (!native() || !native().AddMPStatsConnection) return void ($("#conn-m-note").textContent = "Подключения меняются в окне приложения.");
  const go = $("#conn-m-go");
  go.disabled = true;
  $("#conn-m-note").textContent = "Проверяю ключ в MPStats…";
  try {
    const r = await native().AddMPStatsConnection($("#conn-m-name").value, $("#conn-m-key").value);
    $("#conn-m-note").textContent = "Ключ принят и сохранен: " + r.name + ". «Маркеты → Прогноз» возьмет нишу из MPStats.";
    toast("Добавлено", ` · «${r.name}»: ключ MPStats принят`);
    for (const id of ["#conn-m-name", "#conn-m-key"]) $(id).value = "";
  } catch (err) {
    $("#conn-m-note").textContent = errText(err);
    toast("Не добавлено", " · " + errText(err), null, "bad");
  } finally { go.disabled = false; }
  renderConnections();
});
$("#conn-add-speech").addEventListener("click", () => {
  maskConnKeys();
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
  } catch (err) { $("#conn-s-note").textContent = errText(err); toast("Не добавлено", ` · Сервер распознавания: ${errText(err)}`, null, "bad"); }
  maskConnKeys();
  renderConnections();
});
function showOzonForm() {
  const f = $("#conn-ozon");
  f.hidden = false;
  $("#conn-add-ozon").setAttribute("aria-expanded", "true");
  focusSoon("#conn-o-id");
}
$("#mk-conn").addEventListener("click", async () => { hideTip(); await openSettings("conn"); showOzonForm(); });
$("#conn-add-ozon").addEventListener("click", () => {
  maskConnKeys();
  if (!$("#conn-ozon").hidden) { $("#conn-ozon").hidden = true; $("#conn-add-ozon").setAttribute("aria-expanded", "false"); return; }
  showOzonForm();
});
$("#conn-ozon").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!native() || !native().AddOzonConnection) return void ($("#conn-o-note").textContent = "Подключения меняются в окне приложения.");
  try {
    const r = await native().AddOzonConnection($("#conn-o-name").value, $("#conn-o-id").value, $("#conn-o-key").value, $("#conn-o-share").checked);
    $("#conn-o-note").textContent = "Добавлено: " + r.name + ". Загрузите его в «Маркеты → Магазин».";
    toast("Добавлено", ` · «${r.name}»`);
    for (const id of ["#conn-o-name", "#conn-o-id", "#conn-o-key"]) $(id).value = "";
    $("#conn-o-share").checked = false;
  } catch (err) { $("#conn-o-note").textContent = errText(err); toast("Не добавлено", ` · Кабинет Ozon: ${errText(err)}`, null, "bad"); }
  maskConnKeys();
  renderConnections();
});
$("#conn-add-google").addEventListener("click", () => {
  maskConnKeys();
  const f = $("#conn-google");
  f.hidden = !f.hidden;
  $("#conn-add-google").setAttribute("aria-expanded", String(!f.hidden));
  if (!f.hidden) $("#conn-g-id").focus();
});
$("#conn-google").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!native() || !native().ConnectGoogle) return void ($("#conn-g-note").textContent = "Подключение Google — в окне приложения.");
  maskConnKeys(); // ожидание согласия — до 5 минут: секрет не висит открытым
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
    toast("Не подключено", ` · Google: ${errText(err)}`, null, "bad");
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
  maskConnKeys();
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
// глаз ключей модели и подключений — та же иконка, что у токенов
for (const b of document.querySelectorAll("button[data-eye]")) b.innerHTML = EYE;
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
  // Пока открыты «Доступы», панель настроек скрыта: ошибка в ее строке никому не видна
  // («поменял прод на стенд, сохранить — и ничего не происходит»).
  const el = $(!$("#access-view").hidden ? "#ac-error" : "#settings-error");
  setClamped(el, msg || "", "Сообщение");
  el.hidden = !msg;
}

// --- Подсказка с «Вернуть» -------------------------------------------------------
let toastT, undoFn = null;
function toast(head, rest, undo, tone = "info") {
  const t = $("#toast");
  undoFn = undo;
  t.dataset.tone = tone;
  t.classList.remove("show"); void t.offsetWidth; // перезапуск полоски
  const long = head.length + rest.length > CLAMP_AT + 20;
  t.innerHTML = `<span class="glow"></span><span class="toast-text${long ? " clamp-line" : ""}"><b>${esc(head)}</b>${esc(rest)}</span>` +
    (long ? `<button id="toast-more">Подробнее</button>` : "") +
    (undo ? `<button id="undo">Вернуть</button>` : "") + `<span class="life"></span>`;
  if (long) t.querySelector("#toast-more").onclick = () => moreDialog(head, rest.replace(/^ · /, ""));
  t.classList.add("show");
  clearTimeout(toastT);
  // Беда с советом читается дольше, чем «Сохранено».
  toastT = setTimeout(() => t.classList.remove("show"), tone === "bad" ? 12000 : 4000);
  t.onmouseenter = () => clearTimeout(toastT);
  t.onmouseleave = () => { toastT = setTimeout(() => t.classList.remove("show"), 1200); };
}
// Ошибки окна из bindings.js — подсказкой; накопленные до этой строки — тоже.
// Причину видно в отладочной сборке just desktop-inspect (Inspect Element).
windowErrors.show = (head, text) => toast(head, " · " + text + " — причина: just desktop-inspect, правый клик в поле ввода → Inspect Element", null, "bad");
windowErrors.queue.splice(0).forEach(([h, t]) => windowErrors.show(h, t));
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
// startEdit opens a source row for editing — и из строки, и из «Подключения → Изменить».
function startEdit(key) {
  ui.editing = key;
  ui.replacing = ui.confirmDel = null;
  renderSources();
  requestAnimationFrame(() => $(`[data-key="${key}"]`)?.scrollIntoView({ block: "nearest" }));
  focusSoon(`#en-${key}`);
}

panel.addEventListener("click", async (e) => {
  // data-eye, а не data-reveal: тот же атрибут у «В Finder» в «Проблемах»
  // нес путь файла, и этот обработчик искал элемент с id-путем и падал.
  const eye = e.target.closest("[data-eye]");
  if (eye) {
    setEye(eye.dataset.eye, $("#" + eye.dataset.eye).type === "password");
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
  if (d.edit) return startEdit(d.edit);
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
    if (t.kind === "view") return `<span class="what">экран · ${esc(SCREEN_TABS[t.value].short)}</span>`;
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
  // «По типу…» — и типы выдачи, и экраны («Артефакты» — экраном, ревью #596), которых нет в наборе.
  const types = [...Object.keys(TYPE_NAME).map((v) => ["type", v, TYPE_NAME[v]]), ...Object.keys(SCREEN_TABS).map((v) => ["view", v, "экран «" + DEFAULT_TABS.find((t) => t.kind === "view" && t.value === v).name + "»"])]
    .filter(([k, v]) => !state.tabs.some((t) => t.kind === k && t.value === v));
  $("#tab-add-type").innerHTML = `<option value="">по типу…</option>` + types.map(([k, v, n]) => `<option value="${k}:${v}">${esc(n)}</option>`).join("");
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
const TYPE_TITLE = Object.fromEntries(DEFAULT_TABS.map((t) => [t.kind + ":" + t.value, t.name]));
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
  const [kind, v] = e.target.value.split(":");
  if (!v) return;
  const before = state.tabs.slice(), name = TYPE_TITLE[kind + ":" + v];
  saveTabs([...state.tabs, { name, kind, value: v }]);
  toast("Вкладка добавлена", ` · «${name}»`, () => saveTabs(before));
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
  ["help-title", "Справка: заголовок", "слово «Справка» спектром"],
  ["help-search", "Справка: поиск", "рамка поиска в фокусе"],
  ["help-nav", "Справка: раздел", "переливающаяся черта у открытого раздела"],
  ["help-anim", "Справка: появление", "строки раздела выезжают по очереди"],
  ["fin-src", "Финансы: источник", "имя источника данных над витриной"],
  ["mm-title", "Память: заголовок", "слово «Память» спектром"],
  ["mm-meter", "Память: шкала", "заполнение горячего слоя спектром; у потолка оранжевая, выше красная"],
  ["mm-nav", "Память: раздел", "переливающаяся черта у открытого раздела"],
  ["mh-signal", "Здоровье: сигнал", "рамка с переливом, когда памяти нужно внимание"],
  ["mh-score", "Здоровье: счет", "число и шкала здоровья переливом"],
  ["mh-heat", "Здоровье: ритм", "клетки ритма градиентом"],
  ["ac-buttons", "Машины: главные кнопки", "«Сканировать машину», «Запустить скан» и «Сохранить» в форме машины"],
  ["scan-run", "Машины: ход скана", "бегущая полоса, пока идет скан по ssh"],
  ["art-buttons", "Артефакты: кнопки версии", "Открыть, Просмотр, В Finder, Актуальная, Заметка, Описать — рамкой градиента; палитра в «Артефакты» ниже"],
  ["art-pick", "Артефакты: «сравнить»", "отметка и подпись цветом палитры"],
  ["mk-title", "Маркеты: заголовок", "слово «Маркеты» градиентом; палитра в «Маркеты» ниже"],
  ["mk-tab", "Маркеты: вкладка", "открытая вкладка рамкой градиента и черта под шапкой"],
  ["mk-score", "Маркеты: оценка", "значок оценки градиентом; без него — цвет по уровню: зеленый, желтый, красный"],
  ["mk-spark", "Маркеты: график сезона", "линия продаж по месяцам градиентом"],
  ["mk-row", "Маркеты: выбранная ниша", "черта градиентом у выбранной строки"],
  ["mk-buttons", "Маркеты: главные кнопки", "«Открыть выгрузку» и «Сравнить» рамкой градиента"],
  ["mk-advice", "Маркеты: советы", "плашка совета цветом его вида: рамка градиентом, заливка и заголовок; без нее — тонкая рамка, цвет остается у значка"],
  ["mk-load", "Маркеты: загрузка кабинета", "заливка полосы «Загрузить из Ozon» градиентом и блик по ней"],
];
const ACCENT_IDS = ACCENTS.map((a) => a[0]);
state.accents = { mode: "", off: [] }; // режим "" — твой выбор: все, кроме off
// offNow — какие акценты выключены сейчас, с учетом режима.
const offNow = (a) => (a.mode === "all" ? [] : a.mode === "none" ? ACCENT_IDS : (a.off || []));
function applyAccents() {
  const off = offNow(state.accents);
  for (const id of ACCENT_IDS) document.body.classList.toggle("no-" + id, off.includes(id));
  acLoginApply(); // цвет плашки логина в «Доступах»
  mhApply(); // палитра, скорость, ползунки и громкость сигнала здоровья памяти
  agApply(); // палитра и поведение кнопок «Артефактов»
  mkApply(); // палитра, анимация, график и плотность «Маркетов»
  refreshTabs(); // вид шапки — тоже в оформлении; render возвращает числа у вкладок
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
  renderLookHeader();
  renderLookHealth();
  renderLookArtifacts();
  renderLookMarkets();
}
// «Оформление → Здоровье памяти»: палитра, перелив и громкость сигнала (accents.look).
// По умолчанию перелив включен: сигнал должен бросаться в глаза.
const MH_GRADS = { spectrum: "Спектр DVK", gold: "Золото", ice: "Лед", dusk: "Закат", mono: "Без цвета", custom: "Свои цвета" };
const MH_ANIMS = [["full", "перелив"], ["none", "без анимации"]];
const MH_SIGNALS = [["loud", "заметный: карточка над экраном"], ["calm", "спокойный: одна строка"]];
// Ползунки: ключ в accents.look, пределы, шаг, единица, подпись, значение по умолчанию.
const MH_RANGES = [
  ["health-speed", 2, 30, 1, " с", "Скорость перелива", 6, "меньше секунд — быстрее; без анимации не действует"],
  ["health-edge", 1, 4, 1, " px", "Толщина рамки сигнала", 2, "у «спокойного» сигнала рамка всегда 1 px"],
  ["health-tint", 0, 20, 1, " %", "Тонировка карточки", 7, "насколько карточка сигнала подкрашена цветом состояния"],
];
const MH_HEX = /^#[0-9a-fA-F]{6}$/;
function mhLook() {
  const l = state.accents.look || {};
  const pick = (v, list, d) => (list.includes(v) ? v : d);
  const num = (k, lo, hi, d) => { const n = Math.round(+l[k]); return Number.isFinite(n) && n >= lo && n <= hi ? n : d; };
  return {
    grad: pick(l["health-grad"], Object.keys(MH_GRADS), "spectrum"), anim: pick(l["health-anim"], MH_ANIMS.map((x) => x[0]), "full"), signal: pick(l["health-signal"], MH_SIGNALS.map((x) => x[0]), "loud"),
    speed: num("health-speed", 2, 30, 6), edge: num("health-edge", 1, 4, 2), tint: num("health-tint", 0, 20, 7),
    c1: MH_HEX.test(l["health-c1"] || "") ? l["health-c1"] : "#f0953e", c2: MH_HEX.test(l["health-c2"] || "") ? l["health-c2"] : "#5aa9e6",
  };
}
// mhApply кладет вид на body: палитру и числа ползунков. Свои цвета — в переменные, остальное — в CSS.
function mhApply(L = mhLook()) {
  const st = document.body.style, b = document.body.dataset;
  b.mhg = L.grad; b.mha = L.anim; b.mhs = L.signal;
  st.setProperty("--mh-speed", L.speed + "s"); st.setProperty("--mh-edge", L.edge + "px"); st.setProperty("--mh-tint", L.tint + "%");
  for (const [i, v] of [[1, L.c1], [2, L.c2], [3, L.c1], [4, L.c2]]) {
    if (L.grad === "custom") st.setProperty("--mh" + i, v); else st.removeProperty("--mh" + i);
  }
}
function renderLookHealth() {
  const host = $("#look-health"); if (!host) return;
  const L = mhLook(), dis = native() ? "" : " disabled";
  const chip = (k, v, label, on, extra = "") => `<button class="look-chip" data-hl="${k}" data-v="${v}" aria-pressed="${on}"${dis}>${extra}${esc(label)}</button>`;
  const grads = Object.entries(MH_GRADS).map(([g, n]) => chip("health-grad", g, n, L.grad === g, g === "custom" ? `<i class="gs" style="background:linear-gradient(90deg,${L.c1},${L.c2})"></i>` : `<i class="gs ink-${g}"></i>`)).join("");
  const colors = L.grad === "custom" ? `<label class="muted">от <input type="color" data-hc="health-c1" value="${L.c1}"${dis}></label> <label class="muted">до <input type="color" data-hc="health-c2" value="${L.c2}"${dis}></label>` : "";
  const ranges = MH_RANGES.map(([k, lo, hi, step, unit, name, , tip]) => `<div class="row"><span>${name}</span><div class="opts look-range"><input type="range" data-hr="${k}" min="${lo}" max="${hi}" step="${step}" value="${L[k.slice(7)]}" aria-label="${name}"${dis}><output>${L[k.slice(7)]}${unit}</output><span class="muted">${tip}</span></div></div>`).join("");
  host.innerHTML = `
    <div class="row"><span>Градиент</span><div class="opts">${grads}${colors}
      <span class="muted">цвета счета, шкалы, ритма и черты раздела; при проблемах сигнал краснеет сам</span></div></div>
    <div class="row"><span>Анимация</span><div class="opts">${MH_ANIMS.map(([v, n]) => chip("health-anim", v, n, L.anim === v)).join("")}</div></div>
    ${ranges}
    <div class="row"><span>Сигнал о проблемах</span><div class="opts">${MH_SIGNALS.map(([v, n]) => chip("health-signal", v, n, L.signal === v)).join("")}
      <span class="muted">точка у кнопки «Память» в шапке остается в любом случае</span></div></div>
    <div class="row"><span></span><div class="opts"><button class="look-chip" data-hl-reset${dis}>Сбросить вид здоровья</button>
      <span class="muted">отдельные элементы включаются выше, в списке «Здоровье: …»</span></div></div>`;
}
const MH_KEYS = ["health-grad", "health-anim", "health-signal", "health-speed", "health-edge", "health-tint", "health-c1", "health-c2"];
const mhSave = (patch) => saveAccents({ ...state.accents, look: { ...(state.accents.look || {}), ...patch } });
$("#look-health").addEventListener("click", (e) => {
  if (!native()) return;
  if (e.target.closest("[data-hl-reset]")) {
    const look = { ...(state.accents.look || {}) };
    for (const k of MH_KEYS) delete look[k];
    saveAccents({ ...state.accents, look });
    toast("Сброшено", " · вид здоровья памяти по умолчанию");
    return;
  }
  const b = e.target.closest("[data-hl]"); if (!b) return;
  mhSave({ [b.dataset.hl]: b.dataset.v });
});
// Ползунок двигается вживую (виден результат), сохраняется при отпускании.
$("#look-health").addEventListener("input", (e) => {
  const r = e.target.closest("[data-hr]");
  if (r) {
    const def = MH_RANGES.find((x) => x[0] === r.dataset.hr);
    r.nextElementSibling.textContent = r.value + def[4];
    mhApply({ ...mhLook(), [r.dataset.hr.slice(7)]: +r.value });
    return;
  }
  const c = e.target.closest("[data-hc]");
  if (c) mhApply({ ...mhLook(), [c.dataset.hc.slice(7)]: c.value });
});
$("#look-health").addEventListener("change", (e) => {
  if (!native()) return;
  const r = e.target.closest("[data-hr]"), c = e.target.closest("[data-hc]");
  if (r) mhSave({ [r.dataset.hr]: r.value });
  else if (c) mhSave({ [c.dataset.hc]: c.value });
});
// «Оформление → Артефакты» (accents.look art-*): палитра кнопок версии, когда
// видна рамка и перелив. Владелец 12.11: в темной теме кнопок не видно, нужна
// гибкость под человека — палитры как у здоровья памяти, свои цвета, тихий режим.
const AG_MODES = [["fill", "рамка и заливка"], ["always", "только рамка"], ["hover", "рамка при наведении"]];
const AG_ANIMS = [["full", "перелив при наведении"], ["none", "без анимации"]];
const AG_KEYS = ["art-grad", "art-mode", "art-anim", "art-c1", "art-c2"];
function agLook() {
  const l = state.accents.look || {};
  const pick = (v, list, d) => (list.includes(v) ? v : d);
  return {
    grad: pick(l["art-grad"], Object.keys(MH_GRADS), "ice"), mode: pick(l["art-mode"], AG_MODES.map((x) => x[0]), "fill"), anim: pick(l["art-anim"], AG_ANIMS.map((x) => x[0]), "full"),
    c1: MH_HEX.test(l["art-c1"] || "") ? l["art-c1"] : "#5e8fd0", c2: MH_HEX.test(l["art-c2"] || "") ? l["art-c2"] : "#8fc79a",
  };
}
function agApply(L = agLook()) {
  const st = document.body.style, b = document.body.dataset;
  b.ag = L.grad; b.agm = L.mode; b.aga = L.anim;
  for (const [i, v] of [[1, L.c1], [2, L.c2], [3, L.c2]]) {
    if (L.grad === "custom") st.setProperty("--ag" + i, v); else st.removeProperty("--ag" + i);
  }
}
function renderLookArtifacts() {
  const host = $("#look-artifacts"); if (!host) return;
  const L = agLook(), dis = native() ? "" : " disabled";
  const chip = (k, v, label, on, extra = "") => `<button class="look-chip" data-al="${k}" data-v="${v}" aria-pressed="${on}"${dis}>${extra}${esc(label)}</button>`;
  const grads = Object.entries(MH_GRADS).map(([g, n]) => chip("art-grad", g, n, L.grad === g, g === "custom" ? `<i class="gs" style="background:linear-gradient(90deg,${L.c1},${L.c2})"></i>` : `<i class="gs ink-${g}"></i>`)).join("");
  const colors = L.grad === "custom" ? `<label class="muted">от <input type="color" data-ac="art-c1" value="${L.c1}"${dis}></label> <label class="muted">до <input type="color" data-ac="art-c2" value="${L.c2}"${dis}></label>` : "";
  host.innerHTML = `
    <div class="row"><span>Градиент</span><div class="opts">${grads}${colors}
      <span class="muted">рамка кнопок версии и отметка «сравнить»</span></div></div>
    <div class="row"><span>Рамка</span><div class="opts">${AG_MODES.map(([v, n]) => chip("art-mode", v, n, L.mode === v)).join("")}
      <span class="muted">при наведении — спокойнее, когда версий много</span></div></div>
    <div class="row"><span>Анимация</span><div class="opts">${AG_ANIMS.map(([v, n]) => chip("art-anim", v, n, L.anim === v)).join("")}</div></div>
    <div class="row"><span></span><div class="opts"><button class="look-chip" data-al-reset${dis}>Сбросить вид Артефактов</button>
      <span class="muted">кнопки и «сравнить» выключаются выше, в списке «Артефакты: …»</span></div></div>`;
}
$("#look-artifacts").addEventListener("click", (e) => {
  if (!native()) return;
  if (e.target.closest("[data-al-reset]")) {
    const look = { ...(state.accents.look || {}) };
    for (const k of AG_KEYS) delete look[k];
    saveAccents({ ...state.accents, look });
    toast("Сброшено", " · вид Артефактов по умолчанию");
    return;
  }
  const b = e.target.closest("[data-al]"); if (!b) return;
  mhSave({ [b.dataset.al]: b.dataset.v }); // mhSave пишет любой ключ accents.look
});
// Цвет меняется вживую, сохраняется при отпускании.
$("#look-artifacts").addEventListener("input", (e) => {
  const c = e.target.closest("[data-ac]"); if (!c) return;
  const L = { ...agLook(), [c.dataset.ac.slice(4)]: c.value };
  agApply(L);
  const sw = $('#look-artifacts [data-v="custom"] .gs'); if (sw) sw.style.background = `linear-gradient(90deg,${L.c1},${L.c2})`; // образец не ждет отпускания
});
$("#look-artifacts").addEventListener("change", (e) => { const c = e.target.closest("[data-ac]"); if (c && native()) mhSave({ [c.dataset.ac]: c.value }); });
// «Оформление → Шапка»: вид, подписи и градиент подчеркивания; порядок модулей —
// в «Модули», порядок вкладок поиска — перетаскиванием в «Вкладки».
function renderLookHeader() {
  const host = $("#look-header"); if (!host) return;
  const L = hdrLook(), dis = native() ? "" : " disabled";
  const chip = (k, v, label, tip = "", pre = "") => `<button class="look-chip" data-hlook="${k}" data-v="${v}" aria-pressed="${L[k.slice(7)] === v}"${tip ? ` data-tip="${esc(label)}" data-tip-b="${esc(tip)}"` : ""}${dis}>${pre}${esc(label)}</button>`;
  host.innerHTML = `
    <div class="row"><span>Вид</span><div class="opts">${HDR_MODES.map(([v, n, w]) => chip("header-mode", v, n, w)).join("")}</div></div>
    <div class="row"><span>Подписи</span><div class="opts">${HDR_LABELS.map(([v, n]) => chip("header-labels", v, n)).join("")}
      <span class="muted">тесно — остаются значки: шапка не прокручивается</span></div></div>
    <div class="row"><span>Подчеркивание</span><div class="opts">${HDR_INKS.map(([v, n]) => chip("header-ink", v, n, "", `<i class="gs ink-${v}"></i>`)).join("")}</div></div>`;
}
$("#look-header").addEventListener("click", (e) => {
  const b = e.target.closest("[data-hlook]"); if (!b || !native()) return;
  saveAccents({ ...state.accents, look: { ...(state.accents.look || {}), [b.dataset.hlook]: b.dataset.v } });
});
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
  if (!mod || mod.view) {
    const f = $("#module-frame");
    // Ушли с «Браузера» на другой экран: скрытая страница иначе продолжала бы опрашивать кадры и рисовать.
    if (f.dataset.shown === "browser") { f.src = "about:blank"; f.dataset.src = ""; }
    f.dataset.shown = "";
  }
  // Подсказка графика не переживает уход с вкладки; stTipHide объявлен ниже и при первом вызове еще недоступен.
  const stTipNow = document.querySelector(".st-tip"); if (stTipNow) stTipNow.hidden = true;
  $("#app").classList.toggle("module-open", !!mod);
  $("#module-view").hidden = !mod || !!mod.view;
  $("#chat-view").hidden = !mod || mod.view !== "chat";
  $("#stats-view").hidden = !mod || mod.view !== "stats";
  $("#access-view").hidden = !mod || mod.view !== "access";
  const mmWasOpen = !$("#memory-view").hidden; // render() зовет showModule часто: не перечитывать память зря
  $("#memory-view").hidden = !mod || mod.view !== "memory";
  const helpWasOpen = !$("#help-view").hidden; // render() зовет showModule часто: не сбивать фокус и выбор
  $("#help-view").hidden = !mod || mod.view !== "help";
  $("#markets-view").hidden = !mod || mod.view !== "markets";
  if (!mod || mod.view !== "access") acLeave();
  if (mod && mod.view === "chat") openChatView();
  else if (mod && mod.view === "stats") openStats();
  else if (mod && mod.view === "access") openAccess();
  else if (mod && mod.view === "memory") { if (!mmWasOpen) openMemory(); }
  else if (mod && mod.view === "help") { if (!helpWasOpen) openHelp(); }
  else if (mod && mod.view === "markets") openMarkets();
  else if (mod) {
    $("#fin-src").hidden = id !== "finances";
    renderFinSrc();
    brOpen(id === "browser");
    const frame = $("#module-frame");
    // Браузер открывается заново каждый раз, когда вкладку выбрали: если serve запустили
    // после первой попытки, пояснение «не запущен» не должно висеть до перезапуска окна.
    const reopen = id === "browser" && frame.dataset.shown !== id;
    frame.dataset.shown = id;
    if (frame.dataset.src !== mod.src || reopen) { frame.src = mod.src; frame.dataset.src = mod.src; frame.title = mod.name; }
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
  renderFinServer();
}
// Откуда «Финансы» берут данные (решение владельца 07.11): первый kb-сервер,
// сервер из «Источников» или свой kbengine serve на этом Mac — адрес вписывают.
const FIN_LOCAL = "http://127.0.0.1:8097";
const isLocalServer = finIsLocal; // правило — в finsource.js
function renderFinServer() {
  const cur = (state.modules.find((x) => x.id === "finances") || {}).server || "";
  const servers = state.known.filter((x) => x.kind === "server");
  const pick = $("#fin-server-pick"), url = $("#fin-server-url");
  const local = isLocalServer(cur);
  const gone = finSource(cur, servers, label).kind === "gone";
  pick.innerHTML = `<option value="">первого kb-сервера из «Источников»</option>` +
    servers.map((x) => `<option value="${esc(x.id)}">сервера «${esc(label(x))}»${x.enabled ? "" : " — выключен"}</option>`).join("") +
    (gone ? `<option value="${esc(cur)}">сервера, которого нет в «Источниках»</option>` : "") +
    `<option value="local">этого Mac — свой kbengine serve</option>`;
  pick.value = local ? "local" : cur;
  pick.disabled = !native();
  if (document.activeElement !== url) url.value = local ? cur : FIN_LOCAL;
  url.hidden = $("#fin-server-save").hidden = !local;
  renderFinSrc();
}
// renderFinSrc — строка над витриной «Финансов»: откуда данные и как сменить.
// Сменить можно только в окне: в браузере выбор в настройках неактивен.
function renderFinSrc() {
  const cur = (state.modules.find((x) => x.id === "finances") || {}).server || "";
  const src = finSource(cur, state.known.filter((x) => x.kind === "server"), label);
  const editable = !!native();
  $("#fin-src-name").textContent = src.text;
  $("#fin-src-hint").textContent = editable && src.kind !== "local" ? "· журнал на этом Mac? выберите «этого Mac — свой kbengine serve»" : "";
  $("#fin-src-change").hidden = !editable;
}
// Состояние serve над витриной: зеленая точка «на связи» или красная «нет связи».
// null — еще не мерили (точки нет). Перемена в любую сторону перечитывает iframe.
let finAlive = null;
function renderFinLive() {
  const el = $("#fin-live");
  el.hidden = finAlive === null || !native();
  el.classList.toggle("off", finAlive === false);
  const text = finAlive ? "на связи" : "нет связи";
  $("#fin-live-t").textContent = text;
  el.setAttribute("aria-label", "Источник данных: " + text + ". Нажмите, чтобы проверить и перечитать");
  el.title = (finAlive ? "Источник отвечает" : "Источник не отвечает: запустите его, окно заметит само") + " · нажмите, чтобы перечитать сейчас";
}
async function probeFin() {
  if (!native() || document.hidden || $("#module-view").hidden || $("#fin-src").hidden) return;
  let code = 0;
  try { code = (await fetch("/app/api/capabilities", { cache: "no-store" })).status; } catch { /* нет ответа — как 0 */ }
  const now = finServeAlive(code);
  const flipped = finServeFlipped(finAlive, now);
  finAlive = now;
  renderFinLive();
  const f = $("#module-frame");
  if (flipped && f.dataset.src) f.src = f.dataset.src;
}
// ponytail: опрос раз в 4 с пока открыты «Финансы» — потолок: лишний запрос к своему serve; апгрейд — событие от Go-процесса (фоновая проверка в окне).
setInterval(probeFin, 4000);
window.addEventListener("focus", probeFin);
$("#fin-live").addEventListener("click", () => { // руками: перечитать витрину, не дожидаясь опроса
  const f = $("#module-frame");
  if (f.dataset.src) f.src = f.dataset.src; // load-обработчик замерит заново
});
$("#module-frame").addEventListener("load", () => { finAlive = null; probeFin(); });
$("#fin-src-change").addEventListener("click", async () => {
  await openSettings("prefs");
  const row = $("#fin-server"), pick = $("#fin-server-pick");
  row.scrollIntoView({ block: "center", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  row.classList.remove("flash"); void row.offsetWidth; row.classList.add("flash"); // повторный клик снова подсвечивает
  clearTimeout(row._flash);
  row._flash = setTimeout(() => row.classList.remove("flash"), 1700); // и без анимации подсветка не остается
  pick.focus();
});
// Управление браузером (вкладка «Браузер»): выбрать, включить, выключить. Окно само
// запускает kbengine browser serve; состояние — из Go раз в 2 с, пока вкладка открыта.
let br = null, brBusy = false, brErr = "", brWasRunning = null;
function brOpen(on) {
  $("#br-bar").hidden = !on || !native();
  if (on) pollBrowser(); else { $("#br-hint").hidden = $("#br-err").hidden = true; brWasRunning = null; }
}
async function pollBrowser() {
  if (!native() || document.hidden || $("#br-bar").hidden) return;
  try { br = await native().BrowserState(); } catch { return; }
  const running = br.State.Running;
  const f = $("#module-frame");
  if (brWasRunning !== null && running !== brWasRunning && f.dataset.src) f.src = f.dataset.src; // включили или выключили: страница перечитывается
  brWasRunning = running;
  renderBrowser();
}
function renderBrowser() {
  if (!br) return;
  const { Installed: list = [], Chosen: chosen, Auto: auto, State: st } = br;
  const locked = st.Running || brBusy;
  const chips = [{ Name: "Автоматически", Path: "" }, ...list];
  $("#br-chips").innerHTML = chips.map((b) => `<button class="chip" type="button" data-br-path="${esc(b.Path)}" aria-pressed="${b.Path === chosen}" ${locked ? "disabled" : ""}>${esc(b.Name)}</button>`).join("");
  const sw = $("#br-sw");
  const foreign = st.Running && !st.Own;
  sw.setAttribute("aria-checked", String(st.Running || brBusy));
  sw.setAttribute("aria-label", st.Running || brBusy ? "Браузер включен: выключить" : "Браузер выключен: включить");
  sw.disabled = !list.length || foreign;
  const [cls, text] = brBusy ? ["wait", "запускается…"]
    : st.Running ? ["on", foreign ? "включен из терминала" : "включен"]
    : brErr || st.Why ? ["bad", "не запустился"] : ["", "выключен"];
  $("#br-st").className = "br-st " + cls;
  $("#br-st").innerHTML = `<i></i>${esc(text)}`;
  const names = list.map((b) => b.Name).join(", ");
  const hint = $("#br-hint");
  hint.hidden = false;
  const name = (list.find((b) => b.Path === chosen) || {}).Name;
  hint.innerHTML = !list.length ? "Не нашла браузер на движке Chromium (Chrome, Brave, Edge, Chromium, Vivaldi): поставьте любой, он появится здесь."
    : foreign ? "Браузер запущен в терминале: остановите его там (Ctrl+C), окно чужой процесс не выключает."
    : (chosen ? `Выбран <b class="fin-src-name">${esc(name)}</b>: запустится он, а не первый найденный.`
      : `<b>Автоматически</b> значит: окно само берет первый найденный на этом Mac браузер, сейчас это <b class="fin-src-name">${esc(auto)}</b>.`) + ` Найдено: ${esc(names)}.`;
  hint.innerHTML = `<span>${hint.innerHTML}</span>`; // одним куском: у .fin-src между детьми промежуток
  const why = brErr || (!st.Running ? st.Why : "");
  $("#br-err").hidden = !why;
  if (why) setClamped($("#br-err"), why, "Не удалось запустить браузер");
}
$("#br-chips").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-br-path]");
  if (!b || b.disabled) return;
  try { await native().BrowserChoose(b.dataset.brPath); brErr = ""; } catch (err) { brErr = errText(err); }
  pollBrowser();
});
$("#br-sw").addEventListener("click", async () => {
  if (!br || brBusy || $("#br-sw").disabled) return;
  const stop = br.State.Running;
  brBusy = !stop; brErr = "";
  renderBrowser();
  try { await (stop ? native().BrowserStop() : native().BrowserStart()); } catch (err) { brErr = errText(err); }
  brBusy = false;
  pollBrowser();
});
setInterval(pollBrowser, 2000);
window.addEventListener("focus", pollBrowser);
// withModule меняет запись модуля на месте: порядок state.modules — порядок вкладок.
function withModule(id, patch) {
  return state.modules.some((x) => x.id === id)
    ? state.modules.map((x) => (x.id === id ? { ...x, ...patch } : x))
    : [...state.modules, { id, enabled: false, ...patch }];
}
let finSaves = Promise.resolve();
function saveFinAddress() {
  const v = $("#fin-server-url").value.trim();
  if (!v) { showError("Впишите адрес kbengine serve на этом Mac, например " + FIN_LOCAL); $("#fin-server-url").focus(); return; }
  saveFinServer(v);
}
function saveFinServer(value) {
  const prev = state.modules;
  state.modules = withModule("finances", { server: value });
  renderFinServer();
  finSaves = finSaves.then(() => native().SetModuleServer("finances", value)).then(async () => {
    showError("");
    try { state.modules = (await native().Modules()) || state.modules; } catch { /* остается то, что на экране */ }
    renderFinServer(); // окно приводит адрес (пробелы, «/» в конце) — показать сохраненный
    const saved = (state.modules.find((x) => x.id === "finances") || {}).server || "";
    if (isLocalServer(saved)) $("#fin-server-url").value = saved; // поле в фокусе renderFinServer не трогает
    toast("Финансы", saved ? (isLocalServer(saved) ? ` · данные из ${saved}` : " · данные с выбранного сервера") : " · данные с первого kb-сервера", null, "info");
    const f = $("#module-frame");
    if (f.dataset.src) f.src = f.dataset.src; // открытая витрина перечитывает данные с нового места
  }).catch(async (err) => {
    showError("Сервер финансов не сохранен: " + errText(err));
    state.modules = prev;
    try { state.modules = (await native().Modules()) || prev; } catch { /* остается прежнее */ }
    renderFinServer();
  });
}
$("#fin-server-pick").addEventListener("change", (e) => {
  if (!native()) return;
  if (e.target.value !== "local") { saveFinServer(e.target.value); return; }
  const url = $("#fin-server-url");
  url.hidden = $("#fin-server-save").hidden = false;
  url.focus();
  url.select();
});
$("#fin-server-save").addEventListener("click", saveFinAddress);
$("#fin-server-url").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); saveFinAddress(); } });
// Сохранения по очереди, как у вкладок; при отказе — перечитать из окна.
let moduleSaves = Promise.resolve();
$("#module-list").addEventListener("click", (e) => {
  const b = e.target.closest("[data-module]");
  if (!b || !native()) return;
  const id = b.dataset.module, on = !state.modules.some((x) => x.id === id && x.enabled);
  state.modules = withModule(id, { enabled: on }); // выбор сервера и порядок вкладок остаются
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
const chatState = { id: "", chat: null, busy: false, partial: "", settings: null, opened: false, list: [], attached: [], only: null, moreOpen: false, voices: [], portraitMode: {}, over: null, memNew: null, verOffers: [], offerCost: false, openCost: "", conns: [], steps: [] };
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
  await loadChatConns();
  await renderChatList();
  renderChat();
  renderChatSources();
  await loadChatDraft();
  // Чат из «Денег» (#541); человек успел выбрать другой — его выбор главнее (клик в колонке снимает openCost).
  const want = chatState.openCost;
  chatState.openCost = "";
  if (want && (chatState.busy || chatState.compressing)) showChatNote("Модель сейчас отвечает: откройте дорогой чат после ответа.");
  else if (want && await selectChat(want)) {
    if (canCompress()) { chatState.offerCost = true; renderChat(); }
    else showChatNote("Сжимать пока нечего: старой части разговора мало.");
  }
  $("#chat-q").focus();
}
// openCostlyChat opens chat id from «Деньги» with the offer to compress it (#541).
function openCostlyChat(id) {
  if (!tabKeys().includes("module:chat")) { showError("Чат выключен в «Модулях»: включите его, чтобы открыть этот разговор."); return; }
  chatState.openCost = id;
  pickTab("module:chat");
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
function chatSources() { return [...state.known.filter((s) => s.enabled), ...chatState.conns.filter((c) => c.share).map((c) => ({ id: c.id, conn: c }))]; }
// Подключения в ряду «ищет в» (отзыв 14.11): кабинеты Ozon и MPStats, что
// включены в «Подключениях»; без разрешения чату — плашкой «запрещено».
function setChatConns(rows) {
  chatState.conns = (rows || []).filter((c) => CHAT_CONN[c.kind] && c.enabled);
  if (!$("#chat-view").hidden) renderChatSources();
}
async function loadChatConns() {
  if (native() && native().Connections) setChatConns(await native().Connections().catch(() => []));
}
function chatSrcOn(id) { return !chatState.only || chatState.only.includes(id); }
function chatSrcBad(id) {
  const p = state.problems && (state.problems.problems || []).find((x) => x.source === id && !x.path && x.level === "error");
  return p ? p.what + (p.todo ? " · " + p.todo : "") : "";
}
function renderChatSources() {
  const row = $("#chat-srcs");
  const all = chatSources(), list = all.filter((s) => !s.conn);
  // «Окно ищет» (локальная модель): инструментов нет — подключения чат не спрашивает вовсе.
  const tools = !chatState.settings || chatState.settings.effectiveMode === "tools";
  const conns = tools ? all.filter((s) => s.conn) : [], locked = tools ? chatState.conns.filter((c) => !c.share) : chatState.conns;
  row.hidden = !all.length && !locked.length;
  if (row.hidden) return;
  const hue = chatHues();
  const chip = (s) => {
    const bad = chatSrcBad(s.id), on = chatSrcOn(s.id);
    return `<button type="button" class="chat-src${bad ? " bad" : ""}" style="--c:var(${hue.get(s.id)})" data-chat-src="${esc(s.id)}" aria-pressed="${on}"
      data-tip="${esc(label(s))}" data-tip-b="${esc(bad || (on ? "этот чат ищет здесь · щелчок — убрать" : "этот чат здесь не ищет · щелчок — вернуть"))}"${bad ? " data-tip-bad" : ""}><i></i><span class="t">${esc(label(s))}</span></button>`;
  };
  const connChip = (s) => {
    const on = chatSrcOn(s.id);
    return `<button type="button" class="chat-src conn" data-chat-src="${esc(s.id)}" aria-pressed="${on}"
      data-tip="${esc(connLabel(s.conn))}" data-tip-b="${esc((on ? "подключение: внешний сервис по Вашему ключу, чат спрашивает его, его данные уходят провайдеру модели · щелчок — убрать из этого чата" : "этот чат его не спрашивает · щелчок — вернуть"))}"><i></i><span class="t">${esc(connLabel(s.conn))}</span></button>`;
  };
  const lockChip = (c) => tools
    ? `<button type="button" class="chat-src conn locked" data-chat-conn-off="${esc(c.id)}" aria-disabled="true"
      data-tip="${esc(connLabel(c))}: чату запрещено" data-tip-b="модель не видит его данных · щелчок — «Подключения», там «Чату: разрешить»"><i></i><span class="t">${esc(connLabel(c))}</span></button>`
    : `<button type="button" class="chat-src conn locked" data-chat-mode-off aria-disabled="true"
      data-tip="${esc(connLabel(c))}: чат не спрашивает" data-tip-b="окно само ищет документы и отдает их модели, подключения так не спрашиваются · щелчок — «Чат и модель», режим «модель ищет»"><i></i><span class="t">${esc(connLabel(c))}</span></button>`;
  const max = 6; // больше — в «еще N»: ряд не переносится
  const shown = list.slice(0, max), rest = list.slice(max);
  const restOn = rest.filter((s) => chatSrcOn(s.id)).length;
  // Две группы (отзыв 14.11: в узком окне подключения уходили за край): не
  // помещаются рядом — «спрашивает» переносится второй строкой, плашки сжимаются.
  row.innerHTML = (list.length ? `<span class="chat-srcs-grp"><span class="lbl">ищет в</span>` + shown.map(chip).join("") +
    (rest.length ? `<button type="button" class="chat-src" data-chat-more aria-expanded="${chatState.moreOpen}">еще ${rest.length}${chatState.only ? ` · ${restOn} вкл` : ""}</button>` : "") + `</span>` : "") +
    (conns.length || locked.length ? `<span class="chat-srcs-grp"><span class="lbl">спрашивает</span>` + conns.map(connChip).join("") + locked.map(lockChip).join("") + `</span>` : "") +
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
  if (e.target.closest("[data-chat-conn-off]")) return openSettings("conn");
  if (e.target.closest("[data-chat-mode-off]")) return openSettings("chat");
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
// setEye показывает или прячет набранное в поле id и подписывает его глаз.
function setEye(id, show) {
  const eye = $(`[data-eye="${id}"]`), what = eye.dataset.what || "токен";
  $("#" + id).type = show ? "text" : "password";
  eye.setAttribute("aria-pressed", String(show));
  eye.setAttribute("aria-label", show ? "Скрыть " + what : "Показать набранный " + what);
  eye.dataset.tip = `${show ? "Скрыть" : "Показать"} ${what}`;
}
// maskKey прячет набранный ключ обратно: при каждой загрузке настроек чата.
const maskKey = (id) => setEye(id, false);
async function loadChatSettings() {
  maskKey("chat-key");
  $("#chat-key-saved").hidden = true;
  try { chatState.settings = await native().ChatSettings(); } catch (e) { chatState.settings = null; showChatNote("Настройки чата не прочитаны: " + errText(e)); return; }
  const s = chatState.settings;
  $("#chat-url").value = s.url || "";
  $("#chat-name").value = s.model || "";
  $("#chat-mode").value = s.mode || "";
  $("#chat-ctx-win").value = s.contextWindow || "";
  $("#chat-mic-mode").value = s.mic || "";
  renderMicChoices();
  setMic(dict.on);
  $("#chat-key").value = "";
  // Сохраненный ключ на страницу не возвращается; что он есть — говорит отметка (#496).
  $("#chat-key-saved").hidden = !s.hasKey;
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
const factsLine = (n, wait) => `${n} ${plural(n, "факт", "факта", "фактов")}` + (wait ? ` · ${wait} ждут` : "");
function renderAccValues() {
  $("#acc-v-model").textContent = ($("#chat-name").value || $("#chat-name").placeholder).replace(/^[^/]+\//, "");
  const v = $("#chat-voice-name").value;
  $("#acc-v-voice").textContent = isCloud(v) ? v.split(":")[1].replace(/^./, (c) => c.toUpperCase()) + " · облако" : (v ? v.replace(/\s*\(.*\)$/, "") : "голос системы") + " · Mac";
  $("#acc-v-mic").textContent = (MIC_MODES[$("#chat-mic-mode").value] || MIC_MODES[""])[0];
  const lb = $("#chat-listens").querySelector('[aria-pressed="true"]');
  $("#acc-v-listen").textContent = lb ? lb.firstChild.textContent : "Mac (бесплатно)";
  const p = memState.p;
  $("#acc-v-mem").textContent = !memOn(p) ? "не включено" : factsLine(approvedFacts().length, pendingFacts().length);
}
// renderChatNote says aloud where the documents go (ADR-0006).
function renderChatNote() {
  const s = chatState.settings;
  if (!s) return;
  const who = s.effectiveMode === "tools" ? "модель сама ищет и читает документы" : "окно находит документы и отдает их модели";
  // Портрет идет в каждый чат — человек видит, что он уходит туда же, куда документы.
  const what = memOn(memState.p) ? "документы и портрет" : "документы";
  // Пустое имя (настройки без модели) не должно попадать в строку словом «undefined».
  const model = (s.model || "").trim() || "Модель по умолчанию";
  showChatNote(s.local
    ? `${model} на этом Маке: ${what} никуда не уходят · ${who}`
    : `${model}: найденные ${what} уходят провайдеру модели · ${who}`);
}
const showChatNote = (t) => setClamped($("#chat-note"), t, "Сообщение чата");
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
    : c.id === chatState.renaming
    ? `<li class="chat-rename"><input class="chat-find" id="chat-rename-in" maxlength="60" value="${esc(c.title)}" aria-label="Название чата"></li>`
    : `<li><button class="chat-item${c.id === chatState.id ? " on" : ""}" data-chat="${esc(c.id)}" data-tip="${esc(c.title)}" data-tip-b="${esc(chatWhen(c.updated))}">${esc(c.title)}</button>
      <button class="chat-ren" data-ren="${esc(c.id)}" aria-label="Переименовать чат: ${esc(c.title)}" data-tip="Переименовать">✎</button>
      <button class="chat-del" data-del="${esc(c.id)}" aria-label="Удалить чат: ${esc(c.title)}" data-tip="Удалить чат">×</button></li>`).join("") ||
    `<li class="muted chat-empty" style="padding:7px 10px">${q ? "Чатов с таким названием нет" : "Чатов пока нет"}</li>`;
  dropStaleTip();
  const inp = $("#chat-rename-in");
  if (inp && document.activeElement !== inp) { inp.focus(); inp.select(); }
}
// Переименование (двойной щелчок по названию или «✎»): Enter сохраняет, Esc и
// пустое поле оставляют прежнее название, уход из поля сохраняет как в Finder.
function startRename(id) {
  if (chatState.busy || !chatState.list.some((c) => c.id === id)) return;
  chatState.renaming = id;
  chatState.confirmDel = null;
  paintChatList();
}
async function finishRename(save) {
  const id = chatState.renaming, inp = $("#chat-rename-in");
  if (!id) return; // Esc уже закрыл поле: уход из него второй раз не сохраняет
  chatState.renaming = null;
  const name = inp ? inp.value.trim() : "", old = (chatState.list.find((c) => c.id === id) || {}).title;
  if (save && name && name !== old) {
    try {
      await native().RenameChat(id, name);
      if (id === chatState.id && chatState.chat) { chatState.chat.title = name; renderChat(); }
    } catch (e) { showChatNote("Название не сохранено: " + errText(e)); }
    return renderChatList();
  }
  paintChatList();
}
// answerHTML shows the model's text: абзацы, **жирный**, `код` — без HTML модели.
function answerHTML(t) {
  return esc(t)
    .replace(/\*\*(.+?)\*\*/g, "<b>$1</b>")
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\n/g, "<br>");
}
// Заполнение контекста (решение владельца 09.11): около половины качество еще то
// же, дальше разговор пухнет, и пора записать handoff и начать новый чат. Счет
// токенов — от провайдера, размер контекста человек вносит в настройках.
const CTX_HINT_AT = 0.5, CTX_WARN_AT = 0.8;
const ctxNum = (v) => v.toLocaleString("ru-RU");
// Кольцо видно всегда: счета провайдера еще нет (новый чат) — пустое кольцо с
// размером окна из настроек, а если и его нет — со знаком вопроса.
const ctxNow = () => {
  const x = chatState.chat && chatState.chat.context;
  if (x && x.used) return x;
  // unknown: в чате уже есть разговор, а счета провайдера нет (чат из старой
  // сборки или провайдер счет не шлет) — «0%» было бы неправдой.
  const talked = !!(chatState.chat && (chatState.chat.messages || []).length);
  return { used: 0, unknown: talked, window: (chatState.settings && chatState.settings.contextWindow) || 0 };
};
function renderChatCtx() {
  const ring = $("#chat-ctx-ring"), x = ctxNow();
  const f = x.window ? x.used / x.window : 0, tone = !x.window ? "ok" : f >= CTX_WARN_AT ? "bad" : f >= CTX_HINT_AT ? "warn" : "ok";
  const R = 8, C = 2 * Math.PI * R;
  ring.hidden = false;
  ring.dataset.tone = tone;
  ring.setAttribute("aria-label", x.window ? `Контекст заполнен на ${Math.round(f * 100)}%: подробнее` : "Контекст: подробнее");
  ring.innerHTML = `<svg viewBox="0 0 20 20" aria-hidden="true"><circle class="bg" cx="10" cy="10" r="${R}"/><circle class="fg" cx="10" cy="10" r="${R}" stroke-dasharray="${C * Math.min(1, f)} ${C}" transform="rotate(-90 10 10)"/></svg><span>${x.unknown ? "—" : x.window ? (x.est ? "~" : "") + Math.round(f * 100) + "%" : "?"}</span>`;
  if (!$("#chat-ctx-pop").hidden) fillChatCtx(x, f, tone);
}
// Подробности — окошком поверх по кнопке-кольцу: что насчитал провайдер, размер
// контекста и что делать дальше; в строке поля остается только кольцо.
function fillChatCtx(x, f, tone) {
  const pop = $("#chat-ctx-pop");
  pop.dataset.tone = tone;
  const advice = !x.window
    ? `<p class="muted">Размер контекста модели не указан, поэтому окно не знает, сколько осталось. Внесите его в настройках чата.</p><div class="cx-acts"><button class="btn" type="button" id="chat-ctx-set">Указать размер</button></div>`
    : x.unknown
      ? `<p class="muted">Счета провайдера для этого чата пока нет, поэтому заполнение неизвестно. Он придет с ближайшим ответом.</p>`
    : f >= CTX_HINT_AT
      ? `<p><b>${f >= 1 ? "Контекст переполнен." : f >= CTX_WARN_AT ? "Контекст почти полон." : "Контекст наполовину полон."}</b> ${f >= 1 ? "Запрос не помещается в окно модели: часть разговора она не видит или провайдер откажет. " : "Дальше разговор пухнет, ответы дороже и хуже. "}Запишите handoff (что сделано, что не сработало, следующий шаг) и начните новый чат.</p><div class="cx-acts">${canCompress() ? `<button class="btn primary" type="button" id="chat-ctx-compress">Сжать разговор</button><button class="btn" type="button" id="chat-ctx-new">Новый чат</button>` : `<button class="btn primary" type="button" id="chat-ctx-new">Новый чат</button>`}</div>`
      : `<p class="muted">Пока места хватает. Около половины окно подскажет записать handoff и начать новый чат.</p>`;
  pop.innerHTML = `<div class="cx-head"><b>Контекст чата</b><button class="cx-x" type="button" id="chat-ctx-close" aria-label="Закрыть">✕</button></div>
    ${x.window ? `<div class="chat-ctx-bar" role="meter" aria-valuemin="0" aria-valuemax="${x.window}" aria-valuenow="${x.used}"><i style="width:${Math.min(100, f * 100)}%"></i></div>` : ""}
    <dl class="cx-kv"><dt>В запросе</dt><dd>${!x.used ? `<span class="muted">${x.unknown ? "счета пока нет" : "счет придет с ближайшим ответом"}</span>` : `${x.est ? "≈ " : ""}${ctxNum(x.used)} токенов <span class="muted">${x.est ? "оценка после сжатия, точный счет придет с ответом" : "по счету провайдера"}</span>`}</dd>${x.window ? `<dt>Контекст модели</dt><dd>${ctxNum(x.window)} токенов${x.unknown ? "" : " · занято " + Math.round(f * 100) + "%"}</dd>` : ""}</dl>${advice}`;
}
function openChatCtx() {
  const x = ctxNow();
  const f = x.window ? x.used / x.window : 0;
  fillChatCtx(x, f, !x.window ? "ok" : f >= CTX_WARN_AT ? "bad" : f >= CTX_HINT_AT ? "warn" : "ok");
  $("#chat-ctx-pop").hidden = false;
  $("#chat-ctx-ring").setAttribute("aria-expanded", "true");
}
function closeChatCtx() {
  $("#chat-ctx-pop").hidden = true;
  $("#chat-ctx-ring").setAttribute("aria-expanded", "false");
}
$("#chat-ctx-ring").addEventListener("click", () => ($("#chat-ctx-pop").hidden ? openChatCtx() : closeChatCtx()));
$("#chat-ctx-pop").addEventListener("click", (e) => {
  if (e.target.closest("#chat-ctx-close")) closeChatCtx();
  else if (e.target.closest("#chat-ctx-new")) { closeChatCtx(); newChat(); }
  else if (e.target.closest("#chat-ctx-compress")) { if (!chatState.compressing) compressChat(false); }
  else if (e.target.closest("#chat-ctx-set")) { closeChatCtx(); openSettings("chat").then(() => $("#chat-ctx-win").focus()); }
});
document.addEventListener("click", (e) => { if (!$("#chat-ctx-pop").hidden && !e.target.closest("#chat-ctx-pop, #chat-ctx-ring")) closeChatCtx(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !$("#chat-ctx-pop").hidden) { closeChatCtx(); e.stopPropagation(); } }, true);
function renderChat() {
  renderChatCtx();
  renderCompressOffer();
  const c = chatState.chat;
  const log = $("#chat-log");
  $("#chat-title").textContent = (c && c.title) || "Новый чат";
  if (!c) {
    // Портрет не помещается — сказать в начале нового чата, до вопроса
    // (прототип 06.11, вариант A): иначе первый ответ уйдет без портрета.
    const p = memState.p;
    if (!chatState.busy && memOn(p) && p.size > p.budget && !chatState.portraitMode[""] && !(chatState.over && chatState.over.chat === ""))
      chatState.over = { chat: "", size: p.size, budget: p.budget, props: null, start: true };
    log.innerHTML = memInChat() + verOffersInChat() + `<div class="chat-hello"><p>Спросите о своих записях и файлах.</p><p class="muted">Ответ — со ссылками на документы; к чату можно вернуться в колонке чатов.</p></div>`;
  } else {
    const folded = (c.compressed && c.compressed.folded) || 0;
    log.innerHTML = c.messages.map((m, i) => (m.role === "user"
      ? `<div class="msg user">${esc(m.text)}</div>`
      : `<div class="msg bot">${answerHTML(m.text)}${usesHTML(m.uses)}</div>`)
      + (i === folded - 1 ? `<div class="chat-fold" role="separator">Выше сжато ${folded} сообщ.: модель видит конспект, а не их. Исходный разговор сохранен.</div>` : "")).join("");
  }
  if (c && !chatState.busy) log.insertAdjacentHTML("beforeend", memInChat() + verOffersInChat());
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
  el.innerHTML = t ? answerHTML(t) : chatState.steps.length ? stepsHTML(chatState.steps) : `<span class="building"></span>думаю…`;
  if (atBottom) log.scrollTop = log.scrollHeight; // не уводить, если человек листает вверх
}
// Шаги хода (chat:step, отзыв 14.11): куда чат ходит прямо сейчас — документы,
// подключение или интернет; последний шаг — с полосой «идет».
const useName = (u) => u.kind === "docs" ? "Документы" : u.kind === "web" ? "Интернет · " + u.name : u.name;
function stepsHTML(steps) {
  const last = steps.slice(-6);
  return `<ul class="chat-steps">${last.map((u, i) => `<li class="k-${esc(u.kind)}">${i === last.length - 1 ? `<span class="building"></span>` : "✓ "}<b>${esc(useName(u))}</b>${u.what ? ": " + esc(u.what) : ""}${u.kind === "web" ? ` <span class="muted">· вне Ваших источников</span>` : ""}</li>`).join("")}</ul>`;
}
// usesHTML — откуда ответ, под ним: место один раз, что спрашивали — в подсказке.
function usesHTML(uses) {
  if (!uses || !uses.length) return "";
  const by = new Map();
  for (const u of uses) {
    const k = u.kind + "|" + (u.kind === "docs" ? "" : u.id || u.name);
    if (!by.has(k)) by.set(k, { u, what: [] });
    if (u.what) by.get(k).what.push(u.what);
  }
  return `<div class="chat-uses"><span class="lbl">взято</span>${[...by.values()].map(({ u, what }) =>
    `<span class="chat-use k-${esc(u.kind)}" data-tip="${esc(useName(u))}${u.kind === "web" ? " · вне Ваших источников" : u.kind === "connection" ? " · внешний сервис по Вашему ключу" : ""}" data-tip-b="${esc(what.join(" · "))}">${esc(useName(u))}</span>`).join("")}</div>`;
}
if (window.runtime) window.runtime.EventsOn("chat:step", (u) => {
  if (!chatState.busy || !u) return;
  chatState.steps.push(u);
  if (!chatState.partial) paintPartial();
});
if (window.runtime) window.runtime.EventsOn("chat:delta", (t) => {
  if (!chatState.busy) return;
  chatState.partial = t || "";
  paintPartial();
});
// Сжатие длинного чата (#539). Предлагается, когда занятое не помещается в
// контекст модели (сменили модель на меньшую) или когда не поместится вопрос;
// без согласия ничего не меняется. Сжимает окно (CompressChat): сообщения в
// файле остаются, модель видит конспект и хвост последних сообщений; есть ли
// что сжимать, говорит окно (canCompress): число хвоста знает только оно.
const CHARS_PER_TOKEN = 3; // грубо для русского; счет провайдера придет с ответом
const askNeedsRoom = (q) => {
  const c = chatState.chat, x = c && c.context;
  return !!(x && x.window && x.used + Math.ceil([...q].length / CHARS_PER_TOKEN) > x.window);
};
const canCompress = () => {
  const c = chatState.chat;
  return !!(c && c.id && c.canCompress && !chatState.busy);
};
function renderCompressOffer() {
  const el = $("#chat-offer"), x = chatState.chat && chatState.chat.context;
  const over = !!(x && x.window && x.used > x.window);
  // offerCost — пришли из «Денег» по подсказке «дорогой чат» (#541).
  const show = canCompress() && !chatState.declined && (over || chatState.offerAsk || chatState.offerCost);
  el.hidden = !show;
  if (!show) return;
  el.innerHTML = `<span>${chatState.offerAsk ? "Вопрос не поместится в контекст модели." : over ? "Занято больше, чем помещается в контекст модели." : "Чат дорогой: каждый вопрос заново отправляет модели весь разговор."} Сжать старую часть разговора в конспект? Исходный разговор останется.</span>
    <button class="btn primary" type="button" data-offer="yes"${chatState.compressing ? " disabled" : ""}>${chatState.compressing ? "Сжимаю…" : "Сжать"}</button><button class="btn" type="button" data-offer="no"${chatState.compressing ? " disabled" : ""}>Не надо</button>`;
}
$("#chat-offer").addEventListener("click", (e) => {
  const b = e.target.closest("[data-offer]");
  if (!b || chatState.busy || chatState.compressing) return;
  // Вопрос ждет в поле: после ответа на предложение он уходит сам, иначе «Не надо»
  // выглядит как зависание.
  const pending = chatState.offerAsk && $("#chat-q").value.trim() !== "";
  if (b.dataset.offer === "no") {
    chatState.declined = true; chatState.offerAsk = chatState.offerCost = false;
    renderChat();
    if (pending) askChat();
    return;
  }
  compressChat(pending);
});
async function compressChat(pending) {
  // Своя отметка, а не busy: busy рисует «модель отвечает», а это не ответ.
  const id = chatState.id;
  chatState.compressing = true;
  closeChatCtx();
  renderChat();
  try {
    const prev = chatState.chat.context;
    const c = await native().CompressChat(id);
    if (chatState.id !== id) return; // чат успели сменить или удалить: чужую запись не подставлять
    // Счет провайдера придет с ответом; до него оценка: прежний счет минус то,
    // что сжатие сняло (свернутые знаки за вычетом конспекта). Системное
    // сообщение, портрет и документы остаются в счете.
    if (prev && prev.window) {
      const gone = c.messages.slice(0, c.compressed.folded).reduce((n, m) => n + Array.from(m.text).length, 0) - Array.from(c.compressed.text).length;
      c.context = { used: Math.max(0, prev.used - Math.ceil(Math.max(0, gone) / CHARS_PER_TOKEN)), window: prev.window, est: true };
    }
    chatState.chat = c;
    chatState.offerAsk = chatState.offerCost = false;
    toast("Чат сжат", " · свернуто " + c.compressed.folded + " сообщ.");
  } catch (err) {
    showChatNote("Не сжато: " + errText(err));
    return;
  } finally {
    chatState.compressing = false;
    renderChat();
  }
  if (pending) askChat();
}
async function askChat() {
  const q = $("#chat-q").value.trim();
  if (!q || chatState.busy || chatState.compressing) return;
  // Вопрос не помещается: предложить сжать до отправки; текст остается в поле.
  if (canCompress() && !chatState.declined && askNeedsRoom(q)) { chatState.offerAsk = true; renderChat(); return; }
  chatState.busy = true;
  chatState.partial = "";
  chatState.steps = [];
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
    chatState.offerAsk = chatState.offerCost = chatState.declined = false;
    await renderChatList();
  } catch (e) {
    chatState.chat.messages.pop();
    if (!chatState.chat.messages.length) chatState.chat = null;
    chatState.declined = false; // «Не надо» не глушит предложение после сбоя ответа
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
async function newChat() {
  if (chatState.busy || chatState.compressing) return;
  await flushChatDraft(); // набранное остается за прежним чатом
  if (chatState.busy || chatState.compressing) return;
  chatState.offerAsk = chatState.offerCost = chatState.declined = false;
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
  await loadChatDraft();
  $("#chat-q").focus();
}
$("#chat-ask").addEventListener("submit", (e) => { e.preventDefault(); askChat(); });
// Черновик (#540): набранное, но не отправленное, лежит в окне по чатам и
// переживает смену чата и перезапуск. Сохраняется с задержкой, пока человек
// печатает; смена чата сохраняет сразу. saved — что окно уже знает: повторно
// то же не пишем.
const chatDraft = { timer: 0, id: "", saved: "" };
function scheduleChatDraft() {
  if (!native() || !native().SaveChatDraft) return;
  clearTimeout(chatDraft.timer);
  const id = chatState.id, text = $("#chat-q").value;
  if (id === chatDraft.id && text === chatDraft.saved) return;
  chatDraft.timer = setTimeout(() => writeChatDraft(id, text), 500);
}
async function writeChatDraft(id, text) {
  try {
    await native().SaveChatDraft(id, text);
    chatDraft.id = id; chatDraft.saved = text;
  } catch (e) { showChatNote("Черновик не сохранен: " + errText(e)); }
}
// flushChatDraft saves the pending draft now, before the field changes owner.
async function flushChatDraft() {
  if (!chatDraft.timer) return;
  clearTimeout(chatDraft.timer);
  chatDraft.timer = 0;
  await writeChatDraft(chatState.id, $("#chat-q").value);
}
// loadChatDraft puts the saved draft of the open chat into the field.
async function loadChatDraft() {
  if (!native() || !native().ChatDraft) return;
  const id = chatState.id;
  let text = "";
  try { text = await native().ChatDraft(id); } catch { /* нет черновика — пустое поле */ }
  if (chatState.id !== id || chatState.busy) return; // чат успели сменить
  $("#chat-q").value = text;
  chatDraft.id = id; chatDraft.saved = text;
  fitChatQ();
}
// Поле растет по тексту до 40% высоты; кнопка «Спросить» — только с вопросом.
function fitChatQ() {
  const q = $("#chat-q");
  q.style.height = "auto";
  q.style.height = q.scrollHeight + "px";
  $("#chat-send").disabled = chatState.busy || !q.value.trim();
  q.closest(".chat-box").classList.toggle("has-text", !!q.value.trim() || chatState.busy);
  scheduleChatDraft();
}
$("#chat-q").addEventListener("input", () => {
  fitChatQ(); renderAttached();
  // Вопрос подправили или стерли: «не поместится» считается заново, а не висит.
  if (chatState.offerAsk && !askNeedsRoom($("#chat-q").value.trim())) { chatState.offerAsk = false; renderChat(); }
});

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
$("#chat-model-chip").addEventListener("click", () => { hideTip(); $("#chat-mdl-pop").hidden ? openModelPick() : closeModelPick(); });
// --- Быстрый выбор модели (#584) -----------------------------------------------------
// Чип под полем — список: последние модели сверху, все модели шлюза с ценой за
// миллион токенов, поиск; выбор меняет только модель. «Настройки чата» слева — все остальное.
const mdl = { v: null, q: "", sel: 0 };
async function openModelPick() {
  if (!native() || !native().ChatModels) return openSettings("chat");
  closeChatCtx();
  const pop = $("#chat-mdl-pop");
  pop.hidden = false;
  $("#chat-model-chip").setAttribute("aria-expanded", "true");
  pop.innerHTML = '<p class="md-note">Загружаю список моделей…</p>';
  try { mdl.v = await native().ChatModels(); } catch (e) { pop.innerHTML = `<p class="md-note">Список не прочитан: ${esc(errText(e))}</p>`; return; }
  mdl.q = ""; mdl.sel = 0;
  pop.innerHTML = `<input id="md-q" type="search" placeholder="найти модель${mdl.v.gateway ? "" : " или вписать имя"}…" aria-label="Найти модель" autocomplete="off" spellcheck="false"><div class="md-list" id="md-list" role="listbox" aria-label="Модели"></div>` +
    (mdl.v.priceNote ? `<p class="md-note">Цены шлюза не загружены: ${esc(mdl.v.priceNote)}</p>` : "") +
    (mdl.v.gateway ? "" : '<p class="md-note">Адрес чата — не шлюз Vercel: окно знает только модели, которыми Вы уже пользовались. Другую впишите в поиск и нажмите Enter.</p>') +
    '<button class="md-foot" type="button" id="md-set">Все настройки чата →</button>';
  renderModelPick();
  $("#md-q").focus();
}
function closeModelPick() {
  $("#chat-mdl-pop").hidden = true;
  $("#chat-model-chip").setAttribute("aria-expanded", "false");
}
// mdlItems — строки списка: без поиска «Последние» и «Все», с поиском — найденные; вписанное имя — последней строкой.
function mdlItems() {
  const q = mdl.q.trim().toLowerCase(), v = mdl.v, out = [];
  if (!q) {
    v.recent.forEach((m) => out.push({ cap: out.length ? "" : "Последние", m }));
    const rest = v.all.filter((m) => !v.recent.some((r) => r.id === m.id));
    rest.forEach((m, i) => out.push({ cap: i ? "" : v.gateway ? "Все модели шлюза" : "Модели", m }));
  } else {
    v.all.filter((m) => m.id.toLowerCase().includes(q)).forEach((m) => out.push({ cap: "", m }));
    if (!v.all.some((m) => m.id.toLowerCase() === q)) out.push({ cap: "", m: { id: mdl.q.trim(), in: "", out: "", typed: true } });
  }
  return out;
}
function renderModelPick() {
  const items = mdlItems(), cur = mdl.v.current;
  mdl.sel = Math.max(0, Math.min(mdl.sel, items.length - 1));
  const price = (m) => (m.typed ? "вписать" : m.in ? `$${m.in} / $${m.out}` : "цена неизвестна");
  const tip = (m) => (m.typed ? "модель с таким именем у адреса чата; окно его не проверяет — проверит первый ответ"
    : m.in ? `за миллион токенов: $${m.in} на входе, $${m.out} на выходе${m.own ? " · своя цена из «Статистики»" : " · цена шлюза"}` : "цены нет ни у шлюза, ни своей в «Статистике»");
  $("#md-list").innerHTML = items.map(({ cap, m }, i) => (cap ? `<div class="md-cap">${cap}</div>` : "") +
    `<button class="md-it" type="button" role="option" data-mdl="${esc(m.id)}" aria-selected="${i === mdl.sel}" data-tip="${esc(m.id)}" data-tip-b="${esc(tip(m))}">` +
    `<span class="dot">${m.id === cur ? "●" : ""}</span><span class="id">${esc(m.id)}</span><span class="pr">${price(m)}</span></button>`).join("");
  const s = $("#md-list").querySelector('[aria-selected="true"]'); if (s) s.scrollIntoView({ block: "nearest" });
}
async function chooseModel(id) {
  closeModelPick();
  if (!id || id === mdl.v.current) return;
  try { await native().SetChatModel(id); } catch (e) { return showChatNote("Модель не сменилась: " + errText(e)); }
  await loadChatSettings();
  toast("Модель", " · " + id);
}
$("#chat-mdl-pop").addEventListener("input", (e) => { if (e.target.id === "md-q") { mdl.q = e.target.value; mdl.sel = 0; renderModelPick(); } });
$("#chat-mdl-pop").addEventListener("keydown", (e) => {
  if (e.target.id !== "md-q") return;
  const n = mdlItems().length;
  if (e.key === "ArrowDown" && n) { e.preventDefault(); mdl.sel = (mdl.sel + 1) % n; renderModelPick(); }
  else if (e.key === "ArrowUp" && n) { e.preventDefault(); mdl.sel = (mdl.sel - 1 + n) % n; renderModelPick(); }
  else if (e.key === "Enter") { e.preventDefault(); const it = mdlItems()[mdl.sel]; if (it) chooseModel(it.m.id); }
});
$("#chat-mdl-pop").addEventListener("click", (e) => {
  if (e.target.closest("#md-set")) { closeModelPick(); openSettings("chat"); return; }
  const b = e.target.closest("[data-mdl]"); if (b) { hideTip(); chooseModel(b.dataset.mdl); }
});
document.addEventListener("click", (e) => { if (!$("#chat-mdl-pop").hidden && !e.target.closest("#chat-mdl-pop, #chat-model-chip")) closeModelPick(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !$("#chat-mdl-pop").hidden) { closeModelPick(); $("#chat-q").focus(); e.stopPropagation(); } }, true);
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
document.addEventListener("keydown", (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n" && !$("#chat-view").hidden) { e.preventDefault(); newChat(); }
});
$("#chat-list").addEventListener("dblclick", (e) => {
  const b = e.target.closest("[data-chat]");
  if (b) startRename(b.dataset.chat);
});
$("#chat-list").addEventListener("keydown", (e) => {
  if (!e.target.closest("#chat-rename-in")) return;
  if (e.key === "Enter") { e.preventDefault(); finishRename(true); }
  else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); finishRename(false); }
});
$("#chat-list").addEventListener("focusout", (e) => {
  if (e.target.closest("#chat-rename-in")) finishRename(true);
});
$("#chat-list").addEventListener("click", async (e) => {
  const ren = e.target.closest("[data-ren]");
  if (ren) return startRename(ren.dataset.ren);
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
    if (!chatState.id) await loadChatDraft(); // у «нового» чата свой черновик
    return;
  }
  const b = e.target.closest("[data-chat]");
  if (!b || chatState.busy || chatState.compressing) return;
  chatState.openCost = "";
  await selectChat(b.dataset.chat);
});
// selectChat opens chat id in the chat view: из колонки чатов и из «Денег» (#541).
async function selectChat(id) {
  await flushChatDraft(); // набранное остается за прежним чатом
  try {
    chatState.chat = await native().Chat(id);
    chatState.id = id;
    chatState.offerAsk = chatState.offerCost = chatState.declined = false; // предложение сжать — про тот чат, где его показали
    // Источники чата — только те, что есть сейчас: удаленный или выключенный
    // оставил бы чат искать нигде (ревью #465).
    const alive = (chatState.chat.sources || []).filter((id) => chatSources().some((s) => s.id === id));
    chatState.only = alive.length ? alive : null;
    renderChatSources();
  } catch (err) { showChatNote("Чат не открылся: " + errText(err)); return false; }
  paintChatList();
  renderChat();
  renderChatNote();
  await loadChatDraft();
  return true;
}
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
    // Режим «кто ищет» считает окно: сменились режим или модель — спросить заново (ряд «ищет в»).
    if ("mode" in patch || "url" in patch || "model" in patch) chatState.settings = await native().ChatSettings().catch(() => chatState.settings);
    renderChatSources();
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
  const c = { ...saved, url: $("#chat-url").value.trim(), model: $("#chat-name").value.trim(), mode: $("#chat-mode").value, voice: $("#chat-voice-name").value, listen: $("#chat-listen").value, mic: $("#chat-mic-mode").value, contextWindow: parseInt($("#chat-ctx-win").value, 10) || 0 };
  try {
    await native().SetChatSettings(c, $("#chat-key").value);
    $("#chat-model-note").textContent = "";
    toast("Сохранено", " · модель " + (c.model || $("#chat-name").placeholder).replace(/^[^/]+\//, ""));
    await loadChatSettings();
    // Размер контекста новой модели — к открытому чату: кольцо и предложение сжать.
    if (chatState.chat && chatState.chat.context) { chatState.chat.context.window = c.contextWindow; chatState.declined = false; }
    renderChat();
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
// Новая итерация из чата (#548): модель только предлагает (versions:proposal), файл
// появляется после «Сохранить новой версией». Описание и «что нового» человек
// правит здесь же; прежние версии не меняются.
function verOffersInChat() {
  return chatState.verOffers.filter((o) => !o.chatId || o.chatId === chatState.id).map((o) => `<div class="ver-offer" data-vo="${esc(o.id)}">
    <span class="muted">Сохранить как новую версию «${esc(o.family)}»? Файл <b>${esc(o.file)}</b> ляжет в ${esc(o.folder)}; прежние версии не меняются.</span>
    <details class="ver-offer-text" open><summary class="muted">Текст новой версии, ${(o.size || 0).toLocaleString("ru-RU")} знаков: это будет записано в файл</summary><pre>${esc(o.preview || "")}</pre></details>
    <label class="muted">Описание файла<input data-vo-desc maxlength="300" value="${esc(o.description || "")}"></label>
    <label class="muted">Что нового<input data-vo-note maxlength="300" value="${esc(o.note || "")}"></label>
    <div class="mem-acts"><button type="button" class="btn primary" data-vo-save>Сохранить новой версией</button><button type="button" class="btn" data-vo-drop>Отклонить</button></div>
  </div>`).join("");
}
function verOfferGone(id) { chatState.verOffers = chatState.verOffers.filter((o) => o.id !== id); renderChat(); }
if (window.runtime) window.runtime.EventsOn("versions:proposal", (o) => {
  if (!o || chatState.verOffers.some((x) => x.id === o.id)) return;
  chatState.verOffers.push(o);
  if (!chatState.busy) renderChat();
});
native()?.VersionProposals?.().then((list) => { if (list && list.length) { chatState.verOffers = list; if (!chatState.busy) renderChat(); } }).catch(() => {});
$("#chat-log").addEventListener("input", (e) => {
  const card = e.target.closest("[data-vo]");
  const o = card && chatState.verOffers.find((x) => x.id === card.dataset.vo);
  if (!o) return;
  if (e.target.matches("[data-vo-desc]")) o.description = e.target.value;
  if (e.target.matches("[data-vo-note]")) o.note = e.target.value;
});
$("#chat-log").addEventListener("click", async (e) => {
  const card = e.target.closest("[data-vo]");
  if (!card) return;
  const id = card.dataset.vo, o = chatState.verOffers.find((x) => x.id === id);
  if (e.target.closest("[data-vo-drop]")) { try { await native().DropVersionProposal(id); } catch (err) { /* предложение и так не сохранится */ } verOfferGone(id); return; }
  const save = e.target.closest("[data-vo-save]");
  if (!save || !o || save.disabled) return;
  save.disabled = true; // двойной щелчок не создает второй файл
  try {
    const res = await native().SaveVersionProposal(id, o.description || "", o.note || "");
    // Файл создан: карточка уходит, даже если после записи что-то не удалось (тогда это предупреждение).
    toast(res.warning ? "Версия сохранена, но…" : "Версия сохранена", res.warning ? `${res.path}\n${res.warning}` : res.path);
    verOfferGone(id);
    if (state.tab === "artifacts") loadArtifacts(true);
  } catch (err) { save.disabled = false; toast("Версия не сохранена", errText(err), null, "bad"); }
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
const ST_COLORS = { answer: "--z-sp4", search: "--z-sp5", extract: "--z-sp1", compress: "--z-sp2", listen: "--z-src-teal", speak: "--z-src-violet", describe: "--z-sp3", forecast: "--z-sp6" };
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

async function openStats(fresh = true) {
  if (fresh) { st.money = null; st.cal = null; } // при каждом открытии вкладки; от периода они не зависят
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
$("#st-range").addEventListener("click", (e) => { const b = e.target.closest("[data-d]"); if (!b) return; st.days = +b.dataset.d; st.open = null; st.hist = []; if (st.f.day || st.f.from) Object.assign(st, { f: { ...st.f, day: undefined, from: undefined, to: undefined }, from: null }); openStats(false); }); // «Назад» не вернет строку, день и отрезок прежнего периода

const stChatTitle = (id) => (st.data.chats && st.data.chats[id]) || "Удаленный чат";
function stDescribe() {
  const f = st.f, p = [];
  if (f.day) p.push(`день <b>${stDay(f.day)}</b>`);
  if (f.from) p.push(`период <b>${stDay(f.from)} — ${stDay(f.to)}</b>`);
  if (f.wd != null) p.push(`<b>${ST_WD_FULL[f.wd]}, ${f.hour}:00–${f.hour + 1}:00</b>`);
  if (f.purpose) p.push(`назначение <b>${esc(st.data.purposes.find((x) => x.id === f.purpose)?.title || f.purpose)}</b>`);
  if (f.model) p.push(`модель <b>${esc(f.model)}</b>`);
  if (f.chat) p.push(`чат <b>«${esc(stChatTitle(f.chat))}»</b>`);
  if (f.failed) p.push("<b>только с ошибкой</b>");
  if (f.uncounted) p.push("<b>только без счета</b>");
  return p.join(", ");
}
function stRender() {
  stTipHide();
  if (!st.data) return;
  document.querySelectorAll("#stats-view [data-st]").forEach((b) => { if (b.dataset.st === st.screen) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current"); });
  const f = st.f, d = stDescribe();
  $("#st-cmd").textContent = "dvk stats" + (st.screen === "pulse" ? "" : " " + st.screen) + (st.screen === "money" ? " --month" : f.day ? ` --day ${f.day}` : f.from ? ` --from ${f.from} --to ${f.to}` : ` --since ${st.days}d`) + (f.wd != null ? ` --weekday ${f.wd + 1} --hour ${f.hour}` : "") + (f.purpose ? ` --purpose ${f.purpose}` : "") + (f.model ? ` --model ${f.model}` : "") + (f.chat ? ` --chat ${f.chat}` : "");
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
  const rows = v.calls.filter((c) => (!f.day || c.at.slice(0, 10) === f.day) && stInRange(c.at, f) && (f.wd == null || (stWd(c.at) === f.wd && +c.at.slice(11, 13) === f.hour)) && (!f.purpose || c.purpose === f.purpose) && (!f.model || c.model === f.model) && (!f.chat || c.chat === f.chat) && (!f.failed || c.failed) && (!f.uncounted || !c.counted));
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
// stShade colours a cell by its share of max: пусто — фон, дальше — от 18% акцента до полного.
const stShade = (max) => (n) => (n ? `color-mix(in srgb, var(--z-accent) ${Math.round(18 + (n / max) * 82)}%, var(--z-bg-3))` : "var(--z-bg-3)");
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
  // Выделение хранится датами: после перезагрузки периода номера дней значат другое.
  const fa = st.feedSel ? days.findIndex((d) => d.date === st.feedSel.from) : -1, fb = st.feedSel ? days.findIndex((d) => d.date === st.feedSel.to) : -1;
  const sel = fa >= 0 && fb >= fa ? { a: fa, b: fb } : { a: 0, b: n - 1 };
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
  stFeedChart($("#st-c3"), days, ids, grid, st.modes.feed, whole ? null : sel, (a, b) => { st.feedSel = { from: days[a].date, to: days[b].date }; stRender(); });
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
  const shade = stShade(Math.max(1, ...cells.flat()));
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
  const days = st.cal.days, pad = stWd(days[0].date), shade = stShade(Math.max(1, ...days.map((d) => d.calls)));
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
  const m = st.money, hasLim = m.limit !== "", lim = hasLim ? +m.limit.replace(",", ".") : 0, mon = ST_MON_FULL[+m.month.slice(5, 7) - 1];
  // Журнал из Денег — за этот месяц, а не за период Статистики: числа сходятся с долларами.
  const monthF = { from: m.month + "-01", to: m.month + "-" + String(m.daysPassed).padStart(2, "0") };
  const toLog = async (f, why) => { await stEnsure(monthF.from); stGo("log", { ...monthF, ...f }, why); };
  const share = (x) => (m.spent ? Math.round((x / m.spent) * 100) : 0);
  const warn = hasLim && m.spent >= lim && m.spent > 0 ? `Лимит месяца превышен: ${stUsd(m.spent)} из ${stUsd(lim)}.` : hasLim && lim > 0 && m.spent >= lim * 0.8 ? `Потрачено ${Math.round((m.spent / lim) * 100)}% лимита месяца.` : lim && m.forecast > lim ? `По прогнозу месяц выйдет за лимит на ${stUsd(m.forecast - lim)}.` : "";
  const tips = [];
  const topP = m.purposes.find((p) => p.cost > 0);
  if (topP && share(topP.cost) >= 40) tips.push({ text: `«${topP.title}» — ${share(topP.cost)}% расхода месяца (${stUsd(topP.cost)})`, f: { purpose: topP.id }, why: `совет про «${topP.title}» в Деньгах` });
  const search = m.purposes.find((p) => p.id === "search" && p.cost > 0);
  if (search) tips.push({ text: `Шаги поиска моделью стоили ${stUsd(search.cost)}: в режиме «Кто ищет документы → окно» этих шагов нет`, f: { purpose: "search" }, why: "совет про поиск моделью в Деньгах" });
  const topM = m.models.find((x) => x.cost > 0);
  if (topM && m.models.filter((x) => x.cost > 0).length > 1) tips.push({ text: `${topM.id} — ${share(topM.cost)}% расхода месяца (${stUsd(topM.cost)}); модель меняется в «Чат и модель»`, f: { model: topM.id }, why: `совет про ${topM.id} в Деньгах` });
  // Дорогой чат (#541): длинный разговор дорожает с каждым вопросом — сжатие (#539) режет его.
  const topC = (m.chats || []).find((c) => c.cost > 0 && c.id);
  if (topC && topC.share >= 0.4 && topC.calls >= 4 && topC.title !== "Удаленный чат") tips.push({ text: `Чат «${topC.title}» — ${Math.round(topC.share * 100)}% расхода месяца (${stUsd(topC.cost)}): длинный разговор дорожает с каждым вопросом, сжатие старой части в конспект это снимает`, f: { chat: topC.id }, why: `совет про чат «${topC.title}» в Деньгах`, act: "Открыть и сжать", run: () => openCostlyChat(topC.id) });
  if (m.unpriced) tips.push({ text: `${m.unpriced} ${plural(m.unpriced, "вызов", "вызова", "вызовов")} без цены модели — не вошли в расход; задайте цену в таблице ниже` });
  host.innerHTML = `<div class="st-kpis"><button data-mk="spent"><span>Потрачено в ${mon}</span><b data-n="${m.spent}" data-f="usd">${stUsd(m.spent)}</b><em>за ${m.daysPassed} ${plural(m.daysPassed, "день", "дня", "дней")}</em><span class="to">Журнал →</span></button>
      <button data-mk="forecast"><span>Прогноз на месяц</span><b data-n="${m.forecast}" data-f="usd">${stUsd(m.forecast)}</b><em>по среднему прошедших дней</em></button>
      <button data-mk="limit"><span>Лимит месяца</span><b>${hasLim ? stUsd(lim) : "не задан"}</b><em>${hasLim ? `осталось ${stUsd(Math.max(0, lim - m.spent))}` : "потяните шкалу ниже"}</em></button>
      ${m.unpriced || m.uncounted ? `<button data-mk="unknown"><span>Не вошло в расход</span><b>${stNum(m.unpriced + m.uncounted)}</b><em>${m.unpriced ? `без цены ${m.unpriced}` : ""}${m.unpriced && m.uncounted ? " · " : ""}${m.uncounted ? `без счета ${m.uncounted}` : ""}</em></button>` : ""}</div>
    ${warn ? `<div class="st-warn" role="status">${esc(warn)}</div>` : ""}
    <div class="st-panel"><div class="st-hd"><div><h3>Расход против лимита</h3><div class="st-sub">потяните метку лимита по шкале или впишите число; метка 80% — порог предупреждения</div></div>
      <div class="st-limit-in"><label>лимит, $ <input id="st-lim" inputmode="decimal" value="${esc(m.limit || "")}" placeholder="нет"></label><button class="st-chip" id="st-lim-save">Сохранить</button>${hasLim ? '<button class="st-chip" id="st-lim-off">Снять</button>' : ""}</div></div>
      <div class="st-scale" id="st-scale"></div><div id="st-c6"></div></div>
    <div class="st-grid"><div class="st-panel"><h3>Расход по назначениям</h3><div class="st-sub">нажмите — Журнал с этим назначением</div>${m.purposes.length ? `<div class="st-hbars">${m.purposes.map((p, i) => `<button data-mp="${i}"><span>${stSw(ST_COLORS[p.id] || "--z-faint")}${esc(p.title)}</span><i style="width:${Math.max(2, m.spent ? (p.cost / m.spent) * 100 : 0)}%;background:${stCss(ST_COLORS[p.id] || "--z-faint")}"></i><span>${stUsd(p.cost)}</span></button>`).join("")}</div>` : `<div class="st-sub">В ${mon} вызовов нет.</div>`}</div>
      <div class="st-panel"><h3>Где сэкономить</h3>${tips.length ? `<div class="st-tips">${tips.map((x, i) => `<div>${esc(x.text)}${x.act ? ` <button class="st-chip" data-tip-a="${i}">${esc(x.act)}</button>` : ""}${x.f ? ` <button class="st-chip" data-tip-i="${i}">Журнал →</button>` : ""}</div>`).join("")}</div>` : `<div class="st-sub">Подсказок нет: расход ровный.</div>`}</div></div>
    <div class="st-panel"><h3>Расход по чатам</h3><div class="st-sub">самые дорогие чаты за ${esc(mon)}; доля — от всего расхода месяца, остальное — вызовы вне чатов (разбор, портрет); нажмите — Журнал этого чата</div>${(m.chats || []).filter((c) => c.cost > 0).length ? `<div class="st-hbars chats">${m.chats.filter((c) => c.cost > 0).slice(0, 5).map((c, i) => `<button data-mc="${i}" title="${esc(c.title)}: ${stUsd(c.cost)}, ${c.calls} ${plural(c.calls, "вызов", "вызова", "вызовов")}"><span>${esc(c.title)}</span><i style="width:${Math.max(2, c.share * 100)}%;background:${stCss("--z-accent")}"></i><span>${stUsd(c.cost)} · ${Math.round(c.share * 100)}%</span></button>`).join("")}</div>` : `<div class="st-sub">В ${mon} расхода по чатам нет.</div>`}</div>
    <div class="st-panel"><h3>Цены моделей</h3><div class="st-sub">долларов за 1 млн токенов; «шлюз» — из списка моделей Vercel AI Gateway, раз в сутки; своя цена главнее, пустые поля — снова цена шлюза${m.priceNote ? `. <b>${esc(m.priceNote)}</b>` : ""}</div>
      <div style="overflow-x:auto"><table class="st-table"><thead><tr><th>Модель</th><th class="n">Вызовов</th><th class="n">Расход</th><th class="n">Вход, $</th><th class="n">Выход, $</th><th>Цена</th><th></th></tr></thead><tbody>${m.models.map((x, i) => `<tr data-mi="${i}"><td>${esc(x.id)}</td><td class="n">${stNum(x.calls)}</td><td class="n">${x.priced ? stUsd(x.cost) : "—"}</td>
        ${x.priceable ? `<td class="n"><input class="st-price" data-pin="${i}" value="${esc(x.source === "своя" ? x.in : "")}" placeholder="${esc(x.in || "нет")}" inputmode="decimal"></td><td class="n"><input class="st-price" data-pout="${i}" value="${esc(x.source === "своя" ? x.out : "")}" placeholder="${esc(x.out || "нет")}" inputmode="decimal"></td>
        <td>${x.source || '<span class="st-pill bad">нет цены</span>'}</td><td><button class="st-chip" data-psave="${i}">Сохранить</button></td>` : `<td colspan="4" class="st-sub">шлюз не присылает счет — цена не нужна</td>`}</tr>`).join("")}</tbody></table></div></div>`;
  const reload = async () => { st.money = null; await stMoney(host); if (!stReduce()) stCount(host); };
  const setLimit = async (v) => { try { await native().SetSpendLimit(v); showError(""); } catch (e) { showError("Лимит не сохранен: " + errText(e)); } await reload(); };
  $("#st-lim-save").addEventListener("click", () => setLimit($("#st-lim").value.trim()));
  $("#st-lim").addEventListener("keydown", (e) => e.key === "Enter" && setLimit($("#st-lim").value.trim()));
  if ($("#st-lim-off")) $("#st-lim-off").addEventListener("click", () => setLimit(""));
  host.querySelectorAll('[data-mk="forecast"], [data-mk="limit"]').forEach((b) => b.addEventListener("click", () => $("#st-lim").focus()));
  host.querySelector('[data-mk="spent"]').addEventListener("click", () => toLog({}, `«Потрачено в ${mon}» в Деньгах`));
  host.querySelectorAll("[data-mc]").forEach((b) => b.addEventListener("click", () => { const c = m.chats.filter((x) => x.cost > 0)[+b.dataset.mc]; toLog({ chat: c.id }, `чат «${c.title}» в Деньгах`); }));
  host.querySelectorAll("[data-mp]").forEach((b) => b.addEventListener("click", () => { const p = m.purposes[+b.dataset.mp]; toLog({ purpose: p.id }, `«${p.title}» в Деньгах`); }));
  host.querySelectorAll("[data-tip-i]").forEach((b) => b.addEventListener("click", () => { const x = tips[+b.dataset.tipI]; toLog(x.f, x.why); }));
  host.querySelectorAll("[data-tip-a]").forEach((b) => b.addEventListener("click", () => tips[+b.dataset.tipA].run()));
  host.querySelectorAll("[data-psave]").forEach((b) => b.addEventListener("click", async () => {
    const i = +b.dataset.psave, x = m.models[i];
    let pin = host.querySelector(`[data-pin="${i}"]`).value.trim(), pout = host.querySelector(`[data-pout="${i}"]`).value.trim();
    // Заполнена одна сторона — другая берется из нынешней цены (ее и показывает подсказка поля).
    if (pin || pout) { pin = pin || x.in; pout = pout || x.out; }
    try { await native().SetModelPrice(x.id, pin, pout); showError(""); toast("Сохранено", ` · цена ${x.id}`); }
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
  let val = lim, moved = false;
  h.addEventListener("pointerdown", (e) => { h.setPointerCapture(e.pointerId); h.dataset.drag = "1"; moved = false; });
  h.addEventListener("pointercancel", () => { delete h.dataset.drag; stScale(host, m, lim, setLimit); }); // прерванная протяжка — метка на место
  h.addEventListener("pointermove", (e) => {
    if (!h.dataset.drag) return;
    const r = track.getBoundingClientRect(), k = Math.max(0, Math.min(1, (e.clientX - r.left) / r.width));
    val = +round(k * top);
    moved = true;
    h.style.left = k * 100 + "%";
    h.querySelector("span").textContent = stUsd(val);
  });
  h.addEventListener("pointerup", () => { if (!h.dataset.drag) return; delete h.dataset.drag; if (moved) setLimit(val > 0 ? String(val) : ""); }); // щелчок без протяжки лимит не переписывает
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
state.ac = { open: false, busy: false, draft: {}, editing: "", confirmDel: "", list: [], q: "", env: "", sel: "", shown: {}, adding: false, left: 0, idle: 0, timers: {},
  // Машины (#549): рядом с учетками, тот же замок и Touch ID.
  mode: "acc", machines: [], msel: "", madding: false, mediting: "", mdraft: {}, mconfirm: "", mshown: {}, scan: null };
const AC_HUES = ["--z-sp4", "--z-sp2", "--z-src-violet", "--z-sp6", "--z-sp5", "--z-sp1", "--z-sp3"];
const AC_GRADS = { spectrum: "Спектр DVK", gold: "Золото", ice: "Лед", dusk: "Закат", mono: "Без цвета" };
const AC_NAMES = ["Хранилище", "Шифр", "Сейф", "Ключи"];
// Цвет плашки логина (владелец 09.11: плашка должна выделяться и настраиваться по цветам).
const AC_LOGIN_COLORS = { accent: ["Акцент", ""], gold: ["Золото", "#e8b04a"], green: ["Зеленый", "#5fc98a"], ice: ["Лед", "#5aa9e6"], violet: ["Фиолет", "#9b7cff"], pink: ["Розовый", "#e8688f"], custom: ["Свой цвет", ""] };
const AC_HEX = /^#[0-9a-fA-F]{6}$/;
function acLoginColor() {
  const l = state.accents.look || {}, id = AC_LOGIN_COLORS[l["login-color"]] ? l["login-color"] : "accent";
  const hex = AC_HEX.test(l["login-hex"] || "") ? l["login-hex"] : "#e8b04a";
  return { id, hex, css: id === "custom" ? hex : AC_LOGIN_COLORS[id][1] };
}
function acLoginApply() { const c = acLoginColor(); if (c.css) document.body.style.setProperty("--ac-login", c.css); else document.body.style.removeProperty("--ac-login"); }
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
  $("#ac-error").hidden = true;
  const ac = state.ac;
  if (!ac || !ac.open) return;
  acClose();
}
async function acLoad() {
  const ac = state.ac;
  try { ac.list = (await native().AccessList(ac.q)) || []; }
  catch (e) { ac.list = []; showError("Доступы не прочитать: " + errText(e)); }
  if (!native().MachineList) return; // сборка окна без машин
  try { ac.machines = (await native().MachineList(ac.q)) || []; }
  catch (e) { ac.machines = []; showError("Машины не прочитать: " + errText(e)); }
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
    <div class="crumb"><i class="ac-sw"></i>продукт</div>
    <h3>${esc(c.product)} <span class="ac-env ${esc(c.env)}">${esc(c.envName)}</span></h3>
    <dl class="ac-kv">
      <dt>логин</dt><dd><code class="ac-login" title="Это имя пользователя для входа">${esc(c.login)}</code></dd>
      <dt>пароль</dt><dd><div class="ac-secret"><code class="${open ? "open" : ""}">${open ? esc(open.pw) : "••••••••••••"}</code>
        <button class="ac-btn" data-ac="${open ? "hide" : "show"}" data-id="${esc(c.id)}">${open ? "Скрыть" : "Показать · Touch ID"}</button>
        <button class="ac-btn pri" data-ac="copy" data-id="${esc(c.id)}">Скопировать на 30 с</button>
        ${open ? `<span class="ac-cd" data-cd="${esc(c.id)}">скроется через ${open.left} с</span>` : ""}</div></dd>
      ${row("адрес", c.url)}${row("хост", c.host)}${row("роль", c.role)}${row("права", c.rights)}${row("выдал", [c.issuedBy, c.issuedAt].filter(Boolean).join(", "))}${row("заметка", c.note)}</dl>
    <div class="ac-note">Пароль — в Связке ключей этого Мака; агенту через MCP уходит только карточка, без пароля и заметки.
      ${ac.confirmDel === c.id ? `<span class="ac-del">Снять доступ ${esc(c.login)}? Пароль уйдет из Связки, это не отменить. <button class="ac-btn" data-ac="del-yes" data-id="${esc(c.id)}">Да, снять · Touch ID</button> <button class="ac-btn" data-ac="del-no">Нет</button></span>`
        : `<button class="ac-btn" data-ac="edit" data-id="${esc(c.id)}" title="Поменять адрес, заметку или пароль">Изменить</button> <button class="ac-btn ac-del-btn" data-ac="del" data-id="${esc(c.id)}">Удалить</button>`}</div></div>`;
}
const acProducts = () => [...new Set(state.ac.list.map((c) => c.product))];
function acAddHTML() {
  // Черновик живет в state.ac.draft: перерисовка (фильтр среды, поиск) не стирает набранное. Пароль в
  // состояние не кладется: он остается только в поле и после перерисовки вводится заново.
  const d = state.ac.draft;
  const f = (k, t, type = "text") => `<label>${t}<input name="${k}" type="${type}" autocomplete="off" spellcheck="false"${k !== "password" && d[k] ? ` value="${esc(d[k])}"` : ""}></label>`;
  const env = (v, t) => `<option value="${v}"${(d.env || "prod") === v ? " selected" : ""}>${t}</option>`;
  return `<form class="ac-add" id="ac-add">
    ${f("product", "продукт (куда доступ)")}<label>среда<select name="env">${env("prod", "прод")}${env("stage", "стенд")}${env("test", "тест")}${env("dev", "dev")}</select></label>
    ${f("login", "логин (имя для входа)")}${f("url", "адрес (ссылка для входа)")}${f("host", "хост (сервер или IP)")}${f("role", "роль")}${f("rights", "права")}${f("issuedBy", "кто выдал")}${f("issuedAt", "когда выдан", "date")}${f("note", "заметка (видна только в окне)")}
    ${f("password", "пароль", "password")}
    <div class="ac-acts"><button type="button" class="ac-btn" data-ac="add-cancel">Отмена</button><button class="ac-btn pri" type="submit">Сохранить</button></div></form>`;
}
// Правка доступа: можно исправить и продукт, среду, логин (ошибся при вводе): адрес карточки пересчитается,
// пароль в Связке переедет сам. Пароль необязателен: пусто — прежний остается. Поля держит черновик.
function acEditHTML(c) {
  const d = state.ac.draft, v = (k, cur) => esc(d[k] !== undefined ? d[k] : cur || "");
  const f = (k, t, cur, type = "text") => `<label>${t}<input name="${k}" type="${type}" value="${v(k, cur)}" autocomplete="off" spellcheck="false"></label>`;
  const env = d.env !== undefined ? d.env : c.env;
  const opt = (val, t) => `<option value="${val}"${env === val ? " selected" : ""}>${t}</option>`;
  return `<form class="ac-add ac-edit" id="ac-edit" data-id="${esc(c.id)}">
    <p class="ac-edit-who">Изменить доступ. Если поправить продукт, среду или логин, карточка и пароль перенесутся сами.</p>
    ${f("product", "продукт (куда доступ)", c.product)}<label>среда<select name="env">${opt("prod", "прод")}${opt("stage", "стенд")}${opt("test", "тест")}${opt("dev", "dev")}</select></label>${f("login", "логин (имя для входа)", c.login)}
    ${f("url", "адрес (ссылка для входа)", c.url)}${f("host", "хост (сервер или IP)", c.host)}${f("role", "роль", c.role)}${f("rights", "права", c.rights)}
    ${f("issuedBy", "кто выдал", c.issuedBy)}${f("issuedAt", "когда выдан", c.issuedAt, "date")}${f("note", "заметка (видна только в окне)", c.note)}
    ${f("password", "новый пароль (пусто — не менять)", "", "password")}
    <div class="ac-acts"><button type="button" class="ac-btn" data-ac="edit-cancel">Отмена</button><button class="ac-btn pri" type="submit">Сохранить · Touch ID</button></div></form>`;
}
// Хвост экрана учеток: форма, список и карточка (как было до машин).
function acAccTailHTML(ac, list, cur, groups) {
  return `${ac.adding ? acAddHTML() : ""}${ac.editing && cur && cur.id === ac.editing ? acEditHTML(cur) : ""}
    ${list.length ? `<div class="ac-grid"><div class="ac-list" role="listbox" aria-label="Доступы">${groups}</div>${acCardHTML(cur)}</div>` : `<p class="muted">${ac.list.length ? "Под этот фильтр доступов нет." : "Доступов пока нет: «Добавить доступ» или kbengine creds add в терминале."}</p>`}`;
}
function acInsideHTML() {
  const ac = state.ac, L = acLook(), sec = acLockSec();
  const list = ac.list.filter((c) => !ac.env || c.env === ac.env);
  const cur = list.find((c) => c.id === ac.sel) || list[0];
  const prods = acProducts();
  const groups = [...new Set(list.map((c) => c.product))].map((p) => {
    const items = list.filter((c) => c.product === p), n = items.length;
    return `<div class="ac-grp" style="--c:var(${AC_HUES[prods.indexOf(p) % AC_HUES.length]})"><div class="ac-gh"><b>${esc(p)}</b><span class="n">${n} ${plural(n, "доступ", "доступа", "доступов")}</span></div>
      ${items.map((c) => `<button class="ac-acc" data-ac="sel" data-id="${esc(c.id)}" aria-selected="${cur && c.id === cur.id}"><span><i class="ac-lbl">логин</i> ${esc(c.login)}</span><span class="ac-env ${esc(c.env)}">${esc(c.envName)}</span><small>${esc(c.role || c.url || c.host)}</small></button>`).join("")}</div>`;
  }).join("");
  return `<div class="ac-inside">
    <div class="ac-meta"><span class="mini"><i class="ac-led on"></i>«${esc(L.name)}» открыто</span>
      <span class="mini">${sec ? `закроется через <b id="ac-idle">${acFmt(ac.left)}</b> без дела` : "само не закрывается"} <button class="ac-btn" data-ac="close">Закрыть</button></span></div>
    <div class="ac-find"><input id="ac-q" type="search" placeholder="найти доступ: продукт, среда, логин, роль, адрес" value="${esc(ac.q)}" autocomplete="off" spellcheck="false">
      ${["", "prod", "stage", "test", "dev"].map((e) => `<button class="look-chip" data-ac="env" data-env="${e}" aria-pressed="${ac.env === e}">${e ? { prod: "прод", stage: "стенд", test: "тест", dev: "dev" }[e] : "все"}</button>`).join("")}
      ${acModeHTML()}<button class="ac-btn pri" data-ac="${ac.mode === "mach" ? "madd" : "add"}">${(ac.mode === "mach" ? ac.madding : ac.adding) ? "Свернуть форму" : ac.mode === "mach" ? "Добавить машину" : "Добавить доступ"}</button></div>
    ${ac.mode === "mach" ? acMachTailHTML() : acAccTailHTML(ac, list, cur, groups)}
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
  for (const id of Object.keys(ac.mshown)) { clearInterval(ac.timers["m:" + id]); delete ac.mshown[id]; }
  Object.assign(ac, { open: false, adding: false, editing: "", draft: {}, confirmDel: "", list: [], machines: [], madding: false, mediting: "", mdraft: {}, mconfirm: "", scan: null });
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
for (const ev of ["click", "keydown", "input"]) $("#access-view").addEventListener(ev, (e) => {
  if (state.ac.open) state.ac.left = acLockSec();
  if (!e.target.closest("#ac-error")) $("#ac-error").hidden = true; // прежняя ошибка снимается следующим действием, «Подробнее» ее не трогает
}, true);
// ---- Машины (#549) --------------------------------------------------------------------------------------
// Сервер, на котором лежит много проектов: не учетка на стенде, а хост, порт, способ входа, цепочка через
// бастион и список проектов. Пароль (при входе по паролю) — в Связке, только по Touch ID; агенту через MCP
// уходит карточка без пароля, пути ключа, подсказки про токен и заметки. Скан читает проекты по ssh.
const MA_AUTH = { password: "Пароль", key: "Ключ ssh", token: "Сертификат или токен" };
function acModeHTML() {
  if (!native().MachineList) return "";
  return `<span class="ac-seg" role="group" aria-label="Что показывать">${[["acc", "Учетки"], ["mach", "Машины"]]
    .map(([k, t]) => `<button class="look-chip" data-ac="mode" data-mode="${k}" aria-pressed="${state.ac.mode === k}">${t}</button>`).join("")}</span>`;
}
const maProjectsText = (m) => ((m && m.projects) || []).map((p) => [p.name, p.path, p.port, p.what].join(" | ").replace(/( \| )+$/, "")).join("\n");
// «имя | путь | порт | что это», по строке на проект; лишнее после четвертой черты остается в «что это».
function maParseProjects(text) {
  return text.split("\n").map((l) => l.trim()).filter(Boolean).map((l) => { const [name, path = "", port = "", ...what] = l.split("|").map((x) => x.trim()); return { name, path, port, what: what.join(" | ") }; });
}
function acMachFormHTML(m) {
  const d = state.ac.mdraft, v = (k, cur) => esc(d[k] !== undefined ? d[k] : cur === undefined || cur === null ? "" : cur);
  const f = (k, t, cur, type = "text") => `<label>${t}<input name="${k}" type="${type}" value="${v(k, cur)}" autocomplete="off" spellcheck="false"></label>`;
  const sel = (k, t, opts, cur) => { const val = d[k] !== undefined ? d[k] : cur; return `<label>${t}<select name="${k}">${opts.map(([o, n]) => `<option value="${esc(o)}"${val === o ? " selected" : ""}>${esc(n)}</option>`).join("")}</select></label>`; };
  const others = state.ac.machines.filter((x) => !m || x.id !== m.id).map((x) => [x.id, x.name]);
  const pj = d.projects !== undefined ? d.projects : maProjectsText(m);
  return `<form class="ac-add ${m ? "ac-edit" : ""}" id="${m ? "mach-edit" : "mach-add"}" data-id="${m ? esc(m.id) : ""}">
    ${m ? `<p class="ac-edit-who">Изменить машину. Пароль пустой — прежний остается.</p>` : ""}
    ${f("name", "имя машины", m && m.name)}${f("group", "группа (папка в списке)", m && m.group)}${sel("env", "среда (необязательно)", [["", "не задана"], ["prod", "прод"], ["stage", "стенд"], ["test", "тест"], ["dev", "dev"]], m ? m.env : "")}
    ${f("host", "хост (сервер или IP)", m && m.host)}${f("port", "порт ssh (пусто — 22)", m && m.port !== 22 ? m.port : "", "number")}${f("user", "пользователь", m && m.user)}
    ${sel("auth", "как входить", [["key", "Ключ ssh"], ["password", "Пароль"], ["token", "Сертификат или токен"]], m ? m.auth : "key")}${f("keyPath", "файл ключа (только путь)", m && m.keyPath)}${sel("jump", "заходить через (бастион)", [["", "напрямую"], ...others], m ? m.jump : "")}
    ${f("tokenHint", "токен или сертификат: где взять", m && m.tokenHint)}${f("note", "заметка (видна только в окне)", m && m.note)}${f("password", "пароль (если вход по паролю)", "", "password")}
    <label class="ac-wide ac-chk"><input type="checkbox" name="scan"${m ? "" : " checked"}> просканировать после сохранения <span class="muted">по ssh только чтение, на машине ничего не меняется; нужен ключ ssh или агент, по паролю скан не работает</span></label>
    <label class="ac-wide">что на машине: проект | путь | порт | что это, по строке на проект<textarea name="projects" rows="4" spellcheck="false">${esc(pj)}</textarea></label>
    <div class="ac-acts"><button type="button" class="ac-btn" data-ac="${m ? "medit-cancel" : "madd-cancel"}">Отмена</button><button class="ac-btn pri" type="submit">Сохранить${m ? " · Touch ID" : ""}</button></div></form>`;
}
function acScanHTML(m) {
  const s = state.ac.scan;
  if (!s || s.id !== m.id) return `<div class="ac-scan idle"><button class="ac-btn pri ac-scan-go" data-ac="mscan" data-id="${esc(m.id)}"><svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="2" fill="currentColor"/><path d="M4.6 4.6a4.8 4.8 0 0 0 0 6.8M11.4 4.6a4.8 4.8 0 0 1 0 6.8M2.3 2.3a8 8 0 0 0 0 11.4M13.7 2.3a8 8 0 0 1 0 11.4" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>Сканировать машину</button><span class="muted">по ssh только чтение: проекты на машине заполнятся сами</span></div>`;
  if (s.step === "ask") return `<div class="ac-scan"><b>Скан «${esc(m.name)}»</b><p class="muted">По ssh выполнится только чтение, на машине ничего не меняется:</p><pre class="ac-plan">${s.plan.map(esc).join("\n")}</pre>
    <p class="muted">Вход по вашему ключу или агенту ssh на этом Mac; пароль не подставляется, секреты и содержимое файлов не читаются, в карточку попадают имена, пути и порты.${m.auth === "password" ? " <b>У этой машины вход по паролю, скан по паролю не работает.</b>" : ""}</p>
    <div class="ac-acts"><button class="ac-btn" data-ac="mscan-x">Отмена</button><button class="ac-btn pri" data-ac="mscan-run" data-id="${esc(m.id)}"${m.auth === "password" ? " disabled" : ""}>Запустить скан</button></div></div>`;
  if (s.step === "run") return `<div class="ac-scan run"><i class="ac-run-bar" aria-hidden="true"></i><span class="muted">Сканирую «${esc(m.name)}»…</span></div>`;
  const n = s.found.filter((f) => f.new).length;
  return `<div class="ac-scan"><b>Найдено ${s.found.length}, новых ${n}</b>
    ${s.found.length ? `<div class="ac-found">${s.found.map((f, i) => `<label><input type="checkbox" data-scan="${i}"${f.new ? " checked" : " disabled"}> <b>${esc(f.name)}</b> <span class="muted">${[f.path, f.port ? "порт " + f.port : "", f.what, f.new ? "" : "уже в списке"].filter(Boolean).map(esc).join(" · ")}</span></label>`).join("")}</div>` : `<p class="muted">Ничего не нашлось: на машине нет docker, служб, сайтов nginx и каталогов в /srv, либо нет прав на чтение.</p>`}
    <div class="ac-acts"><button class="ac-btn" data-ac="mscan-x">Закрыть</button><button class="ac-btn pri" data-ac="mscan-add" data-id="${esc(m.id)}"${n ? "" : " disabled"}>Добавить выбранные</button></div>
    <p class="muted">Повторный скан покажет только новое; то, что вы вписали или поправили, не затирается.</p></div>`;
}
function acMachCardHTML(m) {
  const ac = state.ac, open = ac.mshown[m.id], row = (t, v) => (v ? `<dt>${t}</dt><dd>${esc(v)}</dd>` : "");
  const pw = m.auth === "password" ? `<dt>пароль</dt><dd><div class="ac-secret"><code class="${open ? "open" : ""}">${open ? esc(open.pw) : m.hasPassword ? "••••••••••••" : "не задан"}</code>
    <button class="ac-btn" data-ac="${open ? "mhide" : "mshow"}" data-id="${esc(m.id)}">${open ? "Скрыть" : "Показать · Touch ID"}</button>
    <button class="ac-btn pri" data-ac="mcopy" data-id="${esc(m.id)}">Скопировать на 30 с</button>${open ? `<span class="ac-cd" data-cd="m:${esc(m.id)}">скроется через ${open.left} с</span>` : ""}</div></dd>` : "";
  const pj = (m.projects || []).length ? `<table class="ac-pj"><thead><tr><th>проект</th><th>путь</th><th>порт</th><th>что это</th></tr></thead><tbody>${(m.projects || []).map((p) => `<tr><td>${esc(p.name)}</td><td>${esc(p.path || "")}</td><td>${esc(p.port || "")}</td><td>${esc(p.what || "")}</td></tr>`).join("")}</tbody></table>` : `<p class="muted">Проектов пока нет: просканируйте машину или впишите их при правке.</p>`;
  return `<div class="ac-card" style="--c:var(--z-sp4)">
    <div class="crumb"><i class="ac-sw"></i>машина${m.group ? " · " + esc(m.group) : ""}</div>
    <h3>${esc(m.name)} ${m.env ? `<span class="ac-env ${esc(m.env)}">${esc(m.envName)}</span>` : ""}</h3>
    ${m.command ? `<div class="ac-cmd"><code>${esc(m.command)}</code><button class="ac-btn" data-ac="mcmd" data-id="${esc(m.id)}">Скопировать</button></div>` : `<p class="ac-warn">Цепочка не собирается: ${esc(m.chainError)}</p>`}
    <dl class="ac-kv"><dt>хост</dt><dd><code class="ac-login">${esc(m.host)}:${m.port}</code></dd><dt>пользователь</dt><dd>${esc(m.user)}</dd>
      <dt>вход</dt><dd>${esc(MA_AUTH[m.auth] || m.auth)}${m.jump ? ` · через ${esc((ac.machines.find((x) => x.id === m.jump) || { name: m.jump }).name)}` : ""}</dd>
      ${row("ключ", m.keyPath)}${row("где взять", m.tokenHint)}${pw}${row("заметка", m.note)}</dl>
    <h4 class="ac-sub">Что на машине</h4>${pj}${acScanHTML(m)}
    <div class="ac-note">Пароль — в Связке ключей этого Мака; агенту через MCP уходит карточка без пароля, пути ключа, подсказки про токен и заметки.
      ${ac.mconfirm === m.id ? `<span class="ac-del">Снять машину ${esc(m.name)}? Пароль уйдет из Связки, это не отменить. <button class="ac-btn" data-ac="mdel-yes" data-id="${esc(m.id)}">Да, снять · Touch ID</button> <button class="ac-btn" data-ac="mdel-no">Нет</button></span>`
        : `<button class="ac-btn" data-ac="medit" data-id="${esc(m.id)}" title="Поменять хост, вход, проекты, заметку или пароль">Изменить</button> <button class="ac-btn ac-del-btn" data-ac="mdel" data-id="${esc(m.id)}">Удалить</button>`}</div></div>`;
}
// Хвост экрана машин: форма, список слева (по группам) и карточка справа; фильтр среды и поиск работают так же.
function acMachTailHTML() {
  const ac = state.ac, list = ac.machines.filter((m) => !ac.env || m.env === ac.env);
  const cur = list.find((m) => m.id === ac.msel) || list[0];
  const gname = (m) => m.group || "Без группы", groups = [...new Set(list.map(gname))];
  const left = groups.map((g) => { const items = list.filter((m) => gname(m) === g), n = items.length;
    return `<div class="ac-grp" style="--c:var(${AC_HUES[groups.indexOf(g) % AC_HUES.length]})"><div class="ac-gh"><b>${esc(g)}</b><span class="n">${n} ${plural(n, "машина", "машины", "машин")}</span></div>
      ${items.map((m) => `<button class="ac-acc" data-ac="msel" data-id="${esc(m.id)}" aria-selected="${cur && m.id === cur.id}"><span><i class="ac-lbl">машина</i> ${esc(m.name)}</span>${m.env ? `<span class="ac-env ${esc(m.env)}">${esc(m.envName)}</span>` : ""}</button>`).join("")}</div>`; }).join("");
  return `${ac.madding ? acMachFormHTML(null) : ""}${ac.mediting && cur && cur.id === ac.mediting ? acMachFormHTML(cur) : ""}
    ${list.length ? `<div class="ac-grid"><div class="ac-list" role="listbox" aria-label="Машины">${left}</div>${acMachCardHTML(cur)}</div>` : `<p class="muted">${ac.machines.length ? "Под этот фильтр машин нет." : "Машин пока нет: «Добавить машину» заведет первую, а скан сам заполнит проекты на ней."}</p>`}`;
}
// Форма окна → то, что принимает Go: порт числом, проекты списком.
function maInput(fd) {
  const o = { ...fd, port: fd.port ? Number(fd.port) : 0, projects: maParseProjects(fd.projects || "") };
  delete o.password;
  delete o.scan;
  return o;
}
async function acMShow(id) {
  const ac = state.ac;
  try {
    const pw = await native().MachineReveal(id);
    ac.mshown[id] = { pw, left: 30 };
    clearInterval(ac.timers["m:" + id]);
    ac.timers["m:" + id] = setInterval(() => {
      const s = ac.mshown[id]; if (!s) return;
      if (--s.left <= 0) { acMHide(id); return; }
      document.querySelectorAll(`[data-cd="${CSS.escape("m:" + id)}"]`).forEach((e) => (e.textContent = `скроется через ${s.left} с`));
    }, 1000);
    acRender();
  } catch (e) { showError(errText(e)); }
}
function acMHide(id) { const ac = state.ac; clearInterval(ac.timers["m:" + id]); delete ac.mshown[id]; acRender(); }
async function acMCopyCmd(btn, id) {
  const m = state.ac.machines.find((x) => x.id === id); if (!m || !m.command) return;
  try {
    if (window.runtime && window.runtime.ClipboardSetText) { if (!(await window.runtime.ClipboardSetText(m.command))) throw new Error("буфер"); }
    else await navigator.clipboard.writeText(m.command);
    btn.textContent = "скопировано ✓";
  } catch { const r = document.createRange(); r.selectNodeContents(btn.previousElementSibling); const sel = getSelection(); sel.removeAllRanges(); sel.addRange(r); btn.textContent = "выделено — ⌘C"; }
}
// Действия машин из общего обработчика кликов; true — клик обработан здесь.
async function acMachClick(b, ac, id) {
  switch (b.dataset.ac) {
    case "mode": ac.mode = b.dataset.mode; ac.scan = null; ac.confirmDel = ""; ac.mconfirm = ""; acRender(); break;
    case "msel": ac.msel = id; ac.mconfirm = ""; ac.mediting = ""; ac.mdraft = {}; ac.scan = null; acRender(); break;
    case "madd": ac.madding = !ac.madding; ac.mediting = ""; acRender(); if (ac.madding) $("#mach-add [name=name]").focus(); break;
    case "madd-cancel": ac.madding = false; ac.mdraft = {}; acRender(); break;
    case "medit": ac.mediting = id; ac.madding = false; ac.mconfirm = ""; ac.mdraft = {}; acRender(); $("#mach-edit [name=name]").focus(); break;
    case "medit-cancel": ac.mediting = ""; ac.mdraft = {}; acRender(); break;
    case "mshow": acMShow(id); break;
    case "mhide": acMHide(id); break;
    case "mcopy":
      try { await native().MachineCopy(id); toast("Скопировано", " · пароль машины в буфере на 30 с, менеджеры буфера его не сохранят"); }
      catch (e) { showError(errText(e)); }
      break;
    case "mcmd": acMCopyCmd(b, id); break;
    case "mdel": ac.mconfirm = id; acRender(); break;
    case "mdel-no": ac.mconfirm = ""; acRender(); break;
    case "mdel-yes":
      if (ac.deleting) break;
      ac.deleting = true; b.disabled = true;
      try {
        await native().MachineDelete(id);
        ac.mconfirm = ""; delete ac.mshown[id]; clearInterval(ac.timers["m:" + id]); ac.msel = "";
        await acLoad(); acRender();
        toast("Снято", " · машина удалена: карточка и пароль из Связки");
      } catch (err) { showError("Машина не снята: " + errText(err)); }
      finally { ac.deleting = false; }
      break;
    case "mscan":
      try { ac.scan = { id, step: "ask", plan: (await native().MachineScanPlan()) || [] }; acRender(); }
      catch (e) { showError(errText(e)); }
      break;
    case "mscan-x": ac.scan = null; acRender(); break;
    case "mscan-run": {
      const plan = ac.scan && ac.scan.plan;
      ac.scan = { id, step: "run", plan }; acRender();
      try { ac.scan = { id, step: "done", plan, found: (await native().MachineScan(id)) || [] }; }
      catch (e) { showError("Скан не удался: " + errText(e)); ac.scan = { id, step: "ask", plan }; }
      acRender();
      break;
    }
    case "mscan-add": {
      const m = ac.machines.find((x) => x.id === id), s = ac.scan; if (!m || !s || s.step !== "done") break;
      const picked = [...document.querySelectorAll("[data-scan]:checked")].map((c) => s.found[+c.dataset.scan]);
      if (!picked.length) break;
      const input = { name: m.name, group: m.group, env: m.env, host: m.host, port: m.port, user: m.user, auth: m.auth, keyPath: m.keyPath, jump: m.jump, tokenHint: m.tokenHint, note: m.note,
        projects: [...(m.projects || []), ...picked.map((f) => ({ name: f.name, path: f.path || "", port: f.port || "", what: f.what }))] };
      try {
        await native().MachineUpdate(id, input, "");
        ac.scan = null; await acLoad(); acRender();
        toast("Добавлено", ` · из скана: ${picked.length} ${plural(picked.length, "проект", "проекта", "проектов")}`);
      } catch (err) { showError("Проекты не добавлены: " + errText(err)); }
      break;
    }
    default: return false;
  }
  return true;
}
// Скан после сохранения (галочка формы): найденное новое вставляется само, без выбора;
// пусто — «проектов пока 0». Не удался — машина все равно сохранена, об этом и говорим.
async function acAutoScan(name) {
  const ac = state.ac, m = ac.machines.find((x) => x.name === name);
  if (!m) return;
  if (m.auth === "password") return toast("Скан не выполнен", " · по паролю скан не работает: добавьте ключ ssh или агент, проектов пока 0", null, "bad");
  let found;
  try { found = (await native().MachineScan(m.id)) || []; }
  catch (e) { return toast("Машина сохранена, скан не удался", " · " + errText(e), null, "bad"); }
  const fresh = found.filter((f) => f.new);
  if (!fresh.length) return toast("Скан", found.length ? " · новых проектов нет" : " · проектов пока 0");
  const input = { name: m.name, group: m.group, env: m.env, host: m.host, port: m.port, user: m.user, auth: m.auth, keyPath: m.keyPath, jump: m.jump, tokenHint: m.tokenHint, note: m.note,
    projects: [...(m.projects || []), ...fresh.map((f) => ({ name: f.name, path: f.path || "", port: f.port || "", what: f.what }))] };
  try { await native().MachineUpdate(m.id, input, ""); await acLoad(); acRender(); toast("Скан", ` · найдено и добавлено: ${fresh.length} ${plural(fresh.length, "проект", "проекта", "проектов")}`); }
  catch (err) { showError("Проекты из скана не добавлены: " + errText(err)); }
}
async function acMachSubmit(form) {
  const fd = Object.fromEntries(new FormData(form)), pw = fd.password || "", scan = "scan" in fd;
  form.elements.password.value = ""; // пароль не остается в поле
  const input = maInput(fd), ac = state.ac;
  if (form.id === "mach-edit") {
    try { await native().MachineUpdate(form.dataset.id, input, pw); ac.mediting = ""; ac.mdraft = {}; await acLoad(); acRender(); toast("Сохранено", pw ? " · машина обновлена, пароль сменен" : " · машина обновлена"); }
    catch (err) { showError("Машина не изменена: " + errText(err)); return; }
    if (scan) await acAutoScan(input.name);
    return;
  }
  try { await native().MachineAdd(input, pw); ac.madding = false; ac.mdraft = {}; await acLoad(); acRender(); toast("Сохранено", pw ? " · машина добавлена, пароль — в Связке ключей" : " · машина добавлена"); }
  catch (err) { showError("Машина не сохранена: " + errText(err)); return; }
  if (scan) await acAutoScan(input.name);
}
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
  if (await acMachClick(b, ac, id)) return;
  switch (b.dataset.ac) {
    case "open": acOpen(); break;
    case "close": acClose(); break;
    case "sel": ac.sel = id; ac.confirmDel = ""; ac.editing = ""; ac.draft = {}; acRender(); break;
    case "env": ac.env = b.dataset.env; ac.confirmDel = ""; acRender(); break;
    case "show": acShow(id); break;
    case "hide": acHide(id); break;
    case "copy": acCopy(id); break;
    case "edit": ac.editing = id; ac.adding = false; ac.confirmDel = ""; ac.draft = {}; acRender(); $("#ac-edit [name=url]").focus(); break;
    case "edit-cancel": ac.editing = ""; ac.draft = {}; acRender(); break;
    case "add": ac.adding = !ac.adding; ac.editing = ""; acRender(); if (ac.adding) $("#ac-add [name=product]").focus(); break; // свернутая форма держит набранное
    case "add-cancel": ac.adding = false; ac.draft = {}; acRender(); break;
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
$("#access-view").addEventListener("input", (e) => {
  const el = e.target;
  if (el.form && (el.form.id === "ac-add" || el.form.id === "ac-edit") && el.name !== "password") state.ac.draft[el.name] = el.value;
  if (el.form && (el.form.id === "mach-add" || el.form.id === "mach-edit") && el.name !== "password") state.ac.mdraft[el.name] = el.value;
});
$("#access-view").addEventListener("submit", async (e) => {
  if (e.target.id === "mach-add" || e.target.id === "mach-edit") { e.preventDefault(); acMachSubmit(e.target); return; }
  if (e.target.id === "ac-edit") {
    e.preventDefault();
    const fd = Object.fromEntries(new FormData(e.target)), pw = fd.password, id = e.target.dataset.id;
    delete fd.password;
    e.target.elements.password.value = ""; // пароль не остается в поле
    try {
      await native().AccessUpdate(id, fd, pw || "");
      state.ac.editing = ""; state.ac.draft = {}; state.ac.sel = ""; // адрес карточки мог измениться: выбор встанет на первую
      await acLoad(); acRender();
      toast("Сохранено", pw ? " · доступ обновлен, пароль сменен" : " · доступ обновлен");
    } catch (err) { showError("Доступ не изменен: " + errText(err)); }
    return;
  }
  if (e.target.id !== "ac-add") return;
  e.preventDefault();
  const fd = Object.fromEntries(new FormData(e.target)), pw = fd.password;
  delete fd.password;
  e.target.elements.password.value = ""; // пароль не остается в поле
  try {
    await native().AccessAdd(fd, pw);
    state.ac.adding = false;
    state.ac.draft = {};
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
  const customLock = !AC_LOCKS.some(([v]) => v === L.lock);
  host.innerHTML = `
    <div class="row"><span>Название на табличке</span><div class="opts">${AC_NAMES.map((n) => chip("vault-name", n, n, L.name === n)).join("")}
      <label class="muted">свое <input id="look-name" type="text" maxlength="40" value="${AC_NAMES.includes(L.name) ? "" : esc(L.name)}" style="width:140px"${dis}></label></div></div>
    <div class="row"><span>Градиент сейфа</span><div class="opts">${Object.entries(AC_GRADS).map(([g, n]) => chip("vault-grad", g, n, L.grad === g, `<i class="gs ink-${g}"></i>`)).join("")}</div></div>
    <div class="row"><span>Открытие</span><div class="opts">${[["full", "полное, 2 с"], ["short", "короткое, 0,8 с"], ["none", "без анимации"]].map(([v, n]) => chip("vault-anim", v, n, L.anim === v)).join("")}</div></div>
    <div class="row"><span>Цвет плашки логина</span><div class="opts">${Object.entries(AC_LOGIN_COLORS).map(([id, [n, hex]]) => chip("login-color", id, n, acLoginColor().id === id, hex ? `<i class="gs" style="background:${hex}"></i>` : "")).join("")}
      ${acLoginColor().id === "custom" ? `<label class="muted"><input type="color" id="look-login-hex" value="${acLoginColor().hex}"${dis}></label>` : ""}
      <span class="muted">плашка с логином на карточке доступа</span></div></div>
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
  if (e.target.id === "look-login-hex" && AC_HEX.test(e.target.value)) saveLook("login-hex", e.target.value);
  if (e.target.id === "look-name" && e.target.value.trim()) saveLook("vault-name", e.target.value.trim().slice(0, 40));
  if (e.target.id !== "look-lock") return;
  const n = Math.round(+e.target.value);
  if (n >= 1 && n <= 1440) saveLook("vault-lock", String(n));
});

// --- Справка (решение владельца 07.11) ----------------------------------------
const hp = { terms: helpTerms(""), sec: "", cur: 0, sel: -1, first: true, mark: helpMarker([]) };
const hpKeys = (k) => k.map((x) => (x === "→" || x === "…" ? `<span class="hp-sep">${x}</span>` : `<span class="kbd">${hp.mark(x)}</span>`)).join(" ");
const hpHead = (it) => (it.cmd ? `<div class="hp-cmd"><span class="p">$ </span>${hp.mark(it.cmd)}</div>` : hpKeys(it.k));
function hpRender(animate) {
  const q = hp.terms.words.length > 0;
  const hits = new Map(HELP.map((s) => [s.id, helpHits(s, hp.terms)])); // один раз на отрисовку: счета и список не разойдутся
  const shown = q ? HELP.filter((s) => (!hp.sec || s.id === hp.sec) && hits.get(s.id).length) : [HELP[hp.cur]];
  const total = HELP.reduce((n, s) => n + s.items.length, 0);
  const found = q ? shown.reduce((n, s) => n + hits.get(s.id).length, 0) : total;
  $("#hp-count").textContent = q ? `найдено ${found} из ${total}` : `${total} ${plural(total, "команда", "команды", "команд")} и клавиш`;
  $("#hp-nav").innerHTML = HELP.map((s, i) => {
    const n = hits.get(s.id).length;
    return `<button data-hs="${i}" aria-current="${q ? hp.sec === s.id : i === hp.cur}" class="${q && !n ? "none" : ""}"><span class="hp-dot" style="background:${s.c}"></span>${esc(s.t)}<span class="n">${n}</span></button>`;
  }).join("");
  $("#hp-chips").innerHTML = HELP.map((s) => `<button data-hc="${s.id}" aria-pressed="${hp.sec === s.id}"><span class="hp-dot" style="background:${s.c}"></span>${esc(s.t)}${q ? ` <span class="n">${hits.get(s.id).length}</span>` : ""}</button>`).join("");
  let idx = 0;
  const main = $("#hp-main");
  main.classList.toggle("anim", !!animate);
  main.innerHTML = shown.length ? shown.map((s) => `<section class="hp-sec"><h3><span class="hp-dot" style="background:${s.c}"></span>${esc(s.t)}</h3><p class="hp-lead">${esc(s.lead)}</p>` +
    hits.get(s.id).map((it) => `<div class="hp-item" data-hi="${idx}" style="--i:${idx++}"><div class="hp-body">${hpHead(it)}<div class="d">${hp.mark(it.d)}</div></div>${it.cmd ? `<button class="hp-copy" data-copy="${esc(it.cmd)}">копировать</button>` : ""}</div>`).join("") + `</section>`).join("")
    : `<div class="hp-empty">Ничего не нашлось по «<b>${esc($("#hp-q").value)}</b>». Попробуйте задачу другими словами: «трата», «пароль», «ссылки», «открыть окно».</div>`;
  hp.sel = q && idx ? 0 : -1;
  hpPaintSel();
}
function hpPaintSel() {
  document.querySelectorAll(".hp-item").forEach((el) => el.classList.toggle("sel", +el.dataset.hi === hp.sel));
  const el = document.querySelector(".hp-item.sel");
  if (el) el.scrollIntoView({ block: "nearest" });
}
async function hpCopy(btn) {
  const text = btn.dataset.copy;
  try {
    if (window.runtime && window.runtime.ClipboardSetText) { if (!(await window.runtime.ClipboardSetText(text))) throw new Error("буфер"); }
    else await navigator.clipboard.writeText(text);
  } catch {
    const cell = btn.parentElement.querySelector(".hp-cmd"), r = document.createRange();
    r.selectNodeContents(cell); const sel = getSelection(); sel.removeAllRanges(); sel.addRange(r);
    btn.textContent = "выделено — ⌘C"; return;
  }
  btn.textContent = "скопировано ✓"; btn.classList.add("ok");
  setTimeout(() => { btn.textContent = "копировать"; btn.classList.remove("ok"); }, 1400);
}
function hpSetQuery(v) {
  $("#hp-q").value = v;
  hp.terms = helpTerms(v);
  hp.mark = helpMarker(hp.terms.words);
  if (!v) hp.sec = "";
  $("#hp-clear").hidden = !v;
  hpRender(false);
}
function openHelp() {
  hpRender(hp.first); // появление — при первом открытии, дальше вкладка не мигает
  hp.first = false;
  setTimeout(() => $("#hp-q").focus(), 0);
}
$("#hp-q").addEventListener("input", (e) => hpSetQuery(e.target.value));
$("#hp-q").addEventListener("keydown", (e) => {
  const n = document.querySelectorAll(".hp-item").length;
  if (e.key === "ArrowDown" && n) { e.preventDefault(); hp.sel = (hp.sel + 1) % n; hpPaintSel(); }
  else if (e.key === "ArrowUp" && n) { e.preventDefault(); hp.sel = (hp.sel - 1 + n) % n; hpPaintSel(); }
  else if (e.key === "Enter") { const b = document.querySelector(".hp-item.sel .hp-copy"); if (b) { e.preventDefault(); hpCopy(b); } }
  else if (e.key === "Escape" && e.target.value) { e.preventDefault(); e.stopPropagation(); hpSetQuery(""); }
  // пустой Esc уходит дальше: в модуле он прячет окно, как второй Esc поиска
});
$("#hp-clear").addEventListener("click", () => { hpSetQuery(""); $("#hp-q").focus(); });
$("#hp-nav").addEventListener("click", (e) => {
  const b = e.target.closest("[data-hs]"); if (!b) return;
  const i = +b.dataset.hs;
  if (hp.terms.words.length) hp.sec = hp.sec === HELP[i].id ? "" : HELP[i].id;
  else { hp.cur = i; hp.sec = ""; } // нажатый чип не фильтрует следующий поиск молча (ревью #509)
  hpRender(true);
});
$("#hp-chips").addEventListener("click", (e) => {
  const b = e.target.closest("[data-hc]"); if (!b) return;
  hp.sec = hp.sec === b.dataset.hc ? "" : b.dataset.hc;
  if (!hp.terms.words.length && hp.sec) hp.cur = HELP.findIndex((s) => s.id === hp.sec);
  hpRender(true);
});
$("#hp-main").addEventListener("click", (e) => { const b = e.target.closest(".hp-copy"); if (b) hpCopy(b); });
// / и ⌘K — в поиск справки, только когда она открыта и курсор не в поле.
document.addEventListener("keydown", (e) => {
  if ($("#help-view").hidden || settingsOpen()) return;
  const typing = e.target.closest && e.target.closest("input, textarea, [contenteditable]");
  if ((e.key === "/" && !typing) || ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k")) { e.preventDefault(); $("#hp-q").focus(); $("#hp-q").select(); }
});

// --- Маркеты (#580): экран «Ниша» по макету владельца 12.11 ----------------------
// Выгрузку читает Go (MarketsRead): колонки, ниши, оценку и сезон считает домен;
// окно показывает и объясняет. Помнит только путь и выбор колонок — в localStorage.
const mk = { tb: null, picks: [], q: "", sort: "", dir: -1, sel: 0, mp: "all", season: false, cmp: [], first: true };
const MK_KEY = "dvk.markets.niche";
const MK_MON = ["", "январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"];
const MK_IN = ["", "январе", "феврале", "марте", "апреле", "мае", "июне", "июле", "августе", "сентябре", "октябре", "ноябре", "декабре"];
const MK_MP = { wb: "Wildberries", ozon: "Ozon" };
const MK_PART = { growth: "рост к году", perSeller: "выручка на продавца", top10: "свобода от топ-10", months: "сезон" };
const MK_MISS = { growth: "роста к году", perSeller: "выручки (или продаж и цены) и продавцов", top10: "доли топ-10", months: "продаж по месяцам" };
// Что значит колонка: подсказка у заголовка таблицы, строки карточки и выбора колонки.
const MK_TIP = {
  name: "ниша — предмет или категория из выгрузки",
  sales: "продано штук в месяц по всей нише, по данным сервиса аналитики",
  revenue: "выручка всей ниши в месяц, ₽",
  sellers: "сколько продавцов торгует в нише; чем меньше, тем легче выделиться",
  price: "средняя цена продажи в нише, ₽",
  growth: "как изменился спрос к тому же периоду прошлого года; зеленый — растет, красный — падает",
  top10: "какую долю выручки забирают 10 крупнейших продавцов; больше 45 % — войти труднее",
  marketplace: "на каких площадках есть ниша",
};
function mkSave() { try { localStorage.setItem(MK_KEY, JSON.stringify({ path: mk.tb.path, picks: mk.picks, mps: mk.mps })); } catch (_) { /* без хранилища окно просто не вспомнит файл */ } }
function mkLoad() { try { return JSON.parse(localStorage.getItem(MK_KEY) || "null"); } catch (_) { return null; } }
const mkLabel = (id) => (id === "name" ? "Ниша" : id === "season" ? "Сезон" : id === "score" ? "Оценка" : ((mk.tb.fields.find((f) => f.id === id) || {}).label || id));
const mkTip = (title, body) => ` data-tip="${esc(title)}" data-tip-b="${esc(body)}"`;
function mkVal(id, v) {
  if (v === undefined) return "—";
  if (id === "growth") return (v > 0 ? "+" : v < 0 ? "−" : "") + Math.abs(Math.round(v * 100)) + " %";
  if (id === "top10") return Math.round(v * 100) + " %";
  const s = fmt(Math.round(v));
  return id === "revenue" || id === "price" ? s + " ₽" : s;
}
const mkMoney = (v) => (v >= 1e6 ? fmt(Math.round(v / 1e6)) + " млн ₽" : v >= 1e3 ? fmt(Math.round(v / 1e3)) + " тыс. ₽" : fmt(Math.round(v)) + " ₽");
const mkLevel = (s) => (!s.ok ? "na" : s.score >= 65 ? "hi" : s.score >= 45 ? "mid" : "lo");
// mkScoreTip объясняет число: из каких частей и с каким весом сложено, чего не хватило.
function mkScoreTip(s) {
  const miss = s.missing.map((m) => MK_MISS[m]).filter(Boolean);
  if (!s.ok) return mkTip("Оценки нет", "в файле нет " + miss.join(", ") + ": без этого оценку не посчитать");
  const parts = s.parts.map((p) => `${MK_PART[p.id]}: ${Math.round(p.value * 100)} из 100, вес ${Math.round(p.weight * 100)} %`).join(" · ");
  return mkTip(`Оценка ${s.score} из 100`, parts + (miss.length ? ` · без ${miss.join(", ")}: веса поделены заново` : "") + " · подсказка, а не решение");
}
function mkErr(t) { $("#mk-err").textContent = t; $("#mk-err").hidden = !t; }
async function mkRead(path, picks) {
  mkErr("");
  try {
    mk.tb = await native().MarketsRead(path, picks);
    mk.picks = picks;
    mk.cmp = mk.cmp.filter((i) => i < mk.tb.niches.length);
    mkSave();
  } catch (e) { mkErr("Не прочитал файл: " + errText(e)); }
  mkRender();
}
async function openMarkets() {
  if (!mk.first) return;
  mk.first = false;
  mkRender();
  if (!native() || !native().MarketsRead) return mkErr("Экран «Ниша» работает в окне приложения.");
  const last = mkLoad();
  // Ниши MPStats при запуске не качаются: это запросы тарифа (ревью #606). Панель —
  // с прошлой категорией и тем же «последние N дней», загрузка — по «Показать».
  if (last && last.mps && last.path && last.path.startsWith("mpstats:")) {
    const d = last.mps.days;
    mk.mps = { path: last.mps.path, from: d ? mkDaysBack(d) : last.mps.from, to: d ? mkYesterday() : last.mps.to, days: d };
    mk.mpsOpen = true; $("#mk-mps-bar").hidden = false; mkMpsRender(false);
    return mkErr("Ниши из MPStats: нажмите «Показать» — окно загрузит их за выбранные дни");
  }
  if (last && last.path) await mkRead(last.path, last.picks || []);
}
// --- «Ниша → Из MPStats» (решение владельца 13.11): подкатегории категории за
// выбранные дни и те же дни год назад; «Внутрь» и путь сверху — по дереву.
const MK_MPS_PRESETS = [[30, "30 дн."], [90, "90 дн."], [180, "180 дн."], [365, "год"]];
const mkYesterday = () => { const d = new Date(); d.setDate(d.getDate() - 1); return isoDay(d); };
const mkDaysBack = (n) => { const d = new Date(); d.setDate(d.getDate() - n); return isoDay(d); };
const mkIsMPS = () => !!(mk.tb && mk.tb.path && mk.tb.path.startsWith("mpstats:"));
let mkMpsSeq = 0, mkCatsLoaded = false;
// mkLoadCats fills the MPStats paths list once; strict — бросить ошибку наверх.
async function mkLoadCats(strict) {
  if (mkCatsLoaded || !native() || !native().MarketsMPStatsCategories) return;
  try {
    const paths = await native().MarketsMPStatsCategories();
    $("#mk-mps-cats").innerHTML = paths.map((p) => `<option value="${esc(p)}">`).join("");
    mkCatsLoaded = true;
  } catch (e) { if (strict) throw e; }
}
async function mkReadMPS(path, from, to, days) {
  if (!native() || !native().MarketsNichesMPStats) return mkErr("Экран «Ниша» работает в окне приложения.");
  if (!from) days = 30;
  from = from || mkDaysBack(30); to = to || mkYesterday();
  const seq = ++mkMpsSeq, prev = { mps: mk.mps, tb: mk.tb };
  mk.mps = { path: (path || "").split("/").map((x) => x.trim()).filter(Boolean).join("/"), from, to, days }; // «A / B» → «A/B»
  mkErr("");
  $("#mk-mps-bar").hidden = false;
  mkMpsRender(true);
  try {
    const tb = await native().MarketsNichesMPStats(mk.mps.path, from, to);
    if (seq !== mkMpsSeq) return;
    Object.assign(mk, { tb, picks: [], sel: 0, sort: "", q: "", mp: "all", season: false, cmp: [] });
    $("#mk-q").value = "";
    mkSave();
  } catch (e) {
    if (seq !== mkMpsSeq) return;
    // Отказ: панель — к тому, что на экране (ревью #606); таблица из файла под
    // ошибкой MPStats читалась бы как ответ MPStats (живая проверка 13.11) — убираем.
    if (prev.tb && prev.tb.path && prev.tb.path.startsWith("mpstats:")) mk.mps = prev.mps;
    else { mk.tb = null; mk.picks = []; }
    mkErr("Ниши из MPStats не загружены: " + errText(e));
  }
  if (seq === mkMpsSeq) { mkMpsRender(false); mkRender(); }
}
function mkMpsRender(busy) {
  const m = mk.mps || { path: "", from: mkDaysBack(30), to: mkYesterday() };
  if (document.activeElement !== $("#mk-mps-path")) $("#mk-mps-path").value = m.path;
  const parts = m.path ? m.path.split("/") : [];
  $("#mk-mps-crumbs").innerHTML = parts.map((p, i) => `<button type="button" class="lnk" data-mk-crumb="${esc(parts.slice(0, i + 1).join("/"))}"${mkTip("Подняться", "показать ниши этой категории")}>${esc(p)}</button>`).join(" / ") +
    (busy ? ` <span class="muted">загружаю из MPStats…</span>` : "");
  $("#mk-mps-presets").innerHTML = MK_MPS_PRESETS.map(([n, l]) => `<button type="button" data-mk-days="${n}" aria-pressed="${m.to === mkYesterday() && m.from === mkDaysBack(n)}"${mkTip(l, `последние ${n} дней до вчера`)}>${l}</button>`).join("");
  for (const [id, v] of [["#mk-mps-d1", m.from], ["#mk-mps-d2", m.to]]) { const i = $(id); i.max = mkYesterday(); if (document.activeElement !== i) i.value = v; }
  $("#mk-mps-go").disabled = !!busy;
}
$("#mk-mps").addEventListener("click", async () => {
  hideTip();
  if (!native() || !native().MarketsMPStatsCategories) return mkErr("Экран «Ниша» работает в окне приложения.");
  const bar = $("#mk-mps-bar");
  mk.mpsOpen = mkIsMPS() || !mk.mpsOpen; // при нишах из MPStats панель остается
  bar.hidden = !mk.mpsOpen;
  mkMpsRender(false);
  if (bar.hidden) return;
  focusSoon("#mk-mps-path");
  if (mkCatsLoaded) return;
  try {
    await mkLoadCats(true);
  } catch (e) {
    mkErr(errText(e));
    if (/подключения MPStats нет/.test(errText(e))) { await openSettings("conn"); $("#conn-mpstats").hidden = false; $("#conn-add-mpstats").setAttribute("aria-expanded", "true"); focusSoon("#conn-m-key"); }
  }
});
$("#mk-mps-go").addEventListener("click", () => { hideTip(); const m = mk.mps || {}; mkReadMPS($("#mk-mps-path").value, m.from, m.to, m.days); });
$("#mk-mps-path").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); $("#mk-mps-go").click(); } });
$("#mk-mps-bar").addEventListener("click", (e) => {
  const c = e.target.closest("[data-mk-crumb]");
  if (c) { hideTip(); const m = mk.mps || {}; return mkReadMPS(c.dataset.mkCrumb, m.from, m.to, m.days); }
  const d = e.target.closest("[data-mk-days]");
  if (d) { hideTip(); return mkReadMPS((mk.mps || {}).path || $("#mk-mps-path").value, mkDaysBack(+d.dataset.mkDays), mkYesterday(), +d.dataset.mkDays); }
});
let mkMpsDateT = 0;
for (const id of ["#mk-mps-d1", "#mk-mps-d2"]) $(id).addEventListener("change", () => {
  clearTimeout(mkMpsDateT);
  mkMpsDateT = setTimeout(() => {
    const a = $("#mk-mps-d1").value, b = $("#mk-mps-d2").value;
    if (a && b && /^\d{4}-/.test(a) && a.slice(0, 2) === "20" && b <= mkYesterday()) mkReadMPS((mk.mps || {}).path || $("#mk-mps-path").value, a, b);
  }, 500);
});
$("#mk-open").addEventListener("click", async () => {
  hideTip();
  if (!native() || !native().MarketsPickFile) return mkErr("Экран «Ниша» работает в окне приложения.");
  let p = "";
  try { p = await native().MarketsPickFile(); } catch (e) { return mkErr("Файл не выбран: " + errText(e)); }
  if (!p) return;
  Object.assign(mk, { sel: 0, sort: "", q: "", mp: "all", season: false, cmp: [], mps: null });
  $("#mk-mps-bar").hidden = true; mk.mpsOpen = false; mkMpsSeq++; // файл вместо MPStats: запоздавший ответ MPStats не перетрет его
  $("#mk-q").value = "";
  await mkRead(p, []);
});
// mkSeasonLabel — «пик в ноябре-январе»: этот месяц и два следующих, как в оценке.
function mkSeasonLabel() {
  const m = new Date().getMonth() + 1, last = ((m + 1) % 12) + 1;
  return `пик в ${MK_IN[m]}-${MK_IN[last]}`;
}
function mkRender() {
  if ($('[data-mk-tab="niche"]').getAttribute("aria-selected") !== "true") return; // «Ниша» нарисуется при возвращении на вкладку
  const tb = mk.tb;
  $("#mk-empty").hidden = !!tb;
  $("#mk-file").textContent = tb ? `${mkIsMPS() ? tb.file : "Выгрузка " + tb.file} · ${fmt(tb.niches.length)} ${plural(tb.niches.length, "ниша", "ниши", "ниш")}${mkIsMPS() && mk.mps ? ` · ${mk.mps.from.split("-").reverse().join(".")} – ${mk.mps.to.split("-").reverse().join(".")}` : ""}` : "";
  if (mkIsMPS()) $("#mk-mps-bar").hidden = false;
  // Откуда ниши — ярлыком и словами (отзыв владельца 13.11: «непонятно, откуда»).
  srcTag("#mk-src-tag", !tb ? "" : mkIsMPS() ? "mps" : "file");
  $("#mk-intro").textContent = "Выбор ниши на ближайший сезон: где спрос растет, а продавцов немного. " + (!tb
    ? "Ниши — из выгрузки файлом («Открыть выгрузку…») или из MPStats по ключу («Из MPStats»)."
    : mkIsMPS() ? "Сейчас ниши — из MPStats по ключу, за выбранные дни; файл — «Открыть выгрузку…»." : "Сейчас ниши — из выгрузки файлом; из MPStats по ключу — «Из MPStats».");
  $("#mk-file").dataset.tipB = !tb ? "" : mkIsMPS()
    ? `${$("#mk-file").textContent} · по ключу MPStats: категория и даты уходят в MPStats, ответ — в окно; каждый показ тратит запросы тарифа`
    : `${$("#mk-file").textContent} · ${tb.path} · окно только читает файл и помнит путь; данные никуда не уходят`;
  $("#mk-cols").hidden = !tb || mkIsMPS(); // у MPStats колонок файла нет
  $("#mk-body").hidden = !tb || tb.needName;
  if (!tb) return;
  mkRenderCols();
  if (!tb.needName) { mkRenderFilters(); mkRenderTable(); mkRenderCmp(); mkRenderProbs(); }
}
// mkRenderCols: колонки файла и что в них; без колонки ниши — вопрос раскрыт.
function mkRenderCols() {
  const tb = mk.tb, known = tb.columns.filter((c) => c.field || c.month).length, months = tb.columns.filter((c) => c.month).length;
  $("#mk-cols").classList.toggle("ask", tb.needName);
  if (tb.needName) $("#mk-cols").open = true;
  $("#mk-cols-sum").textContent = tb.needName ? "Не нашел колонку с названием ниши: выберите ее ниже"
    : tb.items ? `Товары категории MPStats: ниши собраны из ${fmt(tb.items)} товаров${tb.periodDays ? ` за ${fmt(tb.periodDays)} дн., продажи и выручка пересчитаны в месяц` : ", период не узнан: продаж в месяц нет"} · рост к году одна выгрузка не знает`
    : `Колонки файла: узнано ${known} из ${tb.columns.length}${months ? ` (месяцев ${months})` : ""} · заголовки в строке ${tb.headerRow}`;
  const opts = (sel) => `<option value=""${sel ? "" : " selected"}>не нужна</option>` +
    tb.fields.map((f) => `<option value="${esc(f.id)}"${f.id === sel ? " selected" : ""}>${esc(f.label)}</option>`).join("");
  $("#mk-cols-b").innerHTML = tb.columns.map((c) => {
    const head = `<label for="mk-c${c.index}" class="${c.field || c.month ? "" : "unk"}">${esc(c.header || "колонка " + (c.index + 1))}</label>`;
    if (c.month) return head + `<span class="mon"${mkTip("Продажи за месяц", "из таких колонок строится график сезона и ищется пик")}>продажи за месяц</span>`;
    return head + `<select id="mk-c${c.index}" data-mk-col="${c.index}"${mkTip("Что в колонке", c.field ? MK_TIP[c.field] || "" : "окно не узнало колонку: выберите, что в ней, или оставьте «не нужна»")}>${opts(c.field)}</select>`;
  }).join("");
}
$("#mk-cols-b").addEventListener("change", (e) => {
  const s = e.target.closest("[data-mk-col]");
  if (!s || !mk.tb) return;
  const i = +s.dataset.mkCol;
  mkRead(mk.tb.path, [...mk.picks.filter((p) => p.index !== i), { index: i, field: s.value }]);
});
// mkShown: величины для таблицы по макету — из тех, что есть хоть у одной ниши.
function mkShown() {
  const ns = mk.tb.niches, has = (id) => ns.some((n) => n.values[id] !== undefined);
  const cols = ["sales", "growth", "sellers", "price"].filter(has);
  if (ns.some((n) => n.months.length)) cols.push("season");
  if (ns.some((n) => n.score.ok)) cols.push("score");
  return cols;
}
function mkRenderFilters() {
  const ns = mk.tb.niches, cols = mkShown();
  const mps = Object.keys(MK_MP).filter((m) => ns.some((n) => n.markets.includes(m)));
  if (mps.length < 2) mk.mp = "all";
  // Одна площадка или один порядок — выбирать не из чего: кнопка, которая
  // ничего не меняет, читается как сломанная (отзыв владельца 13.11).
  $("#mk-mp").innerHTML = mps.length > 1 ? [["all", "все", "ниши всех площадок"], ...mps.map((m) => [m, MK_MP[m], "только ниши, которые есть на " + MK_MP[m]])]
    .map(([v, n, t]) => `<button type="button" data-mk-mp="${v}" aria-pressed="${mk.mp === v}"${mkTip(n, t)}>${n}</button>`).join("") : "";
  const sorts = [["score", "по оценке", "сначала ниши с большей оценкой 0-100"], ["growth", "по росту", "сначала ниши, где спрос растет быстрее"], ["sales", "по продажам", "сначала ниши с большими продажами в штуках"]]
    .filter(([v]) => cols.includes(v));
  $("#mk-sort").innerHTML = sorts.length < 2 ? "" : `<span class="mk-seg-l">сортировка</span>` + sorts.map(([v, n, t]) => `<button type="button" data-mk-by="${v}" aria-pressed="${mk.sort === v}"${mkTip(n, t)}>${n}</button>`).join("");
  const hasSeason = ns.some((n) => n.score.peak);
  $("#mk-season-l").hidden = !hasSeason;
  if (!hasSeason) mk.season = false;
  $("#mk-season").checked = mk.season;
  $("#mk-season-t").textContent = mkSeasonLabel();
  $("#mk-season-l").dataset.tip = mkSeasonLabel();
  $("#mk-season-l").dataset.tipB = "только ниши, где пик продаж в этом месяце или в двух следующих: под них еще успеть с поставкой";
}
// mkList: номера ниш под поиском и фильтрами в порядке сортировки; ниша без величины — внизу.
function mkList() {
  const fold = (t) => t.toLowerCase().replace(/ё/g, "е");
  const q = fold(mk.q.trim()), key = mk.sort, ns = mk.tb.niches;
  const val = (n) => (key === "score" ? (n.score.ok ? n.score.score : undefined) : key === "season" ? (n.months.length ? n.score.peak : undefined) : n.values[key]);
  return ns.map((_, i) => i)
    .filter((i) => (!q || fold(ns[i].name).includes(q)) && (mk.mp === "all" || ns[i].markets.includes(mk.mp)) && (!mk.season || ns[i].score.inSeason))
    .sort((x, y) => {
      if (key === "name") return mk.dir * ns[x].name.localeCompare(ns[y].name, "ru");
      const a = val(ns[x]), b = val(ns[y]);
      if (a === undefined || b === undefined) return (a === undefined) - (b === undefined);
      return mk.dir * (a - b);
    });
}
// mkSpark рисует продажи по месяцам; точка — пик.
function mkSpark(months, w, h) {
  if (!months.length) return "";
  const vals = months.map((m) => m.value), top = Math.max(...vals) || 1, step = months.length > 1 ? (w - 4) / (months.length - 1) : 0;
  const pts = vals.map((v, i) => [2 + i * step, h - 2 - (v / top) * (h - 6)]);
  const line = pts.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const pk = pts[vals.indexOf(top)];
  return `<svg class="mk-spark" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" aria-hidden="true"><polygon class="ar" points="2,${h} ${line} ${pts[pts.length - 1][0].toFixed(1)},${h}"/><polyline class="ln" points="${line}"/><circle class="pk" cx="${pk[0].toFixed(1)}" cy="${pk[1].toFixed(1)}" r="${w > 120 ? 3 : 2}"/></svg>`;
}
const mkRange = (n) => (n.months.length ? `${n.months[0].label} — ${n.months[n.months.length - 1].label}` : "");
function mkSeasonTip(n) {
  if (!n.months.length) return mkTip("Сезона нет", "в файле нет продаж ниши по месяцам");
  const s = n.score;
  return mkTip("Продажи по месяцам", `${mkRange(n)}${s.peak ? ` · пик в ${MK_IN[s.peak]}` + (s.inSeason ? " — скоро" : " — сейчас не сезон") : " · пик не найти: нужно хотя бы полгода"}`);
}
const MK_HEAD_TIP = { ...MK_TIP, season: "продажи ниши по месяцам; точка — пик", score: "0-100: рост, выручка на продавца, свобода от топ-10 и сезон; подробнее — наведите на число в строке" };
function mkRenderTable() {
  const shown = mkShown(), ns = mk.tb.niches;
  if (mk.sort !== "name" && !shown.includes(mk.sort)) { mk.sort = shown.includes("score") ? "score" : shown.includes("sales") ? "sales" : shown[0] || "name"; mk.dir = mk.sort === "name" ? 1 : -1; }
  const list = mkList();
  if (!list.includes(mk.sel)) mk.sel = list.length ? list[0] : -1;
  const th = (id, cls) => `<th class="${cls}"${mk.sort === id ? ` aria-sort="${mk.dir > 0 ? "ascending" : "descending"}"` : ""}>` +
    `<button type="button" data-mk-sort="${id}"${mkTip(mkLabel(id), (MK_HEAD_TIP[id] || "") + " · нажмите — сортировка")}>${esc(mkLabel(id))}${mk.sort === id ? (mk.dir > 0 ? " ↑" : " ↓") : ""}</button></th>`;
  const td = (n, id) => {
    if (id === "season") return `<td class="sp"${mkSeasonTip(n)}>${mkSpark(n.months, 80, 22)}</td>`;
    if (id === "score") return `<td class="n"><span class="mk-sc ${mkLevel(n.score)}"${mkScoreTip(n.score)}>${n.score.ok ? n.score.score : "—"}</span></td>`;
    const v = n.values[id];
    return `<td class="n${id === "growth" && v > 0 ? " up" : id === "growth" && v < 0 ? " down" : ""}">${mkVal(id, v)}</td>`;
  };
  $("#mk-table").innerHTML = `<thead><tr>${th("name", "")}${shown.map((id) => th(id, id === "season" ? "" : "n")).join("")}</tr></thead><tbody>` +
    list.map((i) => `<tr tabindex="0" data-mk-n="${i}" aria-selected="${i === mk.sel}"><td>${esc(ns[i].name)}</td>${shown.map((id) => td(ns[i], id)).join("")}</tr>`).join("") + "</tbody>";
  $("#mk-none").hidden = list.length > 0;
  for (const b of document.querySelectorAll("[data-mk-by]")) b.setAttribute("aria-pressed", String(b.dataset.mkBy === mk.sort));
  mkRenderCard();
}
// mkWhy — выводы карточки из фактов домена; числа те же, что в таблице.
function mkWhy(n) {
  const s = n.score, v = n.values, out = [];
  if (v.growth !== undefined) {
    const g = mkVal("growth", v.growth);
    out.push(s.growth === "fast" ? `спрос растет быстро: ${g} к прошлому году` : s.growth === "falling" ? `спрос падает: ${g} к прошлому году` : `спрос растет умеренно: ${g}`);
  }
  if (v.top10 !== undefined) out.push(`топ-10 держат ${mkVal("top10", v.top10)} выручки — ${s.crowded ? "войти труднее" : "рынок не занят"}`);
  if (s.perSeller) out.push(`на продавца в среднем ${mkMoney(s.perSeller)} выручки в месяц`);
  if (s.peak) out.push(!s.inSeason ? `пик в ${MK_IN[s.peak]}: сейчас не сезон` : s.weeksFrom === 0 ? `пик сейчас, в ${MK_IN[s.peak]}` : `пик в ${MK_IN[s.peak]}: до него ${s.weeksFrom}-${s.weeksTo} недель`);
  const miss = s.missing.map((m) => MK_MISS[m]).filter(Boolean);
  if (!s.ok) out.push(`оценки нет: в файле нет ${miss.join(", ")}`);
  else if (miss.length) out.push(`оценка без ${miss.join(", ")}: веса остальных частей поделены заново`);
  return out;
}
function mkRenderCard() {
  const n = mk.tb.niches[mk.sel];
  if (!n) { $("#mk-card").innerHTML = ""; return; }
  const v = n.values, s = n.score, kv = [];
  const rev = v.revenue !== undefined ? v.revenue : v.sales !== undefined && v.price !== undefined ? v.sales * v.price : undefined;
  if (rev !== undefined) kv.push(["Выручка ниши", mkMoney(rev) + " в месяц", v.revenue !== undefined ? "из колонки выручки" : "продажи × средняя цена: колонки выручки в файле нет"]);
  if (v.sales !== undefined) kv.push(["Продажи", fmt(Math.round(v.sales)) + " шт в месяц", MK_TIP.sales]);
  if (v.sellers !== undefined) kv.push(["Продавцов", fmt(Math.round(v.sellers)), MK_TIP.sellers]);
  if (v.price !== undefined) kv.push(["Средняя цена", mkVal("price", v.price), MK_TIP.price]);
  if (n.markets.length) kv.push(["Маркетплейсы", n.markets.map((m) => MK_MP[m]).join(", "), MK_TIP.marketplace]);
  const inCmp = mk.cmp.includes(mk.sel);
  $("#mk-card").innerHTML = `<div class="mk-card-h"><h3>${esc(n.name)}</h3><span class="mk-sc ${mkLevel(s)}"${mkScoreTip(s)}>${s.ok ? s.score : "—"}</span></div>` +
    (n.months.length ? `<div${mkSeasonTip(n)}>${mkSpark(n.months, 300, 70)}</div><div class="cap">продажи по месяцам: ${esc(mkRange(n))}</div>` : "") +
    `<dl>${kv.map(([k, val, t]) => `<dt${mkTip(k, t)}>${k}</dt><dd>${esc(val)}</dd>`).join("")}</dl>` +
    `<ul>${mkWhy(n).map((w) => `<li>${esc(w)}</li>`).join("")}</ul>` +
    `<div class="row">${mkIsMPS() ? `<button class="btn" type="button" data-mk-in="${esc(n.name)}"${mkTip("Внутрь", "ниши внутри этой: ее подкатегории за те же дни")}>Внутрь</button>` : ""}<button class="btn primary" type="button" data-mk-go-fc="${esc(n.name)}"${mkTip("Прогноз по этой нише", "вкладка «Прогноз»: три файла по этой нише — прошлый год, сейчас и свои продажи")}>Прогноз по этой нише</button>` +
    `<button class="btn mk-main" type="button" data-mk-cmp="${mk.sel}" aria-pressed="${inCmp}"${mkTip(inCmp ? "Убрать из сравнения" : "Сравнить", "до трех ниш рядом по тем же цифрам; лучшее в строке — зеленым")}>${inCmp ? "Убрать из сравнения" : "Сравнить"}</button></div>`;
}
// mkRenderCmp — две-три ниши рядом; лучшее в строке подсвечено.
function mkRenderCmp() {
  const ns = mk.tb.niches, list = mk.cmp.filter((i) => ns[i]);
  $("#mk-cmp").hidden = !list.length;
  if (!list.length) return;
  const rows = [["score", "Оценка", (n) => (n.score.ok ? n.score.score : undefined), (x) => String(x), 1],
    ["sales", "Продажи, шт/мес", (n) => n.values.sales, (x) => fmt(Math.round(x)), 1],
    ["revenue", "Выручка, ₽/мес", (n) => n.values.revenue, mkMoney, 1],
    ["perSeller", "На продавца", (n) => n.score.perSeller || undefined, mkMoney, 1],
    ["growth", "Рост к году", (n) => n.values.growth, (x) => mkVal("growth", x), 1],
    ["sellers", "Продавцов", (n) => n.values.sellers, (x) => fmt(Math.round(x)), -1],
    ["top10", "Доля топ-10", (n) => n.values.top10, (x) => mkVal("top10", x), -1],
    ["peak", "Пик", (n) => n.score.peak || undefined, (x) => MK_MON[x], 0]]
    .filter(([, , get]) => list.some((i) => get(ns[i]) !== undefined));
  const cell = ([, , get, show, better]) => {
    const vals = list.map((i) => get(ns[i])), known = vals.filter((x) => x !== undefined);
    const best = better && known.length > 1 ? (better > 0 ? Math.max(...known) : Math.min(...known)) : undefined;
    return vals.map((x) => `<td class="${x !== undefined && x === best ? "best" : ""}">${x === undefined ? "—" : esc(show(x))}</td>`).join("");
  };
  $("#mk-cmp").innerHTML = `<div class="mk-cmp-h"><span>Сравнение ниш · ${list.length} из 3</span><button class="btn" type="button" data-mk-cmp-clear${mkTip("Очистить сравнение", "убрать все ниши из сравнения")}>Очистить</button></div>` +
    `<table><thead><tr><th></th>${list.map((i) => `<th>${esc(ns[i].name)}<button class="x" type="button" data-mk-cmp="${i}" aria-label="Убрать ${esc(ns[i].name)}">✕</button></th>`).join("")}</tr></thead>` +
    `<tbody>${rows.map((r) => `<tr><td${mkTip(r[1], r[4] > 0 ? "лучше — больше" : r[4] < 0 ? "лучше — меньше" : "месяц пика продаж")}>${r[1]}</td>${cell(r)}</tr>`).join("")}</tbody></table>`;
}
function mkRenderProbs() {
  const p = mk.tb.problems;
  $("#mk-probs").hidden = !p.length;
  $("#mk-probs-sum").textContent = `Не прочитано строк: ${p.length}`;
  $("#mk-probs-list").innerHTML = p.map((x) => `<li>строка ${x.row}: ${esc(x.reason)}</li>`).join("");
}
$("#markets-view").addEventListener("click", (e) => {
  const cmp = e.target.closest("[data-mk-cmp]");
  if (cmp) {
    hideTip();
    const i = +cmp.dataset.mkCmp;
    if (mk.cmp.includes(i)) mk.cmp = mk.cmp.filter((x) => x !== i);
    else if (mk.cmp.length >= 3) { toast("Сравнение", " · не больше трех ниш: уберите одну"); return; }
    else mk.cmp = [...mk.cmp, i];
    mkRenderCard(); mkRenderCmp();
    return;
  }
  if (e.target.closest("[data-mk-cmp-clear]")) { mk.cmp = []; mkRenderCard(); mkRenderCmp(); return; }
  const mp = e.target.closest("[data-mk-mp]");
  if (mp) { mk.mp = mp.dataset.mkMp; mkRenderFilters(); mkRenderTable(); return; }
  const by = e.target.closest("[data-mk-by]");
  if (by) { mk.sort = by.dataset.mkBy; mk.dir = -1; mkRenderTable(); return; }
  const b = e.target.closest("[data-mk-sort]");
  if (b) {
    const id = b.dataset.mkSort;
    if (mk.sort === id) mk.dir = -mk.dir; else { mk.sort = id; mk.dir = id === "name" ? 1 : -1; }
    mkRenderTable();
    const again = document.querySelector(`[data-mk-sort="${id}"]`); if (again) again.focus();
    return;
  }
  const tr = e.target.closest("[data-mk-n]");
  if (tr) { mk.sel = +tr.dataset.mkN; mkRenderTable(); }
});
$("#mk-season").addEventListener("change", (e) => { mk.season = e.target.checked; mkRenderTable(); });
$("#mk-table").addEventListener("keydown", (e) => {
  const tr = e.target.closest("[data-mk-n]");
  if (!tr) return;
  const sib = e.key === "ArrowDown" ? tr.nextElementSibling : e.key === "ArrowUp" ? tr.previousElementSibling : e.key === "Enter" ? tr : null;
  if (!sib) return;
  e.preventDefault();
  mk.sel = +sib.dataset.mkN;
  mkRenderTable();
  const now = document.querySelector(`[data-mk-n="${mk.sel}"]`); if (now) now.focus();
});
$("#mk-q").addEventListener("input", (e) => { mk.q = e.target.value; if (mk.tb && !mk.tb.needName) mkRenderTable(); });
$("#mk-q").addEventListener("keydown", (e) => {
  if (e.key === "Escape" && e.target.value) { e.preventDefault(); e.stopPropagation(); e.target.value = ""; mk.q = ""; mkRenderTable(); }
});
// --- Маркеты → Прогноз (#580 шаг 2) -----------------------------------------------
// Три файла, расчет формулой в Go (MarketsForecast), итоги, график по дням.
// Окно помнит только пути — в localStorage, удобство одного Мака.
const fc = { paths: ["", "", ""], v: null, niche: "", busy: false, cabs: [] };
const fcCabName = (id) => (fc.cabs.find((c) => c.id === id) || {}).name || "кабинет";
const FC_KEY = "dvk.markets.forecast";
const FC_SLOTS = [
  ["1 · Ниша, прошлый год", "Ниша, прошлый год", "выгрузка сервиса аналитики с продажами ниши по дням (дата — колонкой или строкой) за тот же период, что в файле 2, но год назад, и за месяцы после него — например, сен-дек 2025. Не «Товары категории» MPStats: там итог за период по товару, дней нет"],
  ["2 · Ниша, сейчас", "Ниша, сейчас", "та же выгрузка продаж ниши по дням за последние недели — например, сен-окт 2026; «Товары категории» не подойдут"],
  ["3 · Свои продажи", "Свои продажи", "из кабинета Ozon по ключу (90 дней, без отмен) или отчет площадки и своя таблица: дата и количество за те же недели"],
];
const FC_WD = ["вс", "пн", "вт", "ср", "чт", "пт", "сб"];
const fcDate = (s) => new Date(s + "T00:00:00");
const fcDay = (s) => { const d = fcDate(s); return `${d.getDate()} ${MON_GEN[d.getMonth()]}, ${FC_WD[d.getDay()]}`; };
const MON_GEN = ["января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"];
try { const p = JSON.parse(localStorage.getItem(FC_KEY) || "null"); if (Array.isArray(p) && p.length === 3) fc.paths = p.map(String); } catch (_) { /* без хранилища — пустые поля */ }
function fcSave() { try { localStorage.setItem(FC_KEY, JSON.stringify(fc.paths)); } catch (_) { /* окно просто не вспомнит файлы */ } }
// mkTab переключает «Ниша», «Прогноз» и «Магазин»; «План» живет под прогнозом.
function mkTab(id) {
  for (const t of document.querySelectorAll("[data-mk-tab]")) t.setAttribute("aria-selected", String(t.dataset.mkTab === id));
  for (const el of document.querySelectorAll('[data-mk-pane]')) el.hidden = el.dataset.mkPane !== id;
  $("#mk-mps-bar").hidden = id !== "niche" || !(mk.mpsOpen || mkIsMPS()); // панель MPStats — по своему состоянию
  if (id === "forecast") { fcRender(); if (native() && native().Connections) shCabinets().then((r) => { fc.cabs = r; fcRender(); }); } else if (id === "shop") openShop(); else mkRender(); // «Ниша» сама решает, что из нее видно
}
$("#markets-view").addEventListener("click", (e) => {
  const t = e.target.closest("[data-mk-tab]");
  if (t) { hideTip(); mkTab(t.dataset.mkTab); return; }
  const inn = e.target.closest("[data-mk-in]");
  if (inn) { hideTip(); const m = mk.mps || {}; return void mkReadMPS(inn.dataset.mkIn, m.from, m.to, m.days); }
  const go = e.target.closest("[data-mk-go-fc]");
  if (go) {
    hideTip();
    fc.niche = go.dataset.mkGoFc;
    fc.nicheFromFile = !mkIsMPS(); // путь из файла MPStats может не узнать
    if (fc.paths[0].startsWith("mpstats:")) { // источник MPStats берет новую нишу, а не считает прежнюю (ревью #605)
      const id = fc.paths[0].slice(8, fc.paths[0].indexOf("|"));
      fc.paths[0] = fc.paths[1] = `mpstats:${id}|${fc.niche}`; fc.v = null; fc.ask = false; $("#mk-model").hidden = true; fcSave();
    }
    mkTab("forecast");
    return;
  }
  // «План» строится по прогнозу: вкладка ведет к блоку под ним, а без прогноза — к его кнопке.
  if (e.target.closest("#mk-plan-tab")) {
    hideTip();
    mkTab("forecast");
    const plan = $("#mk-plan");
    if (plan.hidden) { mkErr("План строится по прогнозу: выберите три источника и нажмите «Посчитать прогноз» — блок плана появится под ним."); $("#mk-fc-go").scrollIntoView({ block: "center" }); }
    else plan.scrollIntoView({ block: "start" });
  }
});
// Ниша из MPStats (#580): обе ниши — одним источником «mpstats:<id>|<путь>»;
// путь — ниша из «Ниши» («Прогноз по этой нише») или вписанный вручную.
const fcMpsPath = (p) => p.slice(p.indexOf("|") + 1);
function fcMPStatsSlot(i, title, hint, p, info) {
  const path = fcMpsPath(p);
  const meta = info ? `дней ${fmt(info.days)} · ${info.from}–${info.to} · прочитано` : i === 0 ? "те же дни годом раньше и 4 месяца после — загрузятся при расчете, MPStats отвечает до минуты" : "90 дней до вчера — загрузятся при расчете";
  return `<div class="mk-file${info ? " ok" : ""}"><h4${mkTip(title, hint)}>${esc(title)}</h4><span class="hint">${esc(hint)}</span>` +
    srcTagHTML("mps") + `<span class="name"${mkTip("Ниша в MPStats", path || "ниша не выбрана")}>MPStats · ${esc(path || "ниша не выбрана")}</span><span class="meta">${esc(meta)}</span>` +
    (i === 0 ? `<input class="mk-q" id="fc-mps-path" list="mk-mps-cats" value="${esc(path)}" placeholder="Путь категории Ozon: Одежда/Мужская одежда/…" autocomplete="off" spellcheck="false"${mkTip("Ниша в MPStats", "путь категории, как в MPStats: начните печатать — окно подскажет. Имена ниш из выгрузки файлом у MPStats другие; надежнее — «Ниша → Из MPStats» → «Прогноз по этой нише»")}>` +
      (fc.nicheFromFile ? `<span class="meta">ниша взята из выгрузки файлом — у MPStats путь может быть другим</span>` : "") : "") +
    `<button class="btn" type="button" data-fc-pick="${i}" data-fc-title="${esc(FC_SLOTS[i][1])}"${mkTip("Выбрать файл", "вернуться к выгрузке файлом вместо MPStats")}>Файлом…</button></div>`;
}
function fcRender() {
  $("#mk-plan").hidden = !fc.v; // план — по посчитанному прогнозу
  if (!fc.v) plRender(null);
  $("#mk-fc-intro").textContent = (fc.niche ? `Ниша «${fc.niche}». ` : "") + "Прогноз Ваших продаж по дням: как продавала ниша в прошлом году, как продает сейчас и какая у Вас в ней доля.";
  $("#mk-files").innerHTML = FC_SLOTS.map(([title, dlg, hint], i) => {
    const p = fc.paths[i], info = fc.v && fc.v.files[i], oz = p.startsWith("ozon:"), mps = p.startsWith("mpstats:");
    if (mps) return fcMPStatsSlot(i, title, hint, p, info);
    const meta = info ? `${info.products ? `товаров ${fmt(info.products)} · ` : ""}дней ${fmt(info.days)} · ${info.from}–${info.to}${info.format === "ozon" ? " · заказы с отменами" : ""}${info.undated ? ` · без даты ${fmt(info.undated)}, не учтены` : ""} · прочитан` : p ? "выбран, прочитается при расчете" : "файл не выбран";
    return `<div class="mk-file${info ? " ok" : ""}"><h4${mkTip(title, hint)}>${esc(title)}</h4><span class="hint">${esc(hint)}</span>` +
      (p ? srcTagHTML(oz ? "ozon" : "file") : "") +
      `<span class="name"${p && !oz ? mkTip("Файл", p) : ""}>${esc(oz ? "Ozon · " + fcCabName(p.slice(5)) : p ? p.split("/").pop() : "—")}</span><span class="meta">${esc(oz && !info ? "заказы по дням с отменами, загрузятся при расчете" : meta)}</span>` +
      (oz && fc.cabs.length > 1 ? `<select class="mk-sel" id="fc-cab" aria-label="Кабинет Ozon">${fc.cabs.map((c) => `<option value="${esc(c.id)}"${"ozon:" + c.id === p ? " selected" : ""}>${esc(c.name)}</option>`).join("")}</select>` : "") +
      `<button class="btn" type="button" data-fc-pick="${i}" data-fc-title="${esc(dlg)}"${mkTip("Выбрать файл", "xlsx или csv: дни колонками (товары строками) или колонки «Дата» и «Количество»")}>${p && !oz ? "Другой файл…" : "Выбрать файл…"}</button>` +
      (i === 2 ? `<button class="btn" type="button" id="fc-ozon"${mkTip("Из кабинета Ozon", "свои продажи по дням за 90 дней до вчера, без отмен; кабинеты — в Настройках → Подключения")}>Из кабинета Ozon</button>` :
        `<button class="btn" type="button" data-fc-mps${mkTip("Из MPStats", "продажи ниши по дням по ключу MPStats — сразу для прошлого года и сейчас, без выгрузки; ключ — в Настройках → Подключения")}>Из MPStats</button>`) + `</div>`;
  }).join("");
  const v = fc.v;
  $("#mk-kpis").hidden = $("#mk-chart").hidden = $("#mk-fc-how").hidden = !v;
  if (v) { fcKpis(v); fcChart(v); }
}
$("#mk-files").addEventListener("change", (e) => {
  if (e.target.id === "fc-mps-path") {
    if (fc.busy) { mkErr("Идет расчет — ниша сменится после него."); return fcRender(); } // поле вернется к считаемой нише
    const id = fc.paths[0].slice(8, fc.paths[0].indexOf("|"));
    fc.nicheFromFile = false;
    fc.paths[0] = fc.paths[1] = `mpstats:${id}|${e.target.value.trim()}`;
    fc.v = null; fc.ask = false; $("#mk-model").hidden = true; fcSave(); return fcRender(); // сравнение с моделью — по прежней нише (ревью #605)
  }
  if (e.target.id !== "fc-cab" || fc.busy) return;
  fc.paths[2] = "ozon:" + e.target.value; fc.v = null; fc.ask = false; $("#mk-model").hidden = true; fcSave(); fcRender();
});
$("#mk-files").addEventListener("click", async (e) => {
  if (fc.busy && e.target.closest("#fc-ozon, [data-fc-pick], [data-fc-mps]")) return mkErr("Идет расчет — источник сменится после него."); // ответ по старым входам лег бы рядом с новыми (ревью #592)
  if (e.target.closest("[data-fc-mps]")) {
    hideTip();
    mkLoadCats(); // подсказки путей MPStats для поля ниши
    if (!native() || !native().Connections) return mkErr("Прогноз работает в окне приложения.");
    let rows = [];
    try { rows = ((await native().Connections()) || []).filter((c) => c.kind === "mpstats" && c.enabled); } catch (_) { /* нет подключений — откроем форму */ }
    if (!rows.length) {
      await openSettings("conn");
      $("#conn-mpstats").hidden = false; $("#conn-add-mpstats").setAttribute("aria-expanded", "true");
      return focusSoon("#conn-m-key");
    }
    const src = `mpstats:${rows[0].id}|${fc.niche || ""}`;
    fc.paths[0] = fc.paths[1] = src; fc.v = null; fc.ask = false; $("#mk-model").hidden = true; fcSave(); fcRender();
    if (!fc.niche) focusSoon("#fc-mps-path");
    return;
  }
  if (e.target.closest("#fc-ozon")) {
    hideTip();
    if (!native() || !native().Connections) return mkErr("Прогноз работает в окне приложения.");
    const rows = await shCabinets();
    fc.cabs = rows;
    if (!rows.length) { await openSettings("conn"); return showOzonForm(); }
    const cur = fc.paths[2].startsWith("ozon:") && rows.some((c) => "ozon:" + c.id === fc.paths[2]) ? fc.paths[2] : "ozon:" + rows[0].id;
    fc.paths[2] = cur; fc.v = null; fc.ask = false; $("#mk-model").hidden = true; fcSave(); fcRender();
    return;
  }
  const b = e.target.closest("[data-fc-pick]"); if (!b) return;
  hideTip();
  if (!native() || !native().MarketsPickFileTitled) return mkErr("Прогноз работает в окне приложения.");
  let p = "";
  try { p = await native().MarketsPickFileTitled(b.dataset.fcTitle); } catch (err) { return mkErr("Файл не выбран: " + errText(err)); }
  if (!p) return;
  const i = +b.dataset.fcPick;
  if (i < 2 && fc.paths[1 - i].startsWith("mpstats:")) fc.paths[1 - i] = ""; // ниши — обе файлами или обе из MPStats
  fc.paths[i] = p; fc.v = null; fc.ask = false; $("#mk-model").hidden = true; fcSave(); fcRender();
});
$("#mk-fc-go").addEventListener("click", async () => {
  hideTip();
  if (fc.busy) return;
  if (!native() || !native().MarketsForecast) return mkErr("Прогноз работает в окне приложения.");
  const miss = FC_SLOTS.filter((_, i) => !fc.paths[i]).map((s) => s[1]);
  if (miss.length) return mkErr("Выберите файлы: " + miss.join(", ") + ".");
  mkErr(""); fc.busy = true; $("#mk-fc-go").textContent = "Считаю…";
  plRender(null); // новый прогноз — старый план не остается под ним (ревью #599)
  try { fc.v = await native().MarketsForecast(...fc.paths); } catch (err) { fc.v = null; mkErr("Прогноз не посчитан: " + errText(err)); }
  fc.busy = false; $("#mk-fc-go").textContent = "Посчитать прогноз";
  fcRender();
});
// «План продаж» (#580): по тем же трем источникам, что прогноз, и вводу человека.
const PL_KEY = "kb.markets.plan", PL_IN = ["stock", "cost", "price", "comm", "log", "lead"];
try { const v = JSON.parse(localStorage.getItem(PL_KEY) || "null"); if (v) for (const k of PL_IN) if (v[k] !== undefined) $("#pl-" + k).value = v[k]; } catch (_) { /* без хранилища — значения по умолчанию */ }
// Число как пишет человек: «1 500», «1.500» (точка — тысячи), «1500,5», «−5» (ревью #599).
function plNum(k) {
  let t = String($("#pl-" + k).value).replace(/[\s\u00a0₽%]/g, "").replace(/\u2212/g, "-");
  if (/^-?\d{1,3}(\.\d{3})+$/.test(t)) t = t.replace(/\./g, "");
  return t.split(",").length > 2 ? NaN : Number(t.replace(",", "."));
}
function plInput() {
  try { localStorage.setItem(PL_KEY, JSON.stringify(Object.fromEntries(PL_IN.map((k) => [k, $("#pl-" + k).value])))); } catch (_) { /* окно просто не вспомнит ввод */ }
  const bad = PL_IN.filter((k) => !Number.isFinite(plNum(k)) || String($("#pl-" + k).value).trim() === "");
  if (bad.length) return null;
  return { stock: plNum("stock"), cost: plNum("cost"), price: plNum("price"), commission: plNum("comm"), logistics: plNum("log"), leadDays: Math.round(plNum("lead")) };
}
function plRender(v) {
  const has = !!v;
  for (const id of ["#pl-kpis", "#pl-table", "#pl-how"]) $(id).hidden = !has;
  $("#pl-xlsx").disabled = !has;
  if (!has) return;
  const kpi = (k, val, s, cls, tip) => `<div class="mk-kpi"${mkTip(k, tip)}><div class="k">${k}</div><div class="v${cls ? " " + cls : ""}">${val}</div><div class="s">${s}</div></div>`;
  $("#pl-kpis").innerHTML = kpi("Продать", fmt(v.sell) + " шт", `за ${fmt(v.weeks.length)} недель прогноза`, "", "сколько продать за период, не больше остатка") +
    kpi("Прибыль по плану", fmt(v.profit) + " ₽", "до рекламы и налогов", v.profit < 0 ? "bad" : "", "сумма прибыли по дням плана") +
    (v.supplyQty ? kpi("Довезти", fmt(v.supplyQty) + " шт", v.supplyLate ? `срок прошел (${v.supplyBy}) — везти сейчас` : "к " + v.supplyBy, "bad", "недостающее к спросу; дата — когда остаток кончится, минус срок поставки")
      : kpi("Довезти", "не нужно", "остатка хватает на весь прогноз", "", "")) +
    kpi("Пик", v.peak, v.shortForPeak ? "остатка не хватит на пик" : "остатка на пик хватает", v.shortForPeak ? "bad" : "", "день с наибольшим прогнозом");
  const marks = (w) => [w.peak && "пик", w.dip && "провал", w.short && "не хватит остатка"].filter(Boolean).join(", ");
  $("#pl-table").innerHTML = `<thead><tr><th>Неделя с</th><th>Спрос</th><th>Продать</th><th>Остаток</th><th>Цена</th><th>Прибыль</th><th>Пометки</th></tr></thead><tbody>` +
    v.weeks.map((w) => `<tr class="${w.peak ? "peak" : ""}"><td>${esc(w.from)}${w.days < 7 ? ` <span class="muted">(${w.days} дн.)</span>` : ""}</td><td>${fmt(w.demand)}</td><td>${fmt(w.sell)}</td>` +
      `<td${w.short ? ' class="bad"' : ""}>${fmt(w.stockEnd)}</td><td>${fmt(w.price)} ₽</td><td${w.profit < 0 ? ' class="bad"' : ""}>${fmt(w.profit)} ₽</td><td class="m">${esc(marks(w))}</td></tr>`).join("") + "</tbody>";
}
$("#pl-go").addEventListener("click", async () => {
  hideTip();
  if (!native() || !native().MarketsPlan) return mkErr("План работает в окне приложения.");
  const inp = plInput();
  if (!inp) return mkErr("Заполните все поля плана числами.");
  mkErr(""); $("#pl-go").textContent = "Строю…";
  try { plRender(await native().MarketsPlan(...fc.paths, inp)); } catch (err) { plRender(null); mkErr("План не построен: " + errText(err)); }
  // ввод с этого момента — тот, по которому построен план
  $("#pl-go").textContent = "Построить план";
});
// Правка ввода после расчета — Excel гаснет до нового «Построить план»: файл не разойдется с таблицей.
$("#mk-plan").addEventListener("input", (e) => { if (e.target.matches("input")) $("#pl-xlsx").disabled = true; });
$("#pl-xlsx").addEventListener("click", async () => {
  hideTip();
  if (!native() || !native().MarketsPlanExcel) return;
  try {
    const path = await native().MarketsPlanExcel(); // ровно план на экране (ревью #599)
    if (path) toast("План сохранен", " · " + path.split("/").pop());
  } catch (err) { mkErr("Excel не сохранен: " + errText(err)); }
});
// «Посчитать моделью»: первое нажатие говорит, что уйдет провайдеру; второе — отправляет.
fc.ask = false;
$("#mk-fc-model").addEventListener("click", async () => {
  hideTip();
  if (fc.busy) return;
  if (!native() || !native().MarketsModelForecast) return mkErr("Расчет моделью работает в окне приложения.");
  const miss = FC_SLOTS.filter((_, i) => !fc.paths[i]).map((s) => s[1]);
  if (miss.length) return mkErr("Выберите файлы: " + miss.join(", ") + ".");
  const box = $("#mk-model");
  if (!fc.ask) {
    fc.ask = true;
    box.hidden = false;
    box.innerHTML = `<h4>Отправить модели?</h4><p class="warn">Модели чата уйдут только суммы продаж по неделям из трех файлов и их периоды — без названий товаров, продавцов и файлов. Это вызов модели за деньги, он появится в «Статистике» как «Прогноз продаж». Нажмите «Посчитать моделью» еще раз, чтобы отправить.</p>`;
    $("#mk-fc-model").textContent = "Отправить модели";
    return;
  }
  fc.ask = false; fc.busy = true; mkErr("");
  $("#mk-fc-model").textContent = "Модель считает…";
  box.innerHTML = '<p class="warn">Модель считает…</p>';
  try { fcModel(await native().MarketsModelForecast(...fc.paths, true)); }
  catch (err) { box.innerHTML = `<h4>Модель не посчитала</h4><p class="warn">${esc(errText(err))}</p>`; }
  fc.busy = false; $("#mk-fc-model").textContent = "Посчитать моделью";
});
function fcModel(m) {
  const pct = (x) => (x > 0 ? "+" : x < 0 ? "−" : "") + Math.abs(Math.round(x * 100)) + " %";
  $("#mk-model").innerHTML = `<h4${mkTip("Расчет модели", "модель " + m.model + " посчитала по тем же суммам по неделям; формула выше проверена тестами, модель — нет")}>Расчет модели ${esc(m.model)} — перепроверьте</h4>` +
    `<p class="warn">Модель считает на глаз: ее числа нельзя проверить тестом, а формула выше проверена. Если расхождение больше 15 %, верьте формуле и спросите модель, почему она думает иначе.</p>` +
    `<table><thead><tr><th>Месяц</th><th>модель</th><th>формула</th><th>расхождение</th></tr></thead><tbody>` +
    m.months.map((x) => `<tr><td>${esc(x.label)}</td><td>${fmt(x.model)} шт</td><td>${x.formula ? fmt(x.formula) + " шт" : "—"}</td><td class="${Math.abs(x.diff) > 0.15 ? "far" : ""}">${x.formula ? pct(x.diff) : "нет в формуле"}</td></tr>`).join("") +
    `</tbody></table><p>Пик по модели: <b>${esc(fcDay(m.peak))}</b>${fc.v ? ` · по формуле: ${esc(fcDay(fc.v.peak.day))}` : ""}</p>` +
    (m.note ? `<p class="warn">Модель: ${esc(m.note)}</p>` : "");
}
function fcKpis(v) {
  const pct = (x) => (x * 100).toFixed(1).replace(".", ",") + " %";
  const months = v.months.map((m) => `<div class="mk-kpi"${mkTip(m.label + ", прогноз", "сумма прогноза по дням месяца; диапазон — из разброса прошлого года")}><div class="k">${esc(m.label[0].toUpperCase() + m.label.slice(1))}, прогноз</div>` +
    `<div class="v">${fmt(m.value)} шт</div><div class="s">${fmt(m.lo)}–${fmt(m.hi)}</div></div>`).join("");
  const g = v.growth, gs = (g > 0 ? "+" : g < 0 ? "−" : "") + Math.abs(Math.round(g * 100)) + " %";
  $("#mk-kpis").innerHTML = months +
    `<div class="mk-kpi"${mkTip("Пик продаж", "день с наибольшим прогнозом; рядом стоит проверить акции площадки")}><div class="k">Пик продаж</div><div class="v hl">${esc(fcDay(v.peak.day))}</div><div class="s">≈ ${fmt(Math.round(v.peak.value))} шт за день</div></div>` +
    `<div class="mk-kpi"${mkTip("Ваша доля ниши", "Ваши продажи в штуках к продажам ниши за те же дни из файлов 2 и 3")}><div class="k">Ваша доля ниши</div><div class="v">${pct(v.share)}</div><div class="s">по штукам, ${esc(v.files[2].from)}–${esc(v.files[2].to)}</div></div>` +
    `<div class="mk-kpi"${mkTip("Рост ниши", "продажи ниши сейчас к тем же дням прошлого года")}><div class="k">Рост ниши к году</div><div class="v">${gs}</div><div class="s">по файлам 1 и 2</div></div>`;
}
function fcChart(v) {
  const W = 920, H = 240, padL = 40, padB = 22, padT = 18, n = v.days.length;
  const top = Math.max(...v.days.map((d) => d.hi), ...v.lastYear.map((d) => d.value), 1);
  const x = (i) => padL + (n > 1 ? (i * (W - padL - 8)) / (n - 1) : 0), y = (val) => padT + (H - padT - padB) * (1 - val / top);
  const step = n > 1 ? (W - padL - 8) / (n - 1) : 0;
  const pts = (arr, k) => arr.map((d, i) => `${x(i).toFixed(1)},${y(d[k]).toFixed(1)}`).join(" ");
  const band = v.days.map((d, i) => `${x(i).toFixed(1)},${y(d.hi).toFixed(1)}`).join(" ") + " " + v.days.map((d, i) => `${x(i).toFixed(1)},${y(d.lo).toFixed(1)}`).reverse().join(" ");
  const we = v.days.map((d, i) => { const wd = fcDate(d.day).getDay(); return wd === 0 || wd === 6 ? `<rect class="we" x="${(x(i) - step / 2).toFixed(1)}" y="${padT}" width="${step.toFixed(1)}" height="${H - padT - padB}"/>` : ""; }).join("");
  const pi = v.days.findIndex((d) => d.day === v.peak.day);
  const ticks = v.days.map((d, i) => { const dt = fcDate(d.day); return dt.getDate() === 1 || dt.getDate() % 7 === 1 ? `<text class="ax" x="${x(i).toFixed(1)}" y="${H - 6}" text-anchor="middle">${dt.getDate()} ${MON_GEN[dt.getMonth()].slice(0, 3)}</text>` : ""; }).join("");
  const yt = [0, 0.5, 1].map((f) => `<text class="ax" x="${padL - 6}" y="${(y(top * f) + 3).toFixed(1)}" text-anchor="end">${fmt(Math.round(top * f))}</text>`).join("");
  const hits = v.days.map((d, i) => `<rect class="hit" x="${(x(i) - step / 2).toFixed(1)}" y="${padT}" width="${Math.max(step, 2).toFixed(1)}" height="${H - padT - padB}"` +
    `${mkTip(fcDay(d.day), `прогноз ${fmt(Math.round(d.value))} шт · диапазон ${fmt(Math.round(d.lo))}–${fmt(Math.round(d.hi))} · прошлый год в Вашем масштабе ${fmt(Math.round(v.lastYear[i].value))}`)}/>`).join("");
  $("#mk-chart").innerHTML = `<h4>Продажи по дням, ${esc(v.months.map((m) => m.label).join(" — "))}</h4>` +
    `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="Прогноз продаж по дням">${we}<polygon class="band" points="${band}"/>` +
    `<polyline class="ly" points="${pts(v.lastYear, "value")}"/><polyline class="fc" points="${pts(v.days, "value")}"/>` +
    (pi >= 0 ? `<circle class="pk" cx="${x(pi).toFixed(1)}" cy="${y(v.days[pi].value).toFixed(1)}" r="4"/><text class="pkl" x="${x(pi).toFixed(1)}" y="${(y(v.days[pi].value) - 9).toFixed(1)}" text-anchor="middle">пик</text>` : "") +
    `${ticks}${yt}${hits}</svg>` +
    `<div class="mk-legend"><span><i class="fc"></i>прогноз, штук</span><span><i class="band"></i>диапазон</span><span><i class="ly"></i>прошлый год, кривая ниши в Вашем масштабе</span><span>серым — выходные</span></div>`;
}
// --- Маркеты → Магазин (#580) ----------------------------------------------------
// Отчет Ozon «По товарам» читает Go (MarketsShop): доли, проблемы и счет по ним
// считает домен; окно показывает и объясняет. Помнит только путь — в localStorage.
const sh = { v: null, q: "", issue: "", sort: "ordered", dir: -1, sel: 0, first: true, only: null, adv: "" };
const SH_KEY = "dvk.markets.shop";
const SH_ISSUE = {
  cancels: ["отменяют больше половины", "отменили больше половины заказов: чаще всего товара нет на складе к сроку или долгая доставка"],
  stock: ["кончается остаток", "Ozon советует срочно поставить, товара не было на складе неделю и больше из последних четырех или запаса меньше чем на две недели продаж"],
  returns: ["много возвратов", "вернули больше 10 % заказов: смотрите размеры, фото и описание"],
  ads: ["дорогая реклама", "ДРР больше 15 %: реклама съедает заметную часть выручки"],
  rating: ["рейтинг ниже 4,5", "рейтинг ниже 4,5 при отзывах: читайте отзывы, что не нравится"],
};
// Колонки таблицы: [ключ, подпись, наше пояснение, поле подсказки Ozon].
const SH_COLS = [
  ["ordered", "Заказано", "заказано штук за период, включая отмены и возвраты", "ordered"],
  ["buyout", "Выкуп", "выкуплено к заказанному: сколько дошло до покупателя и осталось у него", "boughtOut"],
  ["cancel", "Отмены", "отменено к заказанному", "cancelled"],
  ["refuse", "Отказ", "отказались при получении — часть отмен; Ozon пишет «товар не подошел»: чаще всего размер или посадка", "refused"],
  ["return", "Возвраты", "возвращено к заказанному", "returned"],
  ["stock", "Остаток", "штук на складах на конец периода", "stock"],
  ["daysOut", "Без остатка", "дней без товара на складах из последних 28", "daysOut"],
  ["avgPrice", "Цена", "средняя цена реализации, ₽", "avgPrice"],
  ["drr", "ДРР", "доля расходов на рекламу в выручке", "drr"],
  ["rating", "Рейтинг", "оценка товара на конец периода", "rating"],
  ["onAccount", "На счет", "начислено за товар за период: продажи минус вознаграждение, доставка и услуги Ozon; до рекламы и себестоимости", "onAccount"],
  ["profit", "Прибыль", "на счет минус себестоимость проданного; до рекламы и налогов; «—» — себестоимость не задана", "profit"],
];
const shPct = (x) => Math.round(x * 100) + " %";
const shOzon = () => sh.v && sh.v.source === "ozon";
const SH_REFUSE = 0.4; // как adviceRefusals в домене: «40 % и больше»
// shCols — колонки, что есть у источника: у кабинета по API нет возвратов, ДРР, рейтинга и дней без остатка, у отчета — отказов.
// Деньги товара (на счет, прибыль) — только у кабинета: их дают начисления.
const shCols = () => SH_COLS.filter(([k]) => (shOzon() ? !["return", "daysOut", "drr", "rating"].includes(k) : !["refuse", "onAccount", "profit"].includes(k)));
const shRub = (x) => fmt(Math.round(x) || 0) + " ₽";
function shVal(k, p) {
  switch (k) {
    case "buyout": case "cancel": case "refuse": case "return": case "drr": return p.ordered || k === "drr" ? shPct(p[k]) : "—";
    case "daysOut": return p.daysOutOf ? `${p.daysOut} из ${p.daysOutOf}` : "—";
    case "avgPrice": return p.avgPrice ? fmt(Math.round(p.avgPrice)) + " ₽" : "—";
    case "rating": return (p.reviews || p.rating) && Number.isFinite(p.rating) ? String(Math.round(p.rating * 100) / 100).replace(".", ",") : "—"; // отчет отдает 4,9340909…
    case "onAccount": return p.money && (p.money.units || p.money.total) ? shRub(p.onAccount) : "—";
    case "profit": return p.profit !== null && p.profit !== undefined ? shRub(p.profit) : "—";
    default: return fmt(Math.round(p[k] || 0));
  }
}
const shHint = (k) => { const c = SH_COLS.find((x) => x[0] === k); const oz = c && sh.v && sh.v.hints[c[3]]; return (c ? c[2] : "") + (oz ? " · Ozon: " + oz : ""); };
// Источник помнится строкой: путь к отчету или «ozon:<id подключения>».
function shSave() { try { localStorage.setItem(SH_KEY, (shOzon() ? "ozon:" : "") + sh.v.path); } catch (_) { /* окно просто не вспомнит отчет */ } }
async function shRead(src) {
  if (sh.loading) return; // вторая загрузка поверх первой перетерла бы выбор (ревью #590)
  sh.loading = true;
  mkErr("");
  const oz = src.startsWith("ozon:"), b = $("#sh-ozon");
  $("#sh-open").disabled = b.disabled = true;
  const cab = oz && [...$("#sh-cab").options].find((o) => o.value === src.slice(5));
  const load = ++shLoad.seq;
  const stop = shProgress(oz ? `Загружаю кабинет${cab ? " «" + cab.textContent + "»" : ""} из Ozon` : "Читаю отчет", oz, load);
  let done = "";
  try {
    sh.v = oz ? await native().MarketsOzon(src.slice(5), load) : await native().MarketsShop(src);
    Object.assign(sh, { sel: 0, only: null, adv: "" }, sh.sort ? {} : { sort: "ordered", dir: -1 });
    shSave();
    if (oz && [...$("#sh-cab").options].some((o) => o.value === sh.v.path)) $("#sh-cab").value = sh.v.path;
    done = `${oz ? "Загружено из Ozon" : "Отчет прочитан"}: товаров ${fmt(sh.v.products.length)}`;
  } catch (e) { mkErr((oz ? "Не загрузил кабинет: " : "Не прочитал отчет: ") + errText(e)); }
  finally { sh.loading = false; $("#sh-open").disabled = b.disabled = false; stop(done); }
  shRender();
}
// Этапы «Загрузить из Ozon» в порядке полосы; события «markets:stage» шлет
// загрузчик кабинета (markets_ozon.go). Начисления и возвраты читаются
// параллельно с отправлениями, поэтому непройденные этапы все — «идет».
const SH_STAGES = [["posts", "отправления за 90 дней"], ["stock", "остатки"], ["accruals", "начисления"], ["returns", "возвраты"]];
// shLoad: номер последней загрузки кабинета и ее пройденные этапы; событие
// прежней загрузки (упала, а запоздавший список дочитался) не засчитывается.
const shLoad = { seq: 0, cur: 0, done: null };
if (window.runtime) window.runtime.EventsOn("markets:stage", (ev) => { if (shLoad.done && ev.load === shLoad.cur) { shLoad.done.add(ev.stage); shStagesRender(); } });
function shStagesRender() {
  const d = shLoad.done;
  $("#sh-progress-fill").style.width = `${Math.max(4, (d.size / SH_STAGES.length) * 100)}%`;
  $("#sh-stages").innerHTML = SH_STAGES.map(([k, l]) => `<li class="${d.has(k) ? "ok" : ""}">${d.has(k) ? "✓" : "…"} ${l}</li>`).join("");
}
// shProgress показывает полосу, пока идет загрузка: у кабинета (staged) она
// заполняется по этапам, у файла — бежит. stop(итог) убирает полосу и
// оставляет итог со временем, пустой итог — прячет блок (ошибку называет mkErr).
function shProgress(what, staged, load) {
  const box = $("#sh-progress"), bar = $("#sh-progress-bar"), st = $("#sh-stages"), t = $("#sh-progress-t"), t0 = Date.now();
  const tick = () => { t.textContent = `${what} · ${Math.round((Date.now() - t0) / 1000)} с`; };
  shLoad.done = staged ? new Set() : null;
  shLoad.cur = load;
  box.classList.remove("done");
  box.classList.toggle("indet", !staged);
  box.hidden = bar.hidden = false;
  st.hidden = !staged;
  if (staged) shStagesRender();
  tick();
  const timer = setInterval(tick, 1000);
  return (done) => {
    clearInterval(timer);
    shLoad.done = null;
    bar.hidden = st.hidden = true;
    box.classList.add("done");
    box.hidden = !done;
    t.textContent = done ? `✓ ${done} · ${new Date().toLocaleTimeString("ru", { hour: "2-digit", minute: "2-digit" })}` : "";
  };
}
// shCabinets — включенные кабинеты Ozon; выбор виден, когда их больше одного.
async function shCabinets() {
  let rows = [];
  try { rows = ((await native().Connections()) || []).filter((c) => c.kind === "ozon" && c.enabled); } catch (_) { /* нет подключений — кнопка ведет их добавить */ }
  const sel = $("#sh-cab"), cur = shOzon() ? sh.v.path : sel.value;
  sel.innerHTML = rows.map((c) => `<option value="${esc(c.id)}">${esc(c.name)}</option>`).join("");
  if (rows.some((c) => c.id === cur)) sel.value = cur;
  sel.hidden = rows.length < 2;
  return rows;
}
async function openShop() {
  shRender();
  if (!sh.first) return;
  sh.first = false;
  let p = ""; try { p = localStorage.getItem(SH_KEY) || ""; } catch (_) { /* нет хранилища */ }
  if (native() && native().Connections) await shCabinets();
  // Кабинет — последней загрузкой с этого Мака, без похода в Ozon (отзыв
  // владельца 13.11); не загружался ни разу — грузим, как раньше.
  if (p.startsWith("ozon:") && native() && native().MarketsOzonLast) {
    try {
      sh.v = await native().MarketsOzonLast(p.slice(5));
      Object.assign(sh, { sel: 0, only: null, adv: "" }, sh.sort ? {} : { sort: "ordered", dir: -1 });
      if ([...$("#sh-cab").options].some((o) => o.value === sh.v.path)) $("#sh-cab").value = sh.v.path;
      shLastLine();
      return shRender();
    } catch (_) { /* последней загрузки нет — загрузим */ }
  }
  if (p && native() && native().MarketsShop) await shRead(p);
}
// shLastLine — строка «последняя загрузка …» над плитками: что на экране и как обновить.
function shLastLine() {
  const box = $("#sh-progress");
  if (!sh.v || !sh.v.loadedAt) return;
  box.hidden = false; box.classList.add("done"); box.classList.remove("indet");
  $("#sh-progress-bar").hidden = $("#sh-stages").hidden = true;
  $("#sh-progress-t").textContent = `Последняя загрузка из Ozon: ${sh.v.loadedAt} · обновить — «Загрузить из Ozon»`;
}
$("#sh-ozon").addEventListener("click", async () => {
  hideTip();
  if (sh.loading) return;
  if (!native() || !native().MarketsOzon) return mkErr("Магазин работает в окне приложения.");
  const rows = await shCabinets();
  if (!rows.length) { await openSettings("conn"); return showOzonForm(); }
  sh.issue = ""; sh.q = ""; $("#sh-q").value = "";
  await shRead("ozon:" + (rows.length > 1 ? $("#sh-cab").value : rows[0].id));
});
$("#sh-open").addEventListener("click", async () => {
  hideTip();
  if (!native() || !native().MarketsPickFileTitled) return mkErr("Магазин работает в окне приложения.");
  let p = "";
  try { p = await native().MarketsPickFileTitled("Отчет Ozon «По товарам»"); } catch (e) { return mkErr("Файл не выбран: " + errText(e)); }
  if (p) { sh.issue = ""; sh.q = ""; $("#sh-q").value = ""; await shRead(p); }
});
// srcTag — ярлык источника данных: файл, MPStats или кабинет Ozon; "" — прячет.
const SRC_TAGS = { file: ["Файл", "данные из файла с этого Мака: окно только читает его"], mps: ["MPStats", "данные из MPStats по ключу: запросы уходят в MPStats и тратят тариф"], ozon: ["Кабинет Ozon", "данные из кабинета по ключу Seller API, только чтение"] };
const srcTagHTML = (kind) => `<span class="src-tag" data-kind="${kind}"${mkTip("Источник: " + SRC_TAGS[kind][0], SRC_TAGS[kind][1])}>${SRC_TAGS[kind][0]}</span>`;
function srcTag(sel, kind) {
  const el = $(sel), t = SRC_TAGS[kind];
  el.hidden = !t;
  if (!t) return;
  el.textContent = t[0]; el.dataset.kind = kind; el.dataset.tip = "Источник: " + t[0]; el.dataset.tipB = t[1];
}
// shPart — показана часть загруженных дней.
const shPart = () => !!(sh.v && sh.v.windowFrom && (sh.v.from !== sh.v.windowFrom || sh.v.to !== sh.v.windowTo));
// shSourceLine — откуда данные одной строкой: «Кабинет FURIA (Ozon) · 15.08.2026 – 12.11.2026 · 547 товаров».
function shSourceLine(v) {
  const what = shOzon() ? `Кабинет ${(v.file || "").replace(/^Ozon · /, "")} (Ozon)` : `Отчет ${v.file || ""}`;
  const n = (v.products || []).length;
  return [what, v.period, `${fmt(n)} ${plural(n, "товар", "товара", "товаров")}`].filter((x) => x && !/undefined/.test(x)).join(" · ");
}
// Период «Магазина» (#580): кабинет загружается за 90 дней, дни режутся из
// загруженного без запроса к Ozon. Быстрые кнопки — от последнего дня назад.
const SH_PRESETS = [["all", "все 90 дн."], [30, "30 дн."], [7, "7 дн."], [1, "вчера"]];
// shYesterday: «вчера» — только если загружено сегодня; иначе последний день — датой (ревью #604).
const shYesterday = (v) => { const d = new Date(); d.setDate(d.getDate() - 1); return isoDay(d) === v.windowTo; };
const isoDay = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
function shPresetRange(p, v) {
  if (p === "all") return [v.windowFrom, v.windowTo];
  const [y, m, d] = v.windowTo.split("-").map(Number), from = new Date(y, m - 1, d - (p - 1));
  return [isoDay(from) < v.windowFrom ? v.windowFrom : isoDay(from), v.windowTo];
}
function shPeriodRender() {
  const v = sh.v, on = !!(v && shOzon() && v.windowFrom);
  $("#sh-period").hidden = !on;
  if (!on) return;
  $("#sh-presets").innerHTML = SH_PRESETS.map(([p, l]) => {
    const [a, b] = shPresetRange(p, v), cur = a === v.from && b === v.to;
    const label = p === 1 && !shYesterday(v) ? v.windowTo.split("-").reverse().slice(0, 2).join(".") : l;
    return `<button type="button" data-sh-preset="${p}" aria-pressed="${cur}"${mkTip(label, p === "all" ? "все загруженные дни кабинета" : p === 1 ? "последний загруженный день; загрузите кабинет снова, чтобы увидеть свежие" : `последние ${p} дней загруженного`)}>${label}</button>`;
  }).join("");
  // Поле, в которое сейчас вводят, не перезаписываем: набор даты с клавиатуры не сбивается (ревью #604).
  for (const [id, val] of [["#sh-d1", v.from], ["#sh-d2", v.to]]) { const i = $(id); i.min = v.windowFrom; i.max = v.windowTo; if (document.activeElement !== i) i.value = val; }
}
// shPeriod: ответ устаревшего запроса (щелкнули второй период, сменили кабинет)
// отбрасывается по номеру; выбранный товар держится по SKU, а не по строке (ревью #604).
let shPeriodSeq = 0;
async function shPeriod(from, to) {
  if (sh.loading || !sh.v || !native().MarketsOzonPeriod) return;
  const seq = ++shPeriodSeq, path = sh.v.path, sku = (sh.v.products[sh.sel] || {}).sku;
  mkErr("");
  try {
    const v = await native().MarketsOzonPeriod(path, from, to);
    if (seq !== shPeriodSeq || !sh.v || sh.v.path !== path) return;
    sh.v = v;
    sh.only = null; sh.adv = "";
    sh.sel = Math.max(0, v.products.findIndex((p) => p.sku === sku));
  } catch (e) { if (seq === shPeriodSeq) mkErr("Период не применен: " + errText(e)); }
  shRender();
}
$("#sh-presets").addEventListener("click", (e) => {
  const b = e.target.closest("[data-sh-preset]");
  if (!b || !sh.v) return;
  hideTip();
  const p = b.dataset.shPreset === "all" ? "all" : +b.dataset.shPreset;
  shPeriod(...shPresetRange(p, sh.v));
});
// Дата применяется, когда она целиком внутри загруженных дней: «0002-11-10» на
// полпути набора года — не повод для запроса и ошибки (ревью #604).
let shDateTimer = 0;
for (const id of ["#sh-d1", "#sh-d2"]) $(id).addEventListener("change", () => {
  clearTimeout(shDateTimer);
  shDateTimer = setTimeout(() => {
    const a = $("#sh-d1"), b = $("#sh-d2"), ok = (i) => i.value && i.value >= i.min && i.value <= i.max;
    if (ok(a) && ok(b)) shPeriod(a.value, b.value);
  }, 500);
});
function shRender() {
  const v = sh.v;
  $("#sh-empty").hidden = !!v;
  for (const id of ["#sh-kpis", "#sh-chips", "#sh-filters", "#sh-grid"]) $(id).hidden = !v;
  $("#sh-file").dataset.tipB = shOzon() ? "кабинет по ключу Seller API, только чтение; ключ — в Связке ключей, данные никуда не уходят" : "окно только читает файл и помнит путь; данные никуда не уходят";
  srcTag("#sh-src-tag", !v ? "" : shOzon() ? "ozon" : "file");
  $("#sh-file").textContent = v ? shSourceLine(v) : "";
  if (v) $("#sh-file").dataset.tipB = shSourceLine(v) + " · " + $("#sh-file").dataset.tipB; // строка обрезана — полностью здесь
  shPeriodRender();
  $("#sh-costs").hidden = !shOzon();
  if (v && v.costsErr) mkErr("Себестоимость не прочитана, прибыль не посчитана: " + v.costsErr);
  if (!v) return void ($("#sh-advice").hidden = $("#sh-econ").hidden = $("#sh-loss").hidden = true);
  const t = v.total, urgent = v.products.filter((p) => p.urgent).length, oz = shOzon();
  const kpi = (k, val, s, cls, tip) => `<div class="mk-kpi"${mkTip(k, tip)}><div class="k">${k}</div><div class="v${cls ? " " + cls : ""}">${val}</div><div class="s">${s}</div></div>`;
  $("#sh-kpis").innerHTML =
    kpi("Заказано", fmt(t.ordered) + " шт", `на ${fmt(Math.round(t.orderedSum / 1e3))} тыс. ₽ за ${v.periodDays || "—"} дн., с отменами`, "", shHint("ordered") + " · сумма заказов — не выручка: в нее входят отмены") +
    kpi("Выкуп", shPct(t.buyout), `выкуплено ${fmt(t.boughtOut)} шт${t.boughtSum ? ` на ${fmt(Math.round(t.boughtSum / 1e3))} тыс. ₽` : ""}`, t.buyout < 0.5 ? "bad" : "", shHint("buyout") + (t.boughtSum ? " · сумма — по цене продажи, до комиссии и логистики Ozon" : "")) +
    kpi("Отмены", shPct(t.cancel), `отменено ${fmt(t.cancelled)} шт`, t.cancel > 0.5 ? "bad" : "", shHint("cancel")) +
    (oz ? kpi("Отказ при получении", shPct(t.refuse), `отказались ${fmt(t.refused)} шт — часть отмен`, t.refuse >= SH_REFUSE ? "bad" : "", shHint("refuse"))
      : kpi("Возвраты", shPct(t.return), `возвращено ${fmt(t.returned)} шт`, t.return > 0.1 ? "bad" : "", shHint("return"))) +
    kpi("Остаток", fmt(t.stock) + " шт", oz ? "к продаже сейчас" : t.daysOutOf ? `без остатка ${t.daysOut} из ${t.daysOutOf} дней` : "на конец периода", t.daysOut >= 7 ? "bad" : "", oz ? "штук на складах минус резерв, FBO и FBS вместе" : shHint("stock")) +
    (oz ? kpi("Срочно довезти", fmt(urgent), "товаров: запаса меньше чем на 2 недели", urgent ? "bad" : "", "товары, которых при нынешних продажах без отмен хватит меньше чем на две недели; сколько довезти на четыре — в карточке")
      : kpi("Срочно поставить", fmt(urgent), "товаров по рекомендации Ozon", urgent ? "bad" : "", "сколько товаров Ozon пометил «Срочно поставить»; сколько штук — в карточке товара"));
  shEcon();
  shLoss();
  shAdvice();
  $("#sh-chips").innerHTML = [["", "все товары", "показать все товары отчета"], ...Object.keys(SH_ISSUE).filter((k) => v.counts[k]).map((k) => [k, SH_ISSUE[k][0], SH_ISSUE[k][1]])]
    .map(([k, n, tip]) => `<button class="sh-chip" type="button" data-sh-issue="${k}" aria-pressed="${sh.issue === k && !sh.adv}"${mkTip(n, tip + " · нажмите — только такие товары")}>${k ? `<b>${v.counts[k]}</b>` : ""}${esc(n)}</button>`).join("");
  shTable();
  const p = v.problems;
  $("#sh-probs").hidden = !p.length;
  $("#sh-probs-sum").textContent = `Не прочитано строк: ${p.length}`;
  $("#sh-probs-list").innerHTML = p.map((x) => `<li>${x.row ? `строка ${x.row}: ` : ""}${esc(x.reason)}</li>`).join("");
}
// Деньги магазина (#580): строки «Экономики магазина» Ozon из начислений за тот же период.
const SH_ECON = [
  ["sales", "Продажи и возвраты", "по цене продавца, с баллами за скидки; возвраты — минусом"],
  ["commission", "Вознаграждение Ozon", "комиссия за продажи"],
  ["delivery", "Доставка", "логистика, обратная логистика отказов и возвратов, доставка до места выдачи силами Ozon"],
  ["promotion", "Продвижение", "оплата за клик и продвижение товара"],
  ["partners", "Услуги партнеров", "доставка до места выдачи, эквайринг, обработка возвратов, упаковка, временное размещение, звездные товары"],
  ["other", "Остальное", "прочие статьи Ozon: перенос карточек, материалы, вывоз, утилизация, компенсации"],
  ["total", "На счет", "все строки вместе: сколько начислено к выплате, до себестоимости товара"],
];
function shEcon() {
  const e = sh.v.economy, el = $("#sh-econ");
  el.hidden = !e;
  if (!e) return void (el.innerHTML = "");
  if (e.err) return void (el.innerHTML = `<h3>Деньги магазина</h3><div class="note">Начисления не загрузились: ${esc(e.err)}</div>`);
  // Доля — только от положительных продаж; знак строки виден по сумме; Math.round(-0,4) — без «-0» (ревью #594).
  const rub = (x) => fmt(Math.round(x) || 0) + " ₽", pct = (x) => (e.sales > 0 && Math.round(x) ? ` <small>${shPct(Math.abs(x) / e.sales)}</small>` : "");
  el.innerHTML = `<h3>Деньги магазина · ${esc(sh.v.period)}</h3><dl>` +
    SH_ECON.filter(([k]) => k !== "other" || e.other).map(([k, label, tip]) => `<div class="${k}"${mkTip(label, tip + " · по начислениям Ozon")}><dt>${label}</dt><dd>${rub(e[k])}${k === "sales" ? "" : pct(e[k])}</dd></div>`).join("") +
    `</dl><div class="note">как «Экономика магазина» в кабинете Ozon; доля — от продаж${shPart() ? "; по дню начисления, а заказы в таблице — по дню заказа: продажа заказа прошлого периода попадает сюда" : ""}</div>`;
}
// Возможные пропажи (#580): вид — что случилось и что сделать в кабинете.
const SH_LOSS = {
  no_sale: ["Доставлено, продажи нет", "отправление доставлено больше двух недель назад, а продажи в начислениях нет — проверьте отправление в кабинете и, если денег нет, напишите в поддержку Ozon"],
  write_off: ["Возврат списан", "Ozon списал невыкуп или возврат — проверьте, есть ли за него компенсация, иначе подайте претензию"],
  utilized: ["Возврат утилизирован", "товар утилизирован — если вы не заказывали утилизацию, подайте претензию"],
  lost: ["Возможно потерян", "Ozon пометил возврат «возможно потерян» — дождитесь компенсации или подайте претензию"],
  stuck: ["Возврат застрял", "возврат больше двух недель без движения — спросите поддержку Ozon, где товар"],
  pickup: ["Заберите возврат", "возврат FBS больше двух недель ждет вас в пункте выдачи — заберите его, пока не начали брать за хранение или не утилизировали"],
};
function shLoss() {
  const l = sh.v.losses, el = $("#sh-loss");
  el.hidden = !l || (!l.err && !l.rows.length);
  if (el.hidden) return void (el.innerHTML = "");
  if (l.err) return void (el.innerHTML = `<div class="note">Пропажи не проверены: ${esc(l.err)}</div>`);
  const sum = l.rows.reduce((a, r) => a + r.price, 0), kind = (k) => SH_LOSS[k] || [k, ""];
  el.innerHTML = `<details><summary><b>Проверить: ${fmt(l.rows.length)}</b> — возможные пропажи на ${fmt(Math.round(sum))} ₽ по цене товара${shPart() ? " · за все загруженные дни: пропажа видна через 14 дней" : ""}</summary><div class="wrap"><table><tbody>` +
    l.rows.map((r) => `<tr><td class="why"${mkTip(kind(r.kind)[0], kind(r.kind)[1])}>${esc(kind(r.kind)[0])}</td><td>${esc(r.name)}<br>отправление ${esc(r.posting)} · SKU ${shOzonSKU(r.sku)}</td><td class="n">${fmt(Math.round(r.price))} ₽</td><td class="n"${mkTip("Дата", "для «доставлено, продажи нет» — день последнего начисления по отправлению; для возврата — когда сменился его статус")}>${r.since ? "с " + esc(r.since) : "—"}</td></tr>`).join("") +
    `</tbody></table></div><div class="note">компенсации Ozon платит общей суммой без номера отправления — оплачена ли пропажа, смотрите в отчете о компенсациях в разделе «Финансы» кабинета</div></details>`;
}
// Советы (#580): домен решает, каким товарам какой совет; слова здесь.
const SH_ADV = {
  refusals: (a, n) => [`<b>Отказываются при получении</b>: ${fmt(a.units)} шт у ${fmt(n)} товаров`, "чаще всего не подходит размер или посадка: проверьте замеры изделия в карточке, таблицу размеров и фото на модели с ростом и размером, почитайте отзывы и вопросы"],
  supply: (a, n) => (shOzon() ? [`<b>Довезти</b>: ${fmt(n)} товаров кончатся меньше чем через две недели`, `на четыре недели нужно около ${fmt(a.units)} шт; считано по продажам без отмен`]
    : [`<b>Поставить</b>: Ozon советует срочно поставить ${fmt(n)} товаров`, `всего ${fmt(a.units)} шт по рекомендации Ozon`]),
  dead: (a, n) => [`<b>Лежит без заказов</b>: ${fmt(n)} товаров, ${fmt(a.units)} шт за ${fmt(sh.v.periodDays)} дн.`, "акция или скидка, реклама на ходовые размеры или вывоз со склада, чтобы не платить за хранение"],
  keep: (a, n) => [`<b>Продается чисто</b>: ${fmt(n)} товаров с 10+ заказами, ${shOzon() ? "отказами до 20 % и отменами до 30 %" : "отменами до 30 % и без проблем с возвратами, рекламой и рейтингом"}`, "держите их в наличии во всех размерах — на них держится магазин"],
};
// Значок совета — в стиле значков окна (контур 24×24); цвет задает вид совета в style.css.
const SH_ADV_ICON = {
  refusals: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11"/>', // вернули при получении
  supply: '<path d="M2 6h12v11H2z"/><path d="M14 10h4l4 4v3h-8"/><circle cx="6" cy="18" r="2"/><circle cx="18" cy="18" r="2"/>', // довезти
  dead: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>', // лежит без заказов
  keep: '<path d="m3 17 6-6 4 4 8-8"/><path d="M15 7h6v6"/>', // продается чисто
};
function shAdvice() {
  const v = sh.v, el = $("#sh-advice"), adv = (v.advice || []).filter((a) => SH_ADV[a.kind]);
  el.hidden = !adv.length;
  const name = (sku) => (v.products.find((p) => p.sku === sku) || {}).name || sku;
  el.innerHTML = adv.length ? "<h3>Советы</h3>" + adv.map((a) => {
    const [head, how] = SH_ADV[a.kind](a, a.skus.length), on = sh.adv === a.kind;
    const ex = [...new Set(a.skus.map(name))].slice(0, 3).join(", ");
    const ic = SH_ADV_ICON[a.kind] ? `<span class="ic" aria-hidden="true"><svg viewBox="0 0 24 24">${SH_ADV_ICON[a.kind]}</svg></span>` : "";
    return `<div class="sh-adv" data-kind="${a.kind}" aria-current="${on}">${ic}<div>${head} — ${esc(how)}</div><div class="ex">например: ${esc(ex)}</div>` +
      `<button class="btn" type="button" data-sh-adv="${a.kind}"${mkTip(on ? "Все товары" : "Товары совета", on ? "вернуть все товары в таблицу" : "оставить в таблице только эти товары, по важности")}>${on ? "Все товары" : "Показать " + fmt(a.skus.length)}</button></div>`;
  }).join("") : "";
}
function shList() {
  const fold = (t) => t.toLowerCase().replace(/ё/g, "е"), q = fold(sh.q.trim()), ps = sh.v.products, k = sh.sort;
  return ps.map((_, i) => i).filter((i) => (!q || fold(ps[i].name).includes(q) || ps[i].sku.includes(q) || fold(ps[i].article || "").includes(q)) && (!sh.issue || ps[i].issues.includes(sh.issue)) && (!sh.only || sh.only.has(ps[i].sku)))
    .sort((a, b) => (!k && sh.only ? sh.only.get(ps[a].sku) - sh.only.get(ps[b].sku) // совет: по его важности
      : k === "name" ? sh.dir * ps[a].name.localeCompare(ps[b].name, "ru")
      : k === "profit" && (ps[a].profit == null || ps[b].profit == null) ? (ps[a].profit == null) - (ps[b].profit == null) // «—» — в конце при любом направлении (ревью #598)
      : sh.dir * ((ps[a][k] || 0) - (ps[b][k] || 0))));
}
function shTable() {
  const ps = sh.v.products, list = shList();
  if (!list.includes(sh.sel)) sh.sel = list.length ? list[0] : -1;
  const th = (k, label, cls, tip) => `<th class="${cls}"${sh.sort === k ? ` aria-sort="${sh.dir > 0 ? "ascending" : "descending"}"` : ""}><button type="button" data-sh-sort="${k}"${mkTip(label, tip + " · нажмите — сортировка")}>${label}${sh.sort === k ? (sh.dir > 0 ? " ↑" : " ↓") : ""}</button></th>`;
  const bad = (k, p) => (k === "profit" && p.profit < 0) || (k === "onAccount" && p.onAccount < 0) || (k === "cancel" && p.cancel > 0.5) || (k === "refuse" && p.refuse >= SH_REFUSE) || (k === "return" && p.return > 0.1) || (k === "drr" && p.drr > 0.15) || (k === "daysOut" && p.daysOut >= 7) || (k === "rating" && p.reviews && Number.isFinite(p.rating) && p.rating < 4.5);
  const tags = (p) => p.issues.map((i) => `<span class="sh-tag ${i}"${mkTip(SH_ISSUE[i][0], SH_ISSUE[i][1])}>${esc(SH_ISSUE[i][0])}</span>`).join("");
  $("#sh-table").innerHTML = `<thead><tr>${th("name", "Товар", "", "название и SKU")}${shCols().map(([k, l]) => th(k, l, "n", shHint(k))).join("")}${th("issues", "Что не так", "", "проблемы товара по порогам; пороги — в подсказках плашек выше")}</tr></thead><tbody>` +
    list.map((i) => { const p = ps[i]; return `<tr tabindex="0" data-sh-n="${i}" aria-selected="${i === sh.sel}"><td>${esc(p.name)}<span class="sku">${p.article ? esc(p.article) + shCopyBtn(p.article) + " · " : ""}${shOzonSKU(p.sku)}${p.scheme ? " · " + esc(p.scheme) : ""}</span></td>` +
      shCols().map(([k]) => `<td class="n${bad(k, p) ? " bad" : ""}">${shVal(k, p)}</td>`).join("") + `<td>${tags(p)}</td></tr>`; }).join("") + "</tbody>";
  $("#sh-none").hidden = list.length > 0;
  shCard();
}
// shWhy — выводы по товару из тех же чисел, что в таблице.
function shWhy(p) {
  const out = [];
  if (p.refused && p.refused * 2 >= p.cancelled && p.refuse >= 0.2) out.push(`при получении отказались ${fmt(p.refused)} из ${fmt(p.ordered)} (${shPct(p.refuse)}): чаще всего не подошел размер или посадка — проверьте замеры, таблицу размеров и фото`);
  else if (p.issues.includes("cancels")) out.push(`отменили ${shPct(p.cancel)} заказов (${fmt(p.cancelled)} из ${fmt(p.ordered)})${p.daysOut ? ` — и товара не было ${p.daysOut} из ${p.daysOutOf} дней: похоже, отменяют из-за нехватки` : ""}`);
  if (p.urgent && shOzon()) out.push(`запаса меньше чем на две недели: остаток ${fmt(p.stock)} шт, на четыре недели довезти ${fmt(p.supplyQty)} шт`);
  else if (p.urgent) out.push(`Ozon советует срочно поставить${p.supplyQty ? ` ${fmt(p.supplyQty)} шт` : ""}; остаток ${fmt(p.stock)} шт`);
  else if (p.issues.includes("stock")) out.push(`товара не было ${p.daysOut} из ${p.daysOutOf} дней; остаток ${fmt(p.stock)} шт`);
  if (p.issues.includes("returns")) out.push(`вернули ${shPct(p.return)} заказов: проверьте размерную сетку и фото`);
  if (p.issues.includes("ads")) out.push(`реклама — ${shPct(p.drr)} выручки`);
  if (p.issues.includes("rating")) out.push(`рейтинг ${String(p.rating).replace(".", ",")} при ${fmt(p.reviews)} отзывах`);
  if (!out.length) out.push(p.ordered ? `выкуп ${shPct(p.buyout)}, явных проблем по порогам нет` : p.stock ? `заказов за период не было, лежит ${fmt(p.stock)} шт: акция, реклама или вывоз со склада` : "заказов за период не было");
  return out;
}
// shOzonSKU — SKU ссылкой на страницу товара на ozon.ru; не число — просто текст.
const shOzonSKU = (sku) => /^\d+$/.test(sku || "")
  ? `<button type="button" class="lnk" data-open-url="https://www.ozon.ru/product/${sku}/"${mkTip("Открыть на Ozon", "страница товара на ozon.ru в браузере")}>${sku} ↗</button>` : esc(sku || "—");
// shCopyBtn — значок «скопировать» у артикула продавца: его ищут в кабинете и в своих таблицах.
const COPY_ICON = `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3"/></svg>`;
const shCopyBtn = (text) => `<button type="button" class="sh-copy" data-sh-copy="${esc(text)}" aria-label="Скопировать артикул"${mkTip("Скопировать артикул", text)}>${COPY_ICON}</button>`;
async function shCopy(btn) {
  const text = btn.dataset.shCopy;
  try {
    if (window.runtime && window.runtime.ClipboardSetText) { if (!(await window.runtime.ClipboardSetText(text))) throw new Error("буфер"); }
    else await navigator.clipboard.writeText(text);
    toast("Скопировано", ` · ${text}`);
  } catch { toast("Не скопировал", " · выделите артикул и нажмите ⌘C", null, "off"); }
}
document.addEventListener("click", (e) => { const b = e.target.closest("[data-sh-copy]"); if (b) { hideTip(); shCopy(b); } });
function shCard() {
  const p = sh.v.products[sh.sel];
  if (!p) { $("#sh-card").innerHTML = ""; return; }
  const rows = [["SKU", p.sku || "—", "номер товара на Ozon"], ["Артикул", p.article || "—", "артикул продавца: размер и цвет обычно в нем"], ["Схема", p.scheme || "—", "FBO — со склада Ozon, FBS — со своего"], ["ABC по сумме", p.abc || "—", sh.v.hints.abc || "A — 80 % заказов, B — 15 %, C — 5 %"],
    ["Заказано", `${fmt(p.ordered)} шт на ${fmt(Math.round(p.orderedSum))} ₽ с отменами`, shHint("ordered")],
    ["Выкуплено на", p.boughtSum ? fmt(Math.round(p.boughtSum)) + " ₽" : "—", "по цене продажи, до комиссии и логистики Ozon"], ["Доставлено", fmt(p.delivered) + " шт", sh.v.hints.delivered || ""],
    ["Выкуплено", `${fmt(p.boughtOut)} шт · ${shPct(p.buyout)}`, shHint("buyout")], ["Отменено", `${fmt(p.cancelled)} шт · ${shPct(p.cancel)}`, shHint("cancel")],
    ["Отказ при получении", `${fmt(p.refused)} шт · ${shPct(p.refuse)}`, shHint("refuse")],
    ["Возвращено", `${fmt(p.returned)} шт · ${shPct(p.return)}`, shHint("return")], ["Остаток", fmt(p.stock) + " шт", shHint("stock")],
    ["Цена", shVal("avgPrice", p), shHint("avgPrice")], ["Индекс цен", p.priceIndex || "—", sh.v.hints.priceIndex || ""], ["ДРР", shVal("drr", p), shHint("drr")],
    ["Рейтинг", `${shVal("rating", p)} · отзывов ${fmt(p.reviews)}`, shHint("rating")]];
  const gone = shOzon() ? ["ABC по сумме", "Возвращено", "Индекс цен", "ДРР", "Рейтинг"] : ["Отказ при получении", "Выкуплено на"];
  $("#sh-card").innerHTML = `<div class="mk-card-h"><h3>${esc(p.name)}</h3></div><dl>${rows.filter((r) => !gone.includes(r[0])).map(([k, v, t]) => `<dt${t ? mkTip(k, t) : ""}>${k}</dt><dd>${k === "SKU" ? shOzonSKU(p.sku) : k === "Артикул" && p.article ? esc(v) + shCopyBtn(p.article) : esc(v)}</dd>`).join("")}</dl>` +
    shMoneyCard(p) + `<ul>${shWhy(p).map((w) => `<li>${esc(w)}</li>`).join("")}</ul>`;
}
// Деньги товара в карточке (#580): строки начислений и себестоимость — поле, по артикулу.
function shMoneyCard(p) {
  if (!shOzon()) return "";
  const m = p.money || { units: 0, total: 0, unitCost: 0, hasCost: false, cost: 0 };
  const line = (k, v, t) => `<dt${mkTip(k, t)}>${k}</dt><dd>${esc(v)}</dd>`;
  const rows = p.money && (m.units || m.total) ? line("Продано", `${fmt(m.units)} шт за вычетом возвратов`, "по начислениям Ozon за период") +
    line("Продажи", shRub(m.sales), "по цене продавца, с баллами за скидки") + line("Вознаграждение", shRub(m.commission), "комиссия Ozon") +
    line("Доставка", shRub(m.delivery), "логистика и обратная логистика") + line("Услуги", shRub(m.partners + m.other), "партнеры, эквайринг, упаковка, прочее") +
    line("На счет", shRub(m.total), "до рекламы: ее Ozon начисляет на магазин") + (m.hasCost ? line("Себестоимость", `${shRub(m.cost)} (${shRub(m.unitCost)} × ${fmt(m.units)})`, "себестоимость проданного") +
    line("Прибыль", shRub(p.profit ?? m.total - m.cost), "на счет минус себестоимость; до рекламы и налогов") : "")
    : line("Деньги", sh.v.economy && sh.v.economy.err ? "начисления не загрузились — причина в «Деньгах магазина»" : "начислений по товару за период нет", "");
  const cost = m.hasCost ? String(m.unitCost).replace(".", ",") : "";
  return `<h4 class="sh-money-h">Деньги товара</h4><dl>${rows}</dl>` + (p.article
    ? `<label class="sh-cost"${mkTip("Себестоимость штуки", "по артикулу продавца; хранится на этом Mac, у каждого кабинета своя; пусто — убрать")}>Себестоимость штуки <input data-sh-cost="${esc(p.article)}" inputmode="decimal" value="${esc(cost)}" placeholder="—"> ₽</label>`
    : `<div class="note">у товара нет артикула — себестоимость не задать</div>`);
}
// Себестоимость из карточки: сохраняется по Enter или уходу из поля, прибыль пересчитывается на месте.
$("#mk-shop").addEventListener("change", async (e) => {
  const inp = e.target.closest("[data-sh-cost]");
  if (!inp || !shOzon() || !native() || !native().MarketsSetCost) return;
  const art = inp.dataset.shCost, raw = inp.value.trim();
  let unit = 0;
  try { unit = await native().MarketsSetCost(sh.v.path, art, raw); } catch (err) { mkErr("Себестоимость не сохранена: " + errText(err)); return; }
  mkErr("");
  shApplyCosts({ [art]: raw ? unit : null }); // сохраненное окном, а не свой разбор ввода (ревью #598)
  toast("Себестоимость", raw ? ` · ${art}: ${shRub(unit)}` : ` · ${art}: убрана`);
});
// shApplyCosts пересчитывает прибыль на месте: артикул → рубли, null — убрать.
function shApplyCosts(costs) {
  for (const p of sh.v.products) {
    if (!(p.article in costs)) continue;
    const unit = costs[p.article], m = (p.money ||= { units: 0, sales: 0, commission: 0, delivery: 0, partners: 0, other: 0, total: 0, unitCost: 0, hasCost: false, cost: 0 });
    Object.assign(m, { unitCost: unit ?? 0, hasCost: unit !== null, cost: unit !== null ? unit * Math.max(m.units, 0) : 0 });
    p.profit = unit !== null && (m.units || m.total) ? m.total - m.cost : null;
  }
  shTable();
}
$("#sh-costs").addEventListener("click", async () => {
  hideTip();
  if (!shOzon() || !native() || !native().MarketsImportCosts) return;
  let p = "";
  try { p = await native().MarketsPickFileTitled("Себестоимость: колонки «Артикул» и «Себестоимость»"); } catch (err) { return mkErr("Файл не выбран: " + errText(err)); }
  if (!p) return;
  try {
    const r = await native().MarketsImportCosts(sh.v.path, p);
    const bad = r.problems.length ? ` · не принято ${r.problems.length}: строка ${r.problems[0].row} — ${r.problems[0].reason}` : "";
    toast("Себестоимость", ` · принято ${fmt(r.read)} артикулов${bad}`, null, r.problems.length ? "bad" : "");
    shApplyCosts(r.costs || {}); // на месте, без новой загрузки кабинета (ревью #598)
  } catch (err) { mkErr("Себестоимость не прочитана: " + errText(err)); }
});
$("#mk-shop").addEventListener("click", (e) => {
  const c = e.target.closest("[data-sh-issue]");
  if (c) { hideTip(); Object.assign(sh, { issue: c.dataset.shIssue, only: null, adv: "" }, sh.sort ? {} : { sort: "ordered", dir: -1 }); shRender(); return; }
  const adv = e.target.closest("[data-sh-adv]");
  if (adv) {
    hideTip();
    const a = (sh.v.advice || []).find((x) => x.kind === adv.dataset.shAdv), on = sh.adv === adv.dataset.shAdv;
    Object.assign(sh, on || !a ? { only: null, adv: "", sort: "ordered", dir: -1 } : { only: new Map(a.skus.map((k, i) => [k, i])), adv: a.kind, issue: "", sort: "", sel: -1 });
    shRender(); return;
  }
  const s = e.target.closest("[data-sh-sort]");
  if (s) {
    const k = s.dataset.shSort === "issues" ? "cancel" : s.dataset.shSort;
    if (sh.sort === k) sh.dir = -sh.dir; else { sh.sort = k; sh.dir = k === "name" ? 1 : -1; }
    shTable(); return;
  }
  const tr = e.target.closest("[data-sh-n]");
  if (tr && !e.target.closest("[data-open-url], [data-sh-copy]")) { sh.sel = +tr.dataset.shN; shTable(); } // ссылка и копирование не меняют выбор
});
$("#sh-table").addEventListener("keydown", (e) => {
  const tr = e.target.closest("[data-sh-n]"); if (!tr) return;
  const sib = e.key === "ArrowDown" ? tr.nextElementSibling : e.key === "ArrowUp" ? tr.previousElementSibling : null;
  if (!sib) return;
  e.preventDefault(); sh.sel = +sib.dataset.shN; shTable();
  const now = document.querySelector(`[data-sh-n="${sh.sel}"]`); if (now) now.focus();
});
$("#sh-q").addEventListener("input", (e) => { sh.q = e.target.value; if (sh.v) shTable(); });
$("#sh-q").addEventListener("keydown", (e) => { if (e.key === "Escape" && e.target.value) { e.preventDefault(); e.stopPropagation(); e.target.value = ""; sh.q = ""; shTable(); } });
// «Оформление → Маркеты» (accents.look mk-*): палитра, свои цвета, анимация,
// вид графика и плотность строк. Владелец 13.11: гибкость, как у Артефактов.
const MK_ANIMS = [["full", "перелив при наведении"], ["none", "без анимации"]];
const MK_CHARTS = [["line", "линия"], ["area", "линия с заливкой"]];
const MK_DENSE = [["normal", "обычные"], ["dense", "плотные"]];
const MK_KEYS = ["mk-grad", "mk-anim", "mk-chart", "mk-dense", "mk-c1", "mk-c2"];
function mkLook() {
  const l = state.accents.look || {};
  const pick = (v, list, d) => (list.includes(v) ? v : d);
  return {
    grad: pick(l["mk-grad"], Object.keys(MH_GRADS), "spectrum"), anim: pick(l["mk-anim"], MK_ANIMS.map((x) => x[0]), "full"),
    chart: pick(l["mk-chart"], MK_CHARTS.map((x) => x[0]), "line"), dense: pick(l["mk-dense"], MK_DENSE.map((x) => x[0]), "normal"),
    c1: MH_HEX.test(l["mk-c1"] || "") ? l["mk-c1"] : "#e8b84a", c2: MH_HEX.test(l["mk-c2"] || "") ? l["mk-c2"] : "#5fb0d8",
  };
}
function mkApply(L = mkLook()) {
  const st = document.body.style, b = document.body.dataset;
  b.mk = L.grad; b.mka = L.anim; b.mkc = L.chart; b.mkd = L.dense;
  for (const [i, v] of [[1, L.c1], [2, L.c2], [3, L.c2]]) {
    if (L.grad === "custom") st.setProperty("--mk" + i, v); else st.removeProperty("--mk" + i);
  }
}
function renderLookMarkets() {
  const host = $("#look-markets"); if (!host) return;
  const L = mkLook(), dis = native() ? "" : " disabled";
  const chip = (k, v, label, on, extra = "") => `<button class="look-chip" data-ml="${k}" data-v="${v}" aria-pressed="${on}"${dis}>${extra}${esc(label)}</button>`;
  const grads = Object.entries(MH_GRADS).map(([g, n]) => chip("mk-grad", g, n, L.grad === g, g === "custom" ? `<i class="gs" style="background:linear-gradient(90deg,${L.c1},${L.c2})"></i>` : `<i class="gs ink-${g}"></i>`)).join("");
  const colors = L.grad === "custom" ? `<label class="muted">от <input type="color" data-mc="mk-c1" value="${L.c1}"${dis}></label> <label class="muted">до <input type="color" data-mc="mk-c2" value="${L.c2}"${dis}></label>` : "";
  host.innerHTML = `
    <div class="row"><span>Градиент</span><div class="opts">${grads}${colors}
      <span class="muted">заголовок, вкладка, оценка, график сезона, выбранная строка и главные кнопки</span></div></div>
    <div class="row"><span>Анимация</span><div class="opts">${MK_ANIMS.map(([v, n]) => chip("mk-anim", v, n, L.anim === v)).join("")}</div></div>
    <div class="row"><span>График сезона</span><div class="opts">${MK_CHARTS.map(([v, n]) => chip("mk-chart", v, n, L.chart === v)).join("")}
      <span class="muted">заливка под линией — легче увидеть, где провал</span></div></div>
    <div class="row"><span>Строки таблицы</span><div class="opts">${MK_DENSE.map(([v, n]) => chip("mk-dense", v, n, L.dense === v)).join("")}
      <span class="muted">плотные — больше ниш на экране</span></div></div>
    <div class="row"><span></span><div class="opts"><button class="look-chip" data-ml-reset${dis}>Сбросить вид Маркетов</button>
      <span class="muted">отдельные элементы выключаются выше, в списке «Маркеты: …»</span></div></div>`;
}
$("#look-markets").addEventListener("click", (e) => {
  if (!native()) return;
  if (e.target.closest("[data-ml-reset]")) {
    const look = { ...(state.accents.look || {}) };
    for (const k of MK_KEYS) delete look[k];
    saveAccents({ ...state.accents, look });
    toast("Сброшено", " · вид Маркетов по умолчанию");
    return;
  }
  const b = e.target.closest("[data-ml]"); if (!b) return;
  mhSave({ [b.dataset.ml]: b.dataset.v }); // mhSave пишет любой ключ accents.look
});
// Цвет меняется вживую, сохраняется при отпускании.
$("#look-markets").addEventListener("input", (e) => {
  const c = e.target.closest("[data-mc]"); if (!c) return;
  const L = { ...mkLook(), [c.dataset.mc.slice(3)]: c.value };
  mkApply(L);
  const sw = $('#look-markets [data-v="custom"] .gs'); if (sw) sw.style.background = `linear-gradient(90deg,${L.c1},${L.c2})`;
});
$("#look-markets").addEventListener("change", (e) => { const c = e.target.closest("[data-mc]"); if (c && native()) mhSave({ [c.dataset.mc]: c.value }); });


// --- Память (ADR-0008) -------------------------------------------------------------
// Два слоя и журнал рядом, очередь на одобрение, потолок горячего слоя. Окно
// показывает те же файлы, что читает `kbengine memory`; своего хранилища нет.
const MEM_TREE = [["health", "Здоровье", "можно ли доверять памяти"], ["hot", "HOT.md", "читается в каждой сессии"], ["cold", "cold/", "факты, которые ищутся"], ["journal", "journal/", "хроника, из поиска исключена"], ["queue", "Ждет одобрения", "предложения агента"]];
const MEM_PROBLEMS = {
  "hot-too-big": "Горячий слой больше потолка",
  "no-topic-list": "В HOT.md нет перечня тем",
  "stale-topics": "Перечень тем отстал от записей",
  "no-topic": "У записи нет темы",
  "no-description": "У записи нет описания",
  "not-indexed": "Записи нет в индексе",
  "dangling-index": "Индекс ведет на файл, которого нет",
  "long-hook": "Крючок длиннее 150 знаков",
  "index-too-big": "Индекс больше потолка",
  "stale-vectors": "Векторы отстали от записей",
  "invalid-note": "У записи недопустимый текст",
  "hot-changed-outside": "HOT.md изменен мимо системы",
};
// Плашка при входе (решение владельца 09.11): эти файлы читают агенты в каждой
// сессии, поэтому правка горячего слоя меняет их поведение везде.
const MM_WARN = `<div class="mm-warn" role="note"><b>Чувствительный уровень.</b> Эти файлы читают агенты в каждой сессии: правило в горячем слое начинает действовать везде сразу, а в памяти могут лежать личные и рабочие факты. Решайте внимательно и не показывайте экран другим. Любая запись в HOT.md идет по Touch ID и остается в журнале решений; правка файла мимо системы будет видна. Отклоненное не удаляется, а уходит в journal/.</div>`;
const MEM_TOPIC_FIX = new Set(["no-topic-list", "stale-topics"]);
const mmTokens = (n) => n.toLocaleString("ru-RU");
// Путь у каждого свой (домашняя папка пользователя): на экране ~, чтобы имя учетной записи не светилось на скриншотах.
const mmPath = (p) => p.replace(/^\/Users\/[^/]+/, "~");
async function openMemory() { await mmLoad(); mmRender(); }
// Сигнал о здоровье виден и снаружи экрана: точка на кнопке «Память» в шапке (решение
// владельца 09.11: внимание должно бросаться в глаза, иначе человек пропустит и забудет).
function mmDot() { return state.mm.alert ? `<i class="mm-alert ${state.mm.alert}" role="img" aria-label="Память требует внимания" title="Память требует внимания: откройте раздел «Память»"></i>` : ""; }
function mmSetAlert(d) {
  const a = d && d.exists && d.health && d.health.mode !== "ok" ? d.health.mode : "";
  if (a !== state.mm.alert) { state.mm.alert = a; renderTabs(); }
}
async function mmProbe() {
  if (!native() || !state.modules.some((m) => m.id === "memory" && m.enabled)) return;
  try {
    const d = await native().MemoryState();
    mmSetAlert(d);
    if (!$("#memory-view").hidden && !state.mm.busy) { state.mm.data = d; mmRender(); } // открытый экран не остается со старым счетом
  } catch { /* сигнал необязателен: экран сам покажет ошибку */ }
}
setTimeout(mmProbe, 1500);
setInterval(mmProbe, 10 * 60 * 1000);
window.addEventListener("focus", mmProbe);
function mmAlertHTML(d) {
  const h = d.health;
  if (!d.exists || h.mode === "ok") return "";
  const bad = h.mode === "bad";
  const items = h.reasons.map((r) => { const [t, det, act] = mhReasonHTML(h, r); return `<li><span><b>${t}</b> <span class="muted">${det}</span></span>${act}</li>`; }).join("");
  return `<div class="mh-alert ${h.mode}" role="alert"><div class="mh-alert-ic" aria-hidden="true">!</div>
    <div class="mh-alert-main"><div class="mh-alert-t">${bad ? "ПАМЯТЬ НЕ ПРОВЕРЕНА: ЕЙ РАНО ДОВЕРЯТЬ" : "ПАМЯТИ НУЖЕН ОСМОТР"} <span class="mh-alert-n">${h.total}/100</span></div>
      <p>${esc(MH_MODES[h.mode][2])}</p><ul>${items}</ul></div></div>`;
}
async function mmLoad() {
  if (!native()) return;
  try { state.mm.data = await native().MemoryState(); mmSetAlert(state.mm.data); }
  catch (e) { state.mm.data = null; showError("Память не прочитать: " + errText(e)); }
}
function mmPct(h) { return h.limit ? Math.round((h.runes * 100) / h.limit) : 0; }
function mmTone(h) { const p = mmPct(h); return p > 100 ? "over" : p >= 80 ? "warn" : "ok"; }
function mmEmptyHTML(d) {
  return `<div class="mm-empty"><h3>Памяти еще нет</h3>
    <p class="muted">Заведем скелет: <code>HOT.md</code> читается в каждой сессии, в <code>cold/</code> лежат факты, в <code>journal/</code> хроника. Все это обычные файлы с правами 0600 в <code title="${esc(d.dir)}">${esc(mmPath(d.dir))}</code>, терминал их читает так же (<code>kbengine memory</code>).</p>
    <button class="ac-btn pri" data-mm="init">Завести память</button></div>`;
}
function mmHeadHTML(d) {
  const bad = d.problems;
  const list = bad.map((p) => `<li><b>${esc(MEM_PROBLEMS[p.kind] || p.kind)}</b> <span class="muted">${esc(p.file)} · ${esc(p.detail)}</span></li>`).join("");
  const fix = (bad.some((p) => MEM_TOPIC_FIX.has(p.kind)) ? `<button class="ac-btn pri" data-mm="topics">Пересобрать перечень тем · Touch ID</button>` : "") +
    (bad.some((p) => p.kind === "hot-changed-outside") ? `<button class="ac-btn pri" data-mm="seal" title="Подтверждение остается в журнале решений">Прочитал, принимаю текущий HOT.md · Touch ID</button>` : "");
  return `<div class="mm-head"><h2>Память</h2>
      <span class="${bad.length ? "mm-bad" : "mm-ok"}" role="status">${bad.length ? `check: нарушений ${bad.length}` : "check: ok"}</span>
      <button class="mh-headpill" data-mm="hv" data-v="pulse" title="Здоровье памяти: открыть пульт">${mhModePill(d.health)}<span class="muted">${d.health.total}/100</span></button>
      <span class="muted mm-dir" title="${esc(d.dir)}">${esc(mmPath(d.dir))}</span></div>
    ${bad.length ? `<ul class="mm-problems">${list}</ul><div class="mm-fix">${fix}</div>` : ""}`;
}
function mmTreeHTML(d) {
  const n = { health: String(d.health.total), hot: `${mmPct(d.hot)}%`, cold: d.cold.length, journal: d.journal.length, queue: d.queue.length };
  return `<div class="ac-list mm-tree" role="tablist" aria-label="Память">` + MEM_TREE.map(([id, name, hint]) =>
    `<button class="mm-node${id === "queue" && d.queue.length ? " wait" : ""}${id === "health" ? " h-" + d.health.mode : ""}" role="tab" data-mm="sel" data-v="${id}" aria-selected="${state.mm.sel === id}"><span>${esc(name)}<small>${esc(hint)}</small></span><b>${esc(String(n[id]))}</b></button>`).join("") +
    `<div class="mm-sub">Все лежит файлами и читается и правится без окна.</div></div>`;
}
function mmHotHTML(d) {
  const h = d.hot, pct = mmPct(h);
  const topics = h.topics.length ? h.topics.map((t) => `<span class="mm-chip">${esc(t)}</span>`).join("") : `<span class="muted">тем нет, они появятся с первой записью холодного слоя</span>`;
  return `<h3>HOT.md <span class="muted">· читается в каждой сессии</span></h3>
    <div class="mm-meter ${mmTone(h)}" role="meter" aria-label="Заполнение горячего слоя" aria-valuemin="0" aria-valuemax="${h.limit}" aria-valuenow="${h.runes}"><i style="width:${Math.min(pct, 100)}%"></i></div>
    <p class="mm-count"><b>${mmTokens(h.runes)}</b> из ${mmTokens(h.limit)} знаков · ${pct}% · на глаз ${mmTokens(h.tokens)} токенов на каждый ход</p>
    <form class="mm-limit" data-mm-form="limit"><label for="mm-limit">Потолок, знаков</label>
      <input id="mm-limit" type="number" min="200" max="200000" step="100" value="${h.limit}" inputmode="numeric">
      <button class="ac-btn" type="submit">Сохранить</button>
      <span class="muted">Новому человеку хватает 4 000, свод правил больше. Потолок лежит в memory.json: терминал меряет той же линейкой.</span></form>
    <div class="mm-topics"><span class="muted">Темы холодного слоя:</span> ${topics}</div>
    <div class="mm-acts"><button class="ac-btn" data-mm="who">Обновить «Кто я» из портрета · Touch ID</button></div>
    <pre class="mm-text" tabindex="0" aria-label="Текст HOT.md">${esc(h.text)}</pre>`;
}
function mmColdHTML(d) {
  if (!d.cold.length) return `<h3>cold/</h3><p class="muted">Холодный слой пуст. Запись добавляет <code>kbengine memory add</code> или вы одобряете предложение агента.</p>`;
  return `<h3>cold/ <span class="muted">· ${d.cold.length}</span></h3><ul class="mm-notes">` + d.cold.map((n) =>
    `<li><b class="mono">${esc(n.name)}</b> <span class="mm-chip">${esc(n.topic || "без темы")}</span>${n.origin === "agent" ? ` <span class="mm-chip agent">агент</span>` : ""}<div class="muted">${esc(n.description)}</div></li>`).join("") + `</ul>`;
}
function mmJournalHTML(d) {
  const dec = mhJournalHTML(d);
  const files = d.journal.length
    ? `<h4>Файлы</h4><p class="muted">Отклоненное и перенесенное в горячий слой, handoffs и хроника. В обычный поиск не входят.</p><ul class="mm-notes">${d.journal.slice().reverse().map((f) => `<li class="mono">${esc(f)}</li>`).join("")}</ul>`
    : `<h4>Файлы</h4><p class="muted">Файлов нет.</p>`;
  return `<h3>journal/ <span class="muted">· ${d.journal.length}</span></h3>${dec}${files}`;
}
function mmQueueHTML(d) {
  if (!d.queue.length) return `<h3>Ждет одобрения</h3><p class="muted">Очередь пуста. Агент сам в горячий слой не пишет: его предложения (<code>kbengine memory add --origin agent</code>) придут сюда.</p>`;
  return `<h3>Ждет одобрения <span class="muted">· ${d.queue.length}</span></h3>` + d.queue.map((n) =>
    `<article class="mm-card"><header><b class="mono">${esc(n.name)}</b> <span class="mm-chip">${esc(n.topic)}</span> <span class="muted">ждет ${mhDays(n.waitDays || 0)}</span></header>
      <p>${esc(n.description)}</p>${n.body ? `<pre class="mm-text">${esc(n.body)}</pre>` : ""}
      <div class="mm-acts"><button class="ac-btn pri" data-mm="approve" data-to="cold" data-name="${esc(n.name)}" title="Тема записи попадет в перечень HOT.md: потребуется Touch ID">В холодный слой · Touch ID</button>
        ${state.mm.confirm === n.name
          ? `<span class="mm-confirm" role="alert"><b>Правило «${esc(n.description)}» начнет действовать во всех сессиях агентов.</b> <button class="ac-btn pri" data-mm="approve-yes" data-name="${esc(n.name)}">Подтвердить · Touch ID</button> <button class="ac-btn" data-mm="approve-no">Отмена</button></span>`
          : `<button class="ac-btn" data-mm="approve-ask" data-name="${esc(n.name)}" title="В горячий слой уйдет одна строка: описание станет правилом; потребуется Touch ID">В горячий слой, как правило</button>`}
        <button class="ac-btn" data-mm="reject" data-name="${esc(n.name)}" title="Файл уйдет в journal/, не удалится">Отклонить</button></div></article>`).join("");
}
function mmRender() {
  const host = $("#mm-body"), m = state.mm, d = m.data;
  if (!native()) { host.innerHTML = `<div class="mm-empty"><h3>Память</h3><p class="muted">Экран работает в окне DVK.</p></div>`; return; }
  if (!d) { host.innerHTML = `<div class="mm-empty muted">Читаю…</div>`; return; }
  if (!d.exists) { host.innerHTML = mmEmptyHTML(d); return; }
  const pane = { health: mmHealthHTML, hot: mmHotHTML, cold: mmColdHTML, journal: mmJournalHTML, queue: mmQueueHTML }[m.sel](d);
  host.innerHTML = mmAlertHTML(d) + MM_WARN + mmHeadHTML(d) + `<div class="ac-grid mm-grid">${mmTreeHTML(d)}<div class="mm-pane" role="tabpanel">${pane}</div></div>`;
}
// --- Здоровье памяти (ADR-0008) ----------------------------------------------------
// Чем реже человек смотрит память, тем ниже доверие. Счет и причины считает Go (те же
// числа в терминале: `kbengine memory health`); страница рисует и ведет от ритма к дню,
// от дня к решению, от решения к правилу. Имена с префиксом mh: mm и mem заняты.
const MH_VIEWS = [["pulse", "Пульт"], ["rhythm", "Ритм"], ["rules", "Правила"], ["modes", "Режимы"]];
const MH_PARTS = { rhythm: "Давность осмотра", queue: "Очередь предложений", integrity: "Целостность", rules: "Свежесть правил" };
const MH_MODES = {
  ok: ["Доверяю", "ok", "Агент получает ответы памяти без оговорок, его предложения идут в очередь."],
  warn: ["С оговоркой", "warn", "Каждый ответ memory_find и memory_propose несет пометку «память давно не проверялась» и список правил без подтверждения больше 30 дней. Предложения идут в очередь."],
  bad: ["Не доверяю", "bad", "Ответы несут пометку «память не проверена», а memory_propose отказывает, пока вы не осмотрите память."],
};
const MH_KINDS = {
  "approve-hot": ["в горячий слой", "hot"], "approve-cold": ["в холодный слой", "cold"], reject: ["отклонено", "rej"], seal: ["печать подтверждена", "seal"],
  review: ["осмотр", "rev"], "confirm-rule": ["правило подтверждено", "hot"], topics: ["перечень тем", "up"], portrait: ["«Кто я»", "up"], index: ["индекс векторов", "up"], other: ["запись", "up"],
};
const MH_FILTERS = [["all", "Все"], ["hot", "Горячий слой"], ["cold", "Холодный слой"], ["rej", "Отклонено"], ["rev", "Осмотры"], ["up", "Обслуживание"]];
const MH_TILE_GO = { rhythm: `data-mm="hv" data-v="rhythm"`, queue: `data-mm="sel" data-v="queue"`, integrity: `data-mm="sel" data-v="hot"`, rules: `data-mm="hv" data-v="rules"` };
const MH_MON = ["янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"], MH_WD = ["вс", "пн", "вт", "ср", "чт", "пт", "сб"];
const mhNum = (iso) => { const [y, m, d] = iso.split("-").map(Number); return Math.round(Date.UTC(y, m - 1, d) / 864e5); };
const mhIso = (n) => new Date(n * 864e5).toISOString().slice(0, 10);
const mhLabel = (n) => { const x = new Date(n * 864e5); return `${MH_WD[x.getUTCDay()]}, ${x.getUTCDate()} ${MH_MON[x.getUTCMonth()]}`; };
const mhKey = (s) => s.toLowerCase().split(/\s+/).filter(Boolean).join(" ").replace(/\.+$/, "");
const mhTone = (ratio) => (ratio > 0.7 ? "ok" : ratio > 0.4 ? "warn" : "bad");
const mhPart = (h, id) => h.parts.find((p) => p.id === id) || { value: 0, max: 1 };
function mhRuleIndex(h, text) { const k = mhKey(text); return h.rules.findIndex((r) => mhKey(r.text) === k); }
const mhWord = (n, one, few, many) => { const a = Math.abs(n) % 100, b = a % 10; return a > 10 && a < 20 ? many : b === 1 ? one : b >= 2 && b <= 4 ? few : many; };
const mhDays = (n) => `${n} ${mhWord(n, "день", "дня", "дней")}`;
function mhReasonHTML(h, r) {
  const L = (view, label) => `<button class="ac-btn" data-mm="hv" data-v="${view}">${label} →</button>`;
  switch (r.kind) {
    case "seal": return [`HOT.md изменен мимо системы`, `файл правили после последней записи системы: прочитайте его и подтвердите`, `<button class="ac-btn pri" data-mm="seal">Принять · Touch ID</button>`];
    case "queue": return [`Предложения ждут решения: ${r.n}`, `самое старое ждет ${mhDays(h.oldestPendingDays)}`, `<button class="ac-btn" data-mm="sel" data-v="queue">Разобрать →</button>`];
    case "rules": return [`Правил без подтверждения больше 30 дней: ${r.n}`, `агент опирается на них вслепую`, L("rules", "Смотреть правила")];
    case "vectors": return [`Векторы отстали от записей: ${r.n}`, `поиск по смыслу их не видит; нужна локальная Ollama с bge-m3`, `<button class="ac-btn" data-mm="index">Пересобрать векторы</button>`];
    default: return [r.n < 0 ? `Решений в журнале нет` : `Осмотра не было ${mhDays(r.n)}`, `память лежит без суждения человека`, `<button class="ac-btn pri" data-mm="review">Отметить осмотр · Touch ID</button>`];
  }
}
function mhReasonsHTML(h) {
  if (!h.reasons.length) return `<p class="mh-ok">Все в порядке: осмотр свежий, очередь пуста, печать и векторы целы.</p>`;
  return h.reasons.map((r) => { const [t, d, act] = mhReasonHTML(h, r); return `<div class="mh-row"><div><b>${t}</b><div class="muted">${d}</div></div>${act}</div>`; }).join("");
}
function mhPartHint(h, id) {
  const q = h.reasons.find((r) => r.kind === "queue"), seal = h.reasons.find((r) => r.kind === "seal"), vec = h.reasons.find((r) => r.kind === "vectors");
  if (id === "rhythm") return h.idleDays < 0 ? "решений в журнале нет" : `последнее решение ${mhDays(h.idleDays)} назад`;
  if (id === "queue") return q ? `ждет ${q.n}, старшее ${mhDays(h.oldestPendingDays)}` : "очередь пуста";
  if (id === "integrity") return seal ? "HOT.md правили мимо системы" : vec ? `векторов отстало: ${vec.n}` : "печать и векторы в порядке";
  return `устаревших: ${h.staleRules} из ${h.rules.length}`;
}
function mhModePill(h) { const [name, tone] = MH_MODES[h.mode]; return `<span class="mh-pill ${tone}"><i></i>${name}</span>`; }
function mhHeat(h, cur) {
  const today = mhNum(h.today), judged = {};
  for (const e of h.events) if (e.judgment) judged[e.day] = (judged[e.day] || 0) + 1;
  const all = {};
  for (const e of h.events) all[e.day] = (all[e.day] || 0) + 1;
  return Array.from({ length: 91 }, (_, i) => {
    const n = today - 90 + i, iso = mhIso(n), c = judged[iso] || 0;
    const lvl = c >= 2 ? " l2" : c === 1 ? " l1" : all[iso] ? " up" : "";
    return `<button class="${lvl}${n === today ? " today" : ""}${iso === cur ? " sel" : ""}" data-mm="hv" data-v="rhythm" data-day="${iso}" title="${mhLabel(n)}: решений ${c}" aria-label="${mhLabel(n)}: решений ${c}"></button>`;
  }).join("");
}
function mhEventHTML(h, e, withDay = true, withRule = true) {
  const [label, cls] = MH_KINDS[e.kind] || MH_KINDS.other;
  const ri = withRule && (e.kind === "approve-hot" || e.kind === "confirm-rule") ? mhRuleIndex(h, e.text) : -1;
  const link = ri >= 0 ? ` <button class="mh-link" data-mm="hv" data-v="rules" data-rule="${ri}">→ правило</button>` : "";
  const day = withDay ? `<button class="mh-link faint" data-mm="hv" data-v="rhythm" data-day="${e.day}">${mhLabel(mhNum(e.day))}</button>` : "";
  return `<div class="mh-row"><div><span class="mh-chip ${cls}">${label}</span> ${esc(e.text)}${link}</div>${day}</div>`;
}
function mhPulseHTML(d) {
  const h = d.health, [mw, tone] = MH_MODES[h.mode];
  const head = h.mode === "ok" ? "Память в порядке" : h.mode === "warn" ? "Нужно внимание" : "Доверять рано";
  const tiles = h.parts.map((p) => { const t = mhTone(p.value / p.max); return `<button class="mh-tile" ${MH_TILE_GO[p.id]}><span class="mh-cap">${MH_PARTS[p.id]}</span><span class="mh-n ${t}">${Math.round(p.value)}<small> / ${p.max}</small></span><span class="mh-bar ${t}"><i style="width:${(p.value / p.max) * 100}%"></i></span><span class="muted">${esc(mhPartHint(h, p.id))}</span></button>`; }).join("");
  const mini = mhHeat(h, "");
  return `<div class="mh-top"><span class="mh-pill ${tone} lg"><i></i>${head}</span>
      <div class="mh-score ${tone} ${tone === "ok" ? "" : "mh-tone-" + tone}"><b>${h.total}</b><span class="muted"> / 100</span></div>
      <button class="ac-btn pri mh-push" data-mm="review" title="Запишет в журнал, что вы просмотрели память">Отметить осмотр · Touch ID</button></div>
    <div class="mh-bar ${tone} wide ${tone === "ok" ? "" : "mh-tone-" + tone}"><i style="width:${h.total}%"></i></div>
    <div class="mh-tiles">${tiles}</div>
    <h4>Что не так</h4>${mhReasonsHTML(h)}
    <div class="mh-side"><div class="mh-box"><h4>Режим доверия</h4><p>${mhModePill(h)}</p><p class="mh-eff ${tone}">${esc(mw)}: ${esc(MH_MODES[h.mode][2])}</p>
      <button class="mh-link" data-mm="hv" data-v="modes">что меняется на каждой ступени →</button></div>
    <div class="mh-box"><h4>Ритм · 13 недель</h4><p class="muted">Нажмите на день: откроется, что в тот день решали.</p><div class="mh-heat mini">${mini}</div>
      <button class="mh-link" data-mm="hv" data-v="rhythm">весь ритм →</button></div></div>`;
}
function mhRhythmHTML(d) {
  const h = d.health, hv = state.mm.hv, today = mhNum(h.today);
  const last = h.events.filter((e) => e.judgment).map((e) => e.day).sort().pop();
  const cur = hv.day && mhNum(hv.day) <= today && mhNum(hv.day) > today - 91 ? hv.day : last || h.today;
  const days = new Set(h.events.filter((e) => e.judgment).map((e) => e.day));
  const weeks = Array.from({ length: 13 }, (_, w) => Array.from({ length: 7 }, (_, k) => mhIso(today - 90 + w * 7 + k)).some((x) => days.has(x))).filter(Boolean).length;
  const ev = h.events.filter((e) => e.day === cur);
  const idleTone = h.idleDays < 0 || h.idleDays > 14 ? "bad" : h.idleDays > 7 ? "warn" : "ok";
  return `<p class="muted">Каждая клетка — день; чем ярче, тем больше решений человека (обслуживание перечня и индекса не в счет, оно бледнее). Здоровье равно регулярности.</p>
    <div class="mh-heat">${mhHeat(h, cur)}</div>
    <div class="mh-stats"><div><b>${weeks}</b> <span class="muted">из 13 недель с решениями</span></div>
      <div><b class="${idleTone}">${h.idleDays < 0 ? "—" : h.idleDays}</b> <span class="muted">${h.idleDays < 0 ? "решений в журнале нет" : "дн. с последнего решения"}</span></div></div>
    <div class="mh-box"><div class="mh-crumbs"><button class="mh-link" data-mm="hv" data-v="rhythm" data-day="">Ритм</button> › <b>${mhLabel(mhNum(cur))}</b></div>
      <h4>Решений в этот день: ${ev.length}</h4>
      ${ev.length ? ev.map((e) => mhEventHTML(h, e, false)).join("") : `<p class="muted">В этот день ничего не решали.${cur === h.today ? ` <button class="ac-btn" data-mm="review">Отметить осмотр сегодня · Touch ID</button>` : ""}</p>`}
      <p class="muted">Каждое решение остается в journal/decisions.md: что, когда и с каким текстом. <button class="mh-link" data-mm="sel" data-v="journal">весь журнал →</button></p></div>`;
}
function mhRulesHTML(d) {
  const h = d.health, hv = state.mm.hv;
  if (!h.rules.length) return `<p class="muted">В разделе «Правила работы» горячего слоя правил нет. Они появятся, когда вы одобрите предложение агента «в горячий слой».</p>`;
  const order = h.rules.map((r, i) => i).sort((a, b) => (h.rules[b].ageDays < 0 ? 9999 : h.rules[b].ageDays) - (h.rules[a].ageDays < 0 ? 9999 : h.rules[a].ageDays));
  const i = hv.rule != null && h.rules[hv.rule] ? hv.rule : order[0], sel = h.rules[i];
  const age = (r) => (r.ageDays < 0 ? "отметки нет" : `подтверждалось ${mhDays(r.ageDays)} назад`);
  const list = order.map((j) => { const r = h.rules[j], f = r.ageDays < 0 ? 0 : Math.max(0, 1 - r.ageDays / 60), t = f > 0.55 ? "ok" : f > 0.2 ? "warn" : "bad";
    return `<button class="mh-rule${j === i ? " sel" : ""}" data-mm="hv" data-v="rules" data-rule="${j}"><span>${esc(r.text)}<span class="muted">${age(r)}</span></span><b class="${r.stale ? "warn" : "ok"}">${r.stale ? "устарело" : "свежее"}</b><span class="mh-bar ${t}"><i style="width:${f * 100}%"></i></span></button>`; }).join("");
  const key = mhKey(sel.text), hist = h.events.filter((e) => (e.kind === "approve-hot" || e.kind === "confirm-rule") && mhKey(e.text) === key).slice().reverse();
  const seen = sel.stale ? `<span class="mh-badge bad">${sel.ageDays < 0 ? "не подтверждалось ни разу" : `проверено ${mhDays(sel.ageDays)} назад`}</span>` : `<span class="mh-badge ok">проверено ${mhDays(sel.ageDays)} назад</span>`;
  const agent = sel.stale ? "это правило агент получит в списке «давно не подтверждалось» в любом режиме" : "это правило агент получит без пометки: оно свежее";
  const stale = h.rules.filter((x) => x.stale).length;
  const bulk = !stale ? "" : state.mm.confirm === "*stale"
    ? `<div class="mm-confirm" role="alert"><b>Вы перечитали ${stale} ${mhWord(stale, "правило", "правила", "правил")} (${h.rules.filter((x) => x.stale).map((x) => "«" + esc(x.text) + "»").join(", ")}) и они верны?</b> <button class="ac-btn pri" data-mm="confirm-all">Подтвердить все · Touch ID</button> <button class="ac-btn" data-mm="confirm-all-no">Отмена</button></div>`
    : `<button class="ac-btn pri" data-mm="confirm-all-ask" title="Один Touch ID, запись в журнале по каждому правилу">Подтвердить все устаревшие (${stale})</button>`;
  return `<div class="mh-cols"><div><p class="muted">Здоровье у каждого правила свое: когда его в последний раз подтверждал человек (одобрение в горячий слой тоже считается).</p>${bulk}${list}</div>
    <div class="mh-stack"><div class="mh-box"><div class="mh-crumbs"><button class="mh-link" data-mm="hv" data-v="rules" data-rule="">Правила</button> › <b>правило ${i + 1}</b></div>
      <h3>${esc(sel.text)}</h3><p>${seen}</p>
      <button class="ac-btn pri" data-mm="confirm" data-i="${i}">Подтвердить сегодня · Touch ID</button>
      <h4>История решений по правилу</h4>${hist.map((e) => mhEventHTML(h, e, true, false)).join("") || `<p class="muted">Решений по этому правилу в журнале нет: правило было в HOT.md до учета.</p>`}</div>
    <div class="mh-box"><h4>Как это увидит агент</h4><p class="muted">${esc(agent)}.</p></div></div></div>`;
}
function mhModesHTML(d) {
  const h = d.health, rs = h.reasons;
  const col = (k, range, items) => `<div class="mh-lvl ${k}${h.mode === k ? " cur" : ""}"><b>${MH_MODES[k][0]}</b><div class="muted">${range}</div><ul>${items.map((x) => `<li>${x}</li>`).join("")}</ul></div>`;
  return `<p class="muted">Режим считается из здоровья (${h.total} из 100) и действует через MCP (<code>kbengine-mcp</code>): он виден агенту в каждом ответе памяти. Сейчас: <b class="${MH_MODES[h.mode][1]}">${MH_MODES[h.mode][0]}</b>.</p>
    <div class="mh-lvls">${col("ok", "здоровье 75 и выше", ["ответы без оговорок; правила без подтверждения все равно называются", "предложения идут в очередь"])}
      ${col("warn", "от 50 до 74", ["ответы несут пометку «давно не проверялась»", "и список правил без подтверждения > 30 дней", "предложения идут в очередь"])}
      ${col("bad", "ниже 50 или правка HOT.md мимо системы", ["ответы несут пометку «память не проверена»", "memory_propose отказывает: очередь приостановлена"])}</div>
    <div class="mh-eff ${MH_MODES[h.mode][1]}">Для агента сейчас: ${esc(MH_MODES[h.mode][2])}</div>
    <p class="muted"><b>Чего режим не закрывает.</b> Агент, который читает HOT.md прямо с диска (а не через MCP), пометок не увидит; это сигнал, а не замок. Он честен ровно в той мере, в какой агент спрашивает память через инструменты.</p>
    <div class="mh-cols"><div class="mh-box"><h4>Что вернет доверие</h4>${mhReasonsHTML(h)}</div>
    <div class="mh-box"><h4>Как считается здоровье</h4><table class="mh-formula"><tr><td>Давность осмотра</td><td>35, минус 1 за каждый день без решений человека</td></tr><tr><td>Очередь</td><td>25, минус 3 за предложение и 0,6 за день ожидания сверх недели</td></tr><tr><td>Целостность</td><td>12 за целую печать HOT.md и 8 за свежие векторы (−2 за отстающий)</td></tr><tr><td>Свежесть правил</td><td>20, средняя свежесть; правило устаревает за 60 дней, «устарело» после 30</td></tr></table>
      <p class="muted">Формула не откалибрована: веса выбраны, а не измерены. То же считает <code>kbengine memory health</code>.</p></div></div>`;
}
function mmHealthHTML(d) {
  const hv = state.mm.hv, view = MH_VIEWS.some(([id]) => id === hv.view) ? hv.view : "pulse";
  const nav = `<div class="mh-nav" role="tablist" aria-label="Здоровье памяти">${MH_VIEWS.map(([id, name]) => `<button role="tab" data-mm="hv" data-v="${id}" aria-selected="${id === view}">${name}</button>`).join("")}</div>`;
  const body = { pulse: mhPulseHTML, rhythm: mhRhythmHTML, rules: mhRulesHTML, modes: mhModesHTML }[view](d);
  return `<h3>Здоровье <span class="muted">· чем реже смотрите память, тем ниже доверие</span></h3>${nav}<div class="mh-body">${body}</div>`;
}
function mhJournalHTML(d) {
  const h = d.health, jf = state.mm.hv.jf;
  const chips = `<div class="mh-filter">${MH_FILTERS.map(([id, name]) => `<button data-mm="jf" data-v="${id}" aria-pressed="${id === jf}">${name}</button>`).join("")}</div>`;
  const all = h.events.slice().reverse().filter((e) => jf === "all" || (MH_KINDS[e.kind] || MH_KINDS.other)[1] === jf);
  const shown = all.slice(0, 60);
  if (!h.events.length) return `<h4>Журнал решений</h4><p class="muted">Пока пуст: сюда попадет каждое ваше одобрение, отклонение и подтверждение.</p>`;
  return `<h4>Журнал решений</h4><p class="muted">Что вы одобрили, отклонили и подтвердили; только дописывается.</p>${chips}
    ${shown.map((e) => mhEventHTML(h, e)).join("") || `<p class="muted">В этом разрезе решений нет.</p>`}
    ${all.length > shown.length ? `<p class="muted">Показаны последние ${shown.length} из ${all.length}; остальное в journal/decisions.md.</p>` : ""}`;
}
// mmDo runs one action of the screen: блокирует повторное нажатие, после — перечитывает память.
async function mmDo(fn, done) {
  const m = state.mm;
  if (m.busy) return;
  m.busy = true;
  try { const r = await fn(); if (done) toast(...done(r)); }
  catch (e) { toast("Память", " · " + errText(e), null, "bad"); }
  finally { m.busy = false; await mmLoad(); mmRender(); }
}
$("#mm-body").addEventListener("click", (e) => {
  const b = e.target.closest("[data-mm]");
  if (!b) return;
  const a = b.dataset.mm, name = b.dataset.name;
  if (a === "sel") { state.mm.sel = b.dataset.v; mmRender(); return; }
  if (a === "hv") { // переход внутри «Здоровья»: ритм → день → решение → правило
    const hv = state.mm.hv;
    state.mm.sel = "health"; hv.view = b.dataset.v;
    if ("day" in b.dataset) hv.day = b.dataset.day || null;
    if ("rule" in b.dataset) hv.rule = b.dataset.rule === "" ? null : +b.dataset.rule;
    mmRender(); return;
  }
  if (a === "jf") { state.mm.hv.jf = b.dataset.v; mmRender(); return; }
  const api = native();
  if (a === "init") mmDo(() => api.MemoryInit(), () => ["Память заведена", " · HOT.md, cold/, journal/"]);
  else if (a === "review") mmDo(() => api.MemoryReview(), () => ["Осмотр отмечен", " · запись в журнале решений"]);
  else if (a === "index") mmDo(() => api.MemoryIndex(), (rep) => ["Векторы пересобраны", ` · новых ${rep.embedded}, из индекса ${rep.reused}${rep.failed ? `, отказов ${rep.failed}: служба не смогла встроить запись` : ""}`]);
  else if (a === "confirm-all-ask") { state.mm.confirm = "*stale"; mmRender(); }
  else if (a === "confirm-all-no") { state.mm.confirm = ""; mmRender(); }
  else if (a === "confirm-all") {
    state.mm.confirm = "";
    const texts = state.mm.data.health.rules.filter((x) => x.stale).map((x) => x.text);
    mmDo(() => api.MemoryConfirmRules(texts), () => ["Правила подтверждены", ` · ${texts.length}, запись в журнале по каждому`]);
  }
  else if (a === "confirm") {
    const r = state.mm.data.health.rules[+b.dataset.i];
    if (r) mmDo(() => api.MemoryConfirmRule(r.text), () => ["Правило подтверждено", " · запись в журнале решений"]);
  }
  else if (a === "topics") mmDo(() => api.MemoryTopics(), (ch) => ["Перечень тем", ch ? " · обновлен" : " · уже актуален"]);
  else if (a === "seal") mmDo(() => api.MemorySeal(), () => ["Принято", " · текущий HOT.md подтвержден, запись в журнале решений"]);
  else if (a === "who") mmDo(() => api.MemoryWho(), (ch) => ["«Кто я»", ch ? " · обновлено из портрета, одобренные факты теперь в HOT.md" : " · уже актуально"]);
  else if (a === "reject") mmDo(() => api.MemoryReject(name), () => ["Отклонено", ` · ${name}, файл в journal/`]);
  else if (a === "approve-ask") { state.mm.confirm = name; mmRender(); }
  else if (a === "approve-no") { state.mm.confirm = ""; mmRender(); }
  else if (a === "approve-yes") {
    state.mm.confirm = "";
    mmDo(() => api.MemoryApprove(name, "hot"), () => ["Одобрено", ` · ${name} стало правилом в HOT.md`]);
  }
  else if (a === "approve") {
    const to = b.dataset.to;
    mmDo(() => api.MemoryApprove(name, to), () => ["Одобрено", to === "hot" ? ` · ${name} стало правилом в HOT.md` : ` · ${name} в холодном слое`]);
  }
});
$("#mm-body").addEventListener("submit", (e) => {
  const f = e.target.closest("[data-mm-form]");
  if (!f) return;
  e.preventDefault();
  const n = parseInt($("#mm-limit").value, 10);
  // Пустое поле дало бы NaN, до Go дошел бы 0 («умолчание»), и свой потолок молча сбросился бы.
  if (!Number.isFinite(n)) { toast("Потолок", " · укажите число знаков", null, "bad"); return; }
  const runes = state.mm.data ? state.mm.data.hot.runes : 0;
  if (n < runes) toast("Потолок ниже размера", ` · сейчас в HOT.md ${mmTokens(runes)} знаков, check покажет нарушение`, null, "off");
  mmDo(() => native().MemorySetHotLimit(n), () => ["Потолок сохранен", ` · ${mmTokens(n)} знаков`]);
});
