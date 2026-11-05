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
function setIndexed(n, skipped, rec) {
  $("#indexed").textContent = `${n.toLocaleString("ru-RU")} файлов` + (skipped ? ` · ${skipped} не прочитано` : "") +
    (rec ? ` · распознавание ${rec.done.toLocaleString("ru-RU")} из ${rec.total.toLocaleString("ru-RU")}` : "");
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
    if (st && st.error) $("#indexed").textContent = `пересборка не удалась: ${st.error}`;
    else if (st && !st.building) setIndexed(st.indexed, st.skipped, st.recognizing);
    if (st && (st.building || st.recognizing)) next = 3000;
  }
  setTimeout(pollIndex, next);
}
if (native()) setTimeout(pollIndex, 3000);
// Список источников — ради цветов строки источников; панель его обновляет сама.
if (native()) native().Sources().then((v) => (state.known = v.sources || [])).catch(() => {});

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
// Вкладка строится от переданных записей и файлов: фильтр источника
// применяется ДО среза «Все» до пяти записей, иначе источник ниже пятой строки
// показал бы пустой список.
const TABS = {
  all: (kb, files) => [...kb.slice(0, 5).map(kbItem), ...files.map(fileItem)],
  kb: (kb) => kb.map(kbItem),
  doc: (_, files) => files.filter((f) => f.kind === "doc").map(fileItem),
  code: (_, files) => files.filter((f) => f.kind === "code").map(fileItem),
};
const kbItem = (e) => ({ type: "kb", e });
const fileItem = (f) => ({ type: "file", f });

$("#tabs").addEventListener("click", (e) => {
  const b = e.target.closest("button");
  if (!b) return;
  state.tab = b.dataset.tab;
  state.sel = 0;
  render();
});
function moveInk() {
  const b = $(`#tabs button[data-tab="${state.tab}"]`), ink = $(".tab-ink");
  document.querySelectorAll("#tabs button").forEach((x) => x.classList.toggle("active", x === b));
  ink.style.left = b.offsetLeft + "px";
  ink.style.width = b.offsetWidth + "px";
}

// --- Отрисовка -----------------------------------------------------------------
function render() {
  // Фильтр источника оставляет только его записи: у базы знаний — записи,
  // у папки — ее файлы.
  const only = state.filter;
  const kb = only ? state.kb.filter((e) => e._src === only) : state.kb;
  const files = only ? state.files.filter((f) => f._src === only) : state.files;
  // «Все» показывает только пять записей базы — счетчик обязан совпадать со списком.
  const counts = { all: Math.min(kb.length, 5) + files.length, kb: kb.length, doc: 0, code: 0 };
  for (const f of files) if (counts[f.kind] !== undefined) counts[f.kind]++;
  document.querySelectorAll("#tabs button").forEach((b) => (b.querySelector("i").textContent = state.q ? counts[b.dataset.tab] : ""));
  moveInk();

  // «Показано» у источника — сколько его записей в текущей вкладке до фильтра:
  // клик по одному источнику не обнуляет счет остальных.
  state.items = TABS[state.tab](kb, files);
  const shown = {};
  const unfiltered = state.filter ? TABS[state.tab](state.kb, state.files) : state.items;
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
    const order = Object.keys(TABS), k = order.indexOf(state.tab);
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
const wakeMark = () => {
  if (matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  brand.classList.remove("wake");
  void brand.offsetWidth; // перезапуск анимации, если окно открыли повторно до ее конца
  brand.classList.add("wake");
};
$(".mark-k").addEventListener("animationend", () => brand.classList.remove("wake"));
// Наведение на идущий цикл его не перезапускает: рывок назад и есть моргание.
brand.addEventListener("mouseenter", () => { if (!brand.classList.contains("wake")) wakeMark(); });
if (window.runtime) window.runtime.EventsOn("kb:show", () => { $("#q").focus(); $("#q").select(); wakeMark(); });
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
const KIND = { folder: "папка", catalog: "каталог KB", server: "kb-сервер" };
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

const target = (s) => (s.kind === "server" ? s.url : s.path) || "";
const tilde = (p) => p.replace(/^\/Users\/[^/]+/, "~");
const label = (s) => s.name || (s.kind === "server" ? hostOf(s.url) : target(s).split("/").filter(Boolean).pop()) || target(s);
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
  strip.innerHTML = `<span class="strip-lbl">источники</span>` + state.sources.map((s) => {
    const name = esc(label(s)), c = `style="--c:var(${hue.get(s.id)})"`;
    if (s.error) return `<span class="chip off" ${c} title="${esc(s.error)}"><span class="chip-dot"></span>${name}<span class="cnt">${esc(s.error.split(":")[0])}</span></span>`;
    return `<button class="chip" ${c} data-src="${esc(s.id)}" aria-pressed="${state.filter === s.id}" title="${esc(target(s))}"><span class="chip-dot"></span>${name}<span class="cnt"><b>${fmt(shown[s.id] || 0)}</b> из ${fmt(s.found)}</span></button>`;
  }).join("") + (state.dupes > 0 ? `<span class="strip-sum">склеено дублей: ${fmt(state.dupes)}</span>` : "");
}
$("#strip").addEventListener("click", (e) => {
  const b = e.target.closest("button.chip");
  if (!b) return;
  state.filter = state.filter === b.dataset.src ? null : b.dataset.src;
  state.sel = 0;
  render();
});
const payload = () => ({
  sources: draft.sources.map(({ id, kind, name, path, url, enabled }) => ({ id, kind, name: name || "", path: path || "", url: url || "", enabled })),
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
const dirty = () => JSON.stringify(payload()) !== draft.saved || Object.keys(liveTokens()).length > 0;
const byKey = (k) => draft.sources.find((s) => s._key === k);

function load(v) {
  state.known = v.sources || [];
  draft.sources = (v.sources || []).map((s) => ({ ...s, _key: "k" + ++keySeq, _orig: s.kind === "server" ? s.url : s.path, _was: s.enabled }));
  draft.exclude = [...(v.exclude || [])];
  draft.tokens = {};
  draft.saved = JSON.stringify(payload());
}

async function openSettings() {
  scrim.hidden = false;
  panel.classList.add("open");
  panel.setAttribute("aria-hidden", "false");
  showError("");
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
      showError("Не удалось прочитать настройки: " + e);
      return;
    }
  }
  const list = $("#sources");
  list.classList.add("enter");
  renderSources();
  setTimeout(() => list.classList.remove("enter"), 500);
  $("#close-settings").focus();
  loadServerStates();
}
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
  panel.classList.remove("open");
  panel.setAttribute("aria-hidden", "true");
  scrim.hidden = true;
  ui.editing = ui.replacing = ui.confirmDel = null;
  if (dirty()) toast("Не сохранено", " · черновик ждет в панели, ⌘,", null, "off");
  $("#q").focus();
}
const settingsOpen = () => panel.classList.contains("open");

function stateOf(s) {
  if (!s.enabled) return `<span class="state off">выключен</span>`;
  // Новый, перенацеленный или только что включенный: состояние узнается после сохранения.
  if (!s._orig || s._orig !== target(s) || !s._was) return `<span class="state new">после сохранения</span>`;
  const note = s.note ? ` title="${esc(s.note)}"` : "";
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
  const what = s.kind === "server" ? "адрес" : "путь";
  const pick = s.kind === "folder" && native() ? `<button class="btn" data-pick="${s._key}">Выбрать…</button>` : "";
  if (ui.editing === s._key)
    return `<div class="form">
      <div class="field"><label for="en-${s._key}">имя</label><input id="en-${s._key}" value="${esc(s.name || "")}" placeholder="${esc(label(s))}"></div>
      <div class="field"><label for="ea-${s._key}">${what}</label><input id="ea-${s._key}" value="${esc(target(s))}" spellcheck="false">${pick}</div>
      ${s.kind === "server" ? tokenField(`et-${s._key}`, s.hasToken || draft.tokens[s.id]) + checkBar(s._key) : ""}
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
const tokenField = (id, saved) =>
  `<div class="field"><label for="${id}">токен</label><input id="${id}" type="password" autocomplete="off" spellcheck="false"
    placeholder="${saved ? "сохранен в Связке ключей — пусто, чтобы оставить" : "токен kbengine serve --token-file"}"></div>`;
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
          <span class="handle" draggable="true" aria-hidden="true" title="Перетащите, чтобы сменить приоритет">⠿</span>
          <span class="prio">${i + 1}</span>
          <div class="who"><div class="name"><i class="swatch"></i><strong>${esc(label(s))}</strong><span class="kind">${esc(KIND[s.kind] || s.kind)}</span></div>
            <div class="addr" title="${esc(target(s))}">${esc(tilde(target(s)))}</div></div>
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
      <span class="hint">сетевая папка через VPN — это тоже путь в /Volumes</span>`;
  else if (ui.addKind === "catalog")
    form = `<div class="row"><input id="na" placeholder="~/…/knowledge-base/_data/catalog.json" spellcheck="false"><button class="btn" data-addok>Добавить</button></div>
      <span class="hint">читается первый включенный каталог списка</span>`;
  else if (ui.addKind === "server")
    form = `<div class="field"><label for="na">адрес</label><input id="na" placeholder="https://kb.example.ru" spellcheck="false"></div>
      ${tokenField("nt", false)}${checkBar("new")}
      <div class="row"><button class="btn primary" data-addok>Подключить</button>
      <span class="hint">http — только для этого Mac; токен уйдет в Связку ключей macOS, не в config.json</span></div>`;
  $("#add").innerHTML = `<div class="row"><span class="muted">+ источник</span><span class="seg" role="group" aria-label="Вид источника">${kinds}</span></div>${form}`;

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
  if (s.kind === "server") s.url = v; else s.path = v;
}
const focusSoon = (sel) => requestAnimationFrame(() => $(sel)?.focus());

panel.addEventListener("click", async (e) => {
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
      verdict = { key: d.check, state: "bad", text: String(err).replace(/^Error: /, "") };
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
    ui.verdict = null;
    const undo = change(() => { s.name = name; setTarget(s, v); if (tok) draft.tokens[s.id] = { url: v, token: tok }; });
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
    try { p = await native().PickFolder(); } catch (err) { return showError(String(err)); }
    if (!p) return;
    const input = d.pick === "new" ? $("#na") : $(`#ea-${d.pick}`) || $(`#ra-${d.pick}`);
    if (input) input.value = p;
    if (d.pick === "new") $("[data-addok]").click();
    return;
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
    setTarget(s, v);
    if (kind === "server") {
      ui.adding = true;
      // id выдает Go сразу: к нему привязан токен в Связке ключей.
      try { s.id = await native().NewSourceID("server"); }
      catch (err) { ui.adding = false; return showError("Не удалось добавить сервер: " + err); }
      ui.adding = false;
      if (ui.addKind !== "server") return; // форму сменили, пока ждали
      ui.verdict = null;
    }
    const undo = change(() => { draft.sources.push(s); if (tok) draft.tokens[s.id] = { url: v, token: tok }; });
    ui.addKind = null;
    renderSources();
    return toast("Добавлен", ` · «${label(s)}», ${draft.sources.length}-й по приоритету`, undo);
  }
  if (d.unex !== undefined) { draft.exclude.splice(+d.unex, 1); return renderSources(); }
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

$("#open-settings").onclick = openSettings;
$("#close-settings").onclick = closeSettings;
scrim.onclick = closeSettings;
$("#add-exclude").onclick = () => {
  const v = $("#exclude-input").value.trim();
  if (v && !draft.exclude.includes(v)) draft.exclude.push(v);
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
    showError(String(e));
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
    showError("Не удалось узнать статус индекса: " + e);
    if (state.q) search(state.q); // сохранение прошло — выдача по новым источникам
    renderSources();
  } finally {
    setBusy(false); // панель не остается запертой ни при каком исходе
  }
};

// Esc закрывает панель раньше, чем очищает поиск; ⌘, открывает ее, как в любом приложении macOS.
document.addEventListener("keydown", (e) => {
  if (settingsOpen() && e.key === "Escape") { e.stopImmediatePropagation(); closeSettings(); }
  else if (e.metaKey && e.key === ",") { e.preventDefault(); e.stopImmediatePropagation(); openSettings(); }
}, true);

function hideIndexingScreen() {
  const screen = $("#indexing");
  if (screen.hidden) return;
  screen.classList.add("leave");
  setTimeout(() => { screen.hidden = true; screen.classList.remove("leave"); }, 500);
}
$("#indexing-settings").onclick = openSettings;
