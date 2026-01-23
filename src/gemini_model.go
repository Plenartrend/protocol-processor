package main

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
)

const geminiModel string = "gemini-2.5-flash"

var geminiClient *genai.Client = nil // lazy initialized

type GeminiModel struct {
	systemInstruction *genai.Content
	responseSchema    genai.Schema
	client            *genai.Client
	model             string
}

func (gm *GeminiModel) Initialize(logger *Logger) error {
	gm.model = geminiModel
	gm.SetSystemInstruction(systemInstruction)
	gm.SetResponseSchema()
	client, err := gm.getGeminiClient() //Initialize the client
	if err != nil {
		logger.Error(fmt.Sprintf("failed to initialize Gemini client: %v", err))
		return fmt.Errorf("failed to initialize Gemini model: %w", err)
	}
	gm.client = client
	logger.Info("Gemini model initialized")
	return nil
}

func (gm *GeminiModel) SetSystemInstruction(systemInstruction string) {
	systemInstructionContent := &genai.Content{
		Parts: []*genai.Part{
			{Text: systemInstruction},
		},
	}
	gm.systemInstruction = systemInstructionContent
}

func (gm *GeminiModel) GenerateContent(query []string, logger *Logger) (*SpeechExtractionResponse, error) {
	var queryParts []*genai.Part = nil
	for _, q := range query {
		queryParts = append(queryParts, &genai.Part{Text: q})
	}
	queryContent := []*genai.Content{
		{
			Parts: queryParts,
		},
	}
	temperature := float32(0.1)
	thinkingBudget := int32(2048)
	resp, err := gm.client.Models.GenerateContent(context.Background(), geminiModel, queryContent, &genai.GenerateContentConfig{
		SystemInstruction:  gm.systemInstruction,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: gm.responseSchema,
		Temperature:        &temperature,
		ThinkingConfig:     &genai.ThinkingConfig{ThinkingBudget: &thinkingBudget},
	})
	if err != nil {
		logger.Error(fmt.Sprintf("failed to generate content: %v", err))
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}
	responseText := resp.Text()
	//logger.Debug(fmt.Sprintf("Gemini model response: %s", responseText))

	return ParseModelResponse(responseText, logger)
}

func (gm *GeminiModel) getGeminiClient() (*genai.Client, error) {
	var err error = nil
	if geminiClient == nil {
		geminiClient, err = genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:  os.Getenv("GEMINI_API_KEY"),
			Backend: genai.BackendGeminiAPI,
		})
	}
	return geminiClient, err
}

func (gm *GeminiModel) SetResponseSchema() {
	gm.responseSchema = GetExemplaryResponseSchema()
}
