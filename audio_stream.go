package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Serve the final merged audio after sound effects processing
func streamMergedChunkAudioHandler(c *gin.Context) {
	bookIDStr := c.Param("book_id")
	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}

	// Check for latest merged audio for this book
	pattern := fmt.Sprintf("./audio/merged_chunk_audio_%d*.mp3", bookID)
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merged audio file not found for this book"})
		return
	}

	// Serve the latest merged audio (use first match)
	audioPath := matches[len(matches)-1]
	c.Header("Content-Type", "audio/mpeg")
	c.File(audioPath)
}
