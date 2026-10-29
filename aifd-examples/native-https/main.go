// Command native-https demonstrates one native HTTP/HTTPS/WSS endpoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	log "github.com/li9chuan/aifirstdev/aifd-observe/logging"
	"github.com/li9chuan/aifirstdev/aifd-proxy/adapters/httpserver"
	"github.com/li9chuan/aifirstdev/aifd-runtime/entrypoint"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19084", "HTTP listener address; public ACME validation must reach port 443")
	mode := flag.String("mode", "plain", "plain, manual or acme")
	domains := flag.String("domains", "", "comma-separated explicit DNS names for TLS")
	cert := flag.String("cert", "", "manual full-chain PEM file")
	key := flag.String("key", "", "manual private-key PEM file")
	cache := flag.String("cache-dir", "", "persistent private ACME directory outside application releases")
	environment := flag.String("acme-environment", "", "staging or production; empty uses production")
	agree := flag.Bool("agree-terms", false, "deployment operator accepts the ACME CA subscriber agreement")
	flag.Parse()
	closeLog, err := log.InitDefault(log.Config{SourceService: "native-https", AlsoStdout: true})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer closeLog()
	tlsCfg := httpserver.TLSConfig{Mode: httpserver.Mode(*mode), CertFile: *cert, KeyFile: *key,
		CacheDir: *cache, ACMEEnvironment: *environment, AgreeTerms: *agree}
	if *domains != "" {
		tlsCfg.Domains = strings.Split(*domains, ",")
	}
	ctx, cancel := entrypoint.SignalContext(context.Background())
	defer cancel()
	var endpoint atomic.Pointer[httpserver.Server]
	var mu sync.Mutex
	peers := make(map[*websocket.Conn]struct{})
	closing := false
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if server := endpoint.Load(); server == nil || !server.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "ready")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "AIFD native HTTP/HTTPS; WebSocket echo at /ws")
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		if closing {
			mu.Unlock()
			_ = conn.CloseNow()
			return
		}
		peers[conn] = struct{}{}
		mu.Unlock()
		defer func() {
			mu.Lock()
			delete(peers, conn)
			mu.Unlock()
			_ = conn.CloseNow()
		}()
		for {
			kind, payload, err := conn.Read(r.Context())
			if err != nil || conn.Write(r.Context(), kind, payload) != nil {
				return
			}
		}
	})
	base := entrypoint.NewHTTPServer(mux)
	base.Addr = *addr
	server, err := httpserver.Start(ctx, httpserver.Config{HTTPServer: base, TLS: tlsCfg,
		CloseWebSockets: func(context.Context) error {
			mu.Lock()
			closing = true
			connections := make([]*websocket.Conn, 0, len(peers))
			for conn := range peers {
				connections = append(connections, conn)
			}
			mu.Unlock()
			for _, conn := range connections {
				_ = conn.CloseNow()
			}
			return nil
		}})
	if err != nil {
		log.Error("native transport startup failed", log.Err(err))
		os.Exit(1)
	}
	endpoint.Store(server)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := ""
	for {
		status := server.Status()
		if status.State+status.LastErrorCode != previous {
			log.Info("native transport status", log.String("state", status.State), log.Bool("ready", status.Ready), log.String("error_code", status.LastErrorCode))
			previous = status.State + status.LastErrorCode
		}
		select {
		case <-server.Done():
			if err := server.Err(); err != nil {
				log.Error("native transport stopped with an error", log.Err(err))
				os.Exit(1)
			}
			return
		case <-ticker.C:
		}
	}
}
