package apdata

import (
	"bufio"
	"compress/zlib"
	"fmt"
	"io"

	"github.com/dpinela/mmm/internal/pickle"
)

func Read(apfile io.Reader) (data File, err error) {
	const expectedAPFileVersion = 3

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
