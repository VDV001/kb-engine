package phonepage_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stateCheck runs static/state.js in node: логика страницы без DOM и сети —
// привязка, разбор ящика, base64 (ревью #636). Печатает «ok» или список провалов.
const stateCheck = `
const s = await import(process.argv[1]);
const fails = [];
const want = (ok, what) => { if (!ok) fails.push(what); };
const u8 = (...b) => new Uint8Array(b);
const key1 = { keyId: u8(1, 1, 1, 1) }, key2 = { keyId: u8(2, 2, 2, 2) };

// 1. Повторная привязка тем же ключом: номер, счетчик, отправитель, seen — прежние.
const prev = { pair: { token: "old", ...key1 }, sender: u8(9, 9, 9, 9, 9, 9, 9, 9), counter: 5n, after: 40,
  seen: { "c|desk|aa": "3" }, chats: { c: { title: "Чат", msgs: [{ who: "me", text: "x" }], updated: 1 } }, outbox: [] };
const same = s.repair(prev, { token: "new", ...key1 }, u8(7, 7, 7, 7, 7, 7, 7, 7));
want(same.pair.token === "new" && same.after === 40 && same.counter === 5n && same.sender[0] === 9 && same.seen["c|desk|aa"] === "3" && same.chats.c.msgs.length === 1, "repair with the same key keeps the cursor");
// Новый ключ: номер и счетчики заново, отправитель новый, история чатов остается.
const other = s.repair(prev, { token: "new", ...key2 }, u8(7, 7, 7, 7, 7, 7, 7, 7));
want(other.after === 0 && other.counter === 0n && other.sender[0] === 7 && Object.keys(other.seen).length === 0 && other.chats.c.msgs.length === 1, "repair with a new key resets the cursor, keeps chats");
want(s.repair(null, { token: "t", ...key1 }, u8(7, 7, 7, 7, 7, 7, 7, 7)).after === 0, "first pairing");

// 2. take: ответ окна — в чат и снимает «ждет»; повтор — мимо; свое — отмечает
// доставленным. take отвечает «чат изменился — перерисовать»; номер двигается всегда.
const st = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [{ who: "me", text: "вопрос", counter: "1", sent: false }], pending: true, updated: 1 } } };
const enc = (o) => new TextEncoder().encode(JSON.stringify(o));
const desk = { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 1n, plain: enc({ kind: "answer", text: "ответ" }) };
want(s.take(st, { seq: 1, chat: "c", from: "desk", at: "2026-11-15T10:00:00Z" }, desk) === true, "answer changes the state");
want(st.chats.c.msgs.length === 2 && st.chats.c.msgs[1].who === "desk" && !st.chats.c.pending && st.after === 1, "answer lands in the chat");
want(s.take(st, { seq: 2, chat: "c", from: "desk", at: "2026-11-15T10:00:01Z" }, desk) === false && st.chats.c.msgs.length === 2, "replay is dropped");
const own = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), counter: 1n, plain: enc({ kind: "question", text: "вопрос" }) };
s.take(st, { seq: 3, chat: "c", from: "phone", at: "2026-11-15T10:00:02Z" }, own);
want(st.chats.c.msgs.length === 2 && st.chats.c.msgs[0].sent === true && st.after === 3, "own message marks the question delivered, not doubled");
const twin = { sender: u8(3, 3, 3, 3, 3, 3, 3, 3), counter: 1n, plain: enc({ kind: "question", text: "с другого" }) };
s.take(st, { seq: 4, chat: "c", from: "phone", at: "2026-11-15T10:00:03Z" }, twin);
want(st.chats.c.msgs.length === 3 && st.chats.c.pending === true, "another phone's question shows and waits");
want(s.take(st, { seq: 5, chat: "c", from: "desk" }, null) === false && st.after === 5, "unreadable message only moves the cursor");

// 2б. Метки «взято» ответа окна — в сообщение (решение 15.11: ответ как в окне).
const st2 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [], pending: true, updated: 1 } } };
const withSrc = { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 1n, plain: enc({ kind: "answer", text: "312", sources: [{ kind: "connection", name: "Ozon · Пример" }] }) };
s.take(st2, { seq: 1, chat: "c", from: "desk", at: "2026-11-15T10:00:00Z" }, withSrc);
want(st2.chats.c.msgs[0].sources?.[0]?.name === "Ozon · Пример", "answer keeps its sources");

// 2в. Поиск идет служебным чатом ящика: выдача и документ — в st.search и st.doc,
// в список чатов не попадают; ошибка снимает «ищет».
const sc = s.searchChat(st2);
want(typeof sc === "string" && sc.length >= 8 && sc === s.searchChat({ ...st2 }), "search chat is stable for the phone");
st2.search = { q: "альфа", pending: true };
const res = { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 2n, plain: enc({ kind: "results", text: "альфа", hits: [{ ref: "file:a.pdf", kind: "pdf", title: "Альфа.pdf" }] }) };
want(s.take(st2, { seq: 2, chat: sc, from: "desk", at: "2026-11-15T10:00:01Z" }, res) === true, "results change the state");
want(st2.search.pending === false && st2.search.hits.length === 1 && st2.search.q === "альфа" && !st2.chats[sc], "results land in search, not in chats");
const docp = { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 3n, plain: enc({ kind: "doc", title: "Альфа.pdf", text: "текст", hits: [{ ref: "file:a.pdf", where: "Договоры" }] }) };
s.take(st2, { seq: 3, chat: sc, from: "desk", at: "2026-11-15T10:00:02Z" }, docp);
want(st2.doc?.title === "Альфа.pdf" && st2.doc.text === "текст" && st2.doc.ref === "file:a.pdf" && st2.doc.where === "Договоры", "doc lands in st.doc");
st2.search.pending = true;
const serr = { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 4n, plain: enc({ kind: "error", text: "поиск не подключен" }) };
s.take(st2, { seq: 4, chat: sc, from: "desk", at: "2026-11-15T10:00:03Z" }, serr);
want(st2.search.pending === false && st2.search.error === "поиск не подключен", "search error shows and stops waiting");
// Результаты старого запроса после нового — не подменяют новый.
st2.search = { q: "новый", pending: true };
const stale = { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 5n, plain: enc({ kind: "results", text: "старый", hits: [] }) };
s.take(st2, { seq: 5, chat: sc, from: "desk", at: "2026-11-15T10:00:04Z" }, stale);
want(st2.search.q === "новый" && st2.search.pending === true, "stale results do not replace the current query");

// 2г. Компьютер «спит»: вопрос ждет дольше 45 с без вестей от окна.
const t0 = Date.parse("2026-11-15T10:00:00Z");
const idle = { lastDesk: t0 - 3_600_000, chats: { c: { pending: true, msgs: [{ who: "me", at: t0 }] } } };
want(s.asleep(idle, t0 + 30_000) === 0, "30 s of waiting is not asleep");
want(s.asleep(idle, t0 + 60_000) === t0 - 3_600_000, "60 s of waiting: asleep since the last word of the desk");
want(s.asleep({ lastDesk: t0 + 50_000, chats: idle.chats }, t0 + 60_000) === 0, "desk spoke after the question: not asleep");
want(s.asleep({ chats: {} }, t0) === 0, "nothing waits: not asleep");
// Старый вопрос, оставшийся без ответа, не прячет сон компьютера после нового.
const stuck = { lastDesk: t0 - 600_000, chats: { old: { pending: true, msgs: [{ who: "me", at: t0 - 3_600_000 }] }, c: { pending: true, msgs: [{ who: "me", at: t0 }] } } };
want(s.asleep(stuck, t0 + 60_000) === t0 - 600_000, "an old stuck question does not hide the sleep after a new one");

// 3. Счетчик двух вкладок: при записи — наибольший.
want(s.mergeCounter(4n, "5") === 5n && s.mergeCounter(6n, "5") === 6n && s.mergeCounter(3n, undefined) === 3n, "counter merge takes the max");

// 4. base64 большого шифротекста — без spread на 64 КБ.
const big = new Uint8Array(65536).map((_, i) => i * 7);
want(s.unb64(s.b64(big)).every((b, i) => b === big[i]) && s.b64(u8(0xfb, 0xff)) === "+/8=", "base64 round trip of 64 KB");

// 5. Ревью #636: поиск другого телефона того же ключа — не чат на этом телефоне.
const st5 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: {} };
const theirs = "search-" + "03".repeat(8);
const tq = { sender: u8(3, 3, 3, 3, 3, 3, 3, 3), counter: 1n, plain: enc({ kind: "search", text: "альфа" }) };
const terr = { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 1n, plain: enc({ kind: "error", text: "источник не ответил" }) };
s.take(st5, { seq: 1, chat: theirs, from: "phone" }, tq);
s.take(st5, { seq: 2, chat: theirs, from: "desk" }, terr);
want(Object.keys(st5.chats).length === 0 && st5.after === 2, "another phone's search makes no chat here");
// Ответ окна неизвестного вида — тоже не заводит чат.
s.take(st5, { seq: 3, chat: "c9", from: "desk" }, { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 2n, plain: enc({ kind: "results", hits: [] }) });
want(!st5.chats.c9, "an unknown kind makes no chat");

// 6. Отказ открыть документ виден на экране документа, а не на вкладке поиска.
const st6 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: {}, search: { q: "альфа", hits: [], opening: "file:a.pdf" }, doc: { title: "Альфа.pdf", text: "", ref: "file:a.pdf" } };
s.take(st6, { seq: 1, chat: s.searchChat(st6), from: "desk" }, { sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: 1n, plain: enc({ kind: "error", text: "файл не читается" }) });
want(st6.doc.error === "файл не читается" && !st6.search.opening && !st6.search.error, "open refusal lands on the document screen");

// 7. single: второй вызов во время прогона не идет параллельно, но и не теряется.
let running = 0, most = 0, runs = 0;
const job = s.single(async () => { running++; most = Math.max(most, running); runs++; await new Promise((r) => setTimeout(r, 5)); running--; });
await Promise.all([job(), job(), job()]);
want(most === 1 && runs === 2, "single: one at a time, calls during a run fold into one more (most " + most + ", runs " + runs + ")");
const failing = s.single(async () => { throw new Error("x"); });
await failing().catch(() => {});
let again = false;
await s.single(async () => { again = true; })();
want(again, "single runs again after a failure");

// 8. Ревью безопасности #636: чужой ключ в ссылке — перепривязка с подтверждением.
want(s.rebinds(prev, key2.keyId) === true, "a new key on a paired phone rebinds");
want(s.rebinds(prev, key1.keyId) === false, "the same key is not a rebind");
want(s.rebinds(null, key2.keyId) === false && s.rebinds({}, key2.keyId) === false, "first pairing is not a rebind");

// 9. Ход модели: шаг окна — под «Компьютер отвечает», ответ его убирает (#622).
const st9 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [{ who: "me", text: "сроки?" }], pending: true, updated: 1 } } };
const dk = (n, o) => ({ sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: BigInt(n), plain: enc(o) });
want(s.take(st9, { seq: 1, chat: "c", from: "desk" }, dk(1, { kind: "step", text: "Документы: поставки" })) === true, "a step changes the chat");
want(st9.chats.c.step === "Документы: поставки" && st9.chats.c.msgs.length === 1 && st9.chats.c.pending, "a step shows as the window words it, not as a message");
s.take(st9, { seq: 2, chat: "c", from: "desk" }, dk(2, { kind: "answer", text: "30 дней" }));
want(!st9.chats.c.step && st9.chats.c.msgs.length === 2, "the answer clears the step");
s.take(st9, { seq: 3, chat: "c", from: "desk" }, dk(3, { kind: "step", text: "поздно" }));
want(!st9.chats.c.step, "a late step after the answer does not show");

// 10. Карточка уведомлений (#622): что сказать и какую кнопку дать.
const pv = (o) => s.pushView({ serverOn: true, supported: true, ios: false, standalone: false, permission: "default", subscribed: false, ...o });
want(pv({}).action === "on", "push off by default offers to turn on");
want(pv({ subscribed: true, permission: "granted" }).action === "off", "subscribed offers to turn off");
want(pv({ permission: "denied" }).action === null && /запрещ/.test(pv({ permission: "denied" }).text), "denied: no button, says where to allow");
want(pv({ supported: false, ios: true }).action === null && /Домой/.test(pv({ supported: false, ios: true }).text), "iPhone in Safari: home screen first");
want(pv({ supported: false }).action === null && !/Домой/.test(pv({ supported: false }).text), "other browser without push");
want(pv({ serverOn: false, subscribed: true }).action === null, "server without push: no button even if subscribed");

// 11. Ключ сервера сменился (перепривязка, новый ключ VAPID) — подписка старым
// ключом мертва, ее надо оформить заново (ревью #643).
const k1 = new Uint8Array([4, 1, 2, 3]), k2 = new Uint8Array([4, 1, 2, 4]);
want(s.sameKey(k1.buffer, new Uint8Array([4, 1, 2, 3])) && !s.sameKey(k1.buffer, k2) && !s.sameKey(null, k2), "subscription key compared with the server's");

// 12. Тема как в окне на Маке: как в системе → светлая → темная по кругу.
want(s.nextTheme("system") === "light" && s.nextTheme("light") === "dark" && s.nextTheme("dark") === "system", "theme cycles like the Mac window");
want(s.nextTheme("purple") === "light", "unknown theme counts as system");
// Полоса состояния — цвет фона выбранной темы, а «как в системе» — по системе.
want(s.themeColor("light", true) === "#ffffff" && s.themeColor("dark", false) === "#0b0b0c", "explicit theme wins over the system");
want(s.themeColor("system", true) === "#0b0b0c" && s.themeColor("system", false) === "#ffffff", "system theme follows the system");

// 13. Цвет полосы состояния — тот же --z-bg, что у страницы (ревью #644).
const css = (await import("node:fs")).readFileSync(new URL("style.css", process.argv[1]), "utf8");
const bgs = [...css.matchAll(/--z-bg:\s*(#[0-9a-fA-F]+)/g)].map((m) => m[1].toLowerCase().replace(/^#(.)(.)(.)$/, "#$1$1$2$2$3$3"));
const themeJs = (await import("node:fs")).readFileSync(new URL("theme.js", process.argv[1]), "utf8");
want(themeJs.includes('"' + s.themeColor("dark", true) + '"') && themeJs.includes('"' + s.themeColor("light", false) + '"'), "theme.js paints the same status bar colors");
want(bgs.length >= 2 && bgs[0] === s.themeColor("dark", true) && bgs.slice(1).every((c) => c === s.themeColor("light", false)), "status bar color equals --z-bg of style.css: " + bgs.join(","));

// 14. Состояния вопроса на телефоне (#646): в очереди → отправлено → компьютер
// принял → отвечает (шаг) → ответ.
const dk14 = (n, p) => ({ sender: u8(2, 2, 2, 2, 2, 2, 2, 2), counter: BigInt(n), plain: enc(p) });
const st14 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [{ who: "me", text: "сколько?", counter: "1", sent: false }], pending: true, updated: 1 } } };
want(s.waitView(st14.chats.c).text === "Ждет отправки", "unsent question waits to be sent: " + s.waitView(st14.chats.c).text);
s.take(st14, { seq: 1, chat: "c", from: "phone", at: "2026-11-16T10:00:00Z" }, { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), counter: 1n, plain: enc({ kind: "question", text: "сколько?" }) });
want(s.waitView(st14.chats.c).text === "Отправлено · ждет компьютер", "sent question waits for the computer: " + s.waitView(st14.chats.c).text);
want(s.take(st14, { seq: 2, chat: "c", from: "desk" }, dk14(1, { kind: "accepted", text: "" })) === true, "accepted changes the chat");
const v14 = s.waitView(st14.chats.c);
want(v14.text === "Компьютер принял · отвечает" && !v14.step && st14.chats.c.msgs.length === 1, "accepted shows, not as a message: " + JSON.stringify(v14));
s.take(st14, { seq: 3, chat: "c", from: "desk" }, dk14(2, { kind: "step", text: "Документы: поставки" }));
want(s.waitView(st14.chats.c).step === "Документы: поставки", "the step shows under accepted");
s.take(st14, { seq: 4, chat: "c", from: "desk" }, dk14(3, { kind: "answer", text: "12" }));
want(!st14.chats.c.pending && !st14.chats.c.accepted, "the answer clears the wait");
want(s.take(st14, { seq: 5, chat: "c", from: "desk" }, dk14(4, { kind: "accepted", text: "" })) === false && !st14.chats.c.accepted, "a late accepted after the answer does not show");

// 15. Круг контекста (#647): заполнение приходит с ответом, пороги — как в окне.
const st15 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [], pending: true, updated: 1 } } };
s.take(st15, { seq: 1, chat: "c", from: "desk" }, dk14(1, { kind: "answer", text: "ок", context: { used: 6000, window: 12000 } }));
want(st15.chats.c.context?.used === 6000, "the answer keeps the context fill");
const r15 = s.ctxView(st15.chats.c.context);
want(r15?.text === "50%" && r15.tone === "warn" && Math.abs(r15.fill - 0.5) < 1e-9, "half full warns like the window: " + JSON.stringify(r15));
want(s.ctxView({ used: 1000, window: 12000 }).tone === "ok" && s.ctxView({ used: 11000, window: 12000 }).tone === "bad", "thresholds 0.5 and 0.8");
want(s.ctxView({ used: 13000, window: 12000 }).fill === 1 && s.ctxView({ used: 13000, window: 12000 }).text === "108%", "overflow shows over 100 and a full ring");
want(s.ctxView({ used: 6000, window: 0 })?.text === "?" && s.ctxView(null) === null, "no window — «?», no count — no ring");
s.take(st15, { seq: 2, chat: "c", from: "desk" }, dk14(2, { kind: "answer", text: "без счета" }));
want(st15.chats.c.context === undefined, "an answer without a count drops the old fill, like the window");
// Ревью #654: сбой хода кольца не снимает — в окне оно остается; переполнение
// называется переполнением; разговор без счета — «—», как в окне.
s.take(st15, { seq: 3, chat: "c", from: "desk" }, dk14(3, { kind: "answer", text: "ок", context: { used: 11000, window: 12000 } }));
s.take(st15, { seq: 4, chat: "c", from: "desk" }, dk14(4, { kind: "error", text: "провайдер отказал" }));
want(st15.chats.c.context?.used === 11000, "an error keeps the ring");
want(s.ctxView({ used: 13000, window: 12000 }).over === true && !s.ctxView({ used: 11000, window: 12000 }).over, "overflow is marked as overflow");
const talk = s.ctxView(undefined, true);
want(talk?.text === "—" && talk.unknown === true && s.ctxView(undefined, false) === null, "a chat with answers but no count shows «—»: " + JSON.stringify(talk));

// 16. Ревью #653: новый вопрос не наследует «принял» и шаг прежнего; вопрос,
// принятый сервером, — «отправлено» сразу, а не после опроса; окно замолчало
// посреди ответа — так и сказать, а не «отвечает» без конца.
const st16 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [{ who: "me", text: "первый", counter: "1", sent: true }], pending: true, accepted: true, step: "Документы: поставки", updated: 1 } } };
s.asked(st16.chats.c, "второй", "2", 5);
const v16 = s.waitView(st16.chats.c);
want(v16.text === "Ждет отправки" && !v16.step && st16.chats.c.msgs.length === 2 && st16.chats.c.pending, "a new question starts its own wait: " + JSON.stringify(v16));
s.posted(st16, { chat: "c", counter: "2" });
want(st16.chats.c.msgs[1].sent === true && s.waitView(st16.chats.c).text === "Отправлено · ждет компьютер", "a question the server took is sent at once");
s.posted(st16, { chat: "c" }); s.posted(st16, { chat: "нет", counter: "9" }); // старые исходящие без счетчика, чужой чат
st16.chats.c.accepted = true;
want(/замолчал/.test(s.waitView(st16.chats.c, true).text) && !/отвечает/.test(s.waitView(st16.chats.c, true).text), "accepted but the desk went silent: " + s.waitView(st16.chats.c, true).text);
want(s.waitView(st16.chats.c, false).text === "Компьютер принял · отвечает", "accepted and the desk alive");

// 17. «Запомнить о Вас?» на телефоне (#647): предложение окна — карточки в
// чате; повтор — без дубля; «запомнено»/«не запоминаю» снимает, отказ — остается со словами.
const st17 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [{ who: "desk", text: "ок" }], updated: 1 } } };
const offer = { kind: "remember", text: "", facts: [{ id: "f1", title: "Работа", text: "Ведет закупки" }, { id: "f2", title: "Семья", text: "Двое детей" }] };
want(s.take(st17, { seq: 1, chat: "c", from: "desk" }, dk14(1, offer)) === true && st17.chats.c.remember?.length === 2, "the offer shows as cards");
s.take(st17, { seq: 2, chat: "c", from: "desk" }, dk14(2, offer));
want(st17.chats.c.remember.length === 2 && st17.chats.c.msgs.length === 1, "a repeated offer is not doubled and is not a message");
s.deciding(st17.chats.c, "f1");
want(st17.chats.c.remember[0].busy === true, "a sent decision marks the card busy");
s.take(st17, { seq: 3, chat: "c", from: "desk" }, dk14(3, { kind: "memory", text: "saved", facts: [{ id: "f1" }] }));
want(st17.chats.c.remember.length === 1 && st17.chats.c.remember[0].id === "f2", "saved takes the card away");
s.take(st17, { seq: 4, chat: "c", from: "desk" }, dk14(4, { kind: "memory", text: "портрет недоступен", facts: [{ id: "f2" }] }));
want(st17.chats.c.remember[0].err === "портрет недоступен" && !st17.chats.c.remember[0].busy, "a refusal keeps the card with the words");
// Ревью #655: «gone» (решено в окне, не предлагался) снимает карточку; другой
// код — карточка остается с ним; «Скрыть» убирает карточку на телефоне.
st17.chats.c.remember = [{ id: "g1", text: "a" }, { id: "g2", text: "b" }, { id: "g3", text: "c" }];
s.take(st17, { seq: 10, chat: "c", from: "desk" }, dk14(10, { kind: "memory", text: "gone", facts: [{ id: "g1" }] }));
want(!st17.chats.c.remember.some((x) => x.id === "g1"), "gone takes the card away");
s.take(st17, { seq: 11, chat: "c", from: "desk" }, dk14(11, { kind: "memory", text: "bad", facts: [{ id: "g2" }] }));
want(st17.chats.c.remember.find((x) => x.id === "g2")?.err === "bad", "another code stays on the card");
s.hideFact(st17.chats.c, "g2");
want(st17.chats.c.remember.length === 1 && st17.chats.c.remember[0].id === "g3", "hide takes the card away on the phone");
const st17b = { ...st17, seen: {}, chats: {} };
want(s.take(st17b, { seq: 5, chat: "x", from: "desk" }, dk14(5, offer)) === false && !st17b.chats.x, "an offer to a chat the phone does not know is dropped");

// 18. Поле ввода как в окне (#647): состояние окна приходит служебным чатом;
// вопрос несет модель и источники, выбранные для чата; не выбрано — как в окне.
const st18 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: { c: { title: "Чат", msgs: [], updated: 1 } } };
const desk18 = { model: "qwen3", models: ["qwen3", "llama3"], sources: [{ id: "f-1", name: "notes" }, { id: "oz1", name: "Ozon · Пример", conn: true }] };
want(s.take(st18, { seq: 1, chat: s.deskChat(st18), from: "desk" }, dk14(1, { kind: "desk", text: "", desk: desk18 })) === true && st18.desk?.model === "qwen3" && st18.desk.sources.length === 2, "the desk state lands in st.desk");
want(JSON.stringify(s.questionPayload(st18.chats.c, "сроки?")) === JSON.stringify({ kind: "question", text: "сроки?" }), "no choice — the window's settings");
Object.assign(st18.chats.c, { model: "llama3", only: ["f-1"] });
want(JSON.stringify(s.questionPayload(st18.chats.c, "сроки?")) === JSON.stringify({ kind: "question", text: "сроки?", model: "llama3", only: ["f-1"] }), "the chat's choice goes with the question");

// 19. Ревью #656: состояние окна — своим служебным чатом, а не чатом поиска:
// отказ на desk не становится ошибкой поиска и наоборот; отказ не снимает
// «спросили» — иначе запрос уходил бы на каждое открытие чата.
const st19 = { sender: u8(1, 1, 1, 1, 1, 1, 1, 1), after: 0, seen: {}, chats: {}, deskAsked: 100 };
const dc = s.deskChat(st19);
want(dc !== s.searchChat(st19) && dc === s.deskChat({ ...st19 }), "the desk has its own stable chat");
want(s.take(st19, { seq: 1, chat: dc, from: "desk" }, dk14(1, { kind: "error", text: "окно старое" })) === true && st19.deskErr === "окно старое" && st19.deskAsked === 100 && !st19.search?.error, "a desk refusal stays the desk's and keeps «asked»");
s.take(st19, { seq: 2, chat: s.searchChat(st19), from: "desk" }, dk14(2, { kind: "error", text: "источник недоступен" }));
want(st19.search?.error === "источник недоступен" && st19.deskErr === "окно старое", "a search refusal is the search's even while the desk was asked");
want(s.take(st19, { seq: 3, chat: dc, from: "desk" }, dk14(3, { kind: "desk", desk: desk18 })) === true && st19.desk?.model === "qwen3" && !st19.deskErr, "the desk state lands from its chat");
want(s.take(st19, { seq: 4, chat: "desk-ffff", from: "desk" }, dk14(4, { kind: "desk", desk: { model: "x" } })) === false && st19.desk.model === "qwen3", "another phone's desk chat is not ours");
// Устаревший выбор не уходит: модели и источники, которых окно больше не
// показывает, отсекаются; ничего не осталось — как в окне.
const c19 = { model: "gone", only: ["f-1", "gone"], msgs: [] };
want(JSON.stringify(s.questionPayload(c19, "q", desk18)) === JSON.stringify({ kind: "question", text: "q", only: ["f-1"] }), "stale model and source are dropped");
want(JSON.stringify(s.questionPayload({ only: ["gone"], msgs: [] }, "q", desk18)) === JSON.stringify({ kind: "question", text: "q" }), "nothing left — the window's choice");
// Выбор источников как в окне: ни одного — нельзя; «все» уходит явным
// списком — иначе после сужения его не вернуть (ревью #656: «не выбирал» —
// это источники чата окна, а не все).
const all19 = ["f-1", "oz1"];
want(s.pickSources(all19, []).ok === false, "no source at all is refused");
want(s.pickSources(all19, ["oz1", "f-1"]).ok && s.pickSources(all19, ["oz1", "f-1"]).only?.join() === "oz1,f-1", "all goes as an explicit list");
want(s.pickSources(all19, ["oz1"]).only?.join() === "oz1", "a part is the chat's choice");

console.log(fails.length ? fails.join("\n") : "ok");
`

// Логика страницы телефона проверяется, а не верится на слово: повторная
// привязка, повтор, свое, другой телефон, счетчик двух вкладок, base64 64 КБ.
func TestPhoneStateLogic(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: логика страницы телефона не проверена")
	}
	mod, _ := filepath.Abs("static/state.js")
	out, err := exec.Command(node, "--input-type=module", "-e", stateCheck, "file://"+mod).CombinedOutput()
	if got := strings.TrimSpace(string(out)); err != nil || got != "ok" {
		t.Fatalf("node: %v\n%s", err, got)
	}
}
