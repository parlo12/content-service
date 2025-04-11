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

// elevenLabsSoundEffectsURL defines the endpoint for generating sound effects.
const elevenLabsSoundEffectsURL = "https://api.elevenlabs.io/v1/sound-generation"

// SoundEffectRequest represents the JSON payload for the sound effects endpoint.
type SoundEffectRequest struct {
	Text            string  `json:"text"`                       // Required: The prompt text for the sound effect.
	DurationSeconds float64 `json:"duration_seconds,omitempty"` // Optional: Duration (e.g., 2.0 seconds).
	PromptInfluence float64 `json:"prompt_influence,omitempty"` // Optional: Value between 0 and 1; defaults to 0.3.
}

// generateSoundEffect calls ElevenLabs' sound effects endpoint using the provided text.
// It returns the response as a string—typically, you would expect an MP3 file or base64 encoded audio.
func generateSoundEffect(text string) (string, error) {
	apiKey := os.Getenv("XI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XI_API_KEY environment variable not set")
	}

	// Prepare the JSON payload.
	reqPayload := SoundEffectRequest{
		Text:            text,
		DurationSeconds: 2.0, // Example value; adjust as needed.
		PromptInfluence: 0.3, // Example value.
	}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal sound effect payload: %w", err)
	}

	// Create the HTTP POST request.
	req, err := http.NewRequest("POST", elevenLabsSoundEffectsURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create sound effects request: %w", err)
	}
	req.Header.Add("xi-api-key", apiKey)
	req.Header.Add("Content-Type", "application/json")

	// Execute the request.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sound effects API request error: %w", err)
	}
	defer resp.Body.Close()

	// Check that the request succeeded.
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sound effects API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read sound effects API response: %w", err)
	}

	return string(bodyBytes), nil
}

// mergeAudio simulates merging two audio streams (the TTS voice and sound effects) into one file.
// In production, you would use an audio processing library to mix the files.
func mergeAudio(ttsAudioPath string, soundEffectsAudioPath string) (string, error) {
	// Simulate a processing delay.
	time.Sleep(3 * time.Second)
	// For this example, we'll simulate that the merged audio is saved to a new file.
	mergedAudioPath := "./audio/merged_output.mp3"
	log.Printf("Merging TTS audio '%s' with sound effects '%s' into '%s'", ttsAudioPath, soundEffectsAudioPath, mergedAudioPath)
	// This is just a placeholder; you'll later replace it with actual audio merging logic.
	return mergedAudioPath, nil
}

// processSoundEffectsAndMerge generates sound effects using ElevenLabs and then merges them with the TTS audio.
// Call this function after TTS conversion has been completed (i.e. after processBookConversion).
func processSoundEffectsAndMerge(book Book) {
	// Define the text prompt for sound effects. This can be generated dynamically based on book context.
	soundEffectText := "Spacious braam suitable for high-impact movie trailer moments"

	// Call ElevenLabs' sound effects endpoint.
	soundEffectsResponse, err := generateSoundEffect(soundEffectText)
	if err != nil {
		log.Printf("Error generating sound effects for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("Sound effects response for book ID %d: %s", book.ID, soundEffectsResponse)

	// In a real implementation, the response would include audio data.
	// For this simulation, assume a dummy sound effects audio file path.
	soundEffectsAudioPath := "./audio/sound_effects_dummy.mp3"

	// Now merge the TTS (voice) audio and the sound effects audio.
	mergedAudioPath, err := mergeAudio(book.AudioPath, soundEffectsAudioPath)
	if err != nil {
		log.Printf("Error merging audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}

	// Update the Book record with the merged audio path.
	book.AudioPath = mergedAudioPath
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record after merging audio for book ID %d: %v", book.ID, err)
	} else {
		log.Printf("Merged audio generated and saved for Book ID %d", book.ID)
	}
}
