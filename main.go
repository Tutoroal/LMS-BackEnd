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

func seedRoles() {
	roles := []Role{
		{ID: 1, RoleName: "Admin"},
		{ID: 2, RoleName: "Guru"},
		{ID: 3, RoleName: "Siswa"},
	}
	for _, role := range roles {
		DB.FirstOrCreate(&role, Role{ID: role.ID})
	}
	fmt.Println("Data (Admin, Guru, Siswa) sukses disuntik")
}

func main() {
	var err error
	DB, err = gorm.Open(sqlite.Open("lms_data.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("Gagal connect ke db:", err)
	}

	DB.AutoMigrate(&Role{}, &User{}, &Class{}, &Subject{}, &Material{}, &Assignment{}, &Submission{})
	
	seedRoles()

	r := gin.Default()

	r.GET("/api/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "sukses", "pesan": "Server Backend idup!"})
	})
	r.POST("/api/register", Register)
	r.POST("/api/login", Login)

	protected := r.Group("/api")
	protected.Use(AuthMiddleware()) 
	{
		protected.GET("/dashboard", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "dashboard lms abi",
				"data": "testing text (login only can read this code).",
			})
		})
		protected.POST("/admin/classes", CreateClass)
		protected.GET("/admin/classes", GetClasses)

		protected.POST("/teacher/materials", CreateMaterial)
		protected.GET("/materials", GetMaterials)

		protected.POST("/student/submissions", SubmitAssignment)
	}

	fmt.Println(" Server on at : http://localhost:8080")
	r.Run(":8080")
}