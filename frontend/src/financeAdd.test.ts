import { describe, expect, it } from 'vitest'
import { addFailure, emptyAddForm, suggestions, toAddRequest } from './financeAdd'
import type { Transaction } from './api'

// Форма операции (#435): запрос — то же, что флаги kbengine fin add; разбор
// суммы и даты остается серверу, здесь только поля и пояс человека.
describe('toAddRequest', () => {
  const base = { ...emptyAddForm('2026-11-06'), amount: ' 418,50 ', place: ' Такси ', category: 'Транспорт' }

  it('трата: поля траты, обрезанные пробелы, пояс человека', () => {
    expect(toAddRequest(base, 'Asia/Yekaterinburg')).toEqual({
      kind: 'expense',
      date: '2026-11-06',
      amount: '418,50',
      category: 'Транспорт',
      place: 'Такси',
      tz: 'Asia/Yekaterinburg',
    })
  })

  it('доход: источник вместо категории и места', () => {
    const f = { ...base, kind: 'income' as const, source: 'Зарплата' }
    expect(toAddRequest(f, 'UTC')).toEqual({ kind: 'income', date: '2026-11-06', amount: '418,50', source: 'Зарплата', tz: 'UTC' })
  })

  it('повтор подтвержден — force', () => {
    expect(toAddRequest(base, 'UTC', true).force).toBe(true)
  })
})

describe('addFailure', () => {
  it.each([
    [409, 'repeat: такая запись уже есть', 'repeat', /уже есть/],
    [404, '404 page not found', 'unsupported', /не принимает запись/],
    [405, 'method not allowed', 'unsupported', /не принимает запись/],
    [400, 'amount is required', 'bad', /amount is required/],
    [403, 'cross-site write refused', 'bad', /cross-site/],
    [500, 'internal', 'server', /не записал/],
    [502, 'нет связи с сервером — проверьте адрес и VPN', 'server', /нет связи/],
  ])('%i → %s', (status, text, kind, says) => {
    const f = addFailure(status, text)
    expect(f.kind).toBe(kind)
    expect(f.message).toMatch(says)
  })
})

describe('suggestions', () => {
  const tx = (p: Partial<Transaction>): Transaction => ({ id: 'x', kind: 'expense', date: '2026-11-01', amount: '1', ...p })

  it('частые выше, без пустых и повторов', () => {
    const s = suggestions([
      tx({ category: 'Еда', place: 'Магнит' }),
      tx({ category: 'Еда', place: 'Магнит' }),
      tx({ category: 'Транспорт', place: '' }),
      tx({ kind: 'income', source: 'Зарплата' }),
    ])
    expect(s.categories).toEqual(['Еда', 'Транспорт'])
    expect(s.places).toEqual(['Магнит'])
    expect(s.sources).toEqual(['Зарплата'])
  })
})
