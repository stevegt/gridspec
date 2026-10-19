// Package provider defines the model boundary used by GridSpec compilers and
// disassemblers. Providers propose bytes; they never receive signing keys and
// cannot advance accepted refs.
package provider

import (
	"context"
	"fmt"
	"os"
)

type Request struct {
	Operation       string
	Instructions    string
	Input           string
	MaxOutputTokens int
}

type Response struct {
	Text  string
	Model string
	ID    string
}

type Provider interface {
	Generate(context.Context, Request) (Response, error)
}

// FromEnvironment configures the supported hosted provider. Explicit
// injection remains the preferred path for tests and offline workflows.
func FromEnvironment() (Provider, error) {
	name := os.Getenv("GRIDSPEC_PROVIDER")
	if name == "" {
		name = "openai"
	}
	switch name {
	case "openai":
		return NewOpenAI(OpenAIOptions{
			APIKey: os.Getenv("OPENAI_API_KEY"), Model: os.Getenv("GRIDSPEC_MODEL"),
			BaseURL: os.Getenv("GRIDSPEC_OPENAI_BASE_URL"),
		})
	default:
		return nil, fmt.Errorf("unsupported GridSpec provider %q", name)
	}
}

// Static is an offline provider useful for deterministic tests and scripted
// review workflows.
type Static struct {
	Text string
	Err  error
}

func (provider Static) Generate(context.Context, Request) (Response, error) {
	return Response{Text: provider.Text, Model: "static", ID: "offline"}, provider.Err
}
