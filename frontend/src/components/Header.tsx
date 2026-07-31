import { useRef, useState } from 'react'
import { useCssVarHeight } from '../hooks/useCssVarHeight'
import { Icon } from './Icon'
import { Logo } from './Logo'
import { ThemeToggle } from './ThemeToggle'

/**
 * The top bar, built to match the Python dashboard so the two read as one
 * product: mark, navigation and controls on a single line, inside a centred
 * container rather than stretched across the viewport.
 *
 * Sticky rather than fixed. Fixed takes the bar out of flow and the page then
 * needs a padding that has to be kept equal to a height nobody remembers to
 * measure again; sticky keeps the same effect and cannot drift.
 */
export function Header<T extends string>({
  tabs,
  current,
  onSelect,
  count,
  extra,
}: {
  tabs: { id: T; label: string }[]
  current: T
  onSelect: (id: T) => void
  count?: number
  /** Управление, относящееся к текущему виду: на финансах — переключатель сумм. */
  extra?: React.ReactNode
}) {
  const ref = useRef<HTMLElement>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  // Высота шапки уезжает в CSS-переменную: прилипающие полосы внутри страниц
  // встают под ней, не завися от захардкоженного числа.
  useCssVarHeight(ref, '--nav-h')

  return (
    <header ref={ref} className="no-print sticky top-0 z-50 border-b border-nav-border bg-nav-bg backdrop-blur-xl">
      <div className="mx-auto flex max-w-screen-2xl flex-wrap items-center gap-x-6 gap-y-3 px-4 py-3 sm:px-6 lg:gap-x-12 lg:px-8">
        <a
          href="#"
          onClick={(e) => {
            e.preventDefault()
            onSelect(tabs[0].id)
          }}
          className="shrink-0"
        >
          <Logo />
        </a>

        {/* До lg навигация уезжает в бургер, а не на второй ряд.
            Раньше здесь стояло обратное решение — второй ряд, потому что
            «спрятать навигацию значит оставить окно без неё». Бургер эту
            причину снимает: навигация не исчезает, а сворачивается, зато
            шапка остаётся одноэтажной, а второй этаж съедал полосу высотой
            в 41 пиксель на каждом экране уже кого угодно ноутбука.

            Порог xl, а не lg, посчитан: десять вкладок занимают 841px, логотип
            и правые элементы — 137, промежутки 96, паддинги 64, итого ~1138.
            На финансах в шапке ещё переключатель сумм, и нужно уже ~1250.
            На lg (1024) навигация не влезала и начинала скроллиться вбок —
            прокрутка внутри шапки хуже бургера. Горизонтальной прокрутки тут
            больше нет: не влезает — значит в бургер. */}
        <nav className="hidden min-w-0 flex-1 xl:block">
          <ul className="flex items-center gap-5 whitespace-nowrap px-1 xl:gap-8">
            {tabs.map((t) => {
              const active = t.id === current
              return (
                <li key={t.id}>
                  <button
                    type="button"
                    onClick={() => onSelect(t.id)}
                    aria-current={active ? 'page' : undefined}
                    // Активный пункт — тёмный текст с цветной чертой, а не
                    // цветной текст: красить и то, и другое значит заставить
                    // акцент кричать и не оставить признака текущей страницы.
                    className={`border-b-2 pb-1 text-sm font-medium transition-colors ${
                      active
                        ? 'border-secondary text-on-surface'
                        : 'border-transparent text-on-surface-variant hover:text-on-surface'
                    }`}
                  >
                    {t.label}
                  </button>
                </li>
              )
            })}
          </ul>
        </nav>

        <div className="ml-auto flex shrink-0 items-center gap-3">
          <button
            type="button"
            onClick={() => setMenuOpen((v) => !v)}
            aria-expanded={menuOpen}
            aria-label={menuOpen ? 'Закрыть меню' : 'Открыть меню'}
            className="flex h-9 w-9 items-center justify-center rounded-md text-on-surface-variant hover:bg-surface-high hover:text-on-surface xl:hidden"
          >
            <Icon name={menuOpen ? 'close' : 'menu'} className="text-xl" />
          </button>
          {extra}
          <ThemeToggle />
          {count !== undefined && (
            <span
              // Высота как у иконок ряда — 9. Раньше бейдж набирал её из
              // собственных паддингов и вставал чуть выше остальных.
              className="flex h-9 items-center rounded-md bg-secondary px-2.5 font-mono text-xs tabular-nums text-white"
              title="Записей в каталоге"
            >
              {count}
            </span>
          )}
        </div>
      </div>

      {/* Панель бургера: под шапкой, на всю ширину. Закрывается выбором —
          оставлять её открытой после перехода незачем, страница уже сменилась. */}
      {menuOpen && (
        <div className="border-t border-outline-variant bg-nav-bg backdrop-blur-xl xl:hidden">
          <ul className="mx-auto max-w-screen-2xl px-4 py-2 sm:px-6">
            {tabs.map((t) => {
              const active = t.id === current
              return (
                <li key={t.id}>
                  <button
                    type="button"
                    onClick={() => {
                      onSelect(t.id)
                      setMenuOpen(false)
                    }}
                    aria-current={active ? 'page' : undefined}
                    className={`w-full border-l-2 px-3 py-2.5 text-left text-sm font-medium transition-colors ${
                      active
                        ? 'border-secondary bg-surface-high text-on-surface'
                        : 'border-transparent text-on-surface-variant hover:bg-surface-low hover:text-on-surface'
                    }`}
                  >
                    {t.label}
                  </button>
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </header>
  )
}
