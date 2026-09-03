# rowpress-lab

A learning project that converts CSV tables to PDF through a reusable Go
package, with CLI and WebSocket front ends planned.

## Core package

`csvpdf.Convert` uses Go's standard I/O interfaces, so the conversion logic is
independent of its transport:

```go
err := csvpdf.Convert(ctx, destination, source, csvpdf.Options{
	Title: "Quarterly report",
})
```

- `source` is an `io.Reader`: a file, stdin, a byte buffer, or a request body.
- `destination` is an `io.Writer`: a file, stdout, a buffer, or a response.
- the caller owns both values and is responsible for closing them.

The current first slice renders the first record as a repeated table header,
paginates rows, validates consistent field counts, and truncates cells that do
not fit. The built-in PDF font currently limits reliable text rendering to its
Latin character set; embedded Unicode fonts are a future milestone.
