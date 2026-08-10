import { useMemo, useState } from 'react'
import type { ArchFlow, ArchMap, ArchStep } from '../api'
import { filterFlows, unverifiedCount } from '../architecture'
import { Card, Chip } from '../components/ui'
import { Anchor } from './Anchor'

/**
 * Сценарии — главное содержимое карты: как работа проходит от начала до конца,
 * шаг за шагом, и на какой строке кода каждый шаг стоит.
 *
 * Разворачивается по одному. Все сразу — это те самые сто пятьдесят девять
 * шагов простынёй, от которой уходили: длина страницы перестаёт значить
 * что-либо, и найти в ней нужный сценарий нельзя.
 */
export function FlowsPanel({
  map,
  roots,
  zone,
  onZone,
}: {
  map: ArchMap
  roots: Record<string, string>
  zone: string
  onZone: (z: string) => void
}) {
  const [query, setQuery] = useState('')
  const [unverifiedOnly, setUnverifiedOnly] = useState(false)
  const [open, setOpen] = useState<string>('')

  const shown = useMemo(
    () => filterFlows(map.flows, { zone, query, unverifiedOnly }),
    [map.flows, zone, query, unverifiedOnly],
  )
  const titles = useMemo(() => new Map(map.nodes.map((n) => [n.id, n.title])), [map.nodes])

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Вызов, файл, слово из описания"
          className="w-64 rounded-md border border-outline-variant bg-surface-lowest px-3 py-1.5 text-sm outline-none focus:border-secondary"
        />
        <Chip active={zone === ''} onClick={() => onZone('')}>
          все зоны
        </Chip>
        {map.zones.map((z) => (
          <Chip key={z.name} active={zone === z.name} onClick={() => onZone(zone === z.name ? '' : z.name)}>
            {z.name}
          </Chip>
        ))}
        {map.stats.unverified > 0 && (
          <Chip active={unverifiedOnly} onClick={() => setUnverifiedOnly(!unverifiedOnly)}>
            только непроверенные
          </Chip>
        )}
      </div>

      <p className="text-sm text-on-surface-variant tabular-nums">
        {shown.length} из {map.flows.length}
      </p>

      <div className="space-y-3">
        {shown.map((f) => (
          <FlowCard
            key={f.id}
            flow={f}
            roots={roots}
            titles={titles}
            open={open === f.id}
            onToggle={() => setOpen(open === f.id ? '' : f.id)}
          />
        ))}
      </div>

      {shown.length === 0 && (
        <Card>
          <p className="text-sm text-on-surface-variant">Под фильтр не попал ни один сценарий.</p>
        </Card>
      )}
    </div>
  )
}

function FlowCard({
  flow,
  roots,
  titles,
  open,
  onToggle,
}: {
  flow: ArchFlow
  roots: Record<string, string>
  titles: Map<string, string>
  open: boolean
  onToggle: () => void
}) {
  const unverified = unverifiedCount(flow)
  return (
    <Card className="p-0">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex w-full flex-wrap items-baseline gap-x-3 gap-y-1 px-5 py-4 text-left hover:text-secondary"
      >
        <span className="min-w-0 flex-1 text-base">{flow.title}</span>
        {flow.zone && (
          <span className="shrink-0 rounded-full bg-surface-high px-2 py-0.5 text-xs text-on-surface-variant">
            {flow.zone}
          </span>
        )}
        <span className="shrink-0 font-mono text-xs text-on-surface-variant tabular-nums">
          {flow.steps.length} шагов
        </span>
        {unverified > 0 && (
          <span className="shrink-0 rounded-full bg-tag-bg-4 px-2 py-0.5 text-xs text-tag-text-4 tabular-nums">
            {unverified} без подтверждения
          </span>
        )}
      </button>

      {flow.summary && !open && (
        <p className="px-5 pb-4 text-sm text-on-surface-variant">{flow.summary}</p>
      )}

      {open && (
        <div className="border-t border-outline-variant px-5 py-4">
          {flow.summary && <p className="mb-4 text-sm text-on-surface-variant">{flow.summary}</p>}
          <ol className="space-y-4">
            {flow.steps.map((s) => (
              <StepRow key={s.n} step={s} roots={roots} titles={titles} />
            ))}
          </ol>
        </div>
      )}
    </Card>
  )
}

function StepRow({
  step,
  roots,
  titles,
}: {
  step: ArchStep
  roots: Record<string, string>
  titles: Map<string, string>
}) {
  return (
    <li className="relative border-l border-outline-variant pl-5">
      {/* Номер шага абсолютом на линии: он метка позиции, а не часть текста,
          и в потоке он сдвигал бы каждую строку описания. */}
      <span className="absolute -left-[9px] top-0 flex h-[18px] w-[18px] items-center justify-center rounded-full bg-surface-high font-mono text-[10px] tabular-nums">
        {step.n}
      </span>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <span className="font-mono text-sm">{step.call}</span>
        {step.branch && (
          <span className="rounded-full bg-surface-high px-2 py-0.5 text-[10px] tracking-wide uppercase">
            ветка
          </span>
        )}
        {step.unverified && (
          <span className="rounded-full bg-tag-bg-4 px-2 py-0.5 text-[10px] tracking-wide text-tag-text-4 uppercase">
            не подтверждено
          </span>
        )}
      </div>
      <div className="mt-0.5 text-xs text-on-surface-variant">
        {titles.get(step.from) ?? step.from} → {titles.get(step.to) ?? step.to}
      </div>
      {step.detail && <p className="mt-1.5 text-sm">{step.detail}</p>}
      {/* Причина, по которой шаг не подтверждён, стоит рядом с самим шагом:
          собранная в отдельный список, она перестаёт читаться как оговорка к
          конкретному утверждению. */}
      {step.unverified && step.why && (
        <p className="mt-1 text-sm text-on-surface-variant italic">чем не подтверждён: {step.why}</p>
      )}
      {step.source && <Anchor className="mt-1.5" source={step.source} roots={roots} />}
    </li>
  )
}
