# Chat/WebSocket Server

WebSocket chat server with rooms, broadcast, and backpressure handling.

## Location

- Implementation: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/chat_ws.go`
- Tests: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/chat_ws_test.go`

## Endpoint

- `GET /ws/chat?room=<room>&user=<name>`

## Behavior

- Connects clients to room (`room` defaults to `lobby`).
- Broadcasts room-local messages to all room clients.
- Emits join/leave system events.
- Ignores invalid JSON payloads and empty text messages.

### Message formats

Incoming:

```json
{"text":"hello"}
```

Outgoing:

```json
{
  "type": "message",
  "room": "lobby",
  "from": "alice",
  "text": "hello",
  "timestamp": "2026-03-13T13:00:00Z"
}
```

## Backpressure Rule

Each client has bounded send buffer.
If a client cannot keep up (buffer full), server drops that client connection.

## Optional Auth Integration

- If `Authorization: Bearer <access_token>` is present and valid, sender identity is derived from token (`user-<id>`).
- Otherwise falls back to `user` query param, then anonymous id (`anon-*`).
