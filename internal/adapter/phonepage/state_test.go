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

// 3. Счетчик двух вкладок: при записи — наибольший.
want(s.mergeCounter(4n, "5") === 5n && s.mergeCounter(6n, "5") === 6n && s.mergeCounter(3n, undefined) === 3n, "counter merge takes the max");

// 4. base64 большого шифротекста — без spread на 64 КБ.
const big = new Uint8Array(65536).map((_, i) => i * 7);
want(s.unb64(s.b64(big)).every((b, i) => b === big[i]) && s.b64(u8(0xfb, 0xff)) === "+/8=", "base64 round trip of 64 KB");

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
