package indexfile

import (
	"math/rand/v2"
	"strings"
)

func generateRoomName(entropy int) string {
	var sb strings.Builder
	sb.Grow(3 * entropy)
	for range entropy {
		i := rand.IntN(len(grubOnomatopeias))
		sb.WriteString(grubOnomatopeias[i])
	}
	return sb.String()
}

var grubOnomatopeias = [...]string{
	"mi", "Mi", "mI", "MI", "ma", "Ma", "mA", "MA",
	"mee", "meE", "mEe", "mEE", "Mee", "MeE", "MEe", "MEE",
	"maw", "maW", "mAw", "mAW", "Maw", "MaW", "MAw", "MAW",
}
