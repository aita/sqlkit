package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	version := flag.Bool("version", false, "print sqlkitgen version")
	flag.Parse()
	if *version {
		fmt.Fprintln(os.Stdout, "sqlkitgen dev")
		return
	}
	fmt.Fprintln(os.Stdout, "sqlkitgen: code generation is not implemented yet")
}
