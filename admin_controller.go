package main

import (
	"errors"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"strings"
	"time"
)

type ClassInput struct {
	ClassName         string  `json:"class_name" binding:"required"`
	Major             string  `json:"major"`
	HomeroomTeacherID *string `json:"homeroom_teacher_id"`
}

func normalizeClass(input *ClassInput) error {
	input.ClassName = strings.ToUpper(sanitize(input.ClassName))
	input.Major = strings.ToUpper(strings.TrimSpace(input.Major))
	if !validText(input.ClassName, 50) {
		return failExam(400, "Nama kelas wajib diisi, maksimal 50 karakter.")
	}
	if input.Major == "" {
		for _, major := range departments {
			if strings.Contains(input.ClassName, major) {
				input.Major = major
				break
			}
		}
	}
	if input.Major != "" {
		valid := false
		for _, major := range departments {
			if input.Major == major {
				valid = true
			}
		}
		if !valid {
			return failExam(400, "Jurusan harus PPLG, TJKT, DKV, BDR, PERHOTELAN, atau MPLB.")
		}
	}
	if input.HomeroomTeacherID != nil && strings.TrimSpace(*input.HomeroomTeacherID) == "" {
		input.HomeroomTeacherID = nil
	}
	return nil
}
func validateHomeroom(tx *gorm.DB, teacher *string, classID uint) error {
	if teacher == nil {
		return nil
	}
	var user User
	if err := tx.Where("id = ? AND role_id = 2", *teacher).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return failExam(400, "Wali kelas harus merupakan akun guru.")
		}
		return err
	}
	var count int64
	if err := tx.Model(&Class{}).Where("homeroom_teacher_id = ? AND id <> ?", *teacher, classID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return failExam(409, "Guru ini sudah menjadi wali kelas lain.")
	}
	return nil
}
func CreateClass(c *gin.Context) { saveClass(c, false) }
func UpdateClass(c *gin.Context) { saveClass(c, true) }
func saveClass(c *gin.Context, update bool) {
	if !isAdmin(c) {
		return
	}
	var input ClassInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Data kelas tidak lengkap."})
		return
	}
	if err := normalizeClass(&input); err != nil {
		sendExamError(c, err)
		return
	}
	var class Class
	err := DB.Transaction(func(tx *gorm.DB) error {
		var id uint
		if update {
			var err error
			id, err = examID(c)
			if err != nil {
				return err
			}
			if err = tx.First(&class, id).Error; err != nil {
				return err
			}
		}
		if err := validateHomeroom(tx, input.HomeroomTeacherID, id); err != nil {
			return err
		}
		if !update {
			class = Class{ClassName: input.ClassName, Major: input.Major, HomeroomTeacherID: input.HomeroomTeacherID}
			return tx.Create(&class).Error
		}
		return tx.Model(&class).Updates(map[string]interface{}{"class_name": input.ClassName, "major": input.Major, "homeroom_teacher_id": input.HomeroomTeacherID}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Kelas tersimpan.", "data": class})
}
func GetClasses(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	classes := []Class{}
	if err := DB.Preload("HomeroomTeacher").Order("class_name").Find(&classes).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": classes, "departments": departments})
}
func GetClassDetails(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var class Class
	if err := DB.Preload("HomeroomTeacher").First(&class, id).Error; err != nil {
		sendExamError(c, err)
		return
	}
	students := []User{}
	if err := DB.Where("class_id = ? AND role_id = 3", id).Order("name").Find(&students).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"class": class, "students": students})
}
func DeleteClass(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var class Class
		if err := tx.First(&class, id).Error; err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("class_id = ?", id).Update("class_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM teacher_classes WHERE class_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Where("class_id = ?", id).Delete(&Schedule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("class_id = ?", id).Delete(&Material{}).Error; err != nil {
			return err
		}
		tasks := tx.Model(&Assignment{}).Select("id").Where("class_id = ?", id)
		if err := tx.Where("assignment_id IN (?)", tasks).Delete(&Submission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("class_id = ?", id).Delete(&Assignment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("class_id = ?", id).Delete(&Exam{}).Error; err != nil {
			return err
		}
		return tx.Delete(&class).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Kelas dan konten terkait dihapus. Akun siswa tetap ada dengan status tanpa kelas."})
}

type UserInput struct {
	Name         string `json:"name" binding:"required"`
	Email        string `json:"email" binding:"required"`
	Password     string `json:"password"`
	RoleID       uint   `json:"role_id" binding:"required,oneof=1 2 3"`
	NISN_NIP     string `json:"nisn_nip"`
	NIS          string `json:"nis"`
	TempatLahir  string `json:"tempat_lahir"`
	TanggalLahir string `json:"tanggal_lahir"`
	JenisKelamin string `json:"jenis_kelamin"`
	Specialty    string `json:"specialty"`
	ClassID      *uint  `json:"class_id"`
}

func normalizeUser(input *UserInput) error {
	input.Name = sanitize(input.Name)
	input.NISN_NIP = strings.TrimSpace(input.NISN_NIP)
	input.NIS = strings.TrimSpace(input.NIS)
	email, err := normalizedEmail(input.Email)
	if err != nil {
		return err
	}
	input.Email = email
	if !validText(input.Name, 100) || len(input.NISN_NIP) > 50 || len(input.NIS) > 50 || len(input.Specialty) > 100 || len(input.TempatLahir) > 100 {
		return failExam(400, "Nama atau identitas terlalu panjang atau kosong.")
	}
	if input.RoleID < 1 || input.RoleID > 3 {
		return failExam(400, "Peran akun tidak valid.")
	}
	if input.RoleID == 2 && input.NISN_NIP == "" {
		return failExam(400, "NIP/NUPTK wajib diisi untuk guru.")
	}
	if input.RoleID != 3 && input.ClassID != nil {
		return failExam(400, "Hanya siswa yang dapat ditempatkan di kelas.")
	}
	if input.TanggalLahir != "" {
		if _, err := time.Parse("2006-01-02", input.TanggalLahir); err != nil {
			return failExam(400, "Tanggal lahir tidak valid.")
		}
	}
	if input.JenisKelamin != "" && input.JenisKelamin != "Laki-laki" && input.JenisKelamin != "Perempuan" {
		return failExam(400, "Jenis kelamin tidak valid.")
	}
	return nil
}
func validateUserRelations(tx *gorm.DB, input UserInput, exceptID string) error {
	if input.ClassID != nil {
		var class Class
		if err := tx.First(&class, *input.ClassID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return failExam(400, "Kelas tidak ditemukan.")
			}
			return err
		}
	}
	var count int64
	if err := tx.Model(&User{}).Where("LOWER(email) = ? AND id <> ?", input.Email, exceptID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return failExam(409, "Email sudah digunakan akun lain.")
	}
	for _, identifier := range []string{input.NISN_NIP, input.NIS} {
		if identifier == "" {
			continue
		}
		if err := tx.Model(&User{}).Where("id <> ? AND (nisn_nip = ? OR nis = ? OR LOWER(email) = ?)", exceptID, identifier, identifier, strings.ToLower(identifier)).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return failExam(409, "NIS/NISN/NIP sudah digunakan akun lain.")
		}
	}
	return nil
}
func GetUsers(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	users := []User{}
	if err := DB.Preload("Class").Preload("Role").Order("created_at DESC").Find(&users).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": users})
}
func CreateUser(c *gin.Context) { saveUser(c, false) }
func UpdateUser(c *gin.Context) { saveUser(c, true) }
func saveUser(c *gin.Context, update bool) {
	if !isAdmin(c) {
		return
	}
	var input UserInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Nama, email, dan peran yang valid wajib diisi."})
		return
	}
	if err := normalizeUser(&input); err != nil {
		sendExamError(c, err)
		return
	}
	var hash []byte
	if !update || input.Password != "" {
		if err := validatePassword(input.Password); err != nil {
			sendExamError(c, err)
			return
		}
		var err error
		hash, err = bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			sendExamError(c, err)
			return
		}
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current User
		exceptID := ""
		if update {
			exceptID = c.Param("id")
			if err := tx.First(&current, "id = ?", exceptID).Error; err != nil {
				return err
			}
			if err := validateClassTransfer(tx, current, input.ClassID); err != nil {
				return err
			}
			if current.RoleID != input.RoleID {
				return failExam(409, "Peran akun tidak dapat diubah melalui formulir ini.")
			}
		}
		if err := validateUserRelations(tx, input, exceptID); err != nil {
			return err
		}
		if !update {
			user := User{Name: input.Name, Email: input.Email, PasswordHash: string(hash), RoleID: input.RoleID, NISN_NIP: input.NISN_NIP, NIS: input.NIS, TempatLahir: input.TempatLahir, TanggalLahir: input.TanggalLahir, JenisKelamin: input.JenisKelamin, Specialty: input.Specialty, ClassID: input.ClassID}
			return tx.Create(&user).Error
		}
		updates := map[string]interface{}{"name": input.Name, "email": input.Email, "nisn_nip": input.NISN_NIP, "nis": input.NIS, "tempat_lahir": input.TempatLahir, "tanggal_lahir": input.TanggalLahir, "jenis_kelamin": input.JenisKelamin, "specialty": input.Specialty, "class_id": input.ClassID}
		if input.ClassID != nil {
			updates["legacy_class_reference"] = ""
		}
		if hash != nil {
			updates["password_hash"] = string(hash)
			updates["session_version"] = current.SessionVersion + 1
		}
		return tx.Model(&current).Updates(updates).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Data pengguna tersimpan."})
}
func DeleteUser(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	id := c.Param("id")
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.First(&user, "id = ?", id).Error; err != nil {
			return err
		}
		if id == c.GetString("user_id") {
			return failExam(409, "Anda tidak dapat menghapus akun yang sedang digunakan.")
		}
		if user.RoleID == 1 {
			var count int64
			if err := tx.Model(&User{}).Where("role_id = 1").Count(&count).Error; err != nil {
				return err
			}
			if count <= 1 {
				return failExam(409, "Administrator terakhir tidak dapat dihapus.")
			}
		}
		if user.RoleID == 2 {
			var count int64
			if err := tx.Model(&Class{}).Where("homeroom_teacher_id = ?", id).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return failExam(409, "Guru masih menjadi wali kelas. Ganti wali kelas terlebih dahulu.")
			}
			if err := tx.Model(&Subject{}).Where("teacher_id = ?", id).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return failExam(409, "Guru masih mengampu mapel. Pindahkan pengampu atau hapus mapel terlebih dahulu.")
			}
		}
		if err := tx.Exec("DELETE FROM teacher_classes WHERE user_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Where("student_id = ?", id).Delete(&Submission{}).Error; err != nil {
			return err
		}
		return tx.Delete(&user).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Akun pengguna dihapus."})
}

type AssignClassInput struct {
	ClassID *uint `json:"class_id"`
}

func AssignStudentToClass(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	var input AssignClassInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Format kelas tidak valid."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var student User
		if err := tx.Where("id = ? AND role_id = 3", c.Param("student_id")).First(&student).Error; err != nil {
			return err
		}
		if input.ClassID != nil {
			var class Class
			if err := tx.First(&class, *input.ClassID).Error; err != nil {
				return failExam(400, "Kelas tujuan tidak ditemukan.")
			}
		}
		var active int64
		if err := tx.Model(&ExamAttempt{}).Where("student_id = ? AND submitted_at IS NULL AND expires_at > ?", student.ID, time.Now().UTC()).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return failExam(409, "Siswa sedang mengerjakan ujian. Tunggu hingga selesai sebelum memindahkan kelas.")
		}
		updates := map[string]interface{}{"class_id": input.ClassID}
		if input.ClassID != nil {
			updates["legacy_class_reference"] = ""
		}
		return tx.Model(&student).Updates(updates).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Penempatan kelas diperbarui."})
}

func validateClassTransfer(tx *gorm.DB, student User, target *uint) error {
	changed := (student.ClassID == nil) != (target == nil)
	if student.ClassID != nil && target != nil {
		changed = *student.ClassID != *target
	}
	if !changed {
		return nil
	}
	var active int64
	if err := tx.Model(&ExamAttempt{}).Where("student_id = ? AND submitted_at IS NULL AND expires_at > ?", student.ID, time.Now().UTC()).Count(&active).Error; err != nil {
		return err
	}
	if active > 0 {
		return failExam(409, "Siswa sedang mengerjakan ujian. Pemindahan kelas ditunda hingga selesai.")
	}
	return nil
}
func AssignStudentsToClass(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	var input struct {
		StudentIDs []string `json:"student_ids" binding:"required,min=1,max=500"`
		ClassID    uint     `json:"class_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(400, gin.H{"error": "Pilih 1 sampai 500 siswa dan kelas tujuan."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var class Class
		if err := tx.First(&class, input.ClassID).Error; err != nil {
			return failExam(400, "Kelas tujuan tidak ditemukan.")
		}
		seen := map[string]bool{}
		for _, id := range input.StudentIDs {
			if seen[id] {
				return failExam(400, "Daftar siswa berisi duplikasi.")
			}
			seen[id] = true
			var student User
			if err := tx.First(&student, "id = ? AND role_id = 3", id).Error; err != nil {
				return failExam(400, "Ada akun yang bukan siswa atau sudah dihapus.")
			}
			if err := validateClassTransfer(tx, student, &input.ClassID); err != nil {
				return err
			}
		}
		return tx.Model(&User{}).Where("id IN ?", input.StudentIDs).Updates(map[string]interface{}{"class_id": input.ClassID, "legacy_class_reference": ""}).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Semua siswa berhasil ditempatkan."})
}
