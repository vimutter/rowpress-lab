package pdfcsv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// DefaultModel balances extraction quality, latency, and cost for this demo.
const DefaultModel = "gpt-5.6-luna"

const extractionPrompt = `Extract the primary CSV-style table from this PDF.
- Return the column header as the first row, followed by every data row in visual order.
- Ignore the document title, logo, page numbers, and table headers repeated after page breaks.
- Preserve visible cell text exactly. Do not calculate, summarize, infer, or invent missing text.
- If a displayed cell is truncated, return only the visible text.
- If there is no table, return an empty rows array.`

// OpenAIOptions configures an OpenAI-backed Extractor.
type OpenAIOptions struct {
	APIKey string
	Model  string

	// BaseURL and HTTPClient are primarily useful for gateways and tests.
	BaseURL    string
	HTTPClient *http.Client
}

// OpenAIExtractor extracts tables with the OpenAI Responses API.
type OpenAIExtractor struct {
	client *openai.Client
	model  string
}

// NewOpenAIExtractor constructs an OpenAI-backed table extractor.
func NewOpenAIExtractor(opts OpenAIOptions) (*OpenAIExtractor, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, errors.New("pdfcsv: OpenAI API key is required")
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultModel
	}

	requestOptions := []option.RequestOption{option.WithAPIKey(opts.APIKey)}
	if opts.BaseURL != "" {
		requestOptions = append(requestOptions, option.WithBaseURL(opts.BaseURL))
	}
	if opts.HTTPClient != nil {
		requestOptions = append(requestOptions, option.WithHTTPClient(opts.HTTPClient))
	}
	client := openai.NewClient(requestOptions...)
	return &OpenAIExtractor{client: &client, model: model}, nil
}

// Extract sends the PDF and extraction schema to the Responses API.
func (extractor *OpenAIExtractor) Extract(ctx context.Context, pdf []byte, filename string) ([][]string, error) {
	fileData := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdf)
	response, err := extractor.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: shared.ResponsesModel(extractor.model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam{
				responses.ResponseInputItemParamOfMessage(
					responses.ResponseInputMessageContentListParam{
						{
							OfInputFile: &responses.ResponseInputFileParam{
								Filename: openai.String(filename),
								FileData: openai.String(fileData),
								Detail:   responses.ResponseInputFileDetailLow,
							},
						},
						responses.ResponseInputContentParamOfInputText(extractionPrompt),
					},
					responses.EasyInputMessageRoleUser,
				),
			},
		},
		MaxOutputTokens: openai.Int(32_768),
		Store:           openai.Bool(false),
		Text: responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:   "csv_table",
					Schema: tableSchema(),
					Strict: openai.Bool(true),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI Responses API: %w", err)
	}
	output := response.OutputText()
	if output == "" {
		return nil, errors.New("OpenAI response contained no table output")
	}

	var table struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(output), &table); err != nil {
		return nil, fmt.Errorf("decode OpenAI table output: %w", err)
	}
	return table.Rows, nil
}

func tableSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"rows": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
		},
		"required":             []string{"rows"},
		"additionalProperties": false,
	}
}
