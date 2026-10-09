package main

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"path/filepath"
	"testing"
)

func TestExistingDatabaseMigration(t *testing.T) {
	source := os.Getenv("LMS_MIGRATION_SOURCE")
	if source == "" {
		t.Skip("Set LMS_MIGRATION_SOURCE to rehearse migration on a read-only snapshot.")
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(absolute); err != nil {
		t.Fatal(err)
	}
	original, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(absolute)+"?mode=ro"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := original.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	snapshot := filepath.Join(t.TempDir(), "migration.db")
	if err = original.Exec("VACUUM INTO ?", snapshot).Error; err != nil {
		t.Fatal(err)
	}
	DB, err = gorm.Open(sqlite.Open(snapshot+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	copied.SetMaxOpenConns(1)
	defer copied.Close()
	tables := []string{"roles", "users", "classes", "subjects", "materials", "assignments", "submissions", "exams", "questions", "exam_results"}
	before := map[string]int64{}
	for _, table := range tables {
		if DB.Migrator().HasTable(table) {
			var count int64
			if err = DB.Table(table).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			before[table] = count
		}
	}
	var invalid []struct {
		ClassID string
		Count   int64
	}
	if err := DB.Raw("SELECT CAST(class_id AS TEXT) AS class_id, COUNT(*) AS count FROM users WHERE class_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM classes WHERE classes.id = users.class_id) GROUP BY class_id").Scan(&invalid).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("Invalid class references: %+v", invalid)
	if err = migrateDatabase(); err != nil {
		t.Fatalf("Migration of snapshot failed: %v", err)
	}
	for table, count := range before {
		var after int64
		if err = DB.Table(table).Count(&after).Error; err != nil {
			t.Fatal(err)
		}
		if count != after {
			t.Fatalf("%s: lost rows (%d -> %d)", table, count, after)
		}
	}
	rows, err := DB.Raw("PRAGMA foreign_key_check").Rows()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var problems int
	for rows.Next() {
		var table, parent string
		var rowid, key int64
		if err := rows.Scan(&table, &rowid, &parent, &key); err != nil {
			t.Fatal(err)
		}
		t.Logf("Orphan table=%s rowid=%d parent=%s key=%d", table, rowid, parent, key)
		problems++
	}
	if problems > 0 {
		t.Fatalf("%d orphan relationships", problems)
	}
	t.Log("Migration snapshot passed; all original row counts preserved and foreign keys valid.")
}
