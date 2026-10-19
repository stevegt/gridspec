package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIResponsesAdapter(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s %#v", request.URL.Path, request.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["store"] != false || body["model"] != "test-model" {
			t.Fatalf("request body = %#v", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"id":"resp_test","model":"test-model","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"generated"}]}]}`)),
		}, nil
	})}
	client, err := NewOpenAI(OpenAIOptions{APIKey: "secret", Model: "test-model", BaseURL: "https://api.example.test/v1", Client: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), Request{Instructions: "do it", Input: "input"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "generated" || response.ID != "resp_test" {
		t.Fatalf("response = %#v", response)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
