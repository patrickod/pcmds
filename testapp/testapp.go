package main

import (
	"io"
	"log"
	"net"
	"net/http"
)

func main() {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	log.Printf("listening on %s", ln.Addr())

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Scheme == "https" {
			io.WriteString(w, "Hello, HTTPS!\n")
		} else {
			io.WriteString(w, "Hello, HTTP!\n")
		}
	})
	mux.HandleFunc("/", func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Scheme == "https" {
			io.WriteString(writer, "Hello, HTTPS!\n")
		} else {
			io.WriteString(writer, "Hello, HTTP!\n")
		}
	})

	if err := http.Serve(ln, mux); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
