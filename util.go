package main

import (
	"fmt"
	"os"
)

func must(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "isolate: "+format+"\n", args...)
	os.Exit(1)
}
