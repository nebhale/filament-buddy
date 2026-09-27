# Changelog

## 1.0.0

The first stable release of Filament Buddy.

- Send start, color-change, and end markers three times at 1.1-second intervals
  so retries span separate firmware metrics batches.
- Track sequential manual color-change sections from Buddy G-code metrics.
- Preserve editable spool assignments and weight corrections with a durable
  Spoolman adjustment ledger and explicit uncertain-request reconciliation.
- Add section planning, archived sessions, purple web UI, slicer setup, and
  non-root ARM64/AMD64 containers.
