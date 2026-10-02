# Contributing

Go 1.27+, Node.js 24+, and Python 3 are used for development. The application has no npm dependencies
or browser build step. Browser regression tests use Playwright as a test-only tool. SQLite uses the pure-Go modernc driver.

```sh
go test -race ./...
go vet ./...
node --test internal/buddy/testdata/*.test.cjs
python3 -m unittest discover -s scripts -p '*_test.py'
```

Build a local image with `docker build -t filament-buddy:dev .`. To test the
bundled runtime directly:

```sh
docker build --target test -t filament-buddy:integration .
docker run --rm filament-buddy:integration -test.timeout=5m
```

The integration suite uses local fake HTTP and UDP servers. Real PrusaSlicer
validation runs automatically when installed at its macOS default path; otherwise
set `PRUSA_SLICER` to the executable and `REQUIRE_SLICER=1` to require it. It
creates synthetic 3MF projects with real color-change metadata in temporary
storage. No tests contact printers or a live Spoolman instance.

`go run ./scripts/demo` serves synthetic sessions on loopback port 8091. It uses
a separate temporary database and never starts a Spoolman synchronization worker.

Sessions are independent JSON documents inside a versioned SQLite database.
Indexed SQL columns support active-session, paginated-history, and work queries.
A session document owns its ordered sections, observations, audit history, applied
per-spool balances, and operation ledger. A transaction saves edits and adjustment
plans together. One process lock and an application mutation mutex serialize
changes. Enhanced spool assignment forms compare expected original values
inside that transaction; legacy forms and structural actions retain revision checks.

The worker persists `inflight` before any remote operation. Confirmed writes
update the applied balance and regenerate desired deltas. A crash leaves an
uncertain request requiring explicit resolution, never a blind retry. Refunds
precede new charges for the same section; uncertainty blocks other writes to the
same spool. Never replace Spoolman's total weight with a locally computed total.

Section weights come only from printer markers. Make manual weight corrections
directly in Spoolman.

Keep tests at behavior boundaries: reassignment after external consumption,
missing markers, stale forms, uncertain remote outcomes, and restart recovery.
Schema changes need migrations. Do not remove audit/ledger records to reclaim
space. Review desktop and mobile layouts. Submit focused changes through PRs.
Source and both container architecture checks must pass; a second-person
approval is not required. Use squash merges with a concise, capitalized
imperative subject and a body explaining motivation or tradeoffs when useful.
Dependabot updates are reviewed manually and grouped weekly by ecosystem.

Release publication follows Snapshot Buddy: publish a GitHub release to trigger
verified ARM64/AMD64 images, SBOM, provenance, exact version and compatible alias
tags. CI starts native ARM64 and AMD64 containers, checks `/healthz` and `/setup`,
and verifies non-root execution. Before image tags are published, the release
workflow uploads the exact Alpine source archives, package inventories, and
checksums described in [THIRD_PARTY.md](THIRD_PARTY.md).

Tag pushes alone do not publish, and no workflow deploys dockerpi. Exact version
tags cannot be overwritten. Stable releases publish `X.Y.Z`; the newest patch in
a minor series updates `X.Y`, and `latest` follows the latest stable release
without moving backwards. Prereleases receive only their exact version tag.
Verify anonymous pulls after the first publication and make the repository-linked
GHCR package public if necessary. Publication uses `GITHUB_TOKEN`, not a stored
registry password.

Keep live printer addresses, credentials, personal inventory, and deployment
files out of source control. Use illustrative configuration and synthetic data.

## Live browser verification

The shared `web/updates.js` transport and reconciler, `web/name.js` inline
editor, and `web/setup.js` setup controls intentionally match in both Buddy
repositories. Keep changes and their regression tests aligned. The server owns
rendered presentation; the browser owns drafts and interaction. Notifications
are invalidations, not commands or mutation acknowledgments.

CI runs `scripts/browser.test.cjs` against isolated loopback demos with synthetic
data at desktop and mobile widths. To run it locally with Node.js 24+:

```sh
buddy_browser_tools="$(mktemp -d)"
npm install --prefix "$buddy_browser_tools" --no-audit --no-fund playwright@1.62.1
node "$buddy_browser_tools/node_modules/playwright/cli.js" install chromium --only-shell
NODE_PATH="$buddy_browser_tools/node_modules" node --test scripts/browser.test.cjs
```

Set `BUDDY_SCREENSHOT_DIR` to retain desktop and mobile screenshots. The tests
build and stop their own demo server with `BUDDY_DEMO_PORT=0`. Demo-only marker,
catalog (Filament), and handler-restart routes exercise real HTTP without touching
production or hardware. Production endpoints do not include these controls.
The suites cover multiple tabs, conflicts, focus, drafts, polling, reconnects,
and fresh credentials after a restart. Unit tests additionally control heartbeat
expiry, hidden tabs, and obsolete responses deterministically. Required Source
and native ARM64/AMD64 container checks must all pass before merge. Follow the
README public-host checks at the next deployment; local results cannot certify
a production Cloudflare Tunnel.
