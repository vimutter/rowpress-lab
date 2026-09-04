# WebSocket protocol

The page opens `GET /ws` when it loads and keeps that connection for its
lifetime. The server sends WebSocket protocol-level pings every 25 seconds;
browsers answer with pongs automatically. If the connection closes, the page
shows the disconnected state and reconnects after 1.5 seconds.

Application messages are processed sequentially. The UI permits one active
conversion at a time, but any number of conversions can use the same session.

## Server ready message

Immediately after connection, the server sends a JSON text message:

```json
{"type":"ready","message":"Connected and ready"}
```

## Conversion request

The client sends a JSON text message with a unique ID:

```json
{
  "type": "convert",
  "id": "f2e8a1d9-23f5-4a45-b273-195da48b428c",
  "title": "Quarterly report",
  "delimiter": ",",
  "csv": "name,score\nAda,10\n",
  "logo_base64": "data:image/png;base64,iVBORw0KGgo..."
}
```

Before conversion, the server acknowledges the job:

```json
{
  "type": "status",
  "id": "f2e8a1d9-23f5-4a45-b273-195da48b428c",
  "message": "Converting CSV to PDF"
}
```

The next message is the generated PDF as a binary WebSocket message. Because
the protocol allows only one in-flight job per browser session, that binary
message belongs to the active job. After receiving it, the page remains
connected and ready for another conversion.

## Error message

Request and conversion failures are JSON text messages. The connection stays
open, so another request can follow:

```json
{
  "type": "error",
  "id": "f2e8a1d9-23f5-4a45-b273-195da48b428c",
  "error": "csvpdf: CSV input is empty"
}
```

Errors that cannot be associated with a parsed request, such as invalid JSON,
omit `id`.
