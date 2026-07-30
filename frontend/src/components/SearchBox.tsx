import { Icon } from './Icon'

/**
 * Поиск по каталогу, живущий в шапке — как в исходном дашборде, где он стоит
 * справа в навигации и фильтрует архив по названию, описанию и тегам.
 *
 * В покое поле шириной с иконку и раскрывается при фокусе. Причина не в
 * красоте: шапка обязана оставаться одноэтажной, а порог ухода навигации в
 * бургер посчитан по её ширине — постоянное поле в 256px сдвинуло бы этот
 * порог с 1138 до почти 1400, то есть бургер появлялся бы уже на обычном
 * ноутбуке. Свёрнутое поле добавляет 36.
 *
 * Раскрытие сделано селекторами, а не состоянием: :focus держит поле открытым,
 * пока в нём работают, а :not(:placeholder-shown) — пока в нём есть текст.
 * Иначе поле схлопывалось бы с непустым запросом, и список оставался бы
 * отфильтрованным без единого признака, почему.
 */
export function SearchBox({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="relative flex items-center">
      <Icon
        name="search"
        className="pointer-events-none absolute left-2.5 text-base text-on-surface-variant"
      />
      <input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="Поиск по записям…"
        aria-label="Поиск по записям"
        className="w-9 rounded-md border border-transparent bg-surface-high py-1.5 pr-2 pl-9 text-sm text-on-surface transition-[width] duration-300 placeholder:text-on-surface-variant focus:w-56 focus:border-outline-variant focus:outline-none [&:not(:placeholder-shown)]:w-56 [&:not(:placeholder-shown)]:border-outline-variant"
      />
    </div>
  )
}
