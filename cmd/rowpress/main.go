package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/vimutter/rowpress-lab/csvpdf"
	"github.com/vimutter/rowpress-lab/pdfcsv"
)

const stdioName = "-"

type conversionMode string

const (
	csvToPDF conversionMode = "csv-to-pdf"
	pdfToCSV conversionMode = "pdf-to-csv"
)

var exitProcess = os.Exit

type outputOpener func(string, io.Writer) (io.Writer, func() error, error)
type extractorFactory func(string, string) (pdfcsv.Extractor, error)

type config struct {
	mode       conversionMode
	inputPath  string
	outputPath string
	title      string
	delimiter  rune
	logoPath   string
	model      string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	exitProcess(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithDependencies(ctx, args, stdin, stdout, stderr, openOutput, newOpenAIExtractor)
}

func runWithOutputOpener(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	openOutputFile outputOpener,
) int {
	return runWithDependencies(ctx, args, stdin, stdout, stderr, openOutputFile, newOpenAIExtractor)
}

func runWithDependencies(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	openOutputFile outputOpener,
	newExtractor extractorFactory,
) int {
	cfg, err := parseFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	if cfg.inputPath != stdioName && cfg.inputPath == cfg.outputPath {
		fmt.Fprintln(stderr, "rowpress: input and output must be different files")
		return 2
	}

	src, closeInput, err := openInput(cfg.inputPath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "rowpress: open input: %v\n", err)
		return 1
	}
	defer closeInput()

	var logo []byte
	var extractor pdfcsv.Extractor
	if cfg.mode == csvToPDF {
		logo, err = loadLogo(cfg.logoPath)
		if err != nil {
			fmt.Fprintf(stderr, "rowpress: load logo: %v\n", err)
			return 1
		}
	} else {
		apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		if apiKey == "" {
			fmt.Fprintln(stderr, "rowpress: OPENAI_API_KEY is required for pdf-to-csv")
			return 1
		}
		extractor, err = newExtractor(apiKey, cfg.model)
		if err != nil {
			fmt.Fprintf(stderr, "rowpress: configure OpenAI: %v\n", err)
			return 1
		}
	}

	dst, closeOutput, err := openOutputFile(cfg.outputPath, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "rowpress: open output: %v\n", err)
		return 1
	}

	if cfg.mode == csvToPDF {
		err = csvpdf.Convert(ctx, dst, src, csvpdf.Options{
			Title: cfg.title,
			Comma: cfg.delimiter,
			Logo:  csvpdf.Logo{Data: logo},
		})
	} else {
		filename := "document.pdf"
		if cfg.inputPath != stdioName {
			filename = filepath.Base(cfg.inputPath)
		}
		err = pdfcsv.Convert(ctx, dst, src, extractor, pdfcsv.Options{
			Filename: filename,
			Comma:    cfg.delimiter,
		})
	}
	closeErr := closeOutput()
	if err != nil {
		fmt.Fprintln(stderr, "rowpress:", err)
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "rowpress: close output: %v\n", closeErr)
		return 1
	}

	return 0
}

func parseFlags(args []string, stderr io.Writer) (config, error) {
	cfg := config{mode: csvToPDF}
	if len(args) > 0 && (args[0] == string(csvToPDF) || args[0] == string(pdfToCSV)) {
		cfg.mode = conversionMode(args[0])
		args = args[1:]
	}
	var delimiter string

	flags := flag.NewFlagSet("rowpress", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if cfg.mode == csvToPDF {
		flags.StringVar(&cfg.inputPath, "input", stdioName, "CSV input file; use - for stdin")
		flags.StringVar(&cfg.outputPath, "output", stdioName, "PDF output file; use - for stdout")
		flags.StringVar(&cfg.title, "title", "", "document title")
		flags.StringVar(&cfg.logoPath, "logo", "", "PNG, JPEG, or GIF logo file")
		flags.StringVar(&delimiter, "delimiter", ",", "single-character input CSV delimiter")
	} else {
		flags.StringVar(&cfg.inputPath, "input", stdioName, "PDF input file; use - for stdin")
		flags.StringVar(&cfg.outputPath, "output", stdioName, "CSV output file; use - for stdout")
		flags.StringVar(&delimiter, "delimiter", ",", "single-character output CSV delimiter")
		model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
		if model == "" {
			model = pdfcsv.DefaultModel
		}
		flags.StringVar(&cfg.model, "model", model, "OpenAI model")
	}
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: rowpress %s [options]\n", cfg.mode)
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "rowpress: positional arguments are not supported")
		flags.Usage()
		return config{}, errors.New("unexpected positional arguments")
	}
	if utf8.RuneCountInString(delimiter) != 1 {
		fmt.Fprintln(stderr, "rowpress: delimiter must be exactly one character")
		return config{}, errors.New("invalid delimiter")
	}

	cfg.delimiter, _ = utf8.DecodeRuneInString(delimiter)
	return cfg, nil
}

func newOpenAIExtractor(apiKey, model string) (pdfcsv.Extractor, error) {
	return pdfcsv.NewOpenAIExtractor(pdfcsv.OpenAIOptions{APIKey: apiKey, Model: model})
}

func loadLogo(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, csvpdf.MaxLogoBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > csvpdf.MaxLogoBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", csvpdf.MaxLogoBytes)
	}
	return data, nil
}

func openInput(path string, stdin io.Reader) (io.Reader, func() error, error) {
	if path == stdioName {
		return stdin, func() error { return nil }, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return file, file.Close, nil
}

func openOutput(path string, stdout io.Writer) (io.Writer, func() error, error) {
	if path == stdioName {
		return stdout, func() error { return nil }, nil
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return file, file.Close, nil
}
