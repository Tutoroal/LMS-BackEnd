package main

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

type SubmissionInput struct {
	AssignmentID uint   `json:"assignment_id" binding:"required"`
	FileURL      string `json:"file_url" binding:"required"`
}

func SubmitAssignment(c *gin.Context) {
	// Cek Hak Akses: Hanya Siswa (RoleID = 3) yang boleh masuk
	roleID, _ := c.Get("role_id")
	if uint(roleID.(float64)) != 3 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak! Anda bukan Siswa."})
		return
	}

	var input SubmissionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	submission := Submission{
		AssignmentID: input.AssignmentID,
		FileURL:      input.FileURL,
	}
	DB.Create(&submission)

	c.JSON(http.StatusOK, gin.H{"message": "Tugas berhasil dikumpulkan!"})
}