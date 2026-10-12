# JMAP Screener

A daemon that watches your email and learns from where you file things. Move a message
from the Screener folder to "Family" — future mail from that sender goes straight to
"Family". No rules to write, no filters to configure.

Built on [Stalwart's](https://stalw.art/) JMAP API and per-account Sieve scripts
(RFC 9661). Runs alongside your mail server, using JMAP WebSocket push when
available and polling for changes as a fallback every 5 minutes.

## What It Does

### Screen-in (from Screener)
When you move a message **out of Screener** into any folder, the daemon learns that
sender → folder mapping and updates your Sieve script. Future mail from that sender
goes directly to the chosen folder.

| Move | Result |
|------|--------|
| Screener → Family | Sender routed to Family |
| Screener → Work | Sender routed to Work |
| Screener → Junk | Sender routed to Junk |
| Screener → Trash | Route **deleted** (sender blocked — future mail stays in Screener) |
| Any folder → Screener | Route **deleted** (sender returns to default screening) |

### Screen-in (from Junk)
If the spam filter wrongly classifies a known sender as spam, move the message **out of
Junk** into any non-Screener folder. The daemon learns the route and will proactively
pull that sender's future messages out of Junk on each poll cycle.

Moving a message from Junk to Screener is a reset, not a learned Junk → Screener route:
the daemon removes any existing route for that sender so future mail is screened by the
default catch-all again.

### Junk Rescue
Junk rescue is an optional fallback. When enabled, the daemon scans your Junk folder for
messages from known senders and moves them to the correct folder. This is usually not
needed when JMAP Screener owns the active per-account Sieve script, because learned
sender routes run before the generated spam rule.

Enable it only if another rule or server-side process files known senders into Junk
before the Screener script can route them.

### Blocking
Move a message from Screener to Trash to block that sender — the daemon removes the
route entirely. Future mail from that sender returns to Screener for you to re-evaluate.
You can also move a message from any other folder, including Junk, back into Screener to
remove the learned route without treating it as a Junk rescue.

## Quick Start

### 1. Create an API Token

In the Stalwart admin interface, create a **user-level** API token with these
permissions:

```
jmapEmailGet
jmapEmailQuery
jmapEmailChanges
jmapEmailSet
jmapMailboxGet
jmapSieveScriptGet
jmapSieveScriptSet
```

### 2. Configure

Two options — pick one:

**Option A: Config file** (recommended for most setups)
```bash
cp config.example.toml config.toml
# Edit config.toml — set jmap.url and jmap.token
```

**Option B: Environment variables** (no config file needed)
```bash
export SCREENER_JMAP_URL=https://mail.example.com/jmap/
export SCREENER_JMAP_TOKEN=your-api-token
```

The daemon discovers your primary JMAP mail account automatically. To set
`jmap.account_id` explicitly, query the discovery endpoint with your API token:

```bash
curl -H "Authorization: Bearer $SCREENER_JMAP_TOKEN" \
  https://mail.example.com/.well-known/jmap
```

Use the account ID listed in `primaryAccounts` for `urn:ietf:params:jmap:mail`.
On Stalwart this is commonly `p`.

### 3. Create a Screener Mailbox

In your email client, create a mailbox named `Screener` in your account. This is where
unknown senders will land. You can use a different name — just set
`monitor.screener_mailbox` in config or `SCREENER_MONITOR_SCREENER_MAILBOX` in env.

### 4. Run

**Docker:**
```bash
docker volume create jmap-screener-data

docker run -d \
  --name screener \
  -v $(pwd)/config.toml:/etc/screener/config.toml:ro \
  -v jmap-screener-data:/data \
  ghcr.io/kwatson/jmap-screener:latest
```

Use a version tag such as `ghcr.io/kwatson/jmap-screener:v0.2.1` if you want to
pin a specific release.

Set `db.path = "/data/screener.db"` in your config so the SQLite database is
stored on the mounted Docker volume. The default `screener.db` path is relative
to the container working directory and is not suitable for persistent Docker
deployments.

The container runs as UID/GID `65532`. A Docker-managed volume, as shown above,
works without extra permission setup because Docker initializes it from the image's
`/data` directory ownership.

If you prefer a host bind mount instead:

```bash
mkdir -p data
sudo chown -R 65532:65532 data

docker run -d \
  --name screener \
  -v $(pwd)/config.toml:/etc/screener/config.toml:ro \
  -v $(pwd)/data:/data \
  ghcr.io/kwatson/jmap-screener:latest
```

The bind-mounted `data/` directory must be writable by UID/GID `65532`.

**Manual:**
```bash
go build -o screener ./cmd/screener/
./screener --config config.toml
```

**Env-only (no config file):**
```bash
./screener
# All settings come from environment variables
```
When `--config` is omitted, the daemon loads `config.toml` if it exists; otherwise it
starts from defaults plus environment variables. When `--config /path/to/file` is
provided explicitly, a missing or invalid file is fatal.

### 5. Verify

```bash
curl http://localhost:8080/healthz
# → "ok"
```

Move a test message from Screener to another folder, wait for push or the next
poll cycle, then check Stalwart's Sieve settings for a script named `screener`.

If your JMAP server advertises WebSocket push, the daemon wakes promptly on Email
state changes. Polling remains enabled as a fallback and as the authoritative sync
path.

## Configuration

All settings have defaults. Only `jmap.url` and `jmap.token` are required.

| Setting | Env Variable | Default | Description |
|---------|-------------|---------|-------------|
| `jmap.url` | `SCREENER_JMAP_URL` | *required* | Stalwart JMAP API endpoint |
| `jmap.token` | `SCREENER_JMAP_TOKEN` | *required* | User API token |
| `jmap.account_id` | `SCREENER_JMAP_ACCOUNT_ID` | auto-discovered | JMAP account identifier |
| `jmap.advertised_host_override` | `SCREENER_JMAP_ADVERTISED_HOST_OVERRIDE` | disabled | Replace the hostname in upload and WebSocket URLs advertised by JMAP discovery |
| `jmap.push_enabled` | `SCREENER_JMAP_PUSH_ENABLED` | `true` | Use JMAP WebSocket push to reduce sync latency; polling still runs as fallback |
| `db.path` | `SCREENER_DB_PATH` | `screener.db` | SQLite database path |
| `daemon.poll_interval_seconds` | — | `300` | Seconds between fallback JMAP polls |
| `monitor.screener_mailbox` | `SCREENER_MONITOR_SCREENER_MAILBOX` | `Screener` | Mailbox name for unknown senders |
| `monitor.junk_mailbox` | `SCREENER_MONITOR_JUNK_MAILBOX` | `Junk Mail` | Mailbox name for spam |
| `monitor.spam_score_threshold` | `SCREENER_MONITOR_SPAM_SCORE_THRESHOLD` | `5` | Spam score at or above which unknown senders route to Junk |
| `monitor.excluded_senders` | — | `["mailer-daemon@", "postmaster@"]` | Senders to never learn routes for |
| `rescue.enabled` | — | `false` | Proactively rescue known senders from Junk |
| `rescue.batch_size` | — | `100` | Emails per rescue query page |
| `health.port` | — | `8080` | Health check HTTP port |
| `logging.level` | — | `info` | `debug`, `info`, `warn`, `error` |

Non-empty env vars override config file values. This is useful for Docker secrets
or keeping tokens out of version-controlled files. An empty env var does not clear
a value set in the config file.

### Split-Host JMAP Discovery

Some proxy or migration setups serve the JMAP API on one hostname while the JMAP
session advertises upload and WebSocket URLs on another. To keep using session
discovery but connect those advertised endpoints through the API host, set:

```bash
export SCREENER_JMAP_URL=https://jmap.example.com/jmap/
export SCREENER_JMAP_ADVERTISED_HOST_OVERRIDE=jmap.example.com
```

The override changes only the hostname in the discovered `uploadUrl` and JMAP
WebSocket capability URL. It does not change `SCREENER_JMAP_URL`, the
`/.well-known/jmap` request, normal JMAP API calls, or an upload URL inferred when
discovery is unavailable. Paths, query parameters, schemes, and advertised ports
are preserved.

The override host receives authenticated requests and Sieve script contents. Use
only a trusted hostname with a valid TLS certificate. Leaving the setting unset
preserves the advertised URLs without modification.

## Sieve Script

The daemon generates and uploads an active Sieve script named `screener` to your
account. JMAP allows only one active per-account Sieve script, so activating
JMAP Screener disables any other active user-managed Sieve script for that
account.

Example of what it looks like:

```sieve
require ["fileinto", "spamtest", "relational", "comparator-i;ascii-numeric"];

# Generated by JMAP Screener at 2026-06-23T18:00:00Z
# 3 known sender(s)

if address :is ["from", "sender"] "alice@example.com" { fileinto "Family"; stop; }
if address :is ["from", "sender"] "bob@work.com" { fileinto "Work"; stop; }
if address :is ["from", "sender"] "carol@newsletter.org" { fileinto "Subscriptions"; stop; }

# Spam: route unknown spam to Junk
if spamtest :value "ge" :comparator "i;ascii-numeric" "5" { fileinto "Junk Mail"; stop; }

# Default: route unknown senders to Screener
fileinto "Screener";
```

- Each `fileinto` is followed by `stop;` — once a sender matches, no further rules run.
- Rules match the visible `From` and `Sender` headers learned from JMAP, not the SMTP envelope sender.
- Known sender routes run before spam routing, so learned senders still go to their target folders even if spam scoring flags a message.
- Unknown messages with a spam score greater than or equal to `monitor.spam_score_threshold` go to `monitor.junk_mailbox`.
- The final `fileinto` is the catch-all that routes unknowns to Screener.
- With WebSocket push, the script is usually updated promptly after you file a message; without push, it updates within one fallback poll interval.
- Nested mailboxes are supported. The daemon resolves JMAP mailbox IDs to display
  paths and generates matching Sieve `fileinto` paths such as `Receipts.2026`.
- You can inspect it at any time via Stalwart's Sieve settings for your account.

## Architecture

```
Sender → SMTP → Stalwart MTA
                   │
                   ├─ Spam filter (scores message)
                   └─ Per-Account Sieve (your script)
                         │
                         ├─ Known sender? → target folder
                         ├─ Spam score ≥ threshold? → Junk Mail
                         └─ Unknown?      → Screener folder
                                             │
                    ┌────────────────────────┘
                    │  You move it to "Family"
                    ▼
              Screener Daemon (JMAP push + polling)
                    │
                    ├─ Detects the move
                    ├─ Saves sender → Family in SQLite
                    ├─ Rescues same sender from Junk if present
                    └─ Updates your Sieve script
```

The daemon only reads mailbox metadata, email headers (From/Sender, mailbox IDs), and Sieve
scripts. It never reads email bodies. All JMAP calls are scoped to your account's
API token.

## Building

```bash
go build -o screener ./cmd/screener/
```

Requires Go 1.25+. Uses pure-Go SQLite (`modernc.org/sqlite`) — no CGO, no system
dependencies.

## Health Check

```
GET /healthz
```

- `200 OK` — daemon is running, JMAP calls are healthy, and the Sieve script is current
- `503 Service Unavailable` — JMAP calls are failing or the Sieve script could not be updated

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
