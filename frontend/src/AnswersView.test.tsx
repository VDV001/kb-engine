// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const toolCalls = vi.fn()
vi.mock('./api', () => ({ api: { toolCalls: () => toolCalls() } }))
import { AnswersView } from './AnswersView'

afterEach(cleanup)

const calls = [
  { tool: 'search_catalog', query: 'harness', at: '2026-08-28T14:00:00+05:00', ok: true },
  { tool: 'get_entry', query: '#9999', at: '2026-08-28T13:00:00+05:00', ok: false },
  { tool: 'stats', at: '2026-08-27T10:00:00+05:00', ok: true },
]

describe('AnswersView', () => {
  it('показывает, о чём спрашивали, и отделяет отказ от ответа', async () => {
    toolCalls.mockResolvedValue({ exists: true, total: 3, calls })
    render(<AnswersView onAskAgain={() => {}} />)

    expect(await screen.findByText(/harness/)).toBeDefined()
    expect(screen.getByText(/#9999/)).toBeDefined()
    // Два дня в журнале — значит и заголовков дней два.
    expect(screen.getAllByTestId('answers-day')).toHaveLength(2)
    // Отказ виден на странице, а не только в журнале.
    expect(screen.getByTestId('answers-failed').textContent).toContain('1')
  })

  // ⚠️ Журнала может не быть вовсе — движок поднимают и без него, а старый
  // бинарь MCP-сервера его не ведёт. Пустая вкладка читалась бы как «агент
  // базу не спрашивал», и это ровно та тишина, ради которой счётчик заводился.
  it('отсутствие журнала называет вслух, а не показывает пустоту', async () => {
    toolCalls.mockResolvedValue({ exists: false, total: 0, calls: [] })
    render(<AnswersView onAskAgain={() => {}} />)
    expect((await screen.findByTestId('answers-empty')).textContent).toMatch(/журнал/i)
  })

  // Пустой журнал и отсутствующий журнал — разные ответы, и различаются они
  // текстом, а не одинаковой пустотой.
  it('пустой журнал отличается от отсутствующего', async () => {
    toolCalls.mockResolvedValue({ exists: true, total: 0, calls: [] })
    render(<AnswersView onAskAgain={() => {}} />)
    const text = (await screen.findByTestId('answers-empty')).textContent ?? ''
    expect(text).toMatch(/ни одного вызова|не спрашивал/i)
    expect(text).not.toMatch(/журнала нет/i)
  })

  it('клик по запросу переспрашивает его в архиве', async () => {
    toolCalls.mockResolvedValue({ exists: true, total: 3, calls })
    const onAskAgain = vi.fn()
    render(<AnswersView onAskAgain={onAskAgain} />)
    fireEvent.click(await screen.findByRole('button', { name: /harness/ }))
    expect(onAskAgain).toHaveBeenCalledWith('harness')
  })
})
