import { useMemo, useState } from 'react'
import type { ArchMap } from '../api'
import { counts, unverifiedCount } from '../architecture'
import { Card, Chip, Label } from '../components/ui'
import { SchemeField } from './SchemeField'
import { StepRow } from './StepRow'

/**
 * Схема и сценарии рядом — так карту и читают: выбираешь сценарий, на поле
 * остаются его участники, справа появляются шаги.
 *
 * Две половины одного действия, поэтому и живут вместе. Разнести их по
 * разделам значило бы заставить переключаться туда-сюда ради одного вопроса.
 */
export function SchemePanel({
  map,
  roots,
  zone,
  onZone,
  pickedID,
  onPick,
}: {
  map: ArchMap
  roots: Record<string, string>
  zone: string
  onZone: (z: string) => void
  pickedID: string
  onPick: (id: string) => void
}) {
  const [hotStep, setHotStep] = useState(0)

  const shown = useMemo(
    () => (zone ? map.flows.filter((f) => f.zone === zone) : map.flows),
    [map.flows, zone],
  )
  const flow = map.flows.find((f) => f.id === pickedID)
  const titles = useMemo(() => new Map(map.nodes.map((n) => [n.id, n.title])), [map.nodes])

  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_clamp(340px,26vw,460px)]">
      <div className="min-w-0 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Label>Поле схемы</Label>
          {flow && <Chip onClick={() => onPick('')}>сбросить выделение</Chip>}
        </div>
        <SchemeField map={map} flow={flow} hotStep={hotStep} onHotStep={setHotStep} />
        {!flow && (
          <p className="text-sm text-on-surface-variant">
            Выберите сценарий — на схеме останутся только его участники, а шаги появятся
            справа. Пунктиром рисуются шаги, не подтверждённые ни прогоном, ни символом.
          </p>
        )}
      </div>

      <div className="min-w-0 space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <Chip active={zone === ''} onClick={() => onZone('')}>
            все {map.flows.length}
          </Chip>
          {map.zones.map((z) => (
            <Chip key={z.name} active={zone === z.name} onClick={() => onZone(zone === z.name ? '' : z.name)}>
              {z.name} {z.flows}
            </Chip>
          ))}
        </div>

        <div className="max-h-[26rem] space-y-2 overflow-y-auto pr-1">
          {shown.map((f) => {
            const unver = unverifiedCount(f)
            return (
              <button
                key={f.id}
                type="button"
                aria-pressed={f.id === pickedID}
                onClick={() => onPick(f.id === pickedID ? '' : f.id)}
                className={`w-full rounded-md border p-3 text-left transition-colors ${
                  f.id === pickedID
                    ? 'border-secondary bg-surface-high'
                    : 'border-outline-variant hover:bg-surface-high'
                }`}
              >
                <div className="text-sm font-semibold">{f.title}</div>
                {f.summary && (
                  <div className="mt-0.5 text-xs text-on-surface-variant">{f.summary}</div>
                )}
                <div className="mt-1 font-mono text-[11px] text-on-surface-variant tabular-nums">
                  {counts.steps(f.steps.length)}
                  {unver > 0 && ` · ${unver} без подтверждения`}
                </div>
              </button>
            )
          })}
          {shown.length === 0 && (
            <p className="text-sm text-on-surface-variant">В этой зоне нет ни одного сценария.</p>
          )}
        </div>

        <Card>
          <Label>{flow ? flow.title : 'Шаги'}</Label>
          {flow ? (
            <ol className="mt-3 space-y-4">
              {flow.steps.map((s) => (
                <StepRow
                  key={s.n}
                  step={s}
                  roots={roots}
                  titles={titles}
                  hot={s.n === hotStep}
                  onHover={setHotStep}
                />
              ))}
            </ol>
          ) : (
            <p className="mt-2 text-sm text-on-surface-variant">
              Сценарий не выбран.
            </p>
          )}
        </Card>
      </div>
    </div>
  )
}
