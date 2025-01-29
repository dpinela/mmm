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
