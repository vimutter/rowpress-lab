package pdfcsv

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
)

// MaxPDFBytes is the largest PDF accepted by Convert.
const MaxPDFBytes = 10 << 20

const (
	maxRows      = 10_000
	maxColumns   = 200
	maxCellBytes = 1 << 20
)

// Extractor turns a PDF into rows. The first row is the table header.
type Extractor interface {
	Extract(context.Context, []byte, string) ([][]string, error)
}

// Options controls CSV output and describes the PDF to the extractor.
type Options struct {
	// Filename is sent as file metadata. An empty value becomes "document.pdf".
	Filename string

	// Comma is the CSV output delimiter. A zero value means a comma.
	Comma rune
}

// Convert reads one PDF from src, extracts its table, and writes CSV to dst.
// Convert does not close src or dst.
func Convert(ctx context.Context, dst io.Writer, src io.Reader, extractor Extractor, opts Options) error {
	if ctx == nil {
		return errors.New("pdfcsv: nil context")
	}
	if dst == nil {
		return errors.New("pdfcsv: nil destination")
	}
	if src == nil {
		return errors.New("pdfcsv: nil source")
	}
	if extractor == nil {
		return errors.New("pdfcsv: nil extractor")
	}

	pdf, err := io.ReadAll(io.LimitReader(src, MaxPDFBytes+1))
	if err != nil {
		return fmt.Errorf("pdfcsv: read PDF: %w", err)
	}
	if len(pdf) == 0 {
		return errors.New("pdfcsv: PDF input is empty")
	}
	if len(pdf) > MaxPDFBytes {
		return fmt.Errorf("pdfcsv: PDF exceeds %d bytes", MaxPDFBytes)
	}
	headerWindow := pdf[:min(len(pdf), 1024)]
	if !bytes.Contains(headerWindow, []byte("%PDF-")) {
		return errors.New("pdfcsv: input does not have a PDF header")
	}

	filename := opts.Filename
	if filename == "" {
		filename = "document.pdf"
	}
	rows, err := extractor.Extract(ctx, pdf, filename)
	if err != nil {
		return fmt.Errorf("pdfcsv: extract table: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pdfcsv: conversion canceled: %w", err)
	}
	if err := validateRows(rows); err != nil {
		return fmt.Errorf("pdfcsv: extracted table: %w", err)
	}

	writer := csv.NewWriter(dst)
	if opts.Comma != 0 {
		writer.Comma = opts.Comma
	}
	if err := writer.WriteAll(rows); err != nil {
		return fmt.Errorf("pdfcsv: write CSV: %w", err)
	}
	return nil
}

func validateRows(rows [][]string) error {
	if len(rows) == 0 {
		return errors.New("no table found")
	}
	if len(rows) > maxRows {
		return fmt.Errorf("table exceeds %d rows", maxRows)
	}
	columns := len(rows[0])
	if columns == 0 {
		return errors.New("header has no columns")
	}
	if columns > maxColumns {
		return fmt.Errorf("table exceeds %d columns", maxColumns)
	}
	for rowIndex, row := range rows {
		if len(row) != columns {
			return fmt.Errorf("row %d has %d columns; expected %d", rowIndex+1, len(row), columns)
		}
		for columnIndex, cell := range row {
			if len(cell) > maxCellBytes {
				return fmt.Errorf("row %d column %d exceeds %d bytes", rowIndex+1, columnIndex+1, maxCellBytes)
			}
		}
	}
	return nil
}
