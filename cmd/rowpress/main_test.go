package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vimutter/rowpress-lab/csvpdf"
)

func TestRunConvertsStandardStreams(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-title", "Scores"},
		strings.NewReader("name,score\nAda,10\n"),
		&stdout,
		&stderr,
	)

	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if !bytes.HasPrefix(stdout.Bytes(), []byte("%PDF-")) {
		t.Fatalf("stdout does not contain a PDF; prefix = %q", stdout.Bytes()[:min(8, stdout.Len())])
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunConvertsFiles(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "input.csv")
	outputPath := filepath.Join(directory, "output.pdf")
	if err := os.WriteFile(inputPath, []byte("name;score\nAda;10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-input", inputPath, "-output", outputPath, "-delimiter", ";"},
		strings.NewReader("unused"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(output, []byte("%PDF-")) {
		t.Fatalf("output file does not contain a PDF; prefix = %q", output[:min(8, len(output))])
	}
}

func TestRunRejectsInvalidDelimiter(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-delimiter", "||"},
		strings.NewReader("a\n1\n"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 2 {
		t.Fatalf("run() exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "exactly one character") {
		t.Fatalf("stderr = %q, want delimiter explanation", stderr.String())
	}
}

func TestRunReportsConversionError(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		nil,
		strings.NewReader("a,b\n1\n"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "record 2") {
		t.Fatalf("stderr = %q, want conversion error", stderr.String())
	}
}

func TestRunRejectsSameInputAndOutput(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-input", "data.csv", "-output", "data.csv"},
		strings.NewReader("unused"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 2 {
		t.Fatalf("run() exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "must be different") {
		t.Fatalf("stderr = %q, want same-file explanation", stderr.String())
	}
}

func TestRunHelpSucceeds(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-help"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(stderr.String(), "Usage: rowpress") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestMainUsesProcessArgumentsAndExitCode(t *testing.T) {
	originalArgs := os.Args
	originalStderr := os.Stderr
	originalExit := exitProcess
	t.Cleanup(func() {
		os.Args = originalArgs
		os.Stderr = originalStderr
		exitProcess = originalExit
	})

	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	os.Args = []string{"rowpress", "-help"}
	os.Stderr = stderr
	exitCode := -1
	exitProcess = func(code int) { exitCode = code }

	main()

	if exitCode != 0 {
		t.Fatalf("main() exit code = %d, want 0", exitCode)
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"data.csv"},
		strings.NewReader("unused"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 2 {
		t.Fatalf("run() exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "positional arguments") {
		t.Fatalf("stderr = %q, want positional-argument explanation", stderr.String())
	}
}

func TestRunReportsInputOpenError(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-input", filepath.Join(t.TempDir(), "missing.csv")},
		strings.NewReader("unused"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "open input") {
		t.Fatalf("stderr = %q, want input error", stderr.String())
	}
}

func TestRunReportsOutputOpenError(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "input.csv")
	if err := os.WriteFile(inputPath, []byte("a\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-input", inputPath, "-output", filepath.Join(directory, "missing", "output.pdf")},
		strings.NewReader("unused"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "open output") {
		t.Fatalf("stderr = %q, want output error", stderr.String())
	}
}

func TestRunReportsOutputCloseError(t *testing.T) {
	openOutput := func(string, io.Writer) (io.Writer, func() error, error) {
		return &bytes.Buffer{}, func() error { return errors.New("close failed") }, nil
	}
	var stderr bytes.Buffer

	exitCode := runWithOutputOpener(
		context.Background(),
		nil,
		strings.NewReader("a\n1\n"),
		&bytes.Buffer{},
		&stderr,
		openOutput,
	)

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "close output") {
		t.Fatalf("stderr = %q, want close error", stderr.String())
	}
}

func TestRunPassesLogoToConverter(t *testing.T) {
	logoPath := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(logoPath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-logo", logoPath},
		strings.NewReader("a\n1\n"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "logo") {
		t.Fatalf("stderr = %q, want logo error", stderr.String())
	}
}

func TestRunReportsLogoOpenError(t *testing.T) {
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-logo", filepath.Join(t.TempDir(), "missing.png")},
		strings.NewReader("a\n1\n"),
		&bytes.Buffer{},
		&stderr,
	)

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "load logo") {
		t.Fatalf("stderr = %q, want logo loading error", stderr.String())
	}
}

func TestLoadLogoRejectsUnreadableAndOversizedFiles(t *testing.T) {
	if _, err := loadLogo(t.TempDir()); err == nil {
		t.Fatal("loadLogo() error = nil for a directory")
	}

	logoPath := filepath.Join(t.TempDir(), "large-logo.png")
	if err := os.WriteFile(logoPath, make([]byte, csvpdf.MaxLogoBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLogo(logoPath); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("loadLogo() error = %v, want size error", err)
	}
}
