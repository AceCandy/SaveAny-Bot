package logfile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDailyWriterAndRead(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 27, 23, 59, 0, 0, time.Local)
	writer, err := newWriter(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, err := writer.Write([]byte("first\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := writer.Write([]byte("third\n")); err != nil {
		t.Fatal(err)
	}

	dates, err := Dates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026-08-28", "2026-08-27"}; !slices.Equal(dates, want) {
		t.Fatalf("Dates() = %v, want %v", dates, want)
	}
	lines, err := Read(dir, "2026-08-27", 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"second", "first"}; !slices.Equal(lines, want) {
		t.Fatalf("Read() = %v, want %v", lines, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08-28.log")); err != nil {
		t.Fatalf("rotated log file: %v", err)
	}
}

func TestReadAcrossChunkBoundary(t *testing.T) {
	dir := t.TempDir()
	date := "2026-08-27"
	tail := strings.Repeat("x", tailChunk-len("older\nsecond\nthird\nfourth\n")) + "older\nsecond\nthird\nfourth\n"
	if err := os.WriteFile(filepath.Join(dir, date+".log"), []byte("discarded\n"+tail), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(dir, date, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"fourth", "third", "second"}; !slices.Equal(lines, want) {
		t.Fatalf("Read() = %v, want %v", lines, want)
	}
}

func TestReadLevelFindsLatestMatchingLines(t *testing.T) {
	dir := t.TempDir()
	date := "2026-08-27"
	noise := strings.Repeat("00:00:00 DEBU noise\n", tailChunk/20)
	content := "00:00:00 INFO first\n" + noise + "00:00:01 WARN second\n" + noise + "00:00:02 ERRO third\n00:00:03 FATA fourth\n"
	if err := os.WriteFile(filepath.Join(dir, date+".log"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadLevel(dir, date, 10, "info")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"00:00:03 FATA fourth", "00:00:02 ERRO third", "00:00:01 WARN second", "00:00:00 INFO first"}; !slices.Equal(lines, want) {
		t.Fatalf("ReadLevel() = %v, want %v", lines, want)
	}
}

func TestReadRejectsInvalidDate(t *testing.T) {
	if _, err := Read(t.TempDir(), "../../config", 10); err != ErrInvalidDate {
		t.Fatalf("Read() error = %v, want %v", err, ErrInvalidDate)
	}
}

func TestReadRejectsInvalidLevel(t *testing.T) {
	if _, err := ReadLevel(t.TempDir(), "2026-08-27", 10, "trace"); err != ErrInvalidLevel {
		t.Fatalf("ReadLevel() error = %v, want %v", err, ErrInvalidLevel)
	}
}
