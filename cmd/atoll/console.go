package main

import (
	"embed"
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

func serveConsole(cfg *serverConfig, db *database, nf *notifier) {
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
		shuffleRoom(w, req, db, nf)
	})
	mux.HandleFunc("GET /rooms/{randoName}", func(w http.ResponseWriter, req *http.Request) {
		displayRoom(w, req, db)
	})
	err = http.ListenAndServe(cfg.ListenAddress+":"+cfg.ConsolePort, mux)
	if err != nil {
		log.Println(err)
	}
}

func createRoom(w http.ResponseWriter, req *http.Request, db *database) {
	const (
		nameEntropy = 10
		maxRetries  = 100
	)
	var (
		id   int64
		err  error
		name string
	)
	for range maxRetries {
		name = generateRoomName(nameEntropy)
		id, err = db.createRoom(name)
		if err == sqlite.ErrConstraintUnique {
			continue
		}
		if err == nil {
			break
		}
		http.Error(w, err.Error(), 500)
		return
	}
	// If this happens, we ran out of attempts.
	if err != nil {
		http.Redirect(w, req, "/too-many-rooms.html", http.StatusSeeOther)
		return
	}
	log.Printf("created room %q with ID %d", name, id)
	http.Redirect(w, req, "/rooms/"+name, http.StatusSeeOther)
}

func displayRoom(w http.ResponseWriter, req *http.Request, db *database) {
	roomName := req.PathValue("randoName")
	info, err := db.getRoomInfo(roomName)
	if err == errRoomNotExist {
		http.NotFound(w, req)
		return
	}
	if err := templates.ExecuteTemplate(w, "room.html", info); err != nil {
		log.Println(err)
	}
}

func shuffleRoom(w http.ResponseWriter, req *http.Request, db *database, nf *notifier) {
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
	nf.shuffleTopic.Notify(randoID)

	log.Println("rando generated for room", randoID)

	name, err := db.getRoomName(randoID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	http.Redirect(w, req, "/rooms/"+name, http.StatusSeeOther)
}
