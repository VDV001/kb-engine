import { useRef } from 'react'
import type { ArchFlow, ArchMap } from '../api'
import { isBrokenKind, kindGlyph, kindLabel, laneColor, legendKinds } from '../architecture'
import { useWires } from '../hooks/useWires'

/**
 * Поле схемы: слои колонками, узлы карточками, шаги выбранного сценария —
 * стрелками между ними.
 *
 * Это главная часть карты и единственная, которую нельзя заменить списком.
 * Список отвечает на вопрос «что здесь есть», схема — на вопрос «как одно
 * доходит до другого», а именно его и задают, открывая карту чужого проекта.
 *
 * Пока сценарий не выбран, поле показывает всех участников поровну. Выбор
 * гасит непричастных: сорок сценариев, нарисованных разом, дают клубок, в
 * котором не виден ни один.
 */
export function SchemeField({
  map,
  flow,
  hotStep,
  onHotStep,
}: {
  map: ArchMap
  flow?: ArchFlow
  hotStep: number
  onHotStep: (n: number) => void
}) {
  const fieldRef = useRef<HTMLDivElement>(null)
  const wires = useWires(fieldRef, flow?.steps)

  const involved = new Set(flow ? flow.steps.flatMap((s) => [s.from, s.to]) : [])
  const layers = [...map.layers].sort((a, b) => a.order - b.order)
  // Узлы слоя, которого в карте нет, идут отдельной колонкой: потерять
  // участника молча значит нарисовать карту полнее, чем она есть.
  const known = new Set(layers.map((l) => l.id))
  const orphans = map.nodes.filter((n) => !n.layer || !known.has(n.layer))

  const columns = [
    ...layers.map((l) => ({ id: l.id, title: l.title, nodes: map.nodes.filter((n) => n.layer === l.id) })),
    ...(orphans.length > 0 ? [{ id: '', title: 'Вне объявленных слоёв', nodes: orphans }] : []),
  ].filter((c) => c.nodes.length > 0)

  return (
    <div className="rounded-lg border border-outline-variant bg-surface-low">
      {/* Легенда перечисляет типы, которые в ЭТОЙ карте есть: у одной их пять,
          у другой пятнадцать, и общий список половину времени врал бы. */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-b border-outline-variant px-5 py-3 text-xs">
        <span className="text-on-surface-variant">цвет полосы — слой, значок — тип узла:</span>
        {legendKinds(map.nodes).map((k) => (
          <span
            key={k}
            className={`inline-flex items-center gap-1.5 ${
              isBrokenKind(k) ? 'text-tag-text-4' : 'text-on-surface-variant'
            }`}
          >
            <b aria-hidden="true">{kindGlyph(k)}</b>
            {kindLabel(k)}
          </span>
        ))}
      </div>
      <div className="overflow-x-auto">
      <div ref={fieldRef} className="relative flex min-w-max gap-5 p-5">
        <svg className="pointer-events-none absolute inset-0 h-full w-full overflow-visible" aria-hidden="true">
          {wires.map((w) => (
            <g key={w.step}>
              <path
                d={w.d}
                fill="none"
                strokeWidth={w.step === hotStep ? 2.5 : 1.5}
                // Непроверенный шаг рисуется пунктиром: на схеме он выглядит
                // такой же связью, как остальные, а подтверждён он ничем.
                strokeDasharray={w.unverified ? '5 4' : undefined}
                stroke="var(--secondary)"
                opacity={w.step === hotStep ? 1 : 0.85}
              />
              <circle
                cx={w.mid.x}
                cy={w.mid.y}
                r={w.step === hotStep ? 11 : 9}
                fill="var(--surface-lowest)"
                stroke="var(--secondary)"
              />
              <text
                x={w.mid.x}
                y={w.mid.y + 3.6}
                textAnchor="middle"
                fontSize="10"
                fill="var(--on-surface)"
              >
                {w.step}
              </text>
            </g>
          ))}
        </svg>

        {columns.map((col, i) => (
          <div key={col.id || 'orphans'} className="flex w-52 shrink-0 flex-col gap-2">
            <h3
              className="border-b-2 pb-1.5 text-[11px] font-semibold tracking-wide uppercase"
              style={{ color: laneColor(i), borderColor: laneColor(i) }}
            >
              {col.title}
            </h3>
            {col.nodes.map((n) => {
              const on = involved.has(n.id)
              return (
                <article
                  key={n.id}
                  data-node={n.id}
                  onMouseEnter={() => {
                    const s = flow?.steps.find((st) => st.from === n.id || st.to === n.id)
                    if (s) onHotStep(s.n)
                  }}
                  onMouseLeave={() => onHotStep(0)}
                  className={`relative rounded-md border border-outline-variant bg-surface-lowest p-2.5 transition-opacity ${
                    flow && !on ? 'opacity-25' : 'opacity-100'
                  }`}
                  style={{ borderLeft: `4px solid ${laneColor(i)}` }}
                >
                  <div
                    className={`flex items-center gap-1.5 text-[10px] tracking-wide uppercase ${
                      isBrokenKind(n.kind) ? 'text-tag-text-4' : 'text-on-surface-variant'
                    }`}
                  >
                    <span aria-hidden="true">{kindGlyph(n.kind)}</span>
                    {kindLabel(n.kind)}
                  </div>
                  <div className="mt-0.5 text-[13px] leading-tight font-semibold break-words">
                    {n.title}
                  </div>
                  {n.subtitle && (
                    <div className="mt-0.5 font-mono text-[11px] leading-snug break-words text-on-surface-variant">
                      {n.subtitle}
                    </div>
                  )}
                </article>
              )
            })}
          </div>
        ))}
        </div>
      </div>
    </div>
  )
}
