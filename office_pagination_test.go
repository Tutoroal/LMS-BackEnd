package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func wordWithBody(t *testing.T, body string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write([]byte("<w:document xmlns:w=\"http://schemas.openxmlformats.org/wordprocessingml/2006/main\"><w:body>" + body + "</w:body></w:document>"))
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestWordPreviewPagination(t *testing.T) {
	for _, marker := range []string{
		"<w:p><w:r><w:br w:type=\"page\"/></w:r></w:p>",
		"<w:p><w:pPr><w:pageBreakBefore/></w:pPr></w:p>",
		"<w:p><w:r><w:lastRenderedPageBreak/></w:r></w:p>",
	} {
		blocks, err := docxBlocks(wordWithBody(t, "<w:p><w:r><w:t>First</w:t></w:r></w:p>"+marker+"<w:p><w:r><w:t>Second</w:t></w:r></w:p>"))
		if err != nil {
			t.Fatal(err)
		}
		pages := docxPreviewPages(blocks)
		if len(pages) != 2 || pages[0][0].Text != "First" || pages[1][0].Text != "Second" {
			t.Fatalf("missing page break: %#v", pages)
		}
	}
	var body strings.Builder
	for i := 1; i <= 45; i++ {
		fmt.Fprintf(&body, "<w:p><w:r><w:t>Paragraph %d</w:t></w:r></w:p>", i)
	}
	blocks, err := docxBlocks(wordWithBody(t, body.String()))
	if err != nil {
		t.Fatal(err)
	}
	pages := docxPreviewPages(blocks)
	if len(pages) != 3 || len(pages[0]) != 20 || pages[1][0].Text != "Paragraph 21" || pages[2][4].Text != "Paragraph 45" {
		t.Fatalf("content lost in pagination: %#v", pages)
	}
}

func TestStudentWordPagesAndBounds(t *testing.T) {
	t.Setenv("S3_BUCKET", "")
	t.Setenv("UPLOAD_DIR", t.TempDir())
	r := setupCBT(t)
	teacher, student := tokenFor(t, "teacher", 2), tokenFor(t, "student", 3)
	body := "<w:p><w:r><w:t>First page</w:t><w:br w:type=\"page\"/><w:t>Second page</w:t></w:r></w:p>"
	uploaded := uploadMaterialFile(t, r, teacher, "pages.docx", wordWithBody(t, body))
	expectStatus(t, uploaded, 201)
	var asset struct{ Data MediaAsset }
	if err := json.Unmarshal(uploaded.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, requestCBT(r, teacher, "POST", "/api/teacher/materials", fmt.Sprintf(`{"title":"Pages","subject_id":1,"class_id":1,"asset_id":%d}`, asset.Data.ID)), 200)
	for page := 1; page <= 2; page++ {
		response := requestCBT(r, student, "GET", fmt.Sprintf("/api/uploads/%d/preview?page=%d", asset.Data.ID, page), "")
		expectStatus(t, response, 200)
		var result struct {
			Blocks []officeBlock
			Page   int
			Total  int  `json:"total_pages"`
			Next   bool `json:"has_next"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		expected := []string{"First page", "Second page"}[page-1]
		if result.Page != page || result.Total != 2 || result.Next != (page == 1) || len(result.Blocks) != 1 || result.Blocks[0].Text != expected {
			t.Fatalf("bad page %s", response.Body.String())
		}
	}
	expectStatus(t, requestCBT(r, student, "GET", fmt.Sprintf("/api/uploads/%d/preview?page=3", asset.Data.ID), ""), 400)
}
