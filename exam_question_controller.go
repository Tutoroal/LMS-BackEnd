package main

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
)

type QuestionInput struct {
	ExamID        uint   `json:"exam_id" binding:"required"`
	QuestionText  string `json:"question_text" binding:"required"`
	OptionA       string `json:"option_a" binding:"required"`
	OptionB       string `json:"option_b" binding:"required"`
	OptionC       string `json:"option_c" binding:"required"`
	OptionD       string `json:"option_d" binding:"required"`
	CorrectAnswer string `json:"correct_answer" binding:"required,oneof=A B C D"`
}

func validQuestion(q Question) bool {
	return validText(q.QuestionText, 10000) && validText(q.OptionA, 255) && validText(q.OptionB, 255) && validText(q.OptionC, 255) && validText(q.OptionD, 255) &&
		(q.CorrectAnswer == "A" || q.CorrectAnswer == "B" || q.CorrectAnswer == "C" || q.CorrectAnswer == "D")
}
func ownedExam(tx *gorm.DB, c *gin.Context, id uint) (Exam, error) {
	var exam Exam
	query := tx.Model(&Exam{}).Where("exams.id = ?", id)
	if c.GetFloat64("role_id") != 1 {
		query = query.Joins("JOIN subjects ON subjects.id = exams.subject_id").Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	err := query.First(&exam).Error
	return exam, err
}
func editableExam(tx *gorm.DB, c *gin.Context, id uint) error {
	exam, err := ownedExam(tx, c, id)
	if err != nil {
		return err
	}
	if exam.IsActive {
		return failExam(409, "Tutup ujian sebelum mengubah soal.")
	}
	var count int64
	if err = tx.Model(&ExamAttempt{}).Where("exam_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return failExam(409, "Soal terkunci karena ujian sudah dikerjakan siswa.")
	}
	return nil
}
func GetQuestionsByExam(c *gin.Context) {
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
	questions := []Question{}
	if err = DB.Where("exam_id = ?", id).Order("id").Find(&questions).Error; err != nil {
		sendExamError(c, err)
		return
	}
	var count int64
	if err := DB.Model(&ExamAttempt{}).Where("exam_id = ?", id).Count(&count).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": questions, "exam": exam, "locked": exam.IsActive || count > 0})
}
func CreateQuestion(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	var input QuestionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Lengkapi soal dan kunci A, B, C, atau D."})
		return
	}
	question := Question{ExamID: input.ExamID, QuestionText: strings.TrimSpace(input.QuestionText), OptionA: strings.TrimSpace(input.OptionA), OptionB: strings.TrimSpace(input.OptionB), OptionC: strings.TrimSpace(input.OptionC), OptionD: strings.TrimSpace(input.OptionD), CorrectAnswer: input.CorrectAnswer}
	if !validQuestion(question) {
		c.JSON(400, gin.H{"error": "Soal dan pilihan tidak boleh kosong."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := editableExam(tx, c, input.ExamID); err != nil {
			return err
		}
		return tx.Create(&question).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(201, gin.H{"message": "Soal berhasil ditambahkan.", "data": question})
}
func DeleteQuestion(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var question Question
		if err := tx.First(&question, id).Error; err != nil {
			return err
		}
		if err := editableExam(tx, c, question.ExamID); err != nil {
			return err
		}
		return tx.Delete(&question).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Soal berhasil dihapus."})
}
func SetExamActive(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var input struct {
		IsActive *bool `json:"is_active" binding:"required"`
	}
	if err = c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Status ujian wajib diisi."})
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		exam, err := ownedExam(tx, c, id)
		if err != nil {
			return err
		}
		if *input.IsActive {
			if exam.Duration < 1 || exam.Duration > 1440 || exam.Date.IsZero() {
				return failExam(400, "Jadwal atau durasi ujian tidak valid.")
			}
			var questions []Question
			if err = tx.Where("exam_id = ?", id).Find(&questions).Error; err != nil {
				return err
			}
			if len(questions) == 0 {
				return failExam(409, "Tambahkan soal sebelum membuka ujian.")
			}
			for _, q := range questions {
				if !validQuestion(q) {
					return failExam(409, "Lengkapi soal dan kunci jawaban.")
				}
			}
		}
		return tx.Model(&exam).Update("is_active", *input.IsActive).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Status ujian diperbarui."})
}
