// Measure the instance folders under a root once and print their facts (what a host's lifecycle does daily).
//
//	go run ./example --root ./pb_root --instances instances.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"

	"github.com/pockethost/pocketone/ext/host"
	"github.com/pockethost/pocketone/ext/lifecycle"
)

func main() {
	root := flag.String("root", "./pb_root", "one folder per instance: <root>/<id>")
	instances := flag.String("instances", "instances.yaml", "instances file")
	flag.Parse()

	src := &host.YAMLSource{Path: *instances, Required: true}
	list, err := src.Snapshot(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	dir := host.NewDirectory(slog.Default())
	dir.Load(list)

	lc := &lifecycle.Lifecycle{
		Directory: dir,
		Folder: func(i host.Instance) string {
			if i.Dir != "" {
				return i.Dir
			}
			return filepath.Join(*root, i.ID)
		},
		Sink: lifecycle.LogSink{},
	}
	lc.Step(context.Background()) // never measured: every instance is measured now
	for _, f := range lc.Local() {
		fmt.Printf("%-24s %12d bytes\n", f.Instance, f.DiskBytes)
	}
}
