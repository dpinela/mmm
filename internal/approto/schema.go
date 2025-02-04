package approto

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

type Version struct {
	Major int    `json:"major"`
	Minor int    `json:"minor"`
	Build int    `json:"build"`
	Class string `json:"class"` // why, just why??
}

const VersionNumberSize = 3

func MakeVersion(nums [VersionNumberSize]int) Version {
	return Version{
		Class: "Version",
		Major: nums[0],
		Minor: nums[1],
		Build: nums[2],
	}
}

type RoomInfo struct {
	Cmd                  string          `json:"cmd"`
	Version              Version         `json:"version"`
	GeneratorVersion     Version         `json:"generator_version"`
	Tags                 []string        `json:"tags"`
	Password             bool            `json:"password"`
	Permissions          RoomPermissions `json:"permissions"`
	HintCost             int             `json:"hintCost"`
	LocationCheckPoints  int             `json:"location_check_points"`
	Games                []string        `json:"games"`
	DataPackageChecksums []string        `json:"data_package_checksums"`
	SeedName             string          `json:"seed_name"`
	Time                 float64         `json:"time"`
}

func (RoomInfo) isServerMessage() {}

type RoomPermissions struct {
	Release   Permission `json:"release"`
	Collect   Permission `json:"collect"`
	Remaining Permission `json:"remaining"`
}

type Permission int

const (
	PermissionEnabled Permission = 0b001
	PermissionGoal    Permission = 0b010
	PermissionAuto    Permission = 0b110
)

func PermissionForMode(mode string) Permission {
	switch mode {
	case "disabled":
		return 0
	case "enabled":
		return PermissionEnabled
	case "auto":
		return PermissionAuto
	case "auto-enabled":
		return PermissionAuto | PermissionEnabled
	case "goal":
		return PermissionGoal
	default:
		return PermissionEnabled
	}
}

type DataPackage struct {
	LocationNameToID map[string]int `json:"location_name_to_id"`
	ItemNameToID     map[string]int `json:"item_name_to_id"`
	Checksum         string         `json:"checksum"`
}

func (dp *DataPackage) SetChecksum() {
	sha := sha256.New()
	if err := json.NewEncoder(sha).Encode(dp); err != nil {
		panic(err)
	}
	dp.Checksum = fmt.Sprintf("%02x", sha.Sum(make([]byte, 0, sha256.Size)))
}

type GetDataPackage struct {
	Games []string
}

func (GetDataPackage) isClientMessage() {}

type DataPackageMessage struct {
	Cmd  string          `json:"cmd"`
	Data DataPackageData `json:"data"`
}

type DataPackageData struct {
	// Uses [any] as the value type so that the original data package
	// from the generator can be passed through unchanged.
	Games map[string]any `json:"games"`
}

func (DataPackageMessage) isServerMessage() {}

func MakeDataPackageMessage() DataPackageMessage {
	return DataPackageMessage{
		Cmd:  "DataPackage",
		Data: DataPackageData{Games: map[string]any{}},
	}
}

type Connect struct {
	Password      string
	Game          string
	Name          string
	UUID          string
	Version       Version
	ItemsHandling *ItemHandlingMode `json:"items_handling"`
	Tags          []string
	SlotData      bool
}

func (Connect) isClientMessage() {}

type ItemHandlingMode int

const (
	ReceiveOthersItems ItemHandlingMode = 1 << iota
	AcceptOwnItems
	AcceptStartingItems
)

type Connected struct {
	Cmd              string              `json:"cmd"`
	Team             int                 `json:"team"`
	Slot             int                 `json:"slot"`
	Players          []NetworkPlayer     `json:"players"`
	MissingLocations []int               `json:"missing_locations"`
	CheckedLocations []int               `json:"checked_locations"`
	SlotData         map[string]any      `json:"slot_data"`
	SlotInfo         map[int]NetworkSlot `json:"slot_info"`
	HintPoints       int                 `json:"hint_points"`
}

func (Connected) isServerMessage() {}

type NetworkPlayer struct {
	Team  int    `json:"team"`
	Slot  int    `json:"slot"`
	Alias string `json:"alias"`
	Name  string `json:"name"`
}

type NetworkSlot struct {
	Name         string   `json:"name"`
	Game         string   `json:"game"`
	Type         SlotType `json:"type"`
	GroupMembers []int    `json:"group_members"`
}

type SlotType int

const (
	SlotTypeSpectator SlotType = iota
	SlotTypePlayer
	SlotTypeGroup
)

type ReceivedItems struct {
	Cmd   string        `json:"cmd"`
	Index int           `json:"index"`
	Items []NetworkItem `json:"items"`
}

func (ReceivedItems) isServerMessage() {}

type NetworkItem struct {
	Item     int `json:"item"`
	Location int `json:"location"`
	Player   int `json:"player"`
	Flags    int `json:"flags"`
}

type SetMessage struct {
	Key        string
	Default    any
	WantReply  bool `json:"want_reply"`
	Operations []DataStorageOperation
}

const ReadOnlyKeyPrefix = "_read_"

func (SetMessage) isClientMessage() {}

type DataStorageOperation struct {
	Operation string
	Value     any
}

type SetReplyMessage struct {
	Cmd           string `json:"cmd"`
	Key           string `json:"key"`
	Value         any    `json:"value"`
	OriginalValue any    `json:"original_value"`
	Slot          int    `json:"slot"`
}

func (SetReplyMessage) isServerMessage() {}

type GetMessage struct {
	Keys []string
	Rest map[string]any
}

func (GetMessage) isClientMessage() {}

type RetrievedMessage struct {
	Keys map[string]any
	Rest map[string]any
}

func (RetrievedMessage) isServerMessage() {}

const (
	ClientStatusUnknown   = 0
	ClientStatusConnected = 5
	ClientStatusReady     = 10
	ClientStatusPlaying   = 20
	ClientStatusGoal      = 30
)
