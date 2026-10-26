package main

import (
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var schoolZone = time.FixedZone("WIB", 7*60*60)
var departments = []string{"PPLG", "TJKT", "DKV", "BDR", "PERHOTELAN", "MPLB"}

func hasRole(c *gin.Context, roles ...uint) bool {
	role := c.GetFloat64("role_id")
	for _, allowed := range roles {
		if role == float64(allowed) {
			return true
		}
	}
	c.AbortWithStatusJSON(403, gin.H{"error": "Anda tidak memiliki hak akses untuk tindakan ini."})
	return false
}
func isAdmin(c *gin.Context) bool          { return hasRole(c, 1) }
func isSuperAdmin(c *gin.Context) bool     { return hasRole(c, 6) }
func isAccountManager(c *gin.Context) bool { return hasRole(c, 1, 6) }
func mayManageRole(c *gin.Context, role uint) bool {
	requester := uint(c.GetFloat64("role_id"))
	return (requester == 6 && role == 1) || (requester == 1 && role >= 2 && role <= 5)
}
func isSiswa(c *gin.Context) bool { return hasRole(c, 3) }
func isGuruOrAdmin(c *gin.Context) bool {
	if c.Request.Method == "GET" {
		return hasRole(c, 1, 2, 4, 5)
	}
	return hasRole(c, 1, 2)
}
func restrictTeacher(c *gin.Context) bool { return c.GetFloat64("role_id") == 2 }
func validText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len([]rune(value)) <= max
}
func validURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil && len(value) <= 4096
}
func normalizedEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 {
		return "", failExam(400, "Format email tidak valid.")
	}
	return email, nil
}
func validatePassword(value string) error {
	if len(value) < 8 || len(value) > 72 {
		return failExam(400, "Kata sandi harus memiliki 8?72 byte.")
	}
	return nil
}
func ownedSubject(tx *gorm.DB, c *gin.Context, id uint) (Subject, error) {
	var subject Subject
	query := tx.Where("id = ?", id)
	if restrictTeacher(c) {
		query = query.Where("teacher_id = ?", c.GetString("user_id"))
	}
	err := query.First(&subject).Error
	return subject, err
}
func ownedAssignment(tx *gorm.DB, c *gin.Context, id uint) (Assignment, error) {
	var assignment Assignment
	query := tx.Model(&Assignment{}).Where("assignments.id = ?", id)
	if restrictTeacher(c) {
		query = query.Joins("JOIN subjects ON subjects.id = assignments.subject_id").Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	err := query.First(&assignment).Error
	return assignment, err
}
func ownedMaterial(tx *gorm.DB, c *gin.Context, id uint) (Material, error) {
	var material Material
	query := tx.Model(&Material{}).Where("materials.id = ?", id)
	if restrictTeacher(c) {
		query = query.Joins("JOIN subjects ON subjects.id = materials.subject_id").Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	err := query.First(&material).Error
	return material, err
}
func validateTarget(tx *gorm.DB, c *gin.Context, subjectID, classID uint) error {
	subject, err := ownedSubject(tx, c, subjectID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return failExam(403, "Anda tidak mengampu mata pelajaran ini.")
		}
		return err
	}
	var class Class
	if err := tx.First(&class, classID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return failExam(400, "Kelas tujuan tidak ditemukan.")
		}
		return err
	}
	{
		var allocationCount int64
		if err := tx.Model(&TeachingAllocation{}).Where("subject_id = ? AND class_id = ?", subjectID, classID).Count(&allocationCount).Error; err != nil {
			return err
		}
		var total int64
		if err := tx.Model(&TeachingAllocation{}).Where("subject_id = ?", subjectID).Count(&total).Error; err != nil {
			return err
		}
		if (total > 0 || subject.AllocationConfigured) && allocationCount == 0 {
			return failExam(403, "Kelas ini belum ditugaskan untuk mapel Anda.")
		}
	}
	return nil
}
func deadlineFromInput(value string) (time.Time, error) {
	if len(value) == 10 {
		date, err := time.ParseInLocation("2006-01-02", value, schoolZone)
		if err != nil {
			return time.Time{}, failExam(400, "Tanggal tenggat tidak valid.")
		}
		return date.AddDate(0, 0, 1).Add(-time.Nanosecond).UTC(), nil
	}
	deadline, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, failExam(400, "Format tenggat tidak valid.")
	}
	return deadline.UTC(), nil
}
func studentAccount(tx *gorm.DB, c *gin.Context) (User, error) {
	var student User
	err := tx.First(&student, "id = ? AND role_id = 3", c.GetString("user_id")).Error
	return student, err
}
