// Страница телефона DVK (#622, этап 4): привязка по QR, чаты, вопрос — в ящик
// kb-сервера, ответ окна — из ящика. Сервер видит только шифротексты: ключ
// приходит во фрагменте адреса QR и живет неизвлекаемым CryptoKey в IndexedDB.
// Логика без DOM и сети — state.js (проверяется тестом в node).
//
// ponytail: без service worker — Chrome ставит страницу и так; push и работа
// без сети — следующим шагом (iOS шлет push только странице с экрана «Домой»).
import { importKey, seal, open } from "./crypto.js";
import { repair, take, mergeCounter, b64, unb64 } from "./state.js";

const POLL_MS = 10_000; // решение 15.11: короткий опрос раз в 10 с
const UNAUTH_MS = 60_000; // 401: реже, но не сдаемся — сервер мог ошибиться ненадолго
const API = "../api/mail"; // относительно /m/: работает и под префиксом пути kb-сервера
const $ = (id) => document.getElementById(id);
const enc = new TextEncoder();

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

let st = null; // { pair, sender, counter, after, seen, chats, outbox }
let current = ""; // открытый чат
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
  if (st) poll();
}

// --- экраны; follow — прокрутить чат вниз (новое сообщение или человек и так внизу)
function show(follow) {
  const chat = st && current ? st.chats[current] : null;
  $("unpaired").hidden = !!st;
  $("chats").hidden = !st || !!chat;
  $("chat").hidden = !chat;
  $("back").hidden = !chat;
  $("title").textContent = chat ? chat.title : "DVK";
  if (st && !chat) paintList();
  if (chat) paintChat(chat, follow);
}

function paintList() {
  const ids = Object.keys(st.chats).sort((a, b) => st.chats[b].updated - st.chats[a].updated);
  $("chats-empty").hidden = ids.length > 0;
  $("chat-list").replaceChildren(...ids.map((id) => {
    const c = st.chats[id], last = c.msgs[c.msgs.length - 1];
    const li = document.createElement("li"), b = document.createElement("button"), t = document.createElement("span"), s = document.createElement("small");
    b.type = "button";
    b.dataset.chat = id;
    t.textContent = c.title;
    s.textContent = c.pending ? "ждет компьютер…" : last ? last.text : "";
    b.append(t, s);
    li.append(b);
    return li;
  }));
}

function paintChat(c, follow) {
  const atBottom = innerHeight + scrollY >= document.body.scrollHeight - 40;
  const items = c.msgs.map((m) => {
    const li = document.createElement("li");
    li.className = m.who + (m.who === "me" && m.sent === false ? " unsent" : "");
    li.textContent = m.text;
    return li;
  });
  if (c.pending) {
    const li = document.createElement("li");
    li.className = "desk wait";
    li.textContent = "Ждем компьютер: ответ придет, когда окно DVK открыто.";
    items.push(li);
  }
  $("msgs").replaceChildren(...items);
  if (follow || atBottom) scrollTo(0, document.body.scrollHeight); // читающего выше не дергаем
}

function note(text, bad = true) {
  $("note").hidden = !text;
  $("note").textContent = text || "";
  $("note").style.color = bad ? "" : "var(--ok)";
}

async function api(path, init = {}) {
  return fetch(path, { ...init, cache: "no-store", headers: { ...(init.headers || {}), "X-KB-Token": st.pair.token } });
}

// --- отправка: вопрос запечатан один раз и лежит в исходящих; повтор — те же
// байты (сервер склеит дубль), свое из ящика отметит вопрос доставленным.
async function ask(text) {
  const chat = current;
  let item;
  await locked(async () => {
    const stored = await load("state");
    st.counter = mergeCounter(st.counter, stored?.counter) + 1n; // счетчик — до отправки
    const sealed = await seal(st.pair, { chat, from: "phone", sender: st.sender, counter: st.counter },
      enc.encode(JSON.stringify({ kind: "question", text })));
    item = { chat, sealed: b64(sealed) };
    st.outbox.push(item);
    const c = st.chats[chat];
    c.msgs.push({ who: "me", text, at: Date.now(), counter: st.counter.toString(), sent: false });
    c.pending = true;
    c.updated = Date.now();
    await save("state", { ...st, counter: st.counter.toString() });
  });
  await flush();
}

// flush posts the outbox in order; неотправленное остается до следующего опроса.
async function flush() {
  while (st?.outbox.length) {
    const o = st.outbox[0];
    const r = await api(API + "/" + o.chat, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sealed: o.sealed }) });
    if (r.status === 401) throw unauth();
    if (r.status === 400 || r.status === 413) { // отказ насовсем: не затыкать исходящие
      st.outbox.shift();
      note("Сервер не принял вопрос: " + (await r.text()));
      continue;
    }
    if (!r.ok) throw new Error("kb-сервер ответил " + r.status);
    st.outbox.shift();
    await persist();
  }
}

function unauth() {
  const e = new Error("unauth");
  e.unauth = true;
  return e;
}

// --- опрос: исходящие, потом новое из ящика по всем чатам
async function poll() {
  clearTimeout(timer);
  let wait = POLL_MS, changed = false;
  note(""); // подсказки этого круга выставит сам круг
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
        if (take(st, m, o)) {
          changed = true;
          if (m.from === "desk") lastAnswer(m.at);
        }
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
  if (changed) show(false);
  if (st) timer = setTimeout(poll, wait);
}

function lastAnswer(at) {
  const t = new Date(Date.parse(at) || Date.now()).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
  $("desk").textContent = "последний ответ " + t;
  $("desk").className = "desk on";
}

// --- события
$("new-chat").addEventListener("click", () => {
  current = Array.from(crypto.getRandomValues(new Uint8Array(8)), (x) => x.toString(16).padStart(2, "0")).join("");
  st.chats[current] = { title: "Новый чат", msgs: [], updated: Date.now() };
  show(true);
  $("q").focus();
});
$("chat-list").addEventListener("click", (e) => {
  const b = e.target.closest("[data-chat]");
  if (b) { current = b.dataset.chat; show(true); }
});
$("back").addEventListener("click", () => { current = ""; show(true); });
$("ask").addEventListener("submit", async (e) => {
  e.preventDefault();
  const text = $("q").value.trim();
  if (!text || !current) return;
  const c = st.chats[current];
  if (!c.msgs.length) c.title = text.slice(0, 60);
  $("send").disabled = true;
  try {
    await ask(text);
    $("q").value = "";
    note("");
  } catch (err) {
    note(err.unauth ? "kb-сервер не пускает телефон: вопрос сохранен и уйдет, когда пустит." : "Пока не отправлено: " + err.message + ". Уйдет со следующим опросом.");
    $("q").value = "";
  }
  $("send").disabled = false;
  show(true);
});
document.addEventListener("visibilitychange", () => { if (!document.hidden && st) poll(); });
// QR открыт, когда страница уже открыта: браузер меняет только фрагмент, без
// загрузки — привязку разбираем здесь же, иначе ключ остался бы в адресе.
addEventListener("hashchange", async () => {
  try {
    if (await pairFromFragment()) { show(true); poll(); }
  } catch (e) {
    note("Код не подошел: " + e.message + ". Покажите новый код в окне DVK.");
  }
});

boot();
