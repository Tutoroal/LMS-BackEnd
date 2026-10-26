package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestObserverReadOnlyAndExport(t *testing.T) {
	r := setupCBT(t)
	for _, role := range []uint{4, 5} {
		id := fmt.Sprintf("observer-%d", role)
		mustCreate(t, &User{ID: id, Name: "Pemantau", Email: id + "@test.local", RoleID: role})
		token := tokenFor(t, id, role)
		for _, path := range []string{"/api/monitor/overview", "/api/monitor/classes", "/api/monitor/teachers", "/api/schedules", "/api/teacher/assignments", "/api/teacher/exams/1/questions", "/api/teacher/export/report"} {
			expectStatus(t, requestCBT(r, token, "GET", path, ""), 200)
		}
		for _, item := range []struct {
			Method string
			Path   string
		}{{"POST", "/api/admin/classes"}, {"PUT", "/api/admin/classes/1"}, {"DELETE", "/api/admin/classes/1"}, {"POST", "/api/teacher/assignments"}, {"PUT", "/api/teacher/assignments/1"}, {"DELETE", "/api/teacher/assignments/1"}, {"POST", "/api/uploads/init"}, {"POST", "/api/student/submissions"}, {"PUT", "/api/teacher/exams/1/essays/student"}} {
			expectStatus(t, requestCBT(r, token, item.Method, item.Path, "{}"), 403)
		}

		expectStatus(t, requestCBT(r, token, "GET", "/api/admin/users", ""), 403)
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/monitor/overview", ""), 403)
}
func TestSemesterRevisionAndConflicts(t *testing.T) {
	r := setupCBT(t)
	admin := tokenFor(t, "admin", 1)
	payload := func(semester, from, start string) string {
		return fmt.Sprintf(`{"class_id":1,"subject_id":1,"day_of_week":"Senin","start_time":%q,"end_time":"10:00","academic_year":"2026/2027","semester":%q,"effective_from":%q,"effective_until":"2027-06-30"}`, start, semester, from)
	}
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/schedules", payload("Ganjil", "2026-01-01", "08:00")), 200)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/schedules", payload("Genap", "2026-01-01", "08:00")), 200)
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/schedules", payload("Ganjil", "2026-02-01", "09:00")), 409)
	expectStatus(t, requestCBT(r, admin, "PUT", "/api/admin/schedules/1", payload("Ganjil", "2026-04-01", "09:00")), 200)
	var old Schedule
	if err := DB.First(&old, 1).Error; err != nil {
		t.Fatal(err)
	}
	if old.EffectiveUntil != "2026-03-31" {
		t.Fatal("revision overwrote history", old)
	}
	for _, date := range []string{"2026-03-01", "2026-04-01"} {
		response := requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/schedules?academic_year=2026%2F2027&semester=Ganjil&date="+date, "")
		expectStatus(t, response, 200)
		var body struct{ Data []Schedule }
		json.Unmarshal(response.Body.Bytes(), &body)
		if len(body.Data) != 1 {
			t.Fatal("schedule versions overlap", response.Body.String())
		}
		expected := "08:00"
		if date == "2026-04-01" {
			expected = "09:00"
		}
		if body.Data[0].StartTime != expected {
			t.Fatal(response.Body.String())
		}
	}
	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/schedules", payload("Ganjil", "not-date", "08:00")), 400)
}
func uploadTestImage(t *testing.T, r *gin.Engine, token string, assignment uint, data []byte, name string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("assignment_id", fmt.Sprint(assignment))
	file, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	file.Write(data)
	writer.Close()
	request := httptest.NewRequest("POST", "/api/uploads/local", &body)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}
func TestPrivateAttachmentsAndFormatLocks(t *testing.T) {
	t.Setenv("S3_BUCKET", "")
	t.Setenv("UPLOAD_DIR", t.TempDir())
	r := setupCBT(t)
	newTask(t, 1, 1, time.Now().Add(time.Hour))
	DB.Model(&Assignment{}).Where("id=1").Update("answer_formats", "image,pdf")
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	student := tokenFor(t, "student", 3)
	teacher := tokenFor(t, "teacher", 2)
	response := uploadTestImage(t, r, student, 1, data.Bytes(), "jawaban.png")
	expectStatus(t, response, 201)
	var uploaded struct{ Data MediaAsset }
	json.Unmarshal(response.Body.Bytes(), &uploaded)
	id := uploaded.Data.ID
	expectStatus(t, requestCBT(r, teacher, "GET", fmt.Sprintf("/api/uploads/%d/access", id), ""), 404)
	expectStatus(t, requestCBT(r, tokenFor(t, "other", 3), "GET", fmt.Sprintf("/api/uploads/%d/content", id), ""), 404)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/submissions", fmt.Sprintf(`{"assignment_id":1,"attachment_ids":[%d]}`, id)), 200)
	expectStatus(t, requestCBT(r, teacher, "GET", fmt.Sprintf("/api/uploads/%d/content", id), ""), 200)
	expectStatus(t, requestCBT(r, tokenFor(t, "other-teacher", 2), "GET", fmt.Sprintf("/api/uploads/%d/content", id), ""), 404)
	expectStatus(t, requestCBT(r, tokenFor(t, "peer", 3), "POST", "/api/student/submissions", fmt.Sprintf(`{"assignment_id":1,"attachment_ids":[%d]}`, id)), 403)
	expectStatus(t, uploadTestImage(t, r, student, 1, []byte("<html>bad</html>"), "fake.png"), 400)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/submissions", `{"assignment_id":1,"file_url":"https://example.org/link"}`), 400)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/submissions/1/grade", `{"score":0}`), 200)
	expectStatus(t, uploadTestImage(t, r, student, 1, data.Bytes(), "locked.png"), 409)
}
func TestEssayScoringDoesNotLeakKeys(t *testing.T) {
	r := setupCBT(t)
	mustCreate(t, &Question{ExamID: 1, QuestionText: "Jelaskan", Kind: "essay", Points: 2, CorrectAnswer: "Pedoman guru rahasia"})
	student := tokenFor(t, "student", 3)
	start := requestCBT(r, student, "POST", "/api/student/exams/1/start", "")
	expectStatus(t, start, 200)
	if strings.Contains(start.Body.String(), "Pedoman guru rahasia") || strings.Contains(start.Body.String(), "CorrectAnswer") {
		t.Fatal("key leaked")
	}
	response := requestCBT(r, student, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B","2":"C","3":"Jawaban esai siswa"}}`)
	expectStatus(t, response, 200)
	var body struct{ Result ExamResult }
	json.Unmarshal(response.Body.Bytes(), &body)
	if body.Result.Score != 50 || !body.Result.PendingGrading {
		t.Fatal("incorrect provisional score", body)
	}
	teacher := tokenFor(t, "teacher", 2)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/essays/student", `{"grades":{"3":3}}`), 400)
	expectStatus(t, requestCBT(r, tokenFor(t, "other-teacher", 2), "PUT", "/api/teacher/exams/1/essays/student", `{"grades":{"3":2}}`), 404)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/essays/student", `{"grades":{"3":2}}`), 200)
	var result ExamResult
	DB.First(&result, body.Result.ID)
	if result.Score != 100 || result.PendingGrading {
		t.Fatal("final score incorrect", result)
	}
}
func TestImportTeachingAllocationsAndIsolation(t *testing.T) {
	r := setupCBT(t)
	response := importWorkbook(t, r, tokenFor(t, "admin", 1), map[string][][]string{"Pengampu": {{"Email Guru", "Mapel", "Kelas"}, {"t@test.local", "Pemrograman", "X PPLG"}}})
	expectStatus(t, response, 200)
	teacher := tokenFor(t, "teacher", 2)
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", `{"subject_id":1,"class_id":2,"title":"Wrong class","content_url":"https://example.org"}`), 403)
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", `{"subject_id":1,"class_id":1,"title":"Own class","content_url":"https://example.org"}`), 200)
	response = requestCBT(r, teacher, "GET", "/api/teacher/classes", "")
	expectStatus(t, response, 200)
	if strings.Contains(response.Body.String(), "X TJKT") {
		t.Fatal("unassigned class returned")
	}
	expectStatus(t, importWorkbook(t, r, tokenFor(t, "admin", 1), map[string][][]string{"Pengampu": {{"Email Guru", "Mapel", "Kelas"}, {"t@test.local", "Mapel Baru", "X PPLG"}, {"t@test.local", "Mapel Baru", "KELAS HILANG"}}}), 400)
	var count int64
	DB.Model(&Subject{}).Where("subject_name=?", "Mapel Baru").Count(&count)
	if count != 0 {
		t.Fatal("failed allocation import was not atomic")
	}
}
func TestReportMonthlyBasesInSchoolTimezone(t *testing.T) {
	r := setupCBT(t)
	deadline, _ := deadlineFromInput("2026-09-30")
	newTask(t, 1, 1, deadline)
	submitted, _ := time.Parse(time.RFC3339, "2026-10-01T00:30:00+07:00")
	graded, _ := time.Parse(time.RFC3339, "2026-11-01T00:30:00+07:00")
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "student", Score: 80, SubmittedAt: submitted, GradedAt: &graded})
	for _, item := range []struct {
		Month string
		Basis string
		Sheet string
		Rows  int
	}{{"2026-09", "deadline", "Nilai Tugas", 3}, {"2026-10", "submitted", "Tugas Pengumpulan", 2}, {"2026-11", "graded", "Tugas Penilaian", 2}, {"2026-10", "graded", "Tugas Penilaian", 1}} {
		response := requestCBT(r, tokenFor(t, "teacher", 2), "GET", "/api/teacher/export/report?month="+item.Month+"&basis="+item.Basis, "")
		expectStatus(t, response, 200)
		workbook, err := excelize.OpenReader(bytes.NewReader(response.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := workbook.GetRows(item.Sheet)
		workbook.Close()
		if err != nil || len(rows) != item.Rows {
			t.Fatalf("monthly report %s/%s: rows=%d err=%v", item.Month, item.Basis, len(rows), err)
		}
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "teacher", 2), "GET", "/api/teacher/export/report?month=2026-99", ""), 400)
}
