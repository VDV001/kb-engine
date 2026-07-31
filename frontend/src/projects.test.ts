import { describe, expect, it } from 'vitest'
import type { ProjectSection } from './api'
import { countCards, filterSections, projectFilters } from './projects'

const sections: ProjectSection[] = [
  {
    title: 'Продукты',
    cards: [
      {
        title: 'AI Sales Assistant',
        short: 'Floq',
        kicker: 'B2B SaaS · AI Sales',
        body: 'Мультиканальные секвенции и квалификация лидов.',
        plain: 'Продавец теряет клиентов, потому что забывает написать.',
        tags: ['go', 'nextjs', 'ai'],
        span: 'full',
      },
      {
        title: 'Движок базы знаний',
        short: 'kb-engine',
        body: 'Аудиты жизненного цикла и поиск дублей.',
        tags: ['go', 'react'],
        span: 'full',
      },
    ],
  },
  {
    title: 'Обучающие продукты',
    cards: [
      {
        title: 'English Hub',
        body: 'Девять курсов грамматики и словарь.',
        tags: ['edu'],
        span: 'third',
      },
    ],
  },
]

describe('projectFilters', () => {
  // Фильтры берутся из тегов карточек, а не из списка рядом с разметкой:
  // список — второй источник правды, и он расходится молча в тот день, когда
  // у проекта появляется новый тег.
  it('собирает фильтры из тегов карточек', () => {
    expect(projectFilters(sections)).toEqual([
      { tag: 'go', label: 'Go', count: 2 },
      { tag: 'ai', label: 'AI / LLM', count: 1 },
      { tag: 'edu', label: 'Обучение', count: 1 },
      { tag: 'nextjs', label: 'Next.js', count: 1 },
      { tag: 'react', label: 'React', count: 1 },
    ])
  })

  // Незнакомый тег показывается как есть: выдуманное название прячет то, что
  // словарь отстал от данных.
  it('незнакомый тег остаётся собой', () => {
    const one: ProjectSection[] = [{ title: 'x', cards: [{ title: 'a', tags: ['quantum'] }] }]
    expect(projectFilters(one)).toEqual([{ tag: 'quantum', label: 'quantum', count: 1 }])
  })

  it('карточки без тегов не ломают счёт', () => {
    const one: ProjectSection[] = [{ title: 'x', cards: [{ title: 'a' }] }]
    expect(projectFilters(one)).toEqual([])
  })
})

describe('filterSections', () => {
  const tests: { name: string; tag: string; query: string; want: string[] }[] = [
    { name: 'пустой фильтр отдаёт всё', tag: '', query: '', want: ['AI Sales Assistant', 'Движок базы знаний', 'English Hub'] },
    { name: 'по тегу', tag: 'go', query: '', want: ['AI Sales Assistant', 'Движок базы знаний'] },
    { name: 'по названию', tag: '', query: 'движок', want: ['Движок базы знаний'] },
    // Короткое имя — то, под которым владелец называет проект вслух, и искать
    // будут именно им.
    { name: 'по короткому имени', tag: '', query: 'floq', want: ['AI Sales Assistant'] },
    // Заказчик ищет словами своей боли, а не терминами из описания.
    { name: 'по описанию простыми словами', tag: '', query: 'забывает написать', want: ['AI Sales Assistant'] },
    { name: 'по надзаголовку', tag: '', query: 'b2b', want: ['AI Sales Assistant'] },
    { name: 'регистр не важен', tag: '', query: 'ENGLISH', want: ['English Hub'] },
    { name: 'тег и поиск сужают вместе', tag: 'go', query: 'дубл', want: ['Движок базы знаний'] },
    { name: 'совпадений нет', tag: '', query: 'зззз', want: [] },
  ]

  for (const t of tests) {
    it(t.name, () => {
      const got = filterSections(sections, { tag: t.tag, query: t.query })
      expect(got.flatMap((s) => (s.cards ?? []).map((c) => c.title))).toEqual(t.want)
    })
  }

  // Пустая секция — это заголовок над пустотой: с ним страница выглядит
  // сломанной, а не отфильтрованной.
  it('секция без совпадений выпадает целиком', () => {
    const got = filterSections(sections, { tag: 'edu', query: '' })
    expect(got.map((s) => s.title)).toEqual(['Обучающие продукты'])
  })
})

describe('countCards', () => {
  it('считает карточки во всех секциях', () => {
    expect(countCards(sections)).toBe(3)
  })

  it('пустой список — ноль', () => {
    expect(countCards([])).toBe(0)
  })
})
