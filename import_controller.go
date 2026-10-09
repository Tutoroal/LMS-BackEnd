package main

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/http"
	"regexp"
	"strings"
)

var extraSpaces = regexp.MustCompile("\\s+")

func sanitize(text string) string { return strings.TrimSpace(extraSpaces.ReplaceAllString(text, " ")) }
func safeGet(row []string, index int) string {
	if len(row) > index {
		return sanitize(row[index])
	}
	return ""
}

type importRecord struct {
	Input     UserInput
	ClassName string
	Hash      string
	Sheet     string
	Row       int
}

func ImportDataExcel(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10*1024*1024)
	if err := c.Request.ParseMultipartForm(10 * 1024 * 1024); err != nil {
		c.JSON(400, gin.H{"error": "File maksimal 10 MB."})
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	header, err := c.FormFile("file")
	if err != nil || !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		c.JSON(400, gin.H{"error": "Unggah file Excel .xlsx maksimal 10 MB."})
		return
	}
	file, err := header.Open()
	if err != nil {
		sendExamError(c, err)
		return
	}
	defer file.Close()
	workbook, err := excelize.OpenReader(file, excelize.Options{UnzipSizeLimit: 32 * 1024 * 1024, UnzipXMLSizeLimit: 8 * 1024 * 1024})
	if err != nil {
		c.JSON(400, gin.H{"error": "File Excel tidak dapat dibaca."})
		return
	}
	defer workbook.Close()
	records := []importRecord{}
	seen := map[string]bool{}
	for _, sheet := range workbook.GetSheetList() {
		name := strings.ToLower(strings.ReplaceAll(sheet, " ", ""))
		if name != "guru" && name != "siswa" {
			continue
		}
		rows, err := workbook.GetRows(sheet)
		if err != nil {
			c.JSON(400, gin.H{"error": "Sheet tidak dapat dibaca."})
			return
		}
		for index, row := range rows {
			if index == 0 || strings.TrimSpace(strings.Join(row, "")) == "" {
				continue
			}
			if len(records) >= 1000 {
				c.JSON(400, gin.H{"error": "Maksimal 1000 akun per import."})
				return
			}
			input := UserInput{Name: safeGet(row, 0), Email: safeGet(row, 1), Password: safeGet(row, 2)}
			className := ""
			if name == "guru" {
				input.RoleID = 2
				input.NISN_NIP = safeGet(row, 3)
				input.TempatLahir = safeGet(row, 4)
				input.TanggalLahir = safeGet(row, 5)
				input.JenisKelamin = safeGet(row, 6)
				input.Specialty = safeGet(row, 7)
			} else {
				input.RoleID = 3
				input.NISN_NIP = safeGet(row, 3)
				input.NIS = safeGet(row, 4)
				input.TempatLahir = safeGet(row, 5)
				input.TanggalLahir = safeGet(row, 6)
				input.JenisKelamin = safeGet(row, 7)
				className = strings.ToUpper(safeGet(row, 8))
			}
			context := fmt.Sprintf("%s baris %d", sheet, index+1)
			if err := normalizeUser(&input); err != nil {
				c.JSON(400, gin.H{"error": context + ": " + err.Error()})
				return
			}
			if seen[input.Email] {
				c.JSON(409, gin.H{"error": context + ": email duplikat dalam berkas."})
				return
			}
			seen[input.Email] = true
			if len(className) > 50 {
				c.JSON(400, gin.H{"error": context + ": nama kelas maksimal 50 karakter."})
				return
			}
			record := importRecord{Input: input, ClassName: className, Sheet: sheet, Row: index + 1}
			if input.Password != "" {
				if err := validatePassword(input.Password); err != nil {
					c.JSON(400, gin.H{"error": context + ": " + err.Error()})
					return
				}
				hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
				if err != nil {
					sendExamError(c, err)
					return
				}
				record.Hash = string(hash)
			}
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		c.JSON(400, gin.H{"error": "Gunakan sheet Guru dan/atau Siswa sesuai template."})
		return
	}
	teacherCount, studentCount, classCount, updated := 0, 0, 0, 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		for _, record := range records {
			input := record.Input
			context := fmt.Sprintf("%s baris %d", record.Sheet, record.Row)
			var user User
			err := tx.Where("LOWER(email) = ?", input.Email).First(&user).Error
			exists := err == nil
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if exists && user.RoleID != input.RoleID {
				return failExam(409, context+": email milik akun dengan peran berbeda.")
			}
			if !exists && record.Hash == "" {
				return failExam(400, context+": kata sandi minimal 8 karakter wajib untuk akun baru.")
			}
			if record.ClassName != "" {
				var class Class
				err := tx.Where("UPPER(class_name) = ?", record.ClassName).First(&class).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					class = Class{ClassName: record.ClassName, Major: departmentFor(record.ClassName)}
					if err := tx.Create(&class).Error; err != nil {
						return err
					}
					classCount++
				} else if err != nil {
					return err
				}
				input.ClassID = &class.ID
			}
			if err := validateUserRelations(tx, input, user.ID); err != nil {
				return failExam(409, context+": "+err.Error())
			}
			if !exists {
				user = User{Name: input.Name, Email: input.Email, RoleID: input.RoleID, PasswordHash: record.Hash, NISN_NIP: input.NISN_NIP, NIS: input.NIS, TempatLahir: input.TempatLahir, TanggalLahir: input.TanggalLahir, JenisKelamin: input.JenisKelamin, Specialty: input.Specialty, ClassID: input.ClassID}
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
				if input.RoleID == 2 {
					teacherCount++
				} else {
					studentCount++
				}
			} else {
				var active int64
				if err := tx.Model(&ExamAttempt{}).Where("student_id = ? AND submitted_at IS NULL", user.ID).Count(&active).Error; err != nil {
					return err
				}
				if active > 0 && (user.ClassID == nil) != (input.ClassID == nil) {
					return failExam(409, context+": siswa masih mengerjakan ujian.")
				}
				if active > 0 && user.ClassID != nil && input.ClassID != nil && *user.ClassID != *input.ClassID {
					return failExam(409, context+": siswa masih mengerjakan ujian.")
				}
				updates := map[string]interface{}{"name": input.Name, "nisn_nip": input.NISN_NIP, "nis": input.NIS, "tempat_lahir": input.TempatLahir, "tanggal_lahir": input.TanggalLahir, "jenis_kelamin": input.JenisKelamin, "specialty": input.Specialty, "class_id": input.ClassID}
				if input.ClassID != nil {
					updates["legacy_class_reference"] = ""
				}
				if record.Hash != "" {
					updates["password_hash"] = record.Hash
					updates["session_version"] = user.SessionVersion + 1
				}
				if err := tx.Model(&user).Updates(updates).Error; err != nil {
					return err
				}
				updated++
			}
		}
		return nil
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "Import selesai. Semua baris berhasil disimpan.", "detail": gin.H{"guru_baru": teacherCount, "siswa_baru": studentCount, "kelas_baru": classCount, "akun_diperbarui": updated}})
}
func DownloadImportTemplate(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	workbook := excelize.NewFile()
	defer workbook.Close()
	if err := workbook.SetSheetName("Sheet1", "Guru"); err != nil {
		sendExamError(c, err)
		return
	}
	if _, err := workbook.NewSheet("Siswa"); err != nil {
		sendExamError(c, err)
		return
	}
	for sheet, headers := range map[string][]string{
		"Guru":  {"Nama", "Email", "Password", "NIP/NUPTK", "Tempat Lahir", "Tanggal Lahir (YYYY-MM-DD)", "Jenis Kelamin", "Specialty"},
		"Siswa": {"Nama", "Email", "Password", "NISN", "NIS", "Tempat Lahir", "Tanggal Lahir (YYYY-MM-DD)", "Jenis Kelamin", "Kelas"},
	} {
		for index, header := range headers {
			cell, _ := excelize.CoordinatesToCellName(index+1, 1)
			if err := workbook.SetCellStr(sheet, cell, header); err != nil {
				sendExamError(c, err)
				return
			}
		}
		if err := workbook.SetColWidth(sheet, "A", "I", 24); err != nil {
			sendExamError(c, err)
			return
		}
	}
	sendWorkbook(c, workbook, "Template_Import_LMS.xlsx")
}
