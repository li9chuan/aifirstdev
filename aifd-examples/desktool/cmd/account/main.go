package main

import (
	"log"
	"net/http"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/account"
)

func main() {
	service := account.NewService()
	mux := http.NewServeMux()
	account.RegisterHTTPHandlers(mux, service)
	log.Println("desktool account service listening on 127.0.0.1:19081")
	log.Fatal(http.ListenAndServe("127.0.0.1:19081", mux))
}
