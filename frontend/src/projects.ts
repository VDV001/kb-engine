import type { ProjectCard, ProjectSection } from './api'

// Pure logic behind the projects view: which cards a filter leaves standing and
// which filters the page offers at all. Kept out of the component so the rules
// a client sees can be checked without rendering anything.

export interface ProjectFilter {
  tag: string
  query: string
}

export const emptyProjectFilter: ProjectFilter = { tag: '', query: '' }

/** Одна кнопка-фильтр: тег, подпись на ней и сколько проектов за ней стоит. */
export interface TagFilter {
  tag: string
  label: string
  count: number
}

// Подписи фильтров. Тег — ключ данных («nextjs»), подпись — то, что читает
// человек («Next.js»). Тега, которого здесь нет, это не ломает: он покажется
// собой, и отставание словаря от данных будет видно, а не заглажено.
const TAG_LABEL: Record<string, string> = {
  go: 'Go',
  nextjs: 'Next.js',
  react: 'React',
  ai: 'AI / LLM',
  ddd: 'DDD',
  edu: 'Обучение',
  tender: 'Тендеры',
  corporate: 'Корпоративные',
}

export function tagLabel(tag: string): string {
  return TAG_LABEL[tag] ?? tag
}

/**
 * projectFilters собирает кнопки фильтра из тегов карточек.
 *
 * Именно из карточек, а не из списка рядом с разметкой: второй список — второй
 * источник правды, и расходится он молча, в тот день, когда у проекта
 * появляется новый тег.
 *
 * Порядок: сначала частые (за ними больше проектов, их и нажимают), при равном
 * счёте — по алфавиту, чтобы порядок не зависел от порядка карточек в файле.
 */
export function projectFilters(sections: ProjectSection[]): TagFilter[] {
  const counts = new Map<string, number>()
  for (const s of sections) {
    for (const c of s.cards ?? []) {
      for (const t of c.tags ?? []) {
        counts.set(t, (counts.get(t) ?? 0) + 1)
      }
    }
  }
  return [...counts.entries()]
    .map(([tag, count]) => ({ tag, label: tagLabel(tag), count }))
    .sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag))
}

/**
 * Поиск идёт по всем полям, которыми проект называют: техническое название,
 * короткое имя, надзаголовок, оба описания и теги.
 *
 * Описание простыми словами здесь не для полноты: страницу показывают
 * заказчику, и ищет он словами своей боли, а не терминами из body.
 */
function matches(card: ProjectCard, q: string): boolean {
  if (q === '') return true
  const haystack = [
    card.title,
    card.short,
    card.kicker,
    card.note,
    card.body,
    card.plain,
    ...(card.tags ?? []),
  ]
  return haystack.some((v) => v?.toLowerCase().includes(q))
}

/**
 * filterSections отдаёт секции, в которых что-то осталось.
 *
 * Пустая секция выбрасывается целиком: заголовок над пустотой читается как
 * поломка страницы, а не как результат фильтра.
 */
export function filterSections(sections: ProjectSection[], filter: ProjectFilter): ProjectSection[] {
  const q = filter.query.trim().toLowerCase()
  return sections
    .map((s) => ({
      ...s,
      cards: (s.cards ?? []).filter(
        (c) => (filter.tag === '' || (c.tags ?? []).includes(filter.tag)) && matches(c, q),
      ),
    }))
    .filter((s) => s.cards.length > 0)
}

export function countCards(sections: ProjectSection[]): number {
  return sections.reduce((n, s) => n + (s.cards?.length ?? 0), 0)
}
