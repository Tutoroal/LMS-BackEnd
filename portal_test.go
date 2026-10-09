package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTask(t *testing.T, id uint, classID uint, deadline time.Time) {
	t.Helper()
	mustCreate(t, &Assignment{ID: id, SubjectID: 1, ClassID: classID, Title: "Tugas", Deadline: deadline, MaxScore: 100})
}
func TestMaterialAndAssignmentOwnership(t *testing.T) {
	r := setupCBT(t)
	teacher := tokenFor(t, "teacher", 2)
	other := tokenFor(t, "other-teacher", 2)
	create := requestCBT(r, teacher, "POST", "/api/teacher/materials", `{"subject_id":1,"class_id":1,"title":"Modul","content_url":"https://example.org/modul"}`)
	expectStatus(t, create, 200)
	expectStatus(t, requestCBT(r, other, "POST", "/api/teacher/materials", `{"subject_id":1,"class_id":1,"title":"Hijack","content_url":"https://example.org/modul"}`), 403)
	expectStatus(t, requestCBT(r, other, "DELETE", "/api/teacher/materials/1", ""), 404)
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", `{"subject_id":1,"class_id":1,"title":"Unsafe","content_url":"javascript:alert(1)"}`), 400)
	newTask(t, 1, 1, time.Now().Add(time.Hour))
	expectStatus(t, requestCBT(r, other, "DELETE", "/api/teacher/assignments/1", ""), 404)
	expectStatus(t, requestCBT(r, other, "GET", "/api/teacher/assignments/1/submissions", ""), 404)
	visible := requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/student/materials", "")
	expectStatus(t, visible, 200)
	if !strings.Contains(visible.Body.String(), "Modul") {
		t.Fatal("student cannot see own material")
	}
	isolated := requestCBT(r, tokenFor(t, "other", 3), "GET", "/api/student/materials", "")
	expectStatus(t, isolated, 200)
	if strings.Contains(isolated.Body.String(), "Modul") {
		t.Fatal("cross-class material leak")
	}
	expectStatus(t, requestCBT(r, teacher, "DELETE", "/api/teacher/materials/nope", ""), 400)
}
func TestSubmissionValidationAndZeroGradeLock(t *testing.T) {
	r := setupCBT(t)
	newTask(t, 1, 1, time.Now().Add(time.Hour))
	student := tokenFor(t, "student", 3)
	teacher := tokenFor(t, "teacher", 2)
	body := `{"assignment_id":1,"file_url":"https://example.org/answer"}`
	expectStatus(t, requestCBT(r, tokenFor(t, "other", 3), "POST", "/api/student/submissions", body), 404)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/submissions", `{"assignment_id":1,"file_url":"file:///secret"}`), 400)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/submissions", body), 200)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/submissions", body), 200)
	var count int64
	DB.Model(&Submission{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate submissions")
	}
	for _, body := range []string{`{"score":-1}`, `{"score":101}`, `{"feedback":"No score"}`, `{"score":"invalid"}`} {
		expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/submissions/1/grade", body), 400)
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "other-teacher", 2), "PUT", "/api/teacher/submissions/1/grade", `{"score":50}`), 404)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/submissions/1/grade", `{"score":0,"feedback":"Perlu diperbaiki"}`), 200)
	var submission Submission
	DB.First(&submission, 1)
	if submission.GradedAt == nil || submission.Score != 0 {
		t.Fatal("zero grade not marked")
	}
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/submissions", body), 409)
	tasks := requestCBT(r, student, "GET", "/api/student/assignments", "")
	if !strings.Contains(tasks.Body.String(), `"is_graded":true`) || !strings.Contains(tasks.Body.String(), `"can_submit":false`) {
		t.Fatal(tasks.Body.String())
	}
	newTask(t, 2, 1, time.Now().Add(-time.Hour))
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/submissions", `{"assignment_id":2,"file_url":"https://example.org/late"}`), 409)
}
func TestDeadlineUsesEndOfSchoolDay(t *testing.T) {
	deadline, err := deadlineFromInput("2026-10-09")
	if err != nil {
		t.Fatal(err)
	}
	if deadline.In(schoolZone).Format("2006-01-02 15:04:05") != "2026-10-09 23:59:59" {
		t.Fatal(deadline)
	}
	if _, err := deadlineFromInput("not-a-date"); err == nil {
		t.Fatal("invalid date accepted")
	}
}
func TestAdminValidationAndHomeroomProtection(t *testing.T) {
	r := setupCBT(t)
	admin := tokenFor(t, "admin", 1)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/classes", `{"class_name":"X DKV","major":"DKV","homeroom_teacher_id":"student"}`), 400)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/classes", `{"class_name":"X DKV","major":"DKV","homeroom_teacher_id":"teacher"}`), 200)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/classes", `{"class_name":"XI DKV","major":"DKV","homeroom_teacher_id":"teacher"}`), 409)
	expectStatus(t, requestCBT(r, admin, "DELETE", "/api/admin/users/teacher", ""), 409)
	expectStatus(t, requestCBT(r, admin, "DELETE", "/api/admin/users/admin", ""), 409)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/users", `{"name":"Bad","email":"bad@test.local","password":"123","role_id":3}`), 400)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/users", `{"name":"Bad","email":"bad@test.local","password":"12345678","role_id":99}`), 400)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/users", `{"name":"Bad","email":"s@test.local","password":"12345678","role_id":3}`), 409)
	expectStatus(t, requestCBT(r, admin, "PUT", "/api/admin/students/student/class", `{"class_id":999}`), 400)
	expectStatus(t, requestCBT(r, admin, "PUT", "/api/admin/students/assign", `{"class_id":2,"student_ids":["student","teacher"]}`), 400)
	var student User
	DB.First(&student, "id = ?", "student")
	if student.ClassID == nil || *student.ClassID != 1 {
		t.Fatal("bulk assignment partially committed")
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "student", 3), "POST", "/api/student/exams/1/start", ""), 200)
	expectStatus(t, requestCBT(r, admin, "PUT", "/api/admin/students/assign", `{"class_id":2,"student_ids":["student","peer"]}`), 409)
}
func TestClassDeleteTransactionRollsBackOnFailure(t *testing.T) {
	r := setupCBT(t)
	newTask(t, 1, 1, time.Now().Add(time.Hour))
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "student", FileURL: "https://example.org/answer"})
	mustCreate(t, &Material{SubjectID: 1, ClassID: 1, Title: "Materi", ContentURL: "https://example.org/material"})
	if err := DB.Exec("CREATE TRIGGER reject_class_delete BEFORE DELETE ON classes BEGIN SELECT RAISE(ABORT,'test failure'); END;").Error; err != nil {
		t.Fatal(err)
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "admin", 1), "DELETE", "/api/admin/classes/1", ""), 500)
	var student User
	DB.First(&student, "id = ?", "student")
	if student.ClassID == nil || *student.ClassID != 1 {
		t.Fatal("class deletion partially committed")
	}
	var count int64
	DB.Model(&Assignment{}).Count(&count)
	if count != 1 {
		t.Fatal("tasks deleted outside transaction")
	}
	DB.Exec("DROP TRIGGER reject_class_delete")
	expectStatus(t, requestCBT(r, tokenFor(t, "admin", 1), "DELETE", "/api/admin/classes/1", ""), 200)
	DB.First(&student, "id = ?", "student")
	if student.ClassID != nil {
		t.Fatal("student was not unassigned")
	}
	for _, model := range []interface{}{&Assignment{}, &Submission{}, &Material{}, &Exam{}, &Question{}} {
		DB.Model(model).Count(&count)
		if count != 0 {
			t.Fatalf("orphan data: %T", model)
		}
	}
}
func TestSubjectCascadeIncludesExamResults(t *testing.T) {
	r := setupCBT(t)
	newTask(t, 1, 1, time.Now().Add(time.Hour))
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "student", FileURL: "https://example.org/answer"})
	student := tokenFor(t, "student", 3)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/exams/1/start", ""), 200)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B"}}`), 200)
	expectStatus(t, requestCBT(r, tokenFor(t, "admin", 1), "DELETE", "/api/admin/subjects/1", ""), 200)
	var count int64
	for _, model := range []interface{}{&Subject{}, &Assignment{}, &Submission{}, &Exam{}, &Question{}, &ExamAttempt{}, &ExamResult{}} {
		DB.Model(model).Count(&count)
		if count != 0 {
			t.Fatalf("orphan after subject delete: %T", model)
		}
	}
}
func TestLogoutAndPasswordChangeRevokeSessions(t *testing.T) {
	r := setupCBT(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("password-old"), bcrypt.MinCost)
	DB.Model(&User{}).Where("id = ?", "student").Update("password_hash", string(hash))
	login := func(password string) string {
		response := requestCBT(r, "", "POST", "/api/login", fmt.Sprintf(`{"identifier":"s@test.local","password":%q}`, password))
		expectStatus(t, response, 200)
		var data struct{ Token string }
		json.Unmarshal(response.Body.Bytes(), &data)
		return data.Token
	}
	first := login("password-old")
	expectStatus(t, requestCBT(r, first, "POST", "/api/auth/logout", ""), 200)
	expectStatus(t, requestCBT(r, first, "GET", "/api/auth/me", ""), 401)
	next := login("password-old")
	expectStatus(t, requestCBT(r, next, "PUT", "/api/auth/password", `{"current_password":"wrong","password":"password-new"}`), 400)
	expectStatus(t, requestCBT(r, next, "PUT", "/api/auth/password", `{"current_password":"password-old","password":"password-new"}`), 200)
	expectStatus(t, requestCBT(r, next, "GET", "/api/auth/me", ""), 401)
	renewed := login("password-new")
	expectStatus(t, requestCBT(r, renewed, "GET", "/api/auth/me", ""), 200)
}
func TestLoginFailureThrottling(t *testing.T) {
	r := setupCBT(t)
	for index := 0; index < 8; index++ {
		expectStatus(t, requestCBT(r, "", "POST", "/api/login", `{"identifier":"missing@test.local","password":"wrong"}`), 401)
	}
	expectStatus(t, requestCBT(r, "", "POST", "/api/login", `{"identifier":"missing@test.local","password":"wrong"}`), 429)
}
func TestSchedulesConflictAndRoleFiltering(t *testing.T) {
	r := setupCBT(t)
	admin := tokenFor(t, "admin", 1)
	create := func(classID uint, start, end, day string) *httptest.ResponseRecorder {
		return requestCBT(r, admin, "POST", "/api/admin/schedules", fmt.Sprintf(`{"class_id":%d,"subject_id":1,"day_of_week":%q,"start_time":%q,"end_time":%q}`, classID, day, start, end))
	}
	expectStatus(t, create(1, "08:00", "09:00", "Senin"), 200)
	expectStatus(t, create(2, "08:30", "09:30", "Senin"), 409)
	expectStatus(t, create(1, "09:00", "10:00", "Senin"), 200)
	expectStatus(t, create(2, "08:00", "09:00", "Selasa"), 200)
	expectStatus(t, create(1, "25:00", "26:00", "Senin"), 400)
	own := requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/schedules", "")
	expectStatus(t, own, 200)
	if strings.Contains(own.Body.String(), "Selasa") {
		t.Fatal("cross-class schedule leak")
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "teacher", 2), "POST", "/api/admin/schedules", `{}`), 403)
}
func importWorkbook(t *testing.T, r *gin.Engine, token string, sheets map[string][][]string) *httptest.ResponseRecorder {
	t.Helper()
	workbook := excelize.NewFile()
	defer workbook.Close()
	for name, rows := range sheets {
		if _, err := workbook.NewSheet(name); err != nil {
			t.Fatal(err)
		}
		for rowIndex, row := range rows {
			for column, value := range row {
				cell, _ := excelize.CoordinatesToCellName(column+1, rowIndex+1)
				if err := workbook.SetCellStr(name, cell, value); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	workbook.DeleteSheet("Sheet1")
	buffer, err := workbook.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "import.xlsx")
	part.Write(buffer.Bytes())
	writer.Close()
	request := httptest.NewRequest("POST", "/api/admin/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}
func TestImportRollbackAndTemplate(t *testing.T) {
	r := setupCBT(t)
	admin := tokenFor(t, "admin", 1)
	teacherHeader := []string{"Nama", "Email", "Password", "NIP", "Tempat", "Tanggal", "Gender", "Specialty"}
	failed := importWorkbook(t, r, admin, map[string][][]string{"Guru": {teacherHeader, {"Baru", "new@test.local", "password123", "0099"}, {"Hijack", "a@test.local", "password123", "0088"}}})
	expectStatus(t, failed, 409)
	var count int64
	DB.Model(&User{}).Where("email = ?", "new@test.local").Count(&count)
	if count != 0 {
		t.Fatal("partial import")
	}
	success := importWorkbook(t, r, admin, map[string][][]string{"Guru": {teacherHeader, {"Baru", "new@test.local", "password123", "0099"}}})
	expectStatus(t, success, 200)
	missing := importWorkbook(t, r, admin, map[string][][]string{"Siswa": {{"Nama", "Email", "Password"}, {"Tanpa Password", "nopass@test.local", ""}}})
	expectStatus(t, missing, 400)
	template := requestCBT(r, admin, "GET", "/api/admin/import/template", "")
	expectStatus(t, template, 200)
	workbook, err := excelize.OpenReader(bytes.NewReader(template.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	if len(workbook.GetSheetList()) != 2 {
		t.Fatal("template sheets missing")
	}
}
func TestPortalSettingsGradesAndReport(t *testing.T) {
	r := setupCBT(t)
	admin := tokenFor(t, "admin", 1)
	student := tokenFor(t, "student", 3)
	expectStatus(t, requestCBT(r, admin, "PUT", "/api/admin/settings", `{"school_name":"SMK Uji","academic_year":"2026/2027"}`), 200)
	settings := requestCBT(r, student, "GET", "/api/settings", "")
	expectStatus(t, settings, 200)
	if !strings.Contains(settings.Body.String(), "SMK Uji") {
		t.Fatal("settings not persisted")
	}
	expectStatus(t, requestCBT(r, student, "PUT", "/api/admin/settings", `{"school_name":"Hijack","academic_year":"2026"}`), 403)
	newTask(t, 1, 1, time.Now().Add(time.Hour))
	now := time.Now()
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "student", FileURL: "https://example.org/answer", Score: 0, GradedAt: &now})
	expectStatus(t, requestCBT(r, student, "GET", "/api/student/overview", ""), 200)
	grades := requestCBT(r, student, "GET", "/api/student/grades", "")
	expectStatus(t, grades, 200)
	if !strings.Contains(grades.Body.String(), `"Score":0`) {
		t.Fatal("zero grade missing")
	}
	report := requestCBT(r, tokenFor(t, "teacher", 2), "GET", "/api/teacher/export/report", "")
	expectStatus(t, report, 200)
	workbook, err := excelize.OpenReader(bytes.NewReader(report.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	rows, err := workbook.GetRows("Nilai Tugas")
	if err != nil || len(rows) != 2 {
		t.Fatal("report missing grades", err)
	}
	if rows[1][5] != "0" || rows[1][7] != "Dinilai" {
		t.Fatal("zero score report is incorrect", rows)
	}
}
