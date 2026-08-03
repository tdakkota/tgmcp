# tgmcp

<p align="center"><img src="logo.svg" alt="tgmcp logo" width="200"/></p>

A [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server for
Telegram, built on the [gotd](https://github.com/gotd/td) client. It lets an MCP
client (Claude Desktop, Claude Code, etc.) discover which channels have unread
messages, read those messages, and mark them as read.

It authenticates as a **user account** (not a bot), so it sees the same channels
and unread state as the logged-in user.

## Tools

| Tool | Description |
| --- | --- |
| `list_unread_channels` | List broadcast channels that currently have unread messages, with unread counts. |
| `read_channel_unread` | Read the unread messages of a channel (by `@username` or numeric ID), newest first. Reading does **not** mark them as read. |
| `mark_chat_read` | Mark all messages in a dialog as read: private chats, groups, supergroups and channels. |
| `mark_all_channels_read` | Mark every unread broadcast channel as read in one call. Does not touch private chats or groups. |
| `list_chats` | List cached dialogs (private, groups, supergroups, channels) with id/title/username/type/unread. Optional `query` matches title or username. |
| `search_chats` | Search Telegram (`contacts.search`) for users, bots, groups and channels, including public ones not in the dialog list. |
| `get_me` | Get the signed-in account: id, name, username, phone and bio. |
| `resolve_peer` | Resolve a user, bot, group or channel by id, `@username`, t.me link or phone, and return its details. |
| `get_file` | Download a message's media into TG_FILE_ROOT, or with `inline: true` return the image in the tool result for vision-capable clients (requires `TG_ALLOW_INLINE_MEDIA`). |
| `get_chat_messages` | Fetch recent history from any chat (by id, @user, me, t.me link). Service messages are included, flagged with `service` and an `action` name. |
| `search_chat_messages` | Search messages in a chat (optional filter: photo/video/document/url/...). |
| `send_message` | Send text; optional reply_to_message_id, silent, no_webpage. |
| `send_file` | Send file from TG_FILE_ROOT; optional caption, as_photo, reply, silent. |
| `send_screenshot_notification` | Tell a chat that a screenshot was taken. Posts a visible service message. |

## How it works

To avoid `FLOOD_WAIT`, the server does **not** re-fetch the dialog list on every
tool call. Instead, mirroring [tdlib](https://github.com/tdlib/td)'s strategy:

- The dialog list is loaded **once** (batched at 100 per request) and served
  from an in-memory cache.
- Per-dialog unread counts are kept live from the Telegram **update stream** via
  gotd's [`updates.Manager`](https://pkg.go.dev/github.com/gotd/td/telegram/updates),
  which recovers gaps with `getDifference`.
- The dialog cache, the update state (`pts/qts/date/seq`), and channel access
  hashes are **persisted to bbolt** (`<session>/updates.bolt`), so a restart
  reconciles incrementally instead of re-listing every dialog.

## Setup

1. Get `APP_ID` and `APP_HASH` from <https://my.telegram.org/apps>.
2. Configure credentials, either via environment variables or a `.env` file:

   ```sh
   cp .env.example .env
   # edit .env
   ```

3. Build:

   ```sh
   go build -o tgmcp .
   ```

4. Log in once. This shows a **QR code** to scan from your Telegram app
   (Settings → Devices → Link Desktop Device) and prompts for the 2FA password
   if you have one set. It stores a reusable session under `./session/`:

   ```sh
   ./tgmcp auth
   ```

## Configuration

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `APP_ID` | yes | — | App ID from my.telegram.org. |
| `APP_HASH` | yes | — | App hash from my.telegram.org. |
| `TG_PHONE` | no | — | Phone number; only used to name the session subfolder. |
| `TG_SESSION_DIR` | no | `session` | Directory for the session and state database. |
| `MCP_ADDR` | no | `127.0.0.1:8080` | Address for the MCP HTTP server. |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, or `error`. |
| `TG_FILE_ROOT` | no | — | Base dir for send_file. Empty disables send_file with clear error. Paths are resolved inside this root; traversal rejected. |
| `TG_ALLOW_SEND` | no | off | Grants `send_message`, `send_file`, `send_reaction`, `send_chat_action`, `send_screenshot_notification`. |
| `TG_ALLOW_PROFILE_EDIT` | no | off | Grants `update_profile`, `update_profile_photo`. |
| `TG_ALLOW_INLINE_MEDIA` | no | off | Lets `get_file` return image bytes in the tool result (`inline: true`) instead of writing to disk. |

Every right is **opt-in** and granted only by the literal string `true`:
anything else, including `1` and `TRUE`, leaves it off. Without any of them the
server is read-only.

### Telemetry

`tgmcp serve` runs under [`go-faster/sdk/app`][sdk], so it emits OpenTelemetry
traces, metrics and logs, and is configured entirely through the standard
`OTEL_*` environment variables.

[sdk]: https://github.com/go-faster/sdk

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_TRACES_EXPORTER` | `otlp` | `otlp`, `stdout`, `stderr`, or `none`. |
| `OTEL_METRICS_EXPORTER` | `otlp` | `otlp`, `prometheus`, `stdout`, `stderr`, or `none`. |
| `OTEL_LOGS_EXPORTER` | `otlp` | `otlp`, `stdout`, `stderr`, or `none`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector endpoint. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | `grpc` or `http/protobuf`. |
| `OTEL_EXPORTER_PROMETHEUS_HOST` / `_PORT` | `localhost` / `9464` | Scrape endpoint when the exporter is `prometheus`. |
| `OTEL_LOG_LEVEL` | — | Overrides `LOG_LEVEL`. |
| `PPROF_ADDR` | — | Serves `/debug/pprof` when set. |

> The exporters default to **OTLP over gRPC to `localhost:4317`**. Without a
> collector there, the SDK retries exports in the background and logs errors.
> Set `OTEL_TRACES_EXPORTER=none OTEL_METRICS_EXPORTER=none
> OTEL_LOGS_EXPORTER=none` to run without telemetry.

Instrumentation:

- **Traces**: one span per MCP request (named `tools/call <tool>`), a child span
  per MTProto call (`tg.rpc: <method>`), and an HTTP server span that continues
  the client's trace context if it sends one.
- **Metrics**: `mcp.request.{count,failures,duration}` by method and tool,
  `tg.rpc.{count,failures,duration}` by MTProto method, and
  `tg.flood_wait.count`. Go runtime metrics are exported too.

## Running

The server speaks MCP over **HTTP** (streamable transport):

```sh
./tgmcp serve
```

It loads the session created by `tgmcp auth` and never prompts; if the session
is missing or expired it exits and asks you to run `tgmcp auth` again.

### Claude Code / Claude Desktop config

Point your MCP client at the HTTP endpoint (adjust the address to `MCP_ADDR`):

```json
{
  "mcpServers": {
    "telegram": {
      "type": "http",
      "url": "http://127.0.0.1:8080"
    }
  }
}
```

## Notes

- Logs are written as JSON to **stderr**, so journald (or any supervisor)
  captures them, and are mirrored to the OTLP logs exporter. Set
  `LOG_LEVEL=debug` to see every MTProto call and MCP request. Set
  `OTEL_ZAP_TEE=false` to stop writing them to stderr.
- Unread detection compares each message ID against the dialog's
  `read_inbox_max_id`; messages newer than that boundary are returned.
- `list_chats`, `get_chat_messages`, and `search_chat_messages` include
  `limited: true` when the returned results were capped. Message tools also
  return `next_offset_id` for continuing with another call.
- After a long disconnect, a too-long difference is resynced automatically: a
  single channel via `messages.getPeerDialogs`, or the whole list via a full
  re-bootstrap. Deleting `<session>/updates.bolt` forces a clean re-bootstrap on
  the next start.
