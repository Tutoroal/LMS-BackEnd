package main

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
)

type ScheduleInput struct {
	ClassID   uint   `json:"class_id" binding:"required"`
	SubjectID uint   `json:"subject_id" binding:"required"`
	DayOfWeek string `json:"day_of_week" binding:"required,oneof=Senin Selasa Rabu Kamis Jumat Sabtu"`
	StartTime string `json:"start_time" binding:"required"`
	EndTime   string `json:"end_time" binding:"required"`
}

func validClock(clock string) bool {
	if len(clock) != 5 || clock[2] != ':' {
		return false
	}
	for _, index := range []int{0, 1, 3, 4} {
		if clock[index] < '0' || clock[index] > '9' {
			return false
		}
	}
	return clock < "24:00" && clock[3:] < "60"
}
func GetSchedules(c *gin.Context) {
	schedules := []Schedule{}
	query := DB.Preload("Class").Preload("Subject.Teacher", func(tx *gorm.DB) *gorm.DB { return tx.Select("id", "name", "email", "role_id") })
	if c.GetFloat64("role_id") == 2 {
		query = query.Where("subject_id IN (SELECT id FROM subjects WHERE teacher_id = ?)", c.GetString("user_id"))
	}
	if c.GetFloat64("role_id") == 3 {
		student, err := studentAccount(DB, c)
		if err != nil {
			sendExamError(c, err)
			return
		}
		if student.ClassID == nil {
			c.JSON(200, gin.H{"data": schedules})
			return
		}
		query = query.Where("class_id = ?", *student.ClassID)
	}
	if err := query.Order("CASE day_of_week WHEN 'Senin' THEN 1 WHEN 'Selasa' THEN 2 WHEN 'Rabu' THEN 3 WHEN 'Kamis' THEN 4 WHEN 'Jumat' THEN 5 ELSE 6 END, start_time").Find(&schedules).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": schedules})
}
func CreateSchedule(c *gin.Context) { saveSchedule(c, false) }
func UpdateSchedule(c *gin.Context) { saveSchedule(c, true) }
func saveSchedule(c *gin.Context, update bool) {
	if !isAdmin(c) {
		return
	}
	var input ScheduleInput
	if err := c.ShouldBindJSON(&input); err != nil || !validClock(input.StartTime) || !validClock(input.EndTime) || input.StartTime >= input.EndTime {
		c.JSON(400, gin.H{"error": "Pilih kelas, mapel, hari, dan waktu mulai sebelum waktu selesai."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := validateTarget(tx, c, input.SubjectID, input.ClassID); err != nil {
			return err
		}
		var subject Subject
		if err := tx.First(&subject, input.SubjectID).Error; err != nil {
			return err
		}
		var current Schedule
		var id uint
		if update {
			var err error
			id, err = examID(c)
			if err != nil {
				return err
			}
			if err := tx.First(&current, id).Error; err != nil {
				return err
			}
		}
		var count int64
		err := tx.Model(&Schedule{}).Joins("JOIN subjects ON subjects.id = schedules.subject_id").Where("schedules.id <> ? AND schedules.day_of_week = ? AND schedules.start_time < ? AND schedules.end_time > ? AND (schedules.class_id = ? OR subjects.teacher_id = ?)", id, input.DayOfWeek, input.EndTime, input.StartTime, input.ClassID, subject.TeacherID).Count(&count).Error
		if err != nil {
			return err
		}
		if count > 0 {
			return failExam(409, "Jadwal bentrok dengan kelas atau guru pada waktu yang sama.")
		}
		if !update {
			return tx.Create(&Schedule{ClassID: input.ClassID, SubjectID: input.SubjectID, DayOfWeek: input.DayOfWeek, StartTime: input.StartTime, EndTime: input.EndTime}).Error
		}
		return tx.Model(&current).Updates(map[string]interface{}{"class_id": input.ClassID, "subject_id": input.SubjectID, "day_of_week": input.DayOfWeek, "start_time": input.StartTime, "end_time": input.EndTime}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Jadwal tersimpan."})
}
func DeleteSchedule(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var schedule Schedule
	if err := DB.First(&schedule, id).Error; err != nil {
		sendExamError(c, err)
		return
	}
	if err := DB.Delete(&schedule).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Jadwal dihapus."})
}
func departmentFor(name string) string {
	for _, major := range departments {
		if strings.Contains(strings.ToUpper(name), major) {
			return major
		}
	}
	return ""
}
