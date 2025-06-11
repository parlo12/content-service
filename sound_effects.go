package main

import (
	"bytes"
	"crypto/sha256"
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
	"time"
)

// -------------------- constants & types --------------------

const (
	elevenLabsSoundEffectsURL = "https://api.elevenlabs.io/v1/sound-generation"
	openAIChatURL             = "https://api.openai.com/v1/chat/completions"
)

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Mood  string  `json:"mood"`
}

type EventMap map[string][]float64

type SoundEffectRequest struct {
	Text            string  `json:"text"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	PromptInfluence float64 `json:"prompt_influence,omitempty"`
}

var effectCache = map[string]string{}
var effectPrompts = map[string]string{
	"sword_clash": "Short metallic sword clash, bright ring, about 2 seconds.",
	"door_creak":  "Wooden door creaking open, slow, about 2 seconds.",
	"thunder":     "Low rumbling thunder roll, about 2 seconds.",
}

// -------------------- background music pipeline --------------------

// generateSoundEffect fetches one 22s music clip from ElevenLabs.

func generateSoundEffect(prompt string, id ...interface{}) (string, error) {
	apiKey := os.Getenv("XI_API_KEY")
	if apiKey == "" {
		return "", errors.New("XI_API_KEY not set")
	}
	payload := SoundEffectRequest{Text: prompt, DurationSeconds: 22, PromptInfluence: 0.5}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", elevenLabsSoundEffectsURL, bytes.NewReader(body))
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("sound effects API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sound effects API returned %d: %s", resp.StatusCode, b)
	}

	data, _ := io.ReadAll(resp.Body)
	os.MkdirAll("./audio", 0755)
	var out string
	if len(id) > 0 {
		out = fmt.Sprintf("./audio/sound_effect_%v.mp3", id[0])
	} else {
		out = "./audio/sound_effect.mp3"
	}
	if err := os.WriteFile(out, data, 0644); err != nil {
		return "", fmt.Errorf("write sound file: %w", err)
	}
	return out, nil
}

// summurizedBookText returns the first 200 chars of txt (or less).
func summurizedBookText(txt string) string {
	if len(txt) > 200 {
		return strings.TrimSpace(txt[:200]) + "..."
	}
	return txt
}

// fallbackSegments chops ttsDur into equal-length "neutral" slices.
func fallbackSegments(ttsDur float64) []Segment {
	n := int(math.Ceil(ttsDur / 22.0))
	chunk := ttsDur / float64(n)
	out := make([]Segment, n)
	for i := 0; i < n; i++ {
		start := float64(i) * chunk
		end := start + chunk
		if end > ttsDur {
			end = ttsDur
		}
		out[i] = Segment{Start: start, End: end, Mood: "neutral"}
	}
	return out
}

// generateSegmentInstructions calls GPT to get emotion-based time segments.
func generateSegmentInstructions(ttsDur float64, bookPath string) ([]Segment, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY not set")
	}
	raw, err := os.ReadFile(bookPath)
	if err != nil {
		return nil, fmt.Errorf("read book: %w", err)
	}
	summary := summurizedBookText(string(raw))
	num := int(math.Ceil(ttsDur / 22.0))

	prompt := fmt.Sprintf(`You are an audio segmentation assistant.
		Given TTS duration of %.2f seconds and this excerpt:%sOutput 
		ONLY a JSON array of %d segments with keys "start", "end", and "mood" (one of "suspense","action","climax","sad","neutral"), no extras.`, ttsDur, summary, num)

	reqBody := map[string]interface{}{
		"model":       "gpt-4o",
		"messages":    []map[string]string{{"role": "system", "content": "Audio segmentation assistant."}, {"role": "user", "content": prompt}},
		"temperature": 0.7,
		"max_tokens":  300,
		"n":           1,
	}
	bb, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bb))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("GPT segmentation error: %v; falling back", err)
		return fallbackSegments(ttsDur), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("GPT segmentation %d: %s; falling back", resp.StatusCode, b)
		return fallbackSegments(ttsDur), nil
	}

	var cr struct {
		Choices []struct{ Message struct{ Content string } } `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		raw2, _ := io.ReadAll(resp.Body)
		log.Printf("decode segmentation failed: %v\nraw: %s\nfalling back", err, raw2)
		return fallbackSegments(ttsDur), nil
	}
	if len(cr.Choices) == 0 {
		log.Print("no segmentation choices; falling back")
		return fallbackSegments(ttsDur), nil
	}

	// clean JSON
	// c := strings.TrimSpace(cr.Choices[0].Message.Content)
	// c = strings.TrimPrefix(c, "```json")
	// c = strings.Trim(c, "```")
	// if !strings.HasSuffix(c, "]") {
	// 	c += "]"
	// }

	// ---- NEW CLEANUP LOGIC ----
	trimmed := cr.Choices[0].Message.Content
	trimmed = strings.TrimSpace(trimmed)
	// pull out the first '[' ... last ']' substring
	if start := strings.Index(trimmed, "["); start >= 0 {
		if end := strings.LastIndex(trimmed, "]"); end > start {
			trimmed = trimmed[start : end+1]
		}
	}
	// ----------------------------

	var segs []Segment
	if err := json.Unmarshal([]byte(trimmed), &segs); err != nil {
		log.Printf("invalid segmentation JSON: %v\nraw: %s\nfalling back", err, trimmed)
		return fallbackSegments(ttsDur), nil
	}
	return segs, nil
}

// generateDynamicBackgroundWithSegments “stretches” the 22s clip.
func generateDynamicBackgroundWithSegments(ttsDur float64, bgPath string, segs []Segment) (string, error) {
	var files []string
	for i, s := range segs {
		segDur := s.End - s.Start
		if segDur <= 0 {
			continue
		}
		out := fmt.Sprintf("./dyn_seg_%d.ogg", i)
		total := s.Start + segDur
		delay := int(s.Start * 1000)
		delayStr := fmt.Sprintf("%d|%d", delay, delay)

		cmd := exec.Command("ffmpeg", "-y",
			"-stream_loop", "-1", "-i", bgPath,
			"-t", fmt.Sprintf("%.2f", total),
			"-af", fmt.Sprintf("adelay=%s,volume=0.30", delayStr),
			out,
		)
		if o, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("segment %d fail: %v\n%s", i, err, o)
		}
		files = append(files, out)
	}

	// write concat list
	list := "./dyn_list.txt"
	f, _ := os.Create(list)
	for _, fn := range files {
		fmt.Fprintf(f, "file '%s'\n", fn)
	}
	f.Close()

	staged := "./dynamic_bg_staged.ogg"
	if o, err := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", staged).CombinedOutput(); err != nil {
		return "", fmt.Errorf("concat fail: %v\n%s", err, o)
	}

	finalBg := "./dynamic_background_final.ogg"
	if o, err := exec.Command("ffmpeg", "-y", "-i", staged,
		"-af", fmt.Sprintf("atrim=duration=%.2f", ttsDur),
		"-c:a", "libopus", "-b:a", "64k",
		finalBg,
	).CombinedOutput(); err != nil {
		return "", fmt.Errorf("trim fail: %v\n%s", err, o)
	}
	return finalBg, nil
}

func computeContentHash(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

// mergeAudio overlays TTS narration with the dynamic background.

func mergeAudio(ttsPath, bgPath string, book Book, bookPath string, hash string) (string, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", ttsPath).Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe: %w", err)
	}
	dur, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	log.Printf("TTS duration: %.2f", dur)

	segs, err := generateSegmentInstructions(dur, bookPath)
	if err != nil {
		return "", err
	}
	dynBg, err := generateDynamicBackgroundWithSegments(dur, bgPath, segs)
	if err != nil {
		return "", err
	}

	outFile := fmt.Sprintf("./merged_output_%d_%s.ogg", book.ID, hash[:8])
	filterComplex := "[1]volume=0.30[bg];[0][bg]amix=inputs=2:duration=first:dropout_transition=2"
	cmd := exec.Command("ffmpeg", "-y", "-i", ttsPath, "-i", dynBg, "-filter_complex", filterComplex, "-c:a", "libopus", "-b:a", "64k", outFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg merge: %v\n%s", err, o)
	}
	log.Printf("Merged into %s", outFile)
	return outFile, nil
}

// getTTSDuration returns the length of an audio file in seconds.
func getTTSDuration(path string) (float64, error) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("parse dur: %w", err)
	}
	return d, nil
}

// -------------------- NEW: sound-event extraction & Foley overlay --------------------

// extractSoundEvents asks GPT to identify event types & timestamps.
func extractSoundEvents(bookPath string, ttsDur float64) (EventMap, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY not set")
	}

	raw, err := os.ReadFile(bookPath)
	if err != nil {
		return nil, err
	}
	sn := string(raw)
	if len(sn) > 500 {
		sn = sn[:500]
	}

	prompt := fmt.Sprintf(`You are an audio event assistant.Given TTS duration of %.2f seconds and this excerpt:%sIdentify distinct event types (e.g. "sword_clash","door_creak") and output ONLY a JSON object mapping each event to an array of timestamps.`, ttsDur, sn)

	reqBody := map[string]interface{}{
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "system", "content": "Audio event assistant."},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.7,
		"max_tokens":  150,
		"n":           1,
	}
	bb, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bb))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("event API %d: %s", resp.StatusCode, b)
	}

	var ch struct {
		Choices []struct{ Message struct{ Content string } } `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return nil, err
	}
	if len(ch.Choices) == 0 {
		return nil, errors.New("no event choices")
	}

	rawC := strings.TrimSpace(ch.Choices[0].Message.Content)
	rawC = strings.TrimPrefix(rawC, "```json")
	rawC = strings.Trim(rawC, "`")
	rawC = strings.TrimSpace(rawC)

	var ev EventMap
	if err := json.Unmarshal([]byte(rawC), &ev); err != nil {
		return nil, fmt.Errorf("unmarshal events: %w\nraw: %s", err, rawC)
	}
	return ev, nil
}

// getOrGenerateEffect returns (and caches) one short clip per eventType.
func getOrGenerateEffect(eventType string) (string, error) {
	if p, ok := effectCache[eventType]; ok {
		return p, nil
	}
	prompt, ok := effectPrompts[eventType]
	if !ok {
		prompt = fmt.Sprintf("Sound effect for event: %s, about 2 seconds.", eventType)
	}
	path, err := generateSoundEffect(prompt, eventType)
	if err != nil {
		return "", err
	}
	effectCache[eventType] = path
	return path, nil
}

// -------------------- orchestration --------------------

// processSoundEffectsAndMerge now also injects background Foley.
func processSoundEffectsAndMerge(book Book, hash string) {

	if book.ContentHash == "" && hash != "" {
		book.ContentHash = hash
		db.Model(&Book{}).Where("id = ?", book.ID).Update("content_hash", hash)
	}
	// Ensure audio_path is set

	if book.AudioPath == "" {
		log.Printf("🚫 No audio_path set for book ID %d, skipping sound effects processing", book.ID)
		return
	}

	// Ensure file exists
	if _, err := os.Stat(book.FilePath); os.IsNotExist(err) {
		log.Printf("🚫 File does not exist for book ID %d: %s", book.ID, book.FilePath)
		return
	}
	// Ensure audio file exists
	if _, err := os.Stat(book.AudioPath); os.IsNotExist(err) {
		log.Printf("🚫 Audio file does not exist for book ID %d: %s", book.ID, book.AudioPath)
		updateBookStatus(book.ID, "failed")
		return
	}
	// Ensure content hash is set

	if book.ContentHash == "" {
		log.Println("⚠️ Book ContentHash is still empty — fallback reuse may not work properly")
	}
	// Check for existing processed audio with same content hash
	var existing Book
	err := db.Where("content_hash = ? AND audio_path IS NOT NULL AND status = 'completed'", book.ContentHash).First(&existing).Error
	if err == nil {
		log.Printf("🎵 Reusing audio from book ID %d for book ID %d", existing.ID, book.ID)
		db.Model(&Book{}).Where("id = ?", book.ID).Updates(map[string]interface{}{
			"audio_path": existing.AudioPath,
			"status":     "completed (reused)",
		})
		return
	}

	// 1) Generate background music prompt
	prompt, err := generateOverallSoundPrompt(book.FilePath)
	if err != nil {
		log.Printf("prompt err: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}
	bg, err := generateSoundEffect(prompt)
	if err != nil {
		log.Printf("music err: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}

	log.Printf("🎶 Background music generated: %s", bg)

	// 2) Mix TTS with background
	baseMix, err := mergeAudio(book.AudioPath, bg, book, book.FilePath, hash)
	if err != nil {
		log.Printf("mergeAudio err: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}

	// 3) Extract events and overlay
	ttsDur, _ := getTTSDuration(book.AudioPath)
	events, err := extractSoundEvents(book.FilePath, ttsDur)
	if err != nil {
		log.Printf("extractSoundEvents warning: %v", err)
		book.AudioPath = baseMix
	} else {
		fxMix, err := overlaySoundEvents(baseMix, events, book)
		if err != nil {
			log.Printf("overlaySoundEvents warning: %v", err)
			book.AudioPath = baseMix
		} else {
			book.AudioPath = fxMix
		}
	}

	// 4) Save and cleanup
	if err := db.Model(&Book{}).Where("id = ?", book.ID).Updates(map[string]interface{}{
		"audio_path": book.AudioPath,
		"status":     "completed",
	}).Error; err != nil {
		log.Printf("db update err: %v", err)
	} else {
		log.Printf("✅ Book %d updated with audio_path → %s", book.ID, book.AudioPath)
	}
	cleanupTempFiles(book.ID)
}

// overlaySoundEvents updated to accept book
func overlaySoundEvents(baseMix string, events EventMap, book Book) (string, error) {
	safeTitle := strings.ReplaceAll(strings.ToLower(book.Title), " ", "_")
	hashSuffix := book.ContentHash[:8]
	outFile := fmt.Sprintf("./final_with_fx_%s_%d_%s.ogg", safeTitle, book.ID, hashSuffix)

	args := []string{"-y", "-i", baseMix}
	var filters, labels []string
	inputIdx := 1

	for evt, times := range events {
		clip, err := getOrGenerateEffect(evt)
		if err != nil {
			log.Printf("warning: %s clip error: %v", evt, err)
			continue
		}
		args = append(args, "-i", clip)
		for j, t := range times {
			d := int(t * 1000)
			inLbl := fmt.Sprintf("[%d:a]", inputIdx)
			outLbl := fmt.Sprintf("[e%d_%d]", inputIdx, j)
			filters = append(filters, fmt.Sprintf("%sadelay=%d|%d,volume=0.45%s", inLbl, d, d, outLbl))
			labels = append(labels, outLbl)
		}
		inputIdx++
	}
	amixIn := "[0:a]" + strings.Join(labels, "")
	totalIn := 1 + len(labels)
	filters = append(filters, fmt.Sprintf("%samix=inputs=%d:duration=first:dropout_transition=0", amixIn, totalIn))

	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-c:a", "libopus", "-b:a", "64k", outFile)

	if o, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("overlaySoundEvents FFmpeg fail: %v\n%s", err, o)
	}
	return outFile, nil
}

// cleanupTempFiles removes dynamic segments and lists
func cleanupTempFiles(_ uint) {
	pattern := "dyn_seg_*.ogg"
	matches, _ := filepath.Glob(pattern)
	for _, file := range matches {
		os.Remove(file)
	}
	os.Remove("dyn_list.txt")
}
