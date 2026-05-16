package main

import "time"

// OllamaTagsResponse represents the structure of a response from the /api/tags endpoint.
type OllamaTagsResponse struct {
	Models []OllamaTagModel `json:"models"`
}

// OllamaTagModel represents a single model entry in the /api/tags response.
type OllamaTagModel struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
	Digest     string    `json:"digest"`
	Details    struct {
		Format        string `json:"format"`
		Family        string `json:"family"`
		ParameterSize string `json:"parameter_size"`
	} `json:"details"`
}

// OllamaPSResponse represents the structure of a response from the /api/ps endpoint.
type OllamaPSResponse struct {
	Models []OllamaPSModel `json:"models"`
}

// OllamaPSModel represents a single running model entry in the /api/ps response.
type OllamaPSModel struct {
	Name              string `json:"name"`
	Size              int64  `json:"size"`
	SizeVRAM          int64  `json:"size_vram"`
	ExpiresAt         string `json:"expires_at"`
	Memory            int64  `json:"memory"`
	Details           any    `json:"details"`
	CPU               any    `json:"cpu,omitempty"`
	Uptime            any    `json:"uptime,omitempty"`
	QuantizationLevel string `json:"quantization_level,omitempty"`
}

// OllamaVersionResponse represents the structure of a response from the /api/version endpoint.
type OllamaVersionResponse struct {
	Version string `json:"version"`
}

// GenericOllamaRequest is a generic struct to extract the model name from various requests.
type GenericOllamaRequest struct {
	Model  string `json:"model"`
	Name   string `json:"name"`   // For pull, delete, show
	Source string `json:"source"` // For copy
}

// OpenAIModelsResponse is the response structure for the /v1/models endpoint.
type OpenAIModelsResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

// OpenAIModel represents a single model in the OpenAI-compatible API.
type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

