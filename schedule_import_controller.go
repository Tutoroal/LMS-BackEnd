package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ScheduleImportClass struct {
	Name    string `json:"name"`
	Major   string `json:"major"`
	Program string `json:"program"`
	ClassID uint   `json:"class_id"`
	Create  bool   `json:"create"`
	Matches []uint `json:"matches,omitempty"`
}
type ScheduleImportSlot struct {
	ClassName   string `json:"class_name"`
	TeacherCode string `json:"teacher_code"`
	SubjectID   uint   `json:"subject_id"`
	Day         string `json:"day"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Cell        string `json:"cell"`
}
type ScheduleImportPreview struct {
	Year     string                `json:"year"`
	Sheet    string                `json:"sheet"`
	Classes  []ScheduleImportClass `json:"classes"`
	Slots    []ScheduleImportSlot  `json:"slots"`
	Warnings []string              `json:"warnings"`
}

var classHeaderPattern = regexp.MustCompile(`^(10|11|12|X|XI|XII)\s+`)
var teacherCodePattern = regexp.MustCompile(`^[0-9]{1,4}$`)
var timeRangePattern = regexp.MustCompile(`^\s*([0-9]{1,2}[.:][0-9]{2})\s*[-\x{2013}]\s*([0-9]{1,2}[.:][0-9]{2})\s*$`)
var yearPattern = regexp.MustCompile(`20[0-9]{2}\s*/\s*20[0-9]{2}`)

func scheduleClassKey(name string) string {
	parts := strings.Fields(strings.ToUpper(name))
	if len(parts) > 0 {
		switch parts[0] {
		case "X":
			parts[0] = "10"
		case "XI":
			parts[0] = "11"
		case "XII":
			parts[0] = "12"
		}
	}
	for i, p := range parts {
		if p == "TKJ" {
			parts[i] = "TJKT"
		}
		if p == "PM" {
			parts[i] = "BDR"
		}
	}
	return strings.Join(parts, " ")
}
func parseScheduleWorkbook(book *excelize.File) (ScheduleImportPreview, error) {
	out := ScheduleImportPreview{Classes: []ScheduleImportClass{}, Slots: []ScheduleImportSlot{}, Warnings: []string{}}
	for _, sheet := range book.GetSheetList() {
		rows, err := book.GetRows(sheet)
		if err != nil {
			return out, err
		}
		header := -1
		columns := map[int]ScheduleImportClass{}
		for i, row := range rows {
			if i > 30 {
				break
			}
			candidate := map[int]ScheduleImportClass{}
			for col, value := range row {
				name := scheduleClassKey(value)
				if !classHeaderPattern.MatchString(name) {
					continue
				}
				major := departmentFor(name)
				if major == "" {
					continue
				}
				program := "reguler"
				if strings.Contains(name, "PLUS") {
					program = "plus"
				}
				candidate[col] = ScheduleImportClass{Name: strings.Join(strings.Fields(value), " "), Major: major, Program: program}
			}
			if len(candidate) >= 2 {
				header = i
				columns = candidate
				break
			}
		}
		if header < 0 {
			continue
		}
		out.Sheet = sheet
		for _, row := range rows[:header+1] {
			for _, cell := range row {
				if match := yearPattern.FindString(cell); match != "" {
					out.Year = strings.ReplaceAll(match, " ", "")
				}
			}
		}
		order := []int{}
		for col := range columns {
			order = append(order, col)
		}
		sort.Ints(order)
		for _, col := range order {
			out.Classes = append(out.Classes, columns[col])
		}
		day := ""
		for index, row := range rows {
			if index <= header || len(row) < 3 {
				continue
			}
			label := strings.ToUpper(strings.Join(strings.Fields(row[0]), ""))
			switch label {
			case "SENIN":
				day = "Senin"
			case "SELASA":
				day = "Selasa"
			case "RABU":
				day = "Rabu"
			case "KAMIS":
				day = "Kamis"
			case "JUMAT", "JUM'AT":
				day = "Jumat"
			case "SABTU":
				day = "Sabtu"
			}
			timeMatch := timeRangePattern.FindStringSubmatch(row[2])
			if len(timeMatch) != 3 || day == "" {
				continue
			}
			start, end := normalizeScheduleClock(timeMatch[1]), normalizeScheduleClock(timeMatch[2])
			if !validClock(start) || !validClock(end) || start >= end {
				return out, fmt.Errorf("Jam pada %s!C%d tidak valid", sheet, index+1)
			}
			for _, col := range order {
				if col >= len(row) {
					continue
				}
				code := strings.TrimSpace(row[col])
				if code == "" {
					continue
				}
				cell, _ := excelize.CoordinatesToCellName(col+1, index+1)
				if !teacherCodePattern.MatchString(code) {
					out.Warnings = append(out.Warnings, fmt.Sprintf("%s!%s: kegiatan %q tidak diimpor sebagai pelajaran", sheet, cell, code))
					continue
				}
				out.Slots = append(out.Slots, ScheduleImportSlot{ClassName: columns[col].Name, TeacherCode: code, Day: day, Start: start, End: end, Cell: sheet + "!" + cell})
				if len(out.Slots) > 5000 {
					return out, fmt.Errorf("Maksimal 5000 slot pelajaran per berkas")
				}
			}
		}
		if len(out.Slots) == 0 {
			return out, fmt.Errorf("Tidak ada slot berisi kode guru yang terbaca")
		}
		out.Warnings = append(out.Warnings, "Baris kegiatan tanpa jam eksplisit tidak dimasukkan. Kode guru bukan nama mapel; periksa pemetaan sebelum menyimpan.")
		return out, nil
	}
	return out, fmt.Errorf("Sheet matriks jadwal belum dikenali. Gunakan kolom Hari, Jam ke, Pukul, lalu header kelas seperti contoh sekolah")
}
func PreviewScheduleImport(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 11*1024*1024)
	if err := c.Request.ParseMultipartForm(11 * 1024 * 1024); err != nil {
		c.JSON(400, gin.H{"error": "Unggah Excel .xlsx maksimal 10 MB."})
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "Pilih berkas Excel jadwal."})
		return
	}
	defer file.Close()
	if header.Size > 10*1024*1024 || !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		c.JSON(400, gin.H{"error": "Gunakan Excel .xlsx maksimal 10 MB."})
		return
	}
	book, err := excelize.OpenReader(file, excelize.Options{UnzipSizeLimit: 100 * 1024 * 1024, UnzipXMLSizeLimit: 10 * 1024 * 1024})
	if err != nil {
		c.JSON(400, gin.H{"error": "Excel tidak dapat dibaca."})
		return
	}
	defer book.Close()
	out, err := parseScheduleWorkbook(book)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	var classes []Class
	if err := DB.Find(&classes).Error; err != nil {
		sendExamError(c, err)
		return
	}
	for i := range out.Classes {
		for _, class := range classes {
			if scheduleClassKey(class.ClassName) == scheduleClassKey(out.Classes[i].Name) {
				out.Classes[i].Matches = append(out.Classes[i].Matches, class.ID)
			}
		}
		if len(out.Classes[i].Matches) == 1 {
			out.Classes[i].ClassID = out.Classes[i].Matches[0]
		}
	}
	c.JSON(200, gin.H{"data": out})
}
func CommitScheduleImport(c *gin.Context) {
	if !isAdmin(c) {
		return
	}
	var input struct {
		Year          string                `json:"year"`
		Semester      string                `json:"semester"`
		From          string                `json:"from"`
		Until         string                `json:"until"`
		AssignClasses bool                  `json:"assign_classes"`
		Classes       []ScheduleImportClass `json:"classes"`
		Slots         []ScheduleImportSlot  `json:"slots"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Slots) == 0 || len(input.Slots) > 5000 || len(input.Classes) == 0 || len(input.Classes) > 200 {
		c.JSON(400, gin.H{"error": "Periksa pratinjau kelas dan slot jadwal (maksimal 5000 slot)."})
		return
	}
	input.Year = strings.TrimSpace(input.Year)
	if !validText(input.Year, 50) || (input.Semester != "Ganjil" && input.Semester != "Genap") || !validScheduleDate(input.From) {
		c.JSON(400, gin.H{"error": "Lengkapi tahun ajaran, semester, dan tanggal mulai berlaku."})
		return
	}
	if input.Until == "" {
		input.Until = "9999-12-31"
	}
	if !validScheduleDate(input.Until) || input.Until < input.From {
		c.JSON(400, gin.H{"error": "Rentang tanggal berlaku tidak valid."})
		return
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existingClasses []Class
		if err := tx.Find(&existingClasses).Error; err != nil {
			return err
		}
		classIDs := map[string]uint{}
		for _, item := range input.Classes {
			key := scheduleClassKey(item.Name)
			if !validText(item.Name, 100) || classIDs[key] != 0 {
				return failExam(400, "Nama kelas kosong atau duplikat pada pratinjau.")
			}
			if item.Create {
				for _, old := range existingClasses {
					if scheduleClassKey(old.ClassName) == key {
						return failExam(409, "Kelas "+item.Name+" sudah tersedia; pilih kelas tersebut pada pratinjau.")
					}
				}
				if departmentFor(item.Major) != item.Major || (item.Program != "reguler" && item.Program != "plus") {
					return failExam(400, "Jurusan/program kelas tidak valid.")
				}
				row := Class{ClassName: strings.TrimSpace(item.Name), Major: item.Major, Program: item.Program}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				classIDs[key] = row.ID
			} else {
				found := false
				for _, old := range existingClasses {
					if old.ID == item.ClassID {
						found = true
						break
					}
				}
				if !found {
					return failExam(400, "Pilih kelas tujuan untuk "+item.Name+".")
				}
				classIDs[key] = item.ClassID
			}
		}
		ids := []uint{}
		seen := map[uint]bool{}
		for _, slot := range input.Slots {
			if slot.SubjectID == 0 {
				return failExam(400, "Mapel belum dipilih pada "+slot.Cell)
			}
			if !seen[slot.SubjectID] {
				seen[slot.SubjectID] = true
				ids = append(ids, slot.SubjectID)
			}
		}
		var subjects []Subject
		if err := tx.Preload("Teacher").Where("id IN ?", ids).Find(&subjects).Error; err != nil {
			return err
		}
		bySubject := map[uint]Subject{}
		for _, subject := range subjects {
			bySubject[subject.ID] = subject
		}
		var allocations []TeachingAllocation
		if err := tx.Where("subject_id IN ?", ids).Find(&allocations).Error; err != nil {
			return err
		}
		allowed := map[string]bool{}
		configured := map[uint]bool{}
		for _, a := range allocations {
			allowed[fmt.Sprintf("%d/%d", a.SubjectID, a.ClassID)] = true
			configured[a.SubjectID] = true
		}
		var old []Schedule
		if err := tx.Preload("Subject").Where("academic_year=? AND semester=? AND effective_from<=? AND effective_until>=?", input.Year, input.Semester, input.Until, input.From).Find(&old).Error; err != nil {
			return err
		}
		type interval struct{ start, end, label string }
		byClass := map[string][]interval{}
		byTeacher := map[string][]interval{}
		for _, row := range old {
			it := interval{row.StartTime, row.EndTime, "jadwal tersimpan #" + strconv.Itoa(int(row.ID))}
			byClass[fmt.Sprintf("%d/%s", row.ClassID, row.DayOfWeek)] = append(byClass[fmt.Sprintf("%d/%s", row.ClassID, row.DayOfWeek)], it)
			byTeacher[row.Subject.TeacherID+"/"+row.DayOfWeek] = append(byTeacher[row.Subject.TeacherID+"/"+row.DayOfWeek], it)
		}
		codeTeachers := map[string]string{}
		prepared := []Schedule{}
		pairs := map[string]TeachingAllocation{}
		for _, slot := range input.Slots {
			classID := classIDs[scheduleClassKey(slot.ClassName)]
			subject, ok := bySubject[slot.SubjectID]
			if classID == 0 || !ok || subject.Teacher.RoleID != 2 {
				return failExam(400, "Kelas/mapel/guru tidak valid pada "+slot.Cell)
			}
			if !teacherCodePattern.MatchString(slot.TeacherCode) {
				return failExam(400, "Kode guru tidak valid pada "+slot.Cell)
			}
			if teacher, ok := codeTeachers[slot.TeacherCode]; ok && teacher != subject.TeacherID {
				return failExam(400, "Kode "+slot.TeacherCode+" dipetakan ke dua guru berbeda; periksa "+slot.Cell)
			}
			codeTeachers[slot.TeacherCode] = subject.TeacherID
			slot.Start = normalizeScheduleClock(slot.Start)
			slot.End = normalizeScheduleClock(slot.End)
			validDay := false
			for _, day := range []string{"Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"} {
				if slot.Day == day {
					validDay = true
				}
			}
			if !validDay || !validClock(slot.Start) || !validClock(slot.End) || slot.Start >= slot.End {
				return failExam(400, "Hari/jam tidak valid pada "+slot.Cell)
			}
			pair := fmt.Sprintf("%d/%d", slot.SubjectID, classID)
			if !input.AssignClasses && (subject.AllocationConfigured || configured[subject.ID]) && !allowed[pair] {
				return failExam(400, "Mapel belum ditugaskan ke kelas pada "+slot.Cell+"; periksa pilihan pembaruan penugasan.")
			}
			pairs[pair] = TeachingAllocation{SubjectID: subject.ID, ClassID: classID}
			ck := fmt.Sprintf("%d/%s", classID, slot.Day)
			tk := subject.TeacherID + "/" + slot.Day
			for _, list := range [][]interval{byClass[ck], byTeacher[tk]} {
				for _, it := range list {
					if slot.Start < it.end && slot.End > it.start {
						return failExam(409, "Bentrok pada "+slot.Cell+" dengan "+it.label+"; tidak ada jadwal yang disimpan.")
					}
				}
			}
			it := interval{slot.Start, slot.End, slot.Cell}
			byClass[ck] = append(byClass[ck], it)
			byTeacher[tk] = append(byTeacher[tk], it)
			prepared = append(prepared, Schedule{AcademicYear: input.Year, Semester: input.Semester, EffectiveFrom: input.From, EffectiveUntil: input.Until, ClassID: classID, SubjectID: subject.ID, TeacherCode: slot.TeacherCode, DayOfWeek: slot.Day, StartTime: slot.Start, EndTime: slot.End})
		}
		if input.AssignClasses {
			for _, pair := range pairs {
				if err := tx.Where("subject_id=? AND class_id=?", pair.SubjectID, pair.ClassID).FirstOrCreate(&pair).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&Subject{}).Where("id IN ?", ids).Update("allocation_configured", true).Error; err != nil {
				return err
			}
		}
		return tx.CreateInBatches(&prepared, 100).Error
	})
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": fmt.Sprintf("%d slot jadwal tersimpan.", len(input.Slots)), "count": len(input.Slots)})
}
