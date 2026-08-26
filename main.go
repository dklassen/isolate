package main

import (
	"fmt"
	"os"
)

// version is the single source of the tool's version; `isolate version`
// prints it and imageRef (container.go) derives its tag from it.
const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "version":
		fmt.Println("isolate " + version)
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: isolate run <folder> [--agent pi|claude] [--config <path>] [--config-file <path>] [--sessions <path>] [--memory <size>] [--secrets-file <path>] [-- <agent args>...]")
}
