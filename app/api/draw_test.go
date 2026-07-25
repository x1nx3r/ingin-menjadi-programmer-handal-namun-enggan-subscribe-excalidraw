package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gotth/app/lib"
	"gotth/app/middleware"
)

func setupTestDB(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	lib.InitDB(dbPath)

	return func() {
		lib.StopWAL()
		if lib.DB != nil {
			lib.DB.Close()
		}
	}
}

func mustCreateUserAndDrawing(t *testing.T, uid, drawingID string) {
	t.Helper()
	_, err := lib.DB.Exec(
		`INSERT INTO users (uid, email, name, storage_max_bytes, storage_used_bytes)
		 VALUES (?, ?, ?, ?, ?)`,
		uid, uid+"@example.com", "Test User", 100*1024*1024, 0,
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = lib.DB.Exec(
		`INSERT INTO drawings (id, owner_id, title, content, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		drawingID, uid, "Test Drawing", `{"elements":[],"appState":{}}`, time.Now(), time.Now(),
	)
	if err != nil {
		t.Fatalf("create drawing: %v", err)
	}
}

func withUser(ctx context.Context, uid string) context.Context {
	ctx = context.WithValue(ctx, middleware.UserUIDKey, uid)
	return context.WithValue(ctx, middleware.UserEmailKey, uid+"@example.com")
}

func TestSaveFileRejectsPathTraversal(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	uid := "user-1"
	drawingID := "drawing-1"
	mustCreateUserAndDrawing(t, uid, drawingID)

	payload := []byte("malicious content")
	req := httptest.NewRequest(http.MethodPost, "/api/draw/"+drawingID+"/file?fileId=../../../etc/passwd", nil)
	req.SetPathValue("id", drawingID)
	req.Body = io.NopCloser(strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "text/html") // blocked
	req = req.WithContext(withUser(req.Context(), uid))

	rr := httptest.NewRecorder()
	SaveFileHandler(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415 for disallowed MIME, got %d", rr.Code)
	}

	// Try with allowed MIME but traversal fileId.
	req = httptest.NewRequest(http.MethodPost, "/api/draw/"+drawingID+"/file?fileId=../../../etc/passwd", nil)
	req.SetPathValue("id", drawingID)
	req.Body = io.NopCloser(strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "image/png")
	req.ContentLength = int64(len(payload))
	req = req.WithContext(withUser(req.Context(), uid))

	rr = httptest.NewRecorder()
	SaveFileHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// The file must be stored under the drawing directory, not outside it.
	storageID := lib.GenerateStorageID(drawingID, "../../../etc/passwd")
	expectedPath := lib.StorageFilePath(drawingID, storageID)
	if expectedPath == "" {
		t.Fatal("expected valid storage path")
	}
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected file to exist at %s: %v", expectedPath, err)
	}

	// No file should be written outside the storage root.
	outsidePath := filepath.Join(filepath.Dir(lib.StorageRoot()), "etc", "passwd")
	if _, err := os.Stat(outsidePath); err == nil {
		t.Fatalf("path traversal succeeded: file exists at %s", outsidePath)
	}
}

func TestServeFileUsesStoredStorageID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	uid := "user-2"
	drawingID := "drawing-2"
	mustCreateUserAndDrawing(t, uid, drawingID)

	fileID := "my-image"
	storageID := lib.GenerateStorageID(drawingID, fileID)
	filePath := lib.StorageFilePath(drawingID, storageID)
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("image data"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := lib.DB.Exec(
		`INSERT INTO drawing_files (id, drawing_id, owner_id, storage_id, mime_type, file_size)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fileID, drawingID, uid, storageID, "image/png", int64(len("image data")),
	)
	if err != nil {
		t.Fatalf("insert file row: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/file/%s/%s", drawingID, fileID), nil)
	req.SetPathValue("drawingId", drawingID)
	req.SetPathValue("fileId", fileID)
	req = req.WithContext(withUser(req.Context(), uid))

	rr := httptest.NewRecorder()
	ServeFileHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("expected Content-Type image/png, got %q", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected X-Content-Type-Options nosniff, got %q", got)
	}
	if rr.Body.String() != "image data" {
		t.Errorf("unexpected body: %q", rr.Body.String())
	}
}

func TestServeFileBlocksUnsafeMIME(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	uid := "user-3"
	drawingID := "drawing-3"
	mustCreateUserAndDrawing(t, uid, drawingID)

	fileID := "evil"
	storageID := lib.GenerateStorageID(drawingID, fileID)
	filePath := lib.StorageFilePath(drawingID, storageID)
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("<script>alert(1)</script>"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := lib.DB.Exec(
		`INSERT INTO drawing_files (id, drawing_id, owner_id, storage_id, mime_type, file_size)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fileID, drawingID, uid, storageID, "text/html", int64(len("<script>alert(1)</script>")),
	)
	if err != nil {
		t.Fatalf("insert file row: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/file/%s/%s", drawingID, fileID), nil)
	req.SetPathValue("drawingId", drawingID)
	req.SetPathValue("fileId", fileID)
	req = req.WithContext(withUser(req.Context(), uid))

	rr := httptest.NewRecorder()
	ServeFileHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("expected safe Content-Type application/octet-stream, got %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != "attachment" {
		t.Errorf("expected attachment disposition, got %q", got)
	}
}

func TestDeleteFileUsesStoredStorageID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	uid := "user-4"
	drawingID := "drawing-4"
	mustCreateUserAndDrawing(t, uid, drawingID)

	fileID := "to-delete"
	storageID := lib.GenerateStorageID(drawingID, fileID)
	filePath := lib.StorageFilePath(drawingID, storageID)
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("data"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := lib.DB.Exec(
		`INSERT INTO drawing_files (id, drawing_id, owner_id, storage_id, mime_type, file_size)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		fileID, drawingID, uid, storageID, "image/png", int64(len("data")),
	)
	if err != nil {
		t.Fatalf("insert file row: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/draw/"+drawingID+"/file?fileId="+fileID, nil)
	req.SetPathValue("id", drawingID)
	req = req.WithContext(withUser(req.Context(), uid))

	rr := httptest.NewRecorder()
	DeleteFileHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filePath); err == nil {
		t.Fatal("expected file to be deleted")
	}
}
