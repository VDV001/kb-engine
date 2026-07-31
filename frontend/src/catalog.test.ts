import { describe, expect, it } from 'vitest'
import type { Entry } from './api'
import {
  categoryLabel,
  tagLabel,
  dateOf,
  emptyFilter,
  filterEntries,
  pageWindow,
  sortByDate,
  statusOf,
  topTags,
} from './catalog'

const entry = (over: Partial<Entry>): Entry => ({
  id: 1,
  title: 'x',
  category: 'golang',
  kind: 'article',
  lifecycle: 'active',
  ...over,
})

describe('statusOf', () => {
  // Решение старше закладки: reader читает статью, чтобы вынести вердикт, и
  // «прочитано» после вынесенного вердикта не сообщает ничего нового.
  // На живом каталоге это 341 запись из 1340 — все keep и все skip приходят
  // из Go с read_state='read' рядом с вердиктом, и пока read state побеждал,
  // столбец «Статус» показывал у них «Прочитано», а KEEP и SKIP было не
  // отфильтровать вообще: их не было в списке значений фильтра.
  it.each([
    ['keep', 'keep', 'KEEP'],
    ['consider', 'consider', 'На подумать'],
    ['skip', 'skip', 'SKIP'],
    ['skip-unavailable', 'skip-unavailable', 'SKIP · нет доступа'],
  ])('verdict %s outranks the read state', (verdict, key, label) => {
    const s = statusOf(entry({ verdict, read_state: 'read' }))
    expect(s.key).toBe(key)
    expect(s.label).toBe(label)
  })

  it('falls back to the read state when no verdict was recorded', () => {
    expect(statusOf(entry({ read_state: 'unread' })).label).toBe('Unread')
    expect(statusOf(entry({ read_state: 'read' })).label).toBe('Прочитано')
  })

  // Свои материалы владельца не проходят триаж — у них publish stage, и до
  // сих пор он терялся: без read_state запись падала в lifecycle и печаталась
  // как «active». В каталоге таких восемь.
  it('shows the publish stage of owner creations, not their lifecycle', () => {
    expect(statusOf(entry({ publish_stage: 'draft' })).key).toBe('draft')
    expect(statusOf(entry({ publish_stage: 'published' })).label).toBe('published')
  })

  it('falls back to lifecycle last', () => {
    expect(statusOf(entry({})).key).toBe('active')
  })

  // Незнакомое значение показывает себя как есть и остаётся ВИДИМЫМ: прятать
  // его — значит прятать работу, которую надо доделать. Тон status-draft
  // (#c9c4bc на #fbf9f2) даёт контраст 1.6 — это и есть «спрятать».
  it('keeps an unknown value visible and verbatim', () => {
    const s = statusOf(entry({ lifecycle: 'zzz-неизвестный' }))
    expect(s.label).toBe('zzz-неизвестный')
    expect(s.tone).not.toContain('draft')
  })
})

describe('filterEntries', () => {
  const data = [
    entry({ id: 1, title: 'Про Go', category: 'golang', tags: ['go'], source: 'bot-inbox' }),
    entry({ id: 2, title: 'Про промпты', category: 'meta', description: 'контекст важнее' }),
  ]

  it('search covers title, description and tags', () => {
    expect(filterEntries(data, { ...emptyFilter, search: 'контекст' })).toHaveLength(1)
    expect(filterEntries(data, { ...emptyFilter, search: 'go' })).toHaveLength(1)
  })

  it('filters compose', () => {
    expect(
      filterEntries(data, { ...emptyFilter, category: 'golang', source: 'bot-inbox' }),
    ).toHaveLength(1)
    expect(filterEntries(data, { ...emptyFilter, category: 'golang', source: 'x' })).toHaveLength(0)
  })
})

// В каталоге живут ДВА поля даты, и они почти не пересекаются: 862 записи
// только с date_added, 461 только с date_created, две с обоими, пятнадцать без
// даты вовсе. Пока вид смотрел в одно поле, у 461 записи в колонке стоял
// прочерк, и все они проваливались в бесдатный хвост сортировки. Старый
// дашборд смотрел в другое поле и ровно так же терял 862.
describe('dateOf', () => {
  it('takes whichever of the two date fields the entry carries', () => {
    expect(dateOf(entry({ date_added: '2026-07-01' }))).toBe('2026-07-01')
    expect(dateOf(entry({ date_created: '2026-04-15' }))).toBe('2026-04-15')
    expect(dateOf(entry({}))).toBe('')
  })

  // У id=294 поля разошлись на тринадцать дней: создана 15.04, добавлена
  // 28.04. Колонка в Архиве про каталог, поэтому побеждает «когда добавлено».
  it('prefers the date the entry entered the base', () => {
    expect(dateOf(entry({ date_created: '2026-04-15', date_added: '2026-04-28' }))).toBe(
      '2026-04-28',
    )
  })
})

describe('sortByDate', () => {
  it('newest first, dateless tail in stable id order', () => {
    const sorted = sortByDate([
      entry({ id: 1 }),
      entry({ id: 2, date_added: '2026-07-01' }),
      entry({ id: 3, date_added: '2026-07-15' }),
      entry({ id: 4 }),
    ])
    expect(sorted.map((e) => e.id)).toEqual([3, 2, 4, 1])
  })

  it('sorts entries dated by either field against each other', () => {
    const sorted = sortByDate([
      entry({ id: 1, date_created: '2026-07-10' }),
      entry({ id: 2, date_added: '2026-07-20' }),
      entry({ id: 3, date_created: '2026-07-15' }),
    ])
    expect(sorted.map((e) => e.id)).toEqual([2, 3, 1])
  })

  // Эталон при равных датах ставит новый id выше: внутри одного дня разбора
  // порядок иначе плавал бы от запуска к запуску.
  it('breaks a tie by id, newest first', () => {
    const sorted = sortByDate([
      entry({ id: 7, date_added: '2026-07-01' }),
      entry({ id: 9, date_added: '2026-07-01' }),
    ])
    expect(sorted.map((e) => e.id)).toEqual([9, 7])
  })
})

describe('categoryLabel', () => {
  const labels = { 'local-ai': 'Локальный AI: запуск моделей на своём железе' }

  // В словаре лежит «Название: описание» одной строкой. Списку нужно название,
  // описание уходит в подсказку — поэтому режем, а не храним два поля.
  it('shows the name, not the whole line', () => {
    expect(categoryLabel('local-ai', labels)).toBe('Локальный AI')
  })

  it('falls back to the key when the catalog has no name for it', () => {
    expect(categoryLabel('нет-такой', labels)).toBe('нет-такой')
  })

  it('survives a label without a description', () => {
    expect(categoryLabel('x', { x: 'Просто имя' })).toBe('Просто имя')
  })
})

describe('tagLabel', () => {
  // Подписи есть у двух десятков тегов из без малого четырёх тысяч: они
  // появились там, где русский тег свели к латинскому ключу. Остальные теги
  // читаемы сами по себе, и ключ для них — это и есть название.
  const labels = { 'job-market': 'Рынок труда' }

  it('shows the readable name when the catalog has one', () => {
    expect(tagLabel('job-market', labels)).toBe('Рынок труда')
  })

  it('falls back to the key, which for most tags is already the name', () => {
    expect(tagLabel('mcp', labels)).toBe('mcp')
  })

  // В отличие от категории, подпись тега не режется по двоеточию: у тега нет
  // описания, и двоеточие внутри названия — часть названия.
  it('keeps a colon inside the name', () => {
    expect(tagLabel('x', { x: 'Go: язык' })).toBe('Go: язык')
  })
})

describe('pageWindow', () => {
  // Форма, выбранная Даниилом: две стороны, одно многоточие, без ведущего.
  it('page 1 of 26', () => {
    expect(pageWindow(1, 26)).toEqual([1, 2, null, 25, 26])
  })
  it('middle follows the current page', () => {
    expect(pageWindow(13, 26)).toEqual([13, 14, null, 25, 26])
  })
  it('collapses near the end', () => {
    expect(pageWindow(24, 26)).toEqual([24, 25, 26])
    expect(pageWindow(26, 26)).toEqual([25, 26])
  })
  it('tiny sets have no ellipsis', () => {
    expect(pageWindow(1, 2)).toEqual([1, 2])
    expect(pageWindow(1, 1)).toEqual([1])
  })
})

// Облако тегов: частоты считаются по каталогу, а размер знака — по месту тега
// в этой выборке. Без нормализации внутри выборки редкие теги оказываются
// неразличимо мелкими рядом с сотенными.
describe('topTags', () => {
  const entries = [
    { id: 1, tags: ['llm', 'go', 'mcp'] },
    { id: 2, tags: ['llm', 'go'] },
    { id: 3, tags: ['llm'] },
    { id: 4, tags: ['mcp'] },
    { id: 5, tags: [] },
    { id: 6 },
  ] as Entry[]

  it('считает частоты и отдаёт самые частые первыми', () => {
    const top = topTags(entries, 10)
    expect(top.map((t) => [t.tag, t.count])).toEqual([
      ['llm', 3],
      ['go', 2],
      ['mcp', 2],
    ])
  })

  it('режет до предела и не падает на записях без тегов', () => {
    expect(topTags(entries, 2).map((t) => t.tag)).toEqual(['llm', 'go'])
    expect(topTags([], 5)).toEqual([])
  })

  it('ставит равным по частоте тегам устойчивый порядок', () => {
    // go и mcp встречаются поровну — порядок по алфавиту, иначе облако
    // перетасовывается от запроса к запросу без единой правки в каталоге.
    expect(topTags(entries, 10).map((t) => t.tag)).toEqual(['llm', 'go', 'mcp'])
  })

  it('нормализует масштаб от самого редкого в выборке к самому частому', () => {
    const top = topTags(entries, 10)
    expect(top[0].scale).toBe(1)
    expect(top.at(-1)!.scale).toBe(0)
    expect(top.every((t) => t.scale >= 0 && t.scale <= 1)).toBe(true)
  })

  it('не делит на ноль, когда все теги равны по частоте', () => {
    const flat = [{ id: 1, tags: ['a'] }, { id: 2, tags: ['b'] }] as Entry[]
    expect(topTags(flat, 5).every((t) => t.scale === 1)).toBe(true)
  })
})

// Перевод — свойство записи, которое до сих пор было видно только по слову
// «[Перевод]» в начале заголовка. Слово убрали в поле, значит фильтр обязан
// уметь то, что раньше делал поиск по этому слову: их шестьдесят из 1340.
describe('translation filter', () => {
  const data = [
    entry({ id: 1, title: 'Оригинал' }),
    entry({ id: 2, title: 'Переведённая', is_translation: true }),
  ]

  it('keeps only translations when asked', () => {
    const got = filterEntries(data, { ...emptyFilter, translation: 'yes' })
    expect(got.map((e) => e.id)).toEqual([2])
  })

  it('keeps only originals when asked', () => {
    const got = filterEntries(data, { ...emptyFilter, translation: 'no' })
    expect(got.map((e) => e.id)).toEqual([1])
  })

  it('an unset filter touches nothing', () => {
    expect(filterEntries(data, emptyFilter)).toHaveLength(2)
  })
})
