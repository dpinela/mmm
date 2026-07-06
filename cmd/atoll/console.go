package main

import (
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"

	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/mwfile"
	"github.com/dpinela/mmm/internal/sqlite"
)

//go:embed static
var staticPages embed.FS

//go:embed templates
var templatePages embed.FS

var templates = template.Must(template.ParseFS(templatePages, "templates/*.html"))

func serveConsole(cfg *serverConfig, workdir string, nf *notifier) {
	mux := http.NewServeMux()
	static, err := fs.Sub(staticPages, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServerFS(static))
	mux.HandleFunc("POST /create-room", func(w http.ResponseWriter, req *http.Request) {
		createRoom(w, req, workdir)
	})
	mux.HandleFunc("POST /shuffle", func(w http.ResponseWriter, req *http.Request) {
		shuffleRoom(w, req, workdir, nf)
	})
	mux.HandleFunc("GET /rooms/{randoName}", func(w http.ResponseWriter, req *http.Request) {
		displayRoom(w, req, workdir)
	})
	mux.HandleFunc("POST /unjoin-player", func(w http.ResponseWriter, req *http.Request) {
		unjoinPlayer(w, req, workdir, nf)
	})
	err = http.ListenAndServe(cfg.ListenAddress+":"+cfg.ConsolePort, mux)
	if err != nil {
		log.Println(err)
	}
}

func createRoom(w http.ResponseWriter, req *http.Request, workdir string) {
	index, err := openIndex(workdir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer index.Close()

	name, err := index.CreateRoom()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, req, "/rooms/"+name, http.StatusSeeOther)
}

type room struct {
	ID       int64
	Name     string
	Shuffled bool
	Players  []mwfile.Player
}

func openMWByName(workdir, name string) (indexfile.RandoID, *mwfile.File, error) {
	index, err := openIndex(workdir)
	if err != nil {
		return 0, nil, err
	}
	defer index.Close()

	randoID, err := index.FindRoom(name)
	if err == indexfile.ErrRoomNotExist {
		return 0, nil, err
	}

	mw, err := openMW(workdir, randoID)
	if errors.Is(err, sqlite.ErrCantOpen) {
		if err := index.DeleteRoom(randoID); err != nil {
			log.Printf("delete room %d: %s", randoID, err)
		}
		err = indexfile.ErrRoomNotExist
	}
	return randoID, mw, err
}

func displayRoom(w http.ResponseWriter, req *http.Request, workdir string) {
	roomName := req.PathValue("randoName")
	randoID, mw, err := openMWByName(workdir, roomName)
	if err == indexfile.ErrRoomNotExist {
		http.NotFound(w, req)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer mw.Close()

	info := room{ID: int64(randoID), Name: roomName}
	info.Shuffled, err = mw.IsShuffled()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	info.Players, err = mw.GetPlayers()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if err := templates.ExecuteTemplate(w, "room.html", info); err != nil {
		log.Println(err)
	}
}

func handleRoomOp(w http.ResponseWriter, req *http.Request, workdir string, handler func(string, indexfile.RandoID, *mwfile.File)) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	roomName := req.FormValue("randoName")
	randoID, mw, err := openMWByName(workdir, roomName)
	if err == indexfile.ErrRoomNotExist {
		http.NotFound(w, req)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer mw.Close()

	handler(roomName, randoID, mw)
}

func shuffleRoom(w http.ResponseWriter, req *http.Request, workdir string, nf *notifier) {
	handleRoomOp(w, req, workdir, func(roomName string, randoID indexfile.RandoID, mw *mwfile.File) {
		if err := mw.Shuffle(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		nf.shuffleTopic.Notify(indexfile.RandoID(randoID))

		log.Println("rando generated for room", randoID)

		http.Redirect(w, req, "/rooms/"+roomName, http.StatusSeeOther)
	})
}

func unjoinPlayer(w http.ResponseWriter, req *http.Request, workdir string, nf *notifier) {
	handleRoomOp(w, req, workdir, func(roomName string, randoID indexfile.RandoID, mw *mwfile.File) {
		rawPlayerID := req.FormValue("playerID")
		playerID, err := strconv.ParseInt(rawPlayerID, 10, 64)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		err = mw.Unjoin(mwfile.PlayerID(playerID))
		if err == mwfile.ErrRoomAlreadyShuffled {
			if err := templates.ExecuteTemplate(w, "room-already-shuffled.html", roomName); err != nil {
				log.Println(err)
			}
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		log.Printf("room %d, player %d deleted", randoID, playerID)
		nf.playerChangeTopic.Notify(indexfile.RandoID(randoID))
		http.Redirect(w, req, "/rooms/"+roomName, http.StatusSeeOther)
	})
}
