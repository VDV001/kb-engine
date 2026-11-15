package phonepage_test

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/mailcrypt"
	"github.com/daniil/kb-engine/internal/domain"
)

// cryptoCheck runs in node against static/crypto.js — тот же модуль, что у
// страницы телефона. Аргументы: путь к модулю и JSON задания; печатает JSON итога.
const cryptoCheck = `
const [mod, task] = process.argv.slice(1);
const c = await import(mod);
const t = JSON.parse(task);
const hex = (b) => Buffer.from(b).toString("hex");
const unhex = (s) => new Uint8Array(Buffer.from(s, "hex"));
const k = await c.importKey(t.key);
const out = { keyId: hex(k.keyId) };
const nonce = unhex("a0a1a2a3a4a5a6a7a8a9aaab");
const v = (counter, text) => c.seal(k, { chat: "c-1", from: "phone", sender: unhex("5152535455565758"), counter }, new TextEncoder().encode(text), nonce);
out.golden = hex(await v(7n, "привет, DVK"));
out.wide = hex(await v(0x0102030405060708n, "счетчик"));
const fromGo = await c.open(k, "c-1", "desk", unhex(t.fromGo));
out.fromGo = { sender: hex(fromGo.sender), counter: fromGo.counter.toString(), text: new TextDecoder().decode(fromGo.plain) };
out.toGo = hex(await c.seal(k, { chat: "c-2", from: "phone", sender: unhex("0102030405060708"), counter: 3n }, new TextEncoder().encode("с телефона")));
const fail = async (f) => { try { await f(); return "opened"; } catch (e) { return e.code || String(e); } };
const bad = unhex(t.fromGo); bad[bad.length - 1] ^= 1;
out.tampered = await fail(() => c.open(k, "c-1", "desk", bad));
out.otherChat = await fail(() => c.open(k, "c-9", "desk", unhex(t.fromGo)));
const other = await c.importKey(t.other);
out.wrongKey = await fail(() => c.open(other, "c-1", "desk", unhex(t.fromGo)));
out.short = await fail(() => c.open(k, "c-1", "desk", new Uint8Array(10)));
out.badKey = await fail(() => c.importKey(t.key + "\n"));
console.log(JSON.stringify(out));
`

// Телефон и окно говорят одним форматом: оба контрольных вектора mailcrypt,
// Go → JS и JS → Go, отказы по правке, чату, ключу, длине (#622, этап 4).
func TestPhoneCryptoMatchesGo(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: крипто телефона не сверено с Go")
	}
	raw32 := make([]byte, 32) // ключ вектора — байты 00..1f, не секрет
	for i := range raw32 {
		raw32[i] = byte(i)
	}
	keyText := base64.RawURLEncoding.EncodeToString(raw32)
	key, err := mailcrypt.ParseKey(keyText)
	if err != nil {
		t.Fatal(err)
	}
	fromGo, err := mailcrypt.Seal(key, mailcrypt.Meta{Chat: "c-1", From: domain.MailDesk, Sender: mailcrypt.Sender{9, 9, 9, 9, 9, 9, 9, 9}, Counter: 1 << 40}, []byte("ответ окна"))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := json.Marshal(map[string]string{"key": keyText, "other": mailcrypt.NewKey().Encode(), "fromGo": hex.EncodeToString(fromGo)})
	mod, _ := filepath.Abs("static/crypto.js")
	raw, err := exec.Command(node, "--input-type=module", "-e", cryptoCheck, "file://"+mod, string(task)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, raw)
	}
	var out struct {
		KeyID, Golden, Wide, ToGo                    string
		FromGo                                       struct{ Sender, Counter, Text string }
		Tampered, OtherChat, WrongKey, Short, BadKey string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &out); err != nil {
		t.Fatalf("node said: %s", raw)
	}
	if id := key.ID(); out.KeyID != hex.EncodeToString(id[:]) {
		t.Errorf("key id %s, Go %x", out.KeyID, id)
	}
	const golden = "0162e400aa51525354555657580000000000000007a0a1a2a3a4a5a6a7a8a9aaab36a7adad9573d20db2d056512b5a84883b2660a75009881d7981425d4404bb281f"
	const goldenWide = "0162e400aa51525354555657580102030405060708a0a1a2a3a4a5a6a7a8a9aaab3799adaa957ed33db3e2576bd7c06f1f71b66ce2927efc6db417f0225e71"
	if out.Golden != golden || out.Wide != goldenWide {
		t.Errorf("vectors differ from mailcrypt:\n%s\n%s", out.Golden, out.Wide)
	}
	if out.FromGo != (struct{ Sender, Counter, Text string }{"0909090909090909", "1099511627776", "ответ окна"}) {
		t.Errorf("JS opened Go's message as %+v", out.FromGo)
	}
	toGo, _ := hex.DecodeString(out.ToGo)
	m, plain, err := mailcrypt.Open(key, "c-2", domain.MailPhone, toGo)
	if err != nil || string(plain) != "с телефона" || m.Counter != 3 || m.Sender != (mailcrypt.Sender{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("Go opened JS's message: %+v %q %v", m, plain, err)
	}
	for name, got := range map[string][2]string{
		"tampered": {out.Tampered, "not-authentic"}, "other chat": {out.OtherChat, "not-authentic"},
		"wrong key": {out.WrongKey, "wrong-key"}, "short": {out.Short, "format"}, "key with newline": {out.BadKey, "format"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s: %s, want %s", name, got[0], got[1])
		}
	}
}
