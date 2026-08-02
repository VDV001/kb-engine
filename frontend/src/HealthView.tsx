import { useState } from 'react'
import { api } from './api'
import type { Audits, DuplicateGroup, Entry, Finding } from './api'
import { useResource } from './hooks/useResource'
import { dateOf, statusOf } from './catalog'
import { Card, Chip, Label, Section } from './components/ui'
import {
  conflictingIds,
  duplicateEntries,
  duplicateKindLabel,
  groupByReason,
  plural,
  reasonLabel,
  type ReasonGroup,
} from './hygiene'

// Здоровье базы: один вид на всю гигиену каталога — три раздела аудита и
// дубли. Раньше это были две вкладки верхнего уровня, и обе почти всегда
// пустые: на 1340 записях dedup находит одну группу, supersession — ноль.
// Две вкладки ради одной строки — самая дорогая недвижимость интерфейса,
// отданная под самое редкое содержимое.

// Команда перепроверки. CLI умеет только читать (ни audit, ни dedup не имеют
// --apply), поэтому кнопка даёт команду, которая ПОКАЖЕТ находки заново, и
// честно не обещает исправить их за тебя. $KB_CATALOG — путь к catalog.json:
// подставить сюда путь с сервера значило бы напечатать чужую файловую систему
// на странице, которую AGPL разрешает открыть наружу.
function auditCommand(check: string): string {
  return `kbengine audit --check ${check} --catalog "$KB_CATALOG"`
}

const DEDUP_COMMAND = 'kbengine dedup --catalog "$KB_CATALOG"'

function CopyButton({ text, title }: { text: string; title: string }) {
  const [done, setDone] = useState(false)
  return (
    <Chip
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => setDone(true),
          // Отказ буфера (нет разрешения, не защищённый контекст) не должен
          // выглядеть как успех: кнопка просто не меняет подпись.
          () => setDone(false),
        )
      }}
    >
      {done ? 'Скопировано' : title}
    </Chip>
  )
}

function ExternalLink({ url }: { url: string }) {
  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer"
      className="shrink-0 text-xs text-on-surface-variant underline decoration-outline-variant underline-offset-2 hover:text-secondary"
      onClick={(e) => e.stopPropagation()}
    >
      оригинал
    </a>
  )
}

function FindingRow({
  finding,
  entry,
  conflict,
  onOpen,
}: {
  finding: Finding
  entry?: Entry
  conflict: boolean
  onOpen: (id: number) => void
}) {
  // Причина, по которой находка попала в группу, стоит в её заголовке —
  // повторять её в каждой строке значит печатать одно слово пятьдесят раз.
  const extra = finding.Reasons.slice(1)
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-outline-variant px-4 py-2.5 text-sm first:border-t-0">
      <button
        type="button"
        onClick={() => onOpen(finding.EntryID)}
        // Нижняя граница ширины, а не min-w-0: заголовок — то, ради чего строка
        // существует, и сжиматься до «У..» ради статуса и плашек он не должен.
        // Не влезло — плашки уходят на вторую строку, строка их и так переносит.
        className="flex min-w-[12rem] flex-1 items-center gap-3 text-left hover:text-secondary"
        title="Открыть запись в архиве"
      >
        <span className="shrink-0 font-mono text-xs tabular-nums text-on-surface-variant">
          #{finding.EntryID}
        </span>
        <span className="truncate">{finding.Title}</span>
      </button>

      {conflict && (
        <span className="shrink-0 rounded-full bg-tag-bg-4 px-2 py-0.5 text-xs text-tag-text-4">
          в двух разделах
        </span>
      )}
      {finding.Current && (
        <span className="shrink-0 label" title="Текущий статус записи">
          {finding.Current}
        </span>
      )}
      {extra.map((r) => (
        <span
          key={r}
          title={r}
          className="shrink-0 rounded bg-surface-high px-1.5 py-0.5 text-xs text-on-surface-variant"
        >
          {reasonLabel(r)}
        </span>
      ))}
      {entry?.url && <ExternalLink url={entry.url} />}
    </li>
  )
}

// Группа разворачивается, если она небольшая: пятьдесят одинаковых строк по
// умолчанию — это стена, а три строки прятать не за чем.
const OPEN_BY_DEFAULT = 8

function ReasonBlock({
  group,
  byID,
  conflicts,
  onOpen,
}: {
  group: ReasonGroup
  byID: Map<number, Entry>
  conflicts: Set<number>
  onOpen: (id: number) => void
}) {
  const [open, setOpen] = useState(group.items.length <= OPEN_BY_DEFAULT)
  const n = group.items.length
  return (
    <div className="rounded-lg border border-outline-variant bg-surface-lowest">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 px-4 py-3 text-left"
        aria-expanded={open}
      >
        <span className="font-headline text-base font-bold">{group.label}</span>
        <span className="rounded-full bg-secondary px-2 py-0.5 font-mono text-xs font-bold text-white tabular-nums">
          {n}
        </span>
        <span className="ml-auto label">{open ? 'свернуть' : 'показать'}</span>
      </button>
      {open && (
        <ul>
          {group.items.map((f) => (
            <FindingRow
              key={f.EntryID}
              finding={f}
              entry={byID.get(f.EntryID)}
              conflict={conflicts.has(f.EntryID)}
              onOpen={onOpen}
            />
          ))}
        </ul>
      )}
    </div>
  )
}

function AuditSection({
  title,
  hint,
  check,
  findings,
  byID,
  conflicts,
  onOpen,
}: {
  title: string
  hint: string
  check: string
  findings: Finding[] | null
  byID: Map<number, Entry>
  conflicts: Set<number>
  onOpen: (id: number) => void
}) {
  const items = findings ?? []
  // Пустой раздел не занимает карточку: «Проблем замен (0)» — это не находка,
  // а её отсутствие, и место на экране оно занимать не должно.
  if (items.length === 0) return null
  const ids = items.map((f) => f.EntryID).join(', ')
  return (
    <Section
      title={title}
      subtitle={`${items.length} ${plural(items.length, ['находка', 'находки', 'находок'])} · ${hint}`}
      aside={
        <div className="flex gap-2">
          <CopyButton text={ids} title="Скопировать id" />
          <CopyButton text={auditCommand(check)} title="Команда" />
        </div>
      }
    >
      <div className="space-y-2">
        {groupByReason(items).map((g) => (
          <ReasonBlock key={g.code} group={g} byID={byID} conflicts={conflicts} onOpen={onOpen} />
        ))}
      </div>
    </Section>
  )
}

function DuplicateCard({
  group,
  entries,
  onOpen,
}: {
  group: DuplicateGroup
  entries: Entry[]
  onOpen: (id: number) => void
}) {
  const members = duplicateEntries(group, entries)
  return (
    <Card>
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-headline text-base font-bold">{duplicateKindLabel(group.Kind)}</span>
        <span className="label">
          {members.length} {plural(members.length, ['запись', 'записи', 'записей'])}
        </span>
        <span className="ml-auto truncate font-mono text-xs text-on-surface-variant" title={group.Key}>
          {group.Key}
        </span>
      </div>

      {/* Две записи рядом, а не два номера через запятую: решение «дубль или
          нет» принимается сравнением заголовков, дат и статусов, и если их не
          показать, за ними всё равно придётся идти в архив — по одной. */}
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        {members.map((e) => {
          const s = statusOf(e)
          return (
            <div key={e.id} className="rounded-md border border-outline-variant bg-surface-low p-3">
              <div className="flex items-center gap-2">
                <span className="font-mono text-xs tabular-nums text-on-surface-variant">#{e.id}</span>
                <span className="label" style={{ color: s.tone }}>
                  {s.label}
                </span>
                <span className="ml-auto label">{dateOf(e) || '—'}</span>
              </div>
              <button
                type="button"
                onClick={() => onOpen(e.id)}
                className="mt-1.5 block w-full text-left text-sm hover:text-secondary"
                title="Открыть запись в архиве"
              >
                {e.title}
              </button>
              <div className="mt-1.5 flex items-center gap-3">
                <span className="truncate text-xs text-on-surface-variant">{e.category}</span>
                {e.url && <ExternalLink url={e.url} />}
              </div>
            </div>
          )
        })}
      </div>
    </Card>
  )
}


const DRIFT_COMMAND = 'kbengine drift --catalog "$KB_CATALOG" --apply'

/**
 * Что скан узнал про адреса базы.
 *
 * Существует потому, что до этого не существовало: результат проверки лежал в
 * каталоге с 01.08 и не попадал ни на один экран — база знала про свои ссылки
 * больше, чем могла сказать.
 *
 * Непроверенные показываются всегда, включая ноль. Это Правило 11 в исходной
 * формулировке: доля живых без числа непроверенных читается как утверждение обо
 * всей базе, хотя относится только к спрошенной её части.
 */
function LinkHealthSection() {
  const res = useResource(api.linkHealth)
  if (res.status !== 'ready') return null
  const h = res.data
  if (h.with_url === 0) return null

  const cells: { n: number; label: string; hint: string; tone: string }[] = [
    { n: h.alive, label: 'отвечают', hint: 'ответ 200', tone: 'text-on-surface' },
    { n: h.moved, label: 'переехали', hint: 'редирект: материал на месте, адрес устарел', tone: 'text-secondary' },
    { n: h.gone, label: 'исчезли', hint: '404 или 410', tone: 'text-primary' },
    { n: h.undecidable, label: 'не знаем', hint: '403 — так отвечают и на снятую статью, и на бота', tone: 'text-on-surface-variant' },
    { n: h.unchecked, label: 'не спрашивали', hint: 'адрес есть, проверки не было ни разу', tone: 'text-on-surface-variant' },
  ]

  return (
    <Section
      title="Здоровье ссылок"
      subtitle={`${h.with_url} ${plural(h.with_url, ['запись с адресом', 'записи с адресом', 'записей с адресом'])} · по последней проверке`}
      aside={<CopyButton text={DRIFT_COMMAND} title="Перепроверить" />}
    >
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {cells.map((c) => (
          <Card key={c.label} className="min-w-0">
            <div className={`text-3xl ${c.tone}`}>{c.n}</div>
            <div className="mt-1 text-sm text-on-surface">{c.label}</div>
            <p className="mt-1 text-xs text-on-surface-variant">{c.hint}</p>
          </Card>
        ))}
      </div>
    </Section>
  )
}

export function HealthView({
  audits,
  duplicates,
  entries,
  onOpenEntry,
}: {
  audits: Audits
  duplicates: DuplicateGroup[]
  entries: Entry[]
  onOpenEntry: (id: number) => void
}) {
  const byID = new Map(entries.map((e) => [e.id, e]))
  const conflicts = new Set(conflictingIds(audits))
  const sections = [audits.outdated, audits.canonical, audits.supersession]
  const total = sections.reduce((n, s) => n + (s?.length ?? 0), 0) + duplicates.length

  return (
    <div className="space-y-8">
      <header>
        <Label className="text-secondary">Гигиена каталога</Label>
        <h1 className="mt-1 text-4xl">Здоровье базы.</h1>
        <p className="mt-2 max-w-3xl text-sm text-on-surface-variant">
          Что движок считает подозрительным: записи, похожие на устаревшие, кандидаты в
          канонические, сломанные ссылки замены и совпадающие записи. Это предложения, а не
          изменения — каталог правишь ты.
        </p>
        {total === 0 && (
          <p className="mt-4 text-sm text-on-surface-variant">
            Находок нет: аудит и поиск дублей ничего не вернули.
          </p>
        )}
      </header>

      {conflicts.size > 0 && (
        <Card className="border-l-2 border-l-secondary">
          <Label className="text-secondary">Сначала это</Label>
          <p className="mt-2 text-sm">
            {conflicts.size}{' '}
            {plural(conflicts.size, ['запись попала', 'записи попали', 'записей попали'])} больше чем
            в один раздел: движок предлагает по ним взаимоисключающее. Решать такие вручную.
          </p>
          <div className="mt-3 flex flex-wrap gap-2">
            {[...conflicts].map((id) => (
              <Chip key={id} onClick={() => onOpenEntry(id)}>
                #{id}
              </Chip>
            ))}
          </div>
        </Card>
      )}

      <LinkHealthSection />

      <AuditSection
        title="Похоже на устаревшие"
        hint="в тексте есть признак, что материала больше нет"
        check="outdated"
        findings={audits.outdated}
        byID={byID}
        conflicts={conflicts}
        onOpen={onOpenEntry}
      />
      <AuditSection
        title="Кандидаты в канонические"
        hint="на них ссылаются другие записи"
        check="canonical"
        findings={audits.canonical}
        byID={byID}
        conflicts={conflicts}
        onOpen={onOpenEntry}
      />
      <AuditSection
        title="Замены не сходятся"
        hint="supersedes_id ведёт не туда"
        check="supersession"
        findings={audits.supersession}
        byID={byID}
        conflicts={conflicts}
        onOpen={onOpenEntry}
      />

      {duplicates.length > 0 && (
        <Section
          title="Дубликаты"
          subtitle={`${duplicates.length} ${plural(duplicates.length, ['группа', 'группы', 'групп'])} · одинаковый адрес или похожий заголовок`}
          aside={<CopyButton text={DEDUP_COMMAND} title="Команда" />}
        >
          <div className="space-y-3">
            {duplicates.map((g) => (
              <DuplicateCard key={`${g.Kind}:${g.Key}`} group={g} entries={entries} onOpen={onOpenEntry} />
            ))}
          </div>
        </Section>
      )}
    </div>
  )
}
