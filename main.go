package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

// === DEFINISI MODEL ===
type Role struct {
	ID       uint   `gorm:"primaryKey"`
	RoleName string `gorm:"type:varchar(50);unique;not null"`
}
type User struct {
	ID           uint   `gorm:"primaryKey"`
	Name         string `gorm:"type:varchar(100);not null"`
	Email        string `gorm:"type:varchar(100);unique;not null"`
	PasswordHash string `gorm:"type:varchar(255);not null"`
	RoleID       uint
	Role         Role `gorm:"foreignKey:RoleID"`
	CreatedAt    time.Time
}
type Class struct {
	ID        uint   `gorm:"primaryKey"`
	ClassName string `gorm:"type:varchar(50);not null"`
	MajorID   uint
	CreatedAt time.Time
}
type Subject struct {
	ID          uint   `gorm:"primaryKey"`
	SubjectName string `gorm:"type:varchar(100);not null"`
	TeacherID   uint
	Teacher     User `gorm:"foreignKey:TeacherID"`
}
type Material struct {
	ID         uint   `gorm:"primaryKey"`
	SubjectID  uint
	Title      string `gorm:"type:varchar(255);not null"`
	ContentURL string `gorm:"type:text"`
	UploadedBy uint
}
type Assignment struct {
	ID        uint   `gorm:"primaryKey"`
	SubjectID uint
	Title     string `gorm:"type:varchar(255);not null"`
	Deadline  time.Time
	MaxScore  int `gorm:"default:100"`
}
type Submission struct {
	ID           uint   `gorm:"primaryKey"`
	AssignmentID uint
	StudentID    uint
	FileURL      string `gorm:"type:text;not null"`
	Score        int
	Feedback     string `gorm:"type:text"`
}

// === FUNGSI SEEDER (Pengisi Data Awal) ===
func seedRoles() {
	roles := []Role{
		{ID: 1, RoleName: "Admin"},
		{ID: 2, RoleName: "Guru"},
		{ID: 3, RoleName: "Siswa"},
	}
	for _, role := range roles {
		DB.FirstOrCreate(&role, Role{ID: role.ID})
	}
	fmt.Println("Data Role (Admin, Guru, Siswa) berhasil disuntikkan!")
}

// === FUNGSI UTAMA ===
func main() {
	var err error
	DB, err = gorm.Open(sqlite.Open("lms_data.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("Gagal terhubung ke database:", err)
	}

	DB.AutoMigrate(&Role{}, &User{}, &Class{}, &Subject{}, &Material{}, &Assignment{}, &Submission{})
	
	// Jalankan Seeder
	seedRoles()

	r := gin.Default()

	// === RUTE PUBLIK (TIDAK PERLU LOGIN) ===
	r.GET("/api/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "sukses", "pesan": "Server Backend Hidup!"})
	})
	r.POST("/api/register", Register)
	r.POST("/api/login", Login)

	// === RUTE YANG DILINDUNGI (HARUS LOGIN) ===
	protected := r.Group("/api")
	protected.Use(AuthMiddleware()) 
	{
		// Rute Tes Dashboard
		protected.GET("/dashboard", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "Selamat datang di Dashboard Rahasia LMS!",
				"data": "Hanya user yang sudah login yang bisa melihat teks ini.",
			})
		})

		// 1. Rute Khusus Admin
		protected.POST("/admin/classes", CreateClass)
		protected.GET("/admin/classes", GetClasses)

		// 2. Rute Khusus Guru (dan Siswa untuk GET)
		protected.POST("/teacher/materials", CreateMaterial)
		protected.GET("/materials", GetMaterials)

		// 3. Rute Khusus Siswa
		protected.POST("/student/submissions", SubmitAssignment)
	}

	fmt.Println("======================================")
	fmt.Println(" Server nyala : http://localhost:8080")
	fmt.Println("======================================")
	r.Run(":8080")
}