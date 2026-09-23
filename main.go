package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var DB *gorm.DB

type Role struct {
	ID       uint   `gorm:"primaryKey"`
	RoleName string `gorm:"type:varchar(50);unique;not null"`
}

type User struct {
	ID           string `gorm:"primaryKey;type:varchar(36)"`
	// Penambahan pengunci nama kolom (column:nisn_nip) agar tidak error saat login
	NISN_NIP     string `gorm:"column:nisn_nip;type:varchar(50)"` 
	NIS          string `gorm:"column:nis;type:varchar(50)"` 
	Name         string `gorm:"type:varchar(100);not null"`
	Email        string `gorm:"type:varchar(100);unique;not null"`
	PasswordHash string `gorm:"type:varchar(255);not null"`
	TempatLahir  string `gorm:"type:varchar(100)"`
	TanggalLahir string `gorm:"type:date"`
	JenisKelamin string `gorm:"type:varchar(20)"`
	Specialty    string `gorm:"type:varchar(100)"` 
	
	RoleID       uint   `gorm:"column:role_id"`
	Role         Role   `gorm:"foreignKey:RoleID"`
	
	ClassID      *uint  `gorm:"column:class_id"`
	Class        *Class `gorm:"foreignKey:ClassID"`

	TaughtClasses []Class `gorm:"many2many:teacher_classes;"`

	CreatedAt    time.Time
}

func (u *User) BeforeCreate(tx *gorm.DB) (err error) {
	if u.ID == "" {
		u.ID = uuid.New().String()
	}
	return
}

type Class struct {
	ID                uint   `gorm:"primaryKey"`
	ClassName         string `gorm:"type:varchar(50);not null;unique"`
	HomeroomTeacherID *string 
	HomeroomTeacher   *User   `gorm:"foreignKey:HomeroomTeacherID"`
	CreatedAt         time.Time
}

type Subject struct {
	ID          uint   `gorm:"primaryKey"`
	SubjectName string `gorm:"type:varchar(100);not null"`
	TeacherID   string 
	Teacher     User   `gorm:"foreignKey:TeacherID"`
}

type Schedule struct {
	ID        uint   `gorm:"primaryKey"`
	ClassID   uint   
	Class     Class  `gorm:"foreignKey:ClassID"`
	SubjectID uint   
	Subject   Subject `gorm:"foreignKey:SubjectID"`
	DayOfWeek string `gorm:"type:varchar(20);not null"`
	StartTime string `gorm:"type:varchar(10);not null"`
	EndTime   string `gorm:"type:varchar(10);not null"`
}

type Material struct {
	ID         uint   `gorm:"primaryKey"`
	SubjectID  uint
	Title      string `gorm:"type:varchar(255);not null"`
	ContentURL string `gorm:"type:text"`
	UploadedBy string 
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
	StudentID    string 
	FileURL      string `gorm:"type:text;not null"`
	Score        int
	Feedback     string `gorm:"type:text"`
}

func seedRoles() {
	roles := []Role{{ID: 1, RoleName: "Admin"}, {ID: 2, RoleName: "Guru"}, {ID: 3, RoleName: "Siswa"}}
	for _, role := range roles { DB.FirstOrCreate(&role, Role{ID: role.ID}) }
	log.Println("✅ Data Role berhasil disuntikkan!")
}

func seedUsers() {
	hash, _ := bcrypt.GenerateFromPassword([]byte("121212"), bcrypt.DefaultCost)
	passString := string(hash)

	var admin User
	if DB.Where("email = ?", "admin@cn.edu").First(&admin).Error != nil {
		DB.Create(&User{Name: "Administrator Pusat", Email: "admin@cn.edu", PasswordHash: passString, RoleID: 1})
	}

	var guru User
	if DB.Where("email = ?", "guru@cn.edu").First(&guru).Error != nil {
		DB.Create(&User{Name: "Budi Santoso, S.Kom", Email: "guru@cn.edu", NISN_NIP: "198001012005011001", PasswordHash: passString, RoleID: 2, Specialty: "Guru Kejuruan PPLG"})
	}

	var siswa User
	if DB.Where("email = ?", "siswa@cn.edu").First(&siswa).Error != nil {
		DB.Create(&User{Name: "Andi Pratama", Email: "siswa@cn.edu", NISN_NIP: "0081234567", NIS: "10121", PasswordHash: passString, RoleID: 3})
	}
	
	log.Println("✅ Data Akun Bawaan (Admin, Guru, Siswa) berhasil disuntikkan!")
}

func main() {
	var err error
	DB, err = gorm.Open(sqlite.Open("lms_data.db"), &gorm.Config{})
	if err != nil { log.Fatal(err) }

	DB.AutoMigrate(&Role{}, &User{}, &Class{}, &Subject{}, &Schedule{}, &Material{}, &Assignment{}, &Submission{})
	
	seedRoles()
	seedUsers() 

	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins: []string{"http://localhost:3000"}, AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Authorization"}, AllowCredentials: true, MaxAge: 12 * time.Hour,
	}))

	r.GET("/api/status", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "sukses"}) })
	r.POST("/api/register", Register)
	r.POST("/api/login", Login)

	protected := r.Group("/api")
	protected.Use(AuthMiddleware()) 
	{
		protected.GET("/admin/classes", GetClasses)
		protected.GET("/admin/classes/:id/details", GetClassDetails)
		protected.POST("/admin/classes", CreateClass)
		protected.PUT("/admin/classes/:id", UpdateClass)
		protected.DELETE("/admin/classes/:id", DeleteClass)

		protected.GET("/admin/users", GetUsers)
		protected.POST("/admin/users", CreateUser)
		protected.PUT("/admin/users/:id", UpdateUser)
		protected.DELETE("/admin/users/:id", DeleteUser)

		protected.PUT("/admin/students/:student_id/class", AssignStudentToClass)
		protected.POST("/admin/import", ImportDataExcel)
	}

	log.Println(" Server LMS Backend berjalan di: http://localhost:8080")
	r.Run(":8080")
}