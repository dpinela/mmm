package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"

	"github.com/dpinela/mmm/internal/sqlite"
)

//go:embed static
var staticPages embed.FS

//go:embed templates
var templatePages embed.FS

var templates = template.Must(template.ParseFS(templatePages, "templates/*.html"))

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
	mux.HandleFunc("POST /shuffle", func(w http.ResponseWriter, req *http.Request) {
		shuffleRoom(w, req, db)
	})
	mux.HandleFunc("GET /rooms/{randoID}", func(w http.ResponseWriter, req *http.Request) {
		displayRoom(w, req, db)
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

func displayRoom(w http.ResponseWriter, req *http.Request, db *database) {
	rawRoomID := req.PathValue("randoID")
	randoID, err := strconv.ParseInt(rawRoomID, 10, 64)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	info, err := db.getRoomInfo(randoID)
	if err == errRoomNotExist {
		http.NotFound(w, req)
		return
	}
	if err := templates.ExecuteTemplate(w, "room.html", info); err != nil {
		log.Println(err)
	}
}

func shuffleRoom(w http.ResponseWriter, req *http.Request, db *database) {
	req.ParseForm()
	rawRoomID := req.FormValue("randoID")
	randoID, err := strconv.ParseInt(rawRoomID, 10, 64)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	if err := db.lockRoom(randoID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	worlds, err := db.getAttachedRandos(randoID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	result := mix(worlds)
	if err := db.saveShuffleResult(randoID, result); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	log.Println("rando generated for room", randoID)
	http.Redirect(w, req, fmt.Sprintf("/rooms/%d", randoID), http.StatusSeeOther)
}
