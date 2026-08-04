import { useState } from 'react'
import type { Freshness } from './api'

/**
 * Полоса «страница отстала от базы».
 *
 * Повод конкретный: страница «что в работе сейчас» тухнет тихо. Текст остаётся
 * правдоподобным — «четыре PR смержены» выглядит ровно так же, как если бы их
 * было четыре, — и владелец увидел собственную страницу с этой строкой в момент,
 * когда PR было восемь.
 *
 * Отставание считает движок, и считает его не по возрасту файла: страница,
 * которую не трогали месяц, но и база вокруг которой не менялась, верна.
 * Поэтому свежая страница здесь молчит — предупреждение, которое горит всегда,
 * перестают читать, и тогда оно не работает в тот единственный раз, когда важно.
 */
export function FreshnessBanner({ freshness }: { freshness?: Freshness }) {
  const [copied, setCopied] = useState(false)

  // Старая сборка сервера поля не отдаёт. Тогда страница показывает документ
  // без проверки — молча, потому что выдуманная причина хуже пустоты.
  if (!freshness) return null

  if (freshness.unknown) {
    return (
      <div
        className="rounded-lg border border-outline-variant px-4 py-3 text-sm text-on-surface-variant"
        data-testid="freshness-unknown"
      >
        Когда страницу правили последний раз — неизвестно, поэтому отставание не проверялось.
      </div>
    )
  }
  if (!freshness.behind) return null

  return (
    <div className="rounded-lg border border-secondary bg-secondary-container/40 px-4 py-3">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <span className="text-sm font-bold text-on-surface">⚠ Страница отстала от базы</span>
        {freshness.edited_at && (
          <span className="text-xs text-on-surface-variant tabular" data-testid="freshness-edited">
            правлена {shortDateTime(freshness.edited_at)}
          </span>
        )}
      </div>

      <ul className="mt-2 space-y-1 text-sm text-on-surface-variant">
        {freshness.facts.map((f) => (
          <li key={f.kind}>
            {f.text}
            {/* Записи названы поимённо, но не все: пять — это подсказка,
                двадцать — вторая лента поверх той, что уже есть на Dashboard. */}
            {f.ids && f.ids.length > 0 && (
              <span className="tabular"> — {f.ids.map((id) => `#${id}`).join(', ')}</span>
            )}
          </li>
        ))}
      </ul>

      {freshness.draft && (
        <details className="mt-3">
          <summary className="cursor-pointer text-xs text-secondary">
            черновик блока — скопировать и дописать словами
          </summary>
          {/* Движок не пишет в файл сам: Now — личный документ, и автотекст в
              нём за неделю стал бы шумом, который перестают читать. Заготовка
              собирает факты, слова остаются за человеком. */}
          <pre
            className="mt-2 overflow-x-auto rounded bg-surface-container p-3 text-xs"
            data-testid="freshness-draft"
          >
            {freshness.draft}
          </pre>
          <button
            type="button"
            className="mt-2 rounded border border-outline-variant px-3 py-1 text-xs"
            onClick={() => {
              void navigator.clipboard?.writeText(freshness.draft ?? '')
              setCopied(true)
            }}
          >
            {copied ? 'скопировано' : 'копировать'}
          </button>
        </details>
      )}
    </div>
  )
}

/** Дата правки в том виде, в каком её читает человек: 04.08 15:12. */
function shortDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const two = (n: number) => String(n).padStart(2, '0')
  return `${two(d.getDate())}.${two(d.getMonth() + 1)} ${two(d.getHours())}:${two(d.getMinutes())}`
}
