package api

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestUserOrderAPI(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	handler := NewHandler(db, t.TempDir())
	response := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "123456",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("initialize = %d, %s", response.Code, response.Body.String())
	}
	admin := response.Result().Cookies()[0]
	secondAdmin, secondID := registerAccount(t, db, handler, admin, "vip", "z-admin")
	if _, err := db.Exec(`UPDATE users SET role = 'admin' WHERE id = ?`, secondID); err != nil {
		t.Fatal(err)
	}
	vip, vipID := registerAccount(t, db, handler, admin, "vip", "member")
	user, userID := registerAccount(t, db, handler, admin, "carpool", "alice")
	subscriber, subscriberID := registerAccount(t, db, handler, admin, "subscriber", "subscriber")
	const path, key = "/api/users", "users"
	expectOrderAPI(t, handler, admin, path, key, 1, userID, vipID, subscriberID, secondID)
	// The administrator's own row participates in the same adjacent moves.
	moveOrderAPI(t, handler, admin, "users", 1, "down", http.StatusNoContent)
	expectOrderAPI(t, handler, admin, path, key, userID, 1, vipID, subscriberID, secondID)
	expectOrderAPI(t, handler, secondAdmin, path, key, 1, userID, vipID, subscriberID, secondID)
	moveOrderAPI(t, handler, secondAdmin, "users", secondID, "up", http.StatusNoContent)
	expectOrderAPI(t, handler, secondAdmin, path, key, 1, userID, vipID, secondID, subscriberID)
	for _, cookie := range []*http.Cookie{vip, user, subscriber, nil} {
		status := http.StatusForbidden
		if cookie == nil {
			status = http.StatusUnauthorized
		}
		moveOrderAPI(t, handler, cookie, "users", 1, "down", status)
	}
	moveOrderAPI(t, handler, admin, "users", 1, "sideways", http.StatusBadRequest)
	moveOrderAPI(t, handler, admin, "users", 0, "up", http.StatusBadRequest)
	moveOrderAPI(t, handler, admin, "users", 9999, "up", http.StatusNotFound)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	handler = NewHandler(db, t.TempDir())
	expectOrderAPI(t, handler, admin, path, key, userID, 1, vipID, subscriberID, secondID)
	expectOrderAPI(t, handler, secondAdmin, path, key, 1, userID, vipID, secondID, subscriberID)
	_, newID := registerAccount(t, db, handler, admin, "vip", "zzz-new")
	// New resources precede stored positions, as in every existing listorder kind.
	expectOrderAPI(t, handler, admin, path, key, newID, userID, 1, vipID, subscriberID, secondID)
	deleted := performRequest(t, handler, http.MethodDelete, "/api/admin/users/"+strconv.FormatInt(vipID, 10), nil, admin)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, %s", deleted.Code, deleted.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_account_order WHERE account_user_id = ?`, vipID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted account order rows = %d, %v", count, err)
	}
	expectOrderAPI(t, handler, admin, path, key, newID, userID, 1, subscriberID, secondID)
}
