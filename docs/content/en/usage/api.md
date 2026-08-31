---
title: "HTTP API"
weight: 20
---

# HTTP API

SaveAny-Bot provides an HTTP API that allows you to programmatically create download/transfer tasks, query task status, cancel tasks, and more — without going through Telegram.

## Enabling the API

Add or modify the following section in `config.toml`:

```toml
[api]
enable = true
host   = "0.0.0.0"   # Bind address, default 0.0.0.0
port   = 8080         # Listen port, default 8080
token  = "your-token" # Auth token — strongly recommended
```

You can also override these settings with environment variables (prefix `SAVEANY_`):

| Environment Variable | Config Key |
|---|---|
| `SAVEANY_API_ENABLE` | `api.enable` |
| `SAVEANY_API_HOST` | `api.host` |
| `SAVEANY_API_PORT` | `api.port` |
| `SAVEANY_API_TOKEN` | `api.token` |

{{< hint warning >}}
`token` is required. The server refuses to start when the API is enabled without a token.
{{< /hint >}}

## Authentication

When `token` is configured, all API requests must include a Bearer token in the HTTP header:

```
Authorization: Bearer <your-token>
```

On authentication failure, the server returns `401`:

```json
{ "error": "unauthorized", "message": "invalid token" }
```

## Web Dashboard

After starting the API, open `http://<server-address>:<port>/` to use the built-in dashboard. It provides Product Settings, Tasks, Bot Relay, Runtime Logs, and Advanced Settings pages. Common options use a form, while the complete `config.toml` remains available under Advanced Settings.

{{< hint info >}}
The token is stored only in the current browser session's `sessionStorage`. Tokens, passwords, and keys in the global configuration are hidden behind unique placeholders. Keep a placeholder to preserve its original value, or replace it with a new value.
{{< /hint >}}

Bot Relay routes are stored separately in the database, so create, edit, enable/disable, and delete operations take effect immediately. Enable Userbot first and ensure its account has joined the source channel. The receiving SaveAny user must also have an available default storage.

Saving a local configuration writes the active `config.toml` file. With a Docker single-file bind mount, the host file changes as well. HTTP(S) configuration sources are read-only. Environment variables and CLI flags still override TOML, so an overridden file value will not become the effective runtime value.

Saving interrupts current tasks and exits the process. Docker Compose's `restart: unless-stopped` or another external supervisor restarts the service with the new configuration. Without a supervisor, the process remains stopped.

## Error Response Format

All errors use a consistent JSON format:

```json
{
  "error":   "error_code",
  "message": "human readable description"
}
```

Common error codes:

| Error Code | HTTP Status | Meaning |
|---|---|---|
| `unauthorized` | 401 | Authentication failed |
| `method_not_allowed` | 405 | Wrong HTTP method |
| `invalid_request` | 400 | Malformed request body or parameters |
| `task_creation_failed` | 400 | Failed to create task |
| `task_not_found` | 404 | Task ID does not exist |
| `cancel_failed` | 500 | Failed to cancel task |
| `internal_error` | 500 | Internal server error |

---

## Endpoints

### GET /health — Health Check

No authentication required.

**Response `200 OK`:**

```json
{ "status": "ok" }
```

---

### GET /api/v1/storages — List Storages

Returns all currently loaded storage backends.

**Response `200 OK`:**

```json
{
  "storages": [
    { "name": "local",   "type": "local" },
    { "name": "MyMinio", "type": "s3" }
  ]
}
```

---

### GET /api/v1/task-types — List Supported Task Types

**Response `200 OK`:**

```json
{
  "types": [
    "directlinks",
    "ytdlp",
    "aria2",
    "parseditem",
    "tgfiles",
    "tphpics",
    "transfer"
  ]
}
```

---

### GET /api/v1/users — List Relay Owners

Returns each user's database ID, Telegram Chat ID, default storage, and whether the user is ready to receive Relay files.

---

### GET/POST /api/v1/bot-relays — List or Create Relays

Example create request:

```json
{
  "user_id": 1,
  "source_chat": "mychannel",
  "target_bot": "btfffbot",
  "enabled": true,
  "timeout_seconds": 900,
  "quiet_seconds": 5,
  "scan_interval_minutes": 5
}
```

The source must be a Telegram channel accessible to the userbot and may be an `@username`, a `username`, or a Bot API-style `-100...` channel ID. The target must be a bot. Relay does not process source channel updates in real time; it scans incrementally every `scan_interval_minutes` (default 5, range 1–1440). The first scan processes the latest 10 messages. Later scans page back to the returned `last_message_id` and process newer messages in ascending ID order; this field is `null` before the first scan. The GET response's `history` keeps the latest 20 message results with `message_id`, `success`, `error`, and `processed_at`; retries update the existing message record. `timeout_seconds` accepts 1–86400 seconds; `quiet_seconds` accepts 1–300 seconds and cannot exceed the timeout.

### PUT/DELETE /api/v1/bot-relays/:id — Update or Delete a Relay

`PUT` uses the same request body as the create endpoint. Changing the source channel or target bot resets `last_message_id`, clears the old history, and causes the next scan to process the latest 10 messages again.

---

### GET/PUT /api/v1/config — Read or Save Global Configuration

`GET` returns the current source, read-only state, and redacted TOML:

```json
{
  "source": "/app/config.toml",
  "read_only": false,
  "content": "workers = 3\n..."
}
```

`PUT` accepts the complete TOML document. The service restores unchanged secret placeholders, validates the configuration, writes the active local file, and exits so the supervisor can restart it:

```json
{
  "content": "workers = 4\n..."
}
```

Remote HTTP(S) configuration sources cannot be saved through this endpoint.

---

### GET /api/v1/logs — Read Runtime Logs

Reads the latest logs by date. `date` uses `YYYY-MM-DD` and defaults to the newest available log date. `level` accepts `debug`, `info`, `warn`, `error`, or `fatal` and returns that level and above; it defaults to `debug`. `limit` defaults to 500 and has a maximum of 2000. The response returns `dates` newest first and `lines` in reverse chronological order.

---

### POST /api/v1/tasks — Create Task

**Request headers:**

```
Content-Type: application/json
Authorization: Bearer <token>
```

**Request body:**

```json
{
  "type":    "<task_type>",
  "storage": "<storage_name>",
  "path":    "<subpath>",
  "webhook": "<callback_url>",
  "params":  { }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | string | Yes | Task type — see below |
| `storage` | string | Yes | Target storage name, must match a name in your config |
| `path` | string | No | Subdirectory path within the storage |
| `webhook` | string | No | Callback URL invoked when the task reaches a terminal state |
| `params` | object | Yes | Type-specific parameters — see below |

**Response `201 Created`:**

```json
{
  "task_id":    "abc123xyz",
  "type":       "directlinks",
  "status":     "queued",
  "created_at": "2026-03-11T10:00:00Z"
}
```

#### Task Types and params

##### directlinks — Direct URL Download

Download one or more files from direct HTTP/HTTPS URLs.

```json
{
  "type":    "directlinks",
  "storage": "local",
  "path":    "downloads",
  "params": {
    "urls": [
      "https://example.com/file.zip",
      "https://example.com/other.zip"
    ]
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `urls` | []string | Yes | List of download URLs, at least 1 |

##### ytdlp — yt-dlp Media Download

{{< hint warning >}}
Requires yt-dlp to be installed on the system.
{{< /hint >}}

Download videos or audio via yt-dlp, supporting YouTube, Bilibili, and 1000+ other sites.

```json
{
  "type":    "ytdlp",
  "storage": "local",
  "path":    "videos",
  "params": {
    "urls":  ["https://www.youtube.com/watch?v=xxx"],
    "flags": ["--extract-audio", "--audio-format", "mp3"]
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `urls` | []string | Yes | List of media URLs, at least 1 |
| `flags` | []string | No | Extra yt-dlp command-line flags |

##### aria2 — Aria2 Download

{{< hint warning >}}
Requires Aria2 to be enabled and configured (RPC) in the config file.
{{< /hint >}}

Download files via the Aria2 download manager, supporting HTTP/HTTPS, FTP, BitTorrent (magnet links, torrent files), and more.

```json
{
  "type":    "aria2",
  "storage": "local",
  "path":    "downloads",
  "params": {
    "urls":    ["magnet:?xt=urn:btih:..."],
    "options": { "split": "4" }
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `urls` | []string | Yes | List of download URIs, at least 1 |
| `options` | map[string]string | No | Aria2 download options |

##### parseditem — Parser Plugin Download

Hand a URL off to a registered JS plugin or built-in parser for processing and downloading.

```json
{
  "type":    "parseditem",
  "storage": "local",
  "path":    "parsed",
  "params": {
    "url": "https://some-site.com/page"
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `url` | string | Yes | The URL to parse |

Returns `400 task_creation_failed` if no parser is able to handle the URL.

##### tgfiles — Telegram Message File Download

Download files from Telegram messages via message links. Supported link formats:

- `https://t.me/username/123` — public channel or group
- `https://t.me/c/123456789/123` — private channel by numeric ID
- `https://t.me/c/123456789/111/456` — topic message (thread ID / message ID)
- `https://t.me/username/111/456` — topic under a username-based chat

If the message is part of a media group (album), all files in the group are downloaded by default. Append `?single` to the link to force downloading only the single specified message.

```json
{
  "type":    "tgfiles",
  "storage": "local",
  "path":    "telegram",
  "params": {
    "message_links": [
      "https://t.me/username/123",
      "https://t.me/c/1234567890/456"
    ]
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `message_links` | []string | Yes | List of Telegram message links, at least 1 |

##### tphpics — Telegraph Article Images

Download all images from a Telegra.ph article.

Supported URL prefixes: `https://telegra.ph/`, `http://telegra.ph/`, `https://telegraph.co/`, `http://telegraph.co/`

```json
{
  "type":    "tphpics",
  "storage": "local",
  "path":    "telegraph",
  "params": {
    "telegraph_url": "https://telegra.ph/Some-Article-01-01"
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `telegraph_url` | string | Yes | URL of the Telegra.ph article |

##### transfer — Storage-to-Storage Transfer

Transfer files directly between two storage backends without going through Telegram. The source storage must support both listing and reading.

{{< hint info >}}
For `transfer` tasks, the top-level `storage` field is still required for validation, but the actual storages used are determined by `source_storage` and `target_storage` inside `params`.
{{< /hint >}}

```json
{
  "type":    "transfer",
  "storage": "local",
  "params": {
    "source_storage": "MyS3",
    "source_path":    "backups/",
    "target_storage": "LocalDisk",
    "target_path":    "restored/"
  }
}
```

| params field | Type | Required | Description |
|---|---|---|---|
| `source_storage` | string | Yes | Source storage name |
| `source_path` | string | Yes | Path within the source storage; must contain at least one file |
| `target_storage` | string | Yes | Target storage name |
| `target_path` | string | Yes | Destination path within the target storage |

---

### GET /api/v1/tasks — List All Tasks

Returns save tasks created through the HTTP API, Telegram Bot, and Bot Relay. Records are kept in memory only and cleared on restart; `source` is `api`, `bot`, or `relay`.

**Response `200 OK`:**

```json
{
  "tasks": [
    {
      "task_id":    "abc123xyz",
      "type":       "directlinks",
      "status":     "running",
      "title":      "file.zip",
      "source":     "api",
      "storage":    "local",
      "path":       "downloads",
      "error":      "",
      "created_at": "2026-03-11T10:00:00Z",
      "updated_at": "2026-03-11T10:00:05Z",
      "progress": {
        "total_bytes":      10485760,
        "downloaded_bytes": 5242880,
        "percent":          50.0
      }
    }
  ],
  "total": 1
}
```

The `progress` field is only included when `total_bytes > 0`. The `error` field is only included when non-empty.

---

### GET /api/v1/tasks/{task_id} — Get Task

**Path parameter:** `task_id` — the ID returned when the task was created.

**Response `200 OK`:** Same structure as a single task object from the list above.

**Error responses:**
- `400 invalid_request` — no task ID in path
- `404 task_not_found` — task does not exist

---

### DELETE /api/v1/tasks/{task_id} — Cancel Task

**Path parameter:** `task_id`

**Response `200 OK`:**

```json
{ "message": "task cancelled successfully" }
```

**Error responses:**
- `400 invalid_request` — no task ID in path
- `404 task_not_found` — task does not exist
- `500 cancel_failed` — cancellation failed

---

## Task Statuses

| Status | Meaning |
|---|---|
| `queued` | Task is queued and waiting to run |
| `running` | Task is currently executing |
| `completed` | Task finished successfully |
| `failed` | Task encountered an error |
| `cancelled` | Task was cancelled via the DELETE endpoint |

---

## Webhook Callbacks

When a `webhook` URL is provided in the create request, SaveAny-Bot sends a `POST` request to that URL when the task reaches a terminal state (`completed`, `failed`, or `cancelled`).

**Callback request headers:**

```
Content-Type: application/json
User-Agent: SaveAny-Bot/1.0
```

**Callback request body:**

```json
{
  "task_id":      "abc123xyz",
  "type":         "directlinks",
  "status":       "completed",
  "storage":      "local",
  "path":         "downloads",
  "completed_at": "2026-03-11T10:01:00Z",
  "error":        ""
}
```

`completed_at` is only present when status is `completed` or `failed`. `error` is only present when non-empty.

**Retry policy:** Up to 3 attempts, with delays of 1s, 2s, and 3s between retries. Each request has a 30-second timeout.
