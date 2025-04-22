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
	"strconv"
	"strings"
	"time"
)

// ElevenLabs URL for generating a fixed 22‑second background track.
const elevenLabsSoundEffectsURL = "https://api.elevenlabs.io/v1/sound-generation"

// OpenAI ChatGPT “chat completions” endpoint for segmentation instructions.
const OpenAIResponsesURL = "https://api.openai.com/v1/chat/completions"

// Segment represents one segment’s instructions for the dynamic background.
type Segment struct {
	Start float64 `json:"start"` // Seconds into TTS timeline
	End   float64 `json:"end"`   // Seconds into TTS timeline
	Mood  string  `json:"mood"`  // "suspense", "action", etc.
}

// SoundEffectRequest is the payload for ElevenLabs’ sound‐generation endpoint.
type SoundEffectRequest struct {
	Text            string  `json:"text"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	PromptInfluence float64 `json:"prompt_influence,omitempty"`
}

// generateSoundEffect calls ElevenLabs to get a 22‑second background track.
func generateSoundEffect(prompt string) (string, error) {
	apiKey := os.Getenv("XI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XI_API_KEY environment variable not set")
	}

	payload := SoundEffectRequest{
		Text:            prompt,
		DurationSeconds: 22.0,
		PromptInfluence: 0.5,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", elevenLabsSoundEffectsURL, bytes.NewReader(body))
	req.Header.Add("xi-api-key", apiKey)
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sound effects API request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sound effects API returned %d: %s", resp.StatusCode, string(b))
	}

	data, _ := io.ReadAll(resp.Body)
	os.MkdirAll("./audio", 0755)
	filename := fmt.Sprintf("sound_effect_%d.mp3", time.Now().Unix())
	path := "./audio/" + filename
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write sound file: %w", err)
	}
	return path, nil
}

// summurizedBookText returns up to the first 200 characters of the text.
func summurizedBookText(txt string) string {
	if len(txt) > 200 {
		return strings.TrimSpace(txt[:200]) + "..."
	}
	return txt
}

// fallbackSegments divides ttsDuration into equal, "neutral" segments (~22s each).
func fallbackSegments(ttsDuration float64) []Segment {
	num := int(math.Ceil(ttsDuration / 22.0))
	chunk := ttsDuration / float64(num)
	out := make([]Segment, num)
	for i := 0; i < num; i++ {
		start := float64(i) * chunk
		end := start + chunk
		if end > ttsDuration {
			end = ttsDuration
		}
		out[i] = Segment{Start: start, End: end, Mood: "neutral"}
	}
	return out
}

// generateSegmentInstructions calls ChatGPT to get a JSON array of segments,
// strips any markdown fences, forces a trailing ], and falls back on parse errors.
func generateSegmentInstructions(ttsDuration float64, bookFilePath string) ([]Segment, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY environment variable not set")
	}

	// Read & summarize the book text for prompt brevity.
	b, err := os.ReadFile(bookFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read book file: %w", err)
	}
	summary := summurizedBookText(string(b))

	numSegments := int(math.Ceil(ttsDuration / 22.0))
	systemMsg := map[string]string{"role": "system", "content": "You are an audio segmentation assistant."}
	userPrompt := fmt.Sprintf(
		`Given a TTS narration duration of %.2f seconds and this excerpt:

%s

Output ONLY a JSON array of %d objects, each with:
  - "start": number (seconds into narration)
  - "end":   number (seconds into narration)
  - "mood":  one of "suspense","action","climax","sad","neutral"

Do NOT include any markdown fences or extra text.`, ttsDuration, summary, numSegments)
	userMsg := map[string]string{"role": "user", "content": userPrompt}

	reqBody := map[string]interface{}{
		"model":       "gpt-4o",
		"messages":    []map[string]string{systemMsg, userMsg},
		"temperature": 0.7,
		"max_tokens":  300,
		"n":           1,
	}
	reqBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", OpenAIResponsesURL, bytes.NewReader(reqBytes))
	req.Header.Add("Authorization", "Bearer "+apiKey)
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("GPT segmentation error: %v; falling back", err)
		return fallbackSegments(ttsDuration), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("GPT segmentation returned %d: %s; falling back", resp.StatusCode, string(b))
		return fallbackSegments(ttsDuration), nil
	}

	// Minimal struct to extract content.
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		raw, _ := io.ReadAll(resp.Body)
		log.Printf("Decode GPT response failed: %v\nraw: %s\nfalling back", err, string(raw))
		return fallbackSegments(ttsDuration), nil
	}
	if len(chatResp.Choices) == 0 {
		log.Print("GPT returned no choices; falling back")
		return fallbackSegments(ttsDuration), nil
	}

	rawContent := chatResp.Choices[0].Message.Content
	// strip ```json / ``` fences:
	trimmed := strings.TrimSpace(rawContent)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	// force trailing ]
	if !strings.HasSuffix(trimmed, "]") {
		if idx := strings.LastIndex(trimmed, "}"); idx != -1 {
			trimmed = trimmed[:idx+1] + "]"
		} else {
			trimmed += "]"
		}
	}

	// parse
	var segs []Segment
	if err := json.Unmarshal([]byte(trimmed), &segs); err != nil {
		log.Printf("Invalid JSON from GPT: %v\nGPT raw: %s\nfalling back", err, rawContent)
		return fallbackSegments(ttsDuration), nil
	}
	return segs, nil
}

// generateDynamicBackgroundWithSegments builds a full‑length background
// by looping, trimming, delaying and concatenating the 22 s source.
func generateDynamicBackgroundWithSegments(ttsDur float64, bgPath string, segs []Segment) (string, error) {
	var files []string
	for i, s := range segs {
		// dur := s.End - s.Start
		segDur := s.End - s.Start
		if segDur <= 0 {
			continue
		}
		out := fmt.Sprintf("./audio/dyn_seg_%d.ogg", i)
		// how long to let ffmpeg run: include silence (s.Start) + the actual segment
		totalLen := s.Start + segDur
		delayMs := int(s.Start * 1000)
		delayStr := fmt.Sprintf("%d|%d", delayMs, delayMs)

		cmd := exec.Command("ffmpeg", "-y",
			"-stream_loop", "-1", "-i", bgPath,
			// now run for silence + audio so we don't chop off the delayed part
			"-t", fmt.Sprintf("%.2f", totalLen),
			"-af", fmt.Sprintf("adelay=%s,volume=0.10", delayStr),
			out,
		)
		if o, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("segment %d failed: %v\n%s", i, err, string(o))
		}
		files = append(files, out)
	}

	// Write concat list (use basenames since ffmpeg runs from project root)--> old ways
	// new way writ concat list using the actual paths
	listFile := "./dyn_list.txt"
	f, err := os.Create(listFile)
	if err != nil {
		return "", fmt.Errorf("failed to create dynamic segment list file: %w", err)
	}
	for _, fn := range files {
		// fn is "./audio/dyn_seg_0.ogg" etc.
		f.WriteString(fmt.Sprintf("file '%s'\n", fn))
	}
	f.Close()

	// First, concatenate into a staged file
	staged := "./audio/dynamic_bg_staged.ogg"
	cat := exec.Command("ffmpeg", "-y",
		"-f", "concat", "-safe", "0",
		"-i", listFile, // file is now at the project root
		"-c", "copy", staged,
	)
	if o, err := cat.CombinedOutput(); err != nil {
		return "", fmt.Errorf("concat fail: %v\n%s", err, string(o))
	}

	// Then trim & re-encode (you cannot filter + copy in one go)
	finalBg := "./audio/dynamic_background_final.ogg"
	trim := exec.Command("ffmpeg", "-y",
		"-i", staged,
		"-af", fmt.Sprintf("atrim=duration=%.2f", ttsDur),
		"-c:a", "libopus", "-b:a", "64k",
		finalBg,
	)
	if o, err := trim.CombinedOutput(); err != nil {
		return "", fmt.Errorf("trim fail: %v\n%s", err, string(o))
	}

	return finalBg, nil
}

// mergeAudio overlays the TTS file with our dynamically stretched background.
func mergeAudio(ttsPath, bgPath, bookPath string) (string, error) {
	// probe duration
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", ttsPath).Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe error: %w", err)
	}
	dur, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	log.Printf("TTS duration: %.2f", dur)

	// segmentation
	segs, err := generateSegmentInstructions(dur, bookPath)
	if err != nil {
		return "", fmt.Errorf("segmentation error: %w", err)
	}
	log.Printf("Segments: %+v", segs)

	// dynamic background
	dynBg, err := generateDynamicBackgroundWithSegments(dur, bgPath, segs)
	if err != nil {
		return "", fmt.Errorf("dynamic background error: %w", err)
	}

	outFile := "./audio/merged_output.ogg"
	filter := "[0][1]amix=inputs=2:duration=first:dropout_transition=2"
	cmd := exec.Command("ffmpeg", "-y", "-i", ttsPath, "-i", dynBg,
		"-filter_complex", filter,
		"-c:a", "libopus", "-b:a", "64k", outFile,
	)
	if o, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("merge fail: %v\n%s", err, string(o))
	}
	log.Printf("Merged into %s", outFile)
	return outFile, nil
}

// processSoundEffectsAndMerge ties everything together.
func processSoundEffectsAndMerge(book Book) {
	prompt, err := generateOverallSoundPrompt(book.FilePath)
	if err != nil {
		log.Printf("prompt err: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("Prompt: %s", prompt)

	bgPath, err := generateSoundEffect(prompt)
	if err != nil {
		log.Printf("sfx err: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}

	merged, err := mergeAudio(book.AudioPath, bgPath, book.FilePath)
	if err != nil {
		log.Printf("merge err: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}

	book.AudioPath = merged
	if err := db.Save(&book).Error; err != nil {
		log.Printf("db save err: %v", err)
	} else {
		log.Printf("Merged audio saved for book %d", book.ID)
	}
}
