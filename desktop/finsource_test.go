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

// Окно само замечает, что свой kbengine serve включили или выключили
// (finServeAlive / finServeFlipped): витрина грузится в iframe один раз, и без
// опроса ошибка «не отвечает» жила бы до нового «Сохранить», а выключенный
// serve оставлял на экране прежние данные. Перечитывать надо только при смене.
const finProbeCheck = `
const vm = require("vm"), fs = require("fs");
const ctx = { URL };
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[1], "utf8") + ";this.api = { finServeAlive, finServeFlipped };", ctx);
const { finServeAlive, finServeFlipped } = ctx.api;
const fail = (m) => { console.error(m); process.exit(1); };
const alive = [[200, true], [204, true], [304, true], [401, true], [403, true], [404, true], [500, true], [502, false], [503, false], [0, false], [undefined, false]];
for (const [code, want] of alive) {
  if (finServeAlive(code) !== want) fail("finServeAlive(" + code + ") должен быть " + want);
}
const flips = [[null, true, false], [null, false, false], [true, true, false], [false, false, false], [false, true, true], [true, false, true]];
for (const [prev, now, want] of flips) {
  if (finServeFlipped(prev, now) !== want) fail("finServeFlipped(" + prev + "," + now + ") должен быть " + want);
}
`

func TestFinServeProbe(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: опрос serve финансов не проверен")
	}
	out, err := exec.Command(node, "-e", finProbeCheck, "frontend/finsource.js").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
