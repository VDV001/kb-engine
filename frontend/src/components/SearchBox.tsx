import { Icon } from './Icon'

/**
 * Поиск по каталогу, живущий в шапке — как в исходном дашборде, где он стоит
 * справа в навигации и фильтрует архив по названию, описанию и тегам.
 *
 * В покое это ровно такая же иконка, как бургер и переключатель темы: тот же
 * квадрат 36×36, без фона и рамки, иконка по центру. Фон и рамка появляются
 * только когда полем пользуются — иначе в ряду одинаковых иконок одна выглядит
 * кнопкой другого рода.
 *
 * Раскрытие держится селекторами, а не состоянием: :focus — пока в поле
 * работают, :not(:placeholder-shown) — пока в нём есть текст. Без второго
 * условия поле схлопнулось бы с непустым запросом, и список остался бы
 * отфильтрованным без единого признака, почему.
 *
 * Порядок в разметке — сначала input, потом иконка: peer-селекторы Tailwind
 * действуют только на следующих соседей.
 */
export function SearchBox({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="relative flex h-9 items-center">
      <input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="Поиск по записям…"
        aria-label="Поиск по записям"
        className="peer h-9 w-9 rounded-md border border-transparent bg-transparent pr-2 pl-9 text-sm text-on-surface transition-[width] duration-300 placeholder:text-on-surface-variant focus:w-56 focus:border-outline-variant focus:bg-surface-high focus:outline-none [&:not(:placeholder-shown)]:w-56 [&:not(:placeholder-shown)]:border-outline-variant [&:not(:placeholder-shown)]:bg-surface-high"
      />
      {/* В покое иконка по центру квадрата, при раскрытии уезжает влево, к
          тексту. Через translate, а не через смену left: так переход плавный. */}
      <Icon
        name="search"
        className="pointer-events-none absolute left-1/2 -translate-x-1/2 text-xl text-on-surface-variant transition-all duration-300 peer-focus:left-2.5 peer-focus:translate-x-0 peer-focus:text-base peer-[:not(:placeholder-shown)]:left-2.5 peer-[:not(:placeholder-shown)]:translate-x-0 peer-[:not(:placeholder-shown)]:text-base"
      />
    </div>
  )
}
