import React, { Suspense, lazy } from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes, Link } from 'react-router-dom'
import { IconContext } from '@phosphor-icons/react'
import { queryClient } from '@/lib/api'
import { AppShell, RootBoundary, SessionGate } from '@/app'
import { Loading, Empty } from '@/components/common'
import { Button } from '@/components/ui/button'
import './index.css'

const OverviewPage = lazy(() => import('@/pages/overview'))
const ProvidersPage = lazy(() =>
  import('@/pages/providers').then((m) => ({ default: m.ProvidersPage })),
)
const ProviderEditor = lazy(() =>
  import('@/pages/providers').then((m) => ({ default: m.ProviderEditor })),
)
const SecretsPage = lazy(() => import('@/pages/secrets'))
const SchedulesPage = lazy(() => import('@/pages/schedules'))
const RunsPage = lazy(() => import('@/pages/runs').then((m) => ({ default: m.RunsPage })))
const RunDetailPage = lazy(() => import('@/pages/runs').then((m) => ({ default: m.RunDetailPage })))
const TorrentsPage = lazy(() =>
  import('@/pages/library').then((m) => ({ default: m.TorrentsPage })),
)
const RawListPage = lazy(() => import('@/pages/library').then((m) => ({ default: m.RawListPage })))
const RawDetailPage = lazy(() =>
  import('@/pages/library').then((m) => ({ default: m.RawDetailPage })),
)
const RawPageView = lazy(() => import('@/pages/library').then((m) => ({ default: m.RawPageView })))
const LogsPage = lazy(() => import('@/pages/logs').then((m) => ({ default: m.LogsPage })))
const NotificationsPage = lazy(() =>
  import('@/pages/notifications').then((m) => ({ default: m.NotificationsPage })),
)
const WebhooksPage = lazy(() =>
  import('@/pages/webhooks').then((m) => ({ default: m.WebhooksPage })),
)
const SharingPage = lazy(() => import('@/pages/sharing'))
const RemotesPage = lazy(() => import('@/pages/remotes'))
const HealthPage = lazy(() => import('@/pages/health').then((m) => ({ default: m.HealthPage })))
const BackupsPage = lazy(() => import('@/pages/backups').then((m) => ({ default: m.BackupsPage })))
const SecurityPage = lazy(() =>
  import('@/pages/security').then((m) => ({ default: m.SecurityPage })),
)
const SettingsPage = lazy(() =>
  import('@/pages/settings').then((m) => ({ default: m.SettingsPage })),
)

ReactDOM.createRoot(document.getElementById('root')!).render(
  <RootBoundary>
    {
      <React.StrictMode>
        <IconContext.Provider value={{ weight: 'regular', size: 18 }}>
          <QueryClientProvider client={queryClient}>
            <BrowserRouter>
              <SessionGate>
                <Suspense
                  fallback={
                    <div className="page">
                      <Loading />
                    </div>
                  }
                >
                  <Routes>
                    <Route element={<AppShell />}>
                      <Route index element={<OverviewPage />} />
                      <Route path="health" element={<HealthPage />} />
                      <Route path="providers" element={<ProvidersPage />} />
                      <Route path="providers/new" element={<ProviderEditor key="new" />} />
                      <Route path="providers/:id" element={<ProviderEditor />} />
                      <Route path="remotes" element={<RemotesPage />} />
                      <Route path="sharing" element={<SharingPage />} />
                      <Route path="secrets" element={<SecretsPage />} />
                      <Route path="schedules" element={<SchedulesPage />} />
                      <Route path="runs" element={<RunsPage />} />
                      <Route path="runs/:id" element={<RunDetailPage />} />
                      <Route path="logs" element={<LogsPage />} />
                      <Route path="notifications" element={<NotificationsPage />} />
                      <Route path="webhooks" element={<WebhooksPage />} />
                      <Route path="torrents" element={<TorrentsPage />} />
                      <Route path="raw" element={<RawListPage />} />
                      <Route path="raw/:id" element={<RawDetailPage />} />
                      <Route path="pages/:id" element={<RawPageView />} />
                      <Route path="backups" element={<BackupsPage />} />
                      <Route path="security" element={<SecurityPage />} />
                      <Route path="settings" element={<SettingsPage />} />
                      <Route
                        path="*"
                        element={
                          <div className="page">
                            <Empty
                              title="Page not found"
                              description="This address does not match a console page."
                              action={
                                <Button asChild>
                                  <Link to="/">Back to overview</Link>
                                </Button>
                              }
                            />
                          </div>
                        }
                      />
                    </Route>
                  </Routes>
                </Suspense>
              </SessionGate>
            </BrowserRouter>
          </QueryClientProvider>
        </IconContext.Provider>
      </React.StrictMode>
    }
  </RootBoundary>,
)
