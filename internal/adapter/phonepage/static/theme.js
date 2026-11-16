// Тема страницы до первой отрисовки, как theme.js окна на Маке: выбор человека
// из localStorage ставится на <html> раньше стилей, иначе страница мелькнула бы
// чужой темой. Нет выбора — как в системе (prefers-color-scheme в style.css).
try {
  const t = localStorage.getItem("theme");
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
} catch { /* как в системе */ }
