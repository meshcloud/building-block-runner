package runnerproc

import (
	"bufio"
	"encoding/json/v2"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Record map[string]any

func (r Record) String() string {
	encoded, _ := json.Marshal(r)
	return string(encoded)
}

func (r Record) Attr(key string) string {
	value, _ := r[key].(string)
	return value
}

func (r Record) belowInfo() bool {
	var level slog.Level
	return level.UnmarshalText([]byte(r.Attr(slog.LevelKey))) == nil && level < slog.LevelInfo
}

type Log struct {
	mu      sync.Mutex
	records []Record
	lines   []string
	// A debug record may hold a secret.
	atLeastInfo []string
}

func (l *Log) AssertNotLoggedAtInfo(t *testing.T, secret string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	assert.NotContains(t, strings.Join(l.atLeastInfo, "\n"), secret)
}

func (l *Log) OfRun(t *testing.T, runUuid string) []Record {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(l.records), func(r Record) bool { return r.Attr("run") != runUuid })
}

func (l *Log) Await(t *testing.T, description string, matches func(Record) bool) Record {
	t.Helper()
	return l.AwaitInOrder(t, description, matches)[0]
}

func (l *Log) AwaitInOrder(t *testing.T, description string, matchers ...func(Record) bool) []Record {
	t.Helper()
	var found []Record
	require.Eventuallyf(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		found = found[:0]
		for _, r := range l.records {
			if len(found) < len(matchers) && matchers[len(found)](r) {
				found = append(found, r)
			}
		}
		return len(found) == len(matchers)
	}, RunTimeout, PollInterval, "no records %s", description)
	return found
}

func (l *Log) AwaitRunInStep(t *testing.T, step string) string {
	t.Helper()
	return l.Await(t, "of a run in step "+step, func(r Record) bool {
		return r.Attr("step") == step && r.Attr("run") != ""
	}).Attr("run")
}

func (l *Log) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		var parsed Record
		parseErr := json.Unmarshal([]byte(line), &parsed)
		l.mu.Lock()
		l.lines = append(l.lines, line)
		if parseErr == nil {
			l.records = append(l.records, parsed)
		}
		if parseErr != nil || !parsed.belowInfo() {
			l.atLeastInfo = append(l.atLeastInfo, line)
		}
		l.mu.Unlock()
	}
}

func (l *Log) raw() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func AssertRecord(t *testing.T, records []Record, description string, matches func(Record) bool) {
	t.Helper()
	assert.Truef(t, slices.ContainsFunc(records, matches), "no record %s among %d records of the run", description, len(records))
}

func AssertNoRecord(t *testing.T, records []Record, description string, matches func(Record) bool) {
	t.Helper()
	assert.Falsef(t, slices.ContainsFunc(records, matches), "a record %s", description)
}
