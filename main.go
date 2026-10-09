package main

import (
	"context"
	"errors"
	"flag"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var DB *gorm.DB

type Role struct {
	ID       uint   `gorm:"primaryKey"`
	RoleName string `gorm:"unique;not null"`
}
type User struct {
	ID                   string `gorm:"primaryKey;type:varchar(36)"`
	NISN_NIP             string `gorm:"column:nisn_nip;type:varchar(50)"`
	NIS                  string `gorm:"column:nis;type:varchar(50)"`
	Name                 string `gorm:"type:varchar(100);not null"`
	Email                string `gorm:"type:varchar(254);unique;not null"`
	PasswordHash         string `gorm:"not null" json:"-"`
	SessionVersion       uint   `gorm:"default:0;not null" json:"-"`
	TempatLahir          string
	TanggalLahir         string
	JenisKelamin         string
	Specialty            string
	RoleID               uint
	Role                 Role `gorm:"foreignKey:RoleID"`
	LegacyClassReference string
	ClassID              *uint
	Class                *Class  `gorm:"foreignKey:ClassID;constraint:OnDelete:SET NULL;"`
	TaughtClasses        []Class `gorm:"many2many:teacher_classes;"`
	CreatedAt            time.Time
}

func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	return nil
}

type Class struct {
	ID                uint   `gorm:"primaryKey"`
	ClassName         string `gorm:"unique;not null"`
	Major             string
	HomeroomTeacherID *string
	HomeroomTeacher   *User `gorm:"foreignKey:HomeroomTeacherID;constraint:OnDelete:RESTRICT;"`
	CreatedAt         time.Time
}
type Subject struct {
	ID          uint   `gorm:"primaryKey"`
	SubjectName string `gorm:"not null"`
	TeacherID   string
	Teacher     User `gorm:"foreignKey:TeacherID;constraint:OnDelete:RESTRICT;"`
}
type Schedule struct {
	ID        uint `gorm:"primaryKey"`
	ClassID   uint
	Class     Class `gorm:"constraint:OnDelete:CASCADE;"`
	SubjectID uint
	Subject   Subject `gorm:"constraint:OnDelete:CASCADE;"`
	DayOfWeek string
	StartTime string
	EndTime   string
}
type Material struct {
	ID         uint `gorm:"primaryKey"`
	SubjectID  uint
	Subject    Subject `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	ClassID    uint
	Class      Class  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	Title      string `gorm:"not null"`
	ContentURL string
	UploadedBy string
}
type Assignment struct {
	ID        uint `gorm:"primaryKey"`
	SubjectID uint
	Subject   Subject `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	ClassID   uint
	Class     Class  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	Title     string `gorm:"not null"`
	Deadline  time.Time
	MaxScore  int `gorm:"default:100"`
}
type Submission struct {
	ID           uint       `gorm:"primaryKey"`
	AssignmentID uint       `gorm:"uniqueIndex:idx_submission_student_assignment"`
	Assignment   Assignment `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	StudentID    string     `gorm:"uniqueIndex:idx_submission_student_assignment"`
	Student      User       `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	FileURL      string
	Score        int
	Feedback     string
	SubmittedAt  time.Time
	GradedAt     *time.Time
}
type Exam struct {
	ID        uint `gorm:"primaryKey"`
	SubjectID uint
	Subject   Subject `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	ClassID   uint
	Class     Class  `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	Title     string `gorm:"not null"`
	Type      string
	Date      time.Time `gorm:"type:date"`
	Duration  int
	IsActive  bool `gorm:"default:false"`
}
type Question struct {
	ID            uint `gorm:"primaryKey"`
	ExamID        uint
	Exam          Exam `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	QuestionText  string
	OptionA       string
	OptionB       string
	OptionC       string
	OptionD       string
	CorrectAnswer string
}
type ExamResult struct {
	ID        uint   `gorm:"primaryKey"`
	ExamID    uint   `gorm:"uniqueIndex:idx_result_student_exam"`
	Exam      Exam   `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	StudentID string `gorm:"uniqueIndex:idx_result_student_exam"`
	Student   User   `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	Score     float64
}

func seedRoles() {
	for _, role := range []Role{{1, "Admin"}, {2, "Guru"}, {3, "Siswa"}} {
		if err := DB.FirstOrCreate(&role, Role{ID: role.ID}).Error; err != nil {
			log.Fatal(err)
		}
	}
}
func migrateDatabase() error {
	// Older imports stored an unset class as 0/empty rather than SQL NULL.
	// Normalize only those sentinel values; never guess a missing nonzero class.
	if DB.Migrator().HasTable(&User{}) && DB.Migrator().HasTable(&Class{}) && DB.Migrator().HasColumn(&User{}, "ClassID") {
		if err := DB.Exec("UPDATE users SET class_id = NULL WHERE (class_id = 0 OR class_id = '') AND NOT EXISTS (SELECT 1 FROM classes WHERE classes.id = users.class_id)").Error; err != nil {
			return err
		}
	}

	if err := DB.AutoMigrate(&Role{}, &User{}, &Class{}, &Subject{}, &Schedule{}, &Material{}, &Assignment{}, &Submission{}, &Exam{}, &Question{}, &ExamResult{}, &ExamAttempt{}, &SchoolSettings{}); err != nil {
		return err
	}
	// Preserve dangling legacy references for administrators; the original database is backed up before migration.
	normalized := DB.Exec("UPDATE users SET legacy_class_reference = CAST(class_id AS TEXT), class_id = NULL WHERE class_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM classes WHERE classes.id = users.class_id)")
	if normalized.Error != nil {
		return normalized.Error
	}
	if normalized.RowsAffected > 0 {
		log.Printf("Migration: %d accounts need class placement; old IDs retained in legacy_class_reference.", normalized.RowsAffected)
	}
	// Old positive scores/feedback prove a grading action. Old zero-only records have no such marker.
	if err := DB.Model(&Submission{}).Where("graded_at IS NULL AND (score > 0 OR feedback <> '')").Update("graded_at", time.Now().UTC()).Error; err != nil {
		return err
	}
	var classes []Class
	if err := DB.Where("major = '' OR major IS NULL").Find(&classes).Error; err != nil {
		return err
	}
	for _, class := range classes {
		if major := departmentFor(class.ClassName); major != "" {
			if err := DB.Model(&class).Update("major", major).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
func backupDatabase(databasePath string) (string, error) {
	directory := filepath.Join(filepath.Dir(databasePath), "backups")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	target := filepath.Join(directory, "lms-"+time.Now().UTC().Format("20060102-150405.000000000")+".db")
	if err := DB.Exec("VACUUM INTO ?", target).Error; err != nil {
		return "", err
	}
	return target, nil
}
func main() {
	bootstrap := flag.Bool("bootstrap-admin", false, "Create first administrator from BOOTSTRAP_ADMIN_* environment")
	backup := flag.Bool("backup", false, "Create a consistent SQLite backup and exit")
	flag.Parse()
	if err := loadEnvironment(".env"); err != nil {
		log.Fatal(err)
	}
	if err := configureJWT(); err != nil {
		log.Fatal(err)
	}
	databasePath := os.Getenv("DB_PATH")
	if databasePath == "" {
		databasePath = "lms_data.db"
	}
	_, statErr := os.Stat(databasePath)
	var err error
	DB, err = gorm.Open(sqlite.Open(databasePath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if *backup || (statErr == nil && os.Getenv("AUTO_BACKUP") != "false") {
		location, err := backupDatabase(databasePath)
		if err != nil {
			log.Fatal("Backup failed: ", err)
		}
		log.Printf("Database backup: %s", location)
		if *backup {
			return
		}
	}
	if err := migrateDatabase(); err != nil {
		log.Fatal("Migration failed (restore/inspect backup): ", err)
	}
	seedRoles()
	if *bootstrap {
		if err := bootstrapAdministrator(); err != nil {
			log.Fatal(err)
		}
		log.Println("Administrator pertama berhasil dibuat.")
		return
	}
	if err := finalizeExpiredAttempts(time.Now().UTC()); err != nil {
		log.Fatal(err)
	}
	context, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-context.Done():
				return
			case now := <-ticker.C:
				if err := finalizeExpiredAttempts(now.UTC()); err != nil {
					log.Printf("CBT expiry: %v", err)
				}
			}
		}
	}()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: ":" + port, Handler: newRouter(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	go func() {
		log.Printf("LMS backend berjalan di http://localhost:%s", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-context.Done()
	shutdown, cancel := contextpkgWithTimeout()
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Printf("Shutdown: %v", err)
	}
}

// Separate helper keeps the signal context name from obscuring the context package.
func contextpkgWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
func newRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.SetTrustedProxies(nil)
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		if c.Request.URL.Path != "/api/admin/import" {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
		}
		c.Next()
	})
	origin := os.Getenv("FRONTEND_ORIGIN")
	if origin == "" {
		origin = "http://localhost:3000"
	}
	r.Use(cors.New(cors.Config{AllowOrigins: []string{origin}, AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}, AllowHeaders: []string{"Origin", "Content-Type", "Authorization"}, ExposeHeaders: []string{"Content-Disposition"}, MaxAge: 12 * time.Hour}))
	r.GET("/api/status", func(c *gin.Context) {
		var value int
		if err := DB.Raw("SELECT 1").Scan(&value).Error; err != nil {
			c.JSON(503, gin.H{"status": "database unavailable"})
			return
		}
		c.JSON(200, gin.H{"status": "sukses"})
	})
	guard := newLoginGuard()
	r.POST("/api/login", func(c *gin.Context) { c.Set("login_guard", guard); Login(c) })
	r.POST("/api/register", Register)
	api := r.Group("/api", AuthMiddleware())
	api.GET("/auth/me", CurrentSession)
	api.POST("/auth/logout", Logout)
	api.PUT("/auth/password", ChangePassword)
	api.GET("/settings", GetSchoolSettings)
	api.PUT("/admin/settings", UpdateSchoolSettings)
	api.GET("/subjects", GetSubjects)
	api.POST("/admin/subjects", CreateSubject)
	api.PUT("/admin/subjects/:id", UpdateSubject)
	api.DELETE("/admin/subjects/:id", DeleteSubject)
	api.GET("/admin/users", GetUsers)
	api.POST("/admin/users", CreateUser)
	api.PUT("/admin/users/:id", UpdateUser)
	api.DELETE("/admin/users/:id", DeleteUser)
	api.GET("/admin/classes", GetClasses)
	api.POST("/admin/classes", CreateClass)
	api.GET("/admin/classes/:id/details", GetClassDetails)
	api.PUT("/admin/classes/:id", UpdateClass)
	api.DELETE("/admin/classes/:id", DeleteClass)
	api.PUT("/admin/students/:student_id/class", AssignStudentToClass)
	api.PUT("/admin/students/assign", AssignStudentsToClass)
	api.POST("/admin/import", ImportDataExcel)
	api.GET("/admin/import/template", DownloadImportTemplate)
	api.GET("/schedules", GetSchedules)
	api.POST("/admin/schedules", CreateSchedule)
	api.PUT("/admin/schedules/:id", UpdateSchedule)
	api.DELETE("/admin/schedules/:id", DeleteSchedule)
	api.GET("/teacher/classes", GetTeacherClasses)
	api.GET("/teacher/subjects", GetTeacherSubjects)
	api.GET("/teacher/materials", GetMaterials)
	api.POST("/teacher/materials", CreateMaterial)
	api.PUT("/teacher/materials/:id", UpdateMaterial)
	api.DELETE("/teacher/materials/:id", DeleteMaterial)
	api.GET("/teacher/assignments", GetAssignments)
	api.POST("/teacher/assignments", CreateAssignment)
	api.PUT("/teacher/assignments/:id", UpdateAssignment)
	api.DELETE("/teacher/assignments/:id", DeleteAssignment)
	api.GET("/teacher/assignments/:id/submissions", GetSubmissionsByAssignment)
	api.PUT("/teacher/submissions/:id/grade", GradeSubmission)
	api.GET("/teacher/exams", GetExams)
	api.POST("/teacher/exams", CreateExam)
	api.DELETE("/teacher/exams/:id", DeleteExam)
	api.PUT("/teacher/exams/:id/active", SetExamActive)
	api.GET("/teacher/exams/:id/questions", GetQuestionsByExam)
	api.POST("/teacher/exams/questions", CreateQuestion)
	api.DELETE("/teacher/exams/questions/:id", DeleteQuestion)
	api.GET("/teacher/exams/:id/results", GetTeacherExamResults)
	api.GET("/teacher/export/report", ExportTeacherReport)
	api.GET("/student/overview", GetStudentOverview)
	api.GET("/student/grades", GetStudentGrades)
	api.GET("/student/materials", GetStudentMaterials)
	api.GET("/student/assignments", GetStudentAssignments)
	api.POST("/student/submissions", SubmitAssignment)
	api.GET("/student/exams", GetStudentExams)
	api.POST("/student/exams/:id/start", StartStudentExam)
	api.PUT("/student/exams/:id/answers", SaveStudentAnswers)
	api.POST("/student/exams/:id/submit", SubmitStudentExam)
	r.NoRoute(func(c *gin.Context) { c.JSON(404, gin.H{"error": "Endpoint tidak ditemukan."}) })
	return r
}
