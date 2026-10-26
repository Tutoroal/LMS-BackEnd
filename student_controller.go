package main

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strings"
	"time"
)

type SubmissionInput struct {
	AssignmentID  uint   `json:"assignment_id" binding:"required"`
	FileURL       string `json:"file_url"`
	AttachmentIDs []uint `json:"attachment_ids"`
}

func GetStudentMaterials(c *gin.Context) {
	if !isSiswa(c) {
		return
	}
	student, err := studentAccount(DB, c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	rows := []MaterialRow{}
	if student.ClassID != nil {
		err = DB.Model(&Material{}).Select("materials.*, classes.class_name, subjects.subject_name").Joins("JOIN classes ON classes.id = materials.class_id").Joins("JOIN subjects ON subjects.id = materials.subject_id").Where("materials.class_id = ?", *student.ClassID).Order("materials.id DESC").Scan(&rows).Error
	}
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func GetStudentAssignments(c *gin.Context) {
	if !isSiswa(c) {
		return
	}
	student, err := studentAccount(DB, c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	response := []gin.H{}
	if student.ClassID == nil {
		c.JSON(200, gin.H{"data": response})
		return
	}
	var assignments []Assignment
	if err := DB.Preload("Subject").Where("class_id = ?", *student.ClassID).Order("deadline, id DESC").Find(&assignments).Error; err != nil {
		sendExamError(c, err)
		return
	}
	var submissions []Submission
	if err := DB.Where("student_id = ? AND assignment_id IN (?)", student.ID, DB.Model(&Assignment{}).Select("id").Where("class_id = ?", *student.ClassID)).Find(&submissions).Error; err != nil {
		sendExamError(c, err)
		return
	}
	byAssignment := make(map[uint]Submission, len(submissions))
	for _, submission := range submissions {
		byAssignment[submission.AssignmentID] = submission
	}
	for _, task := range assignments {
		submission, hasSubmitted := byAssignment[task.ID]
		response = append(response, gin.H{"assignment": task, "subject_name": task.Subject.SubjectName, "has_submitted": hasSubmitted, "submission": submission, "is_graded": submission.GradedAt != nil, "can_submit": submission.GradedAt == nil && time.Now().Before(task.Deadline)})
	}
	c.JSON(200, gin.H{"data": response})
}
func SubmitAssignment(c *gin.Context) {
	if !isSiswa(c) {
		return
	}
	var input SubmissionInput
	if err := c.ShouldBindJSON(&input); err != nil || (input.FileURL != "" && !validURL(input.FileURL)) || len(input.AttachmentIDs) > 5 || (input.FileURL == "" && len(input.AttachmentIDs) == 0) {
		c.JSON(400, gin.H{"error": "Pilih tugas dan masukkan URL http/https yang valid."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		student, err := studentAccount(tx, c)
		if err != nil {
			return err
		}
		var assignment Assignment
		if student.ClassID == nil {
			return failExam(403, "Anda belum memiliki kelas.")
		}
		if err := tx.Where("id = ? AND class_id = ?", input.AssignmentID, *student.ClassID).First(&assignment).Error; err != nil {
			return err
		}
		if !time.Now().Before(assignment.Deadline) {
			return failExam(409, "Batas waktu pengumpulan sudah berakhir.")
		}
		if input.FileURL != "" && !strings.Contains(","+assignment.AnswerFormats+",", ",link,") {
			return failExam(400, "Guru tidak menerima jawaban berbentuk link.")
		}
		seen := map[uint]bool{}
		for _, id := range input.AttachmentIDs {
			if id == 0 || seen[id] {
				return failExam(400, "Berkas jawaban tidak valid atau duplikat.")
			}
			seen[id] = true
			var asset MediaAsset
			if err := tx.Where("id=? AND owner_id=? AND assignment_id=? AND ready=true", id, student.ID, assignment.ID).First(&asset).Error; err != nil {
				return failExam(403, "Berkas bukan milik Anda atau bukan untuk tugas ini.")
			}
			if !strings.Contains(","+assignment.AnswerFormats+",", ","+mediaFormat(asset.ContentType)+",") {
				return failExam(400, "Format jawaban tidak diizinkan guru.")
			}
		}
		attachments, err := json.Marshal(input.AttachmentIDs)
		if err != nil {
			return err
		}
		if input.AttachmentIDs == nil {
			attachments = []byte("[]")
		}
		var submission Submission
		err = tx.Where("assignment_id = ? AND student_id = ?", assignment.ID, student.ID).First(&submission).Error
		if err == nil {
			if submission.GradedAt != nil {
				return failExam(409, "Jawaban sudah dinilai dan tidak dapat diubah.")
			}
			return tx.Model(&submission).Updates(map[string]interface{}{"attachments": string(attachments), "file_url": strings.TrimSpace(input.FileURL), "submitted_at": time.Now().UTC()}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		submission = Submission{Attachments: string(attachments), AssignmentID: assignment.ID, StudentID: student.ID, FileURL: strings.TrimSpace(input.FileURL), SubmittedAt: time.Now().UTC()}
		return tx.Create(&submission).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Jawaban tugas tersimpan."})
}
