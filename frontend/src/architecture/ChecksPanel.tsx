import type { ArchMap } from '../api'
import { Card, Label } from '../components/ui'

/**
 * Чем карта подтверждена и чего она про себя не знает — на одном экране,
 * потому что порознь читается только первое.
 *
 * Правило 11 стандарта harness-engineering-defaults целиком про это:
 * инструмент обязан называть, чего он НЕ проверил. Прогоны, область с
 * исключениями и раздел «вне карты» — три формы одного ответа.
 */
export function ChecksPanel({ map }: { map: ArchMap }) {
  const { coverage } = map

  return (
    <div className="space-y-4">
      <Card>
        <Label>Прогнано живьём</Label>
        {map.runtime_checks.length === 0 ? (
          <p className="mt-2 text-sm text-on-surface-variant">
            Ни одного прогона: карта целиком стоит на чтении кода.
          </p>
        ) : (
          <ul className="mt-3 space-y-2 text-sm">
            {map.runtime_checks.map((c, i) => (
              <li key={c} className="relative pl-6">
                <span className="absolute left-0 top-0 font-mono text-xs text-on-surface-variant tabular-nums">
                  {i + 1}
                </span>
                {c}
              </li>
            ))}
          </ul>
        )}
      </Card>

      {coverage && (
        <Card>
          <Label>Область карты</Label>
          {coverage.note && <p className="mt-2 text-sm">{coverage.note}</p>}
          <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-2">
            {coverage.scope.root && (
              <Row term="Корень">
                <span className="font-mono">{coverage.scope.root}</span>
              </Row>
            )}
            {coverage.scope.patterns.length > 0 && (
              <Row term="Маска">{coverage.scope.patterns.join(' · ')}</Row>
            )}
            {coverage.scope.also.length > 0 && <Row term="Плюс">{coverage.scope.also.join(' · ')}</Row>}
            {coverage.scope.exclude_dirs.length > 0 && (
              <Row term="Мимо каталогов">{coverage.scope.exclude_dirs.join(' · ')}</Row>
            )}
          </dl>

          {coverage.exclusions.length > 0 && (
            <div className="mt-4 border-t border-outline-variant pt-3">
              <Label>Исключено с причиной ({coverage.exclusions.length})</Label>
              <ul className="mt-2 space-y-1.5 text-sm">
                {coverage.exclusions.map((e) => (
                  <li key={e.path}>
                    <span className="font-mono text-xs">{e.path}</span>
                    {e.why && <span className="text-on-surface-variant"> — {e.why}</span>}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </Card>
      )}

      <Card tone={map.gaps.length > 0 ? 'muted' : 'plain'}>
        <Label>Чего карта не знает</Label>
        {map.gaps.length === 0 ? (
          <p className="mt-2 text-sm text-on-surface-variant">
            Раздела о собственных пробелах в карте нет — а это ровно тот раздел, чьё
            отсутствие ничем не отличается от «пробелов не нашли».
          </p>
        ) : (
          <ul className="mt-3 space-y-2 text-sm">
            {map.gaps.map((g) => (
              <li key={g} className="relative pl-4">
                <span className="absolute left-0 top-0">·</span>
                {g}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}

function Row({ term, children }: { term: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs tracking-wide text-on-surface-variant uppercase">{term}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  )
}
