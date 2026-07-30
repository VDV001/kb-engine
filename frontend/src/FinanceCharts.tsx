import type { Bar } from './financeSeries'
import { formatRub } from './money'

// Графики финансов. Ни canvas, ни библиотеки: в исходном дашборде они тоже
// нарисованы блоками и CSS, и переносить это в React можно один в один — а
// значит без ещё одной зависимости в бинаре, который встраивает фронт целиком.

/** Строка списка: имя, доля полосой, сумма и процент. */
export interface Share {
  name: string
  title?: string
  kopecks: number
}

/**
 * Список долей. Один компонент на три разреза — места, источники оплаты,
 * потоки доходов, — потому что форма у них одна: имя, сколько, какая доля.
 *
 * Доля считается от суммы показанного, а не от «всех расходов»: у источников
 * оплаты заполнено меньше половины строк, и процент от общего расхода читался
 * бы как «остальное непонятно куда», хотя это просто незаполненное поле.
 */
export function ShareBars({ items, limit = 15 }: { items: Share[]; limit?: number }) {
  const shown = items.slice(0, limit)
  if (shown.length === 0) {
    return <p className="py-6 text-center text-sm text-on-surface-variant">Нет данных</p>
  }
  const max = Math.max(1, ...shown.map((i) => i.kopecks))
  const total = shown.reduce((n, i) => n + i.kopecks, 0)
  return (
    <div className="space-y-2.5">
      {shown.map((i) => (
        <div key={i.name} className="flex items-center gap-3 text-sm">
          <span className="w-44 shrink-0 truncate text-right text-on-surface-variant" title={i.title ?? i.name}>
            {i.name}
          </span>
          <div className="h-2 min-w-0 flex-1 rounded-full bg-surface-high">
            {/* Ширина зажата снизу нулём: отрицательная ширина невалидна, браузер
                откатывается на auto, и блок растягивается на всю строку — возврат,
                перевесивший покупки, нарисовался бы самой длинной полосой. */}
            <div
              className={`h-2 rounded-full ${i.kopecks < 0 ? 'bg-secondary-light' : 'bg-donut-primary'}`}
              style={{ width: `${Math.max(0, i.kopecks / max) * 100}%` }}
            />
          </div>
          <span className="privacy-mask w-24 shrink-0 text-right font-mono text-xs tabular-nums text-on-surface">
            {formatRub(i.kopecks)}
          </span>
          <span className="w-10 shrink-0 text-right font-mono text-[10px] tabular-nums text-on-surface-variant">
            {total > 0 ? `${Math.round((i.kopecks / total) * 100)}%` : '—'}
          </span>
        </div>
      ))}
    </div>
  )
}

/**
 * Столбчатый график периодов. Используется и помесячной динамикой, и плотностью
 * за 31 день: разница только в подготовке данных, а не в отрисовке.
 *
 * Нули остаются в ряду. Пропуск — это тоже сведение: выкинув пустые дни, три
 * разрозненные покупки нарисуешь ровной неделей.
 */
export function PeriodBars({ bars, height = 'h-52' }: { bars: Bar[]; height?: string }) {
  if (bars.length === 0) {
    return <p className="py-6 text-center text-sm text-on-surface-variant">Нет данных</p>
  }
  const max = Math.max(1, ...bars.map((b) => b.kopecks))
  return (
    <div>
      <div className={`flex items-end justify-between gap-[3px] ${height}`}>
        {bars.map((b) => (
          <div key={b.key} className="group relative flex h-full min-w-0 flex-1 flex-col justify-end">
            <div
              className={`w-full rounded-t transition-[height] ${
                b.current ? 'bg-secondary' : b.kopecks > 0 ? 'bg-donut-primary' : 'bg-surface-high'
              }`}
              // Минимум 2% — иначе день без трат выглядит как отсутствующий
              // столбец, и ряд читается короче, чем он есть.
              style={{ height: `${Math.max((b.kopecks / max) * 100, 2)}%` }}
            />
            <span className="privacy-mask pointer-events-none absolute -top-6 left-1/2 hidden -translate-x-1/2 whitespace-nowrap rounded bg-surface-highest px-1.5 py-0.5 font-mono text-[10px] tabular-nums text-on-surface group-hover:block">
              {formatRub(b.kopecks)}
            </span>
          </div>
        ))}
      </div>
      <div className="mt-2 flex justify-between font-mono text-[10px] text-on-surface-variant">
        <span>{bars[0].label}</span>
        <span>{bars[bars.length - 1].label}</span>
      </div>
    </div>
  )
}
