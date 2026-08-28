import { artefactHref, opensInBrowser } from '../artefacts'
import { Icon } from './Icon'

// Ссылка на собственный артефакт базы. Стоит рядом со ссылкой на источник и
// намеренно отличается значком: у чужой статьи «открыть во внешнем», у своей —
// раскрытая книга, иначе две ссылки в одной строке неразличимы.
//
// Новый глиф ради этого не заводился: набор держит 64 пути вручную, а `school`
// свободен и читается как «своё, учебное». Пара «книга — квадрат со стрелкой»
// различается с одного взгляда, что и требовалось.
//
// Открывается в новой вкладке: артефакт это отдельная страница на 12-130 КБ, и
// уводить с витрины, потеряв выборку, незачем.
export function ArtefactLink({ file }: { file?: string }) {
  const href = opensInBrowser(file) ? artefactHref(file) : ''
  if (!href) return null
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="shrink-0 text-secondary hover:underline"
      aria-label="Открыть артефакт базы"
      title="Открыть артефакт базы"
    >
      <Icon name="school" className="text-base" />
    </a>
  )
}
