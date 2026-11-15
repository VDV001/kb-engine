// Логика страницы телефона без DOM и сети (#622): привязка, разбор ящика,
// base64 — проверяется тестом phonepage в node (ревью #636).

const hex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
const sameKey = (a, b) => !!a && !!b && a.length === b.length && a.every((x, i) => x === b[i]);

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
  return false;
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
  const answer = m.from === "desk" && (p.kind === "answer" || p.kind === "error");
  if (!answer && !(m.from === "phone" && p.kind === "question")) return false; // чат не заводится
  const c = (st.chats[m.chat] ||= { title: "Чат", msgs: [], updated: 0 });
  if (answer) {
    c.msgs.push({ who: p.kind === "error" ? "error" : "desk", text: p.text, at, sources: p.sources || [] });
    c.pending = false;
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

// searchChat is the mailbox chat of this phone's searches: выдача и документы
// идут им и в список чатов не попадают. От отправителя — повторная привязка
// тем же ключом сохраняет его.
export function searchChat(st) {
  return "search-" + hex(st.sender || []);
}

// found applies the desk's answer to a search: выдача, документ или отказ.
// Выдача старого запроса не подменяет новый.
function found(st, m, p) {
  if (m.from !== "desk") return false; // свой запрос: на экране он уже есть
  const s = (st.search ||= { q: "", hits: [] });
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
