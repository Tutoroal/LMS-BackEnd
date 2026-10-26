package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"strings"
	"time"
)

type metricRow struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	ClassName      string   `json:"class_name,omitempty"`
	Due            int64    `json:"due"`
	Completed      int64    `json:"completed"`
	Completion     *float64 `json:"completion"`
	Average        *float64 `json:"average"`
	GradedStudents int64    `json:"graded_students"`
	Students       int64    `json:"students"`
}
type monthMetric struct {
	Month       string   `json:"month"`
	TaskAverage *float64 `json:"task_average"`
	ExamAverage *float64 `json:"exam_average"`
	Due         int64    `json:"due"`
	Completed   int64    `json:"completed"`
	Completion  *float64 `json:"completion"`
}

func analyticsRange(c *gin.Context) (string, string, error) {
	month := c.Query("month")
	if month != "" {
		if c.Query("from") != "" || c.Query("until") != "" {
			return "", "", failExam(400, "Pilih bulan atau rentang tanggal.")
		}
		start, end, err := reportRange(month)
		return start.Format(time.RFC3339), end.Format(time.RFC3339), err
	}
	from, until := c.Query("from"), c.Query("until")
	if from == "" && until == "" {
		return "", "", nil
	}
	start, err := time.ParseInLocation("2006-01-02", from, schoolZone)
	if err != nil {
		return "", "", failExam(400, "Tanggal awal tidak valid.")
	}
	end, err := time.ParseInLocation("2006-01-02", until, schoolZone)
	if err != nil || end.Before(start) {
		return "", "", failExam(400, "Tanggal akhir tidak valid.")
	}
	return start.UTC().Format(time.RFC3339), end.AddDate(0, 0, 1).UTC().Format(time.RFC3339), nil
}
func GetAnalytics(c *gin.Context) {
	role := c.GetFloat64("role_id")
	if role < 1 || role > 6 {
		c.JSON(403, gin.H{"error": "Akses analitik tidak tersedia."})
		return
	}
	if err := validateReportFilters(c); err != nil {
		sendExamError(c, err)
		return
	}
	from, until, err := analyticsRange(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if role == 6 {
		distribution := []struct {
			RoleID uint   `json:"role_id"`
			Name   string `json:"name"`
			Count  int64  `json:"count"`
		}{}
		if err := DB.Table("roles").Select("roles.id AS role_id,roles.role_name AS name,COUNT(users.id) AS count").Joins("LEFT JOIN users ON users.role_id=roles.id").Group("roles.id,roles.role_name").Order("roles.id").Scan(&distribution).Error; err != nil {
			sendExamError(c, err)
			return
		}
		growth := []struct {
			Month string `json:"month"`
			Count int64  `json:"count"`
		}{}
		query := DB.Table("users").Select("strftime('%Y-%m',created_at,'+7 hours') AS month,COUNT(*) AS count").Where("datetime(created_at)>datetime('2000-01-01')")
		if from != "" {
			query = query.Where("datetime(created_at)>=datetime(?) AND datetime(created_at)<datetime(?)", from, until)
		}
		if err := query.Group("month").Order("month").Scan(&growth).Error; err != nil {
			sendExamError(c, err)
			return
		}
		c.JSON(200, gin.H{"role": role, "users": distribution, "growth": growth})
		return
	}
	conditions := []string{"1=1"}
	args := []interface{}{}
	if role == 2 {
		conditions = append(conditions, "subjects.teacher_id=?")
		args = append(args, c.GetString("user_id"))
	}
	for _, filter := range []struct{ key, col string }{{"class_id", "o.class_id"}, {"subject_id", "subjects.id"}} {
		if value := c.Query(filter.key); value != "" {
			conditions = append(conditions, filter.col+"=?")
			args = append(args, value)
		}
	}
	if from != "" {
		conditions = append(conditions, "datetime(o.due)>=datetime(?) AND datetime(o.due)<datetime(?)")
		args = append(args, from, until)
	}
	memberWhere := ""
	if role == 3 {
		memberWhere = " AND u.id=?"
	}
	now := time.Now().UTC()
	cte := `WITH assessments AS (SELECT 'task' AS kind,id,class_id,subject_id,deadline AS due,max_score,1 AS active FROM assignments UNION ALL SELECT 'exam',id,class_id,subject_id,date,100,is_active FROM exams), scoped AS(SELECT o.* FROM assessments o JOIN subjects ON subjects.id=o.subject_id WHERE ` + strings.Join(conditions, " AND ") + `), members AS(SELECT o.*,u.id AS student_id,u.name AS student_name FROM scoped o JOIN users u ON u.role_id=3 AND (u.class_id=o.class_id OR EXISTS(SELECT 1 FROM submissions WHERE o.kind='task' AND assignment_id=o.id AND student_id=u.id) OR EXISTS(SELECT 1 FROM exam_results WHERE o.kind='exam' AND exam_id=o.id AND student_id=u.id)) WHERE 1=1` + memberWhere + `), valueset AS(SELECT m.*,CASE WHEN (m.kind='task' AND datetime(m.due)<=datetime(?)) OR (m.kind='exam' AND m.active=1 AND date(m.due)<=?) THEN 1 ELSE 0 END AS eligible,CASE WHEN s.id IS NOT NULL OR r.id IS NOT NULL THEN 1 ELSE 0 END AS finished,CASE WHEN m.kind='task' AND s.graded_at IS NOT NULL AND m.max_score>0 THEN 100.0*s.score/m.max_score WHEN m.kind='exam' AND r.id IS NOT NULL AND r.pending_grading=0 THEN r.score ELSE NULL END AS final_score FROM members m LEFT JOIN submissions s ON m.kind='task' AND s.assignment_id=m.id AND s.student_id=m.student_id LEFT JOIN exam_results r ON m.kind='exam' AND r.exam_id=m.id AND r.student_id=m.student_id), student_metrics AS(SELECT student_id,student_name,class_id,SUM(eligible) AS due,SUM(eligible*finished) AS completed,AVG(final_score) AS average FROM valueset GROUP BY student_id,student_name,class_id) `
	if role == 3 {
		args = append(args, c.GetString("user_id"))
	}
	args = append(args, now, now.In(schoolZone).Format("2006-01-02"))
	classes := []metricRow{}
	query := `SELECT CAST(classes.id AS TEXT) AS id,classes.class_name AS name,SUM(m.due) AS due,SUM(m.completed) AS completed,CASE WHEN SUM(m.due)>0 THEN 100.0*SUM(m.completed)/SUM(m.due) END AS completion,AVG(m.average) AS average,COUNT(m.average) AS graded_students,COUNT(*) AS students FROM student_metrics m JOIN classes ON classes.id=m.class_id GROUP BY classes.id,classes.class_name ORDER BY completion DESC,classes.class_name`
	if err := DB.Raw(cte+query, args...).Scan(&classes).Error; err != nil {
		sendExamError(c, err)
		return
	}
	students := []metricRow{}
	if role == 2 {
		query = `SELECT m.student_id AS id,m.student_name AS name,classes.class_name,SUM(m.due) AS due,SUM(m.completed) AS completed,CASE WHEN SUM(m.due)>0 THEN 100.0*SUM(m.completed)/SUM(m.due) END AS completion,AVG(m.average) AS average FROM student_metrics m JOIN classes ON classes.id=m.class_id GROUP BY m.student_id,m.student_name,m.class_id,classes.class_name ORDER BY completion DESC,m.student_name`
		if err := DB.Raw(cte+query, args...).Scan(&students).Error; err != nil {
			sendExamError(c, err)
			return
		}
	}
	months := []monthMetric{}
	if role == 3 {
		query = `SELECT strftime('%Y-%m',due,CASE WHEN kind='task' THEN '+7 hours' ELSE '+0 hours' END) AS month,AVG(CASE WHEN kind='task' THEN final_score END) AS task_average,AVG(CASE WHEN kind='exam' THEN final_score END) AS exam_average,SUM(eligible) AS due,SUM(eligible*finished) AS completed,CASE WHEN SUM(eligible)>0 THEN 100.0*SUM(eligible*finished)/SUM(eligible) END AS completion FROM valueset GROUP BY month ORDER BY month`
		if err := DB.Raw(cte+query, args...).Scan(&months).Error; err != nil {
			sendExamError(c, err)
			return
		}
	}
	c.JSON(200, gin.H{"role": role, "classes": classes, "students": students, "months": months, "definition": fmt.Sprintf("Penyelesaian tugas yang jatuh tempo dan ujian aktif sampai %s WIB. Nilai menurut bulan tenggat/jadwal; rata-rata hanya hasil final.", now.In(schoolZone).Format("2006-01-02"))})
}
