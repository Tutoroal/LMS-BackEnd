package main

import (
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
	"time"
)

type TeachingAllocation struct {
	ID        uint    `gorm:"primaryKey"`
	SubjectID uint    `gorm:"uniqueIndex:idx_allocation"`
	Subject   Subject `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	ClassID   uint    `gorm:"uniqueIndex:idx_allocation"`
	Class     Class   `gorm:"constraint:OnDelete:CASCADE;"`
}

func migrateAcademicRevision() error {
	var settings SchoolSettings
	err := DB.Where("id = 1").First(&settings).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		settings = SchoolSettings{ID: 1, SchoolName: "SMK Citra Negara", AcademicYear: "2026/2027 - Ganjil"}
		if err := DB.Create(&settings).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	semester := "Ganjil"
	if strings.Contains(strings.ToLower(settings.AcademicYear), "genap") {
		semester = "Genap"
	}
	if err := DB.Model(&Schedule{}).Where("academic_year IS NULL OR academic_year = ''").Updates(map[string]interface{}{"academic_year": settings.AcademicYear, "semester": semester, "effective_from": "2000-01-01", "effective_until": "9999-12-31"}).Error; err != nil {
		return err
	}
	if err := DB.Exec("INSERT OR IGNORE INTO teaching_allocations(subject_id,class_id) SELECT targets.subject_id,targets.class_id FROM (SELECT subject_id,class_id FROM schedules UNION SELECT subject_id,class_id FROM materials UNION SELECT subject_id,class_id FROM assignments UNION SELECT subject_id,class_id FROM exams) AS targets JOIN subjects ON subjects.id=targets.subject_id WHERE subjects.allocation_configured=false").Error; err != nil {
		return err
	}
	if err := DB.Exec("UPDATE subjects SET allocation_configured=true WHERE allocation_configured=false AND EXISTS(SELECT 1 FROM teaching_allocations WHERE teaching_allocations.subject_id=subjects.id)").Error; err != nil {
		return err
	}
	for _, sql := range []string{
		"CREATE INDEX IF NOT EXISTS idx_users_role_class ON users(role_id,class_id)",
		"CREATE INDEX IF NOT EXISTS idx_assignment_class_deadline ON assignments(class_id, deadline)",
		"CREATE INDEX IF NOT EXISTS idx_material_class ON materials(class_id)",
		"CREATE INDEX IF NOT EXISTS idx_exam_class ON exams(class_id)",
		"CREATE INDEX IF NOT EXISTS idx_users_role_class ON users(role_id,class_id)",
		"CREATE INDEX IF NOT EXISTS idx_subject_teacher ON subjects(teacher_id)",
		"CREATE INDEX IF NOT EXISTS idx_submission_dates ON submissions(submitted_at,graded_at)",
	} {
		if err := DB.Exec(sql).Error; err != nil {
			return err
		}
	}
	return nil
}
func validAnswerFormats(value string) bool {
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		if seen[part] {
			return false
		}
		seen[part] = true
		switch part {
		case "link", "pdf", "image", "document":
		default:
			return false
		}
	}
	return len(seen) > 0
}
func GetAllocations(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	rows := []TeachingAllocation{}
	if err := DB.Preload("Class").Order("subject_id,class_id").Find(&rows).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func replaceAllocations(tx *gorm.DB, subjectID uint, classIDs []uint) error {
	seen := map[uint]bool{}
	for _, id := range classIDs {
		if id == 0 || seen[id] {
			return failExam(400, "Kelas penugasan tidak valid atau duplikat.")
		}
		seen[id] = true
		var class Class
		if err := tx.First(&class, id).Error; err != nil {
			return failExam(400, "Kelas penugasan tidak ditemukan.")
		}
	}
	if err := tx.Where("subject_id = ?", subjectID).Delete(&TeachingAllocation{}).Error; err != nil {
		return err
	}
	for _, id := range classIDs {
		if err := tx.Create(&TeachingAllocation{SubjectID: subjectID, ClassID: id}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&Subject{}).Where("id=?", subjectID).Update("allocation_configured", true).Error
}
func SaveAllocations(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	var input struct {
		SubjectID uint   `json:"subject_id" binding:"required"`
		ClassIDs  []uint `json:"class_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Data penugasan tidak valid."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var subject Subject
		if err := tx.First(&subject, input.SubjectID).Error; err != nil {
			return err
		}
		return replaceAllocations(tx, subject.ID, input.ClassIDs)
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Penugasan mapel dan kelas tersimpan."})
}
func MonitorOverview(c *gin.Context) {
	if !hasRole(c, 1, 4, 5) {
		return
	}
	counts := map[string]int64{}
	for key, model := range map[string]interface{}{"classes": &Class{}, "subjects": &Subject{}, "materials": &Material{}, "assignments": &Assignment{}, "submissions": &Submission{}, "exams": &Exam{}} {
		var n int64
		if err := DB.Model(model).Count(&n).Error; err != nil {
			sendExamError(c, err)
			return
		}
		counts[key] = n
	}
	for key, role := range map[string]int{"students": 3, "teachers": 2} {
		var n int64
		if err := DB.Model(&User{}).Where("role_id = ?", role).Count(&n).Error; err != nil {
			sendExamError(c, err)
			return
		}
		counts[key] = n
	}
	var pending int64
	if err := DB.Model(&Submission{}).Where("graded_at IS NULL").Count(&pending).Error; err != nil {
		sendExamError(c, err)
		return
	}
	counts["pending_grades"] = pending
	c.JSON(200, gin.H{"counts": counts, "at": time.Now().UTC()})
}
func MonitorClasses(c *gin.Context) {
	if !hasRole(c, 1, 4, 5) {
		return
	}
	rows := []struct {
		Class
		Students    int64
		Materials   int64
		Assignments int64
		Exams       int64
	}{}
	err := DB.Model(&Class{}).Select("classes.*, (SELECT COUNT(*) FROM users WHERE users.class_id=classes.id AND users.role_id=3) AS students,(SELECT COUNT(*) FROM materials WHERE materials.class_id=classes.id) AS materials,(SELECT COUNT(*) FROM assignments WHERE assignments.class_id=classes.id) AS assignments,(SELECT COUNT(*) FROM exams WHERE exams.class_id=classes.id) AS exams").Order("classes.class_name").Scan(&rows).Error
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func MonitorTeachers(c *gin.Context) {
	if !hasRole(c, 1, 4, 5) {
		return
	}
	rows := []struct {
		ID            string
		Name          string
		Subjects      int64
		Materials     int64
		Assignments   int64
		Exams         int64
		Submissions   int64
		PendingGrades int64
	}{}
	err := DB.Model(&User{}).Select("users.id,users.name,(SELECT COUNT(*) FROM subjects WHERE subjects.teacher_id=users.id) AS subjects,(SELECT COUNT(*) FROM materials JOIN subjects ON subjects.id=materials.subject_id WHERE subjects.teacher_id=users.id) AS materials,(SELECT COUNT(*) FROM assignments JOIN subjects ON subjects.id=assignments.subject_id WHERE subjects.teacher_id=users.id) AS assignments,(SELECT COUNT(*) FROM exams JOIN subjects ON subjects.id=exams.subject_id WHERE subjects.teacher_id=users.id) AS exams,(SELECT COUNT(*) FROM submissions JOIN assignments ON assignments.id=submissions.assignment_id JOIN subjects ON subjects.id=assignments.subject_id WHERE subjects.teacher_id=users.id) AS submissions,(SELECT COUNT(*) FROM submissions JOIN assignments ON assignments.id=submissions.assignment_id JOIN subjects ON subjects.id=assignments.subject_id WHERE subjects.teacher_id=users.id AND submissions.graded_at IS NULL) AS pending_grades").Where("users.role_id = 2").Order("users.name").Scan(&rows).Error
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}

func attachSubjectClasses(subjects []Subject) error {
	ids := []uint{}
	for _, subject := range subjects {
		ids = append(ids, subject.ID)
	}
	rows := []TeachingAllocation{}
	if len(ids) > 0 {
		if err := DB.Where("subject_id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
	}
	mapping := map[uint][]uint{}
	for _, row := range rows {
		mapping[row.SubjectID] = append(mapping[row.SubjectID], row.ClassID)
	}
	for i := range subjects {
		subjects[i].ClassIDs = mapping[subjects[i].ID]
		if subjects[i].ClassIDs == nil {
			subjects[i].ClassIDs = []uint{}
		}
	}
	return nil
}
