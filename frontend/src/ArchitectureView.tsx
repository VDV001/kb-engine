import { useMemo, useState } from 'react'
import { api } from './api'
import type { ArchMapIndexEntry } from './api'
import { rootsOf } from './architecture'
import { useResource } from './hooks/useResource'
import { Card, Chip, ErrorBox, Section, Spinner } from './components/ui'
import { ChecksPanel } from './architecture/ChecksPanel'
import { FindingsPanel } from './architecture/FindingsPanel'
import { FlowsPanel } from './architecture/FlowsPanel'
import { MapHeader } from './architecture/MapHeader'
import { NodesPanel } from './architecture/NodesPanel'
import { ZonesPanel } from './architecture/ZonesPanel'

type Part = 'zones' | 'flows' | 'nodes' | 'findings' | 'checks'

const PARTS: { id: Part; label: string }[] = [
  { id: 'zones', label: 'Зоны' },
  { id: 'flows', label: 'Сценарии' },
  { id: 'nodes', label: 'Узлы' },
  { id: 'findings', label: 'Находки' },
  { id: 'checks', label: 'Проверено и не проверено' },
]

/**
 * Карта архитектуры на витрине движка.
 *
 * До этого карты жили страницами по 140 КБ разметки: всё содержимое сразу,
 * ни фильтров, ни адреса у сценария. Здесь то же содержимое разложено по
 * разделам, а тяжёлые части грузятся только когда карту выбрали, — оглавление
 * отдельным запросом как раз затем и существует.
 */
export function ArchitectureView() {
  const index = useResource(api.maps)
  const [pickedID, setPickedID] = useState('')
  const [part, setPart] = useState<Part>('zones')
  const [zone, setZone] = useState('')

  const list: ArchMapIndexEntry[] = index.status === 'ready' ? index.data.maps : []
  // Пока карту не выбрали — первая из списка: вкладка с одним выпадающим
  // списком и пустотой под ним требует лишнего клика ради ничего.
  const currentID = pickedID || list[0]?.id || ''
  const map = useResource(() => api.map(currentID), { enabled: currentID !== '', key: currentID })

  const roots = useMemo(
    () => (map.status === 'ready' ? rootsOf(map.data) : {}),
    [map],
  )

  if (index.status === 'loading') return <Spinner />
  if (index.status === 'failed') return <ErrorBox message={index.error} />

  if (list.length === 0) {
    // Пустота, объясняющая себя: список пуст ровно в одном случае — флаг не
    // передавали, потому что непустой --maps движок бы не запустил молча.
    // «Карт нет» без этой строки неотличимо от «движок сломан».
    return (
      <Section title="Архитектура" subtitle="как проект работает на самом деле">
        <Card>
          <p className="text-sm text-on-surface-variant">
            Карты движку не переданы. Запусти его с <span className="font-mono">--maps</span>{' '}
            и путём к map.json — флаг можно повторить, карт бывает несколько.
          </p>
        </Card>
      </Section>
    )
  }

  return (
    <Section
      title="Архитектура"
      subtitle="как проект работает на самом деле — каждое утверждение на строке живого кода"
      aside={
        list.length > 1 ? (
          <div className="flex flex-wrap gap-2">
            {list.map((m) => (
              <Chip
                key={m.id}
                active={m.id === currentID}
                onClick={() => {
                  setPickedID(m.id)
                  // Зона принадлежит карте: оставить выбранную «Финансы» при
                  // переходе на карту, где такой зоны нет, значит показать
                  // пустой список и не сказать почему.
                  setZone('')
                }}
              >
                {m.project}
              </Chip>
            ))}
          </div>
        ) : undefined
      }
    >
      {map.status === 'loading' && <Spinner />}
      {map.status === 'failed' && <ErrorBox message={map.error} />}
      {map.status === 'ready' && (
        <div className="space-y-6">
          <MapHeader map={map.data} />

          <div className="flex flex-wrap gap-2 border-b border-outline-variant pb-3">
            {PARTS.map((p) => (
              <Chip key={p.id} active={part === p.id} onClick={() => setPart(p.id)}>
                {p.label}
                {p.id === 'findings' && map.data.findings.length > 0 && (
                  <span className="ml-1.5 tabular-nums">{map.data.findings.length}</span>
                )}
              </Chip>
            ))}
          </div>

          {part === 'zones' && (
            <ZonesPanel
              map={map.data}
              onPick={(z) => {
                setZone(z)
                setPart('flows')
              }}
            />
          )}
          {part === 'flows' && (
            <FlowsPanel map={map.data} roots={roots} zone={zone} onZone={setZone} />
          )}
          {part === 'nodes' && <NodesPanel map={map.data} roots={roots} />}
          {part === 'findings' && <FindingsPanel map={map.data} roots={roots} />}
          {part === 'checks' && <ChecksPanel map={map.data} />}
        </div>
      )}
    </Section>
  )
}
