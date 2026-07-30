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
  // «На подумать» — решение, read state — закладка; решение старше.
  it('lets the verdict outrank the read state', () => {
    expect(statusOf(entry({ verdict: 'napodumat', read_state: 'unread' }))).toBe('на подумать')
  })
  it('falls back to read state, then lifecycle', () => {
    expect(statusOf(entry({ read_state: 'unread' }))).toBe('unread')
    expect(statusOf(entry({}))).toBe('active')
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
