package main

import (
	"bufio"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"os"
	"strings"
)

func loadEnvironment(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	allowed := map[string]bool{"JWT_SECRET": true, "DB_PATH": true, "PORT": true, "FRONTEND_ORIGIN": true, "AUTO_BACKUP": true, "BOOTSTRAP_ADMIN_NAME": true, "BOOTSTRAP_ADMIN_EMAIL": true, "BOOTSTRAP_ADMIN_PASSWORD": true}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "'\"")
		if found && allowed[key] {
			if _, present := os.LookupEnv(key); !present {
				if err := os.Setenv(key, value); err != nil {
					return err
				}
			}
		}
	}
	return scanner.Err()
}
func bootstrapAdministrator() error {
	input := UserInput{Name: os.Getenv("BOOTSTRAP_ADMIN_NAME"), Email: os.Getenv("BOOTSTRAP_ADMIN_EMAIL"), Password: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"), RoleID: 1}
	if err := normalizeUser(&input); err != nil {
		return err
	}
	if err := validatePassword(input.Password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&User{}).Where("role_id = 1").Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("administrator sudah tersedia; bootstrap tidak mengubah akun existing")
		}
		return tx.Create(&User{Name: input.Name, Email: input.Email, RoleID: 1, PasswordHash: string(hash)}).Error
	})
}
