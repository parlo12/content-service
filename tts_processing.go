package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// openaiTTSEndpoint is the endpoint for generating audio from text using OpenAI's TTS.
const openaiTTSEndpoint = "https://api.openai.com/v1/audio/speech"

// TTSPayload represents the JSON payload for the OpenAI TTS endpoint.
type TTSPayload struct {
	Input          string  `json:"input"`                     // SSML input
	Model          string  `json:"model"`                     // e.g. "gpt-4o-mini-tts"
	Voice          string  `json:"voice"`                     // e.g. "alloy"
	Instructions   string  `json:"instructions,omitempty"`    // must mention SSML
	ResponseFormat string  `json:"response_format,omitempty"` // "mp3"
	Speed          float64 `json:"speed,omitempty"`
}

// generateSSML wraps plain text in expressive SSML (breaks, emphasis, prosody).
// It asks GPT to produce a single <speak>…</speak> block.
func generateSSML(rawText string) (string, error) {
	systemContent := `You are an expressive audiobook narrator.
Convert this into SSML:
- Use <break time="500ms"/> at natural pauses
- Wrap key phrases in <emphasis>
- Use <prosody rate="80%">…</prosody> for sad passages
- Use <prosody rate="110%">…</prosody> for action passages
Output only the SSML wrapped in one <speak>…</speak> block.`

	reqBody := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: systemContent},
			{Role: "user", Content: rawText},
		},
		Temperature: 0.7,
		MaxTokens:   1500,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GPT SSML call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("GPT SSML returned %d: %s", resp.StatusCode, b)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode SSML JSON: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", errors.New("no SSML choices returned")
	}

	ssml := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if !strings.HasPrefix(ssml, "<speak") {
		ssml = "<speak>" + ssml + "</speak>"
	}
	return ssml, nil
}

// convertTextToAudio turns plain text into SSML via GPT, then into MP3 via OpenAI TTS.
func convertTextToAudio(text string) (string, error) {
	ssml, err := generateSSML(text)
	if err != nil {
		return "", fmt.Errorf("SSML generation failed: %w", err)
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	payload := TTSPayload{
		Input:          ssml,
		Model:          "gpt-4o-mini-tts",
		Voice:          "alloy",
		Instructions:   "Interpret the input as SSML.",
		ResponseFormat: "mp3",
		Speed:          1.0,
	}
	reqBody, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", openaiTTSEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create TTS request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("TTS API request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("TTS API returned %d: %s", resp.StatusCode, body)
	}

	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("audio_%d.mp3", time.Now().Unix())
	path := "./audio/" + filename

	outFile, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create audio file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return "", fmt.Errorf("write audio: %w", err)
	}
	return path, nil
}

// processBookConversion reads the book, TTS-converts it and kicks off sound-effects.
func processBookConversion(book Book) {
	contentBytes, err := os.ReadFile(book.FilePath)
	if err != nil {
		log.Printf("Error reading file for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	bookText := string(contentBytes)

	ttsPath, err := convertTextToAudio(bookText)
	if err != nil {
		log.Printf("Error converting text to audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("TTS audio file generated at: %s for book ID %d", ttsPath, book.ID)

	book.AudioPath = ttsPath
	book.Status = "TTS completed"
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record for book ID %d: %v", book.ID, err)
		return
	}

	go processSoundEffectsAndMerge(book)
}

// updateBookStatus updates the status of a book in the database.
func updateBookStatus(bookID uint, status string) {
	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		log.Printf("Error finding book with ID %d: %v", bookID, err)
		return
	}
	book.Status = status
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating status for book ID %d: %v", bookID, err)
	}
}
