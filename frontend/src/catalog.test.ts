import { describe, expect, it } from 'vitest'
import type { Entry } from './api'
import { emptyFilter, filterEntries, pageWindow, sortByDate, statusOf } from './catalog'

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
    ['napodumat', 'napodumat', 'На подумать'],
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
