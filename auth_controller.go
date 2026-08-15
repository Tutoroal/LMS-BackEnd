package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Struktur data yang dikirim dari Frontend saat Daftar
type RegisterInput struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	RoleID   uint   `json:"role_id" binding:"required"`
}

func Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashedPassword, _ := HashPassword(input.Password)

	user := User{
		Name:         input.Name,
		Email:        input.Email,
		PasswordHash: hashedPassword,
		RoleID:       input.RoleID,
	}

	// Simpan ke database
	if err := DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Email sudah terdaftar!"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Registrasi berhasil! Silakan login."})
}

// Struktur data yang dikirim dari Frontend saat Login
type LoginInput struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user User
	// Cari user berdasarkan email
	if err := DB.Where("email = ?", input.Email).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email atau password salah"})
		return
	}

	// Cek kecocokan password
	if !CheckPasswordHash(input.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email atau password salah"})
		return
	}

	// Buat token JWT
	token, err := GenerateJWT(user.Email, user.RoleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membuat token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Login berhasil!",
		"token":   token,
		"role_id": user.RoleID,
	})
}