package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// elevenLabsTTSURLTemplate is the URL template for the ElevenLabs TTS streaming endpoint.
const elevenLabsTTSURLTemplate = "https://api.elevenlabs.io/v1/text-to-speech/%s/stream/with-timestamps?output_format=mp3_44100_128"

// TTSRequest represents the payload sent to the ElevenLabs TTS endpoint.
type TTSRequest struct {
	Text    string `json:"text"`
	ModelID string `json:"model_id,omitempty"`
}

// TTSResponse represents a single chunk of JSON returned by the ElevenLabs TTS endpoint.
// We assume each JSON object contains an "audio_base64" field.
type TTSResponse struct {
	AudioBase64 string `json:"audio_base64"`
	// Additional fields such as timing info may be returned.
}

// convertTextToAudio connects to ElevenLabs’ TTS endpoint, reads all JSON chunks from the stream,
// decodes the base64-encoded audio, concatenates the resulting audio bytes, saves them to a file,
// and returns the file path.
func convertTextToAudio(text string) (string, error) {
	// Retrieve necessary environment variables.
	voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
	if voiceID == "" {
		return "", errors.New("ELEVENLABS_VOICE_ID environment variable not set")
	}
	apiKey := os.Getenv("XI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XI_API_KEY environment variable not set")
	}

	// Construct the URL.
	url := fmt.Sprintf(elevenLabsTTSURLTemplate, voiceID)

	// Create the TTS payload.
	payload := TTSRequest{
		Text:    text,
		ModelID: "eleven_multilingual_v2", // Adjust if needed.
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal TTS payload: %w", err)
	}

	// Create the HTTP POST request.
	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create TTS request: %w", err)
	}
	req.Header.Add("xi-api-key", apiKey)
	req.Header.Add("Content-Type", "application/json")

	// Send the request.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("TTS API request error: %w", err)
	}
	defer resp.Body.Close()

	// Check for a successful response.
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("TTS API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Create a buffer to hold the complete audio output.
	var combinedAudio bytes.Buffer

	// Use a buffered scanner to process the streaming response line by line.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// Each line should be a complete JSON object.
		var chunk TTSResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			log.Printf("Error parsing a chunk of TTS response: %v", err)
			// Depending on your needs, you could choose to skip this chunk or return an error.
			continue
		}
		// Decode the base64 audio chunk.
		audioChunk, err := base64.StdEncoding.DecodeString(chunk.AudioBase64)
		if err != nil {
			log.Printf("Error decoding audio chunk: %v", err)
			continue
		}
		// Append the decoded audio to our combined buffer.
		combinedAudio.Write(audioChunk)
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading TTS streaming response: %w", err)
	}

	// Ensure the audio directory exists.
	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	// Generate a unique filename (here we use a timestamp).
	audioFileName := fmt.Sprintf("audio_%d.mp3", time.Now().Unix())
	audioPath := "./audio/" + audioFileName

	// Write the combined audio bytes to the file.
	if err := os.WriteFile(audioPath, combinedAudio.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("failed to write audio file: %w", err)
	}

	return audioPath, nil
}

// processBookConversion processes a book by reading its text, converting it to audio using ElevenLabs,
// and updating the Book record with the generated audio file path and marking the status as "completed".
func processBookConversion(book Book) {
	// Read the text from the book's file (assuming it's a TXT file).
	contentBytes, err := os.ReadFile(book.FilePath)
	if err != nil {
		log.Printf("Error reading file for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	bookText := string(contentBytes)

	// Call ElevenLabs to convert the text into audio.
	audioPath, err := convertTextToAudio(bookText)
	if err != nil {
		log.Printf("Error converting text to audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("Audio file generated at: %s for book ID %d", audioPath, book.ID)

	// Update the Book record with the audio file path and mark the conversion as completed.
	book.AudioPath = audioPath
	book.Status = "completed"
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record for book ID %d: %v", book.ID, err)
	} else {
		log.Printf("Book conversion completed for Book ID %d", book.ID)
	}
}

// updateBookStatus updates the status of a book in the database by its ID.
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
