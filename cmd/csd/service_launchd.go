//go:build darwin

package main

import (
	"strings"
)

func launchAgentPlist(cfg serviceConfig) string {
	args := append([]string{cfg.Executable}, cfg.Args...)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>`)
	b.WriteString(xmlEscape(cfg.Name))
	b.WriteString(`</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, arg := range args {
		b.WriteString("    <string>")
		b.WriteString(xmlEscape(arg))
		b.WriteString("</string>\n")
	}
	b.WriteString(`  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>CODEX_SWARM_DAEMON_ADDR</key>
    <string>`)
	b.WriteString(xmlEscape(cfg.Addr))
	b.WriteString(`</string>
    <key>CODEX_SWARM_STATE</key>
    <string>`)
	b.WriteString(xmlEscape(cfg.StatePath))
	b.WriteString(`</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/codex-swarm-daemon.out.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/codex-swarm-daemon.err.log</string>
</dict>
</plist>
`)
	return b.String()
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
