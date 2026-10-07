package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
)

const sessionHeader = "X-Desktool-Session"

// RegisterHTTPHandlers 注册客户端只访问 proxy 的 HTTP API。
func RegisterHTTPHandlers(mux *http.ServeMux, service *Service) {
	mux.HandleFunc("/api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		result, err := service.Bootstrap()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := service.Login(req)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/api/workspace", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		result, err := service.Workspace(r.Header.Get(sessionHeader))
		if errors.Is(err, ErrSessionMissing) {
			w.WriteHeader(http.StatusUnauthorized)
		} else if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		result, err := service.Logout(r.Header.Get(sessionHeader))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}

// SessionHeader 返回 proxy HTTP API 使用的内部 session header。
func SessionHeader() string {
	return sessionHeader
}
