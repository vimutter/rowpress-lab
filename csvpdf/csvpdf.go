package csvpdf

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"codeberg.org/go-pdf/fpdf"
)

const (
	defaultTitle = "CSV export"
	pageMargin   = 10.0
	rowHeight    = 7.0
	headerFillR  = 225
	headerFillG  = 231
	headerFillB  = 239
)

// Options controls the generated document. Zero values produce a usable PDF.
type Options struct {
	// Title is displayed above the table. It defaults to "CSV export".
	Title string

	// Comma is the CSV field delimiter. A zero value means a comma.
	Comma rune
}

// Convert reads CSV records from src and writes a PDF document to dst.
//
// Convert does not close src or dst. The first CSV record is treated as the
// table header, and every later record must contain the same number of fields.
func Convert(ctx context.Context, dst io.Writer, src io.Reader, opts Options) error {
	if ctx == nil {
		return errors.New("csvpdf: nil context")
	}
	if dst == nil {
		return errors.New("csvpdf: nil destination")
	}
	if src == nil {
		return errors.New("csvpdf: nil source")
	}

	reader := csv.NewReader(src)
	if opts.Comma != 0 {
		reader.Comma = opts.Comma
	}

	header, err := readRecord(ctx, reader)
	if errors.Is(err, io.EOF) {
		return errors.New("csvpdf: CSV input is empty")
	}
	if err != nil {
		return fmt.Errorf("csvpdf: read header: %w", err)
	}
	if len(header) == 0 {
		return errors.New("csvpdf: CSV header has no fields")
	}

	reader.FieldsPerRecord = len(header)
	doc := newDocument(opts, len(header))
	drawHeader(doc, header)

	for recordNumber := 2; ; recordNumber++ {
		record, readErr := readRecord(ctx, reader)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("csvpdf: read record %d: %w", recordNumber, readErr)
		}

		if needsPage(doc) {
			doc.AddPage()
			drawHeader(doc, header)
		}
		drawRow(doc, record)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("csvpdf: conversion canceled: %w", err)
	}
	if err := doc.Output(dst); err != nil {
		return fmt.Errorf("csvpdf: write PDF: %w", err)
	}
	return nil
}

func readRecord(ctx context.Context, reader *csv.Reader) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("conversion canceled: %w", err)
	}
	return reader.Read()
}

func newDocument(opts Options, columns int) *fpdf.Fpdf {
	orientation := "P"
	if columns > 5 {
		orientation = "L"
	}

	doc := fpdf.New(orientation, "mm", "A4", "")
	doc.SetMargins(pageMargin, pageMargin, pageMargin)
	doc.SetAutoPageBreak(false, pageMargin)
	doc.SetTitle(title(opts), false)
	doc.AddPage()
	doc.SetFont("Helvetica", "B", 16)
	doc.CellFormat(0, 10, title(opts), "", 1, "L", false, 0, "")
	doc.Ln(2)
	return doc
}

func title(opts Options) string {
	if strings.TrimSpace(opts.Title) == "" {
		return defaultTitle
	}
	return opts.Title
}

func drawHeader(doc *fpdf.Fpdf, fields []string) {
	doc.SetFont("Helvetica", "B", 9)
	doc.SetFillColor(headerFillR, headerFillG, headerFillB)
	drawCells(doc, fields, true)
}

func drawRow(doc *fpdf.Fpdf, fields []string) {
	doc.SetFont("Helvetica", "", 9)
	drawCells(doc, fields, false)
}

func drawCells(doc *fpdf.Fpdf, fields []string, fill bool) {
	pageWidth, _ := doc.GetPageSize()
	cellWidth := (pageWidth - 2*pageMargin) / float64(len(fields))
	for _, field := range fields {
		doc.CellFormat(cellWidth, rowHeight, fitText(doc, field, cellWidth-2), "1", 0, "L", fill, 0, "")
	}
	doc.Ln(-1)
}

func fitText(doc *fpdf.Fpdf, value string, availableWidth float64) string {
	if doc.GetStringWidth(value) <= availableWidth {
		return value
	}

	const ellipsis = "..."
	for len(value) > 0 && doc.GetStringWidth(value+ellipsis) > availableWidth {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value + ellipsis
}

func needsPage(doc *fpdf.Fpdf) bool {
	_, pageHeight := doc.GetPageSize()
	return doc.GetY()+rowHeight > pageHeight-pageMargin
}
