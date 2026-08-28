import { describe, expect, it } from 'vitest'
import { linkedQueryOf, selectionOf } from './selection'

describe('полоса контекста над архивом', () => {
  const base = { total: 1573, shown: 80, searching: false, linkedQuery: '' }

  it('без запроса полосы нет: показан весь каталог, объяснять нечего', () => {
    expect(selectionOf({ ...base, query: '', shown: 1573 })).toBeNull()
    expect(selectionOf({ ...base, query: '   ', shown: 1573 })).toBeNull()
  })

  it('называет запрос и оба числа — иначе «80 записей» не отличить от всей базы', () => {
    const s = selectionOf({ ...base, query: 'ddd' })
    expect(s).toEqual({ query: 'ddd', shown: 80, total: 1573, fromAgent: false })
  })

  // Пока ответ поиска летит, отбор ещё не применён, и в списке лежит ВЕСЬ
  // каталог. Показать это число значило бы соврать «по запросу нашлось 1573».
  it('во время поиска число не называется вовсе', () => {
    const s = selectionOf({ ...base, query: 'ddd', shown: 1573, searching: true })
    expect(s?.shown).toBeNull()
    expect(s?.total).toBe(1573)
  })

  it('ссылка агента отмечается — по ней видно, что это ответ на вопрос', () => {
    const s = selectionOf({ ...base, query: 'ddd', linkedQuery: 'ddd' })
    expect(s?.fromAgent).toBe(true)
  })

  // Отрицательный контроль: как только запрос сменили руками, это уже свой
  // поиск. Оставить отметку значило бы приписать агенту чужой вопрос.
  it('свой запрос поверх ссылки отметку снимает', () => {
    const s = selectionOf({ ...base, query: 'финансы', linkedQuery: 'ddd' })
    expect(s?.fromAgent).toBe(false)
  })

  it('пробелы по краям не делают запрос другим', () => {
    const s = selectionOf({ ...base, query: '  ddd  ', linkedQuery: 'ddd' })
    expect(s).toEqual({ query: 'ddd', shown: 80, total: 1573, fromAgent: true })
  })
})

describe('запрос, пришедший ссылкой', () => {
  it('берётся только при объявленном происхождении', () => {
    expect(linkedQueryOf({ q: 'ddd', src: 'mcp' })).toBe('ddd')
  })

  // Отрицательный контроль: свой поиск владельца тоже лежит в адресе — его
  // туда пишет сама витрина, чтобы ссылку можно было скопировать. Считать его
  // ответом агента значило бы называть ответом агента любой сохранённый поиск.
  it('запрос без отметки происхождения ссылкой не считается', () => {
    expect(linkedQueryOf({ q: 'ddd' })).toBe('')
    expect(linkedQueryOf({})).toBe('')
  })
})
