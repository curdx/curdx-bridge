// curdx-cleanup - Clean up zombie daemons and stale files.
//
// Usage:
//
//	curdx-cleanup [--list] [--clean] [--kill-zombies]
//
// Source: claude_code_bridge/bin/curdx-cleanup
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

type daemonInfo struct {
	PID         int
	ParentPID   int
	ParentAlive bool
	ProjectHash string
	StartedAt   string
}

func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return isProcessAlive(pid)
}

func cleanupStaleStateFiles() []string {
	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".cache", "curdx", "projects")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}

	var removed []string
	pattern := filepath.Join(cacheDir, "*", "askd.json")
	matches, _ := filepath.Glob(pattern)
	for _, stateFile := range matches {
		data, err := os.ReadFile(stateFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %s\n", stateFile, err)
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %s\n", stateFile, err)
			continue
		}
		pid := intFromJSON(obj, "pid")
		if pid > 0 && !isPIDAlive(pid) {
			if err := os.Remove(stateFile); err != nil {
				fmt.Fprintf(os.Stderr, "Error processing %s: %s\n", stateFile, err)
				continue
			}
			removed = append(removed, stateFile)
			fmt.Printf("Removed stale state file: %s\n", stateFile)
		}
	}
	return removed
}

func cleanupStaleLocks() []string {
	home, _ := os.UserHomeDir()
	runDir := filepath.Join(home, ".curdx", "run")
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		return nil
	}

	var removed []string
	pattern := filepath.Join(runDir, "*.lock")
	matches, _ := filepath.Glob(pattern)
	for _, lockFile := range matches {
		data, err := os.ReadFile(lockFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %s\n", lockFile, err)
			continue
		}
		pidStr := string(data)
		if pidStr == "" {
			continue
		}
		pid := 0
		fmt.Sscanf(pidStr, "%d", &pid)
		if pid <= 0 {
			continue
		}
		if !isPIDAlive(pid) {
			if err := os.Remove(lockFile); err != nil {
				fmt.Fprintf(os.Stderr, "Error processing %s: %s\n", lockFile, err)
				continue
			}
			removed = append(removed, lockFile)
			fmt.Printf("Removed stale lock: %s\n", filepath.Base(lockFile))
		}
	}
	return removed
}

func listRunningDaemons() []daemonInfo {
	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".cache", "curdx", "projects")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}

	var daemons []daemonInfo
	pattern := filepath.Join(cacheDir, "*", "askd.json")
	matches, _ := filepath.Glob(pattern)
	for _, stateFile := range matches {
		data, err := os.ReadFile(stateFile)
		if err != nil {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err != nil {
			continue
		}
		pid := intFromJSON(obj, "pid")
		parentPID := intFromJSON(obj, "parent_pid")
		if pid <= 0 || !isPIDAlive(pid) {
			continue
		}
		parentAlive := false
		if parentPID > 0 {
			parentAlive = isPIDAlive(parentPID)
		}
		projectHash := filepath.Base(filepath.Dir(stateFile))
		startedAt := "unknown"
		if v, ok := obj["started_at"]; ok {
			startedAt = fmt.Sprintf("%v", v)
		}
		daemons = append(daemons, daemonInfo{
			PID:         pid,
			ParentPID:   parentPID,
			ParentAlive: parentAlive,
			ProjectHash: projectHash,
			StartedAt:   startedAt,
		})
	}
	return daemons
}

func intFromJSON(obj map[string]interface{}, key string) int {
	v, ok := obj[key]
	if !ok || v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		n := 0
		fmt.Sscanf(val, "%d", &n)
		return n
	default:
		return 0
	}
}

func main() {
	listFlag := flag.Bool("list", false, "List running daemons")
	cleanFlag := flag.Bool("clean", false, "Clean stale files")
	killZombies := flag.Bool("kill-zombies", false, "Kill zombie daemons (parent dead)")
	flag.Parse()

	if *listFlag || (!*cleanFlag && !*killZombies) {
		fmt.Println("=== Running askd daemons ===")
		daemons := listRunningDaemons()
		if len(daemons) == 0 {
			fmt.Println("No running daemons found")
		} else {
			for _, d := range daemons {
				status := "OK"
				if !d.ParentAlive {
					status = "ZOMBIE (parent dead)"
				}
				fmt.Printf("  PID %d (parent %d) - %s\n", d.PID, d.ParentPID, status)
				fmt.Printf("    Project: %s\n", d.ProjectHash)
				fmt.Printf("    Started: %s\n", d.StartedAt)
			}
		}
	}

	if *cleanFlag {
		fmt.Println("\n=== Cleaning stale files ===")
		cleanupStaleStateFiles()
		cleanupStaleLocks()
	}

	if *killZombies {
		fmt.Println("\n=== Killing zombie daemons ===")
		daemons := listRunningDaemons()
		for _, d := range daemons {
			if !d.ParentAlive {
				err := terminateProcess(d.PID)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to kill PID %d: %s\n", d.PID, err)
				} else {
					fmt.Printf("Killed zombie daemon PID %d\n", d.PID)
				}
			}
		}
	}
}
