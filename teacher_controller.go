package main

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

type MaterialInput struct {
	SubjectID  uint   `json:"subject_id" binding:"required"`
	Title      string `json:"title" binding:"required"`
	ContentURL string `json:"content_url"`
}

func CreateMaterial(c *gin.Context) {
	// Cek Hak Akses: Hanya Guru (RoleID = 2) yang boleh masuk
	roleID, _ := c.Get("role_id")
	if uint(roleID.(float64)) != 2 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak! Anda bukan Guru."})
		return
	}

	var input MaterialInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	material := Material{
		SubjectID:  input.SubjectID,
		Title:      input.Title,
		ContentURL: input.ContentURL,
	}
	DB.Create(&material)

	c.JSON(http.StatusOK, gin.H{"message": "Materi baru berhasil diunggah!", "data": material})
}

func GetMaterials(c *gin.Context) {
	roleID, _ := c.Get("role_id")
	if uint(roleID.(float64)) != 2 && uint(roleID.(float64)) != 3 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak!"})
		return
	}

	var materials []Material
	DB.Find(&materials)

	c.JSON(http.StatusOK, gin.H{
		"message": "Berhasil mengambil data materi",
		"data":    materials,
	})
}