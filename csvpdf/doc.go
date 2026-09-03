// Package csvpdf converts CSV data into a simple, paginated PDF table.
//
// The package deals only with conversion. It does not open or close files,
// write HTTP responses, or send WebSocket messages. Callers retain ownership
// of the readers and writers they pass in.
package csvpdf
