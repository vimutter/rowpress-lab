# rowpress-lab

A learning project that converts CSV tables to PDF through a reusable Go
package, with CLI and WebSocket front ends planned.

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
either dimension. Binary callers use `csvpdf.Logo{Data: imageBytes}`. Future
WebSocket callers can use `csvpdf.Logo{Base64: encodedImage}`, with either
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
