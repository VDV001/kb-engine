import { useMemo } from 'react'
import type { ArchMap } from '../api'
import { isFixed, sortFindings } from '../architecture'
import { Card, Label } from '../components/ui'
import { Anchor } from './Anchor'

/**
 * Что карта нашла, пока её писали.
 *
 * Починенные находки остаются здесь намеренно. Вычистить их значит потерять
 * причину, по которой проверка выглядит именно так: почти каждая механическая
 * проверка в этих проектах заведена после того, как её отсутствие чего-то
 * стоило.
 */
export function FindingsPanel({ map, roots }: { map: ArchMap; roots: Record<string, string> }) {
  const sorted = useMemo(() => sortFindings(map.findings), [map.findings])
  const open = sorted.filter((f) => !isFixed(f.status)).length

  if (sorted.length === 0) {
    return (
      <Card>
        <p className="text-sm text-on-surface-variant">
          Раздела находок в этой карте нет. Это не «ничего не нашли» — записи о находках
          карта просто не ведёт.
        </p>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <p className="text-sm text-on-surface-variant tabular-nums">
        {sorted.length} находок · {open} не закрыто
      </p>
      <div className="space-y-3">
        {sorted.map((f) => (
          <Card key={f.id}>
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <span className="min-w-0 flex-1 text-base">{f.title}</span>
              {f.zone && (
                <span className="shrink-0 rounded-full bg-surface-high px-2 py-0.5 text-xs text-on-surface-variant">
                  {f.zone}
                </span>
              )}
              {f.severity && (
                <span
                  className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
                    f.severity === 'high' ? 'bg-tag-bg-4 text-tag-text-4' : 'bg-tag-bg-3 text-tag-text-3'
                  }`}
                >
                  {f.severity}
                </span>
              )}
              {f.status && (
                <span
                  className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
                    isFixed(f.status) ? 'bg-tag-bg-2 text-tag-text-2' : 'bg-tag-bg-3 text-tag-text-3'
                  }`}
                >
                  {f.status}
                </span>
              )}
            </div>
            {f.detail && <p className="mt-2 text-sm">{f.detail}</p>}
            {f.fix && (
              <div className="mt-3 border-t border-outline-variant pt-3">
                <Label>Как чинилось</Label>
                <p className="mt-1 text-sm text-on-surface-variant">{f.fix}</p>
              </div>
            )}
            {f.evidence && <Anchor className="mt-2" source={f.evidence} roots={roots} />}
          </Card>
        ))}
      </div>
    </div>
  )
}
