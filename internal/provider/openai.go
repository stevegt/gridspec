package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIOptions struct {
	APIKey  string
	Model   string
	BaseURL string
	Client  *http.Client
}

type OpenAI struct {
	apiKey, model, endpoint string
	client                  *http.Client
}

func NewOpenAI(options OpenAIOptions) (*OpenAI, error) {
	if options.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required for the OpenAI provider")
	}
	if options.Model == "" {
		options.Model = "gpt-5.4"
	}
	if options.BaseURL == "" {
		options.BaseURL = "https://api.openai.com/v1"
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	return &OpenAI{
		apiKey: options.APIKey, model: options.Model,
		endpoint: strings.TrimRight(options.BaseURL, "/") + "/responses", client: options.Client,
	}, nil
}

func (provider *OpenAI) Generate(ctx context.Context, request Request) (Response, error) {
	if provider == nil || request.Instructions == "" || request.Input == "" {
		return Response{}, fmt.Errorf("provider, instructions, and input are required")
	}
	maximum := request.MaxOutputTokens
	if maximum == 0 {
		maximum = 16_384
	}
	payload := struct {
		Model           string `json:"model"`
		Instructions    string `json:"instructions"`
		Input           string `json:"input"`
		MaxOutputTokens int    `json:"max_output_tokens"`
		Store           bool   `json:"store"`
	}{provider.model, request.Instructions, request.Input, maximum, false}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Response{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+provider.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "gridspec/1")
	httpResponse, err := provider.client.Do(httpRequest)
	if err != nil {
		return Response{}, err
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, 32<<20))
	if err != nil {
		return Response{}, err
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return Response{}, fmt.Errorf("OpenAI Responses API returned HTTP %d: %s", httpResponse.StatusCode, boundedMessage(body))
	}
	var result struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Error  any    `json:"error"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Response{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if result.Status != "completed" {
		return Response{}, fmt.Errorf("OpenAI response %s ended with status %q", result.ID, result.Status)
	}
	var text strings.Builder
	for _, item := range result.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
		}
	}
	if text.Len() == 0 {
		return Response{}, fmt.Errorf("OpenAI response %s contained no output text", result.ID)
	}
	return Response{Text: text.String(), Model: result.Model, ID: result.ID}, nil
}

func boundedMessage(value []byte) string {
	const maximum = 1024
	if len(value) > maximum {
		value = value[:maximum]
	}
	return strings.TrimSpace(string(value))
}
