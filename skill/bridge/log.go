package main

import (
	"fmt"
	"strings"
	"time"
)

func logMsg(level, msg string, args ...any) {
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	prefix := fmt.Sprintf("[%s] [%s]", ts, strings.ToUpper(level))
	if len(args) > 0 {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, fmt.Sprint(a))
		}
		fmt.Println(prefix, msg, strings.Join(parts, " "))
	} else {
		fmt.Println(prefix, msg)
	}
}
