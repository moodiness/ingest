import {
  Component,
  useEffect,
  useId,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
} from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, Outlet, useLocation } from 'react-router-dom'
import {
  DatabaseIcon,
  SquaresFourIcon,
  PlugsConnectedIcon,
  KeyIcon,
  ClockCounterClockwiseIcon,
  CalendarIcon,
  StackIcon,
  SignOutIcon,
  ListIcon,
  XIcon,
  ArrowClockwiseIcon,
  BellIcon,
  TerminalWindowIcon,
  WebhooksLogoIcon,
  ShareNetworkIcon,
  GlobeIcon,
  HeartbeatIcon,
  HardDrivesIcon,
  ShieldCheckIcon,
  CaretDownIcon,
  DownloadSimpleIcon,
  FolderSimpleIcon,
  ChartLineIcon,
  GearSixIcon,
} from '@phosphor-icons/react'
import { api, queryClient, refreshData, setCSRF } from '@/lib/api'
import type { Session } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { ErrorNotice, Loading } from '@/components/common'
import { NotificationCenter } from '@/components/notification-center'
import { Login } from '@/components/login'

export class RootBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  render() {
    if (this.state.error)
      return (
        <main className="mx-auto max-w-xl space-y-5 px-6 py-24">
          <h1 className="page-title">The console could not be displayed</h1>
          <p className="page-description">
            Reload the application. Data already saved on the server is not affected.
          </p>
          <ErrorNotice error={this.state.error} />
          <Button onClick={() => window.location.reload()}>
            <ArrowClockwiseIcon />
            Reload application
          </Button>
        </main>
      )
    return this.props.children
  }
}
const overview = { to: '/', label: 'Overview', icon: SquaresFourIcon }
const navigationGroups = [
  {
    id: 'collection',
    label: 'Collection',
    icon: DownloadSimpleIcon,
    defaultOpen: true,
    items: [
      { to: '/providers', label: 'Sources', icon: PlugsConnectedIcon },
      { to: '/remotes', label: 'Remote catalogs', icon: GlobeIcon },
      { to: '/schedules', label: 'Schedules', icon: CalendarIcon },
      { to: '/runs', label: 'Runs', icon: ClockCounterClockwiseIcon },
    ],
  },
  {
    id: 'library',
    label: 'Library',
    icon: FolderSimpleIcon,
    defaultOpen: true,
    items: [
      { to: '/torrents', label: 'Torrents', icon: StackIcon },
      { to: '/raw', label: 'Observations', icon: DatabaseIcon },
    ],
  },
  {
    id: 'monitoring',
    label: 'Monitoring',
    icon: ChartLineIcon,
    defaultOpen: true,
    items: [
      { to: '/health', label: 'System health', icon: HeartbeatIcon },
      { to: '/logs', label: 'Logs', icon: TerminalWindowIcon },
      { to: '/notifications', label: 'Notifications', icon: BellIcon },
    ],
  },
  {
    id: 'administration',
    label: 'Administration',
    icon: GearSixIcon,
    defaultOpen: false,
    items: [
      { to: '/settings', label: 'Settings', icon: GearSixIcon },
      { to: '/sharing', label: 'Sharing', icon: ShareNetworkIcon },
      { to: '/webhooks', label: 'Webhooks', icon: WebhooksLogoIcon },
      { to: '/backups', label: 'Backups', icon: HardDrivesIcon },
      { to: '/secrets', label: 'Secrets', icon: KeyIcon },
      { to: '/security', label: 'Security', icon: ShieldCheckIcon },
    ],
  },
]
const navigation = [overview, ...navigationGroups.flatMap((group) => group.items)]
const navigationStorageKey = 'ingest.navigation.groups'
const navigationLinkClassName =
  'flex min-h-11 items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground aria-[current=page]:bg-primary/10 aria-[current=page]:font-medium aria-[current=page]:text-primary lg:min-h-9'

function AppNavigation({
  activeItem,
  activeGroupId,
  openGroups,
  onToggle,
  onNavigate,
}: {
  activeItem: (typeof navigation)[number] | undefined
  activeGroupId: string | undefined
  openGroups: Record<string, boolean>
  onToggle: (groupId: string) => void
  onNavigate?: () => void
}) {
  const id = useId()
  const navRef = useRef<HTMLElement>(null)
  const activeGroupOpen = activeGroupId ? openGroups[activeGroupId] : true
  useEffect(() => {
    navRef.current?.querySelector('[aria-current="page"]')?.scrollIntoView({ block: 'nearest' })
  }, [activeItem, activeGroupOpen])
  const handleNavigate = (event: MouseEvent<HTMLAnchorElement>) => {
    if (
      !event.defaultPrevented &&
      event.button === 0 &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.shiftKey &&
      !event.altKey
    ) {
      onNavigate?.()
    }
  }
  return (
    <nav
      ref={navRef}
      aria-label="Main navigation"
      className="min-h-0 flex-1 space-y-1 overflow-y-auto overscroll-contain px-3 py-4 [scrollbar-width:thin]"
    >
      <Link
        to={overview.to}
        aria-current={activeItem === overview ? 'page' : undefined}
        className={navigationLinkClassName}
        onClick={handleNavigate}
      >
        <overview.icon size={19} aria-hidden="true" className="shrink-0" />
        {overview.label}
      </Link>
      {navigationGroups.map((group) => (
        <section key={group.id} className={`space-y-1 ${openGroups[group.id] ? 'pb-2' : ''}`}>
          <h2>
            <button
              type="button"
              aria-expanded={openGroups[group.id]}
              aria-controls={`${id}-${group.id}`}
              onClick={() => onToggle(group.id)}
              className="flex min-h-11 w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground aria-expanded:text-foreground lg:min-h-9"
            >
              <group.icon size={19} aria-hidden="true" className="shrink-0" />
              <span className="min-w-0 flex-1">{group.label}</span>
              <CaretDownIcon
                aria-hidden="true"
                className={`size-3.5 shrink-0 transition-transform motion-reduce:transition-none ${openGroups[group.id] ? '' : '-rotate-90'}`}
              />
            </button>
          </h2>
          <ul
            id={`${id}-${group.id}`}
            hidden={!openGroups[group.id]}
            className="ml-5 space-y-0.5 border-l border-border pl-2"
          >
            {group.items.map((item) => (
              <li key={item.to}>
                <Link
                  to={item.to}
                  aria-current={activeItem === item ? 'page' : undefined}
                  className={navigationLinkClassName}
                  onClick={handleNavigate}
                >
                  <item.icon size={19} aria-hidden="true" className="shrink-0" />
                  {item.label}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </nav>
  )
}

export function SessionGate({ children }: { children: ReactNode }) {
  const session = useQuery({
    queryKey: ['session'],
    queryFn: async ({ signal }) => {
      const value = await api<Session>('/session', { signal })
      setCSRF(value.csrf_token)
      return value
    },
    staleTime: 60_000,
    refetchInterval: 300_000,
    retry: false,
  })
  useEffect(() => {
    async function expired() {
      await queryClient.cancelQueries()
      queryClient.setQueryData<Session>(['session'], { authenticated: false })
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== 'session' })
      queryClient.getMutationCache().clear()
    }
    window.addEventListener('session-expired', expired)
    return () => window.removeEventListener('session-expired', expired)
  }, [])
  if (session.isPending)
    return (
      <div className="mx-auto max-w-lg px-6 py-24">
        <Loading label="Checking session" />
      </div>
    )
  if (!session.data)
    return (
      <div className="mx-auto max-w-lg space-y-6 px-6 py-24">
        <h1 className="page-title">Connect to the server</h1>
        <ErrorNotice error={session.error} retry={() => void session.refetch()} />
      </div>
    )
  if (!session.data.authenticated) return <Login />
  return (
    <>
      <div className="fixed right-4 bottom-4 z-40 max-w-md">
        <ErrorNotice error={session.error} retry={() => void session.refetch()} />
      </div>
      {children}
    </>
  )
}

function useLiveUpdates() {
  const [state, setState] = useState<'connecting' | 'live' | 'reconnecting'>('connecting')
  useEffect(() => {
    const source = new EventSource('/api/events', { withCredentials: true })
    let timer: number | undefined
    let connected = false
    const queueRefresh = () => {
      if (!timer)
        timer = window.setTimeout(() => {
          timer = undefined
          void refreshData()
        }, 500)
    }
    source.onopen = () => {
      connected = true
      setState('live')
      queueRefresh()
    }
    source.addEventListener('update', queueRefresh)
    source.onerror = () => {
      connected = false
      setState('reconnecting')
      void queryClient.invalidateQueries({ queryKey: ['session'] })
    }
    const fallback = setInterval(() => {
      if (!connected) queueRefresh()
    }, 30_000)
    return () => {
      source.close()
      clearInterval(fallback)
      clearTimeout(timer)
    }
  }, [])
  return state
}

export function AppShell() {
  const live = useLiveUpdates()
  const location = useLocation()
  const navigationPath = location.pathname.startsWith('/pages/') ? '/raw' : location.pathname
  const activeItem = navigation.find(
    (item) =>
      navigationPath === item.to || (item.to !== '/' && navigationPath.startsWith(`${item.to}/`)),
  )
  const activeGroup = navigationGroups.find((group) =>
    group.items.some((item) => item === activeItem),
  )
  const [openGroups, setOpenGroups] = useState<Record<string, boolean>>(() => {
    let stored: unknown
    try {
      stored = JSON.parse(localStorage.getItem(navigationStorageKey) ?? 'null')
    } catch {
      // Browser preferences must not prevent access to the console.
    }
    return Object.fromEntries(
      navigationGroups.map((group) => {
        const preference =
          stored !== null && typeof stored === 'object'
            ? (stored as Record<string, unknown>)[group.id]
            : undefined
        return [
          group.id,
          group.id === activeGroup?.id ||
            (typeof preference === 'boolean' ? preference : group.defaultOpen),
        ]
      }),
    )
  })
  const [mobileOpen, setMobileOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const mainRef = useRef<HTMLElement>(null)
  const previousPath = useRef(location.pathname)
  const focusMainOnClose = useRef(false)
  useEffect(() => {
    setMobileOpen(false)
    if (activeGroup) {
      setOpenGroups((current) =>
        current[activeGroup.id] ? current : { ...current, [activeGroup.id]: true },
      )
    }
    if (previousPath.current !== location.pathname) {
      focusMainOnClose.current = true
      mainRef.current?.focus()
      previousPath.current = location.pathname
      window.scrollTo({ top: 0 })
    }
  }, [location.pathname, activeGroup])
  useEffect(() => {
    try {
      localStorage.setItem(navigationStorageKey, JSON.stringify(openGroups))
    } catch {
      // Keep the current session usable when browser storage is unavailable.
    }
  }, [openGroups])
  useEffect(() => {
    const desktop = window.matchMedia('(min-width: 64rem)')
    const closeOnDesktop = (event: MediaQueryListEvent) => {
      if (event.matches) {
        focusMainOnClose.current = true
        setMobileOpen(false)
      }
    }
    desktop.addEventListener('change', closeOnDesktop)
    return () => desktop.removeEventListener('change', closeOnDesktop)
  }, [])
  const logout = useMutation({
    mutationFn: () => api<void>('/logout', { method: 'POST' }),
    onSuccess: () => {
      setCSRF()
      window.dispatchEvent(new Event('session-expired'))
    },
  })
  const navigationProps = {
    activeItem,
    activeGroupId: activeGroup?.id,
    openGroups,
    onToggle: (groupId: string) =>
      setOpenGroups((current) => ({ ...current, [groupId]: !current[groupId] })),
  }
  const branding = (
    <>
      <DatabaseIcon size={32} aria-hidden="true" className="shrink-0 text-primary" />
      <span className="text-2xl leading-none font-semibold tracking-tight">Ingest</span>
    </>
  )
  const footer = (
    <div className="shrink-0 space-y-3 border-t border-border p-3">
      <ErrorNotice error={logout.error} />
      <Button
        variant="destructive"
        className="min-h-11 w-full justify-center lg:min-h-9"
        disabled={logout.isPending}
        onClick={() => logout.mutate()}
      >
        <SignOutIcon />
        {logout.isPending ? 'Signing out…' : 'Sign out'}
      </Button>
    </div>
  )
  return (
    <Dialog
      open={mobileOpen}
      onOpenChange={(open) => {
        if (open) focusMainOnClose.current = false
        setMobileOpen(open)
      }}
    >
      <div className="min-h-dvh">
        <a
          href="#main-content"
          className="fixed left-4 top-3 z-50 -translate-y-24 rounded-md bg-primary px-4 py-2 text-primary-foreground focus:translate-y-0"
        >
          Skip to content
        </a>
        <aside className="fixed inset-y-0 left-0 z-30 hidden w-60 flex-col overflow-hidden border-r border-border bg-[#141719] lg:flex">
          <div className="flex h-20 shrink-0 items-center gap-3 px-6">{branding}</div>
          <Separator />
          <AppNavigation {...navigationProps} />
          {footer}
        </aside>
        <div className="lg:pl-60">
          <header className="sticky top-0 z-20 flex min-h-16 items-center justify-between gap-3 border-b border-border bg-background/95 px-5 backdrop-blur-sm md:px-9">
            <div className="flex items-center gap-3">
              <DialogTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-11 lg:hidden"
                  aria-label="Open navigation"
                  aria-controls="mobile-navigation"
                >
                  <ListIcon />
                </Button>
              </DialogTrigger>
              <span className="text-xs text-muted-foreground">
                Administration<span className="mx-3 text-border">/</span>
                <span className="text-foreground">{activeItem?.label ?? 'Administration'}</span>
              </span>
            </div>
            <div className="flex items-center gap-3">
              <span
                className="hidden items-center gap-2 text-xs text-muted-foreground sm:flex"
                role="status"
              >
                <span
                  className={`size-1.5 rounded-full ${live === 'live' ? 'bg-primary' : 'bg-muted-foreground'}`}
                />
                {live === 'live'
                  ? 'Live updates'
                  : live === 'connecting'
                    ? 'Connecting to updates'
                    : 'Reconnecting · refreshing every 30 s'}
              </span>
              <NotificationCenter />
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Refresh data"
                disabled={refreshing}
                onClick={async () => {
                  setRefreshing(true)
                  try {
                    await refreshData()
                  } finally {
                    setRefreshing(false)
                  }
                }}
              >
                <ArrowClockwiseIcon />
              </Button>
            </div>
          </header>
          <main id="main-content" ref={mainRef} tabIndex={-1} className="outline-none">
            <Outlet />
          </main>
        </div>
        <DialogContent
          id="mobile-navigation"
          aria-describedby={undefined}
          showCloseButton={false}
          className="top-0 left-0 flex h-dvh max-h-dvh w-80 max-w-[calc(100%-2rem)] translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none border-y-0 border-l-0 bg-[#141719] p-0 data-[state=closed]:animate-none data-[state=open]:animate-none sm:max-w-80"
          onCloseAutoFocus={(event) => {
            if (focusMainOnClose.current) {
              event.preventDefault()
              focusMainOnClose.current = false
              mainRef.current?.focus()
            }
          }}
        >
          <DialogTitle className="sr-only">Main navigation</DialogTitle>
          <div className="flex h-20 shrink-0 items-center gap-3 border-b border-border px-5">
            {branding}
            <DialogClose asChild>
              <Button
                variant="ghost"
                size="icon"
                className="ml-auto size-11 shrink-0"
                aria-label="Close navigation"
              >
                <XIcon />
              </Button>
            </DialogClose>
          </div>
          <AppNavigation
            {...navigationProps}
            onNavigate={() => {
              focusMainOnClose.current = true
              setMobileOpen(false)
            }}
          />
          {footer}
        </DialogContent>
      </div>
    </Dialog>
  )
}
