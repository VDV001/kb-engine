// Theme selection. The palette itself is generated from design/tokens.json —
// this only decides which of the two sets of custom properties is live, by
// putting the `dark` class on the root element.
//
// The class, not a media query: the same mechanism the Python dashboard uses,
// so a screenshot from either looks like the same product, and a deliberate
// choice can override the system one.

export type Theme = 'light' | 'dark'

// Окно DVK пишет сюда выбор своей кнопки, когда витрина живет в его рамке.
export const storageKey = 'kbengine-theme'

/** resolveTheme picks the theme to open in: a stored choice wins, otherwise
 * the system preference. A stored value this code did not write is ignored —
 * localStorage is shared with every other page on the origin. */
export function resolveTheme(stored: string | null, prefersDark: boolean): Theme {
  if (stored === 'light' || stored === 'dark') return stored
  return prefersDark ? 'dark' : 'light'
}

export function nextTheme(current: Theme): Theme {
  return current === 'dark' ? 'light' : 'dark'
}

/** initialTheme reads the two ambient inputs. Kept separate from resolveTheme
 * so the decision stays testable without a browser. */
export function initialTheme(): Theme {
  return resolveTheme(
    localStorage.getItem(storageKey),
    window.matchMedia('(prefers-color-scheme: dark)').matches,
  )
}

/** applyTheme makes the choice visible. It does not remember it: opening the
 * page is not a choice, and storing the system theme here once pinned it —
 * the page stopped following the system and the DVK window. */
export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle('dark', theme === 'dark')
  // color-scheme so the browser's own furniture — scrollbars, form controls,
  // the canvas behind an overscroll — matches the page instead of staying
  // light under a dark dashboard.
  document.documentElement.style.colorScheme = theme
}

/** saveTheme remembers a deliberate choice — a click on the toggle. */
export function saveTheme(theme: Theme): void {
  localStorage.setItem(storageKey, theme)
}

/** watchTheme calls onChange when the theme to show changes from outside:
 * another page on the origin wrote the choice (the DVK window around this
 * page, or another tab), or the system switched while nothing is stored.
 * Returns the unsubscribe. */
export function watchTheme(onChange: (theme: Theme) => void): () => void {
  // key === null — localStorage.clear(): выбора больше нет, тоже смена.
  const onStorage = (e: StorageEvent) => {
    if (e.key === storageKey || e.key === null) onChange(initialTheme())
  }
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  const onSystem = () => {
    if (localStorage.getItem(storageKey) === null) onChange(initialTheme())
  }
  window.addEventListener('storage', onStorage)
  mq.addEventListener('change', onSystem)
  return () => {
    window.removeEventListener('storage', onStorage)
    mq.removeEventListener('change', onSystem)
  }
}
