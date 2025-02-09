package main

import (
	"bufio"
	"compress/zlib"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/dpinela/mmm/internal/approto"
	"github.com/dpinela/mmm/internal/mwproto"
	"github.com/dpinela/mmm/internal/pickle"
)

func main() {
	var opts options
	flag.StringVar(&opts.workdir, "workdir", "./multipelago-seed", "Store multiworld result and game data in `dir`")
	flag.StringVar(&opts.apfile, "apfile", "./AP.archipelago", "The Archipelago seed to serve")
	flag.StringVar(&opts.mwserver, "mwserver", "127.0.0.1:38281", "The multiworld server to join")
	flag.StringVar(&opts.mwroom, "mwroom", "", "The room to join")
	flag.IntVar(&opts.apport, "apport", 38281, "Serve Archipelago on port `port`")
	flag.Parse()

	if err := serve(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type options struct {
	workdir  string
	apfile   string
	mwserver string
	mwroom   string
	apport   int
}

type placedItem struct {
	ownerID int
	name    string
}

func serve(opts options) error {
	data, err := readAPFile(opts.apfile)
	if err != nil {
		return err
	}
	if len(data.ConnectNames) != 1 {
		return fmt.Errorf(".archipelago contains %d worlds, expected only one", len(data.ConnectNames))
	}
	if len(data.Version) != approto.VersionNumberSize {
		return fmt.Errorf("invalid .archipelago version: %v", data.Version)
	}
	info, err := os.Stat(opts.workdir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("non-directory already present at workdir path %q", opts.workdir)
		}
		return playMW(opts, data)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := setupMW(opts, data); err != nil {
		return err
	}
	log.Println("MW setup complete")
	return playMW(opts, data)
}

func singularKey[K comparable, V any](m map[K]V) K {
	for k := range m {
		return k
	}
	panic("singularKey undefined on empty map")
}

type apdata struct {
	ConnectNames  map[string][]int
	Spheres       []map[int][]int
	Locations     map[int]map[int][]int
	Datapackage   map[string]apgamedata
	SlotInfo      map[int]apslot
	SlotData      map[int]map[string]any `pickle:"require_string_keys"`
	Version       []int
	Tags          []string
	ServerOptions apserveroptions
	SeedName      string
}

type apserveroptions struct {
	LocationCheckPoints int
	HintCost            int
	ReleaseMode         string
	CollectMode         string
	RemainingMode       string
}

type apslot struct {
	Name string
	Game string
	Type struct {
		Code int
	}
	GroupMembers []string
}

type apgamedata struct {
	ItemNameToID     map[string]int
	LocationNameToID map[string]int
	Checksum         string
	Original         map[string]any `pickle:"require_string_keys,remainder"`
}

func readAPFile(name string) (data apdata, err error) {
	const expectedAPFileVersion = 3

	apfile, err := os.Open(name)
	if err != nil {
		return
	}
	defer apfile.Close()
	r := bufio.NewReader(apfile)
	version, err := r.ReadByte()
	if err != nil {
		err = fmt.Errorf("read .archipelago version: %w", err)
		return
	}
	if version != expectedAPFileVersion {
		err = fmt.Errorf(".archipelago file is version %d, expected %d", version, expectedAPFileVersion)
		return
	}
	zr, err := zlib.NewReader(r)
	if err != nil {
		err = fmt.Errorf("decompress .archipelago: %w", err)
		return
	}
	defer zr.Close()
	err = pickle.Decode(zr, &data)
	return
}

var errConnectionLost = errors.New("connection lost")

const mwResultFileName = "mwresult.json"

func readMWResult(workdir string) (res mwproto.ResultMessage, err error) {
	f, err := os.Open(filepath.Join(workdir, mwResultFileName))
	if err != nil {
		return
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(&res)
	return
}

// This is the main item group used by the HK rando as well as
// the sole item group used by Haiku and Death's Door for multiworld
// purposes.
const singularItemGroup = "Main Item Group"

const (
	fakeLocationName = "Somewhere"
	fakeLocationID   = 1
)

var apServerVersion = approto.Version{
	Minor: 5,
	Build: 1,
	Class: "Version",
}
