package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
)

func safeGet(row []string, idx int) string {
	if len(row) > idx {
		return strings.TrimSpace(row[idx])
	}
	return ""
}

func ImportDataExcel(c *gin.Context) {
	if !isAdmin(c) {
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File Excel tidak ditemukan atau rusak."})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membuka file."})
		return
	}
	defer file.Close()

	f, err := excelize.OpenReader(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format tidak didukung. Pastikan file berformat .xlsx"})
		return
	}
	defer f.Close()

	sheets := f.GetSheetList()
	fmt.Printf("📂 Sheet yang ditemukan di Excel: %v\n", sheets)

	guruCount := 0
	siswaCount := 0
	kelasCount := 0

	// Pindai setiap sheet secara cerdas berdasarkan header kolomnya
	for _, sheetName := range sheets {
		rows, err := f.GetRows(sheetName)
		if err != nil || len(rows) <= 1 {
			continue
		}

		// Gabungkan teks baris pertama (header) untuk mendeteksi isi sheet
		headerText := strings.ToLower(strings.Join(rows[0], " "))
		sheetLower := strings.ToLower(strings.ReplaceAll(sheetName, " ", ""))

		// Deteksi tipe data berdasarkan kata kunci di header atau nama sheet
		isGuruSheet := strings.Contains(headerText, "nip") || strings.Contains(headerText, "mapel") || strings.Contains(sheetLower, "guru")
		isSiswaSheet := strings.Contains(headerText, "nis") || strings.Contains(headerText, "nisn") || strings.Contains(headerText, "kelas") || strings.Contains(sheetLower, "siswa")

		// Jika header tidak spesifik, gunakan nama sheet sebagai acuan
		if !isGuruSheet && !isSiswaSheet {
			if strings.Contains(sheetLower, "guru") {
				isGuruSheet = true
			} else {
				isSiswaSheet = true // Default aman sebagai siswa jika tidak ada indikasi guru
			}
		}

		// ==========================================
		// PROSES JIKA SHEET GURU
		// ==========================================
		if isGuruSheet && !isSiswaSheet {
			fmt.Printf("👨‍🏫 Memproses Sheet Guru: %s\n", sheetName)
			for i := 1; i < len(rows); i++ {
				row := rows[i]
				if len(row) < 2 { continue }

				nama := safeGet(row, 0)
				email := safeGet(row, 1)
				if email == "" { continue }

				passRaw := safeGet(row, 2)
				if passRaw == "" { passRaw = "guru123" }
				hash, _ := bcrypt.GenerateFromPassword([]byte(passRaw), bcrypt.DefaultCost)

				var user User
				if DB.Where("email = ?", email).First(&user).Error == nil {
					DB.Model(&user).Updates(map[string]interface{}{
						"Name": nama, "NISN_NIP": safeGet(row, 3), "TempatLahir": safeGet(row, 4),
						"TanggalLahir": safeGet(row, 5), "JenisKelamin": safeGet(row, 6), "Specialty": safeGet(row, 7),
						"RoleID": 2, // Paksa pastikan role tetap Guru
					})
				} else {
					newUser := User{
						Name: nama, Email: email, PasswordHash: string(hash), RoleID: 2,
						NISN_NIP: safeGet(row, 3), TempatLahir: safeGet(row, 4), TanggalLahir: safeGet(row, 5),
						JenisKelamin: safeGet(row, 6), Specialty: safeGet(row, 7),
					}
					DB.Create(&newUser)
					guruCount++
				}
			}
		} else {
			// ==========================================
			// PROSES JIKA SHEET SISWA
			// ==========================================
			fmt.Printf("🎓 Memproses Sheet Siswa: %s\n", sheetName)
			for i := 1; i < len(rows); i++ {
				row := rows[i]
				if len(row) < 2 { continue }

				nama := safeGet(row, 0)
				email := safeGet(row, 1)
				if email == "" { continue }

				passRaw := safeGet(row, 2)
				if passRaw == "" { passRaw = "siswa123" }
				hash, _ := bcrypt.GenerateFromPassword([]byte(passRaw), bcrypt.DefaultCost)

				namaKelas := safeGet(row, 8)
				if namaKelas == "" { namaKelas = "Kelas Umum" }

				var kelas Class
				if DB.Where("class_name = ?", namaKelas).First(&kelas).Error != nil {
					kelas = Class{ClassName: namaKelas}
					DB.Create(&kelas)
					kelasCount++
				}

				var user User
				if DB.Where("email = ?", email).First(&user).Error == nil {
					DB.Model(&user).Updates(map[string]interface{}{
						"Name": nama, "NISN_NIP": safeGet(row, 3), "NIS": safeGet(row, 4), 
						"TempatLahir": safeGet(row, 5), "TanggalLahir": safeGet(row, 6), 
						"JenisKelamin": safeGet(row, 7), "ClassID": &kelas.ID,
						"RoleID": 3, // Paksa pastikan role tetap Siswa
					})
				} else {
					newUser := User{
						Name: nama, Email: email, PasswordHash: string(hash), RoleID: 3,
						NISN_NIP: safeGet(row, 3), NIS: safeGet(row, 4), TempatLahir: safeGet(row, 5), 
						TanggalLahir: safeGet(row, 6), JenisKelamin: safeGet(row, 7), ClassID: &kelas.ID,
					}
					DB.Create(&newUser)
					siswaCount++
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Import Data Selesai & Disinkronkan dengan Tepat!",
		"detail": gin.H{
			"guru_baru":  guruCount,
			"siswa_baru": siswaCount,
			"kelas_baru": kelasCount,
		},
	})
}