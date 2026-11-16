// Страница телефона DVK (#622): привязка по QR, чаты, поиск по базе и файлам
// компьютера, ответ с метками «взято». Вопрос и запрос поиска — в ящик
// kb-сервера, ответ окна — из ящика. Сервер видит только шифротексты: ключ
// приходит во фрагменте адреса QR и живет неизвлекаемым CryptoKey в IndexedDB.
// Логика без DOM и сети — state.js (проверяется тестом в node). Вид — макет
// outputs/2026-11-15_dvk-devices-mockup.html (решения владельца 15.11).
//
// Service worker (sw.js) — только push: «компьютер ответил», когда страница
// закрыта. Работы без сети нет — страница всегда свежая с сервера.
import { importKey, seal, open } from "./crypto.js";
import { repair, take, mergeCounter, b64, unb64, searchChat, asleep, single, rebinds, pushView, sameKey } from "./state.js";

const POLL_MS = 10_000; // решение 15.11: короткий опрос раз в 10 с
const UNAUTH_MS = 60_000; // 401: реже, но не сдаемся — сервер мог ошибиться ненадолго
const ONLINE_MS = 10 * 60_000; // последний ответ окна моложе — «на связи»
const API = "../api/mail"; // относительно /m/: работает и под префиксом пути kb-сервера
const $ = (id) => document.getElementById(id);
const enc = new TextEncoder();
const hex = (b) => Array.from(b || [], (x) => x.toString(16).padStart(2, "0")).join("");
const hhmm = (t) => new Date(t).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
const wide = matchMedia("(min-width: 900px)");

// --- хранилище: IndexedDB, одна таблица ключ → значение (CryptoKey хранится как есть).
const db = new Promise((resolve, reject) => {
  const r = indexedDB.open("dvk-phone", 1);
  r.onupgradeneeded = () => r.result.createObjectStore("kv");
  r.onsuccess = () => resolve(r.result);
  r.onerror = () => reject(r.error);
});
async function kv(mode, fn) {
  const d = await db;
  return new Promise((resolve, reject) => {
    const tx = d.transaction("kv", mode);
    const req = fn(tx.objectStore("kv"));
    tx.oncomplete = () => resolve(req?.result);
    tx.onerror = () => reject(tx.error);
  });
}
const load = (k) => kv("readonly", (s) => s.get(k));
const save = (k, v) => kv("readwrite", (s) => s.put(v, k));
// locked runs fn alone across tabs: экран «Домой» и вкладка браузера делят
// одно состояние (ревью #636). Без Web Locks — как есть.
const locked = (fn) => (navigator.locks ? navigator.locks.request("dvk-phone-state", fn) : fn());

let st = null; // { pair, sender, counter, after, seen, chats, outbox, lastDesk, search, doc }
const ui = { tab: "list", view: "", chat: "", kind: "" }; // view: chat | doc | desk | ""
let timer = 0;

// persist writes the state; счетчик — наибольший из своего и записанного
// другой вкладкой, иначе вкладка с устаревшим счетчиком откатит его.
async function persist() {
  await locked(async () => {
    const stored = await load("state");
    st.counter = mergeCounter(st.counter, stored?.counter);
    await save("state", { ...st, counter: st.counter.toString() });
  });
}

// pairFromFragment takes «#pair=<токен>.<ключ>» and wipes it from the address and history.
async function pairFromFragment() {
  const m = /^#pair=([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)$/.exec(location.hash);
  if (!m) return false;
  history.replaceState(null, "", location.pathname); // ключ не остается в адресе и истории
  const k = await importKey(m[2]);
  const prev = await load("state");
  // Ссылку с чужим ключом может прислать кто угодно: без «да» привязка прежняя.
  if (rebinds(prev, k.keyId) && !confirm("Привязать телефон к другому компьютеру? Дальше вопросы и поиск уйдут туда, а не на компьютер, к которому телефон привязан сейчас.")) {
    note("Привязка не изменилась.", false);
    return false;
  }
  st = repair(prev && { ...prev, counter: BigInt(prev.counter || "0") }, { token: m[1], key: k.key, keyId: k.keyId }, crypto.getRandomValues(new Uint8Array(8)));
  await persist();
  note("Телефон привязан. Задачи выполнит компьютер с открытым окном DVK.", false);
  return true;
}

async function boot() {
  try {
    await pairFromFragment();
  } catch (e) {
    note("Код не подошел: " + e.message + ". Покажите новый код в окне DVK."); // прежняя привязка ниже — цела
  }
  if (!st) {
    try {
      const s = await load("state");
      if (s?.pair) st = { outbox: [], ...s, counter: BigInt(s.counter || "0") };
    } catch (e) {
      note("Хранилище браузера недоступно: " + e.message);
    }
  }
  show(true);
  if (st) { poll(); pushSync(); }
}

// --- уведомления (#622) ------------------------------------------------------

const pushSupported = "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
const isIOS = /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
const standalone = matchMedia("(display-mode: standalone)").matches || navigator.standalone === true;
const swReady = pushSupported ? navigator.serviceWorker.register("sw.js").then(() => navigator.serviceWorker.ready) : null;
let pushKey = null; // открытый ключ VAPID сервера; "" — на сервере push выключен

async function serverPushKey() {
  if (pushKey !== null) return pushKey;
  const r = await api(API + "/push/key");
  if (r.status === 404) return (pushKey = "");
  if (!r.ok) throw new Error("сервер ответил " + r.status);
  return (pushKey = (await r.json()).key);
}

const unb64url = (t) => unb64(t.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (t.length % 4)) % 4));

async function putSubscription(sub) {
  const j = sub.toJSON();
  const r = await api(API + "/push", { method: "PUT", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint: j.endpoint, keys: { p256dh: j.keys.p256dh, auth: j.keys.auth } }) });
  if (!r.ok) throw new Error("сервер не принял подписку: " + r.status);
}

// pushSync paints the card and resends a live subscription: адрес подписки
// браузер может сменить, а сервер узнает новый только от телефона.
async function pushSync() {
  let view;
  try {
    const key = await serverPushKey(), serverOn = key !== "";
    let sub = pushSupported && serverOn ? await (await swReady).pushManager.getSubscription() : null;
    // Подписка другим ключом (перепривязка, новый ключ сервера) мертва: сервисы
    // push отклоняют ее молча. Снимаем — карточка предложит включить заново.
    if (sub && !sameKey(sub.options.applicationServerKey, unb64url(key))) {
      await sub.unsubscribe();
      sub = null;
    }
    if (sub && Notification.permission === "granted") await putSubscription(sub);
    view = pushView({ serverOn, supported: pushSupported, ios: isIOS, standalone, permission: pushSupported ? Notification.permission : "default", subscribed: !!sub });
  } catch (e) {
    view = { text: "Не удалось узнать: " + e.message, action: null };
  }
  $("push-text").textContent = view.text;
  $("push-btn").hidden = !view.action;
  $("push-btn").dataset.action = view.action || "";
  $("push-btn").textContent = view.action === "off" ? "Выключить уведомления" : "Включить уведомления";
  $("push-btn").className = "btn wide" + (view.action === "on" ? " primary" : "");
}

// Разрешение браузер спрашивает только по нажатию — iOS иначе отказывает молча.
$("push-btn").addEventListener("click", async () => {
  const btn = $("push-btn");
  btn.disabled = true;
  try {
    const reg = await swReady;
    if (btn.dataset.action === "on") {
      if ((await Notification.requestPermission()) !== "granted") throw new Error("разрешение не дано");
      const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: unb64url(await serverPushKey()) });
      await putSubscription(sub);
      note("Уведомления включены", false);
    } else {
      await (await reg.pushManager.getSubscription())?.unsubscribe();
      await api(API + "/push", { method: "DELETE" });
      note("Уведомления выключены", false);
    }
  } catch (e) {
    note("Уведомления: " + e.message);
  }
  btn.disabled = false;
  pushSync();
});

// --- экраны -----------------------------------------------------------------

// show paints everything from st and ui; follow — прокрутить чат вниз.
function show(follow) {
  $("unpaired").hidden = !!st;
  $("shell").hidden = !st;
  if (!st) return;
  const chat = ui.view === "chat" ? st.chats[ui.chat] : null;
  if (ui.view === "chat" && !chat) ui.view = "";
  // Телефон — один экран: список или содержимое; планшет — оба (style.css).
  $("shell").dataset.screen = ui.view ? "main" : "side";
  document.body.dataset.view = ui.view; // плашка ошибки над полем ввода чата (style.css)
  $("list-pane").hidden = ui.tab !== "list";
  $("search-pane").hidden = ui.tab !== "search";
  for (const b of document.querySelectorAll(".tabs [data-tab]")) {
    if (b.dataset.tab === ui.tab) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current");
  }
  for (const id of ["chat", "doc", "desk-view"]) $(id).hidden = true;
  $("empty-main").hidden = !!ui.view;
  $("title").textContent = "DVK";
  paintDesk();
  if (ui.tab === "list") paintList(); else paintSearch();
  if (ui.view === "chat") { $("chat").hidden = false; $("title").textContent = chat.title; paintChat(chat, follow); }
  if (ui.view === "doc") { $("doc").hidden = false; paintDoc(); }
  if (ui.view === "desk") { $("desk-view").hidden = false; $("title").textContent = "Компьютер"; }
}

function row(cls, parts) {
  const li = document.createElement("li"), b = document.createElement("button");
  b.type = "button";
  for (const [k, v] of Object.entries(cls)) b.dataset[k] = v;
  for (const [c, text] of parts) {
    const s = document.createElement("span");
    s.className = c;
    s.textContent = text;
    b.append(s);
  }
  li.append(b);
  return li;
}

function paintList() {
  const ids = Object.keys(st.chats).sort((a, b) => st.chats[b].updated - st.chats[a].updated);
  $("chats-empty").hidden = ids.length > 0;
  $("chat-list").replaceChildren(...ids.map((id) => {
    const c = st.chats[id], last = c.msgs[c.msgs.length - 1];
    const li = row({ chat: id }, [["t", c.title], ["tm", last ? when(last.at) : ""], [c.pending ? "s wait" : "s", c.pending ? "ждет компьютер…" : last ? last.text : ""]]);
    if (ui.view === "chat" && ui.chat === id) li.firstChild.setAttribute("aria-current", "true");
    return li;
  }));
}

function when(t) {
  if (!t) return "";
  const d = new Date(t), now = new Date();
  if (d.toDateString() === now.toDateString()) return hhmm(t);
  return d.toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit" });
}

// kindOf — группа строки выдачи для чипов: записи базы и файлы.
const kindOf = (h) => (h.kind === "запись" ? "kb" : "file");
const KINDS = [["", "Все"], ["kb", "База"], ["file", "Файлы"]];

function paintSearch() {
  const s = st.search || { q: "", hits: [] };
  if (document.activeElement !== $("fq")) $("fq").value = s.q || "";
  const hits = s.hits || [];
  const counts = { "": hits.length, kb: 0, file: 0 };
  for (const h of hits) counts[kindOf(h)]++;
  $("kinds").hidden = !hits.length;
  $("kinds").replaceChildren(...KINDS.filter(([k]) => !k || counts[k]).map(([k, name]) => {
    const b = document.createElement("button");
    b.type = "button";
    b.dataset.kind = k;
    b.setAttribute("aria-pressed", String(ui.kind === k));
    b.textContent = name + " " + counts[k];
    return b;
  }));
  const shown = hits.filter((h) => !ui.kind || kindOf(h) === ui.kind);
  $("hits").replaceChildren(...shown.map((h) => {
    const li = row({ ref: h.ref }, [[kindOf(h) === "kb" ? "ty kb" : "ty", h.kind || ""], ["t", h.title], ["s", [h.snippet, h.where].filter(Boolean).join(" · ")]]);
    if (ui.view === "doc" && st.doc?.ref === h.ref) li.firstChild.setAttribute("aria-current", "true");
    return li;
  }));
  $("find-note").textContent = s.error ? "Компьютер не нашел: " + s.error
    : s.pending ? "Ищет компьютер…"
    : s.q && !hits.length ? "Ничего не нашлось по «" + s.q + "»."
    : s.q ? "Нашел компьютер · файл открывается текстом с компьютера, пароли «Доступов» сюда не попадают."
    : "Ищет компьютер: база знаний, kb-серверы и папки окна DVK. Пароли «Доступов» сюда не попадают.";
}

// paintText draws an answer: строки «- …» — список, **…** — жирное; только
// textContent, без разметки из ответа.
function paintText(el, text) {
  let list = null;
  for (const line of text.split("\n")) {
    const item = /^\s*[-•]\s+(.*)$/.exec(line);
    if (item) {
      if (!list) { list = document.createElement("ul"); el.append(list); }
      const li = document.createElement("li");
      inline(li, item[1]);
      list.append(li);
      continue;
    }
    list = null;
    if (!line.trim()) continue;
    const p = document.createElement("p");
    inline(p, line);
    el.append(p);
  }
}

function inline(el, text) {
  text.split(/(\*\*[^*]+\*\*)/).forEach((part) => {
    if (/^\*\*[^*]+\*\*$/.test(part)) {
      const b = document.createElement("b");
      b.textContent = part.slice(2, -2);
      el.append(b);
    } else if (part) {
      el.append(document.createTextNode(part));
    }
  });
}

const CHIP = { connection: "", docs: "docs", web: "web", doc: "doc" };

function chips(sources) {
  const d = document.createElement("div");
  d.className = "chips";
  const k = document.createElement("span");
  k.className = "k";
  k.textContent = "взято";
  d.append(k);
  for (const s of sources) {
    const c = document.createElement("span");
    c.className = "chip " + (CHIP[s.kind] ?? "");
    c.textContent = s.name;
    d.append(c);
  }
  return d;
}

function paintChat(c, follow) {
  const box = $("chat");
  const atBottom = box.scrollTop + box.clientHeight >= box.scrollHeight - 40;
  const items = c.msgs.map((m) => {
    const li = document.createElement("li");
    if (m.who === "desk") {
      li.className = "desk";
      paintText(li, m.text);
      if (m.sources?.length) li.append(chips(m.sources));
    } else {
      li.className = m.who + (m.who === "me" && m.sent === false ? " unsent" : "");
      li.textContent = m.text;
      if (m.who === "me" && m.sent === false) {
        const meta = document.createElement("span");
        meta.className = "meta";
        meta.textContent = "в очереди · уйдет при связи";
        li.append(meta);
      }
    }
    return li;
  });
  if (c.pending) {
    const li = document.createElement("li"), sp = document.createElement("span");
    li.className = "wait";
    sp.className = "spin";
    li.append(sp, document.createTextNode("Компьютер отвечает: ответ придет, пока окно DVK открыто"));
    if (typeof c.step === "string") { // ход модели подписью окна
      const step = document.createElement("div");
      step.className = "step";
      step.textContent = c.step;
      li.append(step);
    }
    items.push(li);
  }
  $("msgs").replaceChildren(...items);
  const since = asleep(st, Date.now());
  $("asleep").hidden = !since;
  if (since) {
    const b = document.createElement("b");
    b.textContent = "Компьютер не отвечает" + (since ? " с " + hhmm(since) : "");
    const s = document.createElement("span");
    s.textContent = "Вопрос ждет в ящике и уйдет в работу, когда окно DVK на компьютере откроется. Страницу можно закрыть.";
    $("asleep").replaceChildren(b, s);
  }
  if (follow || atBottom) box.scrollTop = box.scrollHeight; // читающего выше не дергаем
}

function paintDoc() {
  const d = st.doc, opening = st.search?.opening;
  $("title").textContent = opening ? "Открывает…" : d?.title || "Документ";
  $("doc-where").textContent = opening ? "Компьютер читает документ…" : d?.where ? "лежит в «" + d.where + "» на компьютере" : "";
  const err = !opening && d?.error;
  $("doc-text").textContent = opening ? "" : err ? "Компьютер не открыл документ: " + err : d?.text || "";
  $("doc-ask").hidden = !!opening || !d || !!err;
}

function paintDesk() {
  const since = asleep(st, Date.now()), last = st.lastDesk || 0;
  for (const pc of [$("desk"), $("desk-main")]) {
    pc.className = "pc" + (since ? " off" : last && Date.now() - last < ONLINE_MS ? " on" : "");
    pc.textContent = since ? "спит с " + hhmm(since) : last && Date.now() - last < ONLINE_MS ? "на связи" : last ? "ответ в " + when(last) : "компьютер";
  }
  $("desk-main").hidden = ui.view === "desk";
  $("kv-link").textContent = since ? "не отвечает с " + hhmm(since) : last ? "последний ответ " + when(last) + " " + hhmm(last) : "ответов еще не было";
  $("kv-link").className = since ? "off" : last && Date.now() - last < ONLINE_MS ? "ok" : "";
  $("kv-server").textContent = location.host;
  $("kv-key").textContent = "id " + hex(st.pair.keyId).slice(0, 8) + " · только здесь и на компьютере";
}

let toastTimer = 0;
function note(text, bad = true) {
  clearTimeout(toastTimer);
  $("note").hidden = !text;
  $("note").textContent = text || "";
  $("note").className = "toast" + (bad ? "" : " ok");
  if (text && !bad) toastTimer = setTimeout(() => { $("note").hidden = true; }, 4000);
}

async function api(path, init = {}) {
  return fetch(path, { ...init, cache: "no-store", headers: { ...(init.headers || {}), "X-KB-Token": st.pair.token } });
}

// --- отправка: запрос запечатан один раз и лежит в исходящих; повтор — те же
// байты (сервер склеит дубль), свое из ящика отметит вопрос доставленным.
async function send(chat, payload, mark) {
  await locked(async () => {
    const stored = await load("state");
    st.counter = mergeCounter(st.counter, stored?.counter) + 1n; // счетчик — до отправки
    const sealed = await seal(st.pair, { chat, from: "phone", sender: st.sender, counter: st.counter }, enc.encode(JSON.stringify(payload)));
    st.outbox.push({ chat, sealed: b64(sealed) });
    mark?.(st.counter.toString());
    await save("state", { ...st, counter: st.counter.toString() });
  });
  await flush();
}

async function ask(text) {
  const c = st.chats[ui.chat];
  await send(ui.chat, { kind: "question", text }, (counter) => {
    c.msgs.push({ who: "me", text, at: Date.now(), counter, sent: false });
    c.pending = true;
    c.updated = Date.now();
  });
}

// flush posts the outbox in order; неотправленное остается до следующего опроса.
// По одному: опрос и отправка не шлют и не снимают один запрос дважды (ревью #636).
const flush = single(async () => {
  while (st?.outbox.length) {
    const o = st.outbox[0];
    const r = await api(API + "/" + o.chat, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sealed: o.sealed }) });
    if (r.status === 401) throw unauth();
    if (r.status === 400 || r.status === 413) { // отказ насовсем: не затыкать исходящие
      st.outbox.shift();
      note("Сервер не принял запрос: " + (await r.text()));
      continue;
    }
    if (!r.ok) throw new Error("kb-сервер ответил " + r.status);
    st.outbox.shift();
    await persist();
  }
});

function unauth() {
  const e = new Error("unauth");
  e.unauth = true;
  return e;
}

// --- опрос: исходящие, потом новое из ящика по всем чатам
// По одному: опрос во время опроса (вернулись на вкладку) — еще круг после, а
// не вторая цепочка таймеров.
const poll = single(async () => {
  clearTimeout(timer);
  let wait = POLL_MS, changed = false;
  try {
    await flush();
    for (let more = true; more && st;) {
      const r = await api(API + "?after=" + st.after);
      if (r.status === 401) throw unauth();
      if (r.status === 429) { wait = 1000 * (Number(r.headers.get("Retry-After")) || 10); break; }
      if (r.status === 404) { note("На kb-сервере нет ящика для телефона: сервер старше чата с телефона."); break; }
      if (!r.ok) throw new Error("kb-сервер ответил " + r.status);
      const page = await r.json();
      const before = st.after;
      for (const m of page.messages) {
        let o = null;
        try { o = await open(st.pair, m.chat, m.from, unb64(m.sealed)); } catch { /* чужое или поддельное */ }
        if (take(st, m, o)) changed = true;
      }
      if (st.after !== before) await persist();
      more = page.more && page.messages.length > 0;
    }
  } catch (e) {
    if (e.unauth) {
      wait = UNAUTH_MS; // ключ и история целы: отвязали — привяжите заново, сбой — пройдет сам
      note("kb-сервер не пускает телефон. Если его отвязали в окне DVK — привяжите заново; иначе повторим через минуту.");
    } else {
      note("Нет связи с kb-сервером: " + e.message + ". Повторим через 10 с.");
    }
  }
  show(false); // и без новостей: «спит» и «на связи» зависят от часов
  if (changed && ui.view === "chat") show(true);
  // Скрытая вкладка не опрашивает и не пишет: с ней делит состояние вкладка на
  // экране (экран «Домой» и браузер), вернувшись, она перечитает его.
  if (st && !document.hidden) timer = setTimeout(poll, wait);
});

// --- события
function openChat(id) {
  ui.chat = id;
  ui.view = "chat";
  show(true);
}

$("new-chat").addEventListener("click", () => {
  const id = hex(crypto.getRandomValues(new Uint8Array(8)));
  st.chats[id] = { title: "Новый чат", msgs: [], updated: Date.now() };
  persist();
  openChat(id);
  $("q").focus();
});
$("chat-list").addEventListener("click", (e) => {
  const b = e.target.closest("[data-chat]");
  if (b) openChat(b.dataset.chat);
});
document.querySelector(".tabs").addEventListener("click", (e) => {
  const b = e.target.closest("[data-tab]");
  if (!b) return;
  ui.tab = b.dataset.tab;
  show(false);
  if (ui.tab === "search" && !st.search?.q) $("fq").focus();
});
$("back").addEventListener("click", () => {
  ui.view = "";
  show(false);
});
for (const id of ["desk", "desk-main"]) {
  $(id).addEventListener("click", () => {
    ui.view = "desk";
    show(false);
  });
}
$("q").addEventListener("input", () => { // поле растет с текстом до 40% экрана
  $("q").style.height = "auto";
  $("q").style.height = $("q").scrollHeight + "px";
});
$("ask").addEventListener("submit", async (e) => {
  e.preventDefault();
  const text = $("q").value.trim();
  if (!text || !ui.chat) return;
  const c = st.chats[ui.chat];
  if (!c.msgs.length) c.title = text.slice(0, 60);
  $("send").disabled = true;
  $("q").value = "";
  $("q").style.height = "";
  try {
    await ask(text);
    note("");
  } catch (err) {
    note(err.unauth ? "kb-сервер не пускает телефон: вопрос сохранен и уйдет, когда пустит." : "Пока не отправлено: " + err.message + ". Уйдет со следующим опросом.");
  }
  $("send").disabled = false;
  show(true);
});
$("q").addEventListener("keydown", (e) => { // Enter на компьютерной клавиатуре планшета — отправить, Shift+Enter — строка
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing && wide.matches) { e.preventDefault(); $("ask").requestSubmit(); }
});
$("find").addEventListener("submit", async (e) => {
  e.preventDefault();
  const q = $("fq").value.trim();
  if (!q) return;
  $("fq").blur();
  st.search = { q, hits: [], pending: true };
  ui.kind = "";
  show(false);
  try {
    await send(searchChat(st), { kind: "search", text: q });
  } catch (err) {
    note(err.unauth ? "kb-сервер не пускает телефон: поиск уйдет, когда пустит." : "Поиск уйдет со следующим опросом: " + err.message);
  }
});
$("kinds").addEventListener("click", (e) => {
  const b = e.target.closest("[data-kind]");
  if (b) { ui.kind = b.dataset.kind; show(false); }
});
$("hits").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-ref]");
  if (!b) return;
  const h = st.search.hits.find((x) => x.ref === b.dataset.ref);
  st.search.opening = b.dataset.ref;
  st.doc = { title: h?.title || "", text: "", ref: b.dataset.ref, where: h?.where || "" };
  ui.view = "doc";
  show(false);
  try {
    await send(searchChat(st), { kind: "open", text: b.dataset.ref });
  } catch (err) {
    note("Документ запросится со следующим опросом: " + err.message);
  }
});
$("doc-ask").addEventListener("click", () => {
  const id = hex(crypto.getRandomValues(new Uint8Array(8)));
  st.chats[id] = { title: st.doc.title, msgs: [], updated: Date.now() };
  persist();
  ui.tab = "list";
  openChat(id);
  $("q").value = "По документу «" + st.doc.title + "»: ";
  $("q").focus();
});
let unpairArmed = 0;
$("unpair").addEventListener("click", async () => {
  if (Date.now() - unpairArmed > 4000) { // второе касание за 4 с — подтверждение
    unpairArmed = Date.now();
    $("unpair").textContent = "Нажмите еще раз: ключ и чаты сотрутся с телефона";
    return;
  }
  clearTimeout(timer);
  try { // забытый телефон не должен звенеть: подписку — с сервера и из браузера
    await (await swReady)?.pushManager.getSubscription().then((s) => s?.unsubscribe());
    await api(API + "/push", { method: "DELETE" });
  } catch { /* привязку стираем все равно */ }
  await kv("readwrite", (s) => s.delete("state"));
  st = null;
  location.reload();
});
wide.addEventListener("change", () => show(false));
// ponytail: «пишет только вкладка на экране» — две вкладки рядом (планшет,
// два окна) все еще затирают друг другу чаты. Путь: слияние чатов и
// исходящих при persist, как счетчика.
document.addEventListener("visibilitychange", async () => {
  if (document.hidden || !st) return;
  try {
    const s = await load("state"); // другая вкладка могла писать, пока эта была скрыта
    if (s?.pair) st = { outbox: [], ...s, counter: mergeCounter(st.counter, s.counter) };
  } catch (e) {
    note("Хранилище браузера недоступно: " + e.message);
  }
  show(false);
  poll();
});
// QR открыт, когда страница уже открыта: браузер меняет только фрагмент, без
// загрузки — привязку разбираем здесь же, иначе ключ остался бы в адресе.
addEventListener("hashchange", async () => {
  try {
    if (await pairFromFragment()) { show(true); poll(); pushSync(); }
  } catch (e) {
    note("Код не подошел: " + e.message + ". Покажите новый код в окне DVK.");
  }
});

boot();
