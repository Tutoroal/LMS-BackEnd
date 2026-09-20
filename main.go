package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

//definisi model
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

// seeder awal
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

// main fungsi nya
func main() {
	var err error
	DB, err = gorm.Open(sqlite.Open("lms_data.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("Gagal terhubung ke database:", err)
	}

	DB.AutoMigrate(&Role{}, &User{}, &Class{}, &Subject{}, &Material{}, &Assignment{}, &Submission{})
	
	//jalanin seeder
	seedRoles()

	r := gin.Default()

	// cors setting
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000"}, //akses ai di next js
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// rute publik no login
	r.GET("/api/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "sukses", "pesan": "Server Backend Hidup!"})
	})
	r.POST("/api/register", Register)
	r.POST("/api/login", Login)

	//secure rute harus login
	protected := r.Group("/api")
	protected.Use(AuthMiddleware()) 
	{
		//rute dashboard
		protected.GET("/dashboard", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "Selamat datang di Dashboard Rahasia LMS!",
				"data": "Hanya user yang sudah login yang bisa melihat teks ini.",
			})
		})

		//rute Admin
		protected.POST("/admin/classes", CreateClass)
		protected.GET("/admin/classes", GetClasses)

		//rute Guru (Siswa buat GET)
		protected.POST("/teacher/materials", CreateMaterial)
		protected.GET("/materials", GetMaterials)

		//rute Siswa
		protected.POST("/student/submissions", SubmitAssignment)
	}

	fmt.Println("on server at: http://localhost:8080")
	r.Run(":8080")
}