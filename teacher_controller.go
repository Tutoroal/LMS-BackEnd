package main

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
	"time"
)

type MaterialInput struct {
	SubjectID  uint   `json:"subject_id" binding:"required"`
	ClassID    uint   `json:"class_id" binding:"required"`
	Title      string `json:"title" binding:"required"`
	ContentURL string `json:"content_url" binding:"required"`
}
type AssignmentInput struct {
	SubjectID uint   `json:"subject_id" binding:"required"`
	ClassID   uint   `json:"class_id" binding:"required"`
	Title     string `json:"title" binding:"required"`
	Deadline  string `json:"deadline" binding:"required"`
	MaxScore  int    `json:"max_score"`
}
type MaterialRow struct {
	Material
	ClassName   string
	SubjectName string
}
type AssignmentRow struct {
	Assignment
	ClassName       string
	SubjectName     string
	SubmissionCount int
}

func GetTeacherClasses(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	var classes []Class
	if err := DB.Preload("HomeroomTeacher", func(tx *gorm.DB) *gorm.DB { return tx.Select("id", "name", "email", "role_id") }).Order("class_name").Find(&classes).Error; err != nil {
		sendExamError(c, err)
		return
	}
	if classes == nil {
		classes = []Class{}
	}
	c.JSON(200, gin.H{"data": classes})
}
func GetTeacherSubjects(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	subjects := []Subject{}
	query := DB.Order("subject_name")
	if c.GetFloat64("role_id") != 1 {
		query = query.Where("teacher_id = ?", c.GetString("user_id"))
	}
	if err := query.Find(&subjects).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": subjects})
}
func CreateMaterial(c *gin.Context) { saveMaterial(c, false) }
func UpdateMaterial(c *gin.Context) { saveMaterial(c, true) }
func saveMaterial(c *gin.Context, update bool) {
	if !isGuruOrAdmin(c) {
		return
	}
	var input MaterialInput
	if err := c.ShouldBindJSON(&input); err != nil || !validText(input.Title, 255) || !validURL(input.ContentURL) {
		c.JSON(400, gin.H{"error": "Judul dan URL materi http/https wajib diisi."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := validateTarget(tx, c, input.SubjectID, input.ClassID); err != nil {
			return err
		}
		material := Material{SubjectID: input.SubjectID, ClassID: input.ClassID, Title: strings.TrimSpace(input.Title), ContentURL: strings.TrimSpace(input.ContentURL), UploadedBy: c.GetString("user_id")}
		if !update {
			return tx.Create(&material).Error
		}
		id, err := examID(c)
		if err != nil {
			return err
		}
		current, err := ownedMaterial(tx, c, id)
		if err != nil {
			return err
		}
		return tx.Model(&current).Updates(map[string]interface{}{"subject_id": input.SubjectID, "class_id": input.ClassID, "title": material.Title, "content_url": material.ContentURL}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Materi berhasil disimpan."})
}
func GetMaterials(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	rows := []MaterialRow{}
	query := DB.Model(&Material{}).Select("materials.*, classes.class_name, subjects.subject_name").Joins("JOIN classes ON classes.id = materials.class_id").Joins("JOIN subjects ON subjects.id = materials.subject_id")
	if c.GetFloat64("role_id") != 1 {
		query = query.Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	if err := query.Order("materials.id DESC").Scan(&rows).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func DeleteMaterial(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		material, err := ownedMaterial(tx, c, id)
		if err != nil {
			return err
		}
		return tx.Delete(&material).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Materi dihapus."})
}
func CreateAssignment(c *gin.Context) { saveAssignment(c, false) }
func UpdateAssignment(c *gin.Context) { saveAssignment(c, true) }
func saveAssignment(c *gin.Context, update bool) {
	if !isGuruOrAdmin(c) {
		return
	}
	var input AssignmentInput
	if err := c.ShouldBindJSON(&input); err != nil || !validText(input.Title, 255) || input.MaxScore < 0 || input.MaxScore > 1000 {
		c.JSON(400, gin.H{"error": "Lengkapi judul, kelas, mapel, tenggat, dan skor maksimal 1?1000."})
		return
	}
	deadline, err := deadlineFromInput(input.Deadline)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if input.MaxScore == 0 {
		input.MaxScore = 100
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := validateTarget(tx, c, input.SubjectID, input.ClassID); err != nil {
			return err
		}
		assignment := Assignment{SubjectID: input.SubjectID, ClassID: input.ClassID, Title: strings.TrimSpace(input.Title), Deadline: deadline, MaxScore: input.MaxScore}
		if !update {
			return tx.Create(&assignment).Error
		}
		id, err := examID(c)
		if err != nil {
			return err
		}
		current, err := ownedAssignment(tx, c, id)
		if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&Submission{}).Where("assignment_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 && (current.ClassID != input.ClassID || current.SubjectID != input.SubjectID || current.MaxScore != input.MaxScore) {
			return failExam(409, "Kelas, mapel, dan skor maksimal terkunci setelah ada pengumpulan.")
		}
		return tx.Model(&current).Updates(map[string]interface{}{"subject_id": input.SubjectID, "class_id": input.ClassID, "title": assignment.Title, "deadline": deadline, "max_score": input.MaxScore}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Tugas berhasil disimpan."})
}
func GetAssignments(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	rows := []AssignmentRow{}
	query := DB.Model(&Assignment{}).Select("assignments.*, classes.class_name, subjects.subject_name, (SELECT COUNT(*) FROM submissions WHERE submissions.assignment_id = assignments.id) AS submission_count").Joins("JOIN classes ON classes.id = assignments.class_id").Joins("JOIN subjects ON subjects.id = assignments.subject_id")
	if c.GetFloat64("role_id") != 1 {
		query = query.Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	if err := query.Order("assignments.id DESC").Scan(&rows).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func DeleteAssignment(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		assignment, err := ownedAssignment(tx, c, id)
		if err != nil {
			return err
		}
		if err := tx.Where("assignment_id = ?", id).Delete(&Submission{}).Error; err != nil {
			return err
		}
		return tx.Delete(&assignment).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Tugas dan pengumpulannya dihapus."})
}

type SubmissionRow struct {
	Submission
	StudentName string
	StudentNIS  string
	ClassName   string
}

func GetSubmissionsByAssignment(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if _, err := ownedAssignment(DB, c, id); err != nil {
		sendExamError(c, err)
		return
	}
	rows := []SubmissionRow{}
	err = DB.Model(&Submission{}).Select("submissions.*, users.name AS student_name, users.nis AS student_nis, classes.class_name").Joins("JOIN users ON users.id = submissions.student_id").Joins("LEFT JOIN classes ON classes.id = users.class_id").Where("submissions.assignment_id = ?", id).Order("users.name").Scan(&rows).Error
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func GradeSubmission(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var input struct {
		Score    *int   `json:"score" binding:"required"`
		Feedback string `json:"feedback"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Feedback) > 5000 {
		c.JSON(400, gin.H{"error": "Nilai wajib diisi dan catatan maksimal 5000 karakter."})
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var submission Submission
		if err := tx.First(&submission, id).Error; err != nil {
			return err
		}
		assignment, err := ownedAssignment(tx, c, submission.AssignmentID)
		if err != nil {
			return err
		}
		if *input.Score < 0 || *input.Score > assignment.MaxScore {
			return failExam(400, "Nilai harus berada antara 0 dan skor maksimal tugas.")
		}
		return tx.Model(&submission).Updates(map[string]interface{}{"score": *input.Score, "feedback": strings.TrimSpace(input.Feedback), "graded_at": time.Now().UTC()}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Nilai dan catatan tersimpan."})
}
func CreateExam(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	var input struct {
		SubjectID uint   `json:"subject_id" binding:"required"`
		ClassID   uint   `json:"class_id" binding:"required"`
		Title     string `json:"title" binding:"required"`
		Type      string `json:"type" binding:"required,oneof=UH STS SAS"`
		Date      string `json:"date" binding:"required"`
		Duration  int    `json:"duration" binding:"required,min=1,max=1440"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !validText(input.Title, 255) {
		c.JSON(400, gin.H{"error": "Lengkapi data ujian dengan durasi 1?1440 menit."})
		return
	}
	date, err := time.Parse("2006-01-02", input.Date)
	if err != nil {
		c.JSON(400, gin.H{"error": "Tanggal ujian tidak valid."})
		return
	}
	var exam Exam
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := validateTarget(tx, c, input.SubjectID, input.ClassID); err != nil {
			return err
		}
		exam = Exam{SubjectID: input.SubjectID, ClassID: input.ClassID, Title: strings.TrimSpace(input.Title), Type: input.Type, Date: date, Duration: input.Duration}
		return tx.Create(&exam).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Jadwal ujian dibuat.", "data": exam})
}
func GetExams(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	rows := []struct {
		Exam
		SubjectName    string
		ClassName      string
		QuestionCount  int
		CompletedCount int
	}{}
	query := DB.Model(&Exam{}).Select("exams.*, subjects.subject_name, classes.class_name, (SELECT COUNT(*) FROM questions WHERE questions.exam_id = exams.id) AS question_count, (SELECT COUNT(*) FROM exam_results WHERE exam_results.exam_id = exams.id) AS completed_count").Joins("JOIN subjects ON subjects.id = exams.subject_id").Joins("JOIN classes ON classes.id = exams.class_id")
	if c.GetFloat64("role_id") != 1 {
		query = query.Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	if err := query.Order("exams.date DESC, exams.id DESC").Scan(&rows).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func DeleteExam(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		exam, err := ownedExam(tx, c, id)
		if err != nil {
			return err
		}
		if exam.IsActive {
			return failExam(409, "Tutup ujian sebelum menghapusnya.")
		}
		return tx.Delete(&exam).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Ujian, soal, dan hasilnya dihapus."})
}
