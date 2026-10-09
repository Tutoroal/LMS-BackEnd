package main

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var jwtSecret []byte

func configureJWT() error {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 || strings.HasPrefix(secret, "replace-with") {
		return errors.New("JWT_SECRET wajib diisi dengan secret acak minimal 32 byte")
	}
	jwtSecret = []byte(secret)
	return nil
}

type LoginInput struct {
	Identifier string `json:"identifier" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

func Login(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil || strings.TrimSpace(input.Identifier) == "" {
		c.JSON(400, gin.H{"error": "Identitas dan kata sandi wajib diisi."})
		return
	}
	identifier := strings.TrimSpace(input.Identifier)
	guardValue, _ := c.Get("login_guard")
	guard, _ := guardValue.(*loginGuard)
	key := ""
	if guard != nil {
		key = guard.key(c, strings.ToLower(identifier))
		if guard.blocked(key) {
			c.Header("Retry-After", "900")
			c.JSON(429, gin.H{"error": "Terlalu banyak percobaan gagal. Coba lagi dalam 15 menit."})
			return
		}
	}
	var user User
	err := DB.Where("LOWER(email) = ? OR nisn_nip = ? OR nis = ?", strings.ToLower(identifier), identifier, identifier).First(&user).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(500, gin.H{"error": "Layanan login belum tersedia."})
		return
	}
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)) != nil || user.RoleID < 1 || user.RoleID > 3 {
		if guard != nil {
			guard.record(key, false)
		}
		c.JSON(401, gin.H{"error": "Identitas atau kata sandi tidak valid."})
		return
	}
	if len(jwtSecret) < 32 {
		c.JSON(503, gin.H{"error": "Konfigurasi sesi belum tersedia."})
		return
	}
	if guard != nil {
		guard.record(key, true)
	}
	expires := time.Now().Add(24 * time.Hour)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": user.ID, "role_id": user.RoleID, "user_name": user.Name, "email": user.Email,
		"iat": time.Now().Unix(), "exp": expires.Unix(), "ver": user.SessionVersion,
	})
	signed, err := token.SignedString(jwtSecret)
	if err != nil {
		c.JSON(500, gin.H{"error": "Gagal membuat sesi."})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"message": "Login berhasil.", "token": signed, "role_id": user.RoleID})
}
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(jwtSecret) < 32 {
			c.AbortWithStatusJSON(401, gin.H{"error": "Silakan masuk kembali."})
			return
		}
		token, err := jwt.Parse(parts[1], func(token *jwt.Token) (interface{}, error) { return jwtSecret, nil },
			jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(401, gin.H{"error": "Sesi tidak valid atau kedaluwarsa."})
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(401, gin.H{"error": "Sesi tidak valid."})
			return
		}
		userID, ok := claims["user_id"].(string)
		if !ok || userID == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "Sesi tidak valid."})
			return
		}
		var user User
		err = DB.First(&user, "id = ?", userID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.AbortWithStatusJSON(401, gin.H{"error": "Akun tidak tersedia."})
			} else {
				c.AbortWithStatusJSON(503, gin.H{"error": "Layanan sesi belum tersedia."})
			}
			return
		}
		role, ok := claims["role_id"].(float64)
		if !ok || role != float64(user.RoleID) || user.RoleID < 1 || user.RoleID > 3 {
			c.AbortWithStatusJSON(401, gin.H{"error": "Hak akses berubah. Silakan masuk kembali."})
			return
		}
		version := float64(0)
		if value, exists := claims["ver"]; exists {
			var valid bool
			version, valid = value.(float64)
			if !valid {
				c.AbortWithStatusJSON(401, gin.H{"error": "Sesi tidak valid."})
				return
			}
		}
		if version != float64(user.SessionVersion) {
			c.AbortWithStatusJSON(401, gin.H{"error": "Sesi telah diakhiri. Silakan masuk kembali."})
			return
		}
		c.Set("user_id", user.ID)
		c.Set("role_id", float64(user.RoleID))
		c.Set("session_user", user)
		expiry, _ := claims.GetExpirationTime()
		c.Set("session_expiry", expiry.Time)
		path := c.Request.URL.Path
		if (strings.HasPrefix(path, "/api/admin/") && user.RoleID != 1) ||
			(strings.HasPrefix(path, "/api/teacher/") && user.RoleID != 1 && user.RoleID != 2) ||
			(strings.HasPrefix(path, "/api/student/") && user.RoleID != 3) {
			c.AbortWithStatusJSON(403, gin.H{"error": "Anda tidak memiliki akses ke halaman ini."})
			return
		}
		c.Next()
	}
}
func CurrentSession(c *gin.Context) {
	value, _ := c.Get("session_user")
	user := value.(User)
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"id": user.ID, "name": user.Name, "email": user.Email, "role_id": user.RoleID, "expires_at": c.MustGet("session_expiry"), "class_id": user.ClassID, "nis": user.NIS, "nisn_nip": user.NISN_NIP})
}
func Register(c *gin.Context) { c.JSON(403, gin.H{"error": "Pendaftaran dikelola administrator."}) }
