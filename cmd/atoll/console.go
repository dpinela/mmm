package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"github.com/dpinela/mmm/internal/sqlite"
)

//go:embed static
var staticPages embed.FS

func serveConsole(cfg *serverConfig, db *database) {
	mux := http.NewServeMux()
	static, err := fs.Sub(staticPages, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServerFS(static))
	mux.HandleFunc("POST /create-room", func(w http.ResponseWriter, req *http.Request) {
		createRoom(w, req, db)
	})
	err = http.ListenAndServe(cfg.ListenAddress+":"+cfg.ConsolePort, mux)
	if err != nil {
		log.Println(err)
	}
}

func createRoom(w http.ResponseWriter, req *http.Request, db *database) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	name := req.PostForm.Get("name")
	id, err := db.createRoom(name)
	if err == sqlite.ErrConstraintUnique {
		http.Redirect(w, req, "/room-already-exists.html", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	log.Printf("created room %q with ID %d", name, id)
	http.Redirect(w, req, fmt.Sprintf("/rooms/%d", id), http.StatusSeeOther)
}
