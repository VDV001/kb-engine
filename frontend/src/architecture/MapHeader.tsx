import type { ArchMap } from '../api'
import { acceptanceState } from '../architecture'
import { Card, Label, Stat } from '../components/ui'

/**
 * Шапка карты: чем она себя называет, против чего сверялась и какого объёма.
 *
 * Свежесть — самое важное здесь и самое неудобное. У карты кода есть коммит,
 * с которым её можно сверить; у карты рабочего места коммита нет вовсе, потому
 * что проект намеренно не под git. Показывать в обоих случаях одну плашку
 * значило бы выдать отсутствие опоры за подтверждение.
 */
export function MapHeader({ map }: { map: ArchMap }) {
  const acceptance = acceptanceState(map)

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Узлов" value={map.stats.nodes} hint="участников на карте" />
        <Stat label="Сценариев" value={map.stats.flows} hint={`${map.stats.steps} шагов`} />
        <Stat
          label="Прогонов живьём"
          value={map.stats.runtime_checks}
          hint="подтверждено запуском, а не чтением"
          tone="muted"
        />
        <Stat
          label="Не подтверждено"
          value={map.stats.unverified}
          hint={
            map.stats.unverified > 0
              ? 'шагов без прогона и без символа'
              : 'каждый шаг стоит на якоре'
          }
          tone={map.stats.unverified > 0 ? 'spotlight' : 'plain'}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <Label>Против чего сверена</Label>
          <div className="mt-2 space-y-1.5 text-sm">
            {map.commit ? (
              <p>
                Коммит кода <span className="font-mono">{map.commit}</span>
                {map.checked_at && <> · проверена {map.checked_at}</>}
              </p>
            ) : (
              <p className="text-on-surface-variant">
                Коммита нет — проект не под git, и свежесть держится только на пересчёте
                якорей{map.checked_at && <>. Последняя сверка: {map.checked_at}</>}
              </p>
            )}
            {map.note && <p className="text-on-surface-variant">{map.note}</p>}
            {map.roots.length > 0 && (
              <p className="text-on-surface-variant">
                Деревьев: {map.roots.length} —{' '}
                {map.roots.map((r) => r.name).join(', ')}
                {map.roots_note && <> · {map.roots_note}</>}
              </p>
            )}
          </div>
        </Card>

        <Card>
          <Label>Приёмка смысла</Label>
          {acceptance.state === 'unknown' ? (
            <p className="mt-2 text-sm text-on-surface-variant">
              Записи о приёмке в карте нет. Это не «не принята» — сверять не с чем, и
              галочка здесь обещала бы проверку, которой не было.
            </p>
          ) : (
            <p className="mt-2 text-sm">
              {acceptance.accepted} из {acceptance.total}{' '}
              {acceptance.state === 'accepted' ? 'зон приняты' : 'зон приняты, остальные нет'}
              {map.acceptance?.not_done && (
                <span className="mt-1 block text-on-surface-variant">
                  Не сделано: {map.acceptance.not_done}
                </span>
              )}
            </p>
          )}
        </Card>
      </div>
    </div>
  )
}
