import type { ReactNode } from 'react'

// Reusable presentational primitives shared across every view.

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <div className={`rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-700 dark:bg-slate-800 ${className}`}>
      {children}
    </div>
  )
}

export function Section({ title, subtitle, children }: { title: string; subtitle?: string; children: ReactNode }) {
  return (
    <section className="space-y-3">
      <div>
        <h2 className="text-lg font-semibold text-slate-800 dark:text-slate-100">{title}</h2>
        {subtitle && <p className="text-sm text-slate-500">{subtitle}</p>}
      </div>
      {children}
    </section>
  )
}

const badgeColors: Record<string, string> = {
  keep: 'bg-emerald-100 text-emerald-700',
  napodumat: 'bg-amber-100 text-amber-700',
  skip: 'bg-slate-200 text-slate-600',
  'skip-unavailable': 'bg-rose-100 text-rose-700',
  active: 'bg-sky-100 text-sky-700',
  canonical: 'bg-violet-100 text-violet-700',
  outdated: 'bg-orange-100 text-orange-700',
  superseded: 'bg-slate-200 text-slate-600',
  'dead-end': 'bg-rose-100 text-rose-700',
}

export function Badge({ value }: { value: string }) {
  const color = badgeColors[value] ?? 'bg-slate-100 text-slate-600'
  return <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${color}`}>{value}</span>
}

// BarList renders a sorted horizontal bar chart from a label→count map.
// valueClassName lets a caller style just the numbers — the finances view masks
// the amounts while leaving the labels and the bar shapes readable.
export function BarList({ data, valueClassName = '' }: { data: Record<string, number>; valueClassName?: string }) {
  const entries = Object.entries(data).sort((a, b) => b[1] - a[1])
  const max = Math.max(1, ...entries.map(([, n]) => n))
  return (
    <div className="space-y-1.5">
      {entries.map(([label, n]) => (
        <div key={label} className="flex items-center gap-2 text-sm">
          <span className="w-40 shrink-0 truncate text-slate-600 dark:text-slate-300" title={label}>
            {label}
          </span>
          <div className="h-4 flex-1 rounded bg-slate-100 dark:bg-slate-700">
            <div className="h-4 rounded bg-sky-500" style={{ width: `${(n / max) * 100}%` }} />
          </div>
          <span className={`w-10 shrink-0 text-right tabular-nums text-slate-500 ${valueClassName}`}>{n}</span>
        </div>
      ))}
    </div>
  )
}

// value is a ReactNode so a caller can wrap it — the finances view puts its
// amounts in a maskable span.
export function Stat({ label, value }: { label: string; value: ReactNode }) {
  return (
    <Card>
      <div className="text-3xl font-bold tabular-nums text-slate-800 dark:text-slate-100">{value}</div>
      <div className="text-sm text-slate-500">{label}</div>
    </Card>
  )
}

export function Spinner() {
  return <div className="p-8 text-center text-slate-400">Загрузка…</div>
}

export function ErrorBox({ message }: { message: string }) {
  return <div className="rounded-lg bg-rose-50 p-4 text-sm text-rose-700">Ошибка: {message}</div>
}
