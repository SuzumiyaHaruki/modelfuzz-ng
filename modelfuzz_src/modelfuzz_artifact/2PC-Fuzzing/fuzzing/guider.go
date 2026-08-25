package fuzzing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zeu5/gocov"
	"github.com/egeberkaygulcan/2PC-Fuzzing/intercept"
)

type Guider interface {
	Check(iter string, trace []Step, eventTrace []intercept.Event) (bool, int)
	Coverage() int
	Reset()
}

type TLCStateGuider struct {
	TLCAddr   string
	statesMap map[int64]bool
	tlcClient *TLCClient
}

var _ Guider = &TLCStateGuider{}

func NewTLCStateGuider(tlcAddr string) *TLCStateGuider {
	return &TLCStateGuider{
		TLCAddr:   tlcAddr,
		statesMap: make(map[int64]bool),
		tlcClient: NewTLCClient(tlcAddr),
	}
}

func (t *TLCStateGuider) Reset() {
	t.statesMap = make(map[int64]bool)
}

func (t *TLCStateGuider) Coverage() int {
	return len(t.statesMap)
}

func (t *TLCStateGuider) Check(iter string, trace []Step, eventTrace []intercept.Event) (bool, int) {
	numNewStates := 0
	if tlcStates, err := t.tlcClient.SendTrace(eventTrace); err == nil {
		for _, s := range tlcStates {
			_, ok := t.statesMap[s.Key]
			if !ok {
				numNewStates += 1
				t.statesMap[s.Key] = true
			}
		}
	}
	return numNewStates != 0, numNewStates
}

func parseTLCStateTrace(states []TLCState) []TLCState {
	newStates := make([]TLCState, len(states))
	for i, s := range states {
		repr := strings.ReplaceAll(s.Repr, "\n", ",")
		repr = strings.ReplaceAll(repr, "/\\", "")
		repr = strings.ReplaceAll(repr, "\u003e\u003e", "]")
		repr = strings.ReplaceAll(repr, "\u003c\u003c", "[")
		repr = strings.ReplaceAll(repr, "\u003e", ">")
		newStates[i] = TLCState{
			Repr: repr,
			Key:  s.Key,
		}
	}
	return newStates
}

type TraceCoverageGuider struct {
	traces map[string]bool
	*TLCStateGuider
}

var _ Guider = &TraceCoverageGuider{}

func NewTraceCoverageGuider(tlcAddr string) *TraceCoverageGuider {
	return &TraceCoverageGuider{
		traces:         make(map[string]bool),
		TLCStateGuider: NewTLCStateGuider(tlcAddr),
	}
}

func (t *TraceCoverageGuider) Check(iter string, trace []Step, events []intercept.Event) (bool, int) {
	t.TLCStateGuider.Check(iter, trace, events)

	eTrace := newEventTrace(events)
	key := eTrace.Hash()

	new := 0
	if _, ok := t.traces[key]; !ok {
		t.traces[key] = true
		new = 1
	}

	return new != 0, new
}

func (t *TraceCoverageGuider) Coverage() int {
	return t.TLCStateGuider.Coverage()
}

func (t *TraceCoverageGuider) Reset() {
	t.traces = make(map[string]bool)
	t.TLCStateGuider.Reset()
}

type eventTrace struct {
	Nodes map[string]*eventNode
}

func (e *eventTrace) Hash() string {
	bs, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(bs)
	return hex.EncodeToString(hash[:])
}

type eventNode struct {
	intercept.Event
	Node string
	Prev string
	ID   string `json:"-"`
}

func (e *eventNode) Hash() string {
	bs, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(bs)
	return hex.EncodeToString(hash[:])
}

func newEventTrace(events []intercept.Event) *eventTrace {
	eTrace := &eventTrace{
		Nodes: make(map[string]*eventNode),
	}
	curEvent := make(map[string]*eventNode)

	for _, e := range events {
		node := &eventNode{
			Event: e,
			Node:  e.Node,
			Prev:  "",
		}
		prev, ok := curEvent[e.Node]
		if ok {
			node.Prev = prev.ID
		}
		node.ID = node.Hash()
		curEvent[e.Node] = node
		eTrace.Nodes[node.ID] = node
	}
	return eTrace
}

type LineCoverageGuider struct {
	covData *gocov.Coverage
	*TLCStateGuider
}

func NewLineCoverageGuider(tlcAddr string) *LineCoverageGuider {
	return &LineCoverageGuider{
		covData:        nil,
		TLCStateGuider: NewTLCStateGuider(tlcAddr),
	}
}

var _ Guider = &LineCoverageGuider{}

func (l *LineCoverageGuider) Check(iter string, trace []Step, events []intercept.Event) (bool, int) {
	l.TLCStateGuider.Check(iter, trace, events)
	cov, err := gocov.GetCoverage(gocov.CoverageConfig{
		MatchPkgs: []string{"github.com/egeberkaygulcan/2PC-Fuzzing/server"},
	})
	if err != nil {
		return false, 0
	}
	if l.covData == nil {
		l.covData = cov
		return cov.GetCoveredLines() > 0, cov.GetCoveredLines()
	}
	curLines := l.covData.GetCoveredLines()
	l.covData.Data.Merge(cov.Data)
	updatedLines := l.covData.GetCoveredLines()
	newLines := updatedLines - curLines
	return newLines > 0, newLines // float64(newLines) / float64(max(curLines, 1))
}

func (l *LineCoverageGuider) Reset() {
	fmt.Printf("Percentage of lines covered: %f\n", l.covData.GetPercent())
	l.covData.Reset()
	l.covData = nil
	l.TLCStateGuider.Reset()
}
