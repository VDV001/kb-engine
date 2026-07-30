import { useState } from 'react'
import { api } from './api'
import { useResource } from './hooks/useResource'
import { AnalyticsView } from './AnalyticsView'
import { CatalogView } from './CatalogView'
import { DocumentView, NowView } from './DocViews'
import { Header } from './components/Header'
import { ErrorBox, Spinner } from './components/ui'
import {
  AuditsView,
  DuplicatesView,
  FinancesView,
  OverviewView,
  SettingsView,
} from './views'

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
  const dashboard = useResource(api.dashboard)
  const data = dashboard.status === 'ready' ? dashboard.data : null

  return (
    <div className="min-h-screen bg-bg text-on-surface">
      <Header tabs={tabs} current={tab} onSelect={setTab} count={data?.stats.total} />

      <main className="mx-auto max-w-screen-2xl px-4 py-8 sm:px-6 lg:px-8">
        {dashboard.status === 'failed' && <ErrorBox message={dashboard.error} />}
        {dashboard.status === 'loading' && <Spinner />}
        {data && (
          <>
            {tab === 'overview' && <OverviewView stats={data.stats} />}

            {tab === 'analytics' && (
              <AnalyticsView config={data.analyticsConfig} stats={data.stats} />
            )}
            {tab === 'audits' && <AuditsView audits={data.audits} />}
            {tab === 'duplicates' && <DuplicatesView groups={data.duplicates} />}
            {tab === 'archives' && <CatalogView entries={data.entries} />}
            {tab === 'finances' && <FinancesView finances={data.finances} />}
            {tab === 'projects' && <DocumentView load={api.projects} name="Projects" />}
            {tab === 'team' && <DocumentView load={api.team} name="Team" />}
            {tab === 'now' && <NowView />}
            {tab === 'settings' && <SettingsView stats={data.stats} />}
          </>
        )}
      </main>
    </div>
  )
}
