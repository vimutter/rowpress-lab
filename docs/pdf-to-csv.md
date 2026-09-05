# PDF to CSV with OpenAI

## CLI

Set the API key in the environment and run the reverse subcommand:

```sh
export OPENAI_API_KEY='your-project-key'
go run ./cmd/rowpress pdf-to-csv -input report.pdf -output recovered.csv
```

The default model is `gpt-5.6-luna`. Use `-model MODEL` for one invocation or
set `OPENAI_MODEL` to change the default. Standard streams work in both
directions:

```sh
cat report.pdf | go run ./cmd/rowpress pdf-to-csv > recovered.csv
```

Keep `OPENAI_API_KEY` out of source control. Set it where the CLI or web server
runs. For the deployed web app, add it to the Render service's environment and
restart/redeploy. `OPENAI_MODEL` optionally selects the server's model. GitHub
does not need the key for the automated tests.

## Web app

Choose **PDF → CSV table**, select a PDF up to 10 MiB, choose the output
delimiter, and click **Extract CSV**. The CSV downloads when extraction finishes.
You can switch directions and convert more files on the same WebSocket session.
Extraction has a three-minute deadline and is canceled when the connection closes.
Without a server API key, CSV → PDF remains available and PDF → CSV is disabled.
The key stays on the server; PDFs are sent to OpenAI for extraction.

## Conversion flow

1. `pdfcsv.Convert` reads at most 10 MiB and validates that the input looks like
   a PDF.
2. `OpenAIExtractor` sends the PDF inline as a Base64 `input_file` to the
   Responses API. No persistent Files API object is created.
3. The request uses low-detail page images to reduce image-token cost. Extracted
   PDF text is still included by the API.
4. Strict Structured Outputs returns `{ "rows": [[...], ...] }`.
5. Local validation checks the table shape and size before Go's `encoding/csv`
   writes correctly escaped CSV.

The request sets `store: false`. That controls response storage, but it should
not be read as a blanket data-retention guarantee; apply the OpenAI API data
controls appropriate to the documents being processed.

Official references:

- [OpenAI file inputs](https://developers.openai.com/api/docs/guides/file-inputs)
- [OpenAI Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs)
- [OpenAI model catalog](https://developers.openai.com/api/docs/models)

## What “same CSV” means

The reverse command reconstructs the same table values when the PDF visibly
contains them, then emits a canonical CSV representation. It cannot generally
reproduce the original bytes because a rendered PDF does not retain choices
such as unnecessary quoting, CRLF versus LF line endings, or source formatting.

More importantly, Rowpress currently truncates cells that do not fit their PDF
columns. Text that was never rendered cannot be recovered by any extractor.
For a truly lossless Rowpress-specific round trip, a later version can embed the
original CSV as a PDF attachment and prefer that payload, while retaining the
AI path for arbitrary PDFs.

## Tests

Automated tests use a local mock HTTP server and fake extractors. They verify
the complete request shape, strict schema, PDF Base64 data, API errors, table
validation, CSV escaping, and the CLI's CSV → PDF → CSV flow without sending
documents or consuming API credits.
