package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/common/logfile"
)

const (
	defaultLogLines = 500
	maxLogLines     = 2000
)

var logDirectory = logfile.Directory

type logsResponse struct {
	Dates []string `json:"dates"`
	Date  string   `json:"date"`
	Lines []string `json:"lines"`
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		MethodNotAllowedHandler(w, r)
		return
	}

	limit := defaultLogLines
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLogLines {
			WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 2000")
			return
		}
		limit = parsed
	}
	level := r.URL.Query().Get("level")
	if level == "" {
		level = "debug"
	}

	dates, err := logfile.Dates(logDirectory)
	if err != nil {
		log.FromContext(r.Context()).Error("Failed to list log files", "error", err)
		WriteError(w, http.StatusInternalServerError, "log_read_failed", "failed to read logs")
		return
	}
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
		if len(dates) > 0 {
			date = dates[0]
		}
	}
	lines, err := logfile.ReadLevel(logDirectory, date, limit, level)
	if errors.Is(err, logfile.ErrInvalidDate) {
		WriteError(w, http.StatusBadRequest, "invalid_request", "date must use YYYY-MM-DD")
		return
	}
	if errors.Is(err, logfile.ErrInvalidLevel) {
		WriteError(w, http.StatusBadRequest, "invalid_request", "level must be debug, info, warn, error, or fatal")
		return
	}
	if err != nil {
		log.FromContext(r.Context()).Error("Failed to read log file", "date", date, "error", err)
		WriteError(w, http.StatusInternalServerError, "log_read_failed", "failed to read logs")
		return
	}
	WriteJSON(w, http.StatusOK, logsResponse{Dates: dates, Date: date, Lines: lines})
}
