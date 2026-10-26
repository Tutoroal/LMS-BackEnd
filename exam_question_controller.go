package main

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
)

type QuestionInput struct {
	ExamID        uint    `json:"exam_id"`
	QuestionText  string  `json:"question_text"`
	Kind          string  `json:"kind"`
	Points        float64 `json:"points"`
	ImageAssetID  *uint   `json:"image_asset_id"`
	OptionA       string  `json:"option_a"`
	OptionB       string  `json:"option_b"`
	OptionC       string  `json:"option_c"`
	OptionD       string  `json:"option_d"`
	OptionE       string  `json:"option_e"`
	CorrectAnswer string  `json:"correct_answer"`
}

func questionPoints(q Question) float64 {
	if q.Points == 0 {
		return 1
	}
	return q.Points
}
func validQuestion(q Question) bool {
	if !validText(q.QuestionText, 10000) || questionPoints(q) <= 0 || questionPoints(q) > 1000 {
		return false
	}
	if q.Kind == "essay" {
		return pureEssay(q) && len(q.CorrectAnswer) <= 10000
	}
	if q.Kind != "" && q.Kind != "mcq" {
		return false
	}
	if !validText(q.OptionA, 2000) || !validText(q.OptionB, 2000) || !validText(q.OptionC, 2000) || !validText(q.OptionD, 2000) || len(q.OptionE) > 2000 {
		return false
	}
	return q.CorrectAnswer == "A" || q.CorrectAnswer == "B" || q.CorrectAnswer == "C" || q.CorrectAnswer == "D" || (q.CorrectAnswer == "E" && validText(q.OptionE, 2000))
}
func questionFromInput(c *gin.Context, input QuestionInput) (Question, error) {
	if input.Kind == "" {
		input.Kind = "mcq"
	}
	if input.Points == 0 {
		input.Points = 1
	}
	q := Question{ExamID: input.ExamID, QuestionText: strings.TrimSpace(input.QuestionText), Kind: input.Kind, Points: input.Points, ImageAssetID: input.ImageAssetID, OptionA: strings.TrimSpace(input.OptionA), OptionB: strings.TrimSpace(input.OptionB), OptionC: strings.TrimSpace(input.OptionC), OptionD: strings.TrimSpace(input.OptionD), OptionE: strings.TrimSpace(input.OptionE), CorrectAnswer: strings.TrimSpace(input.CorrectAnswer)}
	if !validQuestion(q) {
		return q, failExam(400, "Lengkapi pertanyaan, bobot, pilihan dan kunci jawaban yang sesuai.")
	}
	return q, nil
}
func checkQuestionImage(tx *gorm.DB, c *gin.Context, q Question) error {
	if q.ImageAssetID == nil {
		return nil
	}
	var count int64
	if err := tx.Model(&MediaAsset{}).Where("id=? AND exam_id=? AND ready=true AND content_type LIKE 'image/%'", *q.ImageAssetID, q.ExamID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return failExam(403, "Gambar soal tidak tersedia untuk ujian ini.")
	}
	return nil
}
func ownedExam(tx *gorm.DB, c *gin.Context, id uint) (Exam, error) {
	var exam Exam
	query := tx.Model(&Exam{}).Where("exams.id = ?", id)
	if restrictTeacher(c) {
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
	c.JSON(200, gin.H{"data": questions, "exam": exam, "locked": exam.IsActive || count > 0, "import_warning": importWarning(questions)})
}
func CreateQuestion(c *gin.Context) { saveQuestion(c, false) }
func UpdateQuestion(c *gin.Context) { saveQuestion(c, true) }
func saveQuestion(c *gin.Context, update bool) {
	if !isGuruOrAdmin(c) {
		return
	}
	var input QuestionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Data soal tidak valid."})
		return
	}
	q, err := questionFromInput(c, input)
	if err != nil {
		sendExamError(c, err)
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if update {
			id, err := examID(c)
			if err != nil {
				return err
			}
			var current Question
			if err := tx.First(&current, id).Error; err != nil {
				return err
			}
			if q.ExamID != current.ExamID {
				return failExam(400, "Ujian soal tidak dapat dipindahkan.")
			}
			q.ID = id
		}
		if q.ExamID == 0 {
			return failExam(400, "Pilih ujian.")
		}
		if err := editableExam(tx, c, q.ExamID); err != nil {
			return err
		}
		if err := checkQuestionImage(tx, c, q); err != nil {
			return err
		}
		if update {
			return tx.Save(&q).Error
		}
		return tx.Create(&q).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(201, gin.H{"message": "Soal tersimpan.", "data": q})
}
func ImportQuestions(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var input struct {
		Questions []QuestionInput `json:"questions"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Questions) == 0 || len(input.Questions) > 200 {
		c.JSON(400, gin.H{"error": "Import 1-200 soal yang sudah diperiksa."})
		return
	}
	questions := []Question{}
	for _, row := range input.Questions {
		row.ExamID = id
		q, err := questionFromInput(c, row)
		if err != nil {
			sendExamError(c, err)
			return
		}
		questions = append(questions, q)
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := editableExam(tx, c, id); err != nil {
			return err
		}
		for _, q := range questions {
			if err := checkQuestionImage(tx, c, q); err != nil {
				return err
			}
		}
		return tx.Create(&questions).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(201, gin.H{"message": "Semua soal hasil pemeriksaan berhasil diimport.", "data": questions})
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
