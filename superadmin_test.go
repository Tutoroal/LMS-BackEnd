package main

import (
	"strings"
	"testing"
)

func TestSuperAdminAndAdminAccountBoundaries(t *testing.T) {
	r := setupCBT(t)
	admin := tokenFor(t, "admin", 1)
	superadmin := tokenFor(t, "superadmin", 6)

	adminUsers := requestCBT(r, admin, "GET", "/api/admin/users", "")
	expectStatus(t, adminUsers, 200)
	if strings.Contains(adminUsers.Body.String(), "a@test.local") || strings.Contains(adminUsers.Body.String(), "root@test.local") || !strings.Contains(adminUsers.Body.String(), "t@test.local") {
		t.Fatal("admin user list is not scoped to academic roles")
	}

	superUsers := requestCBT(r, superadmin, "GET", "/api/admin/users", "")
	expectStatus(t, superUsers, 200)
	if !strings.Contains(superUsers.Body.String(), "a@test.local") || strings.Contains(superUsers.Body.String(), "t@test.local") || strings.Contains(superUsers.Body.String(), "root@test.local") {
		t.Fatal("super admin user list is not scoped to operational admins")
	}

	expectStatus(t, requestCBT(r, admin, "POST", "/api/admin/users", `{"name":"Admin Baru","email":"admin.baru@test.local","password":"Password-123","role_id":1}`), 403)
	expectStatus(t, requestCBT(r, superadmin, "POST", "/api/admin/users", `{"name":"Admin Baru","email":"admin.baru@test.local","password":"Password-123","role_id":1}`), 200)
	expectStatus(t, requestCBT(r, superadmin, "POST", "/api/admin/users", `{"name":"Guru Baru","email":"guru.baru@test.local","password":"Password-123","role_id":2}`), 403)
	expectStatus(t, requestCBT(r, superadmin, "POST", "/api/admin/classes", `{"name":"Kelas Terlarang","major":"PPLG"}`), 403)
}

func TestBootstrapCreatesFirstSuperAdmin(t *testing.T) {
	setupCBT(t)
	if err := DB.Where("role_id = ?", 6).Delete(&User{}).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOOTSTRAP_ADMIN_NAME", "Super Admin Bootstrap")
	t.Setenv("BOOTSTRAP_ADMIN_EMAIL", "bootstrap@test.local")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "Password-123")
	if err := bootstrapAdministrator(); err != nil {
		t.Fatal(err)
	}
	var user User
	if err := DB.Where("email = ? AND role_id = ?", "bootstrap@test.local", 6).First(&user).Error; err != nil {
		t.Fatal("super admin bootstrap was not persisted:", err)
	}
	if err := bootstrapAdministrator(); err == nil {
		t.Fatal("second super admin bootstrap should be rejected")
	}
}

func TestPasswordUpdateAllowsUnchangedLegacyIdentifier(t *testing.T) {
	r := setupCBT(t)
	if err := DB.Model(&User{}).Where("id IN ?", []string{"teacher", "other-teacher"}).Update("nisn_nip", "NIP-DUPLIKAT-LAMA").Error; err != nil {
		t.Fatal(err)
	}
	response := requestCBT(r, tokenFor(t, "admin", 1), "PUT", "/api/admin/users/teacher", `{"name":"Guru","email":"t@test.local","password":"Password-Baru-123","role_id":2,"nisn_nip":"NIP-DUPLIKAT-LAMA"}`)
	expectStatus(t, response, 200)
	var teacher User
	if err := DB.First(&teacher, "id = ?", "teacher").Error; err != nil {
		t.Fatal(err)
	}
	if teacher.SessionVersion != 1 || teacher.PasswordHash == "" {
		t.Fatal("password update was not persisted and old sessions were not revoked")
	}

	expectStatus(t, requestCBT(r, tokenFor(t, "admin", 1), "PUT", "/api/admin/users/teacher", `{"name":"Guru","email":"t@test.local","role_id":2,"nisn_nip":"NIP-BARU"}`), 200)
	expectStatus(t, requestCBT(r, tokenFor(t, "admin", 1), "PUT", "/api/admin/users/other-teacher", `{"name":"Guru lain","email":"ot@test.local","role_id":2,"nisn_nip":"NIP-BARU"}`), 409)
}
