package main

import (
	"encoding/json"
	"fmt"
	"github.com/xuri/excelize/v2"
	"os"
	"strings"
	"testing"
)

func TestSchoolScheduleWorkbook(t *testing.T) {
	book := excelize.NewFile()
	defer book.Close()
	for cell, value := range map[string]string{"A1": "2026/2027", "D6": "10 PPLG PLUS", "E6": "X TKJ 1", "A9": "S E N I N", "C9": "6.30 - 07.10", "D9": "10", "E9": "6", "C10": "07.10 - 07.50", "D10": "10", "E10": "6", "C11": "14.00 - 14.40", "D11": "10"} {
		if err := book.SetCellValue("Sheet1", cell, value); err != nil {
			t.Fatal(err)
		}
	}
	out, err := parseScheduleWorkbook(book)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Classes) != 2 || len(out.Slots) != 5 || out.Year != "2026/2027" || out.Classes[0].Program != "plus" || out.Classes[1].Major != "TJKT" || out.Slots[0].Start != "06:30" || out.Slots[4].End != "14:40" {
		t.Fatalf("unexpected preview: %+v", out)
	}
}
func TestProvidedSchoolSchedule(t *testing.T) {
	path := os.Getenv("LMS_SCHEDULE_EXAMPLE")
	if path == "" {
		t.Skip("optional real school timetable")
	}
	book, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	out, err := parseScheduleWorkbook(book)
	if err != nil {
		t.Fatal(err)
	}
	plus := 0
	for _, c := range out.Classes {
		if c.Program == "plus" {
			plus++
		}
	}
	if len(out.Classes) != 59 || plus != 10 || out.Year != "2026/2027" || len(out.Slots) < 1000 {
		t.Fatalf("unexpected school data classes=%d plus=%d slots=%d year=%s", len(out.Classes), plus, len(out.Slots), out.Year)
	}
	t.Logf("school workbook: %d classes, %d Plus, %d lesson slots", len(out.Classes), plus, len(out.Slots))
}
func TestScheduleImportAtomicReview(t *testing.T) {
	r := setupCBT(t)
	token := tokenFor(t, "admin", 1)
	payload := func(second bool, subject uint) string {
		slots := fmt.Sprintf(`{"class_name":"10 PPLG PLUS","teacher_code":"10","subject_id":%d,"day":"Senin","start":"06:30:00","end":"07:10:00","cell":"D9"}`, subject)
		if second {
			slots += `,{"class_name":"10 TJKT 1","teacher_code":"10","subject_id":1,"day":"Senin","start":"06:30","end":"07:10","cell":"E9"}`
		}
		return `{"year":"2026/2027","semester":"Ganjil","from":"2026-08-17","assign_classes":true,"classes":[{"name":"10 PPLG PLUS","major":"PPLG","program":"plus","create":true},{"name":"10 TJKT 1","class_id":2}],"slots":[` + slots + `]}`
	}
	expectStatus(t, requestCBT(r, token, "POST", "/api/admin/schedules/import", payload(false, 0)), 400)
	response := requestCBT(r, token, "POST", "/api/admin/schedules/import", payload(true, 1))
	expectStatus(t, response, 409)
	if !strings.Contains(response.Body.String(), "E9") {
		t.Fatal(response.Body.String())
	}
	var count int64
	DB.Model(&Class{}).Count(&count)
	if count != 2 {
		t.Fatal("failed import created class")
	}
	DB.Model(&Schedule{}).Count(&count)
	if count != 0 {
		t.Fatal("failed import saved slots")
	}
	expectStatus(t, requestCBT(r, token, "POST", "/api/admin/schedules/import", payload(false, 1)), 200)
	var row Schedule
	DB.First(&row)
	if row.TeacherCode != "10" || row.StartTime != "06:30" || row.EndTime != "07:10" {
		t.Fatal(row)
	}
	DB.Model(&TeachingAllocation{}).Where("subject_id=1 AND class_id=?", row.ClassID).Count(&count)
	if count != 1 {
		t.Fatal("reviewed allocation not saved")
	}
	expectStatus(t, requestCBT(r, token, "POST", "/api/admin/schedules/import", payload(false, 1)), 409)
	for _, role := range []uint{2, 3, 4, 5} {
		mustCreate(t, &User{ID: fmt.Sprintf("schedule-role-%d", role), Name: "Pemantau", Email: fmt.Sprintf("schedule-role-%d@test.local", role), RoleID: role})
		expectStatus(t, requestCBT(r, tokenFor(t, fmt.Sprintf("schedule-role-%d", role), role), "POST", "/api/admin/schedules/import", "{}"), 403)
	}
}
func TestScheduleClockValidation(t *testing.T) {
	r := setupCBT(t)
	token := tokenFor(t, "admin", 1)
	payload := func(start, end string) string {
		return fmt.Sprintf(`{"class_id":1,"subject_id":1,"day_of_week":"Senin","start_time":%q,"end_time":%q,"academic_year":"2026/2027","semester":"Ganjil","effective_from":"2026-08-17"}`, start, end)
	}
	expectStatus(t, requestCBT(r, token, "POST", "/api/admin/schedules", payload("6:30:00", "07:10:00")), 200)
	response := requestCBT(r, token, "POST", "/api/admin/schedules", payload("08:00", "07:00"))
	expectStatus(t, response, 400)
	if !strings.Contains(response.Body.String(), "Jam selesai harus setelah jam mulai") {
		t.Fatal(response.Body.String())
	}
	response = requestCBT(r, token, "POST", "/api/admin/schedules", payload("25:00", "26:00"))
	expectStatus(t, response, 400)
	if !strings.Contains(response.Body.String(), "Jam mulai tidak valid") {
		t.Fatal(response.Body.String())
	}
}

func TestMultipleTeachersSameSubjectAndMultipleClasses(t *testing.T) {
	r := setupCBT(t)
	admin := tokenFor(t, "admin", 1)
	for _, teacher := range []string{"teacher", "other-teacher"} {
		response := requestCBT(r, admin, "POST", "/api/admin/subjects", fmt.Sprintf(`{"subject_name":"Bahasa Inggris","teacher_id":%q,"class_ids":[1,2]}`, teacher))
		expectStatus(t, response, 200)
	}
	var subjects []Subject
	DB.Where("subject_name=?", "Bahasa Inggris").Order("id").Find(&subjects)
	if len(subjects) != 2 {
		t.Fatal("same subject name must allow multiple teachers")
	}
	for index, subject := range subjects {
		for _, class := range []uint{1, 2} {
			response := requestCBT(r, admin, "POST", "/api/admin/schedules", fmt.Sprintf(`{"class_id":%d,"subject_id":%d,"day_of_week":"Selasa","start_time":"%02d:00","end_time":"%02d:30","academic_year":"2026/2027","semester":"Ganjil","effective_from":"2026-08-17"}`, class, subject.ID, 6+index*2+int(class), 6+index*2+int(class)))
			expectStatus(t, response, 200)
		}
		response := requestCBT(r, tokenFor(t, subject.TeacherID, 2), "GET", "/api/subjects", "")
		expectStatus(t, response, 200)
		var data struct{ Data []Subject }
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range data.Data {
			if row.TeacherID != subject.TeacherID {
				t.Fatal("other teacher's subject leaked")
			}
			if row.ID == subject.ID {
				found = true
				if len(row.ClassIDs) != 2 {
					t.Fatal("multiple class allocations missing")
				}
			}
		}
		if !found {
			t.Fatal("assigned subject missing")
		}
	}
}
