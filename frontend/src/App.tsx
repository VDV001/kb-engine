import { useState } from 'react'
import { api } from './api'
import { useResource } from './hooks/useResource'
import { AnalyticsView } from './AnalyticsView'
import { CatalogView } from './CatalogView'
import { DocumentView, NowView } from './DocViews'
import { Header } from './components/Header'
import { ErrorBox, Spinner } from './components/ui'
import { FinancesView } from './FinancesView'
import { UnreadableBanner } from './UnreadableBanner'
import { PrivacyToggle } from './components/PrivacyToggle'
import { SearchBox } from './components/SearchBox'
import { SourceOffer } from './components/SourceOffer'
import { DashboardView } from './DashboardView'
import { HealthView } from './HealthView'
import { findingCount } from './hygiene'
import { ProjectsView } from './ProjectsView'
import { AboutView } from './AboutView'
import { ArchitectureView } from './ArchitectureView'
import { readUrlState, TAB_IDS, type Tab } from './urlstate'
import { linkedQueryOf } from './selection'
import { useUrlSync } from './hooks/useUrlSync'
import { CheatsheetsView } from './CheatsheetsView'


// Порядок вкладок — это четыре группы, а не список: база знаний, витрина и
// оперативка, приватное, служебное. Audits и Duplicates стояли третьей и
// четвёртой, разрывая читательский блок служебной работой; теперь это один
// раздел «Health» в служебном хвосте.
// Подписи вкладок. Порядок задаёт TAB_IDS в urlstate — там же, где разбор
// адреса решает, какая вкладка существует; Record обязывает не забыть подпись
// при добавлении новой.
const TAB_LABELS: Record<Tab, string> = {
  overview: 'Dashboard',
  archives: 'Archives',
  cheatsheets: 'Cheatsheets',
  analytics: 'Analytics',
  projects: 'Projects',
  now: 'Now',
  team: 'Team',
  finances: 'Finances',
  // Карта стоит в служебном хвосте рядом с Health: обе про то, как устроено
  // хозяйство, а не про то, что в базе лежит.
  health: 'Health',
  architecture: 'Architecture',
  about: 'About',
}

const tabs: { id: Tab; label: string }[] = TAB_IDS.map((id) => ({ id, label: TAB_LABELS[id] }))

export default function App() {
  // Начальное состояние берётся из адреса: ссылка на выборку должна открывать
  // именно её, иначе адрес обещает больше, чем делает.
  const initial = readUrlState(window.location.search)
  const [tab, setTab] = useState<Tab>(initial.tab ?? 'overview')
  // Маска сумм живёт здесь, потому что переключатель стоит в шапке, а
  // применяется она к виду финансов.
  const [masked, setMasked] = useState(true)
  // Поиск тоже поднят сюда: поле стоит в шапке, а фильтрует каталог.
  const [search, setSearch] = useState(initial.q ?? '')
  // Запрос, с которым страницу открыли по ссылке из ответа агента. Читается
  // ОДИН раз при старте и обратно в адрес не пишется: отметка живёт до первой
  // своей правки, иначе перезагрузка через час выдавала бы собственный поиск за
  // ответ агента.
  const linkedQuery = linkedQueryOf(initial)
  // Тег, выбранный в облаке на дашборде. Живёт здесь, потому что выбирают его
  // на одной вкладке, а применяется он на другой.
  const [pickedTag, setPickedTag] = useState('')
  // Категория, выбранная ящиком на About — по той же причине, что и тег:
  // выбирают на одной вкладке, применяется на другой.
  const [pickedCategory, setPickedCategory] = useState('')
  useUrlSync(tab, search)

  const dashboard = useResource(api.dashboard)
  const data = dashboard.status === 'ready' ? dashboard.data : null

  return (
    <div className="min-h-screen bg-bg text-on-surface">
      <Header
        // Счётчик показывает объём работы прямо во вкладке: гигиена — то, куда
        // не заходят по расписанию, и заметить её можно только мимоходом.
        tabs={tabs.map((t) =>
          t.id === 'health' && data ? { ...t, badge: findingCount(data.audits, data.duplicates) } : t,
        )}
        current={tab}
        // Заход на финансы всегда прячет суммы заново: безопасное состояние —
        // то, в котором оказываешься, а не то, которое надо не забыть включить.
        onSelect={(t) => {
          setTab(t)
          // Финансы и состав команды — оба про то, что не показывают через
          // плечо; заход возвращает маску, а не оставляет прошлое решение.
          if (t === 'finances' || t === 'team') setMasked(true)
        }}
        count={data?.stats.total}
        // Поиск показывается только на архиве: он и фильтрует только архив, а
        // поле, которое видно всегда и работает через раз, обещает больше, чем
        // делает.
        extra={
          tab === 'finances' || tab === 'team' ? (
            <PrivacyToggle masked={masked} onChange={setMasked} />
          ) : tab === 'archives' ? (
            <SearchBox value={search} onChange={setSearch} />
          ) : undefined
        }
      />

      <main className="mx-auto max-w-screen-2xl px-4 py-8 sm:px-6 lg:px-8">
        {dashboard.status === 'failed' && <ErrorBox message={dashboard.error} />}
        {dashboard.status === 'loading' && <Spinner />}
        {data && (
          <>
            <UnreadableBanner entries={data.stats.unreadable ?? []} total={data.stats.total} />
            {tab === 'overview' && (
              <DashboardView
                stats={data.stats}
                entries={data.entries}
                // Клик по тегу в облаке переносит в архив вместе с фильтром:
                // облако показывает, ЧТО в базе есть, а читать это всё равно
                // идёшь в архив.
                onPickTag={(t) => {
                  setPickedTag(t)
                  setTab('archives')
                }}
              />
            )}

            {tab === 'analytics' && (
              <AnalyticsView config={data.analyticsConfig} stats={data.stats} entries={data.entries} />
            )}
            {tab === 'health' && (
              <HealthView
                audits={data.audits}
                duplicates={data.duplicates}
                entries={data.entries}
                sources={data.sources}
                // Находка ссылается на запись номером, и открывать её надо в
                // архиве: гигиена показывает, что не так, читают — там же, где
                // читают всё остальное.
                onOpenEntry={(id) => {
                  setSearch(`#${id}`)
                  setPickedTag('')
                  setTab('archives')
                }}
              />
            )}
            {tab === 'archives' && (
              <CatalogView
                entries={data.entries}
                labels={data.stats.category_labels ?? {}}
                tagLabels={data.stats.tag_labels ?? {}}
                pickedTag={pickedTag}
                onPickedTagChange={setPickedTag}
                pickedCategory={pickedCategory}
                onPickedCategoryChange={setPickedCategory}
                health={data.stats.health}
                search={search}
                onSearchChange={setSearch}
                linkedQuery={linkedQuery}
              />
            )}
            {tab === 'cheatsheets' && <CheatsheetsView entries={data.entries} />}
            {tab === 'finances' && <FinancesView finances={data.finances} masked={masked} />}
            {tab === 'projects' && <ProjectsView />}
            {tab === 'team' && <DocumentView load={api.team} name="Team" masked={masked} />}
            {tab === 'now' && <NowView />}
            {tab === 'architecture' && <ArchitectureView />}
            {tab === 'about' && (
              <AboutView
                stats={data.stats}
                // Ящик показывает, сколько записей в категории; читают их всё
                // равно в архиве, поэтому клик ведёт туда с фильтром.
                onPickCategory={(c) => {
                  setPickedCategory(c)
                  setPickedTag('')
                  setSearch('')
                  setTab('archives')
                }}
              />
            )}
          </>
        )}
        <SourceOffer />
      </main>
    </div>
  )
}
