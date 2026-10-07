package main

import (
	"log"
	"net/http"
	"os"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/account"
	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business"
	deskproxy "github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/proxy"
)

func main() {
	accountURL := getenv("DESKTOOL_ACCOUNT_URL", "http://127.0.0.1:19081")
	businessURL := getenv("DESKTOOL_BUSINESS_URL", "http://127.0.0.1:19083")
	service := deskproxy.NewService(account.NewHTTPClient(accountURL), business.NewHTTPClient(businessURL))
	mux := http.NewServeMux()
	deskproxy.RegisterHTTPHandlers(mux, service)
	log.Println("desktool proxy service listening on 127.0.0.1:19082")
	log.Fatal(http.ListenAndServe("127.0.0.1:19082", mux))
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
