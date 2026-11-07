package main

import (
	"os/exec"
	"testing"
)

// Откуда «Финансы» берут данные — одна чистая функция finSource
// (frontend/finsource.js) для строки над витриной и для выбора в настройках
// (ревью #510): правило адреса то же, что у appUpstream, — только этот Mac.
const finSourceCheck = `
const vm = require("vm"), fs = require("fs");
const ctx = { URL }; // в окне URL — глобальный, в чистом контексте vm его нет
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[1], "utf8") + ";this.api = { finSource };", ctx);
const { finSource } = ctx.api;
const label = (s) => s.name;
const on = { id: "server-a", name: "Яндекс", enabled: true }, off = { id: "server-b", name: "Резерв", enabled: false };
const fail = (m) => { console.error(m); process.exit(1); };
const cases = [
  ["", [on, off], "first", "Яндекс"],
  ["", [off], "off", "Резерв"],
  ["", [], "none", "не подключен"],
  ["server-a", [on, off], "server", "Яндекс"],
  ["server-b", [on, off], "off", "Резерв"],
  ["server-z", [on], "gone", "нет в"],
  ["http://127.0.0.1:8097", [on], "local", "127.0.0.1:8097"],
  ["http://localhost:9000", [], "local", "localhost:9000"],
  ["http://[::1]:8097", [], "local", "::1"],
  ["https://kb.example.com", [on], "bad", "не подходит"],
  ["http://10.0.0.5:8097", [on], "bad", "не подходит"],
  ["http://", [on], "bad", "не подходит"],
];
for (const [cur, servers, kind, text] of cases) {
  const r = finSource(cur, servers, label);
  if (r.kind !== kind || !r.text.includes(text)) fail(JSON.stringify(cur) + ": " + JSON.stringify(r) + ", ждали " + kind + " / " + text);
}
`

func TestFinSource(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: выбор источника финансов не проверен")
	}
	out, err := exec.Command(node, "-e", finSourceCheck, "frontend/finsource.js").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
