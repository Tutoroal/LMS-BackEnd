package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
	"time"
)

func sendWorkbook(c *gin.Context, workbook *excelize.File, filename string) {
	buffer, err := workbook.WriteToBuffer()
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Cache-Control", "no-store")
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buffer.Bytes())
}
func reportSheet(workbook *excelize.File, name string, headers []string, rows [][]interface{}) error {
	if _, err := workbook.NewSheet(name); err != nil {
		return err
	}
	style, err := workbook.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4F46E5"}}})
	if err != nil {
		return err
	}
	for index, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(index+1, 1)
		if err := workbook.SetCellStr(name, cell, header); err != nil {
			return err
		}
		if err := workbook.SetCellStyle(name, cell, cell, style); err != nil {
			return err
		}
	}
	for index, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, index+2)
		if err := workbook.SetSheetRow(name, cell, &row); err != nil {
			return err
		}
	}
	last, _ := excelize.ColumnNumberToName(len(headers))
	return workbook.SetColWidth(name, "A", last, 24)
}

type taskReportRow struct {
	AssignmentID uint
	ClassID      uint
	SubjectID    uint
	TeacherID    string
	TeacherName  string
	SubjectName  string
	ClassName    string
	Title        string
	StudentName  string
	NIS          string
	StudentID    string
	Score        int
	MaxScore     int
	Feedback     string
	Deadline     time.Time
	SubmittedAt  *time.Time
	GradedAt     *time.Time
}

func reportRange(month string) (time.Time, time.Time, error) {
	if month == "" {
		return time.Time{}, time.Time{}, nil
	}
	start, err := time.ParseInLocation("2006-01", month, schoolZone)
	if err != nil || start.Format("2006-01") != month {
		return start, start, failExam(400, "Bulan laporan harus YYYY-MM.")
	}
	return start.UTC(), start.AddDate(0, 1, 0).UTC(), nil
}
func reportScope(c *gin.Context, query *gorm.DB) *gorm.DB {
	if restrictTeacher(c) {
		query = query.Where("subjects.teacher_id=?", c.GetString("user_id"))
	} else if id := c.Query("teacher_id"); id != "" {
		query = query.Where("subjects.teacher_id=?", id)
	}
	if id := c.Query("class_id"); id != "" {
		query = query.Where("classes.id=?", id)
	}
	if id := c.Query("subject_id"); id != "" {
		query = query.Where("subjects.id=?", id)
	}
	return query
}
func ExportTeacherReport(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	if err := validateReportFilters(c); err != nil {
		sendExamError(c, err)
		return
	}
	start, end, err := reportRange(c.Query("month"))
	if err != nil {
		sendExamError(c, err)
		return
	}
	basis := c.DefaultQuery("basis", "all")
	if basis != "all" && basis != "deadline" && basis != "submitted" && basis != "graded" {
		c.JSON(400, gin.H{"error": "Dasar bulan harus deadline, submitted, graded, atau all."})
		return
	}
	if err := finalizeExpiredAttempts(time.Now().UTC()); err != nil {
		sendExamError(c, err)
		return
	}
	workbook := excelize.NewFile()
	defer workbook.Close()
	for _, item := range []struct {
		Key    string
		Column string
		Name   string
	}{{"deadline", "assignments.deadline", "Tenggat"}, {"submitted", "submissions.submitted_at", "Pengumpulan"}, {"graded", "submissions.graded_at", "Penilaian"}} {
		if basis != "all" && basis != item.Key {
			continue
		}
		rows, err := loadTaskReportRows(c, start, end, item.Key, item.Column)
		if err != nil {
			sendExamError(c, err)
			return
		}
		values := [][]interface{}{}
		type sum struct {
			Name      string
			NIS       string
			Class     string
			Subject   string
			Total     int
			Submitted int
			Graded    int
			Score     float64
		}
		summaries := map[string]*sum{}
		order := []string{}
		for _, row := range rows {
			status := "Belum mengumpulkan"
			var value interface{} = ""
			if row.SubmittedAt != nil {
				status = "Belum dinilai"
			}
			if row.GradedAt != nil {
				status = "Dinilai"
				value = row.Score
			}
			values = append(values, []interface{}{row.SubjectName, row.ClassName, row.Title, row.StudentName, row.NIS, value, row.MaxScore, status, row.Deadline.In(schoolZone).Format("2006-01-02 15:04"), reportTime(row.SubmittedAt), reportTime(row.GradedAt), row.Feedback})
			key := fmt.Sprintf("%s/%d/%d", row.StudentID, row.ClassID, row.SubjectID)
			if summaries[key] == nil {
				summaries[key] = &sum{Name: row.StudentName, NIS: row.NIS, Class: row.ClassName, Subject: row.SubjectName}
				order = append(order, key)
			}
			summary := summaries[key]
			summary.Total++
			if row.SubmittedAt != nil {
				summary.Submitted++
			}
			if row.GradedAt != nil {
				summary.Graded++
				if row.MaxScore > 0 {
					summary.Score += float64(row.Score) * 100 / float64(row.MaxScore)
				}
			}
		}
		if err := reportSheet(workbook, taskSheetName(item.Key, item.Name), []string{"Mapel", "Kelas", "Tugas", "Nama Siswa", "NIS", "Nilai", "Maksimal", "Status", "Tenggat WIB", "Dikumpulkan WIB", "Dinilai WIB", "Catatan"}, values); err != nil {
			sendExamError(c, err)
			return
		}
		aggregate := [][]interface{}{}
		for _, key := range order {
			row := summaries[key]
			var avg interface{} = ""
			denominator := row.Graded
			if c.Query("missing_zero") == "true" {
				denominator = row.Graded + (row.Total - row.Submitted)
			}
			if denominator > 0 {
				avg = row.Score / float64(denominator)
			}
			aggregate = append(aggregate, []interface{}{row.Name, row.NIS, row.Class, row.Subject, row.Total, row.Submitted, row.Graded, row.Total - row.Submitted, row.Submitted - row.Graded, avg})
		}
		if err := reportSheet(workbook, "Rekap "+item.Name, []string{"Nama", "NIS", "Kelas", "Mapel", "Jumlah Tugas", "Dikumpulkan", "Dinilai", "Belum Mengumpulkan", "Belum Dinilai", "Rata-rata /100"}, aggregate); err != nil {
			sendExamError(c, err)
			return
		}
	}
	exams, err := loadExamReportRows(c, start, end)
	if err != nil {
		sendExamError(c, err)
		return
	}
	examRows := [][]interface{}{}
	for _, row := range exams {
		status := "Final"
		if row.PendingGrading {
			status = "Menunggu penilaian esai"
		}
		examRows = append(examRows, []interface{}{row.SubjectName, row.ClassName, row.Title, row.StudentName, row.NIS, row.Score, 100, status})
	}
	if err := reportSheet(workbook, "Nilai Ujian", []string{"Mapel", "Kelas", "Ujian", "Nama Siswa", "NIS", "Nilai", "Maksimal", "Status"}, examRows); err != nil {
		sendExamError(c, err)
		return
	}
	if err := enrichReportWorkbook(c, workbook, start, end, basis); err != nil {
		sendExamError(c, err)
		return
	}
	if err := workbook.DeleteSheet("Sheet1"); err != nil {
		sendExamError(c, err)
		return
	}
	index, _ := workbook.GetSheetIndex("Ringkasan")
	workbook.SetActiveSheet(index)
	sendWorkbook(c, workbook, "Laporan_Nilai_Siswa.xlsx")
}
func reportTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.In(schoolZone).Format("2006-01-02 15:04")
}

func taskSheetName(key, name string) string {
	if key == "deadline" {
		return "Nilai Tugas"
	}
	return "Tugas " + name
}
