// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AddError, api } from './api'
import { ErrorBox } from './components/ui'

// Отказ показывается человеку словами и с тем, что делать, а не «502 Bad
// Gateway»: окно DVK кладет фразу «что не так — что сделать» в {"error"}.
afterEach(() => {
  vi.unstubAllGlobals()
  cleanup()
})

const answer = (status: number, body: string, type = 'application/json') =>
  vi.fn(async () => new Response(body, { status, headers: { 'Content-Type': type } }))

describe('текст отказа', () => {
  it('фраза окна из {"error"} — «что не так» и «Что сделать» строками', async () => {
    vi.stubGlobal('fetch', answer(502, JSON.stringify({ error: 'нет связи с сервером — проверьте адрес и VPN' })))
    await expect(api.stats()).rejects.toThrow('нет связи с сервером\nЧто сделать: проверьте адрес и VPN')
  })

  it('делит по последнему « — »: тире внутри текста сервера остается в «что не так»', async () => {
    vi.stubGlobal('fetch', answer(502, JSON.stringify({ error: 'kb-сервер отказал: поле — пусто — обновите окно' })))
    await expect(api.stats()).rejects.toThrow('kb-сервер отказал: поле — пусто\nЧто сделать: обновите окно')
  })

  it('без фразы — путь и код, без тела сервера и без отсылки к терминалу', async () => {
    vi.stubGlobal('fetch', answer(404, 'open /Users/x/ledger.jsonl: denied', 'text/plain'))
    const msg = String(await api.stats().catch((e: Error) => e.message))
    expect(msg).toContain('api/stats')
    expect(msg).toContain('404')
    expect(msg).not.toContain('/Users/')
    expect(msg).not.toMatch(/терминал/)
  })

  it('форма операции получает фразу окна, а не сырой JSON', async () => {
    vi.stubGlobal('fetch', answer(502, JSON.stringify({ error: 'нет связи с сервером — проверьте адрес и VPN' })))
    const err = await api.addTransaction({ kind: 'expense', amount: '1' }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(AddError)
    expect((err as AddError).text).toBe('нет связи с сервером\nЧто сделать: проверьте адрес и VPN')
  })

  it('форма операции: текст сервера без JSON — как есть', async () => {
    vi.stubGlobal('fetch', answer(409, 'такая запись уже есть', 'text/plain'))
    const err = await api.addTransaction({ kind: 'expense', amount: '1' }).catch((e: unknown) => e)
    expect((err as AddError).text).toBe('такая запись уже есть')
  })
})

describe('dashboard: причина отказа финансов', () => {
  const route = (finances: () => Promise<Response>) =>
    vi.fn(async (url: string) => (String(url).includes('api/finances') ? finances() : new Response('{}', { status: 200 })))

  it('фраза окна — в financesError', async () => {
    vi.stubGlobal('fetch', route(async () => new Response(JSON.stringify({ error: 'нет связи с сервером — проверьте адрес и VPN' }), { status: 502 })))
    const d = await api.dashboard()
    expect(d.finances).toBeNull()
    expect(d.financesError).toBe('нет связи с сервером\nЧто сделать: проверьте адрес и VPN')
  })

  it('сеть упала («Failed to fetch») — причины нет, экран говорит прежнее', async () => {
    vi.stubGlobal('fetch', route(async () => { throw new TypeError('Failed to fetch') }))
    const d = await api.dashboard()
    expect(d.finances).toBeNull()
    expect(d.financesError).toBeUndefined()
  })
})

describe('ErrorBox', () => {
  it('фразу окна показывает двумя строками', () => {
    render(<ErrorBox message={'kb-сервер ответил ошибкой\nЧто сделать: попробуйте позже'} />)
    expect(screen.getByText(/kb-сервер ответил ошибкой/).textContent).toContain('Что сделать: попробуйте позже')
  })

  it('обычное « — » в тексте не превращает объяснение в совет', () => {
    render(<ErrorBox message="карта не найдена — id устарел после перестройки" />)
    expect(screen.getByText(/карта не найдена — id устарел/)).toBeTruthy()
    expect(screen.queryByText(/Что сделать/)).toBeNull()
  })
})
