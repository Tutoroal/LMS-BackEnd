package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
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
func ExportTeacherReport(c *gin.Context) {
	if !isGuruOrAdmin(c) {
		return
	}
	if err := finalizeExpiredAttempts(time.Now().UTC()); err != nil {
		sendExamError(c, err)
		return
	}
	taskRows := []struct {
		SubjectName string
		ClassName   string
		Title       string
		StudentName string
		NIS         string
		Score       int
		MaxScore    int
		Feedback    string
		GradedAt    *time.Time
	}{}
	query := DB.Table("submissions").Select("subjects.subject_name, classes.class_name, assignments.title, users.name AS student_name, users.nis, submissions.score, assignments.max_score, submissions.feedback, submissions.graded_at").Joins("JOIN assignments ON assignments.id = submissions.assignment_id").Joins("JOIN subjects ON subjects.id = assignments.subject_id").Joins("JOIN classes ON classes.id = assignments.class_id").Joins("JOIN users ON users.id = submissions.student_id")
	if c.GetFloat64("role_id") != 1 {
		query = query.Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	if err := query.Order("subjects.subject_name, assignments.title, users.name").Scan(&taskRows).Error; err != nil {
		sendExamError(c, err)
		return
	}
	tasks := [][]interface{}{}
	for _, row := range taskRows {
		var value interface{} = ""
		status := "Belum dinilai"
		if row.GradedAt != nil {
			status = "Dinilai"
			value = row.Score
		}
		tasks = append(tasks, []interface{}{row.SubjectName, row.ClassName, row.Title, row.StudentName, row.NIS, value, row.MaxScore, status, row.Feedback})
	}
	examRows := []struct {
		SubjectName string
		ClassName   string
		Title       string
		StudentName string
		NIS         string
		Score       float64
	}{}
	query = DB.Table("exam_results").Select("subjects.subject_name, classes.class_name, exams.title, users.name AS student_name, users.nis, exam_results.score").Joins("JOIN exams ON exams.id = exam_results.exam_id").Joins("JOIN subjects ON subjects.id = exams.subject_id").Joins("JOIN classes ON classes.id = exams.class_id").Joins("JOIN users ON users.id = exam_results.student_id")
	if c.GetFloat64("role_id") != 1 {
		query = query.Where("subjects.teacher_id = ?", c.GetString("user_id"))
	}
	if err := query.Order("subjects.subject_name, exams.title, users.name").Scan(&examRows).Error; err != nil {
		sendExamError(c, err)
		return
	}
	exams := [][]interface{}{}
	for _, row := range examRows {
		exams = append(exams, []interface{}{row.SubjectName, row.ClassName, row.Title, row.StudentName, row.NIS, row.Score, 100})
	}
	workbook := excelize.NewFile()
	defer workbook.Close()
	if err := reportSheet(workbook, "Nilai Tugas", []string{"Mapel", "Kelas", "Tugas", "Nama Siswa", "NIS", "Nilai", "Maksimal", "Status", "Catatan"}, tasks); err != nil {
		sendExamError(c, err)
		return
	}
	if err := reportSheet(workbook, "Nilai Ujian", []string{"Mapel", "Kelas", "Ujian", "Nama Siswa", "NIS", "Nilai", "Maksimal"}, exams); err != nil {
		sendExamError(c, err)
		return
	}
	if err := workbook.DeleteSheet("Sheet1"); err != nil {
		sendExamError(c, err)
		return
	}
	sendWorkbook(c, workbook, "Laporan_Nilai_Siswa.xlsx")
}
