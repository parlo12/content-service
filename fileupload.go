package main
// fileuploadgo uploadBookFileHandler handles file uploads for books.
// It expects form-data with keys "book_id" and "file".
// It saves the file to a specified directory and updates the book record in the database.
// It also processes the uploaded file by chunking it into smaller parts for further processing.

import (
	"net/http"
	"os"
	"strings"
	"path/filepath"
	"fmt"
	"crypto/sha256"
	"io"

	"github.com/gin-gonic/gin"
)



func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}


// uploadBookFileHandler handles file uploads for books.
// It expects form-data with keys "book_id" and "file".


func uploadBookFileHandler(c *gin.Context) {
	bookID := c.PostForm("book_id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "book_id is required"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File upload error", "details": err.Error()})
		return
	}

	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") &&
		!strings.HasSuffix(strings.ToLower(file.Filename), ".txt") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Only PDF and TXT files are allowed."})
		return
	}

	uploadDir := "./uploads"
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		if err := os.MkdirAll(uploadDir, os.ModePerm); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory", "details": err.Error()})
			return
		}
	}
	dest := filepath.Join(uploadDir, file.Filename)
	if err := c.SaveUploadedFile(file, dest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file", "details": err.Error()})
		return
	}

	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found", "details": err.Error()})
		return
	}

	hash, err := computeFileHash(dest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to compute file hash", "details": err.Error()})
		return
	}

	book.FilePath = dest
	book.Status = "processing"
	book.ContentHash = hash
	if err := db.Save(&book).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update book record", "details": err.Error()})
		return
	}

	numChunks, err := ChunkDocument(book.ID, dest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to chunk document", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "File uploaded and chunked successfully",
		"book_id":     book.ID,
		"chunk_count": numChunks,
		"file_path":   dest,
		"content_hash": hash,
	})
}
