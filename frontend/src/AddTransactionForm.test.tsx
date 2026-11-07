// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AddError } from './api'
import { AddTransactionForm } from './AddTransactionForm'

const add = vi.hoisted(() => vi.fn())
vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), api: { addTransaction: add } }))

beforeEach(() => add.mockReset())
afterEach(cleanup)

function fill(label: RegExp, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } })
}

describe('AddTransactionForm', () => {
  it('без суммы запрос не уходит', async () => {
    render(<AddTransactionForm transactions={[]} accounts={[]} today="2026-11-06" onAdded={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /записать/i }))
    expect(await screen.findByText(/введите сумму/i)).toBeTruthy()
    expect(add).not.toHaveBeenCalled()
  })

  // Подпись валюты короткая, пояснение — в поле: длинная переносилась и
  // сдвигала ряд полей (#496).
  it('подпись валюты в одно слово, пояснение — подсказкой в поле', () => {
    render(<AddTransactionForm transactions={[]} accounts={[]} today="2026-11-06" onAdded={() => {}} />)
    expect((screen.getByLabelText('Валюта') as HTMLInputElement).placeholder).toBe('по счету')
  })

  it('записывает и сообщает, что записано, с поправками сервера', async () => {
    add.mockResolvedValue({ id: '01A', date: '2026-11-06', amount: '418.50', corrections: [{ field: 'place', typed: 'кб', used: 'К&Б' }] })
    const onAdded = vi.fn()
    render(<AddTransactionForm transactions={[]} accounts={[]} today="2026-11-06" onAdded={onAdded} />)
    fill(/сумма/i, '418,50')
    fill(/место/i, 'кб')
    fireEvent.click(screen.getByRole('button', { name: /записать/i }))

    expect(await screen.findByText(/записано/i)).toBeTruthy()
    expect(screen.getByText(/К&Б/)).toBeTruthy()
    expect(add).toHaveBeenCalledWith(expect.objectContaining({ kind: 'expense', amount: '418,50', place: 'кб', date: '2026-11-06' }))
    expect(onAdded).toHaveBeenCalledTimes(1)
    expect((screen.getByLabelText(/сумма/i) as HTMLInputElement).value).toBe('') // готово к следующей
  })

  it('повтор: показывает найденную запись и пишет только по явной кнопке', async () => {
    add.mockRejectedValueOnce(new AddError(409, 'repeat: 2026-11-06 418.50 Такси'))
    add.mockResolvedValueOnce({ id: '01B', date: '2026-11-06', amount: '418.50' })
    render(<AddTransactionForm transactions={[]} accounts={[]} today="2026-11-06" onAdded={() => {}} />)
    fill(/сумма/i, '418,50')
    fireEvent.click(screen.getByRole('button', { name: /записать/i }))

    expect(await screen.findByText(/уже есть/i)).toBeTruthy()
    expect(add).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: /все равно записать/i }))
    expect(await screen.findByText(/записано/i)).toBeTruthy()
    expect(add).toHaveBeenLastCalledWith(expect.objectContaining({ force: true }))
  })
})
