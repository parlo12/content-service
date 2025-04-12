// chat_prompt.go
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
)

// ChatMessage represents an individual message for the ChatGPT API.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents the payload for the chat completions endpoint.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float32       `json:"temperature"`
}

// ChatChoice represents one choice in the chat completion response.
type ChatChoice struct {
	Message ChatMessage `json:"message"`
}

// ChatResponse represents the response payload from the chat completions endpoint.
type ChatResponse struct {
	Choices []ChatChoice `json:"choices"`
}

// generateOverallSoundPrompt reads the text from the given book file path,
// constructs a prompt instructing ChatGPT to suggest background music that fits the content,
// and returns the generated prompt.
func generateOverallSoundPrompt(bookFilePath string) (string, error) {
	// Read the book text
	content, err := os.ReadFile(bookFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to read book file: %w", err)
	}
	bookText := string(content)

	// Construct a detailed prompt for ChatGPT.
	// This prompt instructs ChatGPT to analyze the book excerpt and generate a background music prompt.
	promptMessage := fmt.Sprintf(`Analyze the following excerpt from an audiobook and generate a concise background music prompt that evokes a theatrical and immersive atmosphere for listeners. The prompt should recommend instrumentation, mood, and musical style that will complement the narration.
---
%s
---
Provide only the music prompt without additional commentary.`, bookText)

	// Prepare the chat completions request payload.
	chatReq := ChatRequest{
		Model: "gpt-3.5-turbo",
		Messages: []ChatMessage{
			// The system message can define the behavior of the assistant.
			{Role: "system", Content: "You are a creative audio production assistant that generates music prompts for audiobooks."},
			// The user message contains the prompt to analyze the book text.
			{Role: "user", Content: promptMessage},
		},
		MaxTokens:   60,
		Temperature: 0.7,
	}

	reqBody, err := json.Marshal(chatReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal chat request: %w", err)
	}

	// Retrieve the OpenAI API key from the environment.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY environment variable not set")
	}

	// Create an HTTP POST request to the ChatGPT completions endpoint.
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create chat completions request: %w", err)
	}
	req.Header.Add("Authorization", "Bearer "+apiKey)
	req.Header.Add("Content-Type", "application/json")

	// Use an HTTP client with a timeout.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat completions API request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("chat completions API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Decode the response.
	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("failed to decode chat completions response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", errors.New("no completions returned")
	}

	// Extract and trim the generated prompt.
	overallPrompt := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	return overallPrompt, nil
}
