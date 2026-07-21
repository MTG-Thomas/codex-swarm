//go:build ignore

// Command generate-windows-resources creates architecture-specific Windows
// VERSIONINFO resources for the cs and csd release binaries.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

var numericVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:\.\d+)?$`)

const versionInfoJSON = `{
  "FixedFileInfo": {
    "FileVersion": {},
    "ProductVersion": {},
    "FileFlagsMask": "3f",
    "FileFlags": "00",
    "FileOS": "040004",
    "FileType": "01",
    "FileSubType": "00"
  },
  "StringFileInfo": {},
  "VarFileInfo": {
    "Translation": {
      "LangID": "0409",
      "CharsetID": "04B0"
    }
  }
}`

type binary struct {
	directory   string
	name        string
	description string
}

func main() {
	version := flag.String("version", "", "numeric release version, for example 0.4.1")
	flag.Parse()
	if !numericVersion.MatchString(*version) {
		fatalf("version must contain three or four numeric components, got %q", *version)
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		fatalf("resolve repository root: %v", err)
	}
	config, err := os.CreateTemp("", "codex-swarm-versioninfo-*.json")
	if err != nil {
		fatalf("create temporary VERSIONINFO config: %v", err)
	}
	configPath := config.Name()
	defer os.Remove(configPath)
	if _, err := config.WriteString(versionInfoJSON); err != nil {
		config.Close()
		fatalf("write temporary VERSIONINFO config: %v", err)
	}
	if err := config.Close(); err != nil {
		fatalf("close temporary VERSIONINFO config: %v", err)
	}

	binaries := []binary{
		{directory: filepath.Join(repoRoot, "cmd", "cs"), name: "cs", description: "codex-swarm operator CLI"},
		{directory: filepath.Join(repoRoot, "cmd", "csd"), name: "csd", description: "codex-swarm daemon"},
	}
	for _, item := range binaries {
		removeGeneratedResources(item.directory)
		args := []string{
			"tool", "goversioninfo",
			"-platform-specific",
			"-propagate-ver-strings",
			"-company=MTG-Thomas",
			"-copyright=Copyright (c) 2026 Thomas Bray. Licensed under MIT.",
			"-description=" + item.description,
			"-file-version=" + *version,
			"-internal-name=" + item.name,
			"-original-name=" + item.name + ".exe",
			"-product-name=codex-swarm",
			"-product-version=" + *version,
			configPath,
		}
		command := exec.Command("go", args...)
		command.Dir = item.directory
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			fatalf("generate resources for %s: %v", item.name, err)
		}
		for _, arch := range []string{"amd64", "arm64"} {
			path := filepath.Join(item.directory, "resource_windows_"+arch+".syso")
			if _, err := os.Stat(path); err != nil {
				fatalf("verify generated resource %s: %v", path, err)
			}
		}
	}
	fmt.Printf("generated Windows VERSIONINFO resources version=%s binaries=cs,csd architectures=amd64,arm64\n", *version)
}

func removeGeneratedResources(directory string) {
	matches, err := filepath.Glob(filepath.Join(directory, "resource_windows_*.syso"))
	if err != nil {
		fatalf("find generated resources in %s: %v", directory, err)
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			fatalf("remove stale generated resource %s: %v", path, err)
		}
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "generate-windows-resources: "+format+"\n", args...)
	os.Exit(1)
}
