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
}: {
  tabs: { id: T; label: string }[]
  current: T
  onSelect: (id: T) => void
  count?: number
}) {
  return (
    <header className="sticky top-0 z-50 border-b border-nav-border bg-nav-bg backdrop-blur-xl">
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

        {/* The nav scrolls sideways rather than wrapping or disappearing below
            the medium breakpoint: hiding it leaves a narrow window with no way
            to navigate, and wrapping turns the bar into three ragged lines. */}
        <nav className="-mx-1 order-last w-full overflow-x-auto md:order-none md:mx-0 md:w-auto md:flex-1 md:overflow-visible">
          <ul className="flex items-center gap-5 whitespace-nowrap px-1 lg:gap-8">
            {tabs.map((t) => {
              const active = t.id === current
              return (
                <li key={t.id}>
                  <button
                    type="button"
                    onClick={() => onSelect(t.id)}
                    aria-current={active ? 'page' : undefined}
                    // The active item is dark text with a coloured rule, not
                    // coloured text: colouring both makes the accent shout and
                    // leaves nothing to mark the page you are on.
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
          <ThemeToggle />
          {count !== undefined && (
            <span
              className="rounded-md bg-secondary px-2 py-1 font-mono text-xs tabular-nums text-white"
              title="Записей в каталоге"
            >
              {count}
            </span>
          )}
        </div>
      </div>
    </header>
  )
}
