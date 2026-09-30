import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Popover } from 'radix-ui'
import { BellIcon, CheckIcon, XIcon } from '@phosphor-icons/react'
import { api, queryClient } from '@/lib/api'
import { useDate, number } from '@/lib/format'
import type { Notification, NotificationPage } from '@/lib/activity-types'
import { ActivityLinks, LevelBadge } from '@/components/activity-common'
import { Empty, ErrorNotice, Loading } from '@/components/common'
import { Button } from '@/components/ui/button'

export function useNotificationActions() {
  return useMutation({
    mutationFn: (id: number | 'all') =>
      api<void>(id === 'all' ? '/notifications/read-all' : `/notifications/${id}/read`, {
        method: 'POST',
        body: '{}',
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
  })
}

export function NotificationItems({
  items,
  markRead,
  busy,
}: {
  items: Notification[]
  markRead: (id: number) => void
  busy: boolean
}) {
  const date = useDate()
  return (
    <ul className="divide-y divide-border">
      {items.map((item) => (
        <li key={item.id} className={`space-y-3 p-4 ${!item.read_at ? 'bg-primary/5' : ''}`}>
          <div className="flex flex-wrap items-center gap-2">
            <LevelBadge level={item.level} />
            <span
              className={`text-xs ${item.read_at ? 'text-muted-foreground' : 'font-medium text-primary'}`}
            >
              {item.read_at ? 'Read' : 'Unread'}
            </span>
            <time
              className="ml-auto text-xs text-muted-foreground"
              dateTime={item.created_at}
              title={item.created_at}
            >
              {date(item.created_at)}
            </time>
          </div>
          <div className="min-w-0">
            <h3 className="break-words font-medium">{item.title}</h3>
            <p className="mt-1 break-words text-sm leading-6 text-muted-foreground">
              {item.message}
            </p>
            <p className="mono mt-2 text-muted-foreground">{item.kind}</p>
          </div>
          <ActivityLinks {...item} />
          {!item.read_at ? (
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => markRead(item.id)}>
              <CheckIcon />
              Mark read<span className="sr-only">: {item.title}</span>
            </Button>
          ) : (
            <p className="text-xs text-muted-foreground">Read {date(item.read_at)}</p>
          )}
        </li>
      ))}
    </ul>
  )
}

export function NotificationCenter() {
  const [open, setOpen] = useState(false)
  const [unread, setUnread] = useState(true)
  const count = useQuery({
    queryKey: ['notifications', 'count'],
    queryFn: ({ signal }) => api<NotificationPage>('/notifications?limit=1&offset=0', { signal }),
  })
  const notifications = useQuery({
    queryKey: ['notifications', 'center', unread],
    queryFn: ({ signal }) =>
      api<NotificationPage>(`/notifications?limit=5&offset=0${unread ? '&unread=true' : ''}`, {
        signal,
      }),
    enabled: open,
  })
  const read = useNotificationActions()
  const unreadCount = count.data?.unread_count
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="relative"
          aria-label={
            count.isError
              ? 'Notifications: unread count unavailable'
              : unreadCount === undefined
                ? 'Notifications'
                : `Notifications: ${number(unreadCount)} unread`
          }
        >
          <BellIcon />
          {unreadCount !== undefined && unreadCount > 0 && (
            <span
              aria-hidden="true"
              className="absolute -top-1 -right-1 min-w-4 rounded-full bg-primary px-1 text-[10px] font-semibold leading-4 text-primary-foreground"
            >
              {unreadCount > 99 ? '99+' : unreadCount}
            </span>
          )}
          {count.isError && (
            <span aria-hidden="true" className="absolute -top-1 -right-1 text-xs text-destructive">
              !
            </span>
          )}
        </Button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          align="end"
          sideOffset={10}
          collisionPadding={12}
          aria-label="Notifications"
          className="z-50 w-[min(26rem,calc(100vw-1.5rem))] overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-lg outline-none"
        >
          <div className="space-y-3 border-b border-border p-4">
            <div className="flex items-center justify-between gap-3">
              <h2 className="font-semibold">
                Notifications
                {unreadCount !== undefined && (
                  <span className="ml-2 text-xs font-normal text-muted-foreground">
                    {number(unreadCount)} unread
                  </span>
                )}
              </h2>
              <Popover.Close asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Close notifications">
                  <XIcon />
                </Button>
              </Popover.Close>
            </div>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex gap-1" aria-label="Notification filter">
                <Button
                  size="sm"
                  variant={unread ? 'secondary' : 'ghost'}
                  aria-pressed={unread}
                  onClick={() => setUnread(true)}
                >
                  Unread
                </Button>
                <Button
                  size="sm"
                  variant={!unread ? 'secondary' : 'ghost'}
                  aria-pressed={!unread}
                  onClick={() => setUnread(false)}
                >
                  All
                </Button>
              </div>
              <Button
                size="sm"
                variant="ghost"
                disabled={read.isPending || !unreadCount}
                onClick={() => read.mutate('all')}
              >
                Mark all read
              </Button>
            </div>
          </div>
          <div className="max-h-[min(60dvh,32rem)] overflow-y-auto">
            {(count.error || notifications.error || read.error) && (
              <div className="space-y-2 p-3">
                <ErrorNotice error={count.error} retry={() => void count.refetch()} />
                <ErrorNotice
                  error={notifications.error}
                  retry={() => void notifications.refetch()}
                />
                <ErrorNotice error={read.error} />
              </div>
            )}
            {notifications.isPending ? (
              <div className="p-4">
                <Loading label="Loading notifications" />
              </div>
            ) : (
              notifications.data &&
              (notifications.data.items?.length ? (
                <NotificationItems
                  items={notifications.data.items}
                  markRead={(id) => read.mutate(id)}
                  busy={read.isPending}
                />
              ) : (
                <div className="p-4">
                  <Empty
                    title={unread ? 'All caught up' : 'No notifications yet'}
                    description={
                      unread
                        ? 'There are no unread notifications.'
                        : 'Run outcomes and scheduling failures appear here.'
                    }
                  />
                </div>
              ))
            )}
          </div>
          <div className="border-t border-border p-3">
            <Button asChild variant="outline" className="w-full">
              <Link to="/notifications" onClick={() => setOpen(false)}>
                View all notifications
              </Link>
            </Button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}
