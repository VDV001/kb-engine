package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// «Сохранить» не молчит (#505): в одной сборке кнопка в «Чат и модель» не
// давала ни «Сохранено», ни ошибки. Привязки Go идут через обертку
// (frontend/bindings.js): Set…, не ответивший за срок, сообщает об этом, но сам
// вызов не отменяется и не подменяется отказом — запись в Go не отменить, и
// окно сказало бы «не сохранено» о том, что сохранится позже (ревью #515).
const bindingsCheck = `
const vm = require("vm"), fs = require("fs");
const ctx = { setTimeout, clearTimeout, Promise, Proxy, Error };
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[1], "utf8") + ";this.api = { withSlowNotice, windowErrors, reportWindowError };", ctx);
const { withSlowNotice, windowErrors, reportWindowError } = ctx.api;
const fail = (m) => { console.error(m); process.exit(1); };
let resolveHung;
const app = {
  SetChatSettings: () => new Promise((ok) => { resolveHung = ok; }),
  SetTabs: async (x) => "ok:" + x,
  SetSources: async () => { throw new Error("папки нет: /x") },
  Sources: () => new Promise(() => {}), // не сохранение: о нем не сообщаем
  version: 3,
};
const slow = [];
const w = withSlowNotice(app, (name) => slow.push(name), 50);
const state = (p, ms) => Promise.race([p.then((v) => "resolved: " + v, (e) => "rejected: " + e.message), new Promise((ok) => setTimeout(() => ok("pending"), ms))]);
(async () => {
  const hung = w.SetChatSettings({});
  if (await state(hung, 150) !== "pending") fail("медленный Set… отменен или подменен отказом");
  if (slow.join() !== "SetChatSettings") fail("о медленном Set… не сообщено ровно один раз: " + slow.join());
  resolveHung("записано");
  if (await state(hung, 50) !== "resolved: записано") fail("поздний ответ Go не дошел до окна");
  if (await w.SetTabs("a") !== "ok:a") fail("быстрый Set… потерял ответ");
  if (await state(w.SetSources(), 150) !== "rejected: папки нет: /x") fail("отказ Go подменен");
  w.Sources();
  await new Promise((ok) => setTimeout(ok, 120));
  if (slow.length !== 1) fail("сообщено о том, что не сохранение: " + slow.join());
  if (w.version !== 3) fail("не-функция испорчена");
  if (withSlowNotice(app, () => {}, 50) !== w) fail("обертка не кэшируется: Proxy на каждый вызов native()");
  // Ошибка до готовности подсказок ждет в очереди, а не теряется.
  reportWindowError("Ошибка окна", "рано");
  if (windowErrors.queue.length !== 1) fail("ранняя ошибка потеряна");
  const shown = [];
  windowErrors.show = (h, t) => shown.push(h + ": " + t);
  reportWindowError("Ошибка окна", "поздно");
  if (shown.join() !== "Ошибка окна: поздно") fail("ошибка после готовности не показана: " + shown.join());
})().catch((e) => fail(String(e)));
`

func TestBindings(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: обертка привязок не проверена")
	}
	out, err := exec.Command(node, "-e", bindingsCheck, "frontend/bindings.js").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Проводка без node (CI модуль desktop не собирает, у node-теста есть Skip):
// bindings.js грузится первым — его перехват ошибок видит падение любого
// следующего скрипта; native() идет через обертку; очередь ошибок разбирает app.js.
func TestBindingsWiring(t *testing.T) {
	html, err := os.ReadFile("frontend/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	first := strings.Index(page, `<script src="`)
	if first < 0 || !strings.HasPrefix(page[first:], `<script src="bindings.js">`) {
		t.Errorf("bindings.js не первый скрипт index.html")
	}
	js, err := os.ReadFile("frontend/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"withSlowNotice(window.go.main.App", "windowErrors.show ="} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js без %q", want)
		}
	}
}
