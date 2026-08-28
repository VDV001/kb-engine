// Адрес витрины как состояние: какая вкладка открыта и что ищем.
//
// Заведено потому, что ссылку на конкретную выборку до сих пор нельзя было дать
// никому — ни человеку в переписке, ни инструменту, отвечающему агенту. Поиск и
// вкладка жили в состоянии React, и адрес всегда означал «главная, пусто».
//
// Роутера здесь нет намеренно: две величины в строке запроса покрывает нативный
// URLSearchParams, а библиотека маршрутизации принесла бы зависимость, историю
// переходов и перерисовку ради того, что делают три строки.

export type Tab =
  | 'overview'
  | 'archives'
  | 'cheatsheets'
  | 'answers'
  | 'analytics'
  | 'health'
  | 'finances'
  | 'projects'
  | 'team'
  | 'now'
  | 'architecture'
  | 'about'

// Порядок показа вкладок — четыре группы: база знаний, витрина и оперативка,
// приватное, служебное. Список живёт здесь, а не в App, потому что разбор
// адреса обязан знать, какая вкладка существует, а какая пришла из опечатки;
// два списка разошлись бы, и незнакомая вкладка стала бы открываться.
export const TAB_IDS = [
  'overview',
  'archives',
  'cheatsheets',
  'analytics',
  'projects',
  'now',
  'team',
  'finances',
  // «Ответы» — служебная вкладка, а не содержание базы: она про то, КАК базой
  // пользовались, ровно как Health про её состояние. Рядом со шпаргалками она
  // читалась бы как ещё один раздел с материалами.
  'answers',
  'health',
  'architecture',
  'about',
] as const satisfies readonly Tab[]

// Вкладка по умолчанию в адрес не пишется: «пусто» и «tab=overview» означают
// одно и то же, а две формы одного состояния однажды разойдутся.
const DEFAULT_TAB: Tab = 'overview'

/** Откуда пришла ссылка. Пока источник один — ответ инструмента MCP. */
export type Source = 'mcp'

const SOURCES = ['mcp'] as const satisfies readonly Source[]

export type UrlState = { tab?: Tab; q?: string; src?: Source }

export function readUrlState(search: string): UrlState {
  const p = new URLSearchParams(search)
  const state: UrlState = {}
  const tab = p.get('tab')
  // Незнакомая вкладка приходит из чужой ссылки и из опечатки. Открывать её
  // нельзя, падать незачем: адрес — подсказка, а не команда.
  if (tab && (TAB_IDS as readonly string[]).includes(tab)) state.tab = tab as Tab
  const q = p.get('q')
  if (q) state.q = q
  // Происхождение ссылка объявляет сама. Догадка «раз есть запрос, значит
  // пришли по ссылке» назвала бы ответом агента любой набранный руками поиск;
  // чужое значение не принимается по той же причине, что чужая вкладка.
  const src = p.get('src')
  if (src && (SOURCES as readonly string[]).includes(src)) state.src = src as Source
  return state
}

// Происхождение обратно в адрес не пишется: отметка живёт до первой своей
// правки, иначе перезагрузка страницы через час выдавала бы собственный поиск
// владельца за ответ агента.
export function writeUrlState(tab: Tab, q: string): string {
  const p = new URLSearchParams()
  if (tab !== DEFAULT_TAB) p.set('tab', tab)
  if (q) p.set('q', q)
  const s = p.toString()
  return s ? `?${s}` : ''
}
