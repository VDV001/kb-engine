// Тема страницы до первой отрисовки, как theme.js окна на Маке: выбор человека
// из localStorage ставится на <html> раньше стилей, иначе страница мелькнула бы
// чужой темой. Нет выбора — как в системе (prefers-color-scheme в style.css).
// Полоса состояния — тоже сразу: иначе она мелькнула бы цветом системы до
// загрузки app.js (цвета — --z-bg из style.css, сверяет state_test).
try {
  const t = localStorage.getItem("theme");
  if (t === "light" || t === "dark") {
    document.documentElement.dataset.theme = t;
    for (const m of document.querySelectorAll('meta[name="theme-color"]')) m.content = t === "dark" ? "#0b0b0c" : "#ffffff";
  }
} catch { /* как в системе */ }
