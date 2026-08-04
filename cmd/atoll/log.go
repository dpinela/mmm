package main

import (
	"log/slog"

	"github.com/dpinela/mmm/cmd/atoll/internal/indexfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/isfile"
	"github.com/dpinela/mmm/cmd/atoll/internal/mwfile"
)

func logOp(op string) slog.Attr { return slog.String("op", op) }
func logRandoID[T interface {
	indexfile.MWRandoID | indexfile.ISRandoID
}](id T) slog.Attr {
	return slog.Int64("randoID", int64(id))
}
func logPlayerID[T interface {
	mwfile.PlayerID | isfile.PlayerID
}](id T) slog.Attr {
	return slog.Int64("playerID", int64(id))
}
func logMode(mode string) slog.Attr { return slog.String("mode", mode) }
