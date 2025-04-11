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
	"os/exec"
	"strconv"
	"strings"
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
// Instead of returning a string, this function writes the binary MP3 response to a file
// and returns the file path.
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

	// Set up an HTTP client with a timeout.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sound effects API request error: %w", err)
	}
	defer resp.Body.Close()

	// Check that the request succeeded.
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sound effects API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read the binary response.
	soundData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read sound effects API response: %w", err)
	}

	// Ensure the audio directory exists.
	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	// Generate a unique filename for the sound effect.
	filename := fmt.Sprintf("sound_effect_%d.mp3", time.Now().Unix())
	filePath := "./audio/" + filename

	// Write the binary data to the file.
	if err := os.WriteFile(filePath, soundData, 0644); err != nil {
		return "", fmt.Errorf("failed to write sound effects file: %w", err)
	}

	return filePath, nil
}

// mergeAudio uses FFmpeg to overlay (mix) the TTS audio with the sound effects.
// It first retrieves the duration of the TTS audio via ffprobe, then loops the sound
// effects until it matches the TTS duration, and finally overlays the two audio streams,
// encoding the output in Opus inside an Ogg container.
func mergeAudio(ttsAudioPath string, soundEffectsAudioPath string) (string, error) {
	// Get the duration of the TTS audio file using ffprobe.
	ffprobeCmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", ttsAudioPath)
	ffprobeOutput, err := ffprobeCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get TTS duration: %w", err)
	}
	durationStr := strings.TrimSpace(string(ffprobeOutput))
	ttsDuration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse TTS duration: %w", err)
	}
	log.Printf("TTS duration: %.2f seconds", ttsDuration)

	// Set the merged output file path with an .ogg extension.
	mergedAudioPath := "./audio/merged_output.ogg"

	// Construct the FFmpeg filter_complex string.
	// It loops (using -stream_loop -1) and trims the sound effects to match the TTS duration,
	// then overlays them using amix while preserving the TTS duration.
	filterComplex := fmt.Sprintf("[1]atrim=duration=%.2f,aloop=loop=-1:size=0[sfx];[0][sfx]amix=inputs=2:duration=first:dropout_transition=2", ttsDuration)

	// Construct the FFmpeg command arguments.
	// - "-c:a libopus" ensures that audio is encoded with the Opus codec.
	// - "-b:a 64k" sets the audio bitrate (adjust as needed).
	ffmpegArgs := []string{
		"-y",
		"-i", ttsAudioPath,
		"-stream_loop", "-1", "-i", soundEffectsAudioPath,
		"-filter_complex", filterComplex,
		"-c:a", "libopus",
		"-b:a", "64k",
		mergedAudioPath,
	}

	ffmpegCmd := exec.Command("ffmpeg", ffmpegArgs...)
	ffmpegOutput, err := ffmpegCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg command failed: %v, output: %s", err, string(ffmpegOutput))
	}

	log.Printf("Merging TTS audio '%s' with sound effects '%s' into '%s'", ttsAudioPath, soundEffectsAudioPath, mergedAudioPath)
	return mergedAudioPath, nil
}

// processSoundEffectsAndMerge generates sound effects based on an overall prompt (generated from book text)
// and then merges these effects with the TTS audio for the given book.
// Note: The functions generateOverallSoundPrompt and updateBookStatus are assumed to be defined elsewhere.
func processSoundEffectsAndMerge(book Book) {
	// Generate an overall sound prompt from the book's text.
	overallPrompt, err := generateOverallSoundPrompt(book.FilePath)
	if err != nil {
		log.Printf("Error generating overall sound prompt for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("Generated overall sound prompt for book ID %d: %s", book.ID, overallPrompt)

	// Generate sound effects using the overall prompt.
	soundEffectsFilePath, err := generateSoundEffect(overallPrompt)
	if err != nil {
		log.Printf("Error generating sound effects for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("Sound effects file generated for book ID %d: %s", book.ID, soundEffectsFilePath)

	// Merge the TTS audio and the generated sound effects.
	mergedAudioPath, err := mergeAudio(book.AudioPath, soundEffectsFilePath)
	if err != nil {
		log.Printf("Error merging audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}

	// Update the Book record with the new merged audio file path.
	book.AudioPath = mergedAudioPath
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record after merging audio for book ID %d: %v", book.ID, err)
	} else {
		log.Printf("Merged audio generated and saved for Book ID %d", book.ID)
	}
}
