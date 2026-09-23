package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var jwtSecret = []byte("rahasia-sistem-lms-citra-negara-2026")

type LoginInput struct {
	Identifier string `json:"identifier" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

func Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Identitas dan Kata Sandi wajib diisi!"})
		return
	}

	cleanIdentifier := strings.TrimSpace(input.Identifier)
	var user User
	if err := DB.Where("email = ? OR nisn_nip = ? OR nis = ?", cleanIdentifier, cleanIdentifier, cleanIdentifier).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Identitas tidak ditemukan dalam sistem."})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Kata sandi salah!"})
		return
	}

	// Token sekarang membawa NAMA pengguna untuk ditampilkan di Header Frontend
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   user.ID,
		"role_id":   user.RoleID,
		"user_name": user.Name, 
		"exp":       time.Now().Add(time.Hour * 24).Unix(), 
	})

	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membuat sesi keamanan."})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"message": "Login berhasil!", "token": tokenString, "role_id": user.RoleID})
}

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Akses ditolak."})
			c.Abort(); return
		}
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " { tokenString = tokenString[7:] }
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) { return jwtSecret, nil })
		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Sesi kedaluwarsa."})
			c.Abort(); return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if ok {
			c.Set("user_id", claims["user_id"])
			c.Set("role_id", claims["role_id"])
		}
		c.Next()
	}
}

func Register(c *gin.Context) { c.JSON(http.StatusForbidden, gin.H{"error": "Dilarang"}) }