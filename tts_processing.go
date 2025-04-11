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

const elevenLabsTTSURLTemplate = "https://api.elevenlabs.io/v1/text-to-speech/%s/stream/with-timestamps?output_format=mp3_44100_128"

type TTSRequest struct {
	Text    string `json:"text"`
	ModelID string `json:"model_id,omitempty"`
}

type TTSResponse struct {
	AudioBase64 string `json:"audio_base64"`
}

func convertTextToAudio(text string) (string, error) {
	voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
	if voiceID == "" {
		return "", errors.New("ELEVENLABS_VOICE_ID environment variable not set")
	}
	apiKey := os.Getenv("XI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XI_API_KEY environment variable not set")
	}

	url := fmt.Sprintf(elevenLabsTTSURLTemplate, voiceID)
	payload := TTSRequest{
		Text:    text,
		ModelID: "eleven_multilingual_v2",
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal TTS payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create TTS request: %w", err)
	}
	req.Header.Add("xi-api-key", apiKey)
	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("TTS API request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("TTS API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var combinedAudio bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		var chunk TTSResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			log.Printf("Error parsing a chunk of TTS response: %v", err)
			continue
		}
		audioChunk, err := base64.StdEncoding.DecodeString(chunk.AudioBase64)
		if err != nil {
			log.Printf("Error decoding audio chunk: %v", err)
			continue
		}
		combinedAudio.Write(audioChunk)
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading TTS streaming response: %w", err)
	}

	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	audioFileName := fmt.Sprintf("audio_%d.mp3", time.Now().Unix())
	audioPath := "./audio/" + audioFileName

	if err := os.WriteFile(audioPath, combinedAudio.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("failed to write audio file: %w", err)
	}

	return audioPath, nil
}

func processBookConversion(book Book) {
	contentBytes, err := os.ReadFile(book.FilePath)
	if err != nil {
		log.Printf("Error reading file for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	bookText := string(contentBytes)

	ttsAudioPath, err := convertTextToAudio(bookText)
	if err != nil {
		log.Printf("Error converting text to audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("TTS audio file generated at: %s for book ID %d", ttsAudioPath, book.ID)

	book.AudioPath = ttsAudioPath
	book.Status = "TTS completed"
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record for book ID %d: %v", book.ID, err)
		return
	}

	// Process sound effects and merge with TTS audio.
	processSoundEffectsAndMerge(book)
}

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
