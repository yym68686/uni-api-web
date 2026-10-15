package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type startupTimings struct {
	began, last time.Time
	phase       string
	fields      []string
}

func newStartupTimings() *startupTimings {
	n := time.Now()
	return &startupTimings{began: n, last: n, phase: "inspect_cache"}
}
func (t *startupTimings) next(phase string) {
	n := time.Now()
	t.fields = append(t.fields, fmt.Sprintf("%s_ms=%d", t.phase, n.Sub(t.last).Milliseconds()))
	t.last = n
	t.phase = phase
}
func (t *startupTimings) finish(err error) {
	phase := t.phase
	t.next("done")
	class := ""
	if err != nil {
		class = checkpointFailure(err).Class
	}
	log.Printf("analytics checkpoint_timing elapsed_ms=%d last_stage=%s class=%s %s", time.Since(t.began).Milliseconds(), phase, class, strings.Join(t.fields, " "))
}
