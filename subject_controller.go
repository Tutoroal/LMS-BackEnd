package main

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
)

func GetSubjects(c *gin.Context) {
	subjects := []Subject{}
	query := DB.Preload("Teacher", func(tx *gorm.DB) *gorm.DB { return tx.Select("id", "name", "email", "role_id") }).Order("subject_name")
	if c.GetFloat64("role_id") == 2 {
		query = query.Where("teacher_id = ?", c.GetString("user_id"))
	}
	if c.GetFloat64("role_id") == 3 {
		student, err := studentAccount(DB, c)
		if err != nil {
			sendExamError(c, err)
			return
		}
		if student.ClassID == nil {
			c.JSON(200, gin.H{"data": subjects})
			return
		}
		query = query.Where("id IN (SELECT subject_id FROM schedules WHERE class_id = ? UNION SELECT subject_id FROM materials WHERE class_id = ? UNION SELECT subject_id FROM assignments WHERE class_id = ? UNION SELECT subject_id FROM exams WHERE class_id = ?)", *student.ClassID, *student.ClassID, *student.ClassID, *student.ClassID)
	}
	if err := query.Find(&subjects).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": subjects})
}
func CreateSubject(c *gin.Context) { saveSubject(c, false) }
func UpdateSubject(c *gin.Context) { saveSubject(c, true) }
func saveSubject(c *gin.Context, update bool) {
	if !isAdmin(c) {
		return
	}
	var input struct {
		SubjectName string `json:"subject_name" binding:"required"`
		TeacherID   string `json:"teacher_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !validText(input.SubjectName, 100) {
		c.JSON(400, gin.H{"error": "Nama mapel dan guru pengampu wajib diisi."})
		return
	}
	var subject Subject
	err := DB.Transaction(func(tx *gorm.DB) error {
		var teacher User
		if err := tx.Where("id = ? AND role_id = 2", input.TeacherID).First(&teacher).Error; err != nil {
			return failExam(400, "Pilih guru pengampu yang valid.")
		}
		if !update {
			subject = Subject{SubjectName: strings.TrimSpace(input.SubjectName), TeacherID: input.TeacherID}
			return tx.Create(&subject).Error
		}
		id, err := examID(c)
		if err != nil {
			return err
		}
		if err := tx.First(&subject, id).Error; err != nil {
			return err
		}
		return tx.Model(&subject).Updates(map[string]interface{}{"subject_name": strings.TrimSpace(input.SubjectName), "teacher_id": input.TeacherID}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Mata pelajaran tersimpan.", "data": subject})
}
func DeleteSubject(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var subject Subject
		if err := tx.First(&subject, id).Error; err != nil {
			return err
		}
		if err := tx.Where("subject_id = ?", id).Delete(&Schedule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("subject_id = ?", id).Delete(&Material{}).Error; err != nil {
			return err
		}
		tasks := tx.Model(&Assignment{}).Select("id").Where("subject_id = ?", id)
		if err := tx.Where("assignment_id IN (?)", tasks).Delete(&Submission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("subject_id = ?", id).Delete(&Assignment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("subject_id = ?", id).Delete(&Exam{}).Error; err != nil {
			return err
		}
		return tx.Delete(&subject).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Mapel, jadwal, materi, tugas, dan ujian terkait dihapus."})
}
