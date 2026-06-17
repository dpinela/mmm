package apdata

type File struct {
	ConnectNames      map[string][]int
	Spheres           []map[int][]int64
	Locations         map[int]map[int64][]int64
	Datapackage       map[string]DataPackage
	PrecollectedItems map[int][]int64
	SlotInfo          map[int]Slot
	SlotData          map[int]map[string]any `pickle:"require_string_keys"`
	Version           []int
	Tags              []string
	ServerOptions     ServerOptions
	SeedName          string
}

type ServerOptions struct {
	LocationCheckPoints int
	HintCost            int
	ReleaseMode         string
	CollectMode         string
	RemainingMode       string
}

type Slot struct {
	Name string
	Game string
	Type struct {
		Code int
	}
	GroupMembers []string
}

type DataPackage struct {
	ItemNameToID     map[string]int64
	LocationNameToID map[string]int64
	Checksum         string
	Original         map[string]any `pickle:"require_string_keys,remainder"`
}
