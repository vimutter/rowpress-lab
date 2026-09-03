# rowpress-lab

A learning project that converts CSV tables to PDF through a reusable Go
package, with CLI and WebSocket front ends planned.

## Core package

`csvpdf.Convert` uses Go's standard I/O interfaces, so the conversion logic is
independent of its transport:

```go
err := csvpdf.Convert(ctx, destination, source, csvpdf.Options{
	Title:      "Quarterly report",
	LogoBase64: encodedLogo,
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
  -logo-base64 "iVBORw0KGgo..."
```

Or use standard streams (`-` is the default for both paths):

```sh
cat data.csv | go run ./cmd/rowpress > report.pdf
```

Use `-delimiter ';'` for a delimiter other than a comma. Run
`go run ./cmd/rowpress -help` to see every option.

The logo may be PNG, JPEG, or GIF, up to 5 MiB after decoding. Both plain
base64 and browser-style data URLs (`data:image/png;base64,...`) are accepted,
which lets CLI and future WebSocket callers use the same package API.

## Tests and coverage

Go includes statement coverage in its standard toolchain; no coverage library
is needed:

```sh
make test             # tests with the race detector
make coverage         # writes coverage.out and browsable coverage.html
make coverage-check   # fails below COVERAGE_MIN (100% by default)
make coverage-check COVERAGE_MIN=90
```
