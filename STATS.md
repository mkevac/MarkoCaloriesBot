# Usage stats

Send `/stats` to the bot from the account configured by `ADMIN_USERNAME`.
The command reports:

- Unique users and submitted requests, overall and in the last 24 hours, 7 days, and 30 days.
- Average requests per day since tracking began (the first partial day counts as one day), and per user overall.
- Top 10 users by all-time requests.

A submitted photo is one request; an album is also one request. Repeated delivery
of the same Telegram message does not increase the count. Failed analyses count
as submitted requests. Commands and non-photo messages do not count. Users are
identified by their Telegram user ID, with their latest username used for display.

SQLite stores IDs, usernames, and request timestamps, not meal content. Tracking
starts when this version first runs; older in-memory counts cannot be recovered.
Stats survive restarts.

The local database defaults to `./data/stats.db`. Set `STATS_DB_PATH` to override
it. Docker Compose stores `/app/data/stats.db` in the `stats-data` named volume.
Keep that volume when updating or recreating the bot. The database directory and
SQLite files are excluded from Git. Go 1.24 or later is required by the pure-Go
SQLite driver; `CGO_ENABLED=0` remains supported.

Validation: `go test -race ./...`.
