# Changelog

## Unreleased

- Add editable session display names with original-name fallback, live updates,
  and protection against conflicting edits in another tab. Printer markers and
  session matching remain unchanged.

- Update print libraries and session pages automatically over authenticated SSE,
  with polling fallback, reconnect recovery, and a compact connection indicator.
- Preserve drafts, focus, scroll position, filters, and expanded panels; save
  forms inline and refresh credentials after restarts without replaying writes.
- Add an accessible searchable spool combobox with local catalog search,
  archived filtering, keyboard selection, color and remaining weight, refresh
  status, and preserved choices during catalog updates.
- Compare expected spool assignments and weight overrides transactionally so
  worker progress does not invalidate edits; show explicit conflict resolution
  while retaining legacy revision and inventory reconciliation safeguards.
- Add browser and SSE regression checks and document public-host streaming
  verification for the next Cloudflare Tunnel deployment.

## 1.0.0

The first stable release of Filament Buddy.

- Send start, color-change, and end markers three times at 1.1-second intervals
  so retries span separate firmware metrics batches.
- Track sequential manual color-change sections from Buddy G-code metrics.
- Preserve editable spool assignments and weight corrections with a durable
  Spoolman adjustment ledger and explicit uncertain-request reconciliation.
- Add section planning, archived sessions, purple web UI, slicer setup, and
  non-root ARM64/AMD64 containers.
