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
  const c = (st.chats[m.chat] ||= { title: "Чат", msgs: [], updated: 0 });
  const at = Date.parse(m.at) || Date.now();
  if (m.from === "desk" && (p.kind === "answer" || p.kind === "error")) {
    c.msgs.push({ who: p.kind === "error" ? "error" : "desk", text: p.text, at });
    c.pending = false;
  } else if (m.from === "phone" && p.kind === "question") {
    const mine = who === hex(st.sender);
    const sent = mine && c.msgs.find((x) => x.who === "me" && x.counter === o.counter.toString());
    if (sent) {
      sent.sent = true; // свой вопрос дошел: в чате он уже есть
    } else {
      if (!c.msgs.length) c.title = p.text.slice(0, 60);
      c.msgs.push({ who: "me", text: p.text, at, sent: true }); // с другого телефона человека
      c.pending = true;
    }
  } else {
    return false;
  }
  c.updated = Date.now();
  return true;
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
