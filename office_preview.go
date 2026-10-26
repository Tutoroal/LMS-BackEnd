package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type officeBlock struct {
	Kind string     `json:"kind"`
	Text string     `json:"text,omitempty"`
	Rows [][]string `json:"rows,omitempty"`
}

func mediaBytes(c *gin.Context, asset MediaAsset) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	if asset.Storage == "local" {
		reader, err = os.Open(filepath.Join(mediaRoot(), asset.ObjectKey))
	} else {
		client, bucket, e := mediaS3()
		if e != nil {
			return nil, e
		}
		object, e := client.GetObject(c.Request.Context(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(asset.ObjectKey)})
		if e != nil {
			return nil, e
		}
		reader = object.Body
	}
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxMediaSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxMediaSize || !checkedMedia(data, asset.ContentType) {
		return nil, failExam(400, "Berkas tidak valid untuk pratinjau.")
	}
	return data, nil
}
func docxBlocks(data []byte) ([]officeBlock, error) {
	book, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var entry *zip.File
	for _, f := range book.File {
		if f.Name == "word/document.xml" {
			entry = f
		}
	}
	if entry == nil || entry.UncompressedSize64 > 10*1024*1024 {
		return nil, failExam(400, "Isi Word terlalu besar atau tidak valid.")
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(io.LimitReader(reader, 10*1024*1024))
	blocks := []officeBlock{}
	paragraph, cell := "", ""
	row := []string{}
	table := [][]string{}
	tableDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, failExam(400, "Struktur Word tidak dapat dibaca.")
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl":
				tableDepth++
				if tableDepth == 1 {
					table = [][]string{}
				}
			case "tr":
				if tableDepth == 1 {
					row = []string{}
				}
			case "tc":
				if tableDepth == 1 {
					cell = ""
				}
			case "p":
				paragraph = ""
			case "t":
				var value string
				if err := decoder.DecodeElement(&value, &t); err != nil {
					return nil, err
				}
				paragraph += value
			case "tab":
				paragraph += "\t"
			case "pageBreakBefore", "lastRenderedPageBreak":
				if tableDepth == 0 {
					enabled := true
					for _, attr := range t.Attr {
						if attr.Name.Local == "val" && (attr.Value == "0" || attr.Value == "false" || attr.Value == "off") {
							enabled = false
						}
					}
					if enabled {
						if strings.TrimSpace(paragraph) != "" {
							blocks = append(blocks, officeBlock{Kind: "paragraph", Text: paragraph})
							paragraph = ""
						}
						blocks = append(blocks, officeBlock{Kind: "page_break"})
					}
				}
			case "br":
				pageBreak := false
				for _, attr := range t.Attr {
					if attr.Name.Local == "type" && attr.Value == "page" {
						pageBreak = true
					}
				}
				if pageBreak && tableDepth == 0 {
					if strings.TrimSpace(paragraph) != "" {
						blocks = append(blocks, officeBlock{Kind: "paragraph", Text: paragraph})
						paragraph = ""
					}
					blocks = append(blocks, officeBlock{Kind: "page_break"})
				} else {
					paragraph += "\n"
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				if tableDepth > 0 {
					cell += paragraph + "\n"
				} else if strings.TrimSpace(paragraph) != "" {
					blocks = append(blocks, officeBlock{Kind: "paragraph", Text: paragraph})
				}
			case "tc":
				if tableDepth == 1 {
					row = append(row, strings.TrimSpace(cell))
				}
			case "tr":
				if tableDepth == 1 {
					table = append(table, row)
				}
			case "tbl":
				tableDepth--
				if tableDepth == 0 {
					for start := 0; start < len(table); start += 20 {
						end := start + 20
						if end > len(table) {
							end = len(table)
						}
						blocks = append(blocks, officeBlock{Kind: "table", Rows: table[start:end]})
					}
				}
			}
		}
		if len(blocks) > 20000 {
			return nil, failExam(400, "Dokumen terlalu panjang untuk pratinjau.")
		}
	}
	return blocks, nil
}
func docxPreviewPages(blocks []officeBlock) [][]officeBlock {
	pages := [][]officeBlock{}
	current := []officeBlock{}
	weight := 0
	flush := func() {
		if len(current) > 0 {
			pages = append(pages, current)
			current = []officeBlock{}
			weight = 0
		}
	}
	for _, block := range blocks {
		if block.Kind == "page_break" {
			flush()
			continue
		}
		size := len([]rune(block.Text))
		for _, row := range block.Rows {
			size += 160
			for _, cell := range row {
				size += len([]rune(cell))
			}
		}
		if len(current) >= 20 || (len(current) > 0 && weight+size > 4000) {
			flush()
		}
		current = append(current, block)
		weight += size
	}
	flush()
	if len(pages) == 0 {
		pages = append(pages, []officeBlock{})
	}
	return pages
}

func PreviewOfficeMedia(c *gin.Context) {
	asset, err := readableMedia(c)
	if err != nil {
		sendExamError(c, err)
		return
	}
	if mediaFormat(asset.ContentType) != "document" {
		c.JSON(400, gin.H{"error": "Pratinjau isi tersedia untuk DOCX/XLSX."})
		return
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 || page > 1000 {
		c.JSON(400, gin.H{"error": "Halaman pratinjau tidak valid."})
		return
	}
	data, err := mediaBytes(c, asset)
	if err != nil {
		sendExamError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	if strings.Contains(asset.ContentType, "wordprocessingml") {
		blocks, err := docxBlocks(data)
		if err != nil {
			sendExamError(c, err)
			return
		}
		pages := docxPreviewPages(blocks)
		if page > len(pages) {
			c.JSON(400, gin.H{"error": "Halaman materi tidak tersedia."})
			return
		}
		c.JSON(200, gin.H{"kind": "docx", "blocks": pages[page-1], "page": page, "total_pages": len(pages), "has_next": page < len(pages)})
		return
	}
	book, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 100 * 1024 * 1024, UnzipXMLSizeLimit: 10 * 1024 * 1024})
	if err != nil {
		c.JSON(400, gin.H{"error": "Excel tidak dapat dibaca."})
		return
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) > 100 {
		c.JSON(400, gin.H{"error": "Maksimal 100 sheet untuk pratinjau."})
		return
	}
	sheet := c.Query("sheet")
	if sheet == "" && len(sheets) > 0 {
		sheet = sheets[0]
	}
	found := false
	for _, name := range sheets {
		if name == sheet {
			found = true
		}
	}
	if !found {
		c.JSON(400, gin.H{"error": "Sheet tidak tersedia."})
		return
	}
	iterator, err := book.Rows(sheet)
	if err != nil {
		sendExamError(c, err)
		return
	}
	defer iterator.Close()
	rows := [][]string{}
	start := (page - 1) * 200
	index := 0
	next := false
	for iterator.Next() {
		if index < start {
			index++
			continue
		}
		if len(rows) == 200 {
			next = true
			break
		}
		columns, err := iterator.Columns()
		if err != nil {
			sendExamError(c, err)
			return
		}
		if len(columns) > 500 {
			columns = columns[:500]
		}
		if columns == nil {
			columns = []string{}
		}
		rows = append(rows, columns)
		index++
	}
	if err := iterator.Error(); err != nil {
		sendExamError(c, err)
		return
	}
	c.JSON(200, gin.H{"kind": "xlsx", "sheets": sheets, "sheet": sheet, "rows": rows, "page": page, "start_row": start + 1, "has_next": next})
}
