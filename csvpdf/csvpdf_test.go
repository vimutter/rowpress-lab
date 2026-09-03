package csvpdf_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
