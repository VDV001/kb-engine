import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'

// Окно DVK отдает витрину под /app/ и проксирует /app/api/* на kb-сервер с
// токеном (план G). Абсолютный путь /api/... ушел бы мимо прокси, поэтому
// каждый запрос обязан быть относительным: под kbengine serve (страница в
// корне) он остается /api/..., в окне становится /app/api/....
const calls: [string, () => Promise<unknown>][] = [
  ['stats', () => api.stats()],
  ['entries', () => api.entries()],
  ['search', () => api.search('индекс b-tree')],
  ['audits', () => api.audits()],
  ['duplicates', () => api.duplicates()],
  ['linkHealth', () => api.linkHealth()],
  ['analytics', () => api.analytics()],
  ['analyticsConfig', () => api.analyticsConfig()],
  ['graph', () => api.graph()],
  ['changelog', () => api.changelog()],
  ['engine', () => api.engine()],
  ['capabilities', () => api.capabilities()],
  ['toolCalls', () => api.toolCalls()],
  ['now', () => api.now()],
  ['sources', () => api.sources()],
  ['maps', () => api.maps()],
  ['map', () => api.map('kb-engine')],
  ['team', () => api.team()],
  ['projects', () => api.projects()],
  ['finances', () => api.finances()],
  ['financeSummary', () => api.financeSummary()],
  ['financeSummary за месяцы', () => api.financeSummary(['2026-10', '2026-11'])],
  ['financeExport', () => api.financeExport([])],
]

afterEach(() => vi.unstubAllGlobals())

describe('пути API относительны', () => {
  it.each(calls)('%s уходит под префикс страницы', async (_, call) => {
    const fetch = vi.fn(async () => new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    await call().catch(() => undefined)
    expect(fetch).toHaveBeenCalledTimes(1)
    const path = String((fetch.mock.calls[0] as unknown[])[0])
    expect(path.startsWith('/')).toBe(false)
    expect(new URL(path, 'https://dvk.local/app/').pathname.startsWith('/app/api/')).toBe(true)
    expect(new URL(path, 'http://127.0.0.1:8080/').pathname.startsWith('/api/')).toBe(true)
  })
})
