package main

import (
	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSeedBrowserFixture(t *testing.T) {
	path := os.Getenv("LMS_E2E_DB")
	if path == "" {
		t.Skip("browser fixture is only created by the isolated browser test launcher")
	}
	if !filepath.IsAbs(path) || filepath.Base(path) != "e2e.db" {
		t.Fatal("invalid browser fixture path")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("refusing to seed an existing database")
	}
	var err error
	DB, err = gorm.Open(sqlite.Open(path+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := DB.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err := migrateDatabase(); err != nil {
		t.Fatal(err)
	}
	seedRoles()
	hash, err := bcrypt.GenerateFromPassword([]byte("E2e-password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, &Class{ID: 1, ClassName: "X PPLG Uji", Major: "PPLG"})
	mustCreate(t, &Class{ID: 2, ClassName: "X TJKT Uji", Major: "TJKT"})
	class1, class2 := uint(1), uint(2)
	for _, user := range []User{
		{ID: "e2e-admin", Name: "Admin Uji", Email: "admin@e2e.invalid", RoleID: 1, PasswordHash: string(hash)},
		{ID: "e2e-teacher", Name: "Guru Uji", Email: "guru@e2e.invalid", NISN_NIP: "000101", RoleID: 2, PasswordHash: string(hash)},
		{ID: "e2e-student", Name: "Siswa Uji", Email: "siswa@e2e.invalid", NIS: "000201", NISN_NIP: "0002001", RoleID: 3, ClassID: &class1, PasswordHash: string(hash)},
		{ID: "e2e-other", Name: "Siswa TJKT", Email: "other@e2e.invalid", NIS: "000202", RoleID: 3, ClassID: &class2, PasswordHash: string(hash)},
	} {
		mustCreate(t, &user)
	}
	if err := DB.Model(&Class{}).Where("id = 1").Update("homeroom_teacher_id", "e2e-teacher").Error; err != nil {
		t.Fatal(err)
	}
	mustCreate(t, &Subject{ID: 1, SubjectName: "Pemrograman Web", TeacherID: "e2e-teacher"})
	mustCreate(t, &Material{ID: 1, SubjectID: 1, ClassID: 1, Title: "Modul HTML", ContentURL: "https://example.org/html"})
	mustCreate(t, &Assignment{ID: 1, SubjectID: 1, ClassID: 1, Title: "Proyek Web", Deadline: time.Now().Add(24 * time.Hour), MaxScore: 100})
	mustCreate(t, &Exam{ID: 1, SubjectID: 1, ClassID: 1, Title: "Ujian HTML", Type: "UH", Date: time.Now().Add(-time.Hour * 24), Duration: 30, IsActive: true})
	for index, key := range []string{"B", "C"} {
		mustCreate(t, &Question{ID: uint(index + 1), ExamID: 1, QuestionText: "Pertanyaan HTML " + string(rune('1'+index)), OptionA: "Pilihan A", OptionB: "Pilihan B", OptionC: "Pilihan C", OptionD: "Pilihan D", CorrectAnswer: key})
	}
	mustCreate(t, &Exam{ID: 2, SubjectID: 1, ClassID: 1, Title: "Ujian Timeout", Type: "UH", Date: time.Now().Add(-time.Hour * 24), Duration: 1, IsActive: true})
	mustCreate(t, &Question{ID: 3, ExamID: 2, QuestionText: "Soal Timeout", OptionA: "Pilihan A", OptionB: "Pilihan B", OptionC: "Pilihan C", OptionD: "Pilihan D", CorrectAnswer: "A"})
	mustCreate(t, &Schedule{ID: 1, SubjectID: 1, ClassID: 1, DayOfWeek: "Senin", StartTime: "08:00", EndTime: "09:00"})
}
