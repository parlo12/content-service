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

	"gorm.io/gorm"
)

// wrapSSML ensures we always send a single <speak>…</speak> block
func wrapSSML(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "<speak") {
		return t
	}
	return "<speak>\n" + t + "\n</speak>"
}

// openaiTTSEndpoint is the endpoint for generating audio from text using OpenAI's TTS.
const openaiTTSEndpoint = "https://api.openai.com/v1/audio/speech"

// TTSPayload represents the JSON payload for the OpenAI TTS endpoint.
type TTSPayload struct {
	Input          string  `json:"input"`                     // SSML input
	InputFormat    string  `json:"input_format,omitempty"`    // "ssml or text"
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

	// ssml := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// if !strings.HasPrefix(ssml, "<speak") {
	// 	ssml = "<speak>" + ssml + "</speak>"
	// }
	// return ssml, nil

	// clean out any markdown fences that GPT might wrap it in
	raw := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	raw = strings.ReplaceAll(raw, "```", "")
	raw = strings.ReplaceAll(raw, "```ssml", "")
	raw = strings.ReplaceAll(raw, "```xml", "")
	raw = strings.TrimPrefix(raw, "```xml")
	raw = strings.ReplaceAll(raw, "```xml ssml", "")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	// ensure a single <speak>…</speak> block
	ssml := wrapSSML(raw)
	log.Printf("SSML: %s", ssml)
	return ssml, nil
}

// convertTextToAudio turns plain text into SSML via GPT, then into MP3 via OpenAI TTS.
func convertTextToAudio(text string) (string, error) {
	ssml, err := generateSSML(text)
	if err != nil {
		return "", fmt.Errorf("SSML generation failed: %w", err)
	}
	// ensure all breaks/emphasis/etc. are inside a single <speak>…</speak> block
	ssml = wrapSSML(ssml)
	log.Printf("SSML: %s", ssml)

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	payload := TTSPayload{
		Input:          ssml,
		Model:          "gpt-4o-mini-tts",
		Voice:          "alloy",
		Instructions:   "Interpret the input as SSML: apply breaks, prosody and emphasis tags but do not speak them.",
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
	// increase timeout to 120 seconds for longer books
	client := &http.Client{Timeout: 120 * time.Second}
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
	// 0) if another user already processed the same title+author, just reuse that audio:
	var dup Book
	err := db.
		Where("title = ? AND author = ? AND audio_path IS NOT NULL AND audio_path <> ''",
			book.Title, book.Author).
		First(&dup).Error
	if err == nil {
		log.Printf("Reusing existing audio for '%s' by %s → %s", book.Title, book.Author, dup.AudioPath)
		book.AudioPath = dup.AudioPath
		book.Status = "TTS reused"
		if err := db.Save(&book).Error; err != nil {
			log.Printf("Error saving reused audio for book ID %d: %v", book.ID, err)
		}
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		// some other DB error
		log.Printf("Error checking for existing audio: %v", err)
	}

	// 1) Check file exists...
	if _, err := os.Stat(book.FilePath); os.IsNotExist(err) {
		log.Printf("File does not exist for book ID %d: %s", book.ID, book.FilePath)
		updateBookStatus(book.ID, "failed")
		return
	}

	// 2) Read the file content
	contentBytes, err := os.ReadFile(book.FilePath)
	if err != nil {
		log.Printf("Error reading file for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}

	// 3) Generate TTS
	ttsPath, err := convertTextToAudio(string(contentBytes))
	if err != nil {
		log.Printf("Error converting text to audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("TTS audio file generated at: %s for book ID %d", ttsPath, book.ID)

	// 4) Save and mark complete
	book.AudioPath = ttsPath
	book.Status = "TTS completed"
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record for book ID %d: %v", book.ID, err)
		return
	}

	// 5) Kick off SFX merge
	go processSoundEffectsAndMerge(book)
}

// adding this comment to check if my deployment works

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
