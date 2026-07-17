package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4177", "listen address")
	flag.Parse()
	fs := http.FileServer(http.Dir("."))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/login", "/app":
			http.ServeFile(w, r, "index.html")
		default:
			if len(r.URL.Path) >= len("/project/") && r.URL.Path[:len("/project/")] == "/project/" {
				http.ServeFile(w, r, "index.html")
				return
			}
			fs.ServeHTTP(w, r)
		}
	})
	log.Printf("fixture tetris builder listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
