package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// ChatGPTResponse represents a simulated response from ChatGPT.
type ChatGPTResponse struct {
	Text string
}

// callChatGPTAPI simulates a call to ChatGPT's API.
// Replace this function with actual API calls (using an OpenAI client) in production.
func callChatGPTAPI(ctx context.Context, prompt string) (ChatGPTResponse, error) {
	time.Sleep(2 * time.Second)
	// For simulation: generate a simple prompt based on keywords.
	if strings.Contains(strings.ToLower(prompt), "castle") {
		return ChatGPTResponse{Text: "Dramatic echoing footsteps with a distant eerie wind"}, nil
	}
	return ChatGPTResponse{Text: "Deep, rumbling thunder with crackling fire"}, nil
}

// generateSoundPrompt sends a page's text to ChatGPT and returns a concise sound effect prompt.
func generateSoundPrompt(pageText string) (string, error) {
	if pageText == "" {
		return "", errors.New("page text is empty")
	}
	chatPrompt := fmt.Sprintf("Based on the following text, generate a concise sound effect prompt for an audiobook (only output the prompt, no commentary):\n%s", pageText)
	response, err := callChatGPTAPI(context.Background(), chatPrompt)
	if err != nil {
		return "", err
	}
	return response.Text, nil
}

// splitTextIntoPages splits a file's content into pages using double newline as a delimiter.
func splitTextIntoPages(filePath string) ([]string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	pages := strings.Split(string(content), "\n\n")
	return pages, nil
}

// mergePrompts concatenates multiple sound effect prompts into one overall prompt.
func mergePrompts(prompts []string) string {
	return strings.Join(prompts, "; ")
}

// generateOverallSoundPrompt generates an overall sound prompt for the book by processing each page.
func generateOverallSoundPrompt(filePath string) (string, error) {
	pages, err := splitTextIntoPages(filePath)
	if err != nil {
		return "", err
	}
	var prompts []string
	for _, page := range pages {
		trimmed := strings.TrimSpace(page)
		if trimmed == "" {
			continue
		}
		prompt, err := generateSoundPrompt(trimmed)
		if err != nil {
			log.Printf("Error generating prompt for page: %v", err)
			continue
		}
		prompts = append(prompts, prompt)
	}
	if len(prompts) == 0 {
		return "", errors.New("no prompts generated")
	}
	overallPrompt := mergePrompts(prompts)
	return overallPrompt, nil
}
