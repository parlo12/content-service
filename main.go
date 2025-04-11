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
	"time"
)

// Define your Book type (if not already defined in a shared package)
type Book struct {
	ID        uint   `gorm:"primaryKey"`
	Title     string `gorm:"not null"`
	Author    string
	FilePath  string // Local storage file path.
	AudioPath string // Path/URL of the generated audio.
	Status    string `gorm:"default:'pending'"`
	Category  string `gorm:"not null;index"`
	Genre     string `gorm:"index"`
	UserID    uint   `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TTSPayload represents the payload for the OpenAI TTS endpoint.
type TTSPayload struct {
	Input          string  `json:"input"`                     // The text to be converted to audio.
	Model          string  `json:"model"`                     // One of: "tts-1", "tts-1-hd", or "gpt-4o-mini-tts"
	Voice          string  `json:"voice"`                     // One of the supported voices (e.g., "alloy", "ash", etc.)
	Instructions   string  `json:"instructions,omitempty"`    // Optional, additional voice instructions.
	ResponseFormat string  `json:"response_format,omitempty"` // e.g., "mp3", "opus", "wav", etc.
	Speed          float64 `json:"speed,omitempty"`           // Speed of speech (default: 1.0)
}

// openaiTTSURL is the endpoint for generating audio.
const openaiTTSURL = "https://api.openai.com/v1/audio/speech"

// convertTextToAudio sends a POST request to OpenAI's TTS endpoint, writes the resulting
// binary audio to a file, and returns the file path.
func convertTextToAudio(text string) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY environment variable not set")
	}

	payload := TTSPayload{
		Input:          text,
		Model:          "gpt-4o-mini-tts", // Choose "tts-1", "tts-1-hd" or "gpt-4o-mini-tts" as needed.
		Voice:          "alloy",           // Choose one of the supported voices.
		ResponseFormat: "mp3",             // For efficient file size, use mp3.
		Speed:          1.0,
		// Instructions: "Speak in a clear and engaging tone.", // Optional.
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal TTS payload: %w", err)
	}

	req, err := http.NewRequest("POST", openaiTTSURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create TTS request: %w", err)
	}

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

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read TTS audio response: %w", err)
	}

	// Ensure the audio directory exists.
	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	// Create a unique filename.
	filename := fmt.Sprintf("audio_%d.mp3", time.Now().Unix())
	audioPath := "./audio/" + filename

	if err := os.WriteFile(audioPath, audioData, 0644); err != nil {
		return "", fmt.Errorf("failed to write audio file: %w", err)
	}

	return audioPath, nil
}

// mergeAudio uses FFmpeg via a system call (or other means) to mix the two audio files
// so that the sound effects match the duration of the TTS audio.
// This example uses a simplified approach: it calls FFmpeg to concat the TTS audio and sound effects.
// (For proper mixing, you might need more advanced FFmpeg filtergraphs.)
func mergeAudio(ttsAudioPath string, soundEffectsAudioPath string) (string, error) {
	mergedAudioPath := "./audio/merged_output.mp3"

	// Example FFmpeg command (this is a basic concat, adjust according to your audio mixing needs):
	// Here we assume the TTS audio and sound effects are to be mixed (overlayed) so that the sound effects do not shorten the TTS.
	// For overlaying audio (mixing), the following FFmpeg command is an example:
	// ffmpeg -i tts.mp3 -i effects.mp3 -filter_complex "amix=inputs=2:duration=first:dropout_transition=3" output.mp3
	cmd := fmt.Sprintf("ffmpeg -y -i %s -i %s -filter_complex \"amix=inputs=2:duration=first:dropout_transition=3\" %s",
		ttsAudioPath, soundEffectsAudioPath, mergedAudioPath)

	// Execute the command.
	output, err := executeFFmpegCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to merge audio: %w. FFmpeg output: %s", err, output)
	}

	log.Printf("Merging TTS audio '%s' with sound effects '%s' into '%s'", ttsAudioPath, soundEffectsAudioPath, mergedAudioPath)
	return mergedAudioPath, nil
}

// executeFFmpegCommand executes an FFmpeg command and returns its output (if any).
func executeFFmpegCommand(command string) (string, error) {
	// You can use os/exec to execute the command.
	// For example:
	//    cmd := exec.Command("sh", "-c", command)
	//    output, err := cmd.CombinedOutput()
	// Make sure FFmpeg is installed in your container.
	// Here is a quick implementation:
	cmd := exec.Command("sh", "-c", command)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// execCommand is a helper that executes a command and returns its combined output.
func execCommand(name string, arg ...string) (string, error) {
	// Import the os/exec package at the top of your file.
	// (If not already imported, add: "os/exec")
	cmd := exec.Command(name, arg...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// processBookConversionAsync processes a book asynchronously. It reads the book's text,
// calls the TTS conversion, updates the record, and optionally merges sound effects.
// This function runs in a separate goroutine.
func processBookConversionAsync(book Book) {
	go func(b Book) {
		// Step 1: Read the text from the book file.
		contentBytes, err := os.ReadFile(b.FilePath)
		if err != nil {
			log.Printf("Error reading file for book ID %d: %v", b.ID, err)
			updateBookStatus(b.ID, "failed")
			return
		}
		bookText := string(contentBytes)

		// Step 2: Convert text to TTS audio.
		ttsAudioPath, err := convertTextToAudio(bookText)
		if err != nil {
			log.Printf("Error converting text to audio for book ID %d: %v", b.ID, err)
			updateBookStatus(b.ID, "failed")
			return
		}
		log.Printf("TTS audio file generated at: %s for book ID %d", ttsAudioPath, b.ID)
		b.AudioPath = ttsAudioPath
		b.Status = "TTS completed"
		if err := db.Save(&b).Error; err != nil {
			log.Printf("Error updating book record for book ID %d: %v", b.ID, err)
			return
		}

		// Step 3: (Optional) Generate sound effects and merge with TTS.
		// First, generate an overall sound prompt.
		overallPrompt, err := generateOverallSoundPrompt(b.FilePath)
		if err != nil {
			log.Printf("Error generating overall sound prompt for book ID %d: %v", b.ID, err)
			updateBookStatus(b.ID, "failed")
			return
		}
		log.Printf("Generated overall sound prompt for book ID %d: %s", b.ID, overallPrompt)

		// Generate sound effects using the overall prompt.
		soundEffectsFilePath, err := generateSoundEffect(overallPrompt)
		if err != nil {
			log.Printf("Error generating sound effects for book ID %d: %v", b.ID, err)
			updateBookStatus(b.ID, "failed")
			return
		}
		log.Printf("Sound effects file generated for book ID %d: %s", b.ID, soundEffectsFilePath)

		// Merge the TTS audio with the sound effects.
		mergedAudioPath, err := mergeAudio(b.AudioPath, soundEffectsFilePath)
		if err != nil {
			log.Printf("Error merging audio for book ID %d: %v", b.ID, err)
			updateBookStatus(b.ID, "failed")
			return
		}

		// Update the Book record with the merged audio path.
		b.AudioPath = mergedAudioPath
		if err := db.Save(&b).Error; err != nil {
			log.Printf("Error updating book record after merging audio for book ID %d: %v", b.ID, err)
		} else {
			log.Printf("Merged audio generated and saved for Book ID %d", b.ID)
		}
	}(book)
}

// (You’ll need to define generateOverallSoundPrompt somewhere in your project.)
// For demonstration, here’s a stub that simulates generating a prompt based on the book text.
func generateOverallSoundPrompt(filePath string) (string, error) {
	// In practice, you might call ChatGPT (or similar) asynchronously to generate a detailed prompt.
	// Here, we simply return a static prompt.
	return "Dramatic echoing footsteps with a distant eerie wind", nil
}
