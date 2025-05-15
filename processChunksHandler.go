package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// convertTextToAudio converts text to audio using OpenAI's TTS API.

func ProcessChunksTTSHandler(c *gin.Context) {
	var req struct {
		ChunkIDs []uint `json:"chunk_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.ChunkIDs) == 0 || len(req.ChunkIDs) > 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You must select 1 or 2 chunk_ids"})
		return
	}

	var chunks []BookChunk
	if err := db.Where("id IN ?", req.ChunkIDs).Find(&chunks).Error; err != nil || len(chunks) != len(req.ChunkIDs) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chunk IDs"})
		return
	}

	// Ensure all chunks belong to same book
	bookID := chunks[0].BookID
	for _, ch := range chunks {
		if ch.BookID != bookID || ch.TTSStatus == "completed" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Chunks must be from same book and not already processed"})
			return
		}
	}

	// Process each chunk
	var audioPaths []string
	for _, chunk := range chunks {
		db.Model(&chunk).Update("TTSStatus", "processing")
		audioPath, err := convertTextToAudio(chunk.Content)
		if err != nil {
			db.Model(&chunk).Update("TTSStatus", "failed")
			continue
		}
		chunk.AudioPath = audioPath
		chunk.TTSStatus = "completed"
		db.Save(&chunk)
		audioPaths = append(audioPaths, audioPath)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "TTS processing complete",
		"audio_paths": audioPaths,
	})

	err := processMergedChunks(bookID, req.ChunkIDs)
	if err != nil {
		log.Printf("merge processing failed: %v", err)
	}
}
