// Тема окна до первой отрисовки: выбор человека из localStorage ставится на
// <html> раньше стилей, иначе окно мелькнуло бы чужой темой. Нет выбора —
// как в системе (prefers-color-scheme в style.css).
try {
  const t = localStorage.getItem("theme");
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
} catch { /* как в системе */ }
