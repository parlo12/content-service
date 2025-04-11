package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Global variables
var db *gorm.DB

var jwtSecretKey = []byte(getEnv("JWT_SECRET", "defaultSecrete")) // Replace with your generated secret or use an env var.

// Allowed categories for validation
var allowedCategories = []string{"Fiction", "Non-Fiction"}

// Book represents the model for a book uploaded by a user.
type Book struct {
	ID        uint   `gorm:"primaryKey"`
	Title     string `gorm:"not null"`
	Author    string
	FilePath  string // Local storage file path.
	AudioPath string // Path/URL of the generated audio.
	Status    string `gorm:"default:'pending'"`
	Category  string `gorm:"not null;index"` // Index for faster queries.
	Genre     string `gorm:"index"`
	UserID    uint   `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// BookRequest defines the expected JSON structure for creating a book.
type BookRequest struct {
	Title    string `json:"title" binding:"required"`
	Author   string `json:"author"` // Optional.
	Category string `json:"category" binding:"required"`
	Genre    string `json:"genre"` // Genre can be optional.
}

func main() {
	// Set up the database connection and run migrations.
	setupDatabase()

	// Initialize Gin router.
	router := gin.Default()

	// Protected routes group. (Assuming auth middleware is similar to auth-service.)
	authorized := router.Group("/user")
	authorized.Use(authMiddleware())
	{
		authorized.POST("/books", createBookHandler)
		authorized.GET("/books", listBooksHandler)
		// added a file upload handler for books.
		authorized.POST("/books/upload", uploadBookFileHandler)
		// authorized.GET("/books/:title", getBookHandler) // Uncomment if you need to fetch a specific book.
		// added a streaming handler for books.
		authorized.GET("/books/stream/:id", streamBookAudioHandler)
	}

	// Use PORT env var if set; default to 8082.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	log.Printf("Content service listening on port %s", port)
	router.Run(":" + port)
}

// setupDatabase connects to PostgreSQL and auto migrates the Book model.
func setupDatabase() {
	// Read database configuration.
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

	// Auto migrate the Book model.
	if err := db.AutoMigrate(&Book{}); err != nil {
		log.Fatalf("AutoMigrate failed: %v", err)
	}
	log.Println("Database connected and migrated successfully")
}

// createBookHandler handles POST /user/books for creating a new book record.
func createBookHandler(c *gin.Context) {
	var req BookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Error in book request binding: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book data", "details": err.Error()})
		return
	}

	// Validate that the category is allowed.
	if !isValidCategory(req.Category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category", "allowed_categories": allowedCategories})
		return
	}

	// Retrieve the authenticated user's ID from JWT claims.
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

	// Create the new book.
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

// listBooksHandler handles GET /user/books to list books for the current user.
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

	// Optional filtering.
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
	c.JSON(http.StatusOK, gin.H{"books": books})
}

// isValidCategory checks if the submitted category is allowed.
func isValidCategory(category string) bool {
	for _, allowed := range allowedCategories {
		if strings.EqualFold(category, allowed) {
			return true
		}
	}
	return false
}

// authMiddleware validates JWT tokens in the request.
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, err := extractToken(c.GetHeader("Authorization"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return jwtSecretKey, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		c.Set("claims", token.Claims)
		c.Next()
	}
}

// extractToken expects the Authorization header to be "Bearer <token>" and returns the token part.
func extractToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("Authorization header missing")
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", errors.New("Authorization header format must be Bearer {token}")
	}
	return parts[1], nil
}

// getEnv returns the environment variable value or a fallback.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
