package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ollama/ollama/api"
	"google.golang.org/genai"
)

const ollamaModel string = "llama3:latest"

const ollamaBaseURL string = "http://host.docker.internal:11434"

var ollamaClient *api.Client = nil // lazy initialized

//IMPORTANT: To connect to ollama you need to run sudo systemctl edit ollama.service
//and add the following line:
//Environment="OLLAMA_HOST=0.0.0.0"
//then run sudo systemctl daemon-reload && sudo systemctl restart ollama.service

type OllamaModel struct {
	systemInstruction string
	responseSchema    map[string]any
	client            *api.Client
	model             string
}

func (om *OllamaModel) Initialize(logger *Logger) error {
	om.model = ollamaModel
	om.SetSystemInstruction(systemInstruction)
	om.SetResponseSchema()
	client, err := om.getOllamaClient()
	if err != nil {
		logger.Error(fmt.Sprintf("failed to initialize Ollama client: %v", err))
		return fmt.Errorf("failed to initialize Ollama model: %w", err)
	}
	om.client = client
	logger.Info("Ollama model initialized")
	return nil
}

func (om *OllamaModel) getOllamaClient() (*api.Client, error) {
	var err error = nil
	if ollamaClient == nil {
		baseURL, err := url.Parse(ollamaBaseURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Ollama base URL: %w", err)
		}
		ollamaClient = api.NewClient(baseURL, &http.Client{})
	}
	return ollamaClient, err
}

func (om *OllamaModel) SetSystemInstruction(systemInstruction string) {
	om.systemInstruction = systemInstruction
}

func (om *OllamaModel) SetResponseSchema() {
	// Convert genai.Schema to JSON schema format for Ollama
	exemplarySchema := GetExemplaryResponseSchema()
	om.responseSchema = convertGenaiSchemaToJSONSchema(exemplarySchema)
}

func (om *OllamaModel) GenerateContent(query []string, logger *Logger) (*SpeechExtractionResponse, error) {
	// Combine query strings into a single user message
	userContent := ""
	for i, q := range query {
		if i > 0 {
			userContent += "\n"
		}
		userContent += q
	}

	// Create format as json.RawMessage from the schema
	formatBytes, err := json.Marshal(om.responseSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response schema: %w", err)
	}

	stream := false
	req := &api.ChatRequest{
		Model: om.model,
		Messages: []api.Message{
			{
				Role:    "system",
				Content: om.systemInstruction,
			},
			{
				Role:    "user",
				Content: userContent,
			},
		},
		Format: json.RawMessage(formatBytes),
		Stream: &stream,
		Options: map[string]any{
			"temperature": 0.1,
		},
		Think: &api.ThinkValue{Value: false},
	}

	var responseText string
	err = om.client.Chat(context.Background(), req, func(resp api.ChatResponse) error {
		responseText += resp.Message.Content
		return nil
	})
	if err != nil {
		logger.Error(fmt.Sprintf("failed to generate content: %v", err))
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	logger.Debug(fmt.Sprintf("Ollama model response: %s", responseText))
	return ParseModelResponse(responseText, logger)
}

// convertGenaiSchemaToJSONSchema converts a genai.Schema to Ollama's JSON schema format (map[string]any)
func convertGenaiSchemaToJSONSchema(schema genai.Schema) map[string]any {
	result := make(map[string]any)

	// Convert type
	if schema.Type != "" {
		result["type"] = schema.Type
	}

	// Convert description
	if schema.Description != "" {
		result["description"] = schema.Description
	}

	// Convert required fields
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}

	// Convert properties
	if len(schema.Properties) > 0 {
		properties := make(map[string]any)
		for key, prop := range schema.Properties {
			if prop != nil {
				properties[key] = convertGenaiSchemaProperty(prop)
			}
		}
		result["properties"] = properties
	}

	// Convert items (for arrays)
	if schema.Items != nil {
		result["items"] = convertGenaiSchemaProperty(schema.Items)
	}

	return result
}

// convertGenaiSchemaProperty converts a single genai.Schema property to JSON schema format
func convertGenaiSchemaProperty(schema *genai.Schema) map[string]any {
	result := make(map[string]any)

	if schema.Type != "" {
		result["type"] = schema.Type
	}

	if schema.Description != "" {
		result["description"] = schema.Description
	}

	// Convert required fields
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}

	// Convert properties (for nested objects)
	if len(schema.Properties) > 0 {
		properties := make(map[string]any)
		for key, prop := range schema.Properties {
			if prop != nil {
				properties[key] = convertGenaiSchemaProperty(prop)
			}
		}
		result["properties"] = properties
	}

	// Convert items (for arrays)
	if schema.Items != nil {
		result["items"] = convertGenaiSchemaProperty(schema.Items)
	}

	return result
}
