// Платформа окна (#622, решение владельца 15.11: на Windows окно выглядит как
// на Маке). Страница одна; отличия Windows собраны здесь: метка на <html> для
// стилей, кнопки «— ▢ ✕» в верхней полосе вместо светофора, Ctrl вместо ⌘ в
// сочетаниях и подписях. Грузится до app.js: IS_WIN и modKey нужны ему сразу.
const IS_WIN = /Windows/.test(navigator.userAgent);
document.documentElement.dataset.os = IS_WIN ? "windows" : "mac";

// modKey — главная клавиша сочетаний: ⌘ на Маке, Ctrl на Windows.
const modKey = (e) => (IS_WIN ? e.ctrlKey : e.metaKey);
// otherMod — та, что на этой платформе в сочетаниях окна не участвует.
const otherMod = (e) => (IS_WIN ? e.metaKey : e.ctrlKey);

// hideWindow — Esc и «спрятать»: окно уходит в меню-бар Мака или в трей
// Windows (menubar_windows.go) и продолжает отвечать телефону.
function hideWindow() {
  if (window.runtime) window.runtime.Hide();
}

// winKeys переводит подпись на Windows: ⌘ → Ctrl+, ⌥ → Alt+, ⇧ → Shift+
// («⌘ ,» и «⌘,» дают одно «Ctrl+,»), «в Finder» → «в Проводнике», Связка
// ключей → Диспетчер учетных данных (keychain_windows.go), Мак → компьютер,
// macOS → Windows, Touch ID → Windows Hello (access_auth_windows.go). Падеж —
// по окончанию; \b с кириллицей не работает, отсюда (?![а-яё]).
const MAC_CASE = { "": "компьютер", "а": "компьютера", "е": "компьютере", "у": "компьютеру", "ом": "компьютером" };
const WIN_WORDS = [
  [/⌘\s?/g, "Ctrl+"], [/⌥\s?/g, "Alt+"], [/⇧\s?/g, "Shift+"],
  [/([вВ]) Finder/g, "$1 Проводнике"],
  [/([кК]) Связке( ключей)?(?![а-яё])/g, "$1 Диспетчеру учетных данных"],
  [/Связк[ау]( ключей)?( macOS)?(?![а-яё])/g, "Диспетчер учетных данных"],
  [/Связки( ключей)?(?![а-яё])/g, "Диспетчера учетных данных"],
  [/Связке( ключей)?(?![а-яё])/g, "Диспетчере учетных данных"],
  [/(?<![А-Яа-яё])Мак(а|е|у|ом)?(?![а-яё])/g, (_, end) => MAC_CASE[end || ""]],
  [/macOS/g, "Windows"],
  [/Touch ID/g, "Windows Hello"],
  // Связка — она, Диспетчер — он: глаголы, что стоят в окне после «Связка».
  [/(Диспетчер учетных данных(?: Windows)?(?: не)? )(ответила|прочлась|отдала|заперта)(?![а-яё])/g,
    (_, head, verb) => head + { "ответила": "ответил", "прочлась": "прочелся", "отдала": "отдал", "заперта": "заперт" }[verb]],
];
function winKeys(s) {
  for (const [re, to] of WIN_WORDS) s = s.replace(re, to);
  return s;
}
const KEY_GLYPH = /[⌘⌥⇧]|[вВ] Finder|Touch ID|Связк|(?<![А-Яа-яё])Мак(а|е|у|ом)?(?![а-яё])|macOS/;
const KEY_ATTRS = ["data-tip", "data-tip-k", "data-tip-b", "aria-label", "title", "placeholder"];
// Здесь Ваши данные, а не подписи окна: «на этом Маке» в документе остается как есть.
const CONTENT = "#results, #preview, #chat-log, #chat-list, #chat-docs-pane, #memory-view, #markets-view, #artifacts-view";

function relabel(node) {
  const el = node.nodeType === Node.TEXT_NODE ? node.parentElement : node;
  if (!el || el.nodeType !== Node.ELEMENT_NODE || el.closest(CONTENT)) return;
  if (node.nodeType === Node.TEXT_NODE) {
    if (KEY_GLYPH.test(node.data)) node.data = winKeys(node.data);
    return;
  }
  const walk = document.createTreeWalker(node, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
    acceptNode: (n) => (n.nodeType === Node.ELEMENT_NODE && n.matches(CONTENT) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT),
  });
  for (let n = node; n; n = walk.nextNode()) {
    if (n.nodeType === Node.TEXT_NODE) {
      if (KEY_GLYPH.test(n.data)) n.data = winKeys(n.data);
      continue;
    }
    for (const a of KEY_ATTRS) {
      const v = n.getAttribute(a);
      if (v && KEY_GLYPH.test(v)) n.setAttribute(a, winKeys(v));
    }
  }
}

if (IS_WIN) {
  document.addEventListener("DOMContentLoaded", () => {
    const caps = document.getElementById("win-caps");
    caps.hidden = false;
    caps.addEventListener("click", (e) => {
      const b = e.target.closest("button");
      if (!b || !window.runtime) return;
      if (b.id === "win-min") window.runtime.WindowMinimise();
      else if (b.id === "win-max") window.runtime.WindowToggleMaximise();
      else if (b.id === "win-close") hideWindow(); // как красная кнопка Мака: в трей, выход — из меню значка
    });
    // Подписи строит и app.js по ходу работы (подсказки, справка, меню):
    // переводим и то, что есть, и все, что появится.
    relabel(document.body);
    new MutationObserver((ms) => {
      for (const m of ms) {
        if (m.type === "attributes") relabel(m.target);
        else if (m.type === "characterData") relabel(m.target);
        else m.addedNodes.forEach(relabel);
      }
    }).observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: KEY_ATTRS });
  });
}
