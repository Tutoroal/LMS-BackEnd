package main

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"math"
)

func GetExamEssays(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if _, err := ownedExam(DB, c, id); err != nil {
		sendExamError(c, err)
		return
	}
	attempts := []ExamAttempt{}
	if err := DB.Where("exam_id=? AND submitted_at IS NOT NULL", id).Preload("Student", func(tx *gorm.DB) *gorm.DB { return tx.Select("id", "name", "nis") }).Order("submitted_at").Find(&attempts).Error; err != nil {
		sendExamError(c, err)
		return
	}
	rows := []gin.H{}
	for _, attempt := range attempts {
		questions, answers, err := attemptData(attempt)
		if err != nil {
			sendExamError(c, err)
			return
		}
		if warning := importWarning(questions); warning != "" {
			c.JSON(200, gin.H{"data": []gin.H{}, "import_warning": warning})
			return
		}
		essays := []Question{}
		for _, q := range questions {
			if pureEssay(q) {
				essays = append(essays, q)
			}
		}
		if len(essays) == 0 {
			continue
		}
		grades := map[uint]float64{}
		if attempt.EssayGradesJSON != "" {
			if err := json.Unmarshal([]byte(attempt.EssayGradesJSON), &grades); err != nil {
				sendExamError(c, err)
				return
			}
		}
		rows = append(rows, gin.H{"student_id": attempt.StudentID, "student_name": attempt.Student.Name, "student_nis": attempt.Student.NIS, "questions": essays, "answers": answers, "grades": grades})
	}
	c.JSON(200, gin.H{"data": rows})
}
func GradeExamEssays(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var input struct {
		Grades map[uint]float64 `json:"grades"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Grades == nil {
		c.JSON(400, gin.H{"error": "Isi nilai setiap soal esai."})
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if _, err := ownedExam(tx, c, id); err != nil {
			return err
		}
		var attempt ExamAttempt
		if err := tx.Where("exam_id=? AND student_id=? AND submitted_at IS NOT NULL", id, c.Param("student_id")).First(&attempt).Error; err != nil {
			return err
		}
		questions, answers, err := attemptData(attempt)
		if err != nil {
			return err
		}
		if warning := importWarning(questions); warning != "" {
			return failExam(409, warning)
		}
		score, total := 0.0, 0.0
		essayCount := 0
		for _, q := range questions {
			points := questionPoints(q)
			total += points
			if q.Kind == "essay" {
				if !pureEssay(q) {
					return failExam(400, "Soal pilihan ganda tidak dapat dinilai sebagai esai.")
				}
				essayCount++
				grade, exists := input.Grades[q.ID]
				if !exists || math.IsNaN(grade) || math.IsInf(grade, 0) || grade < 0 || grade > points {
					return failExam(400, "Nilai setiap esai harus diisi antara 0 dan bobot soal.")
				}
				score += grade
			} else if answers[q.ID] == q.CorrectAnswer {
				score += points
			}
		}
		if essayCount == 0 || essayCount != len(input.Grades) {
			return failExam(400, "ID soal esai tidak valid.")
		}
		encoded, err := json.Marshal(input.Grades)
		if err != nil {
			return err
		}
		if err := tx.Model(&attempt).Update("essay_grades_json", string(encoded)).Error; err != nil {
			return err
		}
		return tx.Model(&ExamResult{}).Where("exam_id=? AND student_id=?", id, attempt.StudentID).Updates(map[string]interface{}{"score": score * 100 / total, "pending_grading": false}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Nilai esai tersimpan dan nilai ujian diperbarui."})
}
