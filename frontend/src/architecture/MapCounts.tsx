import type { ArchMap } from '../api'
import { mapCounts } from '../architecture'

/**
 * Строка счётчиков — та же, что печатала страница-предшественник.
 *
 * Половина из них не про объём, а про границу знания: сколько узлов стоит на
 * якоре, сколько шагов несёт источник, сколько связей не выведено из кода,
 * сколько узлов в сломанном положении. Без них объём читается как полнота, а
 * схема, нарисованная по памяти, выглядит так же убедительно, как собранная по
 * коду.
 */
export function MapCounts({ map }: { map: ArchMap }) {
  const c = mapCounts(map)
  const items: { label: string; value: string; tone?: 'warn' | 'bad' }[] = [
    { label: 'узлов', value: String(c.nodes) },
    { label: 'из них с якорем', value: `${c.nodesWithAnchor} из ${c.nodes}` },
    { label: 'сценариев', value: String(c.flows) },
    { label: 'шагов', value: String(c.steps) },
    { label: 'шагов с источником', value: `${c.stepsWithSource} из ${c.steps}` },
    { label: 'связь не подтверждена', value: String(c.unverified), tone: 'warn' },
  ]
  // Три счётчика показываются, только если карта такое вообще ведёт: ноль
  // находок у карты без раздела находок означал бы «ничего не нашли».
  if (c.broken > 0) items.push({ label: 'узлов в сломанном состоянии', value: String(c.broken), tone: 'bad' })
  if (map.findings.length > 0) items.push({ label: 'находок', value: String(c.findings), tone: 'bad' })
  items.push({ label: 'проверок прогоном', value: String(c.runtimeChecks) })
  if (map.gaps.length > 0) items.push({ label: 'пунктов «чего нет»', value: String(c.gaps) })

  return (
    <div className="flex flex-wrap gap-x-5 gap-y-2 rounded-lg border border-outline-variant bg-surface-lowest px-4 py-3 text-xs">
      {items.map((it) => (
        <span
          key={it.label}
          className={
            it.tone === 'bad'
              ? 'text-tag-text-4'
              : it.tone === 'warn'
                ? 'text-secondary'
                : 'text-on-surface-variant'
          }
        >
          {it.label} <b className="font-mono tabular-nums">{it.value}</b>
        </span>
      ))}
    </div>
  )
}
