import type { Entry } from './api'
import { artefactHref, cheatsheetsOf, opensInBrowser } from './artefacts'
import { Card, Section } from './components/ui'
import { Icon } from './components/Icon'

// Вкладка со шпаргалками — вход к тем артефактам, которые открывают чаще всего
// и не ищут по названию: заходишь посмотреть клавишу, а не читать каталог.
//
// Отбор и адрес живут в artefacts.ts, здесь только показ. Разделение не
// формальное: отбор проверяется чистыми тестами без разметки, и правило «что
// считать шпаргалкой» не размазывается по виду.
export function CheatsheetsView({ entries }: { entries: readonly Entry[] }) {
  const sheets = cheatsheetsOf(entries)
  // Сколько своих артефактов есть помимо шпаргалок — конспекты, курсы, разборы.
  // Их вкладка не показывает намеренно, и об этом сказано вслух: раздел,
  // умалчивающий о соседях, выглядит полным. Считаются ОТДЕЛЬНО открываемые и
  // те, что браузер скачал бы: одно число на два разных случая скрыло бы, что
  // у большинства кнопки нет.
  const rest = entries.filter((e) => e.file && !sheets.includes(e))
  const openable = rest.filter((e) => opensInBrowser(e.file)).length
  const notOpenable = rest.length - openable

  return (
    <div className="space-y-8">
      <Section
        title="Шпаргалки"
        subtitle={
          sheets.length
            ? `${sheets.length} собственных страниц базы. Открываются из витрины, а не из файловой системы.`
            : 'Ни одной записи с тегом cheatsheet и собственным файлом.'
        }
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {sheets.map((e) => (
            <a
              key={e.id}
              href={artefactHref(e.file)}
              target="_blank"
              rel="noreferrer"
              className="group block focus:outline-none"
            >
              <Card className="h-full transition group-hover:border-secondary">
                <div className="flex items-start justify-between gap-3">
                  <h3 className="text-lg leading-snug group-hover:underline">{e.title}</h3>
                  <Icon
                    name="school"
                    className="mt-1 shrink-0 text-base text-secondary"
                  />
                </div>
                {e.description && (
                  <p className="mt-2 line-clamp-3 text-sm text-on-surface-variant">
                    {e.description}
                  </p>
                )}
                <p className="mt-3 font-label text-[10px] font-semibold tracking-wider text-on-surface-variant uppercase">
                  #{e.id}
                  {e.date_added ? ` · обновлено ${e.date_added}` : ''}
                </p>
              </Card>
            </a>
          ))}
        </div>
      </Section>

      {rest.length > 0 && (
        <p className="text-sm text-on-surface-variant">
          Помимо шпаргалок база держит ещё {rest.length} собственных артефактов — конспекты, курсы,
          разборы, стандарты. Открываются той же кнопкой в архиве {openable}.
          {notOpenable > 0 &&
            ` Ещё ${notOpenable} — в форматах, которые браузер скачал бы вместо показа, поэтому кнопки у них нет.`}
        </p>
      )}
    </div>
  )
}
