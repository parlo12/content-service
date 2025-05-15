package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// processMergedChunks combines TTS audio and text from selected chunks
// then runs the sound effects pipeline.
func processMergedChunks(bookID uint, chunkIDs []uint) error {
	// 1. Fetch the chunks
	var chunks []BookChunk
	if err := db.Where("id IN ?", chunkIDs).Order("index").Find(&chunks).Error; err != nil {
		return fmt.Errorf("failed to fetch chunks: %w", err)
	}
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks found")
	}

	startIdx := chunks[0].Index
	endIdx := chunks[len(chunks)-1].Index

	// 2. Check if already processed
	if existingPath, found := checkIfChunkGroupProcessed(bookID, startIdx, endIdx); found {
		fmt.Printf("Chunk group [%d-%d] already processed. Reusing: %s\n", startIdx, endIdx, existingPath)
		return nil
	}

	// 3. Combine text into a single .txt file
	mergedText := ""
	for _, ch := range chunks {
		mergedText += ch.Content + "\n"
	}
	textFile := fmt.Sprintf("./audio/book_%d_chunks_%d_%d.txt", bookID, startIdx, endIdx)
	if err := os.WriteFile(textFile, []byte(mergedText), 0644); err != nil {
		return fmt.Errorf("failed to write merged text: %w", err)
	}

	// 4. Combine audio into a single MP3 using FFmpeg concat
	listFile := fmt.Sprintf("./audio/audio_list_%d.txt", time.Now().Unix())
	listHandle, err := os.Create(listFile)
	if err != nil {
		return fmt.Errorf("failed to create audio list: %w", err)
	}
	for _, ch := range chunks {
		if !strings.HasSuffix(ch.AudioPath, ".mp3") {
			continue
		}
		absPath, _ := filepath.Abs(ch.AudioPath)
		fmt.Fprintf(listHandle, "file '%s'\n", absPath)
	}
	listHandle.Close()

	mergedAudio := fmt.Sprintf("./audio/book_%d_chunks_%d_%d.mp3", bookID, startIdx, endIdx)
	cmd := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", listFile, "-c", "copy", mergedAudio)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg merge fail: %v\n%s", err, output)
	}

	// 5. Call sound effects pipeline with temporary Book struct
	book := Book{
		ID:        bookID,
		FilePath:  textFile,
		AudioPath: mergedAudio,
	}
	go processSoundEffectsAndMerge(book) // run asynchronously

	// 6. Save to processed chunk group table
	if err := saveProcessedChunkGroup(bookID, startIdx, endIdx, mergedAudio); err != nil {
		return fmt.Errorf("failed to save chunk group metadata: %w", err)
	}

	return nil
}
