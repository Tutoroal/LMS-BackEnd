package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func uploadMaterialFile(t *testing.T, r *gin.Engine, token, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"purpose": "material", "subject_id": "1", "class_id": "1"} {
		writer.WriteField(key, value)
	}
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
func docxFixture(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	file.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Materi &lt;script&gt;aman&lt;/script&gt;</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Kolom satu</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Kolom dua</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`))
	writer.Close()
	return buffer.Bytes()
}
func TestMaterialUploadsPrivateAndOfficePreview(t *testing.T) {
	t.Setenv("S3_BUCKET", "")
	t.Setenv("UPLOAD_DIR", t.TempDir())
	r := setupCBT(t)
	teacher := tokenFor(t, "teacher", 2)
	student := tokenFor(t, "student", 3)
	response := uploadMaterialFile(t, r, teacher, "materi.docx", docxFixture(t))
	expectStatus(t, response, 201)
	var body struct{ Data MediaAsset }
	json.Unmarshal(response.Body.Bytes(), &body)
	id := body.Data.ID
	expectStatus(t, requestCBT(r, student, "GET", fmt.Sprintf("/api/uploads/%d/preview", id), ""), 404)
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", fmt.Sprintf(`{"title":"Materi Word","subject_id":1,"class_id":1,"asset_id":%d}`, id)), 200)
	response = requestCBT(r, student, "GET", fmt.Sprintf("/api/uploads/%d/preview", id), "")
	expectStatus(t, response, 200)
	if !strings.Contains(response.Body.String(), "Kolom satu") || !strings.Contains(response.Body.String(), "paragraph") {
		t.Fatal(response.Body.String())
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "other", 3), "GET", fmt.Sprintf("/api/uploads/%d/preview", id), ""), 404)
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", fmt.Sprintf(`{"title":"Wrong target","subject_id":1,"class_id":2,"asset_id":%d}`, id)), 400)
	expectStatus(t, uploadMaterialFile(t, r, tokenFor(t, "other-teacher", 2), "other.docx", docxFixture(t)), 403)
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", `{"title":"Materi link","subject_id":1,"class_id":1,"content_url":"https://example.org"}`), 200)
	book := excelize.NewFile()
	book.SetCellStr("Sheet1", "A1", "Nilai")
	book.SetCellStr("Sheet1", "A3", "Setelah baris kosong")
	book.SetCellInt("Sheet1", "B1", 0)
	book.SetCellFormula("Sheet1", "C1", `HYPERLINK("https://invalid.local","link")`)
	buffer, err := book.WriteToBuffer()
	book.Close()
	if err != nil {
		t.Fatal(err)
	}
	response = uploadMaterialFile(t, r, teacher, "materi.xlsx", buffer.Bytes())
	expectStatus(t, response, 201)
	json.Unmarshal(response.Body.Bytes(), &body)
	id = body.Data.ID
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", fmt.Sprintf(`{"title":"Materi Excel","subject_id":1,"class_id":1,"asset_id":%d}`, id)), 200)
	response = requestCBT(r, student, "GET", fmt.Sprintf("/api/uploads/%d/preview", id), "")
	expectStatus(t, response, 200)
	if !strings.Contains(response.Body.String(), `"0"`) || strings.Contains(response.Body.String(), "HYPERLINK") || strings.Contains(response.Body.String(), "null") || !strings.Contains(response.Body.String(), "Setelah baris kosong") {
		t.Fatal("preview executed/exposed formula", response.Body.String())
	}
	for _, role := range []uint{4, 5} {
		user := fmt.Sprintf("office-observer-%d", role)
		mustCreate(t, &User{ID: user, Name: "Observer", Email: user + "@test.local", RoleID: role})
		token := tokenFor(t, user, role)
		expectStatus(t, requestCBT(r, token, "GET", fmt.Sprintf("/api/uploads/%d/preview", id), ""), 200)
		expectStatus(t, uploadMaterialFile(t, r, token, "readonly.docx", docxFixture(t)), 403)
	}
}
func TestMixedExamRejectsManualPGAndKeepsSnapshot(t *testing.T) {
	r := setupCBT(t)
	mustCreate(t, &Question{ExamID: 1, Kind: "essay", Points: 2, QuestionText: "Jelaskan alasan", CorrectAnswer: "Pedoman"})
	student := tokenFor(t, "student", 3)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/exams/1/start", ""), 200)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B","2":"C","3":"Uraian"}}`), 200)
	var before ExamAttempt
	DB.First(&before)
	teacher := tokenFor(t, "teacher", 2)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/essays/student", `{"grades":{"1":1,"3":2}}`), 400)
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/essays/student", `{"grades":{"3":2}}`), 200)
	var after ExamAttempt
	DB.First(&after)
	if before.QuestionsJSON != after.QuestionsJSON || before.AnswersJSON != after.AnswersJSON {
		t.Fatal("grading changed source snapshot")
	}
	var result ExamResult
	DB.First(&result)
	if result.Score != 100 || result.PendingGrading {
		t.Fatal(result)
	}
}
func TestOldImportedExamIsFlaggedWithoutRepair(t *testing.T) {
	r := setupCBT(t)
	for _, text := range []string{"A - Pembahasan HTML", "B - Pembahasan JavaScript"} {
		mustCreate(t, &Question{ExamID: 1, Kind: "essay", Points: 1, QuestionText: text})
	}
	student := tokenFor(t, "student", 3)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/exams/1/start", ""), 200)
	expectStatus(t, requestCBT(r, student, "POST", "/api/student/exams/1/submit", `{"answers":{"1":"B","2":"C"}}`), 200)
	var before ExamAttempt
	DB.First(&before)
	var original ExamResult
	DB.First(&original)
	teacher := tokenFor(t, "teacher", 2)
	response := requestCBT(r, teacher, "GET", "/api/teacher/exams/1/questions", "")
	expectStatus(t, response, 200)
	if !strings.Contains(response.Body.String(), "diduga") {
		t.Fatal("old import not flagged")
	}
	expectStatus(t, requestCBT(r, teacher, "PUT", "/api/teacher/exams/1/essays/student", `{"grades":{"3":1,"4":1}}`), 409)
	var after ExamAttempt
	DB.First(&after)
	var result ExamResult
	DB.First(&result)
	if before.QuestionsJSON != after.QuestionsJSON || before.AnswersJSON != after.AnswersJSON || result.Score != original.Score || result.PendingGrading != original.PendingGrading {
		t.Fatal("legacy history changed")
	}
}
func TestReportPreviewMatrixZeroAndExamScope(t *testing.T) {
	r := setupCBT(t)
	teacher := tokenFor(t, "teacher", 2)
	newTask(t, 1, 1, time.Now().Add(-time.Hour))
	now := time.Now().UTC()
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "student", Score: 0, SubmittedAt: now, GradedAt: &now})
	mustCreate(t, &ExamResult{ExamID: 1, StudentID: "student", Score: 80})
	response := requestCBT(r, teacher, "GET", "/api/teacher/export/report/preview", "")
	expectStatus(t, response, 200)
	var preview struct{ Data reportSummary }
	json.Unmarshal(response.Body.Bytes(), &preview)
	if preview.Data.Tasks != 1 || preview.Data.ExamResults != 1 {
		t.Fatal(preview)
	}
	response = requestCBT(r, teacher, "GET", "/api/teacher/export/report", "")
	expectStatus(t, response, 200)
	book, err := excelize.OpenReader(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	if book.GetSheetName(book.GetActiveSheetIndex()) != "Ringkasan" {
		t.Fatal("report opens on empty task sheet")
	}
	rows, err := book.GetRows("Matriks 1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if len(row) > 6 && row[0] == "Siswa" {
			found = true
			if row[2] != "0" || row[3] != "80" || row[len(row)-1] != "40" {
				t.Fatal("zero missing from average", row)
			}
		}
	}
	if !found {
		t.Fatal(rows)
	}
	response = requestCBT(r, teacher, "GET", "/api/teacher/export/report/preview?exam_id=1", "")
	json.Unmarshal(response.Body.Bytes(), &preview)
	if preview.Data.Tasks != 0 || preview.Data.ExamResults != 1 {
		t.Fatal("exam export not scoped", preview)
	}
}
func TestAnalyticsCompletionFinalAveragesAndIsolation(t *testing.T) {
	r := setupCBT(t)
	DB.Model(&Exam{}).Where("id=1").Update("is_active", false)
	mustCreate(t, &Subject{ID: 2, SubjectName: "Jaringan", TeacherID: "other-teacher"})
	newTask(t, 1, 1, time.Now().Add(-time.Hour))
	newTask(t, 2, 2, time.Now().Add(-time.Hour))
	newTask(t, 3, 2, time.Now().Add(-time.Hour))
	DB.Model(&Assignment{}).Where("id IN ?", []uint{2, 3}).Update("subject_id", 2)
	now := time.Now().UTC()
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "student", Score: 0, SubmittedAt: now, GradedAt: &now})
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "peer", Score: 99, SubmittedAt: now})
	mustCreate(t, &Submission{AssignmentID: 2, StudentID: "other", Score: 80, SubmittedAt: now, GradedAt: &now})
	mustCreate(t, &ExamResult{ExamID: 1, StudentID: "student", Score: 100})
	response := requestCBT(r, tokenFor(t, "admin", 1), "GET", "/api/analytics", "")
	expectStatus(t, response, 200)
	mustCreate(t, &User{ID: "analytics-monitor", Name: "Monitor", Email: "analytics-monitor@test.local", RoleID: 4})
	response = requestCBT(r, tokenFor(t, "analytics-monitor", 4), "GET", "/api/analytics", "")
	expectStatus(t, response, 200)
	var data struct{ Classes []metricRow }
	json.Unmarshal(response.Body.Bytes(), &data)
	if len(data.Classes) != 2 {
		t.Fatal(response.Body.String())
	}
	for _, row := range data.Classes {
		if row.ID == "1" {
			if row.Completion == nil || *row.Completion != 100 || row.Average == nil || *row.Average != 50 || row.GradedStudents != 1 {
				t.Fatal(row)
			}
		} else if row.Completion == nil || *row.Completion != 50 {
			t.Fatal(row)
		}
	}
	response = requestCBT(r, tokenFor(t, "teacher", 2), "GET", "/api/analytics", "")
	expectStatus(t, response, 200)
	if strings.Contains(response.Body.String(), "X TJKT") {
		t.Fatal("teacher analytics leak")
	}
	response = requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/analytics", "")
	expectStatus(t, response, 200)
	if strings.Contains(response.Body.String(), "X TJKT") || strings.Contains(response.Body.String(), "Teman") {
		t.Fatal("student analytics leak")
	}
}
func TestAnalyticsWIBBoundaryAndNoData(t *testing.T) {
	r := setupCBT(t)
	DB.Model(&Exam{}).Where("id=1").Update("is_active", false)
	deadline, _ := deadlineFromInput("2026-09-30")
	newTask(t, 1, 1, deadline)
	graded := time.Date(2026, 10, 1, 0, 30, 0, 0, schoolZone)
	mustCreate(t, &Submission{AssignmentID: 1, StudentID: "student", Score: 0, SubmittedAt: graded, GradedAt: &graded})
	response := requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/analytics?month=2026-09", "")
	expectStatus(t, response, 200)
	var data struct{ Months []monthMetric }
	json.Unmarshal(response.Body.Bytes(), &data)
	if len(data.Months) != 1 || data.Months[0].Month != "2026-09" || data.Months[0].TaskAverage == nil || *data.Months[0].TaskAverage != 0 {
		t.Fatal(response.Body.String())
	}
	response = requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/analytics?month=2026-11", "")
	expectStatus(t, response, 200)
	json.Unmarshal(response.Body.Bytes(), &data)
	if len(data.Months) != 0 {
		t.Fatal("empty period invented records")
	}
	expectStatus(t, requestCBT(r, tokenFor(t, "student", 3), "GET", "/api/analytics?from=2026-10-02&until=2026-10-01", ""), 400)
}
