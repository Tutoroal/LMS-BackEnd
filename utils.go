package main

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Kunci rahasia untuk membuat token (di dunia nyata, ini disimpan di file .env)
var jwtKey = []byte("rahasia_lms_super_aman_123")

// Fungsi untuk mengacak password
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

// Fungsi untuk mengecek kecocokan password asli vs password acak
func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// Fungsi untuk membuat "tiket masuk" JWT
func GenerateJWT(email string, roleID uint) (string, error) {
	claims := jwt.MapClaims{
		"email":   email,
		"role_id": roleID,
		"exp":     time.Now().Add(time.Hour * 72).Unix(), // Tiket berlaku 72 jam
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}