# rowpress-lab

A learning project that converts CSV tables to PDF through a reusable Go
package, with CLI and WebSocket front ends.

## Core package

`csvpdf.Convert` uses Go's standard I/O interfaces, so the conversion logic is
independent of its transport:

```go
err := csvpdf.Convert(ctx, destination, source, csvpdf.Options{
	Title: "Quarterly report",
	Logo:  csvpdf.Logo{Data: logoBytes},
})
```

- `source` is an `io.Reader`: a file, stdin, a byte buffer, or a request body.
- `destination` is an `io.Writer`: a file, stdout, a buffer, or a response.
- the caller owns both values and is responsible for closing them.

The current first slice renders the first record as a repeated table header,
paginates rows, validates consistent field counts, and truncates cells that do
not fit. The built-in PDF font currently limits reliable text rendering to its
Latin character set; embedded Unicode fonts are a future milestone.

## CLI

Convert files:

```sh
go run ./cmd/rowpress \
  -input data.csv \
  -output report.pdf \
  -title "Quarterly report" \
  -logo company-logo.png
```

Or use standard streams (`-` is the default for both paths):

```sh
cat data.csv | go run ./cmd/rowpress > report.pdf
```

Use `-delimiter ';'` for a delimiter other than a comma. Run
`go run ./cmd/rowpress -help` to see every option.

The CLI reads a normal PNG, JPEG, or GIF file with `-logo`; users do not need
to keep base64 files on disk. Logos may be up to 5 MiB and 4096 pixels in
either dimension. Binary callers use `csvpdf.Logo{Data: imageBytes}`. WebSocket
callers can use `csvpdf.Logo{Base64: encodedImage}`, with either
plain base64 or a browser-style data URL (`data:image/png;base64,...`).

## Tests and coverage

Go includes statement coverage in its standard toolchain; no coverage library
is needed:

```sh
make test             # tests with the race detector
make coverage         # writes coverage.out and browsable coverage.html
make coverage-check   # fails below COVERAGE_MIN (100% by default)
make coverage-check COVERAGE_MIN=90
```

## Web app

Run the browser interface locally:

```sh
go run ./cmd/rowpress-server
```

Then open <http://localhost:10000>. Select a CSV and optional logo; the page
opens a persistent WebSocket session, displays its connection state, and
downloads each binary PDF response. You can convert multiple files without
reconnecting. Set `PORT` to listen on a different port. The protocol is
documented in [docs/websocket.md](docs/websocket.md).

Basic Auth is optional locally. Set both variables to enable it:

```sh
BASIC_AUTH_USERNAME=demo BASIC_AUTH_PASSWORD='choose-a-long-password' \
  go run ./cmd/rowpress-server
```

Leaving both unset disables authentication. Setting only one makes the server
refuse to start, which avoids silently running an unprotected deployment.

Build and run the production container:

```sh
docker build -t rowpress .
docker run --rm -p 10000:10000 rowpress
```

`render.yaml` defines the Render web service, Frankfurt region, health check,
free plan, and automatic deployments after the GitHub checks pass. See the
[Render deployment guide](docs/render.md) for the account and Dashboard steps.
