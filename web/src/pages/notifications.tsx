import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { ArrowClockwiseIcon, CheckIcon } from '@phosphor-icons/react'
import { api, params } from '@/lib/api'
import { number } from '@/lib/format'
import { normalizePageOffset, usePageSize } from '@/lib/display-preferences'
import type { NotificationPage } from '@/lib/activity-types'
import { NotificationItems, useNotificationActions } from '@/components/notification-center'
import { Empty, ErrorNotice, Loading, PageHeader, Pager } from '@/components/common'
import { Button } from '@/components/ui/button'

export function NotificationsPage() {
  const [search, setSearch] = useSearchParams()
  const unread = search.get('unread') === 'true'
  const limit = usePageSize()
  const offset = normalizePageOffset(search.get('offset'), limit)
  const query = params({ unread: unread ? 'true' : undefined, limit, offset })
  const notifications = useQuery({
    queryKey: ['notifications', 'page', query],
    queryFn: ({ signal }) => api<NotificationPage>(`/notifications?${query}`, { signal }),
  })
  const read = useNotificationActions()
  function filter(value: boolean) {
    const next = new URLSearchParams(search)
    value ? next.set('unread', 'true') : next.delete('unread')
    next.delete('offset')
    setSearch(next)
  }
  return (
    <div className="page">
      <PageHeader
        title="Notifications"
        description="Run outcomes and scheduling failures, saved across sessions. Reading an item here also updates the notification bell."
        actions={
          <>
            <Button
              variant="outline"
              disabled={notifications.isFetching}
              onClick={() => void notifications.refetch()}
            >
              <ArrowClockwiseIcon />
              Refresh
            </Button>
            <Button
              variant="outline"
              disabled={read.isPending || !notifications.data?.unread_count}
              onClick={() => read.mutate('all')}
            >
              <CheckIcon />
              Mark all read
            </Button>
          </>
        }
      />
      <div className="toolbar">
        <div className="flex gap-2" aria-label="Notification filter">
          <Button
            variant={!unread ? 'secondary' : 'outline'}
            aria-pressed={!unread}
            onClick={() => filter(false)}
          >
            All notifications
          </Button>
          <Button
            variant={unread ? 'secondary' : 'outline'}
            aria-pressed={unread}
            onClick={() => filter(true)}
          >
            Unread
          </Button>
        </div>
        {notifications.data && (
          <p className="text-sm text-muted-foreground" role="status">
            {number(notifications.data.unread_count)} unread
          </p>
        )}
      </div>
      <ErrorNotice error={notifications.error} retry={() => void notifications.refetch()} />
      <ErrorNotice error={read.error} />
      {notifications.isPending ? (
        <Loading label="Loading notifications" />
      ) : (
        notifications.data && (
          <>
            {notifications.data.items?.length ? (
              <div className="table-frame">
                <NotificationItems
                  items={notifications.data.items}
                  markRead={(id) => read.mutate(id)}
                  busy={read.isPending}
                />
              </div>
            ) : (
              <Empty
                title={
                  unread ? 'No unread notifications in this view' : 'No notifications in this view'
                }
                description={
                  offset > 0
                    ? 'Return to the previous page to see earlier results.'
                    : unread
                      ? 'You are caught up. Switch to all notifications to review past activity.'
                      : 'Completed, failed, paused and cancelled runs, along with scheduling failures, appear here.'
                }
              />
            )}
            <Pager
              total={notifications.data.total}
              limit={notifications.data.limit}
              offset={notifications.data.offset}
              busy={notifications.isFetching || read.isPending}
              onChange={(value) => {
                const next = new URLSearchParams(search)
                next.set('offset', String(value))
                setSearch(next)
              }}
            />
          </>
        )
      )}
    </div>
  )
}
