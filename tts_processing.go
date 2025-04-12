package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// openaiTTSEndpoint is the endpoint for generating audio from text using OpenAI's TTS.
const openaiTTSEndpoint = "https://api.openai.com/v1/audio/speech"

// TTSPayload represents the JSON payload for the OpenAI TTS endpoint.
type TTSPayload struct {
	Input          string  `json:"input"`                     // The text to generate audio for.
	Model          string  `json:"model"`                     // One of "tts-1", "tts-1-hd", or "gpt-4o-mini-tts".
	Voice          string  `json:"voice"`                     // One of "alloy", "ash", "ballad", "coral", "echo", "fable", "onyx", "nova", "sage", "shimmer", or "verse".
	Instructions   string  `json:"instructions,omitempty"`    // Optional instructions to control speech (e.g., tone, pace).
	ResponseFormat string  `json:"response_format,omitempty"` // Desired output format: "mp3", "opus", "aac", "flac", "wav", or "pcm". Default is "mp3".
	Speed          float64 `json:"speed,omitempty"`           // Playback speed (0.25 - 4.0, default is 1.0).
}

// convertTextToAudio sends a request to the OpenAI Speech API to generate audio
// from the input text and saves it to a file. It returns the file path.
func convertTextToAudio(text string) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY environment variable not set")
	}

	// Prepare the payload using the new OpenAI Speech API specification.
	payload := TTSPayload{
		Input:          text,
		Model:          "gpt-4o-mini-tts", // or "tts-1", "tts-1-hd" depending on your quality/latency needs.
		Voice:          "alloy",           // Choose your preferred voice.
		ResponseFormat: "mp3",             // Use MP3 for a good balance of quality and file size.
		Speed:          1.0,
		// Instructions can be added if needed, e.g., "Speak in a cheerful tone."
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal TTS payload: %w", err)
	}

	req, err := http.NewRequest("POST", openaiTTSEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create TTS request: %w", err)
	}
	// Set the Authorization header with your API key.
	req.Header.Add("Authorization", "Bearer "+apiKey)
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("TTS API request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("TTS API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Ensure the audio directory exists.
	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	// Generate a unique filename.
	audioFileName := fmt.Sprintf("audio_%d.mp3", time.Now().Unix())
	audioPath := "./audio/" + audioFileName

	outFile, err := os.Create(audioPath)
	if err != nil {
		return "", fmt.Errorf("failed to create audio file: %w", err)
	}
	defer outFile.Close()

	// Stream the entire binary response into the file.
	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write audio to file: %w", err)
	}

	return audioPath, nil
}

// processBookConversion processes a book by reading its text,
// generating TTS audio using the OpenAI Speech API, updating the database,
// and then triggering sound effects merging asynchronously.
func processBookConversion(book Book) {
	// Read the text from the book file (assuming it's a TXT file).
	contentBytes, err := os.ReadFile(book.FilePath)
	if err != nil {
		log.Printf("Error reading file for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	bookText := string(contentBytes)

	// Generate TTS audio from the book text using the asynchronous OpenAI API call.
	ttsAudioPath, err := convertTextToAudio(bookText)
	if err != nil {
		log.Printf("Error converting text to audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("TTS audio file generated at: %s for book ID %d", ttsAudioPath, book.ID)

	// Update the book record.
	book.AudioPath = ttsAudioPath
	book.Status = "TTS completed"
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record for book ID %d: %v", book.ID, err)
		return
	}

	// Trigger sound effects merging asynchronously.
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
