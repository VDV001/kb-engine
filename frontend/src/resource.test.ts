import { describe, expect, it } from 'vitest'
import { errorMessage } from './resource'

describe('errorMessage', () => {
  // Таблица, а не пачка it() — вариантов больше трёх, и различие между ними
  // ровно в паре вход/выход.
  const cases: { name: string; input: unknown; want: string }[] = [
    { name: 'Error отдаёт свой message', input: new Error('/api/now: 500'), want: '/api/now: 500' },
    { name: 'подкласс Error тоже', input: new TypeError('fetch failed'), want: 'fetch failed' },
    { name: 'строка проходит как есть', input: 'boom', want: 'boom' },
    { name: 'Error без текста не отдаёт пустую строку', input: new Error(''), want: 'Error' },
    { name: 'пустая строка не отдаёт пустую строку', input: '', want: 'Unknown error' },
    { name: 'null', input: null, want: 'null' },
    { name: 'undefined', input: undefined, want: 'undefined' },
    { name: 'число', input: 404, want: '404' },
    { name: 'объект без message', input: { a: 1 }, want: '[object Object]' },
  ]

  for (const c of cases) {
    it(c.name, () => {
      expect(errorMessage(c.input)).toBe(c.want)
    })
  }

  it('никогда не возвращает пустую строку', () => {
    for (const input of [new Error(''), '', null, undefined, 0, false, {}]) {
      expect(errorMessage(input)).not.toBe('')
    }
  })
})
