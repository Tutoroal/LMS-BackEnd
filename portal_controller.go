package main

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"strings"
	"time"
)

type SchoolSettings struct {
	ID           uint `gorm:"primaryKey"`
	SchoolName   string
	AcademicYear string
}

func GetSchoolSettings(c *gin.Context) {
	var settings SchoolSettings
	if err := DB.Where("id = ?", 1).Attrs(SchoolSettings{SchoolName: "SMK Citra Negara", AcademicYear: "2026/2027 - Ganjil"}).FirstOrCreate(&settings).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": settings, "departments": departments})
}
func UpdateSchoolSettings(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	var input struct {
		SchoolName   string `json:"school_name" binding:"required"`
		AcademicYear string `json:"academic_year" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !validText(input.SchoolName, 100) || !validText(input.AcademicYear, 50) {
		c.JSON(400, gin.H{"error": "Nama sekolah dan tahun ajaran wajib diisi."})
		return
	}
	settings := SchoolSettings{ID: 1, SchoolName: strings.TrimSpace(input.SchoolName), AcademicYear: strings.TrimSpace(input.AcademicYear)}
	if err := DB.Save(&settings).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Pengaturan sekolah tersimpan.", "data": settings})
}
func ChangePassword(c *gin.Context) {
	var input struct {
		Current  string `json:"current_password" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Kata sandi lama dan baru wajib diisi."})
		return
	}
	if err := validatePassword(input.Password); err != nil {
		sendExamError(c, err)
		return
	}
	var user User
	if err := DB.First(&user, "id = ?", c.GetString("user_id")).Error; err != nil {
		sendExamError(c, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Current)) != nil {
		c.JSON(400, gin.H{"error": "Kata sandi lama tidak benar."})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		sendExamError(c, err)
		return
	}
	updated := DB.Model(&User{}).Where("id = ? AND password_hash = ?", user.ID, user.PasswordHash).Updates(map[string]interface{}{"password_hash": string(hash), "session_version": gorm.Expr("session_version + 1")})
	if updated.Error != nil {
		sendExamError(c, updated.Error)
		return
	}
	if updated.RowsAffected != 1 {
		c.JSON(409, gin.H{"error": "Kata sandi berubah. Silakan masuk kembali."})
		return
	}
	c.JSON(200, gin.H{"message": "Kata sandi diganti. Silakan masuk kembali di semua perangkat."})
}
func Logout(c *gin.Context) {
	if err := DB.Model(&User{}).Where("id = ?", c.GetString("user_id")).UpdateColumn("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Sesi akun di semua perangkat diakhiri."})
}
func GetStudentOverview(c *gin.Context) {
	if !isSiswa(c) {
		return
	}
	student, err := studentAccount(DB, c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var class Class
	counts := map[string]int64{"materials": 0, "assignments": 0, "pending_assignments": 0, "exams": 0, "completed_exams": 0}
	if student.ClassID != nil {
		if err = DB.Preload("HomeroomTeacher", func(tx *gorm.DB) *gorm.DB { return tx.Select("id", "name", "email", "role_id") }).First(&class, *student.ClassID).Error; err != nil {
			sendExamError(c, err)
			return
		}
		for key, model := range map[string]interface{}{"materials": &Material{}, "assignments": &Assignment{}, "exams": &Exam{}} {
			var count int64
			if err := DB.Model(model).Where("class_id = ?", *student.ClassID).Count(&count).Error; err != nil {
				sendExamError(c, err)
				return
			}
			counts[key] = count
		}
		var count int64
		if err := DB.Model(&Assignment{}).Where("class_id = ? AND id NOT IN (SELECT assignment_id FROM submissions WHERE student_id = ?)", *student.ClassID, student.ID).Count(&count).Error; err != nil {
			sendExamError(c, err)
			return
		}
		counts["pending_assignments"] = count
		if err := DB.Model(&ExamResult{}).Where("student_id = ? AND exam_id IN (SELECT id FROM exams WHERE class_id = ?)", student.ID, *student.ClassID).Count(&count).Error; err != nil {
			sendExamError(c, err)
			return
		}
		counts["completed_exams"] = count
	}
	c.JSON(200, gin.H{"name": student.Name, "class": class, "counts": counts})
}
func GetStudentGrades(c *gin.Context) {
	if !isSiswa(c) {
		return
	}
	userID := c.GetString("user_id")
	tasks := []struct {
		ID          uint
		Title       string
		SubjectName string
		Score       int
		MaxScore    int
		Feedback    string
		GradedAt    time.Time
	}{}
	err := DB.Model(&Submission{}).Select("submissions.id, assignments.title, subjects.subject_name, submissions.score, assignments.max_score, submissions.feedback, submissions.graded_at").Joins("JOIN assignments ON assignments.id = submissions.assignment_id").Joins("JOIN subjects ON subjects.id = assignments.subject_id").Where("submissions.student_id = ? AND submissions.graded_at IS NOT NULL", userID).Order("submissions.graded_at DESC").Scan(&tasks).Error
	if err != nil {
		sendExamError(c, err)
		return
	}
	exams := []struct {
		ID          uint
		Title       string
		SubjectName string
		Type        string
		Score       float64
	}{}
	err = DB.Model(&ExamResult{}).Select("exam_results.id, exams.title, subjects.subject_name, exams.type, exam_results.score").Joins("JOIN exams ON exams.id = exam_results.exam_id").Joins("JOIN subjects ON subjects.id = exams.subject_id").Where("exam_results.student_id = ?", userID).Order("exam_results.id DESC").Scan(&exams).Error
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"assignments": tasks, "exams": exams})
}

type ExamResultRow struct {
	ExamResult
	StudentName string
	StudentNIS  string
	StartedAt   *time.Time
	SubmittedAt *time.Time
}

func GetTeacherExamResults(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	exam, err := ownedExam(DB, c, id)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if err := finalizeExpiredAttempts(time.Now().UTC()); err != nil {
		sendExamError(c, err)
		return
	}
	rows := []ExamResultRow{}
	err = DB.Model(&ExamResult{}).Select("exam_results.*, users.name AS student_name, users.nis AS student_nis, exam_attempts.started_at, exam_attempts.submitted_at").Joins("JOIN users ON users.id = exam_results.student_id").Joins("LEFT JOIN exam_attempts ON exam_attempts.exam_id = exam_results.exam_id AND exam_attempts.student_id = exam_results.student_id").Where("exam_results.exam_id = ?", id).Order("users.name").Scan(&rows).Error
	if err != nil {
		sendExamError(c, err)
		return
	}
	var participants int64
	if err := DB.Model(&User{}).Where("class_id = ? AND role_id = 3", exam.ClassID).Count(&participants).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"exam": exam, "data": rows, "student_count": participants})
}
