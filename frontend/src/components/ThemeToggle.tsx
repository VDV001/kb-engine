import { useEffect, useState } from 'react'
import { Icon } from './Icon'
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
      // Ни фона, ни рамки: в шапке это одна из нескольких иконок, и единственная
      // в рамке читалась как кнопка другого рода. Размер общий для всех — h-9,
      // иконка text-xl; раньше здесь стоял текстовый символ ☾, который жил в
      // своём масштабе и ломал ряд.
      className="flex h-9 w-9 items-center justify-center text-on-surface-variant transition-colors hover:text-on-surface"
    >
      <Icon name={theme === 'dark' ? 'light_mode' : 'dark_mode'} className="text-xl" />
    </button>
  )
}
