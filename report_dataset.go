package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"sort"
	"strconv"
	"strings"
	"time"
)

type examReportRow struct {
	ExamID, ClassID, SubjectID                                                         uint
	TeacherID, TeacherName, SubjectName, ClassName, Title, StudentName, NIS, StudentID string
	Score                                                                              float64
	PendingGrading                                                                     bool
}
type reportSummary struct {
	Students    int    `json:"students"`
	Tasks       int    `json:"tasks"`
	Exams       int    `json:"exams"`
	ExamResults int    `json:"exam_results"`
	Pending     int    `json:"pending"`
	Rows        int    `json:"rows"`
	Message     string `json:"message"`
}
type reportData struct {
	Tasks   []taskReportRow
	Exams   []examReportRow
	Summary reportSummary
}

func validateReportFilters(c *gin.Context) error {
	for _, key := range []string{"class_id", "subject_id", "exam_id"} {
		if value := c.Query(key); value != "" {
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil || n == 0 {
				return failExam(400, "Filter "+key+" tidak valid.")
			}
		}
	}
	return nil
}
func loadTaskReportRows(c *gin.Context, start, end time.Time, key, column string) ([]taskReportRow, error) {
	rows := []taskReportRow{}
	if c.Query("exam_id") != "" {
		return rows, nil
	}
	query := DB.Table("assignments").Select("assignments.id AS assignment_id,assignments.class_id,assignments.subject_id,subjects.teacher_id,teacher.name AS teacher_name,subjects.subject_name,classes.class_name,assignments.title,users.name AS student_name,users.id AS student_id,users.nis,submissions.score,assignments.max_score,submissions.feedback,assignments.deadline,submissions.submitted_at,submissions.graded_at").Joins("JOIN subjects ON subjects.id=assignments.subject_id").Joins("JOIN users AS teacher ON teacher.id=subjects.teacher_id").Joins("JOIN classes ON classes.id=assignments.class_id").Joins("JOIN users ON users.role_id=3 AND (users.class_id=assignments.class_id OR EXISTS(SELECT 1 FROM submissions AS historical WHERE historical.assignment_id=assignments.id AND historical.student_id=users.id))").Joins("LEFT JOIN submissions ON submissions.assignment_id=assignments.id AND submissions.student_id=users.id")
	query = reportScope(c, query)
	if !start.IsZero() {
		query = query.Where(column+" >= ? AND "+column+" < ?", start, end)
	}
	if key != "deadline" {
		query = query.Where(column + " IS NOT NULL")
	}
	err := query.Order("classes.class_name,subjects.subject_name,users.name,assignments.deadline").Scan(&rows).Error
	return rows, err
}
func loadExamReportRows(c *gin.Context, start, end time.Time) ([]examReportRow, error) {
	rows := []examReportRow{}
	query := DB.Table("exam_results").Select("exams.id AS exam_id,exams.class_id,exams.subject_id,subjects.teacher_id,teacher.name AS teacher_name,subjects.subject_name,classes.class_name,exams.title,users.id AS student_id,users.name AS student_name,users.nis,exam_results.score,exam_results.pending_grading").Joins("JOIN exams ON exams.id=exam_results.exam_id").Joins("JOIN subjects ON subjects.id=exams.subject_id").Joins("JOIN users AS teacher ON teacher.id=subjects.teacher_id").Joins("JOIN classes ON classes.id=exams.class_id").Joins("JOIN users ON users.id=exam_results.student_id")
	query = reportScope(c, query)
	if id := c.Query("exam_id"); id != "" {
		query = query.Where("exams.id=?", id)
	}
	if !start.IsZero() {
		query = query.Where("exams.date >= ? AND exams.date < ?", start.In(schoolZone).Format("2006-01-02"), end.In(schoolZone).Format("2006-01-02"))
	}
	err := query.Order("classes.class_name,subjects.subject_name,users.name").Scan(&rows).Error
	return rows, err
}
func loadReportData(c *gin.Context, start, end time.Time, basis string) (reportData, error) {
	out := reportData{Tasks: []taskReportRow{}, Exams: []examReportRow{}}
	seen := map[string]bool{}
	tasks, exams, students := map[uint]bool{}, map[uint]bool{}, map[string]bool{}
	for _, item := range []struct{ key, column string }{{"deadline", "assignments.deadline"}, {"submitted", "submissions.submitted_at"}, {"graded", "submissions.graded_at"}} {
		if basis != "all" && basis != item.key {
			continue
		}
		rows, err := loadTaskReportRows(c, start, end, item.key, item.column)
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			key := fmt.Sprintf("%d/%s", row.AssignmentID, row.StudentID)
			if seen[key] {
				continue
			}
			seen[key] = true
			out.Tasks = append(out.Tasks, row)
			tasks[row.AssignmentID] = true
			students[row.StudentID] = true
			if row.SubmittedAt != nil && row.GradedAt == nil {
				out.Summary.Pending++
			}
		}
	}
	rows, err := loadExamReportRows(c, start, end)
	if err != nil {
		return out, err
	}
	out.Exams = rows
	for _, row := range rows {
		exams[row.ExamID] = true
		students[row.StudentID] = true
		if row.PendingGrading {
			out.Summary.Pending++
		}
	}
	out.Summary.Tasks = len(tasks)
	out.Summary.Exams = len(exams)
	out.Summary.ExamResults = len(rows)
	out.Summary.Students = len(students)
	out.Summary.Rows = len(out.Tasks) + len(rows)
	if out.Summary.Rows == 0 {
		out.Summary.Message = "Tidak ada data tugas/hasil ujian untuk filter ini. Pilih semua waktu atau periksa kelas dan pengampu."
	}
	return out, nil
}
func PreviewTeacherReport(c *gin.Context) {
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
		c.JSON(400, gin.H{"error": "Dasar laporan tidak valid."})
		return
	}
	data, err := loadReportData(c, start, end, basis)
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": data.Summary})
}
func enrichReportWorkbook(c *gin.Context, book *excelize.File, start, end time.Time, basis string) error {
	data, err := loadReportData(c, start, end, basis)
	if err != nil {
		return err
	}
	summary := data.Summary
	period := c.Query("month")
	if period == "" {
		period = "Semua waktu"
	}
	values := [][]interface{}{{"Periode", period}, {"Dasar tugas", basis}, {"Siswa", summary.Students}, {"Tugas", summary.Tasks}, {"Ujian dengan hasil", summary.Exams}, {"Hasil ujian", summary.ExamResults}, {"Menunggu penilaian", summary.Pending}, {"Catatan", "Nilai sementara dikecualikan dari rata-rata final. Nilai nol tetap dihitung."}}
	if summary.Message != "" {
		values = append(values, []interface{}{"Data", summary.Message})
	}
	if err := reportSheet(book, "Ringkasan", []string{"Ringkasan laporan", "Nilai"}, values); err != nil {
		return err
	}
	type assessment struct{ key, title, kind string }
	type person struct {
		id         string
		name, nis  string
		values     map[string]interface{}
		task, exam []float64
	}
	type group struct {
		label   string
		columns map[string]assessment
		people  map[string]*person
	}
	groups := map[string]*group{}
	ensure := func(class, subject uint, label, student, name, nis string) *group {
		key := fmt.Sprintf("%d/%d", class, subject)
		g := groups[key]
		if g == nil {
			g = &group{label: label, columns: map[string]assessment{}, people: map[string]*person{}}
			groups[key] = g
		}
		if g.people[student] == nil {
			g.people[student] = &person{id: student, name: name, nis: nis, values: map[string]interface{}{}}
		}
		return g
	}
	for _, row := range data.Tasks {
		g := ensure(row.ClassID, row.SubjectID, row.ClassName+" / "+row.SubjectName+" / "+row.TeacherName, row.StudentID, row.StudentName, row.NIS)
		key := fmt.Sprintf("T%d", row.AssignmentID)
		g.columns[key] = assessment{key, "Tugas #" + strconv.Itoa(int(row.AssignmentID)) + " " + row.Title, "task"}
		p := g.people[row.StudentID]
		if row.GradedAt != nil {
			p.values[key] = row.Score
			if row.MaxScore > 0 {
				p.task = append(p.task, float64(row.Score)*100/float64(row.MaxScore))
			}
		} else if row.SubmittedAt != nil {
			p.values[key] = "Belum dinilai"
		} else {
			p.values[key] = "Belum mengumpulkan"
			if c.Query("missing_zero") == "true" {
				p.task = append(p.task, 0)
			}
		}
	}
	for _, row := range data.Exams {
		g := ensure(row.ClassID, row.SubjectID, row.ClassName+" / "+row.SubjectName+" / "+row.TeacherName, row.StudentID, row.StudentName, row.NIS)
		key := fmt.Sprintf("U%d", row.ExamID)
		g.columns[key] = assessment{key, "Ujian #" + strconv.Itoa(int(row.ExamID)) + " " + row.Title, "exam"}
		p := g.people[row.StudentID]
		if row.PendingGrading {
			p.values[key] = "Menunggu esai"
		} else {
			p.values[key] = row.Score
			p.exam = append(p.exam, row.Score)
		}
	}
	classIDs := []uint{}
	for key := range groups {
		parts := strings.Split(key, "/")
		id, _ := strconv.ParseUint(parts[0], 10, 32)
		classIDs = append(classIDs, uint(id))
	}
	var roster []User
	if len(classIDs) > 0 {
		if err := DB.Select("id,name,nis,class_id").Where("role_id=3 AND class_id IN ?", classIDs).Find(&roster).Error; err != nil {
			return err
		}
	}
	currentMembers := map[uint]map[string]bool{}
	for _, user := range roster {
		if user.ClassID != nil {
			if currentMembers[*user.ClassID] == nil {
				currentMembers[*user.ClassID] = map[string]bool{}
			}
			currentMembers[*user.ClassID][user.ID] = true
		}
	}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index, key := range keys {
		g := groups[key]
		parts := strings.Split(key, "/")
		class, _ := strconv.ParseUint(parts[0], 10, 32)
		for _, user := range roster {
			if user.ClassID != nil && *user.ClassID == uint(class) && g.people[user.ID] == nil {
				g.people[user.ID] = &person{id: user.ID, name: user.Name, nis: user.NIS, values: map[string]interface{}{}}
			}
		}
		columns := []assessment{}
		for _, value := range g.columns {
			columns = append(columns, value)
		}
		sort.Slice(columns, func(i, j int) bool { return columns[i].key < columns[j].key })
		headers := []string{"Nama siswa", "NIS"}
		for _, col := range columns {
			headers = append(headers, col.title)
		}
		headers = append(headers, "Rata-rata tugas /100", "Rata-rata ujian /100", "Rata-rata gabungan /100")
		people := []*person{}
		for _, p := range g.people {
			people = append(people, p)
		}
		sort.Slice(people, func(i, j int) bool { return people[i].name < people[j].name })
		matrix := [][]interface{}{}
		for _, p := range people {
			row := []interface{}{p.name, p.nis}
			for _, col := range columns {
				value, ok := p.values[col.key]
				if !ok {
					if col.kind == "exam" {
						value = "Tidak ditugaskan"
						if currentMembers[uint(class)][p.id] {
							value = "Belum mengerjakan"
						}
					} else {
						value = "Tidak ditugaskan"
						if currentMembers[uint(class)][p.id] {
							value = "Belum mengumpulkan"
							if c.Query("missing_zero") == "true" {
								p.task = append(p.task, 0)
							}
						}
					}
				}
				row = append(row, value)
			}
			row = append(row, reportAverage(p.task), reportAverage(p.exam), reportAverage(append(append([]float64{}, p.task...), p.exam...)))
			matrix = append(matrix, row)
		}
		name := fmt.Sprintf("Matriks %d", index+1)
		if err := reportSheet(book, name, headers, matrix); err != nil {
			return err
		}
		if err := book.SetCellStr(name, "A"+strconv.Itoa(len(matrix)+3), g.label); err != nil {
			return err
		}
	}
	return nil
}
func reportAverage(values []float64) interface{} {
	if len(values) == 0 {
		return ""
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
