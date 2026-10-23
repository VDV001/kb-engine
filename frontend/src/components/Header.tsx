import { useLayoutEffect, useRef, useState } from 'react'
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
/** Число ждущей работы рядом с названием вкладки. Тон приглушённый: это
 * счётчик, а не тревога — база живёт и с полусотней кандидатов в аудите. */
function TabBadge({ value }: { value: number }) {
  return (
    <span className="ml-1.5 rounded-full bg-surface-high px-1.5 py-0.5 font-mono text-[10px] tabular-nums text-on-surface-variant">
      {value}
    </span>
  )
}

export function Header<T extends string>({
  tabs,
  current,
  onSelect,
  count,
  extra,
}: {
  /** badge — число, которое вкладка несёт с собой: сколько там ждёт работы. */
  tabs: { id: T; label: string; badge?: number }[]
  current: T
  onSelect: (id: T) => void
  count?: number
  /** Управление, относящееся к текущему виду: на финансах — переключатель сумм. */
  extra?: React.ReactNode
}) {
  const ref = useRef<HTMLElement>(null)
  const navRef = useRef<HTMLElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const fits = useFits(navRef, listRef)
  const collapsed = !fits
  // Высота шапки уезжает в CSS-переменную: прилипающие полосы внутри страниц
  // встают под ней, не завися от захардкоженного числа.
  useCssVarHeight(ref, '--nav-h')

  return (
    <header ref={ref} className="no-print sticky top-0 z-50 border-b border-nav-border bg-nav-bg backdrop-blur-xl">
      {/* Промежутки по горизонтали — отступами, а не gap: свёрнутая навигация
          стоит в строке, чтобы её мерить, и gap добавлял бы вокруг неё 24 px,
          которых на 360–390 px не хватало — шапка ломалась на два ряда. */}
      <div className="mx-auto flex max-w-screen-2xl flex-wrap items-center gap-y-3 px-4 py-3 sm:px-6 lg:px-8">
        <a
          href="#"
          onClick={(e) => {
            e.preventDefault()
            onSelect(tabs[0].id)
          }}
          className="mr-3 shrink-0 sm:mr-6 lg:mr-12"
        >
          <Logo />
        </a>

        {/* Навигация или бургер — решает замер, а не порог ширины экрана.
            Порог xl когда-то посчитали под десять вкладок (~1138 px, на
            финансах ~1250). Вкладок стало двенадцать, и при 1280–1440 px, а на
            большом мониторе — при масштабе браузера, пункты ложились поверх
            переключателя, темы и счётчика. Список меряется в месте, которое ему
            оставлено: не помещается — строка прячется (место за ней остаётся,
            чтобы мерить дальше), пункты уходят в бургер. Новая вкладка или
            переключатель на одном виде больше ничего не ломают. */}
        <nav
          ref={navRef}
          aria-hidden={collapsed ? true : undefined}
          className={`min-w-0 flex-1 overflow-hidden ${collapsed ? 'invisible' : 'mr-6 lg:mr-12'}`}
        >
          <ul ref={listRef} className="flex w-max items-center gap-8 whitespace-nowrap px-1">
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
                    {t.badge ? <TabBadge value={t.badge} /> : null}
                  </button>
                </li>
              )
            })}
          </ul>
        </nav>

        <div className="ml-auto flex shrink-0 items-center gap-3">
          {collapsed && (
            <button
              type="button"
              onClick={() => setMenuOpen((v) => !v)}
              aria-expanded={menuOpen}
              aria-label={menuOpen ? 'Закрыть меню' : 'Открыть меню'}
              className="flex h-9 w-9 items-center justify-center rounded-md text-on-surface-variant hover:bg-surface-high hover:text-on-surface"
            >
              <Icon name={menuOpen ? 'close' : 'menu'} className="text-xl" />
            </button>
          )}
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
      {menuOpen && collapsed && (
        <div className="border-t border-outline-variant bg-nav-bg backdrop-blur-xl">
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
                    {t.badge ? <TabBadge value={t.badge} /> : null}
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

// useFits отвечает, помещается ли список во всю ширину своего контейнера.
// Пересчитывается при любом изменении размеров обоих: окно, масштаб браузера,
// шрифт, новая вкладка, переключатель на текущем виде.
function useFits(container: React.RefObject<HTMLElement | null>, content: React.RefObject<HTMLElement | null>) {
  const [fits, setFits] = useState(true)
  useLayoutEffect(() => {
    const box = container.current
    const list = content.current
    if (!box || !list) return
    const check = () => setFits(list.scrollWidth <= box.clientWidth)
    check()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(check)
    ro.observe(box)
    ro.observe(list)
    return () => ro.disconnect()
  }, [container, content])
  return fits
}
