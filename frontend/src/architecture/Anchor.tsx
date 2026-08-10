import { useState } from 'react'
import { anchorLabel, parseAnchor } from '../architecture'

/**
 * Якорь карты — ссылка на строку живого кода, и это единственное, чем карта
 * отличается от красивой схемы.
 *
 * Кликом он копируется, а не открывается: страницу смотрят в браузере, а
 * читать её пойдут в редакторе, и «file://» туда не ведёт. Копируется полный
 * путь, если карта объявила дерево, — вставлять относительный в чужой
 * терминал бесполезно.
 */
export function Anchor({
  source,
  roots,
  className = '',
}: {
  source: string
  roots: Record<string, string>
  className?: string
}) {
  const [copied, setCopied] = useState(false)
  const a = parseAnchor(source, roots)
  const full = a.absolute ? `${a.absolute}${a.line ? `:${a.line}` : ''}` : anchorLabel(a)

  return (
    <button
      type="button"
      title={`Скопировать ${full}`}
      onClick={() => {
        void navigator.clipboard?.writeText(full).then(
          () => setCopied(true),
          // Отказ буфера (нет разрешения, не защищённый контекст) не должен
          // выглядеть успехом: подпись просто не меняется.
          () => setCopied(false),
        )
      }}
      className={`inline-flex max-w-full items-baseline gap-1.5 font-mono text-xs text-on-surface-variant hover:text-secondary ${className}`}
    >
      {a.root && (
        <span className="shrink-0 rounded-sm bg-surface-high px-1 text-[10px] tracking-wide uppercase">
          {a.root}
        </span>
      )}
      <span className="truncate">{copied ? 'скопировано' : anchorLabel(a)}</span>
    </button>
  )
}
