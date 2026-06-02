package agent

import (
	"github.com/kez/livie/config"
	openai "github.com/sashabaranov/go-openai"
)

// newClient constructs an openai.Client for the given endpoint.
//
// The go-openai library sends "Authorization: Bearer <key>" on every request.
// When APIKey is empty (e.g. local llama-server), the bearer value is empty —
// llama-server and most local servers accept this without complaint.
func newClient(ep config.EndpointConfig) *openai.Client {
	oc := openai.DefaultConfig(ep.APIKey)
	if ep.BaseURL != "" {
		oc.BaseURL = ep.BaseURL
	}
	return openai.NewClientWithConfig(oc)
}

// modelName returns the model string to send in the request.
// Falls back to "default" when EndpointConfig.Model is empty so the request
// remains valid on servers that ignore the model field (e.g. llama-server).
func modelName(ep config.EndpointConfig) string {
	if ep.Model != "" {
		return ep.Model
	}
	return "default"
}
