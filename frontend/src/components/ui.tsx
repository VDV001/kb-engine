import type { ReactNode } from 'react'

// Presentational primitives, in the visual language of the Python dashboard.
//
// Three things carry that language and every primitive here obeys them:
// hairline borders instead of shadows, a tiny letter-spaced label above a large
// figure rather than beside it, and a rhythm of surfaces — most cards light,
// one dark — so a row of them reads as a composition instead of a fence.
//
// Every colour is a token from design/tokens.json. Nothing here names a shade.
//
// Corners follow one rule, so that radius means something rather than being a
// habit: a radius marks an object you could pick up, a square corner marks
// structure the page is built out of.
//
//   rounded-lg   cards and panels — discrete things resting on the background
//   rounded-md   controls — buttons, chips, inputs; smaller because they are
//   rounded-full capsules by nature — badges, progress bars
//   square       anything that spans an edge or tiles a grid: the header bar,
//                the sidebar, cells inside a divided grid, table rows
//
// The test for a new block is which of the two it is. A ring inside a grid of
// rings is structure and stays square even though it sits in a box; a single
// ring on the page would be an object and would round.

/** Card tones. The token source carries three KPI surfaces and a spotlight
 * because the dashboard they were lifted from uses exactly this rhythm: two
 * quiet cards, then one that stops you. */
type Tone = 'plain' | 'muted' | 'spotlight'

// The spotlight uses the kpi-3 roles rather than the spotlight card's own,
// because those are the ones that invert: black on paper in the light theme,
// paper on black in the dark one. card-spotlight-bg is #1F1F1F in the dark
// theme, a shade away from the plain cards, so the emphasis vanished exactly
// where it was needed.
const toneClass: Record<Tone, string> = {
  plain: 'bg-surface-lowest border-outline-variant',
  muted: 'bg-kpi-2-bg border-transparent',
  spotlight: 'bg-kpi-3-bg border-transparent text-kpi-3-text',
}

export function Card({
  children,
  tone = 'plain',
  className = '',
}: {
  children: ReactNode
  tone?: Tone
  className?: string
}) {
  return (
    <div className={`rounded-lg border p-5 ${toneClass[tone]} ${className}`}>{children}</div>
  )
}

/** Label is the small letter-spaced caps that sits above a figure. It carries
 * more of the design than its size suggests: the contrast between 11px tracked
 * caps and a 40px number is what makes the number look deliberate. */
export function Label({ children, className = '' }: { children: ReactNode; className?: string }) {
  // The default colour lives here rather than in the .label rule, so a caller
  // on an inverted surface can change it — and it is dropped rather than
  // appended when they do. Two colour utilities in one class attribute do not
  // resolve by the order they are written; the one that appears later in the
  // generated stylesheet wins, which makes the override a coin toss.
  const hasColour = /(^|\s)text-(?!xs|sm|base|lg|xl|\dxl|left|center|right|nowrap)/.test(className)
  return (
    <div className={`label ${hasColour ? '' : 'text-on-surface-variant'} ${className}`}>
      {children}
    </div>
  )
}

export function Section({
  title,
  subtitle,
  children,
  aside,
}: {
  title: string
  subtitle?: string
  children: ReactNode
  aside?: ReactNode
}) {
  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-2xl">{title}</h2>
          {subtitle && <p className="mt-1 text-sm text-on-surface-variant">{subtitle}</p>}
        </div>
        {aside}
      </div>
      {children}
    </section>
  )
}

// Badges reuse the tag and status roles rather than inventing a palette. The
// four tag pairs already read as distinct in both themes, which a hand-picked
// set of Tailwind hues would have to be re-checked for.
const badgeTone: Record<string, string> = {
  keep: 'bg-tag-bg-2 text-tag-text-2',
  active: 'bg-tag-bg-2 text-tag-text-2',
  canonical: 'bg-tag-bg-1 text-tag-text-1',
  consider: 'bg-tag-bg-3 text-tag-text-3',
  skip: 'bg-tag-bg-3 text-tag-text-3',
  superseded: 'bg-tag-bg-3 text-tag-text-3',
  outdated: 'bg-tag-bg-3 text-tag-text-3',
  'dead-end': 'bg-tag-bg-4 text-tag-text-4',
  'skip-unavailable': 'bg-tag-bg-4 text-tag-text-4',
}

export function Badge({ value }: { value: string }) {
  return (
    <span
      className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${
        badgeTone[value] ?? 'bg-tag-bg-3 text-tag-text-3'
      }`}
    >
      {value}
    </span>
  )
}

/** Chip is the filter pill: filled in the accent when chosen, hairline when not. */
export function Chip({
  active,
  children,
  onClick,
}: {
  active?: boolean
  children: ReactNode
  onClick?: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`rounded-md px-3 py-1.5 text-xs font-medium tracking-wide uppercase transition-colors ${
        active
          ? 'bg-secondary text-white'
          : 'border border-outline-variant text-on-surface-variant hover:bg-surface-high hover:text-on-surface'
      }`}
    >
      {children}
    </button>
  )
}

// BarList renders a sorted horizontal bar chart from a label→count map.
// valueClassName lets a caller style just the numbers — the finances view masks
// the amounts while leaving the labels and the bar shapes readable.
export function Stat({
  label,
  value,
  hint,
  tone = 'plain',
}: {
  label: string
  value: ReactNode
  hint?: ReactNode
  tone?: Tone
}) {
  return (
    <Card tone={tone}>
      <Label className={tone === 'spotlight' ? 'text-kpi-3-sub' : ''}>{label}</Label>
      <div className="mt-2 text-4xl font-bold tracking-tight tabular-nums">{value}</div>
      {hint && (
        <div
          className={`mt-1 text-xs ${
            tone === 'spotlight' ? 'text-kpi-3-sub' : 'text-on-surface-variant'
          }`}
        >
          {hint}
        </div>
      )}
    </Card>
  )
}

/**
 * Ring is the thin donut the finances view uses one of per category.
 *
 * Drawn with a dash on a stroked circle rather than an arc path: the geometry
 * is one number instead of trigonometry, and it stays correct at any size.
 */
export function Ring({ percent, label }: { percent: number; label: string }) {
  const p = Math.max(0, Math.min(100, percent))
  const r = 30
  const circumference = 2 * Math.PI * r
  return (
    <div className="flex flex-col items-center gap-3 p-4">
      <svg viewBox="0 0 80 80" className="h-20 w-20 -rotate-90" role="img" aria-label={`${label}: ${p}%`}>
        <circle cx="40" cy="40" r={r} fill="none" stroke="var(--surface-high)" strokeWidth="6" />
        <circle
          cx="40"
          cy="40"
          r={r}
          fill="none"
          stroke="var(--secondary)"
          strokeWidth="6"
          strokeLinecap="round"
          strokeDasharray={`${(p / 100) * circumference} ${circumference}`}
        />
      </svg>
      {/* One line, always. A long name wrapping pushes its own percentage down
          and the row stops lining up — the figures are the thing being
          compared, so they are what has to stay on a common baseline. */}
      <div className="w-full min-w-0 text-center">
        <Label className="truncate">
          <span title={label}>{label}</span>
        </Label>
        <div className="mt-0.5 font-mono text-sm tabular-nums">{p}%</div>
      </div>
    </div>
  )
}

export function Spinner() {
  return <div className="p-12 text-center text-on-surface-variant">Загрузка…</div>
}

export function ErrorBox({ message }: { message: string }) {
  return (
    <div className="rounded-lg bg-error-container p-4 text-sm text-on-error-container">
      Ошибка: {message}
    </div>
  )
}
