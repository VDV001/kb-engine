// Страница телефона DVK (#622, этап 4): привязка по QR, чаты, вопрос — в ящик
// kb-сервера, ответ окна — из ящика. Сервер видит только шифротексты: ключ
// приходит во фрагменте адреса QR и живет неизвлекаемым CryptoKey в IndexedDB.
//
// ponytail: без service worker — Chrome ставит страницу и так; push и работа
// без сети — следующим шагом (iOS шлет push только странице с экрана «Домой»).
import { importKey, seal, open } from "./crypto.js";

const POLL_MS = 10_000; // решение 15.11: короткий опрос раз в 10 с
const $ = (id) => document.getElementById(id);
const enc = new TextEncoder();
const dec = new TextDecoder();
const hex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");

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
const drop = (k) => kv("readwrite", (s) => s.delete(k));

let st = null; // { pair, sender, counter, after, seen, chats }
let current = ""; // открытый чат
let timer = 0;

async function persist() {
  await save("state", { ...st, counter: st.counter.toString() });
}

// pairFromFragment takes «#pair=<токен>.<ключ>» and wipes it from the address and history.
async function pairFromFragment() {
  const m = /^#pair=([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]{43})$/.exec(location.hash);
  if (!m) return false;
  history.replaceState(null, "", location.pathname); // ключ не остается в адресе и истории
  const k = await importKey(m[2]);
  const prev = (await load("state")) || {};
  st = {
    pair: { token: m[1], key: k.key, keyId: k.keyId },
    sender: crypto.getRandomValues(new Uint8Array(8)),
    counter: 0n,
    after: 0,
    seen: {},
    chats: prev.chats || {}, // история на телефоне переживает новую привязку
  };
  await persist();
  note("Телефон привязан. Задачи выполнит компьютер с открытым окном DVK.", false);
  return true;
}

async function boot() {
  try {
    if (!(await pairFromFragment())) {
      const s = await load("state");
      if (s?.pair) st = { ...s, counter: BigInt(s.counter || "0") };
    }
  } catch (e) {
    note("Код не подошел: " + e.message + ". Покажите новый код в окне DVK.");
  }
  show();
  if (st) poll();
}

// --- экраны
function show() {
  const chat = st && current ? st.chats[current] : null;
  $("unpaired").hidden = !!st;
  $("chats").hidden = !st || !!chat;
  $("chat").hidden = !chat;
  $("back").hidden = !chat;
  $("title").textContent = chat ? chat.title : "DVK";
  if (st && !chat) paintList();
  if (chat) paintChat(chat);
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

function paintChat(c) {
  const items = c.msgs.map((m) => {
    const li = document.createElement("li");
    li.className = m.who;
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
  scrollTo(0, document.body.scrollHeight);
}

function note(text, bad = true) {
  $("note").hidden = !text;
  $("note").textContent = text || "";
  $("note").style.color = bad ? "" : "var(--ok)";
}

// --- отправка
async function send(text) {
  const chat = current;
  st.counter += 1n; // счетчик — до отправки: повтор того же номера окно сочтет повтором
  await persist();
  const sealed = await seal(st.pair, { chat, from: "phone", sender: st.sender, counter: st.counter },
    enc.encode(JSON.stringify({ kind: "question", text })));
  const r = await api("/api/mail/" + chat, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sealed: b64(sealed) }) });
  if (!r.ok) throw new Error(await r.text());
  const c = st.chats[chat];
  c.msgs.push({ who: "me", text, at: Date.now() });
  c.pending = true;
  c.updated = Date.now();
  await persist();
}

const b64 = (b) => btoa(String.fromCharCode(...b));
const unb64 = (s) => Uint8Array.from(atob(s), (ch) => ch.charCodeAt(0));

async function api(path, init = {}) {
  const r = await fetch(path, { ...init, cache: "no-store", headers: { ...(init.headers || {}), "X-KB-Token": st.pair.token } });
  if (r.status === 401) {
    st = null;
    await drop("state");
    show();
    note("Телефон отвязан в окне DVK. Чтобы снова писать, привяжите его заново.");
    throw new Error("unpaired");
  }
  return r;
}

// --- опрос: новое из ящика по всем чатам
async function poll() {
  clearTimeout(timer);
  let wait = POLL_MS;
  try {
    for (let more = true; more && st;) {
      const r = await api("/api/mail?after=" + st.after);
      if (r.status === 429) { wait = 1000 * (Number(r.headers.get("Retry-After")) || 10); break; }
      if (r.status === 404) { note("На kb-сервере нет ящика для телефона: сервер старше чата с телефона."); break; }
      if (!r.ok) throw new Error("kb-сервер ответил " + r.status);
      const page = await r.json();
      for (const m of page.messages) await take(m);
      more = page.more && page.messages.length > 0;
      await persist();
    }
    if (st) note("");
  } catch (e) {
    if (e.message !== "unpaired") note("Нет связи с kb-сервером: " + e.message + ". Повторим через 10 с.");
  }
  if (st) { show(); timer = setTimeout(poll, wait); }
}

// take opens one message of the mailbox; чужое, поддельное и повторы — мимо.
async function take(m) {
  st.after = Math.max(st.after, m.seq);
  let o;
  try { o = await open(st.pair, m.chat, m.from, unb64(m.sealed)); } catch { return; }
  const who = hex(o.sender);
  if (m.from === "phone" && who === hex(st.sender)) return; // свое: уже в чате
  const key = m.chat + "|" + m.from + "|" + who;
  if (o.counter <= BigInt(st.seen[key] || "0")) return; // повтор: сервер мог прислать еще раз
  st.seen[key] = o.counter.toString();
  let p;
  try { p = JSON.parse(dec.decode(o.plain)); } catch { return; }
  const c = (st.chats[m.chat] ||= { title: "Чат", msgs: [], updated: 0 });
  if (m.from === "desk" && (p.kind === "answer" || p.kind === "error")) {
    c.msgs.push({ who: p.kind === "error" ? "error" : "desk", text: p.text, at: Date.parse(m.at) || Date.now() });
    c.pending = false;
    $("desk").textContent = "последний ответ " + new Date(Date.parse(m.at) || Date.now()).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
    $("desk").className = "desk on";
  } else if (m.from === "phone" && p.kind === "question") {
    if (c.title === "Чат") c.title = p.text.slice(0, 60);
    c.msgs.push({ who: "me", text: p.text, at: Date.parse(m.at) || Date.now() }); // с другого телефона
    c.pending = true;
  } else return;
  c.updated = Date.now();
}

// --- события
$("new-chat").addEventListener("click", () => {
  current = hex(crypto.getRandomValues(new Uint8Array(8)));
  st.chats[current] = { title: "Новый чат", msgs: [], updated: Date.now() };
  show();
  $("q").focus();
});
$("chat-list").addEventListener("click", (e) => {
  const b = e.target.closest("[data-chat]");
  if (b) { current = b.dataset.chat; show(); }
});
$("back").addEventListener("click", () => { current = ""; show(); });
$("ask").addEventListener("submit", async (e) => {
  e.preventDefault();
  const text = $("q").value.trim();
  if (!text || !current) return;
  const c = st.chats[current];
  if (!c.msgs.length) c.title = text.slice(0, 60);
  $("send").disabled = true;
  try {
    await send(text);
    $("q").value = "";
    note("");
  } catch (err) {
    if (err.message !== "unpaired") note("Не отправлено: " + err.message);
  }
  $("send").disabled = false;
  show();
});
document.addEventListener("visibilitychange", () => { if (!document.hidden && st) poll(); });

boot();
