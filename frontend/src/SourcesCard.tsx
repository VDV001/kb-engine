import type { SourceState } from './api'

/**
 * Сводка «какие страницы отстали».
 *
 * Now проверяет себя сама, но на Team и Projects смотрят реже — значит врут они
 * дольше. Живой пример: карточка на странице Projects называла движок v0.5.0,
 * когда он отвечал 0.15.0, и заметить это можно было только вручную.
 *
 * Три состояния, а не два. «Свежая» и «сверять не с чем» — разные ответы:
 * зелёная галочка там, где опор нет вовсе, означала бы «проверено», а проверки
 * не было.
 */
export function SourcesCard({ sources }: { sources: SourceState[] }) {
  // Источники не настроены — блока нет вовсе. Пустая рамка читается как «всё в
  // порядке», хотя проверять было нечего.
  if (sources.length === 0) return null

  return (
    <section className="space-y-3">
      <h2 className="text-lg">Источники страниц</h2>
      <ul className="space-y-2">
        {sources.map((s) => (
          <li
            key={s.name}
            className="rounded-lg border border-outline-variant px-4 py-3"
            data-testid={`source-${s.name}`}
          >
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <span className="text-sm font-bold">
                {s.name} <code className="text-xs text-on-surface-variant">{s.flag}</code>
              </span>
              <span className="text-xs text-on-surface-variant tabular">
                {s.edited_at ? `${shortDate(s.edited_at)} · ${s.age_days} дн` : 'дата правки неизвестна'}
                {' · '}
                <span className={s.behind ? 'font-bold text-secondary' : ''}>{verdict(s)}</span>
              </span>
            </div>
            {s.facts.length > 0 && (
              <ul className="mt-2 space-y-1 text-sm text-on-surface-variant">
                {s.facts.map((f) => (
                  <li key={f.kind}>
                    {f.text}
                    {f.ids && f.ids.length > 0 && (
                      <span className="tabular"> — {f.ids.map((id) => `#${id}`).join(', ')}</span>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}

/** Три состояния, и каждое названо своим словом. */
function verdict(s: SourceState): string {
  if (s.unknown) return 'дату правки не знаем'
  if (s.behind) return 'отстала'
  if (s.no_anchors) return 'сверять не с чем'
  return 'свежая'
}

function shortDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const two = (n: number) => String(n).padStart(2, '0')
  return `${two(d.getDate())}.${two(d.getMonth() + 1)}`
}
