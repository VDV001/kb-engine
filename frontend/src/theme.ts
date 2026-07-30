// Theme selection. The palette itself is generated from design/tokens.json —
// this only decides which of the two sets of custom properties is live, by
// putting the `dark` class on the root element.
//
// The class, not a media query: the same mechanism the Python dashboard uses,
// so a screenshot from either looks like the same product, and a deliberate
// choice can override the system one.

export type Theme = 'light' | 'dark'

const storageKey = 'kbengine-theme'

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

/** applyTheme makes the choice visible and remembers it. */
export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle('dark', theme === 'dark')
  // color-scheme so the browser's own furniture — scrollbars, form controls,
  // the canvas behind an overscroll — matches the page instead of staying
  // light under a dark dashboard.
  document.documentElement.style.colorScheme = theme
  localStorage.setItem(storageKey, theme)
}
