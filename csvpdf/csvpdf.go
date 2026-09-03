package csvpdf

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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
	logoMaxWidth = 32.0
	logoMaxHeight = 16.0
	logoGap       = 5.0
	maxLogoBytes  = 5 << 20
)

// Options controls the generated document. Zero values produce a usable PDF.
type Options struct {
	// Title is displayed above the table. It defaults to "CSV export".
	Title string

	// Comma is the CSV field delimiter. A zero value means a comma.
	Comma rune

	// LogoBase64 is an optional PNG, JPEG, or GIF logo. It accepts either raw
	// base64 or a data URL such as "data:image/png;base64,...".
	LogoBase64 string
}

type logoImage struct {
	data   []byte
	format string
	width  int
	height int
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

	logo, err := decodeLogo(opts.LogoBase64)
	if err != nil {
		return fmt.Errorf("csvpdf: logo: %w", err)
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
	reader.FieldsPerRecord = len(header)
	doc, err := newDocument(opts, len(header), logo)
	if err != nil {
		return fmt.Errorf("csvpdf: create document: %w", err)
	}
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

func newDocument(opts Options, columns int, logo *logoImage) (*fpdf.Fpdf, error) {
	orientation := "P"
	if columns > 5 {
		orientation = "L"
	}

	doc := fpdf.New(orientation, "mm", "A4", "")
	doc.SetMargins(pageMargin, pageMargin, pageMargin)
	doc.SetAutoPageBreak(false, pageMargin)
	doc.SetTitle(title(opts), false)
	doc.AddPage()

	headingHeight := 10.0
	titleWidth := 0.0
	if logo != nil {
		logoWidth, logoHeight := fitDimensions(
		float64(logo.width),
		float64(logo.height),
		logoMaxWidth,
		logoMaxHeight,
		)
		imageOptions := fpdf.ImageOptions{ImageType: logo.format, ReadDpi: true}
		doc.RegisterImageOptionsReader("logo", imageOptions, bytes.NewReader(logo.data))
		if err := doc.Error(); err != nil {
			return nil, fmt.Errorf("register logo: %w", err)
		}

		pageWidth, _ := doc.GetPageSize()
		doc.ImageOptions(
			"logo",
			pageWidth-pageMargin-logoWidth,
			pageMargin,
			logoWidth,
			logoHeight,
			false,
			imageOptions,
			0,
			"",
		)
		headingHeight = max(headingHeight, logoHeight)
		titleWidth = pageWidth - 2*pageMargin - logoWidth - logoGap
	}

	doc.SetFont("Helvetica", "B", 16)
	doc.CellFormat(titleWidth, headingHeight, title(opts), "", 1, "L", false, 0, "")
	doc.Ln(2)
	return doc, nil
}

func decodeLogo(encoded string) (*logoImage, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, nil
	}
	if strings.HasPrefix(strings.ToLower(encoded), "data:") {
		metadata, payload, found := strings.Cut(encoded, ",")
		if !found || !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
			return nil, errors.New("data URL must contain base64 data")
		}
		encoded = strings.TrimSpace(payload)
	}
	if base64.StdEncoding.DecodedLen(len(encoded)) > maxLogoBytes {
		return nil, fmt.Errorf("decoded image exceeds %d bytes", maxLogoBytes)
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("image is empty")
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return &logoImage{
		data:   data,
		format: format,
		width:  config.Width,
		height: config.Height,
	}, nil
}

func fitDimensions(width, height, maxWidth, maxHeight float64) (float64, float64) {
	scale := min(1, maxWidth/width, maxHeight/height)
	return width * scale, height * scale
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
