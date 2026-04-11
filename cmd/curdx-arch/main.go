// curdx-arch - Hippocampus Architecture Manager.
//
// Usage:
//
//	curdx-arch [snapshot [--force] | init]
//
// Source: claude_code_bridge/bin/curdx-arch
package main

import (
	"flag"
	"fmt"
	"os"
)

func usage() {
	fmt.Println("usage: curdx-arch [-h] {snapshot,init} ...")
	fmt.Println()
	fmt.Println("Hippocampus Architecture Manager")
	fmt.Println()
	fmt.Println("positional arguments:")
	fmt.Println("  {snapshot,init}")
	fmt.Println("    snapshot       Update the Long Term Memory snapshot")
	fmt.Println("    init           Initialize Hippocampus memory")
	fmt.Println()
	fmt.Println("options:")
	fmt.Println("  -h, --help       show this help message and exit")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	cmd := os.Args[1]
	if cmd == "-h" || cmd == "--help" {
		usage()
		return
	}

	switch cmd {
	case "snapshot":
		fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
		force := fs.Bool("force", false, "Force update even if no changes")
		fs.Parse(os.Args[2:])
		runSnapshot(*force)
	case "init":
		runSnapshot(true) // init is an alias for snapshot --force
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func runSnapshot(force bool) {
	_ = force
	// The Python implementation depends on hippocampus.ltm.RepomixLTM which
	// is not yet ported to Go. Print a placeholder message matching the
	// expected behavior interface.
	fmt.Println("[INFO] Hippocampus LTM snapshot not yet available in Go build")
	fmt.Println("[INFO] Use the Python version: curdx-arch snapshot")
}
