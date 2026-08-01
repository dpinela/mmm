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

func (srv *server) serveConsole() {
	mux := http.NewServeMux()
	static, err := fs.Sub(staticPages, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServerFS(static))
	mux.HandleFunc("POST /create-room", srv.createRoom)
	mux.HandleFunc("POST /shuffle", srv.shuffleRoom)
	mux.HandleFunc("GET /rooms/{randoName}", srv.displayRoom)
	mux.HandleFunc("POST /unjoin-player", srv.unjoinPlayer)
	err = http.ListenAndServe(srv.config.ListenAddress+":"+srv.config.ConsolePort, mux)
	if err != nil {
		log.Println(err)
	}
}

func (srv *server) createRoom(w http.ResponseWriter, req *http.Request) {
	index, err := srv.openIndex()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer index.Close()

	id, name, err := index.CreateMWRoom()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if err := mwfile.Create(mwPath(srv.workdir, id)); err != nil {
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

func (srv *server) openMWByName(name string) (indexfile.MWRandoID, *mwfile.File, error) {
	index, err := srv.openIndex()
	if err != nil {
		return 0, nil, err
	}
	defer index.Close()

	randoID, err := index.FindRoom(name)
	if err == indexfile.ErrRoomNotExist {
		return 0, nil, err
	}

	mw, err := srv.openMW(randoID)
	if errors.Is(err, sqlite.ErrCantOpen) {
		if err := index.DeleteRoom(randoID); err != nil {
			log.Printf("delete room %d: %s", randoID, err)
		}
		err = indexfile.ErrRoomNotExist
	}
	return randoID, mw, err
}

func (srv *server) displayRoom(w http.ResponseWriter, req *http.Request) {
	roomName := req.PathValue("randoName")
	randoID, mw, err := srv.openMWByName(roomName)
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

func (srv *server) handleRoomOp(w http.ResponseWriter, req *http.Request, handler func(string, indexfile.MWRandoID, *mwfile.File)) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	roomName := req.FormValue("randoName")
	randoID, mw, err := srv.openMWByName(roomName)
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

func (srv *server) shuffleRoom(w http.ResponseWriter, req *http.Request) {
	srv.handleRoomOp(w, req, func(roomName string, randoID indexfile.MWRandoID, mw *mwfile.File) {
		if err := mw.Shuffle(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		srv.mwNotifier.shuffleTopic.Notify(indexfile.MWRandoID(randoID))

		log.Println("rando generated for room", randoID)

		http.Redirect(w, req, "/rooms/"+roomName, http.StatusSeeOther)
	})
}

func (srv *server) unjoinPlayer(w http.ResponseWriter, req *http.Request) {
	srv.handleRoomOp(w, req, func(roomName string, randoID indexfile.MWRandoID, mw *mwfile.File) {
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
		srv.mwNotifier.playerChangeTopic.Notify(indexfile.MWRandoID(randoID))
		http.Redirect(w, req, "/rooms/"+roomName, http.StatusSeeOther)
	})
}
