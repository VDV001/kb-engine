// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
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
  it('берет фразу окна из {"error"}', async () => {
    vi.stubGlobal('fetch', answer(502, JSON.stringify({ error: 'нет связи с сервером — проверьте адрес и VPN' })))
    await expect(api.stats()).rejects.toThrow('нет связи с сервером — проверьте адрес и VPN')
  })

  it('без фразы — код словами и что делать, тело сервера не показывает', async () => {
    vi.stubGlobal('fetch', answer(500, 'open /Users/x/ledger.jsonl: denied', 'text/plain'))
    const err = await api.stats().catch((e: Error) => e)
    expect(String(err)).toContain(' — ')
    expect(String(err)).not.toContain('/Users/')
    expect(String(err)).not.toContain('Internal Server Error')
  })
})

describe('ErrorBox', () => {
  it('делит фразу на «что не так» и «что сделать»', () => {
    render(<ErrorBox message="kb-сервер ответил ошибкой — попробуйте позже" />)
    expect(screen.getByText('kb-сервер ответил ошибкой')).toBeTruthy()
    expect(screen.getByText(/Что сделать:/).textContent).toContain('попробуйте позже')
  })

  it('без « — » остается одной строкой', () => {
    render(<ErrorBox message="что-то сломалось" />)
    expect(screen.getByText(/что-то сломалось/)).toBeTruthy()
    expect(screen.queryByText(/Что сделать:/)).toBeNull()
  })
})
