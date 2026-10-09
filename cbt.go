package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Questions and their keys are snapshotted once, so later edits cannot change a score.
type ExamAttempt struct {
	ID            uint   `gorm:"primaryKey"`
	ExamID        uint   `gorm:"uniqueIndex:idx_attempt_student_exam;not null"`
	Exam          Exam   `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	StudentID     string `gorm:"uniqueIndex:idx_attempt_student_exam;not null"`
	Student       User   `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	StartedAt     time.Time
	ExpiresAt     time.Time `gorm:"index"`
	SubmittedAt   *time.Time
	QuestionsJSON string `json:"-"`
	AnswersJSON   string `json:"-"`
}

type studentQuestion struct {
	ID           uint
	QuestionText string
	OptionA      string
	OptionB      string
	OptionC      string
	OptionD      string
}

type examError struct {
	status  int
	message string
}

func (e examError) Error() string               { return e.message }
func failExam(status int, message string) error { return examError{status, message} }
func sendExamError(c *gin.Context, err error) {
	var apiErr examError
	if errors.As(err, &apiErr) {
		c.JSON(apiErr.status, gin.H{"error": apiErr.message})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "Data tidak ditemukan atau tidak tersedia untuk Anda."})
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		c.JSON(409, gin.H{"error": "Data duplikat. Periksa email, identitas, atau nama yang sudah digunakan."})
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "foreign key constraint") {
		c.JSON(409, gin.H{"error": "Data masih digunakan oleh relasi lain. Periksa pengampu, wali kelas, dan konten terkait."})
		return
	}
	c.Error(err)
	c.JSON(500, gin.H{"error": "Gagal memproses permintaan. Silakan coba lagi."})
}
func examID(c *gin.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		return 0, failExam(400, "ID ujian tidak valid.")
	}
	return uint(id), nil
}
func studentExam(tx *gorm.DB, c *gin.Context) (Exam, error) {
	var exam Exam
	id, err := examID(c)
	if err != nil {
		return exam, err
	}
	var student User
	if err = tx.First(&student, "id = ?", c.GetString("user_id")).Error; err != nil {
		return exam, err
	}
	if student.ClassID == nil {
		return exam, failExam(403, "Anda belum memiliki kelas.")
	}
	err = tx.Where("id = ? AND class_id = ?", id, *student.ClassID).First(&exam).Error
	return exam, err
}
func examAvailable(exam Exam, now time.Time) bool {
	// Date is a school calendar date (WIB), not a UTC instant.
	return exam.IsActive && exam.Duration > 0 && exam.Duration <= 1440 &&
		!exam.Date.IsZero() && now.In(time.FixedZone("WIB", 7*60*60)).Format("2006-01-02") >= exam.Date.Format("2006-01-02")
}
func attemptData(attempt ExamAttempt) ([]Question, map[uint]string, error) {
	var questions []Question
	answers := map[uint]string{}
	if err := json.Unmarshal([]byte(attempt.QuestionsJSON), &questions); err != nil {
		return nil, nil, err
	}
	if len(questions) == 0 {
		return nil, nil, fmt.Errorf("attempt %d has no questions", attempt.ID)
	}
	if err := json.Unmarshal([]byte(attempt.AnswersJSON), &answers); err != nil {
		return nil, nil, err
	}
	if answers == nil {
		answers = map[uint]string{}
	}
	return questions, answers, nil
}
func finishAttempt(tx *gorm.DB, attempt *ExamAttempt, now time.Time) (ExamResult, error) {
	var result ExamResult
	err := tx.Where("exam_id = ? AND student_id = ?", attempt.ExamID, attempt.StudentID).First(&result).Error
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	questions, answers, err := attemptData(*attempt)
	if err != nil {
		return result, err
	}
	correct := 0
	for _, q := range questions {
		if answers[q.ID] == q.CorrectAnswer {
			correct++
		}
	}
	result = ExamResult{ExamID: attempt.ExamID, StudentID: attempt.StudentID, Score: float64(correct) * 100 / float64(len(questions))}
	if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&result).Error; err != nil {
		return result, err
	}
	if err = tx.Where("exam_id = ? AND student_id = ?", attempt.ExamID, attempt.StudentID).First(&result).Error; err != nil {
		return result, err
	}
	if err = tx.Model(attempt).Update("submitted_at", now).Error; err != nil {
		return result, err
	}
	attempt.SubmittedAt = &now
	return result, nil
}

// Sweep also runs at startup: closing the browser does not leave an expired attempt open.
func finalizeExpiredAttempts(now time.Time) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var attempts []ExamAttempt
		if err := tx.Where("submitted_at IS NULL AND expires_at <= ?", now).Find(&attempts).Error; err != nil {
			return err
		}
		for i := range attempts {
			if _, err := finishAttempt(tx, &attempts[i], now); err != nil {
				return err
			}
		}
		return nil
	})
}

func StartStudentExam(c *gin.Context) {
	if !isSiswa(c) {
		return
	}
	var response gin.H
	err := DB.Transaction(func(tx *gorm.DB) error {
		exam, err := studentExam(tx, c)
		if err != nil {
			return err
		}
		userID := c.GetString("user_id")
		var result ExamResult
		err = tx.Where("exam_id = ? AND student_id = ?", exam.ID, userID).First(&result).Error
		if err == nil {
			response = gin.H{"exam": exam, "result": result}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var attempt ExamAttempt
		err = tx.Where("exam_id = ? AND student_id = ?", exam.ID, userID).First(&attempt).Error
		now := time.Now().UTC()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if !examAvailable(exam, now) {
				return failExam(403, "Ujian belum dibuka atau belum sesuai jadwal.")
			}
			var questions []Question
			if err = tx.Where("exam_id = ?", exam.ID).Order("id").Find(&questions).Error; err != nil {
				return err
			}
			if len(questions) == 0 {
				return failExam(409, "Ujian belum memiliki soal.")
			}
			for _, q := range questions {
				if !validQuestion(q) {
					return failExam(409, "Soal ujian belum lengkap. Hubungi guru.")
				}
			}
			snapshot, err := json.Marshal(questions)
			if err != nil {
				return err
			}
			attempt = ExamAttempt{ExamID: exam.ID, StudentID: userID, StartedAt: now, ExpiresAt: now.Add(time.Duration(exam.Duration) * time.Minute), QuestionsJSON: string(snapshot), AnswersJSON: "{}"}
			if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attempt).Error; err != nil {
				return err
			}
			if err = tx.Where("exam_id = ? AND student_id = ?", exam.ID, userID).First(&attempt).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if !now.Before(attempt.ExpiresAt) {
			result, err = finishAttempt(tx, &attempt, now)
			response = gin.H{"exam": exam, "result": result}
			return err
		}
		questions, answers, err := attemptData(attempt)
		if err != nil {
			return err
		}
		safeQuestions := make([]studentQuestion, 0, len(questions))
		for _, q := range questions {
			safeQuestions = append(safeQuestions, studentQuestion{q.ID, q.QuestionText, q.OptionA, q.OptionB, q.OptionC, q.OptionD})
		}
		response = gin.H{"exam": exam, "questions": safeQuestions, "answers": answers, "started_at": attempt.StartedAt, "expires_at": attempt.ExpiresAt, "server_time": time.Now().UTC()}
		return nil
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, response)
}

type examAnswersInput struct {
	Answers map[uint]string `json:"answers"`
}

func SaveStudentAnswers(c *gin.Context) { writeStudentAnswers(c, false) }
func SubmitStudentExam(c *gin.Context)  { writeStudentAnswers(c, true) }
func writeStudentAnswers(c *gin.Context, submit bool) {
	if !isSiswa(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
	var input examAnswersInput
	if err := c.ShouldBindJSON(&input); err != nil || input.Answers == nil {
		c.JSON(400, gin.H{"error": "Format jawaban tidak valid."})
		return
	}
	var response gin.H
	err := DB.Transaction(func(tx *gorm.DB) error {
		exam, err := studentExam(tx, c)
		if err != nil {
			return err
		}
		var result ExamResult
		err = tx.Where("exam_id = ? AND student_id = ?", exam.ID, c.GetString("user_id")).First(&result).Error
		if err == nil {
			response = gin.H{"result": result}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var attempt ExamAttempt
		if err = tx.Where("exam_id = ? AND student_id = ?", exam.ID, c.GetString("user_id")).First(&attempt).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return failExam(409, "Mulai ujian terlebih dahulu.")
			}
			return err
		}
		now := time.Now().UTC()
		if now.Before(attempt.ExpiresAt) {
			questions, answers, err := attemptData(attempt)
			if err != nil {
				return err
			}
			validIDs := map[uint]bool{}
			for _, q := range questions {
				validIDs[q.ID] = true
			}
			for id, answer := range input.Answers {
				if !validIDs[id] || (answer != "A" && answer != "B" && answer != "C" && answer != "D") {
					return failExam(400, "ID soal atau pilihan jawaban tidak valid.")
				}
				answers[id] = answer
			}
			encoded, err := json.Marshal(answers)
			if err != nil {
				return err
			}
			attempt.AnswersJSON = string(encoded)
			if err = tx.Model(&attempt).Update("answers_json", attempt.AnswersJSON).Error; err != nil {
				return err
			}
		}
		// Late payloads are ignored; only answers saved before the deadline are graded.
		if submit || !now.Before(attempt.ExpiresAt) {
			result, err = finishAttempt(tx, &attempt, now)
			response = gin.H{"result": result}
			return err
		}
		response = gin.H{"message": "Jawaban tersimpan.", "server_time": now}
		return nil
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, response)
}
