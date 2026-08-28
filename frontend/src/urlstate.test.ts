import { describe, expect, it } from 'vitest'
import { readUrlState, writeUrlState, TAB_IDS } from './urlstate'

describe('адресуемость витрины', () => {
  it('читает вкладку и запрос из адресной строки', () => {
    expect(readUrlState('?tab=archives&q=harness')).toEqual({ tab: 'archives', q: 'harness' })
  })

  it('пустой адрес не задаёт ничего — умолчания остаются за приложением', () => {
    expect(readUrlState('')).toEqual({})
  })

  // Неизвестная вкладка приходит из чужой ссылки и из опечатки. Открыть её
  // нельзя, но и падать незачем: адрес — подсказка, а не команда.
  it('незнакомую вкладку отбрасывает, запрос сохраняет', () => {
    expect(readUrlState('?tab=выдумка&q=go')).toEqual({ q: 'go' })
  })

  it('собирает адрес обратно, чтобы ссылку можно было скопировать', () => {
    expect(writeUrlState('archives', 'harness')).toBe('?tab=archives&q=harness')
  })

  // Пустой поиск в адресе — мусор: ссылка должна быть короткой и означать ровно
  // то, что видно на экране.
  it('пустой запрос в адрес не пишет', () => {
    expect(writeUrlState('archives', '')).toBe('?tab=archives')
  })

  // Умолчание тоже не пишем: адрес без параметров и адрес с tab=overview
  // означают одно и то же, а две формы одного состояния расходятся.
  it('вкладку по умолчанию в адрес не пишет', () => {
    expect(writeUrlState('overview', '')).toBe('')
  })

  it('переживает круг: что записали, то и прочли', () => {
    expect(readUrlState(writeUrlState('finances', 'юрент'))).toEqual({ tab: 'finances', q: 'юрент' })
  })

  // Список вкладок переехал сюда из App, и перенос мог молча переставить их
  // порядок: ни типы, ни сборка этого не видят, а человек видит сразу.
  it('порядок вкладок остался прежним', () => {
    expect([...TAB_IDS]).toEqual([
      'overview',
      'archives',
      'cheatsheets',
      'analytics',
      'projects',
      'now',
      'team',
      'finances',
      'health',
      'architecture',
      'about',
    ])
  })
})

// Ссылка из ответа агента объявляет себя сама: узнать происхождение иначе
// нельзя, а догадываться по наличию запроса — значит называть ответом агента
// собственный поиск владельца.
describe('происхождение ссылки', () => {
  it('читает src=mcp', () => {
    expect(readUrlState('?tab=archives&q=ddd&src=mcp').src).toBe('mcp')
  })

  it('чужое значение src не принимается: адрес — подсказка, а не команда', () => {
    expect(readUrlState('?q=ddd&src=выдумка').src).toBeUndefined()
  })

  // Отметка живёт до первой своей правки: адрес переписывается на месте без
  // неё, иначе перезагрузка страницы через час выдавала бы свой поиск за
  // ответ агента.
  it('в адрес обратно не пишется', () => {
    expect(writeUrlState('archives', 'ddd')).toBe('?tab=archives&q=ddd')
  })
})
