package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxMediaSize int64 = 20 * 1024 * 1024

type MediaAsset struct {
	Purpose      string
	SubjectID    uint
	ClassID      uint
	ID           uint   `gorm:"primaryKey"`
	OwnerID      string `gorm:"index"`
	Owner        User   `gorm:"constraint:OnDelete:CASCADE;" json:"-"`
	AssignmentID uint   `gorm:"index"`
	ExamID       uint   `gorm:"index"`
	Name         string
	ContentType  string
	Size         int64
	ObjectKey    string `json:"-"`
	Storage      string `json:"-"`
	Ready        bool
	CreatedAt    time.Time
}

func mediaRoot() string {
	root := os.Getenv("UPLOAD_DIR")
	if root == "" {
		root = "uploads"
	}
	return root
}
func mediaS3() (*s3.Client, string, error) {
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		return nil, "", failExam(503, "Penyimpanan cloud belum dikonfigurasi.")
	}
	if os.Getenv("S3_ACCESS_KEY_ID") == "" || os.Getenv("S3_SECRET_ACCESS_KEY") == "" {
		return nil, "", errors.New("incomplete S3 credentials")
	}
	region := os.Getenv("S3_REGION")
	if region == "" {
		region = "auto"
	}
	config := aws.Config{Region: region, Credentials: credentials.NewStaticCredentialsProvider(os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY"), "")}
	client := s3.NewFromConfig(config, func(o *s3.Options) {
		if endpoint := os.Getenv("S3_ENDPOINT"); endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	return client, bucket, nil
}
func mediaFormat(contentType string) string {
	switch contentType {
	case "application/pdf":
		return "pdf"
	case "image/jpeg", "image/png", "image/webp":
		return "image"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "document"
	}
	return ""
}
func authorizeUpload(c *gin.Context, assignmentID, examID uint, contentType string) error {
	role := c.GetFloat64("role_id")
	if role == 3 && assignmentID > 0 && examID == 0 {
		student, err := studentAccount(DB, c)
		if err != nil {
			return err
		}
		var assignment Assignment
		if student.ClassID == nil {
			return failExam(403, "Anda belum memiliki kelas.")
		}
		if err := DB.Where("id=? AND class_id=?", assignmentID, *student.ClassID).First(&assignment).Error; err != nil {
			return err
		}
		if !time.Now().Before(assignment.Deadline) {
			return failExam(409, "Pengumpulan sudah ditutup.")
		}
		var n int64
		if err := DB.Model(&Submission{}).Where("assignment_id=? AND student_id=? AND graded_at IS NOT NULL", assignmentID, student.ID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return failExam(409, "Jawaban sudah dinilai.")
		}
		format := mediaFormat(contentType)
		if format == "" || !strings.Contains(","+assignment.AnswerFormats+",", ","+format+",") {
			return failExam(400, "Format berkas tidak diizinkan guru.")
		}
		return nil
	}
	if (role == 1 || role == 2) && examID > 0 && assignmentID == 0 && mediaFormat(contentType) == "image" {
		return editableExam(DB, c, examID)
	}
	return failExam(403, "Unggahan tidak tersedia untuk Anda.")
}
func parseMediaInput(c *gin.Context) (MediaAsset, error) {
	var input struct {
		Name         string `json:"name"`
		ContentType  string `json:"content_type"`
		Size         int64  `json:"size"`
		AssignmentID uint   `json:"assignment_id"`
		ExamID       uint   `json:"exam_id"`
		Purpose      string `json:"purpose"`
		SubjectID    uint   `json:"subject_id"`
		ClassID      uint   `json:"class_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		return MediaAsset{}, failExam(400, "Data unggahan tidak valid.")
	}
	if !validText(input.Name, 180) || input.Size <= 0 || input.Size > maxMediaSize || mediaFormat(input.ContentType) == "" {
		return MediaAsset{}, failExam(400, "Unggah PDF, JPG, PNG, WebP, DOCX, atau XLSX maksimal 20 MB.")
	}
	if err := authorizeMediaTarget(c, input.Purpose, input.SubjectID, input.ClassID, input.AssignmentID, input.ExamID, input.ContentType); err != nil {
		return MediaAsset{}, err
	}
	return MediaAsset{Purpose: input.Purpose, SubjectID: input.SubjectID, ClassID: input.ClassID, OwnerID: c.GetString("user_id"), Name: filepath.Base(strings.ReplaceAll(input.Name, "\\", "/")), ContentType: input.ContentType, Size: input.Size, AssignmentID: input.AssignmentID, ExamID: input.ExamID, ObjectKey: "staging/" + uuid.NewString(), Storage: "s3"}, nil
}
func InitMedia(c *gin.Context) {
	asset, err := parseMediaInput(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if os.Getenv("S3_BUCKET") == "" {
		c.JSON(200, gin.H{"mode": "local"})
		return
	}
	client, bucket, err := mediaS3()
	if err != nil {
		sendExamError(c, err)
		return
	}
	signed, err := s3.NewPresignClient(client).PresignPutObject(c.Request.Context(), &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(asset.ObjectKey), ContentType: aws.String(asset.ContentType), ContentLength: aws.Int64(asset.Size)}, func(o *s3.PresignOptions) { o.Expires = 5 * time.Minute })
	if err != nil {
		sendExamError(c, err)
		return
	}
	if err := DB.Create(&asset).Error; err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"mode": "s3", "asset": asset, "url": signed.URL, "headers": signed.SignedHeader})
}
func checkedMedia(data []byte, declared string) bool {
	detected := http.DetectContentType(data)
	if strings.HasPrefix(declared, "image/") || declared == "application/pdf" {
		return detected == declared
	}
	if mediaFormat(declared) != "document" {
		return false
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) > 2000 {
		return false
	}
	total := uint64(0)
	found := false
	for _, entry := range reader.File {
		if entry.UncompressedSize64 > 100*1024*1024-total {
			return false
		}
		total += entry.UncompressedSize64
		if strings.Contains(strings.ToLower(entry.Name), "vbaproject") {
			return false
		}
		if declared == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" && entry.Name == "word/document.xml" {
			found = true
		}
		if declared == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" && entry.Name == "xl/workbook.xml" {
			found = true
		}
	}
	return found
}
func CompleteMedia(c *gin.Context) {
	id, err := examID(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	var asset MediaAsset
	if err := DB.Where("id=? AND owner_id=?", id, c.GetString("user_id")).First(&asset).Error; err != nil {
		sendExamError(c, err)
		return
	}
	if asset.Ready {
		c.JSON(200, gin.H{"data": asset})
		return
	}
	if time.Since(asset.CreatedAt) > 15*time.Minute {
		c.JSON(409, gin.H{"error": "Unggahan kedaluwarsa. Unggah kembali."})
		return
	}
	if err := authorizeMediaTarget(c, asset.Purpose, asset.SubjectID, asset.ClassID, asset.AssignmentID, asset.ExamID, asset.ContentType); err != nil {
		sendExamError(c, err)
		return
	}
	client, bucket, err := mediaS3()
	if err != nil {
		sendExamError(c, err)
		return
	}
	object, err := client.GetObject(c.Request.Context(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(asset.ObjectKey)})
	if err != nil {
		sendExamError(c, err)
		return
	}
	defer object.Body.Close()
	data, err := io.ReadAll(io.LimitReader(object.Body, maxMediaSize+1))
	if err != nil || int64(len(data)) != asset.Size || !checkedMedia(data, asset.ContentType) {
		c.JSON(400, gin.H{"error": "Isi atau ukuran berkas tidak sesuai. Unggah kembali."})
		return
	}
	oldKey := asset.ObjectKey
	key := "objects/" + uuid.NewString()
	_, err = client.PutObject(c.Request.Context(), &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(data), ContentType: aws.String(asset.ContentType), ContentLength: aws.Int64(asset.Size)})
	if err != nil {
		sendExamError(c, err)
		return
	}
	if err := DB.Model(&asset).Updates(map[string]interface{}{"object_key": key, "ready": true}).Error; err != nil {
		sendExamError(c, err)
		return
	}
	client.DeleteObject(c.Request.Context(), &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(oldKey)})
	asset.ObjectKey = key
	asset.Ready = true
	c.JSON(200, gin.H{"data": asset})
}
func UploadLocalMedia(c *gin.Context) {
	if os.Getenv("S3_BUCKET") != "" {
		c.JSON(409, gin.H{"error": "Gunakan unggahan langsung ke cloud."})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMediaSize+1024*1024)
	if err := c.Request.ParseMultipartForm(1024 * 1024); err != nil {
		c.JSON(400, gin.H{"error": "Berkas maksimal 20 MB."})
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	header, err := c.FormFile("file")
	if err != nil || header.Size <= 0 || header.Size > maxMediaSize {
		c.JSON(400, gin.H{"error": "Pilih berkas maksimal 20 MB."})
		return
	}
	assignmentID, _ := strconv.ParseUint(c.PostForm("assignment_id"), 10, 32)
	examID, _ := strconv.ParseUint(c.PostForm("exam_id"), 10, 32)
	subjectID, _ := strconv.ParseUint(c.PostForm("subject_id"), 10, 32)
	classID, _ := strconv.ParseUint(c.PostForm("class_id"), 10, 32)
	purpose := c.PostForm("purpose")
	contentType := header.Header.Get("Content-Type")
	if mediaFormat(contentType) == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(header.Filename)))
	}
	if err := authorizeMediaTarget(c, purpose, uint(subjectID), uint(classID), uint(assignmentID), uint(examID), contentType); err != nil {
		sendExamError(c, err)
		return
	}
	file, err := header.Open()
	if err != nil {
		sendExamError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxMediaSize+1))
	if err != nil || int64(len(data)) > maxMediaSize || !checkedMedia(data, contentType) {
		c.JSON(400, gin.H{"error": "Isi berkas tidak sesuai format yang diizinkan."})
		return
	}
	if err := os.MkdirAll(mediaRoot(), 0700); err != nil {
		sendExamError(c, err)
		return
	}
	key := uuid.NewString()
	target := filepath.Join(mediaRoot(), key)
	if err := os.WriteFile(target, data, 0600); err != nil {
		sendExamError(c, err)
		return
	}
	asset := MediaAsset{Purpose: purpose, SubjectID: uint(subjectID), ClassID: uint(classID), OwnerID: c.GetString("user_id"), AssignmentID: uint(assignmentID), ExamID: uint(examID), Name: filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/")), ContentType: contentType, Size: int64(len(data)), ObjectKey: key, Storage: "local", Ready: true}
	if err := DB.Create(&asset).Error; err != nil {
		os.Remove(target)
		sendExamError(c, err)
		return
	}
	c.JSON(201, gin.H{"data": asset})
}
func readableMedia(c *gin.Context) (MediaAsset, error) {
	id, err := examID(c)
	if err != nil {
		return MediaAsset{}, err
	}
	var asset MediaAsset
	if err := DB.Where("id=? AND ready=true", id).First(&asset).Error; err != nil {
		return asset, err
	}
	role := c.GetFloat64("role_id")
	if asset.Purpose == "material" {
		if role == 1 || role == 4 || role == 5 || asset.OwnerID == c.GetString("user_id") {
			return asset, nil
		}
		query := DB.Model(&Material{}).Where("asset_id=?", asset.ID)
		if role == 3 {
			student, err := studentAccount(DB, c)
			if err != nil {
				return asset, err
			}
			if student.ClassID == nil {
				return asset, failExam(404, "Berkas tidak tersedia.")
			}
			query = query.Where("class_id=?", *student.ClassID)
		} else if role == 2 {
			query = query.Where("subject_id IN (SELECT id FROM subjects WHERE teacher_id=?)", c.GetString("user_id"))
		} else {
			return asset, failExam(404, "Berkas tidak tersedia.")
		}
		var n int64
		if err := query.Count(&n).Error; err != nil {
			return asset, err
		}
		if n > 0 {
			return asset, nil
		}
		return asset, failExam(404, "Berkas tidak tersedia untuk Anda.")
	}
	if asset.OwnerID == c.GetString("user_id") || role == 1 || role == 4 || role == 5 {
		return asset, nil
	}
	if role == 2 && asset.AssignmentID > 0 {
		var n int64
		err := DB.Table("submissions").Joins("JOIN assignments ON assignments.id=submissions.assignment_id").Joins("JOIN subjects ON subjects.id=assignments.subject_id").Where("assignments.id=? AND subjects.teacher_id=? AND EXISTS(SELECT 1 FROM json_each(submissions.attachments) WHERE json_each.value=?)", asset.AssignmentID, c.GetString("user_id"), asset.ID).Count(&n).Error
		if err != nil {
			return asset, err
		}
		if n > 0 {
			return asset, nil
		}
	}
	if role == 2 && asset.ExamID > 0 {
		if _, err := ownedExam(DB, c, asset.ExamID); err == nil {
			return asset, nil
		}
	}
	if role == 3 && asset.ExamID > 0 {
		student, err := studentAccount(DB, c)
		if err != nil {
			return asset, err
		}
		if student.ClassID != nil {
			var n int64
			err := DB.Table("questions").Joins("JOIN exams ON exams.id=questions.exam_id").Where("questions.image_asset_id=? AND exams.class_id=? AND (exams.is_active=true OR EXISTS(SELECT 1 FROM exam_attempts WHERE exam_attempts.exam_id=exams.id AND exam_attempts.student_id=?))", asset.ID, *student.ClassID, student.ID).Count(&n).Error
			if err != nil {
				return asset, err
			}
			if n > 0 {
				return asset, nil
			}
		}
	}
	return asset, failExam(404, "Berkas tidak tersedia untuk Anda.")
}
func AccessMedia(c *gin.Context) {
	asset, err := readableMedia(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if asset.Storage == "local" {
		c.JSON(200, gin.H{"data": asset, "path": fmt.Sprintf("/uploads/%d/content", asset.ID)})
		return
	}
	client, bucket, err := mediaS3()
	if err != nil {
		sendExamError(c, err)
		return
	}
	signed, err := s3.NewPresignClient(client).PresignGetObject(c.Request.Context(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(asset.ObjectKey), ResponseContentType: aws.String(asset.ContentType), ResponseContentDisposition: aws.String(mime.FormatMediaType("inline", map[string]string{"filename": asset.Name}))}, func(o *s3.PresignOptions) { o.Expires = 5 * time.Minute })
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": asset, "url": signed.URL})
}
func MediaContent(c *gin.Context) {
	asset, err := readableMedia(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if asset.Storage != "local" {
		c.JSON(400, gin.H{"error": "Gunakan akses cloud."})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Type", asset.ContentType)
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": asset.Name}))
	c.File(filepath.Join(mediaRoot(), asset.ObjectKey))
}

func authorizeMediaTarget(c *gin.Context, purpose string, subjectID, classID, assignmentID, examID uint, contentType string) error {
	if purpose == "material" {
		if assignmentID != 0 || examID != 0 || subjectID == 0 || classID == 0 || mediaFormat(contentType) == "" {
			return failExam(400, "Target materi tidak valid.")
		}
		if !hasRole(c, 1, 2) {
			return failExam(403, "Hanya guru/admin dapat mengunggah materi.")
		}
		return validateTarget(DB, c, subjectID, classID)
	}
	if purpose != "" || subjectID != 0 || classID != 0 {
		return failExam(400, "Tujuan unggahan tidak valid.")
	}
	return authorizeUpload(c, assignmentID, examID, contentType)
}
