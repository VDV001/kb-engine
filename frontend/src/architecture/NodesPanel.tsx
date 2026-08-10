import { useMemo, useState } from 'react'
import type { ArchMap } from '../api'
import { filterNodes, groupByLayer, kindsOf } from '../architecture'
import { Card, Chip, Label } from '../components/ui'
import { Anchor } from './Anchor'

/**
 * Участники карты, разложенные по слоям.
 *
 * Слой — не украшение, а проверяемое утверждение: команда не должна знать
 * адаптер, домен не должен знать никого. Поэтому порядок групп берётся из
 * объявления в карте, а не из порядка узлов, и узел с неизвестным слоем
 * показывается отдельно, а не растворяется.
 */
export function NodesPanel({ map, roots }: { map: ArchMap; roots: Record<string, string> }) {
  const [layer, setLayer] = useState('')
  const [kind, setKind] = useState('')
  const [query, setQuery] = useState('')

  const kinds = useMemo(() => kindsOf(map.nodes), [map.nodes])
  const shown = useMemo(
    () => filterNodes(map.nodes, { layer, kind, query }),
    [map.nodes, layer, kind, query],
  )
  const groups = useMemo(() => groupByLayer(shown, map.layers), [shown, map.layers])

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Узел, файл, символ"
          className="w-56 rounded-md border border-outline-variant bg-surface-lowest px-3 py-1.5 text-sm outline-none focus:border-secondary"
        />
        <Chip active={layer === ''} onClick={() => setLayer('')}>
          все слои
        </Chip>
        {map.layers.map((l) => (
          <Chip key={l.id} active={layer === l.id} onClick={() => setLayer(layer === l.id ? '' : l.id)}>
            {l.title}
          </Chip>
        ))}
      </div>

      {kinds.length > 1 && (
        <div className="flex flex-wrap items-center gap-2">
          <Label className="mr-1">Тип</Label>
          <Chip active={kind === ''} onClick={() => setKind('')}>
            любой
          </Chip>
          {kinds.map((k) => (
            <Chip key={k} active={kind === k} onClick={() => setKind(kind === k ? '' : k)}>
              {k}
            </Chip>
          ))}
        </div>
      )}

      <p className="text-sm text-on-surface-variant tabular-nums">
        {shown.length} из {map.nodes.length}
      </p>

      {groups.map((g) => (
        <section key={g.id || 'orphans'} className="space-y-2">
          <Label>{g.title}</Label>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {g.nodes.map((n) => (
              <Card key={n.id} className="flex min-w-0 flex-col gap-1.5">
                <div className="flex items-baseline justify-between gap-2">
                  <span className="truncate text-base" title={n.title}>
                    {n.title}
                  </span>
                  {n.kind && (
                    <span className="shrink-0 font-mono text-[10px] tracking-wide text-on-surface-variant uppercase">
                      {n.kind}
                    </span>
                  )}
                </div>
                {n.subtitle && (
                  <p className="text-sm text-on-surface-variant">{n.subtitle}</p>
                )}
                {n.sources.length > 0 ? (
                  <div className="mt-1 flex min-w-0 flex-col gap-0.5">
                    {n.sources.map((s) => (
                      <Anchor key={s} source={s} roots={roots} />
                    ))}
                  </div>
                ) : (
                  // Узел без единого якоря — это утверждение, ничем не
                  // подпёртое. Молчать о нём нельзя: половина зоны
                  // «Автоматизация» однажды оказалась именно такой.
                  <p className="mt-1 text-xs text-on-surface-variant italic">
                    якорей нет — узел ни на что не ссылается
                  </p>
                )}
              </Card>
            ))}
          </div>
        </section>
      ))}

      {shown.length === 0 && (
        <Card>
          <p className="text-sm text-on-surface-variant">Под фильтр не попал ни один узел.</p>
        </Card>
      )}
    </div>
  )
}
