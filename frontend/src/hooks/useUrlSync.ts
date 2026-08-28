import { useEffect } from 'react'
import { writeUrlState, type Tab } from '../urlstate'

// Держит адресную строку в согласии с тем, что видно на экране, чтобы ссылку на
// текущую выборку можно было просто скопировать.
//
// Запись идёт replaceState, а не pushState: пролистывать поиск кнопкой «назад»
// никто не просил, а история из полусотни шагов мешает уйти со страницы вовсе.
export function useUrlSync(tab: Tab, search: string): void {
  useEffect(() => {
    const next = writeUrlState(tab, search)
    if (next !== window.location.search) {
      window.history.replaceState(null, '', next || window.location.pathname)
    }
  }, [tab, search])
}
