import type { ArchMap } from '../api'
import { counts } from '../architecture'
import { Card, Label } from '../components/ui'

/**
 * Зоны — то, чем карту принимают. Целиком её за раз не прочитывают, поэтому
 * приёмка идёт частями, и здесь видно, какая часть чем подтверждена.
 *
 * Зона без сценариев не прячется: она и есть самое интересное место — область,
 * которую объявили, но не описали.
 */
export function ZonesPanel({ map, onPick }: { map: ArchMap; onPick: (zone: string) => void }) {
  if (map.zones.length === 0) {
    return (
      <Card>
        <p className="text-sm text-on-surface-variant">
          Зон в карте нет — сценарии в ней не разбиты на области приёмки.
        </p>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2">
        {map.zones.map((z) => (
          <Card key={z.name}>
            <div className="flex items-baseline justify-between gap-3">
              <button
                type="button"
                onClick={() => onPick(z.name)}
                className="text-left text-lg hover:text-secondary"
                title="Показать сценарии этой зоны"
              >
                {z.name}
              </button>
              <span
                className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
                  z.accepted ? 'bg-tag-bg-2 text-tag-text-2' : 'bg-tag-bg-3 text-tag-text-3'
                }`}
              >
                {z.accepted ? 'принята' : 'сверять не с чем'}
              </span>
            </div>
            <div className="mt-2 font-mono text-xs text-on-surface-variant tabular-nums">
              {z.flows === 0
                ? 'ни одного сценария'
                : `${counts.flows(z.flows)} · ${counts.steps(z.steps)}`}
            </div>
            {z.note && <p className="mt-3 text-sm text-on-surface-variant">{z.note}</p>}
          </Card>
        ))}
      </div>

      {map.acceptance && (
        <Card tone="muted">
          <Label>Чем приёмка отличается от валидатора</Label>
          {map.acceptance.note && <p className="mt-2 text-sm">{map.acceptance.note}</p>}
          {/* Текст «что не сделано» живёт здесь, а не в шапке: на живой карте
              это абзац, и в узкой карточке он растягивал её вдвое против
              соседней. Читают его там же, где приёмку. */}
          {map.acceptance.not_done && (
            <div className="mt-3 border-t border-outline-variant pt-3">
              <Label>Не сделано</Label>
              <p className="mt-1 text-sm">{map.acceptance.not_done}</p>
            </div>
          )}
          {map.acceptance.classes_run.length > 0 && (
            <ul className="mt-3 space-y-1 text-sm text-on-surface-variant">
              {map.acceptance.classes_run.map((c) => (
                <li key={c} className="relative pl-4">
                  {/* Маркер абсолютом, а не гридом: у пункта смешанное
                      содержимое, и грид посадил бы текст в колонку маркера. */}
                  <span className="absolute left-0 top-0">·</span>
                  {c}
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}
    </div>
  )
}
