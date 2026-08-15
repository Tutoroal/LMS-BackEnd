package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Akses ditolak. Anda belum login!"})
			c.Abort()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Format token tidak valid!"})
			c.Abort()
			return
		}

		tokenString := parts[1]
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})

		// === PENGAMAN BARU: Hentikan proses jika token cacat/gagal diparse ===
		if err != nil || token == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token tidak valid atau salah format!"})
			c.Abort()
			return
		}

		// Jika token aman, ekstrak datanya
		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			c.Set("user_email", claims["email"])
			c.Set("role_id", claims["role_id"]) 
			c.Next()
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token sudah kadaluarsa atau palsu!"})
			c.Abort()
			return
		}
	}
}