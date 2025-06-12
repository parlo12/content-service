package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// convertTextToAudio converts text to audio using OpenAI's TTS API.

func ProcessChunksTTSHandler(c *gin.Context) {
	var req struct {
		BookID uint  `json:"book_id"`
		Pages  []int `json:"pages"` // 1-based page numbers
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Pages) == 0 || len(req.Pages) > 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You must provide 1 or 2 pages to process"})
		return
	}

	// Convert pages (index + 1) to chunk indices for the specific book
	var chunks []BookChunk
	if err := db.Where("book_id = ? AND index IN ?", req.BookID, toZeroBasedIndexes(req.Pages)).
		Order("index ASC").
		Find(&chunks).Error; err != nil || len(chunks) != len(req.Pages) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page numbers for the given book_id"})
		return
	}

	// Ensure no chunk has been processed yet
	for _, ch := range chunks {
		if ch.TTSStatus == "completed" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "One or more pages already processed"})
			return
		}
	}

	// Process each chunk
	var audioPaths []string
	for _, chunk := range chunks {
		db.Model(&chunk).Update("TTSStatus", "processing")
		audioPath, err := convertTextToAudio(chunk.Content, chunk.ID)
		if err != nil {
			db.Model(&chunk).Update("TTSStatus", "failed")
			continue
		}
		chunk.AudioPath = audioPath
		chunk.TTSStatus = "completed"
		db.Save(&chunk)
		audioPaths = append(audioPaths, audioPath)
	}

	// Attempt to merge (optional)
	err := processMergedChunks(req.BookID, extractIDs(chunks))
	if err != nil {
		log.Printf("merge processing failed: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "TTS processing complete",
		"audio_paths": audioPaths,
	})
}

func toZeroBasedIndexes(pages []int) []int {
	indices := make([]int, len(pages))
	for i, p := range pages {
		indices[i] = p - 1
	}
	return indices
}

func extractIDs(chunks []BookChunk) []uint {
	ids := make([]uint, len(chunks))
	for i, ch := range chunks {
		ids[i] = ch.ID
	}
	return ids
}
