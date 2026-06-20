package main

import (
	"flag"
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func currentVersionInfo() versionInfo {
	return versionInfo{
		Version: appVersion(),
		Commit:  commit,
		Date:    date,
	}
}

func appVersion() string {
	if version == "" {
		return "dev"
	}
	return version
}

func handleVersion() {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Write machine-readable JSON")
	_ = fs.Parse(os.Args[2:])

	info := currentVersionInfo()
	if *jsonOutput {
		writeJSON(info)
		return
	}
	fmt.Printf("snapzip %s\ncommit: %s\nbuilt: %s\n", info.Version, info.Commit, info.Date)
}
