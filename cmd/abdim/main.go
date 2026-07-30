package main

import "os"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(_ []string) int {
	return 0
}
