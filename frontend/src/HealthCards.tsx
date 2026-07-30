import type { Entry, Health } from './api'
import { dateOf } from './catalog'
import { ecg } from './health'
import { Icon } from './components/Icon'

// Длина окружности кольца: r=18 в системе координат 48×48. Захардкожена
// намеренно — она обязана совпадать со strokeDasharray в разметке, и вычислять
// её в двух местах значит однажды разойтись.
const RING = 2 * Math.PI * 18

function Ring({ percent, label, detail, tone }: {
  percent: number
  label: string
  detail: string
  tone: string
}) {
  return (
    <div className="flex items-center gap-4">
      <div className="relative h-14 w-14 shrink-0">
        <svg viewBox="0 0 48 48" className="h-full w-full -rotate-90">
          <circle cx="24" cy="24" r="18" fill="none" stroke="var(--surface-high)" strokeWidth="4" />
          <circle
            cx="24"
            cy="24"
            r="18"
            fill="none"
            stroke={tone}
            strokeWidth="4"
            strokeLinecap="round"
            strokeDasharray={RING}
            // Заполнение идёт от нуля к доле: смещение — это непройденная часть.
            strokeDashoffset={RING - (RING * percent) / 100}
            className="transition-[stroke-dashoffset] duration-1000 ease-out"
          />
        </svg>
        <span className="absolute inset-0 flex items-center justify-center font-label text-[10px] font-bold">
          {percent}%
        </span>
      </div>
      <div>
        <span className="font-label text-xs font-bold text-on-surface">{label}</span>
        <p className="font-label text-[10px] text-on-surface-variant">{detail}</p>
      </div>
    </div>
  )
}

/**
 * Карточка «Здоровье базы»: две доли кольцами, их среднее полосой и пульс на
 * фоне, частота которого растёт вместе со счётом.
 *
 * Все три числа приходят посчитанными с сервера. Здесь только показ — иначе та
 * же арифметика завелась бы вторым экземпляром и однажды разошлась с первым.
 */
export function HealthCard({ health }: { health: Health }) {
  const pct = (part: number) =>
    health.total > 0 ? Math.round((part / health.total) * 100) : 0
  const processed = pct(health.processed)
  const withNotes = pct(health.with_notes)
  const pulse = ecg(health.score)

  return (
    <div className="relative flex flex-col justify-between overflow-hidden rounded-2xl border border-outline-variant bg-surface-highest p-8">
      <div className="pointer-events-none absolute top-0 right-0 left-0 h-[120px] overflow-hidden">
        <svg
          viewBox={`0 0 ${pulse.width} 100`}
          preserveAspectRatio="none"
          style={{ width: '200%', height: '100%', opacity: pulse.opacity }}
        >
          <path
            d={pulse.d}
            fill="none"
            stroke="var(--secondary)"
            strokeWidth={pulse.strokeWidth}
            strokeLinecap="round"
            strokeLinejoin="round"
            className="ecg-line"
            style={{ animationDuration: `${pulse.speed}s` }}
          />
        </svg>
      </div>

      <div className="relative z-10">
        <Icon name="analytics" className="mb-4 text-3xl text-secondary" />
        <h4 className="mb-2 font-headline text-lg font-bold">Здоровье базы</h4>
        <div className="space-y-5">
          <Ring
            percent={processed}
            label="Обработано"
            detail={`${health.processed} из ${health.total}`}
            tone="var(--secondary)"
          />
          <Ring
            percent={withNotes}
            label="С конспектами"
            detail={`${health.with_notes} из ${health.total}`}
            tone="var(--donut-primary)"
          />
          <div className="border-t border-outline-variant pt-3">
            <div className="mb-2 flex items-center justify-between">
              <span className="label">Общее здоровье</span>
              <span className="font-label text-xs font-bold text-secondary">{health.score}%</span>
            </div>
            <div className="h-2 w-full overflow-hidden rounded-full bg-surface-high">
              <div
                className="h-full rounded-full transition-[width] duration-1000 ease-out"
                style={{
                  width: `${health.score}%`,
                  background: 'linear-gradient(90deg, var(--secondary), var(--secondary-light))',
                }}
              />
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

/**
 * Спотлайт «Последнее добавление» — самая свежая запись каталога.
 *
 * Описание сворачивается, а не обрезается насовсем: у длинных разборов первая
 * строка почти ничего не говорит, а карточка на две трети ряда не должна
 * растягиваться на весь экран из-за одной записи.
 */
export function SpotlightCard({
  entry,
  expanded,
  onToggle,
}: {
  entry?: Entry
  expanded: boolean
  onToggle: () => void
}) {
  if (!entry) return null
  const description = entry.description ?? ''
  return (
    <div className="relative col-span-1 overflow-hidden rounded-2xl border border-outline-variant bg-card-spotlight-bg p-8 md:col-span-2">
      <div className="relative z-10 flex h-full flex-col justify-between">
        <div>
          {/* secondary-light, а не secondary: карточка тёмно-синяя, и терракота
              даёт на ней 2.67 — ниже порога даже для крупного текста. */}
          <span className="mb-4 block font-label text-[10px] font-bold tracking-[0.2em] text-secondary-light uppercase">
            Последнее добавление
          </span>
          <h3 className="mb-4 font-headline text-2xl font-bold text-card-spotlight-text">
            {entry.title}
          </h3>
          {description && (
            <p
              className={`text-sm leading-relaxed text-on-primary-container ${
                expanded ? '' : 'line-clamp-3'
              }`}
            >
              {description}
            </p>
          )}
          {description.length > 180 && (
            <button
              type="button"
              onClick={onToggle}
              className="mt-4 inline-flex items-center gap-1 rounded border border-secondary-light px-3 py-1.5 font-label text-[10px] font-bold tracking-wider text-secondary-light uppercase"
            >
              {expanded ? 'Свернуть' : 'Развернуть'}
              <Icon name={expanded ? 'keyboard_arrow_up' : 'keyboard_arrow_down'} className="text-xs" />
            </button>
          )}
        </div>
        {entry.url && (
          <a
            href={entry.url}
            target="_blank"
            rel="noreferrer"
            className="mt-8 flex items-center gap-2 font-label text-sm font-bold text-secondary-light hover:gap-4"
          >
            Открыть <Icon name="arrow_forward" className="text-sm" />
          </a>
        )}
        <p className="mt-6 font-label text-[10px] text-on-primary-container opacity-60">
          Добавлено: {dateOf(entry) || '—'}
        </p>
      </div>
      <div
        className="pointer-events-none absolute -right-20 -bottom-20 h-80 w-80 rounded-full blur-3xl"
        style={{
          background: 'radial-gradient(circle, var(--secondary) 0%, transparent 70%)',
          opacity: 0.1,
        }}
      />
    </div>
  )
}
