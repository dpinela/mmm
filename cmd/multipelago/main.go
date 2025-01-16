package main

import (
	"bufio"
	"compress/zlib"
	"flag"
	"fmt"
	"os"

	"github.com/dpinela/mmm/internal/pickle"
)

func main() {
	var opts options
	flag.StringVar(&opts.apfile, "apfile", "./AP.archipelago", "The Archipelago seed to serve")
	flag.Parse()

	if err := serve(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type options struct {
	apfile string
}

func serve(opts options) error {
	apfile, err := os.Open(opts.apfile)
	if err != nil {
		return err
	}
	defer apfile.Close()
	r := bufio.NewReader(apfile)
	version, err := r.ReadByte()
	if err != nil {
		return fmt.Errorf("read .archipelago version: %w", err)
	}
	fmt.Printf("%s: version %d\n", opts.apfile, version)
	zr, err := zlib.NewReader(r)
	if err != nil {
		return fmt.Errorf("decompress .archipelago: %w", err)
	}
	defer zr.Close()
	var data struct {
		ConnectNames map[string][]int
	}
	if err := pickle.Decode(zr, &data); err != nil {
		return err
	}
	fmt.Println(data)
	return nil
}
