package main

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Task Scheduler starts the daemon in the logged-in user's session. No password,
// elevated token, shell, or host credential is embedded in the task definition.
func userTaskXML(sid, executable, arguments string) string {
	escape := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
 <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
 <Principals><Principal id="User"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
 <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings>
 <Actions Context="User"><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions>
</Task>
`, escape(sid), escape(sid), escape(executable), escape(arguments))
}

// schtasks reads imported task files as UTF-16; match the declaration to the
// little-endian BOM instead of relying on XML's UTF-8 default.
func userTaskFile(sid, executable, arguments string) []byte {
	doc := strings.Replace(userTaskXML(sid, executable, arguments), `encoding="UTF-8"`, `encoding="UTF-16"`, 1)
	words := utf16.Encode([]rune(doc))
	data := make([]byte, 2+len(words)*2)
	data[0], data[1] = 0xff, 0xfe
	for i, w := range words {
		binary.LittleEndian.PutUint16(data[2+i*2:], w)
	}
	return data
}
