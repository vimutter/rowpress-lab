package csvpdf_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/vimutter/rowpress-lab/csvpdf"
)

func TestConvertWritesPDF(t *testing.T) {
	input := strings.NewReader("name,score\nAda,10\nLinus,9\n")
	var output bytes.Buffer

	err := csvpdf.Convert(context.Background(), &output, input, csvpdf.Options{Title: "Scores"})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte("%PDF-")) {
		t.Fatalf("output does not start with a PDF signature: %q", output.Bytes()[:min(8, output.Len())])
	}
	if output.Len() < 500 {
		t.Fatalf("output is unexpectedly small: %d bytes", output.Len())
	}
}

func TestConvertEmbedsUnicodeFont(t *testing.T) {
	input := strings.NewReader("Город,Χώρα,Status\nБерлин,Γερμανία,✓ готово\n")
	var output bytes.Buffer

	err := csvpdf.Convert(
		context.Background(),
		&output,
		input,
		csvpdf.Options{Title: "Отчёт — Αναφορά"},
	)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte("/ToUnicode")) {
		t.Fatal("PDF does not contain an embedded Unicode character map")
	}
}

func TestConvertRejectsEmptyInput(t *testing.T) {
	var output bytes.Buffer

	err := csvpdf.Convert(context.Background(), &output, strings.NewReader(""), csvpdf.Options{})
	if err == nil || !strings.Contains(err.Error(), "CSV input is empty") {
		t.Fatalf("Convert() error = %v, want empty-input error", err)
	}
}

func TestConvertReportsInconsistentRecords(t *testing.T) {
	var output bytes.Buffer

	err := csvpdf.Convert(context.Background(), &output, strings.NewReader("a,b\n1\n"), csvpdf.Options{})
	if err == nil || !strings.Contains(err.Error(), "record 2") {
		t.Fatalf("Convert() error = %v, want record 2 error", err)
	}
}

func TestConvertHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := csvpdf.Convert(ctx, &bytes.Buffer{}, strings.NewReader("a\n1\n"), csvpdf.Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Convert() error = %v, want context.Canceled", err)
	}
}

func TestConvertReportsWriteFailure(t *testing.T) {
	err := csvpdf.Convert(context.Background(), failingWriter{}, strings.NewReader("a\n1\n"), csvpdf.Options{})
	if err == nil || !strings.Contains(err.Error(), "write PDF") {
		t.Fatalf("Convert() error = %v, want write error", err)
	}
}

func TestConvertRejectsNilArguments(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		dst  io.Writer
		src  io.Reader
	}{
		{name: "context", ctx: nil, dst: &bytes.Buffer{}, src: strings.NewReader("a\n")},
		{name: "destination", ctx: context.Background(), dst: nil, src: strings.NewReader("a\n")},
		{name: "source", ctx: context.Background(), dst: &bytes.Buffer{}, src: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := csvpdf.Convert(test.ctx, test.dst, test.src, csvpdf.Options{}); err == nil {
				t.Fatal("Convert() error = nil, want validation error")
			}
		})
	}
}

func TestConvertReportsMalformedHeader(t *testing.T) {
	err := csvpdf.Convert(
		context.Background(),
		&bytes.Buffer{},
		strings.NewReader("\"unterminated"),
		csvpdf.Options{},
	)
	if err == nil || !strings.Contains(err.Error(), "read header") {
		t.Fatalf("Convert() error = %v, want header error", err)
	}
}

func TestConvertPaginatesWideDelimitedInputAndTruncatesText(t *testing.T) {
	var input strings.Builder
	input.WriteString("one;two;three;four;five;six\n")
	for row := 0; row < 100; row++ {
		fmt.Fprintf(&input, "%d;short;short;short;short;%s\n", row, strings.Repeat("wide", 100))
	}
	var output bytes.Buffer

	err := csvpdf.Convert(
		context.Background(),
		&output,
		strings.NewReader(input.String()),
		csvpdf.Options{Comma: ';'},
	)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte("%PDF-")) {
		t.Fatal("output does not contain a PDF")
	}
}

func TestConvertNoticesCancellationAtEndOfInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &cancelAtEOFReader{
		reader: strings.NewReader("a\n1\n"),
		cancel: cancel,
	}

	err := csvpdf.Convert(ctx, &bytes.Buffer{}, src, csvpdf.Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Convert() error = %v, want context.Canceled", err)
	}
}

func TestConvertEmbedsRawBase64AndDataURLLogos(t *testing.T) {
	pngData := testPNG(t)
	encoded := base64.StdEncoding.EncodeToString(pngData)
	tests := []struct {
		name string
		logo csvpdf.Logo
	}{
		{name: "binary data", logo: csvpdf.Logo{Data: pngData}},
		{name: "raw base64", logo: csvpdf.Logo{Base64: encoded}},
		{name: "data URL", logo: csvpdf.Logo{Base64: "data:image/png;base64, " + encoded}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := csvpdf.Convert(
				context.Background(),
				&output,
				strings.NewReader("name,score\nAda,10\n"),
				csvpdf.Options{Title: "Scores", Logo: test.logo},
			)
			if err != nil {
				t.Fatalf("Convert() error = %v", err)
			}
			if !bytes.Contains(output.Bytes(), []byte("/Subtype /Image")) {
				t.Fatal("PDF does not contain an image object")
			}
		})
	}
}

func TestConvertRejectsInvalidLogos(t *testing.T) {
	pngData := testPNG(t)
	oversizedPNG := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 4097, 1)))
	tests := []struct {
		name string
		logo csvpdf.Logo
		want string
	}{
		{name: "both representations", logo: csvpdf.Logo{Data: pngData, Base64: "abc"}, want: "either Data or Base64"},
		{name: "binary data too large", logo: csvpdf.Logo{Data: make([]byte, csvpdf.MaxLogoBytes+1)}, want: "exceeds"},
		{name: "data URL without comma", logo: csvpdf.Logo{Base64: "data:image/png;base64"}, want: "data URL"},
		{name: "data URL without base64 marker", logo: csvpdf.Logo{Base64: "data:image/png,abc"}, want: "data URL"},
		{name: "base64 too large", logo: csvpdf.Logo{Base64: strings.Repeat("A", 7_000_000)}, want: "exceeds"},
		{name: "malformed base64", logo: csvpdf.Logo{Base64: "not!base64"}, want: "decode base64"},
		{name: "empty image", logo: csvpdf.Logo{Base64: "data:image/png;base64,"}, want: "image is empty"},
		{name: "unsupported image data", logo: csvpdf.Logo{Data: []byte("hello")}, want: "decode image"},
		{name: "oversized dimensions", logo: csvpdf.Logo{Data: oversizedPNG}, want: "dimensions exceed"},
		{name: "truncated PNG", logo: csvpdf.Logo{Data: pngData[:33]}, want: "decode complete image"},
		{name: "unsupported interlaced PNG", logo: csvpdf.Logo{Base64: interlacedPNGBase64}, want: "register logo"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := csvpdf.Convert(
				context.Background(),
				&bytes.Buffer{},
				strings.NewReader("a\n1\n"),
				csvpdf.Options{Logo: test.logo},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Convert() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

// A tiny valid interlaced PNG. Go's image decoder accepts interlacing, while
// FPDF deliberately rejects it.
const interlacedPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAQBAAAAAFAA5f8AAAABGdBTUEAAYagMeiWXwAAAAJiS0dEAA86Mj6jAAAACXBIWXMAAABIAAAASABGyWs+AAAAKUlEQVQI12NgYGhgcGA4wKDAkMCwgOEBgwCDAUMAQwHDBIYNDBcYPgAAYyAHgUS6c3EAAAAldEVYdGRhdGU6Y3JlYXRlADIwMTUtMDctMTJUMjA6NDc6NDYrMTA6MDASWXNCAAAAJXRFWHRkYXRlOm1vZGlmeQAyMDE1LTA3LTEyVDIwOjQ3OjE1KzEwOjAwGgzfBwAAAABJRU5ErkJggg=="

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("disk full")
}

type cancelAtEOFReader struct {
	reader *strings.Reader
	cancel context.CancelFunc
}

func (reader *cancelAtEOFReader) Read(destination []byte) (int, error) {
	if reader.reader.Len() == 0 {
		reader.cancel()
		return 0, io.EOF
	}
	return reader.reader.Read(destination)
}

func testPNG(t *testing.T) []byte {
	t.Helper()

	logo := image.NewRGBA(image.Rect(0, 0, 100, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			logo.Set(x, y, color.RGBA{R: 35, G: 99, B: 180, A: 255})
		}
	}

	return encodePNG(t, logo)
}

func encodePNG(t *testing.T, source image.Image) []byte {
	t.Helper()

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return encoded.Bytes()
}
