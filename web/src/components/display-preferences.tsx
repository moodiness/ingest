import { useState } from 'react'
import {
  updateDisplayPreferences,
  useDisplayPreferences,
  validTimezone,
} from '@/lib/display-preferences'
import type { DisplayPreferences } from '@/lib/display-preferences'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export function DisplayPreferencesPanel() {
  const preferences = useDisplayPreferences()
  const [timezoneDraft, setTimezoneDraft] = useState<string | null>(null)
  const [message, setMessage] = useState('')
  const timezone = timezoneDraft ?? preferences.timezone
  const valid = validTimezone(timezone.trim())
  const browserTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone
  function save(patch: Partial<DisplayPreferences>) {
    const persisted = updateDisplayPreferences(patch)
    setMessage(
      persisted
        ? 'Display preferences saved in this browser.'
        : 'Applied for this tab, but browser storage is unavailable. Preferences may reset when you reload.',
    )
  }
  return (
    <section
      className="space-y-5 rounded-md border border-border bg-card/30 p-5 sm:p-6"
      aria-labelledby="display-preferences-heading"
    >
      <div>
        <h2 id="display-preferences-heading" className="font-medium">
          Personal display preferences
        </h2>
        <p className="mt-1 max-w-2xl text-sm leading-6 text-muted-foreground">
          Saved only in this browser and shared with its other tabs. These preferences do not change
          server settings, stored UTC timestamps, source page sizes or collection behavior.
        </p>
      </div>
      <form
        className="max-w-xl space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          if (!valid) return
          save({ timezone: timezone.trim() })
          setTimezoneDraft(null)
        }}
      >
        <div className="field">
          <Label htmlFor="display-timezone">Display timezone</Label>
          <Input
            id="display-timezone"
            value={timezone}
            placeholder={browserTimezone}
            onChange={(event) => {
              setTimezoneDraft(event.target.value)
              setMessage('')
            }}
            aria-invalid={!valid}
            aria-describedby="display-timezone-help"
          />
          <p id="display-timezone-help" className="help">
            Leave empty for the browser timezone ({browserTimezone}). Or enter an IANA timezone,
            such as Europe/Paris, America/New_York or UTC. Date-time input fields always use browser
            local time, independently of this display setting.
          </p>
          {!valid && (
            <p role="alert" className="text-sm text-destructive">
              Enter a valid IANA timezone or leave this field empty.
            </p>
          )}
        </div>
        <Button
          type="submit"
          variant="outline"
          disabled={!valid || timezone === preferences.timezone}
        >
          Apply timezone
        </Button>
      </form>
      <div className="field max-w-sm">
        <Label htmlFor="display-page-size">Rows per page</Label>
        <select
          id="display-page-size"
          className="native-select"
          value={preferences.pageSize}
          aria-describedby="display-page-size-help"
          onChange={(event) =>
            save({ pageSize: Number(event.target.value) as DisplayPreferences['pageSize'] })
          }
        >
          <option value={25}>25</option>
          <option value={50}>50</option>
          <option value={100}>100</option>
        </select>
        <p id="display-page-size-help" className="help">
          Applies immediately to paginated tables. An explicit table page size in the URL takes
          precedence. Small notification previews are unchanged.
        </p>
      </div>
      <p role="status" className="text-sm text-muted-foreground">
        {message}
      </p>
    </section>
  )
}
