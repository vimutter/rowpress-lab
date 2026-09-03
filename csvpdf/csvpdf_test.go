package csvpdf_test

import (
	"bytes"
	"context"
	"errors"
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

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("disk full")
}
