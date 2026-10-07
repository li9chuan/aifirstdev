package main

import (
	"log"
	"net/http"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business"
)

func main() {
	service := business.NewService()
	mux := http.NewServeMux()
	business.RegisterHTTPHandlers(mux, service)
	log.Println("desktool business service listening on 127.0.0.1:19083")
	log.Fatal(http.ListenAndServe("127.0.0.1:19083", mux))
}
