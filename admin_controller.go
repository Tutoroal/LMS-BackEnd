package main

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

type ClassInput struct {
	ClassName string `json:"class_name" binding:"required"`
}

func CreateClass(c *gin.Context) {
	roleID, _ := c.Get("role_id")
	if uint(roleID.(float64)) != 1 { 
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak Anda bukan Admin."})
		return
	}

	
	var input ClassInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	class := Class{ClassName: input.ClassName}
	DB.Create(&class)

	c.JSON(http.StatusOK, gin.H{"message": "Kelas berhasil dibuat!", "data": class})
}

func GetClasses(c *gin.Context) {
	
	roleID, _ := c.Get("role_id")
	if uint(roleID.(float64)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak, Anda bukan Admin."})
		return
	}

	var classes []Class
	DB.Find(&classes)

	c.JSON(http.StatusOK, gin.H{
		"message": "Berhasil mengambil data kelas",
		"data":    classes,
	})
}