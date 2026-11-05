import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
import { appURL } from './appurl'

// Окно DVK отдает витрину под /app/ и проксирует /app/api/* на kb-сервер с
// токеном (план G): каждый запрос обязан идти от корня приложения. Методы
// перебираются из самого api, а не списком: новый метод с абсолютным путем
// иначе прошел бы мимо проверки.
const args: Record<string, unknown[]> = {
  search: ['индекс b-tree'],
  map: ['kb-engine'],
  financeSummary: [['2026-10', '2026-11']],
  financeExport: [[]],
}
// dashboard — пачка из остальных методов, каждый из которых проверен сам.
const methods = Object.entries(api).filter(([name, fn]) => typeof fn === 'function' && name !== 'dashboard')

afterEach(() => vi.unstubAllGlobals())

describe('запросы идут от корня приложения', () => {
  it('методов достаточно, перебор не пуст', () => {
    expect(methods.length).toBeGreaterThan(20)
  })

  it.each(methods)('%s', async (name, fn) => {
    const fetch = vi.fn(async () => new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    await (fn as (...a: unknown[]) => Promise<unknown>)(...(args[name] ?? [])).catch(() => undefined)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(String((fetch.mock.calls[0] as unknown[])[0]).startsWith(appURL('api/'))).toBe(true)
  })
})
