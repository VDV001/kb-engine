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
import { DashboardView } from './DashboardView'
import { ProjectsView } from './ProjectsView'
import { AuditsView, DuplicatesView, SettingsView } from './views'

type Tab = 'overview' | 'archives' | 'analytics' | 'audits' | 'duplicates' | 'finances' | 'projects' | 'team' | 'now' | 'settings'

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Dashboard' },
  { id: 'archives', label: 'Archives' },
  { id: 'analytics', label: 'Analytics' },
  { id: 'audits', label: 'Audits' },
  { id: 'duplicates', label: 'Duplicates' },
  { id: 'finances', label: 'Finances' },
  { id: 'projects', label: 'Projects' },
  { id: 'team', label: 'Team' },
  { id: 'now', label: 'Now' },
  { id: 'settings', label: 'Summary' },
]

export default function App() {
  const [tab, setTab] = useState<Tab>('overview')
  // Маска сумм живёт здесь, потому что переключатель стоит в шапке, а
  // применяется она к виду финансов.
  const [masked, setMasked] = useState(true)
  // Поиск тоже поднят сюда: поле стоит в шапке, а фильтрует каталог.
  const [search, setSearch] = useState('')
  const dashboard = useResource(api.dashboard)
  const data = dashboard.status === 'ready' ? dashboard.data : null

  return (
    <div className="min-h-screen bg-bg text-on-surface">
      <Header
        tabs={tabs}
        current={tab}
        // Заход на финансы всегда прячет суммы заново: безопасное состояние —
        // то, в котором оказываешься, а не то, которое надо не забыть включить.
        onSelect={(t) => {
          setTab(t)
          if (t === 'finances') setMasked(true)
        }}
        count={data?.stats.total}
        // Поиск показывается только на архиве: он и фильтрует только архив, а
        // поле, которое видно всегда и работает через раз, обещает больше, чем
        // делает.
        extra={
          tab === 'finances' ? (
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
            {tab === 'overview' && <DashboardView stats={data.stats} entries={data.entries} />}

            {tab === 'analytics' && (
              <AnalyticsView config={data.analyticsConfig} stats={data.stats} />
            )}
            {tab === 'audits' && <AuditsView audits={data.audits} />}
            {tab === 'duplicates' && <DuplicatesView groups={data.duplicates} />}
            {tab === 'archives' && (
              <CatalogView
                entries={data.entries}
                labels={data.stats.category_labels ?? {}}
                health={data.stats.health}
                search={search}
                onSearchChange={setSearch}
              />
            )}
            {tab === 'finances' && <FinancesView finances={data.finances} masked={masked} />}
            {tab === 'projects' && <ProjectsView />}
            {tab === 'team' && <DocumentView load={api.team} name="Team" />}
            {tab === 'now' && <NowView />}
            {tab === 'settings' && <SettingsView stats={data.stats} />}
          </>
        )}
      </main>
    </div>
  )
}
