package pdfcsv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIExtractorSendsPDFAndDecodesRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("request = %s %s, authorization = %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != DefaultModel || body["store"] != false {
			t.Fatalf("model/store = %v/%v", body["model"], body["store"])
		}
		input := body["input"].([]any)[0].(map[string]any)
		content := input["content"].([]any)
		file := content[0].(map[string]any)
		if file["filename"] != "report.pdf" || file["detail"] != "low" {
			t.Fatalf("file metadata = %#v", file)
		}
		encoded := strings.TrimPrefix(file["file_data"].(string), "data:application/pdf;base64,")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(decoded) != "%PDF-demo" {
			t.Fatalf("decoded PDF = %q, %v", decoded, err)
		}
		text := body["text"].(map[string]any)
		format := text["format"].(map[string]any)
		if format["type"] != "json_schema" || format["strict"] != true || format["schema"] == nil {
			t.Fatalf("structured output = %#v", format)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
          "id":"resp_test","object":"response","created_at":0,"status":"completed","model":"test-model",
          "output":[{"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[
            {"type":"output_text","text":"{\"rows\":[[\"name\",\"score\"],[\"Ada\",\"10\"]]}","annotations":[]}
          ]}]
        }`))
	}))
	defer server.Close()

	extractor, err := NewOpenAIExtractor(OpenAIOptions{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/v1/",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := extractor.Extract(context.Background(), []byte("%PDF-demo"), "report.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "Ada" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestNewOpenAIExtractorValidatesKeyAndAcceptsModel(t *testing.T) {
	if _, err := NewOpenAIExtractor(OpenAIOptions{}); err == nil {
		t.Fatal("empty API key accepted")
	}
	extractor, err := NewOpenAIExtractor(OpenAIOptions{APIKey: "key", Model: " custom-model "})
	if err != nil || extractor.model != "custom-model" {
		t.Fatalf("extractor = %#v, %v", extractor, err)
	}
}

func TestOpenAIExtractorReportsAPIAndOutputErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
		want       string
	}{
		{name: "API", statusCode: http.StatusBadRequest, response: `{"error":{"message":"bad PDF","type":"invalid_request_error"}}`, want: "Responses API"},
		{name: "empty output", statusCode: http.StatusOK, response: `{"id":"r","object":"response","status":"completed","output":[]}`, want: "no table output"},
		{name: "invalid JSON", statusCode: http.StatusOK, response: responseWithText("not JSON"), want: "decode OpenAI"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			extractor, err := NewOpenAIExtractor(OpenAIOptions{
				APIKey: "key", BaseURL: server.URL + "/v1/", HTTPClient: server.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = extractor.Extract(context.Background(), []byte("%PDF"), "test.pdf")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Extract() error = %v, want %q", err, test.want)
			}
		})
	}
}

func responseWithText(text string) string {
	encoded, _ := json.Marshal(text)
	return `{"id":"r","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + string(encoded) + `}]}]}`
}
