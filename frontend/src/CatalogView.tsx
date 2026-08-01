import { useMemo, useState } from 'react'
import type { Entry, Health } from './api'
import {
  categoryLabel,
  dateOf,
  emptyFilter,
  filterEntries,
  pageWindow,
  sortByDate,
  entriesWord,
  statusOf,
  statusStyle,
  tagLabel,
  writeupLinks,
  WRITEUP_CATEGORY,
  type CatalogFilter,
} from './catalog'
import { Icon } from './components/Icon'
import { Label } from './components/ui'
import { HealthCard, SpotlightCard } from './HealthCards'

// Пятнадцать, как в исходном дашборде: столько строк помещается на экран
// ноутбука без прокрутки до пагинации.
const PAGE_SIZE = 15

// Tag pills cycle through the first three tag roles, the way the Python
// dashboard colours them: by position, not by meaning — the meaning is the
// text, the colour only keeps neighbours apart.
const tagTone = [
  'bg-tag-bg-1 text-tag-text-1',
  'bg-tag-bg-2 text-tag-text-2',
  'bg-tag-bg-3 text-tag-text-3',
]

function Status({ e }: { e: Entry }) {
  const s = statusOf(e)
  return (
    <span className="flex items-center gap-2 whitespace-nowrap">
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: s.tone }} />
      <span className="label" style={{ color: s.tone }}>
        {s.label}
      </span>
    </span>
  )
}

function Tags({ tags, labels }: { tags?: string[]; labels: Record<string, string> }) {
  return (
    <span className="flex flex-wrap gap-1.5">
      {(tags ?? []).slice(0, 3).map((t, i) => (
        <span
          key={t}
          // Ключ остаётся в title: подпись отвечает на «что это», ключ — на
          // «что искать», и после сведения русских тегов к латинским ключам
          // второе перестало быть очевидным из первого.
          title={t}
          className={`rounded px-2 py-0.5 font-label text-[10px] font-bold uppercase ${tagTone[i]}`}
        >
          {tagLabel(t, labels)}
        </span>
      ))}
    </span>
  )
}

/**
 * Title — заголовок записи. Ссылкой он становится только при адресе: у 122
 * записей каталога адреса нет вовсе (свой файл вместо чужой статьи), а
 * подчёркивание при наведении обещало переход, которого не происходило.
 */
function Title({ e, className = '' }: { e: Entry; className?: string }) {
  const base = `font-headline text-base font-bold ${className}`
  if (!e.url) return <span className={base}>{e.title}</span>
  return (
    <a href={e.url} target="_blank" rel="noreferrer" className={`${base} hover:underline`}>
      {e.title}
    </a>
  )
}

/**
 * WriteupLink — дорога от статьи к её разбору. Ведёт поиском по номеру, тем
 * же путём, которым открывает запись гигиена: читают разбор там же, где
 * читают всё остальное, и заводить ради него вторую поверхность незачем.
 */
function WriteupLink({ id, onOpen }: { id: number; onOpen: () => void }) {
  return (
    <button
      type="button"
      onClick={onOpen}
      title={`Открыть разбор этой записи (#${id})`}
      className="flex items-center gap-1 whitespace-nowrap text-secondary hover:underline"
    >
      <Icon name="menu_book" className="text-sm" />
      <span className="font-label text-[10px] font-semibold tracking-wider uppercase">Разбор</span>
    </button>
  )
}

/** Сколько записей покрывает разбор. Считается обратным индексом: связь
 * односторонняя, и сам разбор о цитирующих его статьях не знает. */
function Coverage({ n }: { n: number }) {
  return (
    <span className="whitespace-nowrap font-label text-[10px] tracking-wider uppercase text-on-surface-variant">
      Разбирает {n} {entriesWord(n)}
    </span>
  )
}

function Pagination({
  page,
  pages,
  onPage,
}: {
  page: number
  pages: number
  onPage: (p: number) => void
}) {
  if (pages <= 1) return null
  const arrow = 'flex h-8 w-8 items-center justify-center rounded border border-outline-variant text-on-surface-variant disabled:opacity-30'
  return (
    <div className="flex items-center gap-1.5">
      <button type="button" className={arrow} disabled={page === 1} onClick={() => onPage(page - 1)} aria-label="Назад">
        ‹
      </button>
      {pageWindow(page, pages).map((p, i) =>
        p === null ? (
          <span key={`dots-${i}`} className="px-1 select-none text-on-surface-variant">
            …
          </span>
        ) : (
          <button
            key={p}
            type="button"
            onClick={() => onPage(p)}
            className={`h-8 w-8 rounded font-label text-xs ${
              p === page ? 'bg-secondary font-bold text-white' : 'text-on-surface-variant'
            }`}
          >
            {p}
          </button>
        ),
      )}
      <button type="button" className={arrow} disabled={page === pages} onClick={() => onPage(page + 1)} aria-label="Вперёд">
        ›
      </button>
    </div>
  )
}

const selectClass =
  'rounded-md border border-outline-variant bg-surface-low px-3 py-1.5 text-sm text-on-surface'

/**
 * CatalogView is the full catalog browser, the view the KB dashboard calls
 * Archives: category sidebar with counts, four filters, search, table or grid,
 * paginated. Everything recomputes from the entries prop, so a changed catalog
 * reshapes the whole view on the next fetch — nothing here is baked.
 */
export function CatalogView({
  entries,
  labels,
  tagLabels,
  pickedTag,
  onPickedTagChange,
  pickedCategory,
  onPickedCategoryChange,
  health,
  search,
  onSearchChange,
}: {
  entries: Entry[]
  labels: Record<string, string>
  /** Подписи тегов — свой словарь: у категории есть описание после двоеточия,
   * у тега его нет, и путать их значило бы резать название тега пополам. */
  tagLabels: Record<string, string>
  /** Тег, выбранный в облаке на дашборде: выбирают там, применяется здесь. */
  pickedTag: string
  onPickedTagChange: (t: string) => void
  /** Категория, выбранная ящиком на About — тот же путь, что у тега. */
  pickedCategory: string
  onPickedCategoryChange: (c: string) => void
  health: Health
  /** Запрос из поля в шапке: поле живёт там, а фильтрует этот вид. */
  search: string
  onSearchChange: (v: string) => void
}) {
  // Категория, пришедшая снаружи, попадает в фильтр СРАЗУ при создании вида, а
  // не только при последующей смене: переключение вкладки монтирует компонент
  // заново, и сверке «показано не то, что пришло» в этот момент не с чем
  // сравнивать — она видит первое значение как исходное и молчит.
  const [filter, setFilter] = useState<CatalogFilter>(() => ({
    ...emptyFilter,
    category: pickedCategory,
  }))
  const [page, setPage] = useState(1)
  const [grid, setGrid] = useState(false)
  const [withDescriptions, setWithDescriptions] = useState(true)
  const [spotlightOpen, setSpotlightOpen] = useState(false)
  // Свёрнутый сайдбар отдаёт таблице свои 256px. Ниже xl он свёрнут по
  // умолчанию: там места нет, а разворачивать его — осознанный выбор.
  const [sideOpen, setSideOpen] = useState(() => window.innerWidth >= 1280)

  // Спотлайт показывает самую свежую запись КАТАЛОГА, а не текущей выдачи:
  // «последнее добавление», которое меняется от фильтра, — это уже не то, что
  // подписано на карточке.
  const newest = useMemo(() => sortByDate(entries)[0], [entries])

  // Связь считается по всему каталогу, а не по текущей выдаче: разбор и
  // статья почти никогда не попадают в одну страницу фильтра, и счёт по
  // видимому куску показывал бы «разбирает 1» там, где записей десять.
  const { writeupOf, coverage } = useMemo(() => writeupLinks(entries), [entries])
  // Открыть разбор — это поиск по его номеру: тот же путь, которым гигиена
  // открывает свою находку. Фильтры при этом снимаются, иначе искомая запись
  // может не пройти сквозь них и экран окажется пустым.
  const openEntry = (id: number) => {
    setFilter(emptyFilter)
    onPickedTagChange('')
    onPickedCategoryChange('')
    onSearchChange(`#${id}`)
    setPage(1)
  }

  const set = (patch: Partial<CatalogFilter>) => {
    setFilter((f) => ({ ...f, ...patch }))
    setPage(1)
  }

  const categories = useMemo(() => {
    const counts = new Map<string, number>()
    for (const e of entries) counts.set(e.category, (counts.get(e.category) ?? 0) + 1)
    return [...counts.entries()].sort((a, b) => b[1] - a[1])
  }, [entries])

  const sources = useMemo(
    () => [...new Set(entries.map((e) => e.source ?? '').filter(Boolean))].sort(),
    [entries],
  )
  const lifecycles = useMemo(
    () => [...new Set(entries.map((e) => e.lifecycle))].sort(),
    [entries],
  )
  // Список строится из того, что реально лежит в каталоге, а не из зашитого
  // перечня: пункт, который ничего не найдёт, хуже отсутствующего.
  const statuses = useMemo(
    () => [...new Set(entries.map((e) => statusOf(e).key))].sort().map(statusStyle),
    [entries],
  )

  // Новый запрос возвращает на первую страницу — иначе, стоя на пятой, ищешь и
  // видишь пустоту, потому что у найденного столько страниц нет. Подстройка
  // состояния при рендере, а не useEffect: эффект дорисовал бы кадр со старой
  // страницей и тут же перерисовал, да и гейт слоёв держит useEffect в hooks/.
  const [searchShown, setSearchShown] = useState(search)
  if (searchShown !== search) {
    setSearchShown(search)
    setPage(1)
  }
  const [tagShown, setTagShown] = useState(pickedTag)
  if (tagShown !== pickedTag) {
    setTagShown(pickedTag)
    setPage(1)
  }
  // Категория, пришедшая снаружи, ложится в тот же фильтр, что и выбранная
  // селектом: иначе на экране оказались бы два состояния одной категории, и
  // сброс одного не убирал бы другое.
  const [categoryShown, setCategoryShown] = useState(pickedCategory)
  if (categoryShown !== pickedCategory) {
    setCategoryShown(pickedCategory)
    if (pickedCategory !== '') setFilter((f) => ({ ...f, category: pickedCategory }))
    setPage(1)
  }

  // Запрос из шапки подмешивается к остальным фильтрам, а не живёт отдельной
  // веткой: для filterEntries он такое же условие, как категория или статус.
  const active = useMemo(
    () => ({ ...filter, search, tag: pickedTag }),
    [filter, search, pickedTag],
  )
  const filtered = useMemo(() => sortByDate(filterEntries(entries, active)), [entries, active])
  const pages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const current = Math.min(page, pages)
  const slice = filtered.slice((current - 1) * PAGE_SIZE, current * PAGE_SIZE)
  const isFiltered = JSON.stringify(active) !== JSON.stringify(emptyFilter)

  return (
    <div className="flex flex-col gap-6 lg:flex-row">
      {/* Sidebar: structure, so square corners and a hairline — not a card. */}
      {/* Сайдбар сворачивается до ширины своей же кнопки: на 1024 он забирал
          256px, и таблице оставалось 670 при нужных ~830. */}
      {/* Свёрнутый — по размеру содержимого, а не во всю ширину: до lg строка
          вертикальная, и полоса на весь экран с одинокой стрелкой читалась как
          неизвестно что. Там же у кнопки появляется подпись. */}
      <aside className={`shrink-0 ${sideOpen ? 'w-full lg:w-64' : 'w-fit lg:w-12'}`}>
        <div className="border border-outline-variant bg-surface-low">
          <div className="flex items-center justify-between border-b border-outline-variant">
            {sideOpen && (
              <button
                type="button"
                onClick={() => set({ category: '' })}
                className={`flex flex-1 items-center justify-between px-4 py-2.5 text-left text-sm ${
                  filter.category === '' ? 'bg-surface-high font-semibold text-secondary' : 'text-on-surface'
                }`}
              >
                <span>Все записи</span>
                <span className="font-mono text-xs tabular-nums">{entries.length}</span>
              </button>
            )}
            <button
              type="button"
              onClick={() => setSideOpen((v) => !v)}
              aria-expanded={sideOpen}
              aria-label={sideOpen ? 'Свернуть категории' : 'Развернуть категории'}
              title={sideOpen ? 'Свернуть категории' : 'Развернуть категории'}
              className="relative flex h-11 shrink-0 items-center justify-center gap-1.5 px-3 text-on-surface-variant hover:text-on-surface lg:w-12 lg:px-0"
            >
              <Icon name={sideOpen ? 'chevron_left' : 'chevron_right'} className="text-xl" />
              {/* До lg рядом со стрелкой стоит слово: там кнопка лежит поперёк
                  страницы, и одна стрелка не объясняет, что за ней. */}
              {!sideOpen && <span className="text-sm lg:hidden">Категории</span>}
              {/* Точка говорит, что фильтр по категории включён: в свёрнутом
                  виде списка не видно, и выборка иначе выглядела бы поломкой. */}
              {!sideOpen && filter.category !== '' && (
                <span className="absolute top-2 right-2 h-1.5 w-1.5 rounded-full bg-secondary" />
              )}
            </button>
          </div>
          {sideOpen && (
            <div className="max-h-[26rem] overflow-y-auto lg:max-h-none lg:overflow-visible">
              {categories.map(([cat, n]) => (
                <button
                  key={cat}
                  type="button"
                  onClick={() => set({ category: cat === filter.category ? '' : cat })}
                  className={`flex w-full items-center justify-between gap-2 px-4 py-2 text-left text-sm ${
                    filter.category === cat
                      ? 'bg-surface-high font-semibold text-secondary'
                      : 'text-on-surface-variant hover:text-on-surface'
                  }`}
                >
                  {/* Подсказкой — полная строка из каталога: после двоеточия
                      лежит описание, которое в узкий сайдбар не влезает. */}
                  <span className="truncate" title={labels[cat] || cat}>
                    {categoryLabel(cat, labels)}
                  </span>
                  <span className="font-mono text-xs tabular-nums">{n}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </aside>

      <div className="min-w-0 flex-1 space-y-5">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <Label className="text-secondary">Архив документов</Label>
            <h1 className="mt-1 text-4xl">Каталог записей.</h1>
            <p className="mt-2 text-sm text-on-surface-variant">
              Централизованный каталог статей, заметок и конспектов. {entries.length} записей в{' '}
              {categories.length} категориях.
            </p>
          </div>
          <div className="flex rounded-md border border-outline-variant">
            {([false, true] as const).map((g) => (
              <button
                key={String(g)}
                type="button"
                onClick={() => setGrid(g)}
                className={`px-3 py-1.5 font-label text-[11px] font-semibold tracking-wider uppercase ${
                  grid === g ? 'bg-surface-high text-on-surface' : 'text-on-surface-variant'
                }`}
              >
                {g ? 'Grid view' : 'Table view'}
              </button>
            ))}
          </div>
        </header>

        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-outline-variant bg-surface-low p-4">
          {/* Поля поиска здесь больше нет: оно переехало в шапку, как в
              исходном дашборде. Два поля на один запрос — это два места, где
              видно разное, стоит забыть синхронизировать одно из них. */}
          <select value={filter.status} onChange={(e) => set({ status: e.target.value })} className={selectClass}>
            <option value="">Любой статус</option>
            {statuses.map((s) => (
              <option key={s.key} value={s.key}>
                {s.label}
              </option>
            ))}
          </select>
          <select value={filter.source} onChange={(e) => set({ source: e.target.value })} className={selectClass}>
            <option value="">Все источники</option>
            {sources.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          <select
            value={filter.lifecycle}
            onChange={(e) => set({ lifecycle: e.target.value })}
            className={selectClass}
          >
            <option value="">Любой lifecycle</option>
            {lifecycles.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
          {/* Переводов в базе шестьдесят. Пока признак жил словом в заголовке,
              отобрать их можно было только поиском по этому слову — и поиск
              заодно приносил всё, где «перевод» упомянут в описании. */}
          <select
            value={filter.translation}
            onChange={(e) => set({ translation: e.target.value })}
            className={selectClass}
          >
            <option value="">Оригиналы и переводы</option>
            <option value="yes">Только переводы</option>
            <option value="no">Только оригиналы</option>
          </select>
          {/* Тумблер, а не галочка: в исходном дашборде это переключатель, и
              такой же стоит в шапке у сумм — две разные механики для одного и
              того же действия читаются как разные по смыслу. */}
          <div className="flex items-center gap-2">
            <span className="label text-[10px] text-on-surface-variant">Описания</span>
            <label className="toggle-switch">
              <input
                type="checkbox"
                checked={withDescriptions}
                onChange={(e) => setWithDescriptions(e.target.checked)}
                aria-label={withDescriptions ? 'Скрыть описания' : 'Показать описания'}
              />
              <span className="toggle-slider" />
            </label>
            {pickedTag && (
              <button
                type="button"
                onClick={() => onPickedTagChange('')}
                className="flex items-center gap-1.5 rounded-full border border-secondary px-3 py-1 font-label text-[10px] uppercase tracking-wider text-secondary hover:bg-surface-high"
                title="Снять фильтр по тегу"
              >
                {tagLabel(pickedTag, tagLabels)} ✕
              </button>
            )}
          </div>
          <button
            type="button"
            onClick={() => {
              setFilter(emptyFilter)
              // И запрос из шапки тоже: кнопка обещает сбросить фильтры, а не
              // выборочно те из них, что нарисованы рядом с ней.
              onSearchChange('')
              // И тег, пришедший из облака на дашборде: он не нарисован среди
              // селекторов, но он такой же действующий фильтр.
              onPickedTagChange('')
              // Категория с About видна в своём селекторе и снимается вместе с
              // ним, но забыть её наверху нельзя: иначе повторный клик по тому
              // же ящику не сменит состояние и фильтр не вернётся.
              onPickedCategoryChange('')
              setPage(1)
            }}
            disabled={!isFiltered}
            className="text-sm text-on-surface-variant disabled:opacity-40 hover:text-on-surface"
          >
            Сбросить
          </button>
        </div>

        {grid ? (
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {slice.map((e) => (
              <div key={e.id} className="flex flex-col gap-2 rounded-lg border border-outline-variant bg-surface-lowest p-4">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-label text-xs text-on-surface-variant">
                    {dateOf(e) || '—'}
                  </span>
                  <Status e={e} />
                </div>
                <Title e={e} />
                {/* Метка, а не слово в заголовке: заголовок принадлежит
                    автору оригинала, а «перевод» — это про запись. */}
                {e.is_translation && (
                  <span
                    className="ml-2 align-middle rounded border border-outline-variant px-1.5 py-0.5 font-label text-[9px] uppercase tracking-wider text-on-surface-variant"
                    title="Перевод чужого оригинала"
                  >
                    перевод
                  </span>
                )}
                {withDescriptions && e.description && (
                  <p className="text-sm text-on-surface-variant">
                    {e.description.length > 150 ? `${e.description.slice(0, 150)}…` : e.description}
                  </p>
                )}
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                  <Tags tags={e.tags} labels={tagLabels} />
                  {writeupOf.has(e.id) && (
                    <WriteupLink id={writeupOf.get(e.id)!} onOpen={() => openEntry(writeupOf.get(e.id)!)} />
                  )}
                  {e.category === WRITEUP_CATEGORY && coverage.has(e.id) && (
                    <Coverage n={coverage.get(e.id)!} />
                  )}
                </div>
              </div>
            ))}
          </div>
        ) : (
          /* Список, а не таблица. Колонки требовали, чтобы длинный заголовок
             жил в узкой доле ширины, и он ломался по одному слову, пока
             соседние колонки пустовали. Здесь метаданные идут одной строкой
             сверху, а текст занимает всю ширину блока. */
          <ul className="border border-outline-variant bg-surface-lowest">
            {slice.map((e) => (
              <li key={e.id} className="border-t border-outline-variant first:border-t-0">
                <div className="px-5 py-4">
                  {/* Одна строка метаданных: перенос разрешён, чтобы на узком
                      экране они переехали, а не наехали друг на друга. */}
                  <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1.5">
                    <span className="font-label text-xs whitespace-nowrap text-on-surface-variant">
                      {dateOf(e) || '—'}
                    </span>
                    <span
                      className="max-w-[14rem] truncate rounded-full border border-outline-variant bg-surface-high px-2.5 py-0.5 text-xs text-on-surface-variant"
                      title={labels[e.category] || e.category}
                    >
                      {categoryLabel(e.category, labels)}
                    </span>
                    <Tags tags={e.tags} labels={tagLabels} />
                    <Status e={e} />
                    {/* Дорога к разбору стоит среди метаданных записи, а не
                        под текстом: это свойство записи, как её статус или
                        категория, и искать его глазами в другом месте не надо. */}
                    {writeupOf.has(e.id) && (
                      <WriteupLink id={writeupOf.get(e.id)!} onOpen={() => openEntry(writeupOf.get(e.id)!)} />
                    )}
                    {e.category === WRITEUP_CATEGORY && coverage.has(e.id) && (
                      <Coverage n={coverage.get(e.id)!} />
                    )}
                    {e.url && (
                      <a
                        href={e.url}
                        target="_blank"
                        rel="noreferrer"
                        className="ml-auto shrink-0 text-secondary hover:underline"
                        aria-label="Открыть источник"
                        title="Открыть источник"
                      >
                        <Icon name="open_in_new" className="text-base" />
                      </a>
                    )}
                  </div>

                  <Title e={e} />
                  {withDescriptions && e.description && (
                    <p className="mt-1 line-clamp-2 text-sm text-on-surface-variant">
                      {e.description}
                    </p>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3">
          <span className="label">
            {filtered.length > 0
              ? `Показано ${(current - 1) * PAGE_SIZE + 1}–${Math.min(current * PAGE_SIZE, filtered.length)} из ${filtered.length}`
              : 'Нет записей'}
          </span>
          <Pagination page={current} pages={pages} onPage={setPage} />
        </div>

        {/* items-start: у карточек резко разный объём содержимого, и растягивать
            правую до высоты левой незачем — она берёт высоту по своему. */}
        <section className="grid grid-cols-1 items-start gap-6 pt-8 md:grid-cols-3">
          <SpotlightCard
            entry={newest}
            expanded={spotlightOpen}
            onToggle={() => setSpotlightOpen((v) => !v)}
          />
          <HealthCard health={health} />
        </section>
      </div>
    </div>
  )
}
