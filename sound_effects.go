package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// elevenLabsSoundEffectsURL defines the endpoint for generating sound effects.
const elevenLabsSoundEffectsURL = "https://api.elevenlabs.io/v1/sound-generation"

// SoundEffectRequest represents the JSON payload for the sound effects endpoint.
type SoundEffectRequest struct {
	Text            string  `json:"text"`                       // Required: The prompt text for the sound effect.
	DurationSeconds float64 `json:"duration_seconds,omitempty"` // Optional: Duration (e.g., 22 seconds per segment).
	PromptInfluence float64 `json:"prompt_influence,omitempty"` // Optional: Value between 0 and 1.
}

// generateSoundEffect calls ElevenLabs' sound effects endpoint using the provided text.
// It writes the binary MP3 response to a file and returns the file path.
func generateSoundEffect(text string) (string, error) {
	apiKey := os.Getenv("XI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XI_API_KEY environment variable not set")
	}

	// Prepare the JSON payload with a fixed 22-second duration for each segment.
	reqPayload := SoundEffectRequest{
		Text:            text,
		DurationSeconds: 22.0,
		PromptInfluence: 0.5,
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

	// Use an HTTP client with a timeout.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sound effects API request error: %w", err)
	}
	defer resp.Body.Close()

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

	// Generate a unique filename for this segment.
	filename := fmt.Sprintf("sound_effect_%d.mp3", time.Now().UnixNano())
	filePath := "./audio/" + filename

	// Write the binary data to the file.
	if err := os.WriteFile(filePath, soundData, 0644); err != nil {
		return "", fmt.Errorf("failed to write sound effects file: %w", err)
	}

	return filePath, nil
}

// generateMultipleSoundEffects calculates how many 22-second segments are needed to cover the TTS duration,
// then generates them asynchronously and returns a slice of file paths.
func generateMultipleSoundEffects(prompt string, ttsDuration float64, segmentDuration float64) ([]string, error) {
	segmentsNeeded := int(math.Ceil(ttsDuration / segmentDuration))
	log.Printf("Generating %d sound effect segments to cover %.2f seconds (each %.2f seconds long)", segmentsNeeded, ttsDuration, segmentDuration)

	segmentFiles := make([]string, segmentsNeeded)
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, segmentsNeeded)

	for i := 0; i < segmentsNeeded; i++ {
		wg.Add(1)
		go func(segmentIndex int) {
			defer wg.Done()
			// You could modify the prompt per segment if desired.
			segmentPrompt := prompt
			filePath, err := generateSoundEffect(segmentPrompt)
			if err != nil {
				errChan <- err
				return
			}
			mu.Lock()
			segmentFiles[segmentIndex] = filePath
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	close(errChan)
	if len(errChan) > 0 {
		return nil, <-errChan
	}
	return segmentFiles, nil
}

// mergeMultipleSoundEffects uses FFmpeg's concat demuxer to merge multiple sound effect segments into one file.
func mergeMultipleSoundEffects(segmentFiles []string) (string, error) {
	// Ensure the audio directory exists.
	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	// Write the file list in the ./audio directory using only the base names.
	fileListPath := "./audio/segment_list.txt"
	var fileListContent strings.Builder
	for _, file := range segmentFiles {
		baseName := filepath.Base(file)
		fileListContent.WriteString(fmt.Sprintf("file '%s'\n", baseName))
	}
	if err := os.WriteFile(fileListPath, []byte(fileListContent.String()), 0644); err != nil {
		return "", fmt.Errorf("failed to write file list: %w", err)
	}

	// Define the merged output file name relative to the "./audio" directory.
	mergedFileRelative := "merged_sound_effects.mp3"
	mergedFilePath := "./audio/" + mergedFileRelative

	// Construct FFmpeg arguments.
	ffmpegArgs := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", "segment_list.txt",
		"-c", "copy",
		mergedFileRelative,
	}

	// Set working directory to the audio folder.
	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	cmd.Dir = "./audio"
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg concat command failed: %v, output: %s", err, string(output))
	}
	return mergedFilePath, nil
}

// mergeAudio uses FFmpeg to overlay (mix) the TTS audio with the sound effects.
// It retrieves the duration of the TTS audio via ffprobe, then constructs a filter_complex string that:
//   - [1]: Trims and loops the sound effects to match the TTS duration and scales its volume by volumeScale.
//   - [0]: Uses the TTS audio as is.
//
// Then it mixes them using amix (keeping the TTS duration), and encodes the final output using libopus in an Ogg container.
func mergeAudio(ttsAudioPath string, mergedSfxPath string, ttsDuration float64, volumeScale float64) (string, error) {
	finalOutputPath := "./audio/final_merged_output.ogg"

	// Construct FFmpeg filter_complex string.
	// Note: We do NOT want any literal '%' characters in the dropout_transition value.
	filterComplex := fmt.Sprintf("[1]atrim=duration=%.2f,volume=%.2f[sfx];[0][sfx]amix=inputs=2:duration=first:dropout_transition=2", ttsDuration, volumeScale)

	ffmpegArgs := []string{
		"-y",
		"-i", ttsAudioPath,
		"-i", mergedSfxPath,
		"-filter_complex", filterComplex,
		"-c:a", "libopus", // Use the Opus codec.
		"-b:a", "64k", // Target bitrate (adjust as needed).
		finalOutputPath,
	}

	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg merge command failed: %v, output: %s", err, string(output))
	}

	log.Printf("Merging TTS audio '%s' with background music '%s' into '%s'", ttsAudioPath, mergedSfxPath, finalOutputPath)
	return finalOutputPath, nil
}

// processSoundEffectsAndMerge orchestrates the generation and merging of background sound effects with TTS narration.
// It generates an overall background prompt from the book's text, calculates the TTS duration,
// dynamically generates the required number of sound effect segments, merges them into one track, and
// overlays that track (at a reduced volume) with the TTS narration.
func processSoundEffectsAndMerge(book Book) {
	// Generate an overall sound prompt from the book's text.
	overallPrompt, err := generateOverallSoundPrompt(book.FilePath)
	if err != nil {
		log.Printf("Error generating overall sound prompt for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("Generated overall sound prompt for book ID %d: %s", book.ID, overallPrompt)

	// Get TTS duration using ffprobe.
	ffprobeCmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0",
		"-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", book.AudioPath)
	ffprobeOutput, err := ffprobeCmd.Output()
	if err != nil {
		log.Printf("Error getting TTS duration: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}
	durationStr := strings.TrimSpace(string(ffprobeOutput))
	ttsDuration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		log.Printf("Error parsing TTS duration: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("TTS duration: %.2f seconds", ttsDuration)

	// Define segment duration (22 seconds per segment).
	segmentDuration := 22.0

	// Dynamically calculate how many segments are needed.
	segmentFiles, err := generateMultipleSoundEffects(overallPrompt, ttsDuration, segmentDuration)
	if err != nil {
		log.Printf("Error generating sound effect segments for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}

	// Merge the individual segments into one background music track.
	mergedSfxPath, err := mergeMultipleSoundEffects(segmentFiles)
	if err != nil {
		log.Printf("Error merging sound effect segments for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("Merged sound effects track for book ID %d: %s", book.ID, mergedSfxPath)

	// Merge the TTS audio with the merged background track.
	// Set volumeScale to 0.10 (10%) for the background music relative to the narration.
	finalMergedPath, err := mergeAudio(book.AudioPath, mergedSfxPath, ttsDuration, 0.10)
	if err != nil {
		log.Printf("Error merging final audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}

	// Update the Book record with the new merged audio file path.
	book.AudioPath = finalMergedPath
	if err := db.Save(&book).Error; err != nil {
		log.Printf("Error updating book record after merging audio for book ID %d: %v", book.ID, err)
	} else {
		log.Printf("Final merged audio generated and saved for Book ID %d", book.ID)
	}
}
