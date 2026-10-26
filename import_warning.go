package main

import (
	"regexp"
	"strings"
)

var legacyKeyLine = regexp.MustCompile(`^[A-Ea-e]\s*[-\x{2013}:]\s+`)

func importWarning(questions []Question) string {
	mcq, keys := 0, 0
	for _, q := range questions {
		if q.Kind != "essay" {
			mcq++
		} else if legacyKeyLine.MatchString(strings.TrimSpace(q.QuestionText)) {
			keys++
		}
	}
	if mcq >= 2 && keys >= 2 {
		return "Ujian ini diduga mengandung baris kunci/pembahasan yang terimpor sebagai esai. Soal dan hasil lama dipertahankan; buat ujian pengganti dengan impor PDF yang diperbaiki."
	}
	return ""
}
func pureEssay(q Question) bool {
	return q.Kind == "essay" && q.OptionA == "" && q.OptionB == "" && q.OptionC == "" && q.OptionD == "" && q.OptionE == ""
}
