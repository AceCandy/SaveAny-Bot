// Package logfile provides daily application log files for the web console.
package logfile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	Directory  = "data/logs"
	dateLayout = "2006-01-02"
	tailChunk  = 64 * 1024
)

var ErrInvalidDate = errors.New("invalid log date")
var ErrInvalidLevel = errors.New("invalid log level")

var levelRanks = map[string]int{
	"debug": 0,
	"info":  1,
	"warn":  2,
	"error": 3,
	"fatal": 4,
}

var outputLevelRanks = map[string]int{
	"DEBU": 0,
	"INFO": 1,
	"WARN": 2,
	"ERRO": 3,
	"FATA": 4,
}

// Writer writes logs to one file per local calendar day.
type Writer struct {
	mu   sync.Mutex
	dir  string
	now  func() time.Time
	date string
	file *os.File
}

func NewWriter(dir string) (*Writer, error) {
	return newWriter(dir, time.Now)
}

func newWriter(dir string, now func() time.Time) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w := &Writer{dir: dir, now: now}
	if err := w.rotate(now().Format(dateLayout)); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if date := w.now().Format(dateLayout); date != w.date {
		if err := w.rotate(date); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *Writer) rotate(date string) error {
	next, err := os.OpenFile(filepath.Join(w.dir, date+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			w.file = nil
			return errors.Join(err, next.Close())
		}
	}
	w.file = next
	w.date = date
	return nil
}

// Dates returns available log dates newest first.
func Dates(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	dates := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		date := strings.TrimSuffix(entry.Name(), ".log")
		if parsed, err := time.Parse(dateLayout, date); err == nil && parsed.Format(dateLayout) == date {
			dates = append(dates, date)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates, nil
}

// Read returns the last limit lines for date, newest first.
func Read(dir, date string, limit int) ([]string, error) {
	return ReadLevel(dir, date, limit, "debug")
}

// ReadLevel returns the last limit lines at or above minLevel, newest first.
func ReadLevel(dir, date string, limit int, minLevel string) ([]string, error) {
	parsed, err := time.Parse(dateLayout, date)
	if err != nil || parsed.Format(dateLayout) != date {
		return nil, ErrInvalidDate
	}
	minRank, ok := levelRanks[strings.ToLower(minLevel)]
	if !ok {
		return nil, ErrInvalidLevel
	}
	file, err := os.Open(filepath.Join(dir, date+".log"))
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readTail(file, limit, minRank)
}

func readTail(file *os.File, limit, minRank int) ([]string, error) {
	if limit < 1 {
		return []string{}, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	offset := info.Size()
	var carry []byte
	lines := make([]string, 0, limit)
	firstChunk := true
	for offset > 0 && len(lines) < limit {
		size := min(int64(tailChunk), offset)
		offset -= size
		chunk := make([]byte, size)
		n, err := file.ReadAt(chunk, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		data := make([]byte, 0, n+len(carry))
		data = append(data, chunk[:n]...)
		data = append(data, carry...)
		parts := bytes.Split(data, []byte{'\n'})
		end := len(parts)
		if firstChunk && end > 0 && len(parts[end-1]) == 0 {
			end--
		}
		firstChunk = false
		start := 0
		if offset > 0 {
			carry = bytes.Clone(parts[0])
			start = 1
		}
		for i := end - 1; i >= start && len(lines) < limit; i-- {
			line := strings.TrimSuffix(string(parts[i]), "\r")
			if minRank == 0 || lineRank(line) >= minRank {
				lines = append(lines, line)
			}
		}
	}
	return lines, nil
}

func lineRank(line string) int {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return -1
	}
	return outputLevelRanks[fields[1]]
}
