package main

import (
	"net/http"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type ClassInput struct {
	ClassName         string  `json:"class_name" binding:"required"`
	HomeroomTeacherID *string `json:"homeroom_teacher_id"`
}

func CreateClass(c *gin.Context) {
	if !isAdmin(c) { return }
	var input ClassInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	class := Class{ClassName: input.ClassName, HomeroomTeacherID: input.HomeroomTeacherID}
	DB.Create(&class)
	c.JSON(http.StatusOK, gin.H{"message": "Kelas berhasil dibuat!", "data": class})
}

func GetClasses(c *gin.Context) {
	if !isAdmin(c) { return }
	var classes []Class
	DB.Preload("HomeroomTeacher").Find(&classes)
	c.JSON(http.StatusOK, gin.H{"data": classes})
}

func GetClassDetails(c *gin.Context) {
	if !isAdmin(c) { return }
	id := c.Param("id")
	var students []User
	DB.Where("class_id = ? AND role_id = 3", id).Find(&students)
	c.JSON(http.StatusOK, gin.H{"students": students})
}

func UpdateClass(c *gin.Context) {
	if !isAdmin(c) { return }
	id := c.Param("id")
	var class Class
	if err := DB.First(&class, id).Error; err != nil { return }
	var input ClassInput
	if err := c.ShouldBindJSON(&input); err != nil { return }
	DB.Model(&class).Updates(map[string]interface{}{
		"ClassName": input.ClassName, 
		"HomeroomTeacherID": input.HomeroomTeacherID,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Kelas berhasil diperbarui"})
}

func DeleteClass(c *gin.Context) {
	if !isAdmin(c) { return }
	id := c.Param("id")
	DB.Delete(&Class{}, id)
	c.JSON(http.StatusOK, gin.H{"message": "Dihapus"})
}

type UserInput struct {
	Name         string `json:"name" binding:"required"`
	Email        string `json:"email" binding:"required,email"`
	Password     string `json:"password"`
	RoleID       uint   `json:"role_id" binding:"required"`
	NISN_NIP     string `json:"nisn_nip"`
	NIS          string `json:"nis"`
	TempatLahir  string `json:"tempat_lahir"`
	TanggalLahir string `json:"tanggal_lahir"`
	JenisKelamin string `json:"jenis_kelamin"`
	Specialty    string `json:"specialty"` 
	ClassID      *uint  `json:"class_id"` 
}

func GetUsers(c *gin.Context) {
	if !isAdmin(c) { return }
	var users []User
	DB.Preload("Class").Preload("Role").Find(&users)
	c.JSON(http.StatusOK, gin.H{"data": users})
}

func CreateUser(c *gin.Context) {
	if !isAdmin(c) { return }
	var input UserInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Data tidak valid"})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	user := User{
		Name: input.Name, Email: input.Email, PasswordHash: string(hash), RoleID: input.RoleID,
		NISN_NIP: input.NISN_NIP, NIS: input.NIS, TempatLahir: input.TempatLahir, 
		TanggalLahir: input.TanggalLahir, JenisKelamin: input.JenisKelamin, Specialty: input.Specialty,
		ClassID: input.ClassID,
	}
	if err := DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Email sudah digunakan!"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Pengguna berhasil ditambahkan!"})
}

func UpdateUser(c *gin.Context) {
	if !isAdmin(c) { return }
	id := c.Param("id")
	var user User
	if err := DB.First(&user, "id = ?", id).Error; err != nil { return }
	
	var input UserInput
	if err := c.ShouldBindJSON(&input); err != nil { return }
	
	updates := map[string]interface{}{
		"Name": input.Name, "Email": input.Email, "NISN_NIP": input.NISN_NIP, "NIS": input.NIS,
		"TempatLahir": input.TempatLahir, "TanggalLahir": input.TanggalLahir, 
		"JenisKelamin": input.JenisKelamin, "Specialty": input.Specialty,
		"ClassID": input.ClassID,
	}
	if input.Password != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		updates["PasswordHash"] = string(hash)
	}
	DB.Model(&user).Updates(updates)
	c.JSON(http.StatusOK, gin.H{"message": "Data berhasil diperbarui"})
}

func DeleteUser(c *gin.Context) {
	if !isAdmin(c) { return }
	id := c.Param("id")
	DB.Where("id = ?", id).Delete(&User{})
	c.JSON(http.StatusOK, gin.H{"message": "Pengguna dihapus"})
}

type AssignClassInput struct {
	ClassID *uint `json:"class_id"`
}

func AssignStudentToClass(c *gin.Context) {
	if !isAdmin(c) { return }
	studentID := c.Param("student_id")
	var input AssignClassInput
	if err := c.ShouldBindJSON(&input); err != nil { return }

	var student User
	if err := DB.Where("id = ? AND role_id = 3", studentID).First(&student).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Siswa tidak ditemukan"})
		return
	}

	DB.Model(&student).Update("ClassID", input.ClassID)
	c.JSON(http.StatusOK, gin.H{"message": "Kelas siswa diperbarui"})
}

func isAdmin(c *gin.Context) bool {
	roleID, exists := c.Get("role_id")
	if !exists || uint(roleID.(float64)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak!"})
		c.Abort()
		return false
	}
	return true
}