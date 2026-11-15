package main

import (
	"os/exec"
	"testing"
)

// Платформа окна (frontend/platform.js, #622): та же страница на Маке и на
// Windows. node грузит файл с подменным navigator и document и проверяет, что
// Windows узнается по userAgent WebView2, главная клавиша — Ctrl, а подписи
// переводятся без потерь: «⌘ ,» и «⌘,» дают одно «Ctrl+,», Finder — Проводник,
// Связка ключей — Диспетчер учетных данных во всех падежах, Мак — компьютер,
// а «Макет» и «Маркеты» не трогаются.
const platformCheck = `
const vm = require("vm"), fs = require("fs");
const fail = (m) => { console.error(m); process.exit(1); };
const load = (ua) => {
  const html = { dataset: {} };
  const ctx = { navigator: { userAgent: ua }, document: { documentElement: html, addEventListener() {} } };
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(process.argv[1], "utf8") + ";this.api = { IS_WIN, modKey, otherMod, winKeys, KEY_GLYPH, CONTENT };", ctx);
  return { ...ctx.api, os: html.dataset.os };
};
const win = load("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36 Edg/142.0.0.0");
const mac = load("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)");
if (!win.IS_WIN || win.os !== "windows") fail("WebView2 не узнан как Windows: " + win.os);
if (mac.IS_WIN || mac.os !== "mac") fail("WKWebView узнан как Windows: " + mac.os);
const ctrl = { ctrlKey: true, metaKey: false }, cmd = { ctrlKey: false, metaKey: true };
if (!win.modKey(ctrl) || win.modKey(cmd) || !win.otherMod(cmd)) fail("на Windows главная клавиша не Ctrl");
if (!mac.modKey(cmd) || mac.modKey(ctrl) || !mac.otherMod(ctrl)) fail("на Маке главная клавиша не ⌘");
const cases = [
  ["⌘ ,", "Ctrl+,"], ["⌘,", "Ctrl+,"], ["⌘1…⌘9", "Ctrl+1…Ctrl+9"], ["⌥Space", "Alt+Space"], ["⇧⌘N", "Shift+Ctrl+N"],
  ["Показать в Finder", "Показать в Проводнике"], ["В Finder", "В Проводнике"], ["⌘↵ в Finder", "Ctrl+↵ в Проводнике"],
  ["пароль по Touch ID", "пароль по Touch ID"],
  ["Связка ключей не ответила — разблокируйте ее", "Диспетчер учетных данных не ответил — разблокируйте ее"],
  ["Связка не прочлась — откройте", "Диспетчер учетных данных не прочелся — откройте"],
  ["ключ лежит в Связке ключей этого Мака", "ключ лежит в Диспетчере учетных данных этого компьютера"],
  ["взять из Связки ключей", "взять из Диспетчера учетных данных"], ["положить в Связку ключей macOS", "положить в Диспетчер учетных данных"],
  ["доступ к Связке ключей", "доступ к Диспетчеру учетных данных"], ["хранится в Связке", "хранится в Диспетчере учетных данных"],
  ["на этом Маке", "на этом компьютере"], ["с Мака", "с компьютера"], ["этот Мак", "этот компьютер"], ["Голоса macOS", "Голоса Windows"],
  ["Макет и Маркеты", "Макет и Маркеты"], ["Маки", "Маки"],
];
for (const [from, want] of cases) {
  const got = win.winKeys(from);
  if (got !== want) fail(JSON.stringify(from) + " → " + JSON.stringify(got) + ", ждали " + JSON.stringify(want));
}
for (const t of ["Поиск по базе", "Макет", "Маркеты"]) if (win.KEY_GLYPH.test(t)) fail("обычный текст принят за подпись окна: " + t);
`

func TestPlatform(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: платформа окна не проверена")
	}
	out, err := exec.Command(node, "-e", platformCheck, "frontend/platform.js").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
