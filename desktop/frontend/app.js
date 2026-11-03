// Интерфейс kb-desktop. Чистый JS без сборки: ходит только в HTTP API движка
// (/api/search — база знаний, /api/files/* — файлы), поэтому одинаково работает
// в окне, в браузере и против удаленного сервера.
"use strict";

const $ = (s) => document.querySelector(s);
const api = (path) => fetch(path).then(async (r) => ({ status: r.status, body: r.ok ? await r.json() : null }));

const state = { q: "", tab: "all", kb: [], files: [], items: [], sel: 0, seq: 0 };

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
function highlighter(q) {
  const stems = q.toLowerCase().replace(/ё/g, "е").split(/[^\p{L}\p{N}]+/u).filter((w) => [...w].length >= 2).map(stem);
  if (!stems.length) return esc;
  const re = new RegExp(`(${stems.map(reEsc).join("|")})[\\p{L}\\p{N}]*`, "giu");
  return (text) => esc(text).replace(re, (m) => `<mark>${m}</mark>`);
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
      $(".indexing-main .muted").textContent = why.trim() || "Причина неизвестна";
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
function setIndexed(n, skipped) {
  $("#indexed").textContent = `${n.toLocaleString("ru-RU")} файлов` + (skipped ? ` · ${skipped} не прочитано` : "");
}

// --- Поиск ---------------------------------------------------------------------
let timer;
$("#q").addEventListener("input", (e) => {
  clearTimeout(timer);
  timer = setTimeout(() => search(e.target.value.trim()), 110);
});

async function search(q) {
  state.q = q;
  const seq = ++state.seq;
  // HEAD на ветке — текущий запрос, как текущий шаг на сайте.
  $("#branch-label").textContent = q ? `find «${q.length > 40 ? q.slice(0, 40) + "…" : q}»` : "поиск";
  if (!q) { state.kb = []; state.files = []; render(); showEmpty(); return; }
  const enc = encodeURIComponent(q);
  $("#app").classList.add("busy");
  const [kb, files] = await Promise.all([
    api(`/api/search?q=${enc}`).catch(() => ({ body: null })),
    api(`/api/files/search?q=${enc}&limit=60`).catch(() => ({ body: null })),
  ]);
  if (seq !== state.seq) return; // пришел ответ на устаревший запрос
  $("#app").classList.remove("busy");
  state.kb = (kb.body || []).slice(0, 40);
  state.files = (files.body && files.body.hits) || [];
  state.sel = 0;
  render();
}

// --- Вкладки -------------------------------------------------------------------
const TABS = {
  all: () => [...state.kb.slice(0, 5).map(kbItem), ...state.files.map(fileItem)],
  kb: () => state.kb.map(kbItem),
  doc: () => state.files.filter((f) => f.kind === "doc").map(fileItem),
  code: () => state.files.filter((f) => f.kind === "code").map(fileItem),
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
  // «Все» показывает только пять записей базы — счетчик обязан совпадать со списком.
  const counts = { all: Math.min(state.kb.length, 5) + state.files.length, kb: state.kb.length, doc: 0, code: 0 };
  for (const f of state.files) if (counts[f.kind] !== undefined) counts[f.kind]++;
  document.querySelectorAll("#tabs button").forEach((b) => (b.querySelector("i").textContent = state.q ? counts[b.dataset.tab] : ""));
  moveInk();

  state.items = TABS[state.tab]();
  $("#count").textContent = state.q ? `${state.items.length}` : "";
  const hl = highlighter(state.q);
  const list = $("#results");
  if (state.q && !state.items.length) {
    list.innerHTML = `<li class="nothing">Ничего не нашлось</li>`;
    showEmpty();
    return;
  }
  list.innerHTML = state.items.map((it, i) => (it.type === "kb" ? kbRow(it.e, i, hl) : fileRow(it.f, i, hl))).join("");
  if (state.items.length) select(state.sel);
}

// Строка журнала, как таблица на сайте: тип своим цветом (как роли «Я» и
// «Агент») · имя · когда, ниже строка совпадения.
const TYPE_LABEL = { kb: "база", doc: "док", code: "код" };
function kbRow(e, i, hl) {
  const verdict = e.verdict ? `<span class="verdict ${e.verdict === "keep" ? "keep" : ""}">${esc(e.verdict)}</span>` : "";
  return `<li class="hit" data-i="${i}" style="--i:${Math.min(i, 14)}">
    <div class="hit-head"><span class="type kb">${TYPE_LABEL.kb}</span><span class="hit-name">${hl(e.title)}</span>${verdict}</div>
    ${e.description ? `<div class="hit-snip">${hl(e.description)}</div>` : ""}
  </li>`;
}
function fileRow(f, i, hl) {
  const t = TYPE_LABEL[f.kind] ? f.kind : "doc";
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
  const seq = ++previewSeq, hl = highlighter(state.q), box = $("#preview");
  if (it.type === "kb") {
    const e = it.e;
    box.innerHTML = `<div class="doc">
      <div class="doc-head"><div class="prompt"><span class="dollar">$</span> kb show ${e.id}</div><div class="doc-title">${hl(e.title)}</div>
        <div class="doc-path">${esc([e.category, e.verdict, e.author, e.date_added].filter(Boolean).join(" · "))}</div>
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
    <pre class="doc-text">${hl(text.slice(0, 200000))}</pre></div>`;
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
    <p class="muted keys-line"><b>↑↓</b> выбор <b>↵</b> открыть <b>⌘↵</b> в Finder <b>⌘,</b> папки <b>esc</b> очистить</p></div>`;
}

// --- Клавиатура ----------------------------------------------------------------
document.addEventListener("keydown", (e) => {
  if (settingsOpen()) return; // клавиши поиска не трогают панель папок
  if (e.key === "ArrowDown") { e.preventDefault(); select(state.sel + 1); }
  else if (e.key === "ArrowUp") { e.preventDefault(); select(state.sel - 1); }
  else if (e.key === "Enter") { e.preventDefault(); activate(state.items[state.sel], e.metaKey); }
  else if (e.key === "Escape") { $("#q").value = ""; search(""); }
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
moveInk();
waitForIndex();

// --- Папки для поиска ----------------------------------------------------------
// Меняются только в окне, через привязки Wails: у HTTP API такой ручки нет
// намеренно. В браузере (dev-режим) панель объясняет это и ничего не меняет.
const panel = $("#settings"), scrim = $("#scrim");
const draft = { roots: [], exclude: [] };

async function openSettings() {
  scrim.hidden = false;
  panel.classList.add("open");
  panel.setAttribute("aria-hidden", "false");
  showError("");
  const editable = !!native();
  for (const id of ["#pick", "#add-root", "#add-exclude", "#save-settings", "#root-input", "#exclude-input"]) $(id).disabled = !editable;
  if (!editable) {
    draft.roots = []; draft.exclude = [];
    renderFolders();
    $("#settings-note").textContent = "Папки меняются в окне приложения, не в браузере.";
    return;
  }
  let v;
  try {
    v = await native().Folders();
  } catch (e) {
    showError("Не удалось прочитать настройки: " + e);
    return;
  }
  draft.roots = [...(v.roots || [])];
  draft.exclude = [...(v.exclude || [])];
  draft.missing = [...(v.missing || [])];
  $("#settings-note").textContent = "";
  renderFolders();
}
function closeSettings() {
  panel.classList.remove("open");
  panel.setAttribute("aria-hidden", "true");
  scrim.hidden = true;
  $("#q").focus();
}
const settingsOpen = () => panel.classList.contains("open");

function renderFolders() {
  const row = (text, i, list) =>
    `<li><span class="path" title="${esc(text)}">${esc(text.replace(/^\/Users\/[^/]+/, "~"))}</span><button data-list="${list}" data-i="${i}" aria-label="Убрать">✕</button></li>`;
  // Папки из конфига, которых нет или к которым нет доступа: не индексируются
  // и при сохранении уйдут из списка — говорим об этом заранее.
  const missing = (draft.missing || []).map((m) =>
    `<li class="empty-row" title="Не индексируется и уйдет из списка при сохранении">${esc(m.replace(/^\/Users\/[^/]+/, "~"))}</li>`).join("");
  $("#roots").innerHTML = (draft.roots.length
    ? draft.roots.map((r, i) => row(r, i, "roots")).join("")
    : `<li class="empty-row">Ни одной папки — добавьте хотя бы одну</li>`) + missing;
  $("#excludes").innerHTML = draft.exclude.map((r, i) => row(r, i, "exclude")).join("");
}
function showError(msg) {
  const el = $("#settings-error");
  el.textContent = msg;
  el.hidden = !msg;
}
function addTo(list, value) {
  value = (value || "").trim();
  if (value && !draft[list].includes(value)) draft[list].push(value);
  renderFolders();
}

$("#open-settings").onclick = openSettings;
$("#close-settings").onclick = closeSettings;
scrim.onclick = closeSettings;
$("#pick").onclick = async () => {
  try { addTo("roots", await native().PickFolder()); } catch (e) { showError(String(e)); }
};
$("#add-root").onclick = () => { addTo("roots", $("#root-input").value); $("#root-input").value = ""; };
$("#add-exclude").onclick = () => { addTo("exclude", $("#exclude-input").value); $("#exclude-input").value = ""; };
$("#root-input").addEventListener("keydown", (e) => e.key === "Enter" && $("#add-root").click());
$("#exclude-input").addEventListener("keydown", (e) => e.key === "Enter" && $("#add-exclude").click());
panel.addEventListener("click", (e) => {
  const b = e.target.closest("button[data-list]");
  if (!b) return;
  draft[b.dataset.list].splice(+b.dataset.i, 1);
  renderFolders();
});

$("#save-settings").onclick = async () => {
  showError("");
  const btn = $("#save-settings");
  btn.disabled = true;
  try {
    // Проверку делает Go: папка обязана существовать, иначе отказ с ее именем
    // и без изменений — ни конфига, ни индекса.
    await native().SetFolders(draft.roots, draft.exclude);
  } catch (e) {
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
        else {
          $("#settings-note").textContent = `Готово: ${st.indexed.toLocaleString("ru-RU")} файлов`;
          draft.missing = [];
          renderFolders();
          hideIndexingScreen(); // если первая сборка падала, ее экран больше не нужен
        }
        setIndexed(st.indexed, st.skipped);
        if (state.q) search(state.q);
        break;
      }
      await sleep(400);
    }
  } catch (e) {
    showError("Не удалось узнать статус индекса: " + e);
  } finally {
    btn.disabled = false; // кнопка не остается выключенной ни при каком исходе
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
