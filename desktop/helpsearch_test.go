package main

import (
	"os/exec"
	"testing"
)

// Поиск справки (frontend/help.js) — чистые функции без DOM; проверяются node,
// как их видит окно. Стенда для app.js нет, поэтому логика поиска вынесена
// отдельно (ревью #509): синонимы задачи, «ё», И по словам, подсветка по сырому
// тексту — по экранированному короткое слово ломало «&quot;».
const helpSearchCheck = `
const vm = require("vm"), fs = require("fs");
const ctx = {};
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[1], "utf8") + ";this.api = { HELP, helpTerms, helpHits, helpMarker };", ctx);
const { HELP, helpTerms, helpHits, helpMarker } = ctx.api;
const fail = (m) => { console.error(m); process.exit(1); };
const find = (q) => HELP.flatMap((s) => helpHits(s, helpTerms(q)).map((it) => it.cmd || it.k.join(" ")));
if (!find("доход").some((c) => c.includes("--kind income"))) fail("«доход» не нашел fin add --kind income: " + JSON.stringify(find("доход")));
if (!find("отчёт").some((c) => c.includes("fin report"))) fail("«отчёт» с ё не нашел fin report");
if (find("пароль буфер").length !== 1) fail("«пароль буфер» — И по словам: " + JSON.stringify(find("пароль буфер")));
if (!find("артефакты").length || !find("версии документа").length || !find("дубль").length) fail("справка не знает про Артефакты: " + JSON.stringify(find("артефакты")));
if (!find("заметка версии").length || !find("актуальная").length || !find("сравнить версии").length) fail("справка не знает про заметки, значок и сравнение версий");
if (find("zzzz").length) fail("«zzzz» что-то нашел");
if (!find("терминал").some((c) => c.startsWith("kbengine tui"))) fail("«терминал» не нашел kbengine tui");
if (!find("выдать токен").some((c) => c.startsWith("kbengine users add"))) fail("«выдать токен» не нашел users add: " + JSON.stringify(find("выдать токен")));
if (!find("входящие бот").some((c) => c.startsWith("kbengine inbox"))) fail("«входящие бот» не нашел inbox");
// «Браузер»: человек ищет задачу, а не команду.
if (!find("браузер").some((c) => c.startsWith("kbengine browser serve"))) fail("«браузер» не нашел kbengine browser serve: " + JSON.stringify(find("браузер")));
if (!find("brave").some((c) => c.includes("Brave Browser.app"))) fail("«brave» не нашел команду именно для Brave: " + JSON.stringify(find("brave")));
if (!find("chrome путь").some((c) => c.includes("--chrome"))) fail("«chrome путь» не нашел --chrome: " + JSON.stringify(find("chrome путь")));
if (!find("brave").length || !find("сайт").length) fail("«brave» и «сайт» ничего не нашли");
if (!find("окно скрыто").some((c) => c.includes("--window"))) fail("«окно скрыто» не нашел --window: " + JSON.stringify(find("окно скрыто")));
if (!find("не запущен").length) fail("«не запущен» не нашел подсказку про запуск");
if (!find("порядок вкладок").length || !find("config.json").length) fail("настройки окна не находятся");
const m = helpMarker(helpTerms("q").words)('kbengine search -q "x" <id>');
if (m.includes("&<mark>") || !m.includes("&quot;x&quot;") || !m.includes("&lt;id&gt;")) fail("подсветка портит экранирование: " + m);
if (!m.includes("<mark>q</mark>")) fail("«q» не подсвечен: " + m);
if (helpMarker([])("<b>") !== "&lt;b&gt;") fail("без слов текст не экранирован");
`

func TestHelpSearch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет node: поиск справки не проверен")
	}
	out, err := exec.Command(node, "-e", helpSearchCheck, "frontend/help.js").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
