import { useState } from 'react'
import { api } from './api'
import { useResource } from './hooks/useResource'
import { AnalyticsView } from './AnalyticsView'
import { CatalogView } from './CatalogView'
import { DocumentView, NowView } from './DocViews'
import { Header } from './components/Header'
import { ErrorBox, Spinner } from './components/ui'
import { FinancesView } from './FinancesView'
import { PrivacyToggle } from './components/PrivacyToggle'
import { SearchBox } from './components/SearchBox'
import { SourceOffer } from './components/SourceOffer'
import { DashboardView } from './DashboardView'
import { HealthView } from './HealthView'
import { findingCount } from './hygiene'
import { ProjectsView } from './ProjectsView'
import { AboutView } from './AboutView'

type Tab = 'overview' | 'archives' | 'analytics' | 'health' | 'finances' | 'projects' | 'team' | 'now' | 'about'

// Порядок вкладок — это четыре группы, а не список: база знаний, витрина и
// оперативка, приватное, служебное. Audits и Duplicates стояли третьей и
// четвёртой, разрывая читательский блок служебной работой; теперь это один
// раздел «Health» в служебном хвосте.
const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Dashboard' },
  { id: 'archives', label: 'Archives' },
  { id: 'analytics', label: 'Analytics' },
  { id: 'projects', label: 'Projects' },
  { id: 'now', label: 'Now' },
  { id: 'team', label: 'Team' },
  { id: 'finances', label: 'Finances' },
  { id: 'health', label: 'Health' },
  { id: 'about', label: 'About' },
]

export default function App() {
  const [tab, setTab] = useState<Tab>('overview')
  // Маска сумм живёт здесь, потому что переключатель стоит в шапке, а
  // применяется она к виду финансов.
  const [masked, setMasked] = useState(true)
  // Поиск тоже поднят сюда: поле стоит в шапке, а фильтрует каталог.
  const [search, setSearch] = useState('')
  // Тег, выбранный в облаке на дашборде. Живёт здесь, потому что выбирают его
  // на одной вкладке, а применяется он на другой.
  const [pickedTag, setPickedTag] = useState('')
  // Категория, выбранная ящиком на About — по той же причине, что и тег:
  // выбирают на одной вкладке, применяется на другой.
  const [pickedCategory, setPickedCategory] = useState('')
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
              />
            )}
            {tab === 'finances' && <FinancesView finances={data.finances} masked={masked} />}
            {tab === 'projects' && <ProjectsView />}
            {tab === 'team' && <DocumentView load={api.team} name="Team" masked={masked} />}
            {tab === 'now' && <NowView />}
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
