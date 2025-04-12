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

// Use the JWT secret from an environment variable.
var jwtSecretKey = []byte(getEnv("JWT_SECRET", "defaultSecrete"))

// Allowed categories for validation
var allowedCategories = []string{"Fiction", "Non-Fiction"}

// Book represents the model for a book uploaded by a user.
type Book struct {
	ID        uint   `gorm:"primaryKey"`
	Title     string `gorm:"not null"`
	Author    string
	FilePath  string // Local storage file path.
	AudioPath string // Path/URL of the generated (merged) audio.
	Status    string `gorm:"default:'pending'"`
	Category  string `gorm:"not null;index"`
	Genre     string `gorm:"index"`
	UserID    uint   `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// BookRequest defines the expected JSON structure for creating a book.
type BookRequest struct {
	Title    string `json:"title" binding:"required"`
	Author   string `json:"author"`
	Category string `json:"category" binding:"required"`
	Genre    string `json:"genre"`
}

func main() {
	// Set up the database connection and run migrations.
	setupDatabase()

	// Initialize Gin router.
	router := gin.Default()

	// Protected routes group.
	authorized := router.Group("/user")
	authorized.Use(authMiddleware())
	{
		authorized.POST("/books", createBookHandler)
		authorized.GET("/books", listBooksHandler)
		authorized.POST("/books/upload", uploadBookFileHandler)
		authorized.GET("/books/stream/:id", streamBookAudioHandler)
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

	if err := db.AutoMigrate(&Book{}); err != nil {
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
	c.JSON(http.StatusOK, gin.H{"books": books})
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

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
