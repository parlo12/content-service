package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// uploadBookFileHandler handles file uploads for books.
// It expects form-data with keys "book_id" and "file".
func uploadBookFileHandler(c *gin.Context) {
	// Retrieve the book ID (send as form-data field "book_id")
	bookID := c.PostForm("book_id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "book_id is required"})
		return
	}

	// Retrieve the file from the form.
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File upload error", "details": err.Error()})
		return
	}

	// Validate the file type; only allow PDF or TXT
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") &&
		!strings.HasSuffix(strings.ToLower(file.Filename), ".txt") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Only PDF and TXT files are allowed."})
		return
	}

	// Define a destination for the file. Ensure the directory exists.
	dest := "./uploads/" + file.Filename
	if err := c.SaveUploadedFile(file, dest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file", "details": err.Error()})
		return
	}

	// Retrieve the existing Book record.
	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found", "details": err.Error()})
		return
	}

	// Update the Book record with the new file path and change status.
	book.FilePath = dest
	book.Status = "processing"
	if err := db.Save(&book).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update book record", "details": err.Error()})
		return
	}

	// Trigger asynchronous TTS conversion for this book.
	go processBookConversion(book)

	// Return a success response.
	c.JSON(http.StatusOK, gin.H{"message": "File uploaded successfully, processing started", "book": book})
}
