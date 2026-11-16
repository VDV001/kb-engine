// Логика страницы телефона без DOM и сети (#622): привязка, разбор ящика,
// base64 — проверяется тестом phonepage в node (ревью #636).

const hex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");

// repair returns the state after scanning a QR. Тот же ключ (телефон привязан
// снова, ключ у сервера прежний) — номер ящика, счетчик, отправитель и seen
// прежние: иначе весь ящик проигрался бы поверх истории. Новый ключ — заново,
// но история чатов остается на телефоне.
export function repair(prev, pair, newSender) {
  if (prev?.pair && sameKey(prev.pair.keyId, pair.keyId)) {
    return { ...prev, pair, counter: BigInt(prev.counter ?? 0), outbox: prev.outbox || [] };
  }
  return { pair, sender: newSender, counter: 0n, after: 0, seen: {}, chats: prev?.chats || {}, outbox: [] };
}

// rebinds tells whether pairing with keyId moves an already paired phone to
// another key: такую привязку человек подтверждает сам — ссылку с чужим ключом
// может прислать кто угодно (ревью безопасности #636).
export function rebinds(prev, keyId) {
  return !!prev?.pair && !sameKey(prev.pair.keyId, keyId);
}

// take applies one message of the mailbox; o — открытое (null — чужое,
// поддельное). Номер ящика двигается всегда; ответ — «чат изменился,
// перерисовать».
export function take(st, m, o) {
  st.after = Math.max(st.after, m.seq);
  if (!o) return false;
  const who = hex(o.sender);
  const key = m.chat + "|" + m.from + "|" + who;
  if (o.counter <= BigInt(st.seen[key] || "0")) return false; // повтор: сервер мог прислать еще раз
  st.seen[key] = o.counter.toString();
  let p;
  try { p = JSON.parse(new TextDecoder().decode(o.plain)); } catch { return false; }
  const at = Date.parse(m.at) || Date.now();
  if (m.from === "desk") st.lastDesk = Math.max(st.lastDesk || 0, at);
  if (m.chat === searchChat(st)) return found(st, m, p);
  if (m.chat.startsWith("search-")) return false; // поиск другого телефона того же ключа
  if (m.from === "desk" && (p.kind === "remember" || p.kind === "memory")) return remembered(st.chats[m.chat], p);
  if (m.from === "desk" && p.kind === "accepted") { // окно взяло вопрос (#646): показ, не сообщение
    const c = st.chats[m.chat];
    if (!c?.pending) return false; // запоздавшее после ответа
    c.accepted = true;
    return true;
  }
  if (m.from === "desk" && p.kind === "step") { // ход модели: показ, не сообщение (#622)
    const c = st.chats[m.chat];
    if (!c?.pending || !p.text) return false; // запоздавший шаг после ответа
    c.step = p.text; // подпись — как в окне, ее строит окно
    return true;
  }
  const answer = m.from === "desk" && (p.kind === "answer" || p.kind === "error");
  if (!answer && !(m.from === "phone" && p.kind === "question")) return false; // чат не заводится
  const c = (st.chats[m.chat] ||= { title: "Чат", msgs: [], updated: 0 });
  if (answer) {
    c.msgs.push({ who: p.kind === "error" ? "error" : "desk", text: p.text, at, sources: p.sources || [] });
    c.pending = false;
    delete c.step;
    delete c.accepted;
    // Без счета провайдера — без круга, как в окне; сбой хода круг не трогает:
    // в окне он ничего не сохраняет и прежний счет остается (ревью #654).
    if (p.context?.used > 0) c.context = { used: p.context.used, window: p.context.window || 0 };
    else if (p.kind === "answer") delete c.context;
  } else {
    const mine = who === hex(st.sender);
    const sent = mine && c.msgs.find((x) => x.who === "me" && x.counter === o.counter.toString());
    if (sent) {
      sent.sent = true; // свой вопрос дошел: в чате он уже есть
    } else {
      if (!c.msgs.length) c.title = p.text.slice(0, 60);
      c.msgs.push({ who: "me", text: p.text, at, sent: true }); // с другого телефона человека
      c.pending = true;
    }
  }
  c.updated = Date.now();
  return true;
}

// waitView is what the waiting question of chat c shows (#646).
// В очереди → отправлено → компьютер принял → шаг хода; ответ снимает ожидание.
// silent — окно молчит дольше asleep: принятый вопрос оно уже не переспросит.
export function waitView(c, silent = false) {
  if (c.accepted && silent) return { text: "Компьютер принял вопрос и замолчал: ответа может не быть — задайте его еще раз", step: "" };
  if (c.accepted) return { text: "Компьютер принял · отвечает", step: typeof c.step === "string" ? c.step : "" };
  const q = [...c.msgs].reverse().find((x) => x.who === "me");
  return { text: q && q.sent === false ? "Ждет отправки" : "Отправлено · ждет компьютер", step: "" };
}

// Пороги — как CTX_HINT_AT и CTX_WARN_AT окна (desktop/frontend/app.js).
const CTX_HINT_AT = 0.5, CTX_WARN_AT = 0.8;

// ctxView is the context ring of a chat (#647): x — заполнение из ответа,
// talked — в чате уже был ответ. Счета нет: после разговора — «—», как в окне,
// до него — круга нет. over — запрос уже не помещается в окно модели.
//
// ponytail: заполнение — снимок последнего ответа: сжатие чата и новый размер
// окна модели в окне дойдут до телефона только со следующим ответом. Путь —
// состояние окна в ящике (#647, выбор модели и источников).
export function ctxView(x, talked = false) {
  if (!(x?.used > 0)) return talked ? { text: "—", tone: "ok", fill: 0, unknown: true } : null;
  if (!x.window) return { text: "?", tone: "ok", fill: 0 };
  const f = x.used / x.window;
  return { text: Math.round(f * 100) + "%", tone: f >= CTX_WARN_AT ? "bad" : f >= CTX_HINT_AT ? "warn" : "ok", fill: Math.min(1, f), over: f >= 1 };
}

// asked starts a new question in chat c (#646): его ожидание — с нуля.
export function asked(c, text, counter, at) {
  c.msgs.push({ who: "me", text, at, counter, sent: false });
  c.pending = true;
  c.updated = at;
  delete c.accepted; // «принял» и шаг — прежнего вопроса (ревью #653)
  delete c.step;
}

// posted marks the question of outbox item o sent: сервер его принял, ждать
// своего эха из ящика (до опроса) незачем (ревью #653). Старые исходящие —
// без счетчика: их отметит эхо.
export function posted(st, o) {
  if (!o.counter) return;
  const m = st.chats[o.chat]?.msgs.find((x) => x.who === "me" && x.counter === o.counter);
  if (m) m.sent = true;
}

// remembered applies «Запомнить о Вас?» of the desk to chat c (#647):
// предложение — карточки без дублей; итог — снять или оставить со словами.
// Чат, которого телефон не знает, — мимо: пустой чат из одних карточек не нужен.
function remembered(c, p) {
  if (!c) return false;
  const facts = p.facts || [];
  if (p.kind === "remember") {
    c.remember ||= [];
    for (const f of facts) if (f.id && !c.remember.some((x) => x.id === f.id)) c.remember.push({ id: f.id, title: f.title || "", text: f.text || "" });
    return facts.length > 0;
  }
  const f = c.remember?.find((x) => x.id === facts[0]?.id);
  if (!f) return false;
  if (p.text === "saved" || p.text === "skipped" || p.text === "gone") c.remember = c.remember.filter((x) => x !== f); // gone — решено или забыто в окне
  else Object.assign(f, { busy: false, err: p.text || "окно не приняло решение" });
  return true;
}

// hideFact drops the card of fact id on the phone: окно отказало, а карточка
// не нужна (ревью #655).
export function hideFact(c, id) {
  if (c.remember) c.remember = c.remember.filter((x) => x.id !== id);
}

// deciding marks fact id of chat c sent for a decision (#647).
export function deciding(c, id) {
  const f = c.remember?.find((x) => x.id === id);
  if (f) Object.assign(f, { busy: true, err: "" });
}

// questionPayload is the question of chat c with its choice (#647).
// Не выбрано — как настроено в окне: поля не уходят вовсе.
export function questionPayload(c, text) {
  const p = { kind: "question", text };
  if (c.model) p.model = c.model;
  if (Array.isArray(c.only)) p.only = c.only;
  return p;
}

// searchChat is the mailbox chat of this phone's searches: выдача и документы
// идут им и в список чатов не попадают. От отправителя — повторная привязка
// тем же ключом сохраняет его.
export function searchChat(st) {
  return "search-" + hex(st.sender || []);
}

// deskChat is the mailbox chat of the window's state for this phone (#647).
export function deskChat(st) {
  return searchChat(st);
}

// pickSources turns the checked sources of a chat into its choice.
export function pickSources(all, on) {
  return { ok: true, only: on.length === all.length ? undefined : on };
}

// found applies the desk's answer to a search: выдача, документ или отказ.
// Выдача старого запроса не подменяет новый.
function found(st, m, p) {
  if (m.from !== "desk") return false; // свой запрос: на экране он уже есть
  const s = (st.search ||= { q: "", hits: [] });
  if (p.kind === "desk") { // модель и источники окна для поля ввода (#647)
    st.desk = p.desk || null;
    st.deskAt = Date.now();
    st.deskAsked = 0;
    delete st.deskErr;
    return true;
  }
  if (p.kind === "error" && st.deskAsked && !s.pending && !s.opening) { // отказ на desk, а не на поиск
    st.deskAsked = 0;
    st.deskErr = p.text;
    return true;
  }
  if (p.kind === "results") {
    if (p.text !== s.q) return false;
    Object.assign(s, { hits: p.hits || [], pending: false, error: "" });
  } else if (p.kind === "doc") {
    const h = (p.hits || [])[0] || {};
    st.doc = { title: p.title || h.title || "", text: p.text || "", ref: h.ref || "", where: h.where || "" };
    s.opening = "";
  } else if (p.kind === "error" && s.opening && st.doc) { // отказ открыть — на экране документа
    st.doc.error = p.text;
    s.opening = "";
  } else if (p.kind === "error") {
    Object.assign(s, { pending: false, error: p.text });
  } else {
    return false;
  }
  return true;
}

// sleepAfter — столько вопрос ждет, прежде чем телефон скажет «компьютер спит»:
// окно забирает ящик раз в 10 с, ход модели — еще десятки секунд.
const sleepAfter = 45_000;

// asleep returns since when the desk is silent, if the newest waiting question
// waits longer than sleepAfter without a word from it; 0 — не спит или ждать
// нечего. Самый свежий, а не самый старый: старый вопрос, оставшийся без
// ответа, иначе прятал бы сон навсегда — любой позднейший ответ «перекрыл» бы его.
export function asleep(st, now) {
  let newest = 0;
  for (const c of Object.values(st.chats || {})) {
    if (!c.pending) continue;
    const q = [...c.msgs].reverse().find((x) => x.who === "me");
    if (q && q.at > newest) newest = q.at;
  }
  if (!newest || now - newest < sleepAfter || (st.lastDesk || 0) > newest) return 0;
  return st.lastDesk || newest;
}

// single runs fn one at a time: вызов во время прогона — еще один прогон
// после, а не второй параллельно (ревью #636: два flush снимали чужой запрос).
export function single(fn) {
  let run = null, again = false;
  return () => {
    if (run) { again = true; return run; }
    run = (async () => {
      try { do { again = false; await fn(); } while (again); } finally { run = null; }
    })();
    return run;
  };
}

// mergeCounter takes the larger of this tab's counter and the stored one:
// две вкладки (экран «Домой» и браузер) не откатывают счетчик друг другу.
export function mergeCounter(mine, stored) {
  const s = BigInt(stored ?? 0);
  return mine > s ? mine : s;
}

// b64 encodes in chunks: String.fromCharCode(...64 КБ) упирается в предел
// числа аргументов (JavaScriptCore на iOS).
export function b64(bytes) {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  return btoa(s);
}

export function unb64(text) {
  return Uint8Array.from(atob(text), (ch) => ch.charCodeAt(0));
}

// pushView is the notifications card: текст и кнопка (on, off или null).
// iOS дает push только странице с экрана «Домой» (с 16.4); разрешение браузер
// спрашивает только по нажатию — поэтому кнопка, а не запрос при открытии.
export function pushView(o) {
  if (!o.serverOn) return { text: "На этом kb-сервере уведомления не включены.", action: null };
  if (!o.supported) {
    return o.ios && !o.standalone
      ? { text: "На iPhone уведомления приходят, когда DVK открыт с экрана «Домой».", action: null }
      : { text: "Этот браузер не умеет уведомления.", action: null };
  }
  if (o.permission === "denied") return { text: "Уведомления запрещены в настройках браузера для этого сайта.", action: null };
  return o.subscribed
    ? { text: "Включены: компьютер ответил — телефон даст знать.", action: "off" }
    : { text: "Выключены. Включите, чтобы узнавать об ответе компьютера при закрытой странице.", action: "on" };
}

// sameKey compares two keys: id ключа привязки или applicationServerKey
// подписки (он приходит ArrayBuffer'ом) с ключом сервера.
export function sameKey(a, b) {
  if (!a || !b) return false;
  const x = new Uint8Array(a), y = new Uint8Array(b);
  return x.length === y.length && x.every((v, i) => v === y[i]);
}

// nextTheme is the theme after t on a tap: как кнопка окна на Маке.
const THEME_ORDER = ["system", "light", "dark"];
export function nextTheme(t) {
  return THEME_ORDER[(Math.max(0, THEME_ORDER.indexOf(t)) + 1) % THEME_ORDER.length];
}

// themeColor is the status bar color for theme t (system — по prefers-color-scheme).
export function themeColor(t, systemDark) {
  const dark = t === "dark" || (t !== "light" && systemDark);
  return dark ? "#0b0b0c" : "#ffffff";
}
