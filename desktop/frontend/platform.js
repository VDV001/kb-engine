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

// hideWindow — Esc и «спрятать»: на Маке окно уходит в меню-бар, на Windows
// значка в трее пока нет, и спрятанное окно было бы не вернуть — сворачиваем.
function hideWindow() {
  if (!window.runtime) return;
  if (IS_WIN) window.runtime.WindowMinimise();
  else window.runtime.Hide();
}

// winKeys переводит подпись на Windows: ⌘ → Ctrl+, ⌥ → Alt+, ⇧ → Shift+
// («⌘ ,» и «⌘,» дают одно «Ctrl+,»), «в Finder» → «в Проводнике».
// Связку ключей и Touch ID не трогает: их подписи сменятся вместе с заменой.
function winKeys(s) {
  return s.replace(/⌘\s?/g, "Ctrl+").replace(/⌥\s?/g, "Alt+").replace(/⇧\s?/g, "Shift+").replace(/([вВ]) Finder/g, "$1 Проводнике");
}
const KEY_GLYPH = /[⌘⌥⇧]|[вВ] Finder/;
const KEY_ATTRS = ["data-tip", "data-tip-k", "data-tip-b", "aria-label", "title", "placeholder"];

function relabel(node) {
  if (node.nodeType === Node.TEXT_NODE) {
    if (KEY_GLYPH.test(node.data)) node.data = winKeys(node.data);
    return;
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return;
  const walk = document.createTreeWalker(node, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
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
      else if (b.id === "win-close") window.runtime.Quit();
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
