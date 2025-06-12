package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Global variables
var db *gorm.DB

// Use the JWT secret from an environment variable.
var jwtSecretKey = []byte(getEnv("JWT_SECRET", "defaultSecrete"))

// Allowed categories for validation
var allowedCategories = []string{"Fiction", "Non-Fiction"}

// Book represents the model for a book uploaded by a user.
type Book struct {
	ID          uint   `gorm:"primaryKey"`
	Title       string `gorm:"not null"`
	Author      string // Optional author field
	Content     string `gorm:"type:text"` // Text content of the book
	ContentHash string `gorm:"index"`
	FilePath    string // Local storage file path.
	AudioPath   string // Path/URL of the generated (merged) audio.
	Status      string `gorm:"default:'pending'"`
	Category    string `gorm:"not null;index"`
	Genre       string `gorm:"index"`
	UserID      uint   `gorm:"index"`
	CoverPath   string // Optional cover image path
	CoverURL    string // Optional cover image URL for public access
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BookRequest defines the expected JSON structure for creating a book.
type BookRequest struct {
	Title    string `json:"title" binding:"required"`
	Author   string `json:"author"`
	Category string `json:"category" binding:"required"`
	Genre    string `json:"genre"`
}

// Chunk represents the model for chunks or segments of boook
type BookChunk struct {
	ID        uint   `gorm:"primaryKey"`
	BookID    uint   `gorm:"index"`
	Index     int    // Index of the chunk in the book
	Content   string `gorm:"type:text"` // Text content of the chunk
	AudioPath string `gorm:"not null"`
	TTSStatus string // values: "pending", "processing", "completed", "failed"
	StartTime int64  // Start time in seconds
	EndTime   int64  // End time in seconds
	CreatedAt time.Time
	UpdatedAt time.Time
}

type TTSQueueJob struct {
	ID        uint   `gorm:"primaryKey"`
	BookID    uint   `gorm:"index"`
	ChunkIDs  string // Comma-separated chunk ID list
	Status    string `gorm:"default:'queued'"` // queued, processing, complete, failed
	CreatedAt time.Time
	UpdatedAt time.Time
	UserID    uint `gorm:"index"`
}
type BookResponse struct {
	ID          uint   `json:"id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Category    string `json:"category"`
	Content     string `json:"content,omitempty"` // Optional, can be omitted for public response
	ContentHash string `json:"content_hash"`
	Genre       string `json:"genre"`
	FilePath    string `json:"file_path"`
	AudioPath   string `json:"audio_path"`
	Status      string `json:"status"`
	StreamURL   string `json:"stream_url"`
	CoverURL    string `json:"cover_url"`
	CoverPath   string `json:"cover_path"`
}

func main() {

	// err := godotenv.Load()
	// if err != nil {
	// 	log.Println("⚠️ Could not load .env file, using system env variables")
	// }
	// Set up the database connection and run migrations.
	setupDatabase()
	startTTSWorker()

	// Initialize Gin router.
	router := gin.Default()

	// Calling Streaming Route outside of the authorized group
	// router.GET("/user/books/stream/proxy/:id", proxyBookAudioHandler)

	// Protected routes group.
	authorized := router.Group("/user")
	authorized.Use(authMiddleware())
	{
		authorized.POST("/books", createBookHandler)
		authorized.GET("/books", listBooksHandler)
		authorized.POST("/books/upload", uploadBookFileHandler)
		authorized.GET("/books/:book_id/chunks/pages", listBookPagesHandler) // New handler for listing book pages
		// authorized.GET("/books/stream/proxy/:id", proxyBookAudioHandler)
		authorized.GET("/books/stream/proxy/:id", proxyBookAudioHandler)
		authorized.POST("/chunks/tts", ProcessChunksTTSHandler)
		authorized.GET("/chunks/tts/merged-audio/:book_id", streamMergedChunkAudioHandler)
		authorized.GET("/books/:book_id/chunks/:start/:end/audio", streamChunkGroupAudioHandler)
		//authorized.GET("/chunks/status", checkChunkQueueStatusHandler)

		// processing old chunks
		authorized.GET("/books/:book_id/chunks/processed", listProcessedChunkGroupsHandler)
		// stream audio by chunk IDs
		authorized.POST("/chunks/audio-by-id", streamAudioByChunkIDsHandler)

	}

	// Use PORT env var if set; default to 8083.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	log.Printf("Content service listening on port %s", port)
	router.Run(":" + port)
}

// setupDatabase connects to PostgreSQL and auto migrates the Book model.
func setupDatabase() {
	dbHost := getEnv("DB_HOST", "")
	dbUser := getEnv("DB_USER", "")
	dbPassword := getEnv("DB_PASSWORD", "")
	dbName := getEnv("DB_NAME", "")
	dbPort := getEnv("DB_PORT", "")
	dsn := "host=" + dbHost +
		" user=" + dbUser +
		" password=" + dbPassword +
		" dbname=" + dbName +
		" port=" + dbPort +
		" sslmode=disable TimeZone=UTC"

	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	if err := db.AutoMigrate(&Book{}, &BookChunk{}, &ProcessedChunkGroup{}, &TTSQueueJob{}); err != nil {
		log.Fatalf("AutoMigrate failed: %v", err)
	}
	log.Println("Database connected and migrated successfully")
}

func createBookHandler(c *gin.Context) {
	var req BookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Error in book request binding: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book data", "details": err.Error()})
		return
	}

	if !isValidCategory(req.Category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category", "allowed_categories": allowedCategories})
		return
	}

	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication claims missing"})
		return
	}
	userClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid token claims"})
		return
	}
	userIDFloat, ok := userClaims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User ID not found in token"})
		return
	}
	userID := uint(userIDFloat)

	book := Book{
		Title:    req.Title,
		Author:   req.Author,
		Category: req.Category,
		Genre:    req.Genre,
		Status:   "pending",
		UserID:   userID,
	}
	if err := db.Create(&book).Error; err != nil {
		log.Printf("Error creating book record: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save book", "details": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Book saved", "book": book})
}

// adding a new handler for listing book pages
func listBookPagesHandler(c *gin.Context) {
	bookID := c.Param("book_id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Book ID is required"})
		return
	}

	// Optional pagination
	limit := 20 // default limit
	offset := 0

	if l := c.Query("limit"); l != "" {
		if parsedLimit, err := strconv.Atoi(l); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	if o := c.Query("offset"); o != "" {
		if parsedOffset, err := strconv.Atoi(o); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	// Fetch the book itself for metadata
	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	// Fetch chunks for this book with pagination
	var chunks []BookChunk
	if err := db.Where("book_id = ?", bookID).
		Order("index ASC").
		Limit(limit).
		Offset(offset).
		Find(&chunks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve book chunks", "details": err.Error()})
		return
	}

	if len(chunks) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"message": "No pages found for this range"})
		return
	}

	// Check processed status and prepare pages
	pages := make([]map[string]interface{}, 0, len(chunks))
	fullyProcessed := true

	for _, chunk := range chunks {
		if chunk.TTSStatus != "completed" {
			fullyProcessed = false
		}
		pages = append(pages, map[string]interface{}{
			"page":      chunk.Index + 1,
			"content":   chunk.Content,
			"status":    chunk.TTSStatus,
			"audio_url": chunk.AudioPath,
		})
	}

	// Total page count (optional, could cache later for large scale)
	var totalChunks int64
	db.Model(&BookChunk{}).Where("book_id = ?", bookID).Count(&totalChunks)

	// Send JSON response
	c.JSON(http.StatusOK, gin.H{
		"book_id":         book.ID,
		"title":           book.Title,
		"status":          book.Status,
		"total_pages":     totalChunks,
		"limit":           limit,
		"offset":          offset,
		"fully_processed": fullyProcessed,
		"pages":           pages,
	})
}

// listBooksHandler retrieves all books for the authenticated user, optionally filtering by category and genre.
// It returns a list of books with their details, including a public stream URL for each book.
// It expects the user to be authenticated via JWT token.
// The token should contain user_id in its claims.
// If the user_id is not found in the token, it returns an error.
// If the category or genre is provided, it filters the books accordingly.
// If the category is invalid, it returns an error.
// It also adds a public stream URL to each book in the response.
// If the database query fails, it returns an error with details.
// The stream URL is constructed using the STREAM_HOST environment variable, defaulting to "http://100.110.176.220:8083"
// If the STREAM_HOST environment variable is not set, it uses the default value.
// It returns a JSON response with the list of books, each containing its ID, title, author, category, genre, file path, audio path, status, stream URL, cover URL, and cover path.
// It uses the Gin framework for handling HTTP requests and responses.
func listBooksHandler(c *gin.Context) {
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication claims missing"})
		return
	}
	userClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid token claims"})
		return
	}
	userIDFloat, ok := userClaims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User ID not found in token"})
		return
	}
	userID := uint(userIDFloat)

	category := c.Query("category")
	genre := c.Query("genre")

	var books []Book
	query := db.Where("user_id = ?", userID)
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if genre != "" {
		query = query.Where("genre = ?", genre)
	}
	if err := query.Find(&books).Error; err != nil {
		log.Printf("Error retrieving books for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch books", "details": err.Error()})
		return
	}

	//🛡 Add public stream URL to each book
	streamHost := getEnv("STREAM_HOST", "http://100.110.176.220:8083")
	if streamHost == "" {
		log.Println("STREAM_HOST environment variable not set, using default http://100.110.176.220:8083")
		streamHost = "http://100.110.176.220:8083"
	}
	var response []BookResponse
	for _, book := range books {
		streamURL := streamHost + "/user/books/stream/proxy/" + fmt.Sprintf("%d", book.ID)
		response = append(response, BookResponse{
			ID:        book.ID,
			Title:     book.Title,
			Author:    book.Author,
			Category:  book.Category,
			Genre:     book.Genre,
			FilePath:  book.FilePath,
			AudioPath: book.AudioPath,
			Status:    book.Status,
			StreamURL: streamURL,
			CoverURL:  book.CoverURL,
			CoverPath: book.CoverPath,
		})
	}
	c.JSON(http.StatusOK, gin.H{"books": response})
}

func isValidCategory(category string) bool {
	for _, allowed := range allowedCategories {
		if strings.EqualFold(category, allowed) {
			return true
		}
	}
	return false
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenString string

		// Try getting token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// Fallback to query param if header is missing (iOS/AVPlayer)
		if tokenString == "" {
			tokenString = c.Query("token")
		}

		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing token"})
			return
		}

		// Parse and validate token
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return jwtSecretKey, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			return
		}

		// Attach claims to context
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			c.Set("claims", claims)
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
	}
}

func extractToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("authorization header missing")
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", errors.New("authorization header format must be Bearer {token}")
	}
	return parts[1], nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
