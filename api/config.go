package api

import (
	"encoding/json"
	"net/http"

	"github.com/krau/SaveAny-Bot/config"
)

type configFileRequest struct {
	Content string `json:"content"`
}

func configFileHandler(restart func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			file, err := config.ReadManagedConfig()
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "config_read_failed", err.Error())
				return
			}
			WriteJSON(w, http.StatusOK, map[string]any{
				"content":   file.Content,
				"product":   config.ReadProductConfig(),
				"source":    file.Source,
				"read_only": file.ReadOnly,
			})
		case http.MethodPatch:
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			var req config.ProductConfig
			if err := decoder.Decode(&req); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid_request", "failed to decode request body: "+err.Error())
				return
			}
			if restart == nil {
				WriteError(w, http.StatusServiceUnavailable, "restart_unavailable", "restart is not available")
				return
			}
			if err := config.SaveProductConfig(req); err != nil {
				WriteError(w, http.StatusBadRequest, "config_save_failed", err.Error())
				return
			}
			if err := WriteJSON(w, http.StatusOK, map[string]string{"message": "config saved; restarting"}); err == nil {
				restart()
			}
		case http.MethodPut:
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			var req configFileRequest
			if err := decoder.Decode(&req); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid_request", "failed to decode request body: "+err.Error())
				return
			}
			if restart == nil {
				WriteError(w, http.StatusServiceUnavailable, "restart_unavailable", "restart is not available")
				return
			}
			if err := config.SaveManagedConfig(req.Content); err != nil {
				WriteError(w, http.StatusBadRequest, "config_save_failed", err.Error())
				return
			}
			if err := WriteJSON(w, http.StatusOK, map[string]string{"message": "config saved; restarting"}); err == nil {
				restart()
			}
		default:
			MethodNotAllowedHandler(w, r)
		}
	}
}
