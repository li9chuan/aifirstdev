package business

import (
	"encoding/json"
	"net/http"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/session"
)

// DataRequest 是 proxy 请求业务数据时携带的会话和提示信息。
type DataRequest struct {
	Session session.State `json:"session"`
	Message string        `json:"message"`
}

// RegisterHTTPHandlers 注册业务数据 HTTP API。
func RegisterHTTPHandlers(mux *http.ServeMux, service *Service) {
	mux.HandleFunc("/api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		req := DataRequest{}
		if r.Method == http.MethodPost && r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		_ = json.NewEncoder(w).Encode(service.Bootstrap(req.Session, req.Message))
	})
	mux.HandleFunc("/api/workspace", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req DataRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(service.Workspace(req.Session, req.Message))
	})
}
