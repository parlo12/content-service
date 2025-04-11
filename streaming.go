package main

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// streamBookAudioHandler handles GET requests to stream the audio file for a given book.
// It expects the book ID in the URL parameter.
func streamBookAudioHandler(c *gin.Context) {
	// Extract Book ID from the URL parameter.
	bookID := c.Param("id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Book ID is required"})
		return
	}

	// Retrieve the book record from the database.
	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found", "details": err.Error()})
		return
	}

	// Check if the AudioPath field has been set.
	if book.AudioPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio file not available for this book"})
		return
	}

	// Optionally check if the file exists on disk.
	if _, err := os.Stat(book.AudioPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio file not found on server", "details": err.Error()})
		return
	}

	// Set the Content-Type header so clients know it's an MP3 file.
	c.Header("Content-Type", "audio/mpeg")
	// Stream the file to the client.
	c.File(book.AudioPath)
}
