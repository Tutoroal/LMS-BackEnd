package main

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
	"time"
)

type ScheduleInput struct {
	AcademicYear   string `json:"academic_year"`
	Semester       string `json:"semester"`
	EffectiveFrom  string `json:"effective_from"`
	EffectiveUntil string `json:"effective_until"`
	ClassID        uint   `json:"class_id" binding:"required"`
	SubjectID      uint   `json:"subject_id" binding:"required"`
	DayOfWeek      string `json:"day_of_week" binding:"required,oneof=Senin Selasa Rabu Kamis Jumat Sabtu"`
	StartTime      string `json:"start_time" binding:"required"`
	EndTime        string `json:"end_time" binding:"required"`
}

func normalizeScheduleClock(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 7 && strings.HasSuffix(value, ":00") {
		value = strings.TrimSuffix(value, ":00")
	}
	value = strings.ReplaceAll(value, ".", ":")
	if len(value) == 4 {
		value = "0" + value
	}
	return value
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
	year, semester := schedulePeriod(c.Query("academic_year"), c.Query("semester"))
	date := c.DefaultQuery("date", time.Now().In(schoolZone).Format("2006-01-02"))
	if _, err := time.Parse("2006-01-02", date); err != nil {
		c.JSON(400, gin.H{"error": "Tanggal jadwal tidak valid."})
		return
	}
	query := DB.Where("academic_year = ? AND semester = ?", year, semester).Preload("Class").Preload("Subject.Teacher", func(tx *gorm.DB) *gorm.DB { return tx.Select("id", "name", "email", "role_id") })
	if c.Query("history") != "1" || c.GetFloat64("role_id") == 2 || c.GetFloat64("role_id") == 3 {
		query = query.Where("effective_from <= ? AND effective_until >= ?", date, date)
	}
	if c.Query("class_id") != "" {
		query = query.Where("class_id = ?", c.Query("class_id"))
	}
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
	var periods []struct {
		AcademicYear string
		Semester     string
	}
	if err := DB.Model(&Schedule{}).Select("DISTINCT academic_year,semester").Order("academic_year DESC, semester").Scan(&periods).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": schedules, "academic_year": year, "semester": semester, "periods": periods})
}
func CreateSchedule(c *gin.Context) { saveSchedule(c, false) }
func UpdateSchedule(c *gin.Context) { saveSchedule(c, true) }
func saveSchedule(c *gin.Context, update bool) {
	if !isAdmin(c) {
		return
	}
	var input ScheduleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Periksa pilihan kelas, mata pelajaran, dan hari. Semua pilihan tersebut wajib dikirim."})
		return
	}
	input.StartTime = normalizeScheduleClock(input.StartTime)
	input.EndTime = normalizeScheduleClock(input.EndTime)
	if !validClock(input.StartTime) {
		c.JSON(400, gin.H{"error": "Jam mulai tidak valid. Gunakan jam dan menit, contoh 06:30."})
		return
	}
	if !validClock(input.EndTime) {
		c.JSON(400, gin.H{"error": "Jam selesai tidak valid. Gunakan jam dan menit, contoh 07:10."})
		return
	}
	if input.StartTime >= input.EndTime {
		c.JSON(400, gin.H{"error": "Jam selesai harus setelah jam mulai."})
		return
	}
	input.AcademicYear, input.Semester = schedulePeriod(input.AcademicYear, input.Semester)
	if !validText(input.AcademicYear, 50) || (input.Semester != "Ganjil" && input.Semester != "Genap") {
		c.JSON(400, gin.H{"error": "Tahun ajaran dan semester tidak valid."})
		return
	}
	if input.EffectiveFrom == "" {
		input.EffectiveFrom = "2000-01-01"
	}
	if input.EffectiveUntil == "" {
		input.EffectiveUntil = "9999-12-31"
	}
	if !validScheduleDate(input.EffectiveFrom) || !validScheduleDate(input.EffectiveUntil) || input.EffectiveUntil < input.EffectiveFrom {
		c.JSON(400, gin.H{"error": "Rentang tanggal berlaku tidak valid."})
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
		if update && current.AcademicYear == input.AcademicYear && current.Semester == input.Semester && input.EffectiveFrom > current.EffectiveFrom {
			if input.EffectiveFrom > current.EffectiveUntil {
				return failExam(400, "Tanggal revisi di luar rentang jadwal lama.")
			}
			from, _ := time.Parse("2006-01-02", input.EffectiveFrom)
			until := from.AddDate(0, 0, -1).Format("2006-01-02")
			if err := tx.Model(&current).Update("effective_until", until).Error; err != nil {
				return err
			}
			update = false
		}
		var count int64
		err := tx.Model(&Schedule{}).Joins("JOIN subjects ON subjects.id = schedules.subject_id").Where("schedules.academic_year = ? AND schedules.semester = ? AND schedules.effective_from <= ? AND schedules.effective_until >= ? AND schedules.id <> ? AND schedules.day_of_week = ? AND schedules.start_time < ? AND schedules.end_time > ? AND (schedules.class_id = ? OR subjects.teacher_id = ?)", input.AcademicYear, input.Semester, input.EffectiveUntil, input.EffectiveFrom, id, input.DayOfWeek, input.EndTime, input.StartTime, input.ClassID, subject.TeacherID).Count(&count).Error
		if err != nil {
			return err
		}
		if count > 0 {
			return failExam(409, "Jadwal bentrok dengan kelas atau guru pada waktu yang sama.")
		}
		if !update {
			return tx.Create(&Schedule{AcademicYear: input.AcademicYear, Semester: input.Semester, EffectiveFrom: input.EffectiveFrom, EffectiveUntil: input.EffectiveUntil, ClassID: input.ClassID, SubjectID: input.SubjectID, DayOfWeek: input.DayOfWeek, StartTime: input.StartTime, EndTime: input.EndTime}).Error
		}
		return tx.Model(&current).Updates(map[string]interface{}{"academic_year": input.AcademicYear, "semester": input.Semester, "effective_from": input.EffectiveFrom, "effective_until": input.EffectiveUntil, "class_id": input.ClassID, "subject_id": input.SubjectID, "day_of_week": input.DayOfWeek, "start_time": input.StartTime, "end_time": input.EndTime}).Error
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

func validScheduleDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}
func schedulePeriod(year, semester string) (string, string) {
	if year == "" {
		var settings SchoolSettings
		DB.Where("id = 1").First(&settings)
		year = settings.AcademicYear
		if year == "" {
			year = "2026/2027 - Ganjil"
		}
	}
	if semester == "" {
		semester = "Ganjil"
		if strings.Contains(strings.ToLower(year), "genap") {
			semester = "Genap"
		}
	}
	return strings.TrimSpace(year), semester
}
