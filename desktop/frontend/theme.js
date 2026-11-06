// Тема окна до первой отрисовки: выбор человека из localStorage ставится на
// <html> раньше стилей, иначе окно мелькнуло бы чужой темой. Нет выбора —
// как в системе (prefers-color-scheme в style.css).
//
// Витрина «Финансов» в рамке того же адреса читает свой ключ kbengine-theme
// и ловит его смену событием storage. Окно главное: его выбор переписывает
// ключ витрины, и на старте тоже — там мог остаться выбор прежней витрины,
// которая записывала тему уже при открытии.
const FRAME_THEME_KEY = "kbengine-theme";
function rememberTheme(t) {
  try {
    if (t === "light" || t === "dark") {
      localStorage.setItem("theme", t);
      localStorage.setItem(FRAME_THEME_KEY, t);
    } else {
      localStorage.removeItem("theme");
      localStorage.removeItem(FRAME_THEME_KEY);
    }
  } catch { /* только на сеанс */ }
}
try {
  const t = localStorage.getItem("theme");
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
  rememberTheme(t);
} catch { /* как в системе */ }
