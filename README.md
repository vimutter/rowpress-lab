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

The package renders the first record as a repeated table header, paginates
rows, validates consistent field counts, and truncates cells that do not fit.
It embeds DejaVu Sans regular and bold for UTF-8 text, including Latin, Greek,
Cyrillic, and many common symbols. It does not yet provide CJK font fallback or
complex-script shaping.

## CLI

Convert CSV to PDF (the explicit `csv-to-pdf` command is optional for backward
compatibility):

```sh
go run ./cmd/rowpress csv-to-pdf \
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

Convert a PDF table back to canonical CSV with the OpenAI Responses API:

```sh
export OPENAI_API_KEY='your-project-key'

go run ./cmd/rowpress pdf-to-csv \
  -input report.pdf \
  -output recovered.csv
```

The default model is `gpt-5.6-luna`. Override it with `-model` or
`OPENAI_MODEL`. The API key has no command-line flag, so it does not leak into
shell history or process listings. See [PDF-to-CSV design and fidelity](docs/pdf-to-csv.md)
for the API flow, testing approach, and round-trip limits.

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

The page supports both **CSV → PDF** and **PDF → CSV**. To enable PDF extraction,
set `OPENAI_API_KEY` on the server (in Render's environment for deployment).
Optionally set `OPENAI_MODEL`. Without a key, CSV → PDF still works.
PDFs are sent to OpenAI; review extracted values before using them.

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

The server emits structured JSON logs to stdout. Render captures them without
extra configuration. Conversion logs contain request IDs, timings, and sizes,
but never CSV or logo contents; deployment details are in the
[Render guide](docs/render.md).

Build and run the production container:

```sh
docker build -t rowpress .
docker run --rm -p 10000:10000 rowpress
```

`render.yaml` defines the Render web service, Frankfurt region, health check,
free plan, and automatic deployments after the GitHub checks pass. See the
[Render deployment guide](docs/render.md) for the account and Dashboard steps.
