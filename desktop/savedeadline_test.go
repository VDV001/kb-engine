package main

import (
	"os/exec"
	"testing"
)

// «Сохранить» отвечает за срок или отказывает словами (#505): в одной сборке
// кнопка в «Чат и модель» молчала — ни «Сохранено», ни ошибки. Срок держит
// одна обертка над привязками Wails (frontend/savedeadline.js), а не каждая из
// одиннадцати кнопок: все вызовы Set… уже показывают отказ, не хватало только
// отказа у зависшего вызова.
const saveDeadlineCheck = `
const vm = require("vm"), fs = require("fs");
const ctx = { setTimeout, clearTimeout, Promise, Proxy, Error };
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[1], "utf8") + ";this.api = { withSaveDeadline };", ctx);
const { withSaveDeadline } = ctx.api;
const fail = (m) => { console.error(m); process.exit(1); };
const never = () => new Promise(() => {});
const app = {
  SetChatSettings: never,
  SetTabs: async (x) => "ok:" + x,
  SetSources: async () => { throw new Error("папки нет: /x") },
  Sources: never, // не сохранение: чтение и долгие вызовы срока не получают
  version: 3,
};
const w = withSaveDeadline(app, 50);
const late = (p, ms) => Promise.race([p.then(() => "resolved", (e) => "rejected: " + e.message), new Promise((ok) => setTimeout(() => ok("pending"), ms))]);
(async () => {
  const hung = await late(w.SetChatSettings({}), 500);
  if (!hung.startsWith("rejected: ") || !hung.includes("не ответило") || !hung.includes("Inspect Element")) fail("зависший Set…: " + hung);
  if (await w.SetTabs("a") !== "ok:a") fail("быстрый Set… потерял ответ");
  const refused = await late(w.SetSources(), 500);
  if (refused !== "rejected: папки нет: /x") fail("отказ Go подменен: " + refused);
  if (await late(w.Sources(), 150) !== "pending") fail("срок наложен не на сохранение");
  if (w.version !== 3) fail("не-функция испорчена");
  if (withSaveDeadline(app, 50) !== w) fail("обертка не кэшируется: Proxy на каждый вызов native()");
})().catch((e) => fail(String(e)));
`

func TestSaveDeadline(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: срок сохранения не проверен")
	}
	out, err := exec.Command(node, "-e", saveDeadlineCheck, "frontend/savedeadline.js").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
