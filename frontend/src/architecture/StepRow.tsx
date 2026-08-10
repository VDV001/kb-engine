import type { ArchStep } from '../api'
import { Anchor } from './Anchor'

/**
 * Один шаг сценария: вызов, участники, описание и якорь на строку кода.
 *
 * Живёт отдельным файлом, потому что показывается в двух местах — в списке
 * сценариев и рядом со схемой. Две копии однажды разошлись бы, и разошлись бы
 * незаметно: обе выглядят правильно, пока не сравнишь.
 */
export function StepRow({
  step,
  roots,
  titles,
  hot = false,
  onHover,
}: {
  step: ArchStep
  roots: Record<string, string>
  titles: Map<string, string>
  /** Шаг, на который сейчас смотрят: тот же номер горит и на схеме. */
  hot?: boolean
  onHover?: (n: number) => void
}) {
  return (
    <li
      onMouseEnter={() => onHover?.(step.n)}
      onMouseLeave={() => onHover?.(0)}
      className={`relative border-l pl-5 ${hot ? 'border-secondary' : 'border-outline-variant'}`}
    >
      {/* Номер шага абсолютом на линии: он метка позиции, а не часть текста,
          и в потоке он сдвигал бы каждую строку описания. */}
      <span
        className={`absolute -left-[9px] top-0 flex h-[18px] w-[18px] items-center justify-center rounded-full font-mono text-[10px] tabular-nums ${
          hot ? 'bg-secondary text-white' : 'bg-surface-high'
        }`}
      >
        {step.n}
      </span>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <span className="font-mono text-sm">{step.call}</span>
        {step.branch && (
          <span className="rounded-full bg-surface-high px-2 py-0.5 text-[10px] tracking-wide uppercase">
            ветка
          </span>
        )}
        {step.unverified && (
          <span className="rounded-full bg-tag-bg-4 px-2 py-0.5 text-[10px] tracking-wide text-tag-text-4 uppercase">
            не подтверждено
          </span>
        )}
      </div>
      <div className="mt-0.5 text-xs text-on-surface-variant">
        {titles.get(step.from) ?? step.from} → {titles.get(step.to) ?? step.to}
      </div>
      {step.detail && <p className="mt-1.5 text-sm">{step.detail}</p>}
      {/* Причина, по которой шаг не подтверждён, стоит рядом с самим шагом:
          собранная в отдельный список, она перестаёт читаться как оговорка к
          конкретному утверждению. */}
      {step.unverified && step.why && (
        <p className="mt-1 text-sm text-on-surface-variant italic">чем не подтверждён: {step.why}</p>
      )}
      {step.source && <Anchor className="mt-1.5" source={step.source} roots={roots} />}
    </li>
  )
}
