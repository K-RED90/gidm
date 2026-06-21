package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "0.1.0-dev"

func main() {
	fs := flag.NewFlagSet("gidm", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "gidm %s — Go Internet Download Manager\n\n", version)
		fmt.Fprintln(os.Stderr, "Usage: gidm [flags] <command>")
		fmt.Fprintln(os.Stderr, "Commands (arriving in M2): add, list, pause, resume, rm")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Println(version)
		return
	}
	fs.Usage()
}
