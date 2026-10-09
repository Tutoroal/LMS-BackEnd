package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupCBT(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	jwtSecret = []byte("test-only-secret-for-cbt-at-least-32-bytes")
	var err error
	DB, err = gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err = DB.AutoMigrate(&Role{}, &User{}, &Class{}, &Subject{}, &Schedule{}, &Material{}, &Assignment{}, &Submission{}, &Exam{}, &Question{}, &ExamResult{}, &ExamAttempt{}, &SchoolSettings{}); err != nil {
		t.Fatal(err)
	}
	seedRoles()
	mustCreate(t, &Class{ID: 1, ClassName: "X PPLG"})
	mustCreate(t, &Class{ID: 2, ClassName: "X TJKT"})
	class1, class2 := uint(1), uint(2)
	for _, u := range []User{
		{ID: "student", Name: "Siswa", Email: "s@test.local", RoleID: 3, ClassID: &class1},
		{ID: "other", Name: "Siswa lain", Email: "o@test.local", RoleID: 3, ClassID: &class2},
		{ID: "peer", Name: "Teman", Email: "p@test.local", RoleID: 3, ClassID: &class1},
		{ID: "teacher", Name: "Guru", Email: "t@test.local", RoleID: 2},
		{ID: "other-teacher", Name: "Guru lain", Email: "ot@test.local", RoleID: 2},
		{ID: "admin", Name: "Admin", Email: "a@test.local", RoleID: 1},
	} {
		mustCreate(t, &u)
	}
	mustCreate(t, &Subject{ID: 1, SubjectName: "Pemrograman", TeacherID: "teacher"})
	mustCreate(t, &Exam{ID: 1, SubjectID: 1, ClassID: 1, Title: "Ujian", Type: "UH", Duration: 60, Date: time.Now().Add(-24 * time.Hour), IsActive: true})
	mustCreate(t, &Question{ID: 1, ExamID: 1, QuestionText: "Pertanyaan 1", OptionA: "Satu", OptionB: "Dua", OptionC: "Tiga", OptionD: "Empat", CorrectAnswer: "B"})
	mustCreate(t, &Question{ID: 2, ExamID: 1, QuestionText: "Pertanyaan 2", OptionA: "Satu", OptionB: "Dua", OptionC: "Tiga", OptionD: "Empat", CorrectAnswer: "C"})
	return newRouter()
}
func mustCreate(t *testing.T, value interface{}) {
	t.Helper()
	if err := DB.Create(value).Error; err != nil {
		t.Fatal(err)
	}
}
func tokenFor(t *testing.T, user string, role uint) string {
	t.Helper()
	value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": user, "role_id": role, "exp": time.Now().Add(time.Hour).Unix()}).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func requestCBT(r *gin.Engine, token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}
func expectStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status %d, want %d: %s", response.Code, status, response.Body.String())
	}
}
func score(t *testing.T, response *httptest.ResponseRecorder) float64 {
	t.Helper()
	var data struct{ Result ExamResult }
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return data.Result.Score
}
func TestCBTStartResumeAndIsolation(t *testing.T) {
	r := setupCBT(t)
	token := tokenFor(t, "student", 3)
	first := requestCBT(r, token, "POST", "/api/student/exams/1/start", "")
	expectStatus(t, first, 200)
	for _, secret := range []string{"CorrectAnswer", "QuestionsJSON", "PasswordHash", "correct_answer"} {
		if strings.Contains(first.Body.String(), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	var a, b map[string]interface{}
	json.Unmarshal(first.Body.Bytes(), &a)
	second := requestCBT(r, token, "POST", "/api/student/exams/1/start", "")
	expectStatus(t, second, 200)
	json.Unmarshal(second.Body.Bytes(), &b)
	if a["expires_at"] != b["expires_at"] || a["started_at"] != b["started_at"] {
		t.Fatal("resume reset timer")
	}
	var count int64
	DB.Model(&ExamAttempt{}).Count(&count)
	if count != 1 {
		t.Fatalf("attempts: %d", count)
	}
	for _, tc := range []struct {
		user   string
		role   uint
		path   string
		status int
	}{
		{"other", 3, "/api/student/exams/1/start", 404},
		{"teacher", 2, "/api/student/exams/1/start", 403},
		{"student", 3, "/api/student/exams/nope/start", 400},
	} {
		expectStatus(t, requestCBT(r, tokenFor(t, tc.user, tc.role), "POST", tc.path, ""), tc.status)
	}
	expectStatus(t, requestCBT(r, token, "GET", "/api/teacher/exams/1/questions", ""), 403)
	expectStatus(t, requestCBT(r, tokenFor(t, "other-teacher", 2), "GET", "/api/teacher/exams/1/questions", ""), 404)
}
func TestCBTGradingAndIdempotence(t *testing.T) {
	r := setupCBT(t)
	token := tokenFor(t, "student", 3)
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/submit", `{"answers":{}}`), 409)
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/start", ""), 200)
	for _, body := range []string{`{"answers":{"999":"A"}}`, `{"answers":{"1":"Z"}}`, `{"answers":null}`, `{"answers":{"no":"A"}}`} {
		expectStatus(t, requestCBT(r, token, "PUT", "/api/student/exams/1/answers", body), 400)
	}
	expectStatus(t, requestCBT(r, token, "PUT", "/api/student/exams/1/answers", `{"answers":{"1":"B"}}`), 200)
	response := requestCBT(r, token, "POST", "/api/student/exams/1/submit", `{"answers":{"2":"A"}}`)
	expectStatus(t, response, 200)
	if score(t, response) != 50 {
		t.Fatal(response.Body.String())
	}
	again := requestCBT(r, token, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B","2":"C"}}`)
	expectStatus(t, again, 200)
	if score(t, again) != 50 {
		t.Fatal("resubmission changed score")
	}
	var count int64
	DB.Model(&ExamResult{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate results")
	}
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/start", ""), 200)
}
func TestCBTExpiryUsesSavedAnswers(t *testing.T) {
	r := setupCBT(t)
	token := tokenFor(t, "student", 3)
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/start", ""), 200)
	expectStatus(t, requestCBT(r, token, "PUT", "/api/student/exams/1/answers", `{"answers":{"1":"B"}}`), 200)
	DB.Model(&ExamAttempt{}).Where("student_id = ?", "student").Update("expires_at", time.Now().Add(-time.Second))
	response := requestCBT(r, token, "POST", "/api/student/exams/1/submit", `{"answers":{"2":"C"}}`)
	expectStatus(t, response, 200)
	if score(t, response) != 50 {
		t.Fatal("late answer accepted")
	}
	peer := tokenFor(t, "peer", 3)
	expectStatus(t, requestCBT(r, peer, "POST", "/api/student/exams/1/start", ""), 200)
	DB.Model(&ExamAttempt{}).Where("student_id = ?", "peer").Update("expires_at", time.Now().Add(-time.Second))
	if err := finalizeExpiredAttempts(time.Now()); err != nil {
		t.Fatal(err)
	}
	var result ExamResult
	if err := DB.Where("student_id = ?", "peer").First(&result).Error; err != nil {
		t.Fatal(err)
	}
	if result.Score != 0 {
		t.Fatal("unanswered exam must score zero")
	}
}
func TestCBTSchedulePublicationAndFrozenQuestions(t *testing.T) {
	r := setupCBT(t)
	token := tokenFor(t, "student", 3)
	teacher := tokenFor(t, "teacher", 2)
	DB.Model(&Exam{}).Where("id = 1").Update("is_active", false)
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/start", ""), 403)
	expectStatus(t, requestCBT(r, tokenFor(t, "other-teacher", 2), "PUT", "/api/teacher/exams/1/active", `{"is_active":true}`), 404)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/active", `{"is_active":true}`), 200)
	DB.Model(&Exam{}).Where("id = 1").Update("date", time.Now().Add(72*time.Hour))
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/start", ""), 403)
	DB.Model(&Exam{}).Where("id = 1").Update("date", time.Now().Add(-24*time.Hour))
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/start", ""), 200)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/active", `{"is_active":false}`), 200)
	expectStatus(t, requestCBT(r, teacher, "DELETE", "/api/teacher/exams/questions/1", ""), 409)
	// Existing attempts can finish even after the teacher closes new admissions.
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B","2":"C"}}`), 200)
	// Snapshot remains authoritative even if a database administrator changes a key.
	peer := tokenFor(t, "peer", 3)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/active", `{"is_active":true}`), 200)
	expectStatus(t, requestCBT(r, peer, "POST", "/api/student/exams/1/start", ""), 200)
	DB.Model(&Question{}).Where("id = 1").Update("correct_answer", "A")
	response := requestCBT(r, peer, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B","2":"C"}}`)
	if score(t, response) != 100 {
		t.Fatal("snapshot changed")
	}
}
func TestCBTConcurrentSubmitAndCascade(t *testing.T) {
	r := setupCBT(t)
	token := tokenFor(t, "student", 3)
	expectStatus(t, requestCBT(r, token, "POST", "/api/student/exams/1/start", ""), 200)
	responses := make(chan *httptest.ResponseRecorder, 5)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- requestCBT(r, token, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B","2":"C"}}`)
		}()
	}
	wg.Wait()
	close(responses)
	for response := range responses {
		expectStatus(t, response, 200)
		if score(t, response) != 100 {
			t.Fatal(response.Body.String())
		}
	}
	var count int64
	DB.Model(&ExamResult{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate concurrent result")
	}
	if err := DB.Delete(&Exam{}, 1).Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []interface{}{&ExamAttempt{}, &ExamResult{}, &Question{}} {
		DB.Model(model).Count(&count)
		if count != 0 {
			t.Fatalf("cascade failed for %T", model)
		}
	}
}
func TestAuthRejectsInvalidSessions(t *testing.T) {
	r := setupCBT(t)
	for _, tc := range []struct {
		name   string
		method jwt.SigningMethod
		claims jwt.MapClaims
	}{
		{"wrong algorithm", jwt.SigningMethodHS384, jwt.MapClaims{"user_id": "student", "role_id": 3, "exp": time.Now().Add(time.Hour).Unix()}},
		{"expired", jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "student", "role_id": 3, "exp": time.Now().Add(-time.Hour).Unix()}},
		{"missing expiry", jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "student", "role_id": 3}},
		{"wrong identity type", jwt.SigningMethodHS256, jwt.MapClaims{"user_id": 99, "role_id": 3, "exp": time.Now().Add(time.Hour).Unix()}},
		{"changed role", jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "student", "role_id": 1, "exp": time.Now().Add(time.Hour).Unix()}},
		{"deleted user", jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "missing", "role_id": 3, "exp": time.Now().Add(time.Hour).Unix()}},
	} {
		token, err := jwt.NewWithClaims(tc.method, tc.claims).SignedString(jwtSecret)
		if err != nil {
			t.Fatal(err)
		}
		response := requestCBT(r, token, "GET", "/api/auth/me", "")
		if response.Code != 401 {
			t.Fatalf("%s: %d", tc.name, response.Code)
		}
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/admin/users", ""), 403)
	session := requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/auth/me", "")
	expectStatus(t, session, 200)
	if strings.Contains(session.Body.String(), "PasswordHash") {
		t.Fatal("password hash leaked")
	}
	for _, role := range []uint{1, 2, 3} {
		user := map[uint]string{1: "admin", 2: "teacher", 3: "student"}[role]
		expectStatus(t, requestCBT(r, tokenFor(t, user, role), "GET", "/api/auth/me", ""), 200)
	}
}
func TestExamAvailabilityUsesSchoolCalendar(t *testing.T) {
	date, _ := time.Parse("2006-01-02", "2026-10-09")
	exam := Exam{IsActive: true, Duration: 60, Date: date}
	for _, tc := range []struct {
		clock string
		want  bool
	}{{"2026-10-08T16:59:59Z", false}, {"2026-10-08T17:00:00Z", true}} {
		now, _ := time.Parse(time.RFC3339, tc.clock)
		if got := examAvailable(exam, now); got != tc.want {
			t.Fatal(fmt.Sprintf("%s: %v", tc.clock, got))
		}
	}
}

func TestLoginAndSubjectCreation(t *testing.T) {
	r := setupCBT(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err = DB.Model(&User{}).Where("id = ?", "student").Update("password_hash", string(hash)).Error; err != nil {
		t.Fatal(err)
	}
	response := requestCBT(r, "", "POST", "/api/login", `{"identifier":" s@test.local ","password":"test-password"}`)
	expectStatus(t, response, 200)
	var login struct {
		Token  string
		RoleID uint `json:"role_id"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || login.RoleID != 3 {
		t.Fatal("missing login session")
	}
	expectStatus(t, requestCBT(r, login.Token, "GET", "/api/auth/me", ""), 200)
	expectStatus(t, requestCBT(r, "", "POST", "/api/login", `{"identifier":"s@test.local","password":"wrong"}`), 401)
	expectStatus(t, requestCBT(r, "", "POST", "/api/login", `{"identifier":"   ","password":"wrong"}`), 400)
	admin := tokenFor(t, "admin", 1)
	created := requestCBT(r, admin, "POST", "/api/admin/subjects", `{"subject_name":"Basis Data","teacher_id":"teacher"}`)
	expectStatus(t, created, 200)
	var subject Subject
	if err := DB.Where("subject_name = ?", "Basis Data").First(&subject).Error; err != nil {
		t.Fatal(err)
	}
	if subject.TeacherID != "teacher" {
		t.Fatal("teacher ownership was not saved")
	}
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/subjects", `{"subject_name":"Invalid","teacher_id":"student"}`), 400)
	// User responses must never serialize password hashes.
	users := requestCBT(r, admin, "GET", "/api/admin/users", "")
	expectStatus(t, users, 200)
	if strings.Contains(users.Body.String(), "PasswordHash") || strings.Contains(users.Body.String(), string(hash)) {
		t.Fatal("password hash leaked")
	}
}

func TestEmptyExamAndInvalidCreate(t *testing.T) {
	r := setupCBT(t)
	teacher := tokenFor(t, "teacher", 2)
	mustCreate(t, &Exam{ID: 2, SubjectID: 1, ClassID: 1, Title: "Kosong", Type: "UH", Duration: 30, Date: time.Now().Add(-24 * time.Hour)})
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/2/active", `{"is_active":true}`), 409)
	for _, body := range []string{
		`{"subject_id":1,"class_id":1,"title":"Ujian","type":"UH","date":"invalid","duration":60}`,
		`{"subject_id":1,"class_id":1,"title":"Ujian","type":"UH","date":"2026-10-09","duration":-1}`,
		`{"subject_id":1,"class_id":1,"title":"Ujian","type":"OTHER","date":"2026-10-09","duration":60}`,
	} {
		expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/exams", body), 400)
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "other-teacher", 2), "POST", "/api/teacher/exams", `{"subject_id":1,"class_id":1,"title":"Ujian","type":"UH","date":"2026-10-09","duration":60}`), 403)
}
