import type { CSSProperties } from 'react'
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

/** Как нарисовать обложку карточки: класс из палитры и/или инлайновый фон. */
export interface Cover {
  className: string
  style: CSSProperties | undefined
}

// Имя акцента подставляется в класс, а файл владельца правится руками, поэтому
// в класс проходят только буквы, цифры и дефис. Всё прочее — не имя палитры, и
// карточка получает нейтральную плашку вместо разметки с чужой кавычкой.
const ACCENT_NAME = /^[a-z0-9-]+$/

/**
 * cover решает, чем закрашена подложка карточки: именованный градиент палитры,
 * готовая строка CSS из файла владельца или нейтральная тёмная плашка.
 *
 * Скриншот сюда не входит намеренно. Фоном он либо обрезается по краям
 * (background-size: cover срезал шапку снимка — владелец это и увидел), либо
 * оставляет непредсказуемые поля, и подпись поверх него всё равно наезжает на
 * содержимое. Картинка рисуется отдельным слоем и вписывается целиком, а
 * подпись остаётся на градиенте, где она читается всегда.
 */
export function cover(card: ProjectCard): Cover {
  const accent = card.accent ?? ''
  if (accent.startsWith('linear-gradient') || accent.startsWith('radial-gradient')) {
    return { className: '', style: { background: accent } }
  }
  if (ACCENT_NAME.test(accent)) {
    return { className: `proj-${accent}`, style: undefined }
  }
  return { className: '', style: { background: 'var(--card-spotlight-bg)' } }
}

// Статусы владелец пишет словами, и слов больше, чем состояний. Сопоставление
// живёт здесь одной таблицей, а не условиями в разметке.
const BADGE_TONE: Record<string, string> = {
  Production: 'badge-prod',
  'Прод · Vercel': 'badge-prod',
  'В разработке': 'badge-dev',
  Pilot: 'badge-dev',
  MVP: 'badge-dev',
  'Опубликован OSS': 'badge-published',
  Опубликована: 'badge-published',
  'Private repo': 'badge-private',
}

/** Незнакомый статус получает нейтральный тон: выдать непонятое за «работает» —
 * худшее из умолчаний, потому что именно этому бейджу заказчик верит. */
export function badgeTone(badge: string): string {
  return BADGE_TONE[badge] ?? 'badge-private'
}
