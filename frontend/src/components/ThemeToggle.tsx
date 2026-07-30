import { useEffect, useState } from 'react'
import { applyTheme, initialTheme, nextTheme, type Theme } from '../theme'

/**
 * ThemeToggle switches the palette and remembers the choice.
 *
 * The initial value is read once, synchronously, rather than in an effect: an
 * effect runs after the first paint, which is long enough to see the light
 * theme flash before the dark one arrives.
 */
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(initialTheme)

  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  return (
    <button
      type="button"
      onClick={() => setTheme(nextTheme)}
      // The label says what the button does, not what the state is — a moon
      // icon alone reads as "you are in dark mode" to about half of people.
      aria-label={theme === 'dark' ? 'Включить светлую тему' : 'Включить тёмную тему'}
      title={theme === 'dark' ? 'Светлая тема' : 'Тёмная тема'}
      className="rounded-md border border-outline-variant bg-surface-low px-2.5 py-1.5 text-on-surface-variant transition-colors hover:bg-surface-high hover:text-on-surface"
    >
      {theme === 'dark' ? '☀' : '☾'}
    </button>
  )
}
