package main

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"time"
)

func GetStudentExams(c *gin.Context) {
	if !isSiswa(c) {
		return
	}
	if err := finalizeExpiredAttempts(time.Now().UTC()); err != nil {
		sendExamError(c, err)
		return
	}
	var student User
	if err := DB.First(&student, "id = ?", c.GetString("user_id")).Error; err != nil {
		sendExamError(c, err)
		return
	}
	response := make([]gin.H, 0)
	if student.ClassID == nil {
		c.JSON(200, gin.H{"data": response})
		return
	}
	var exams []Exam
	if err := DB.Where("class_id = ?", *student.ClassID).Order("date DESC, id DESC").Find(&exams).Error; err != nil {
		sendExamError(c, err)
		return
	}
	for _, exam := range exams {
		var subject Subject
		if err := DB.First(&subject, exam.SubjectID).Error; err != nil {
			sendExamError(c, err)
			return
		}
		var result ExamResult
		err := DB.Where("exam_id = ? AND student_id = ?", exam.ID, student.ID).First(&result).Error
		completed := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			sendExamError(c, err)
			return
		}
		var attempt ExamAttempt
		err = DB.Where("exam_id = ? AND student_id = ?", exam.ID, student.ID).First(&attempt).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			sendExamError(c, err)
			return
		}
		var count int64
		if err := DB.Model(&Question{}).Where("exam_id = ?", exam.ID).Count(&count).Error; err != nil {
			sendExamError(c, err)
			return
		}
		response = append(response, gin.H{"exam": exam, "subject_name": subject.SubjectName, "has_completed": completed, "result": result,
			"has_started": attempt.ID != 0, "can_start": !completed && (attempt.ID != 0 || (examAvailable(exam, time.Now()) && count > 0))})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"data": response})
}
