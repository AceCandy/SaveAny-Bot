package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLogsHandlerReturnsNewestLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "2026-08-26.log"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-08-27.log"), []byte("00:00:00 DEBU first\n00:00:01 INFO second\n00:00:02 ERRO third\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := logDirectory
	logDirectory = dir
	t.Cleanup(func() { logDirectory = previous })

	recorder := httptest.NewRecorder()
	logsHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logs?date=2026-08-27&level=info&limit=2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response logsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026-08-27", "2026-08-26"}; !slices.Equal(response.Dates, want) {
		t.Fatalf("dates = %v, want %v", response.Dates, want)
	}
	if want := []string{"00:00:02 ERRO third", "00:00:01 INFO second"}; !slices.Equal(response.Lines, want) {
		t.Fatalf("lines = %v, want %v", response.Lines, want)
	}
}

func TestLogsHandlerRejectsInvalidDate(t *testing.T) {
	previous := logDirectory
	logDirectory = t.TempDir()
	t.Cleanup(func() { logDirectory = previous })
	recorder := httptest.NewRecorder()
	logsHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logs?date=../../config", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestLogsHandlerRejectsInvalidLevel(t *testing.T) {
	previous := logDirectory
	logDirectory = t.TempDir()
	t.Cleanup(func() { logDirectory = previous })
	recorder := httptest.NewRecorder()
	logsHandler(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/logs?level=trace", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
