import { useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { PlusIcon, KeyIcon, ArrowClockwiseIcon, TrashIcon } from '@phosphor-icons/react'
import { api, ApiError, queryClient } from '@/lib/api'
import { useDate } from '@/lib/format'
import type { Items, SecretInfo } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from '@/components/ui/table'
import { ConfirmAction, Empty, ErrorNotice, Loading, PageHeader } from '@/components/common'

export default function SecretsPage() {
  const date = useDate()
  const returnFocus = useRef<HTMLElement | null>(null)
  const secrets = useQuery({
    queryKey: ['secrets'],
    queryFn: ({ signal }) => api<Items<SecretInfo>>('/secrets', { signal }),
  })
  const [editing, setEditing] = useState<{ name: string; rotate: boolean } | null>(null)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [notice, setNotice] = useState('')
  const save = useMutation({
    mutationFn: async () => {
      if (!editing?.rotate && secrets.data?.items?.some((secret) => secret.name === name))
        throw new ApiError(409, 'This name already exists. Use rotation to replace its value.')
      await api<void>(`/secrets/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body: JSON.stringify({ value }),
      })
    },
    onSuccess: async () => {
      setNotice(
        editing?.rotate ? `The value of “${name}” was replaced.` : `Secret “${name}” was created.`,
      )
      setValue('')
      setEditing(null)
      await queryClient.invalidateQueries({ queryKey: ['secrets'] })
    },
  })
  function openSecret(secretName = '') {
    returnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null
    setName(secretName)
    setValue('')
    save.reset()
    setEditing({ name: secretName, rotate: Boolean(secretName) })
  }
  return (
    <div className="page">
      <PageHeader
        title="Secrets"
        description="Values remain encrypted on the server. Only names and update dates are visible."
        actions={
          <Button onClick={() => openSecret()}>
            <PlusIcon />
            Create secret
          </Button>
        }
      />
      {notice && (
        <p className="notice text-primary" role="status">
          {notice}
        </p>
      )}
      <div className="notice flex items-start gap-3">
        <KeyIcon className="mt-1 shrink-0 text-primary" />
        <p>
          Reference these names in <code className="mono">auth.secret_ref</code>,{' '}
          <code className="mono">auth.username_ref</code>,{' '}
          <code className="mono">auth.password_ref</code> or{' '}
          <code className="mono">http.secret_headers</code>. Do not put their values in JSON files.
        </p>
      </div>
      <ErrorNotice error={secrets.error} retry={() => void secrets.refetch()} />
      {secrets.isPending ? (
        <Loading />
      ) : secrets.data?.items?.length ? (
        <div className="table-frame">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Reference</TableHead>
                <TableHead>Last updated</TableHead>
                <TableHead>Value</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {secrets.data.items.map((secret) => (
                <TableRow key={secret.name}>
                  <TableCell className="font-mono text-xs">{secret.name}</TableCell>
                  <TableCell className="text-muted-foreground">{date(secret.updated_at)}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    Encrypted · not viewable
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-2">
                      <Button variant="outline" size="sm" onClick={() => openSecret(secret.name)}>
                        <ArrowClockwiseIcon />
                        Replace
                      </Button>
                      <ConfirmAction
                        title={`Delete secret “${secret.name}”?`}
                        description="The encrypted value will be permanently deleted. The server refuses deletion while a source or resumable run still references it."
                        label="Delete secret"
                        destructive
                        trigger={
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Delete secret ${secret.name}`}
                          >
                            <TrashIcon />
                          </Button>
                        }
                        action={async () => {
                          await api<void>(`/secrets/${encodeURIComponent(secret.name)}`, {
                            method: 'DELETE',
                          })
                          setNotice(`Secret “${secret.name}” was deleted.`)
                          await queryClient.invalidateQueries({ queryKey: ['secrets'] })
                        }}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : (
        !secrets.isError && (
          <Empty
            title="The vault contains no secrets"
            description="Add the keys, passwords or cookies your sources need. The server never returns their values."
            action={
              <Button onClick={() => openSecret()}>
                <PlusIcon />
                Create secret
              </Button>
            }
          />
        )
      )}
      <Dialog
        open={Boolean(editing)}
        onOpenChange={(open) => {
          if (!open && !save.isPending) {
            setEditing(null)
            setValue('')
            save.reset()
          }
        }}
      >
        <DialogContent
          showCloseButton={!save.isPending}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            returnFocus.current?.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>{editing?.rotate ? 'Replace secret value' : 'Create secret'}</DialogTitle>
            <DialogDescription>
              {editing?.rotate
                ? `The current value of “${editing.name}” will be replaced. It cannot be retrieved from this console.`
                : 'The value is sent to the server once, then encrypted. It is not recorded in the browser or JSON definitions.'}
            </DialogDescription>
          </DialogHeader>
          <form
            className="space-y-5"
            autoComplete="off"
            onSubmit={(event) => {
              event.preventDefault()
              save.mutate()
            }}
          >
            <div className="field">
              <Label htmlFor="secret-name">Reference name</Label>
              <Input
                id="secret-name"
                required
                pattern="[A-Za-z0-9][A-Za-z0-9_-]*"
                maxLength={128}
                value={name}
                readOnly={editing?.rotate}
                disabled={save.isPending}
                onChange={(event) => setName(event.target.value)}
                spellCheck={false}
                autoComplete="off"
              />
              <p className="help">
                Letters, digits, hyphens and underscores. Sources use this name as a reference.
              </p>
            </div>
            <div className="field">
              <Label htmlFor="secret-value">{editing?.rotate ? 'New value' : 'Secret value'}</Label>
              <Input
                id="secret-value"
                type="password"
                required
                value={value}
                disabled={save.isPending}
                onChange={(event) => setValue(event.target.value)}
                autoComplete="new-password"
                spellCheck={false}
              />
              <p className="help">The value is never returned after saving.</p>
            </div>
            <ErrorNotice error={save.error} />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={save.isPending}
                onClick={() => {
                  setEditing(null)
                  setValue('')
                  save.reset()
                }}
              >
                Back
              </Button>
              <Button type="submit" disabled={save.isPending || !value || !name}>
                {save.isPending
                  ? 'Saving…'
                  : editing?.rotate
                    ? 'Confirm replacement'
                    : 'Create secret'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
