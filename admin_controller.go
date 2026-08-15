package main

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

type ClassInput struct {
	ClassName string `json:"class_name" binding:"required"`
}

func CreateClass(c *gin.Context) {
	// 1. Cek Hak Akses: Hanya Admin (RoleID = 1) yang boleh masuk
	roleID, _ := c.Get("role_id")
	if uint(roleID.(float64)) != 1 { // JWT selalu membaca angka sebagai float64
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak! Anda bukan Admin."})
		return
	}

	// 2. Tangkap data dari frontend
	var input ClassInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 3. Simpan ke database
	class := Class{ClassName: input.ClassName}
	DB.Create(&class)

	c.JSON(http.StatusOK, gin.H{"message": "Kelas berhasil dibuat!", "data": class})
}

func GetClasses(c *gin.Context) {
	// Cek Hak Akses Admin
	roleID, _ := c.Get("role_id")
	if uint(roleID.(float64)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak! Anda bukan Admin."})
		return
	}

	var classes []Class
	// Ambil semua data kelas dari database
	DB.Find(&classes)

	c.JSON(http.StatusOK, gin.H{
		"message": "Berhasil mengambil data kelas",
		"data":    classes,
	})
}