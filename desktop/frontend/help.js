// Справка окна DVK (решение владельца 07.11): содержание и поиск по задаче.
// Без DOM: app.js рисует, а логику проверяет node (desktop/helpsearch_test.go).
// Команды движка и клавиши окна — для человека с окном, без личных путей и
// обёрток. Команды и флаги сверены с `kbengine <команда> --help` сборки 52ad0240;
// суммы, имена счетов и адреса — примеры. Новая команда движка — строка здесь.
const HELP = [
  { id: "window", t: "Окно DVK", c: "var(--z-sp4)", lead: "Клавиши работают в окне; ⌥Space — из любого приложения.", items: [
    { k: ["⌥Space"], d: "открыть или спрятать окно из любого приложения" },
    { k: ["⌘,"], d: "настройки: источники, подключения, чат и модель, окно, оформление" },
    { k: ["⌘1", "…", "⌘9"], d: "Поиск, потом модули по порядку вкладок" },
    { k: ["↑", "↓", "Enter"], d: "выбрать в выдаче и открыть" },
    { k: ["Esc"], d: "очистить поиск, еще раз — спрятать окно; в настройках — закрыть их, при чтении вслух — остановить" },
    { k: ["⌘\\"], d: "в Чате — спрятать или показать панель чатов" },
  ] },
  { id: "search", t: "Поиск", c: "var(--z-sp5)", lead: "То же, что строка поиска окна, но из терминала.", items: [
    { cmd: 'kbengine search -q "сборка мусора" --catalog catalog.json', d: "поиск по базе: слова, опечатки, раскладка; --limit — сколько показать" },
    { cmd: 'kbengine files -q "договор аренды" --root ~/Documents', d: "поиск по файлам папки; --root можно повторять, --exclude — пропустить папку" },
    { cmd: "kbengine search --build-index --catalog catalog.json --embed-url http://127.0.0.1:11434/api/embeddings", d: "собрать смысловой индекс рядом с каталогом (нужна служба эмбеддингов, например Ollama)" },
  ] },
  { id: "fin", t: "Финансы", c: "var(--z-sp1)", lead: "Журнал — --ledger, книга Excel — --from; в примерах файлы лежат в текущей папке. Удобно завести алиас с этими путями.", items: [
    { cmd: "kbengine fin add --ledger transactions.jsonl --amount '1 250' --cat Еда --place Магнит --account Карта", d: "записать трату; дата по умолчанию сегодня, --date ГГГГ-ММ-ДД — другая" },
    { cmd: "kbengine fin add --ledger transactions.jsonl --kind income --amount 50000 --account Карта --note 'за ноябрь'", d: "записать доход" },
    { cmd: "kbengine fin report --ledger transactions.jsonl --year 2026 --month 11", d: "отчет за месяц: расходы, доходы, итог, по категориям и счетам" },
    { cmd: "kbengine fin list --ledger transactions.jsonl --cat Еда --year 2026 --month 11", d: "список записей по фильтру: счет, категория, год и месяц, вид" },
    { cmd: "kbengine fin balance --ledger transactions.jsonl --from Учет.xlsx --bank Карта --amount '12 400'", d: "подтвердить остаток счета; сначала запишите траты, потом остаток" },
    { cmd: "kbengine fin sync --ledger transactions.jsonl --from Учет.xlsx --dry-run", d: "что уйдет в книгу Excel — посмотреть до записи; без --dry-run — записать" },
  ] },
  { id: "serve", t: "Своя витрина", c: "var(--z-sp2)", lead: "Витрина базы и финансов на этом Mac — и данные для модуля «Финансы».", items: [
    { cmd: "kbengine serve --addr 127.0.0.1:8097 --catalog catalog.json --ledger transactions.jsonl --from Учет.xlsx", d: "запустить витрину и открыть http://127.0.0.1:8097; адрес в сети без --token-file и TLS сервер не примет" },
    { k: ["Настройки", "→", "Окно", "→", "Модули"], d: "«Финансы берут данные из» → этот Mac, адрес как в --addr" },
  ] },
  { id: "access", t: "Доступы", c: "var(--z-src-violet)", lead: "Пароль хранится в Связке ключей и в терминал не печатается.", items: [
    { cmd: "kbengine creds add --product Биллинг --env прод --login admin --url https://billing.example.org", d: "завести доступ; пароль спросит и положит в Связку ключей" },
    { cmd: "kbengine creds find биллинг прод", d: "найти карточку доступа по словам" },
    { cmd: "kbengine creds copy <id>", d: "скопировать пароль в буфер на 30 секунд" },
    { cmd: "kbengine creds rm <id>", d: "снять доступ: пароль из Связки и карточку" },
  ] },
  { id: "kb", t: "База знаний", c: "var(--z-sp6)", lead: "Пополнение и уход за каталогом.", items: [
    { cmd: "kbengine add --title 'Разбор очередей' --url https://example.org/queues --category engineering --verdict keep", d: "добавить чужой материал по ссылке; свой — --file вместо --url" },
    { cmd: "kbengine set --ids 12,14 --lifecycle outdated", d: "сменить состояние, категорию, теги у нескольких записей" },
    { cmd: "kbengine audit", d: "проверить каталог" },
    { cmd: "kbengine dedup", d: "найти возможные дубли" },
    { cmd: "kbengine drift --limit 50", d: "проверить, живы ли ссылки; отчет начинается с того, что не проверено" },
  ] },
  { id: "mcp", t: "Агент в чате (MCP)", c: "var(--z-sp3)", lead: "База и папки окна прямо в Claude: агент ищет сам.", items: [
    { cmd: 'kbengine-mcp --catalog catalog.json --files-config "~/Library/Application Support/kb-desktop/config.json"', d: "запустить сервер MCP; источники он берет из настроек окна" },
    { k: ["search_catalog", "get_entry", "stats"], d: "инструменты агента: поиск по базе, карточка записи, сводка" },
    { k: ["search_files", "read_file"], d: "инструменты агента: поиск по папкам окна и чтение файла" },
    { k: ["search_access"], d: "инструмент агента: карточки доступов без паролей — пароль агенту не отдается никогда" },
  ] },
];
// Слова задачи → то, что стоит в команде: человек пишет «трата», а не «fin add».
const HELP_SYN = { "трата": "fin add", "потратил": "fin add", "расход": "fin add", "доход": "income", "зарплата": "income", "остаток": "balance", "баланс": "balance",
  "пароль": "creds", "логин": "creds", "ссылки": "drift", "дубли": "dedup", "открыть окно": "⌥space", "горячая": "⌥space", "excel": "sync", "книга": "sync",
  "агент": "mcp", "claude": "mcp", "файлы": "files", "документ": "files", "настройки": "⌘," };

// helpFold — регистр и «ё», как у поиска окна: тексты справки пишутся через «е».
const helpFold = (s) => s.toLowerCase().replace(/ё/g, "е");
const HELP_SYN_F = Object.entries(HELP_SYN).map(([k, v]) => [helpFold(k), helpFold(v)]);
// helpTerms разбирает запрос один раз: слова и для каждого — слово с синонимами.
function helpTerms(q) {
  const words = helpFold(q).split(/\s+/).filter(Boolean);
  return { words, alts: words.map((w) => [w, ...HELP_SYN_F.filter(([k]) => k.startsWith(w) || w.startsWith(k)).map(([, v]) => v)]) };
}
const helpHay = (s, it) => helpFold(s.t + " " + it.d + " " + (it.cmd || it.k.join(" ")));
// helpHits — строки раздела, где есть каждое слово запроса или его синоним.
function helpHits(s, terms) {
  if (!terms.words.length) return s.items;
  return s.items.filter((it) => { const h = helpHay(s, it); return terms.alts.every((a) => a.some((x) => h.includes(x))); });
}
const helpEsc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
// helpMarker подсвечивает слова по сырому тексту и экранирует куски: по уже
// экранированному тексту «q» находилось бы внутри «&quot;» (ревью #509).
function helpMarker(words) {
  return (text) => {
    const low = helpFold(text), spans = [];
    if (low.length === text.length) for (const w of words) for (let at = low.indexOf(w); at >= 0; at = low.indexOf(w, at + 1)) spans.push([at, at + w.length]);
    spans.sort((a, b) => a[0] - b[0] || b[1] - a[1]);
    let out = "", last = 0;
    for (const [from, to] of spans) {
      if (to <= last) continue;
      const at = Math.max(from, last);
      out += helpEsc(text.slice(last, at)) + `<mark>${helpEsc(text.slice(at, to))}</mark>`;
      last = to;
    }
    return out + helpEsc(text.slice(last));
  };
}
