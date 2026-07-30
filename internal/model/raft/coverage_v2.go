package raft

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

const (
	// SemanticSchemaV2Prototype is deliberately separate from the persisted v1
	// schema. It is experimental and is not used by Corpus admission.
	SemanticSchemaV2Prototype = "raft-coverage-v2-prototype"

	// PrototypeLagSmallMax centralizes the only numeric lag threshold used by
	// the prototype: 0, 1, small (2..3), and large (4+).
	PrototypeLagSmallMax uint64 = 3
)

// PrototypeCoverageProjection contains only coarse semantic states. This
// round intentionally does not define a v2 semantic-transition schema.
type PrototypeCoverageProjection struct {
	Schema    string  `json:"schema"`
	StateKeys []int64 `json:"state_keys"`
}

// ProjectCoverageV2Prototype projects controlled TLC states without changing
// the v1 projector or the active Corpus feedback path.
func ProjectCoverageV2Prototype(states []model.State) (PrototypeCoverageProjection, error) {
	stateSet := make(map[int64]struct{}, len(states))
	for index, state := range states {
		serialized, err := SerializeV2PrototypeState(state)
		if err != nil {
			return PrototypeCoverageProjection{}, fmt.Errorf("state %d: %w", index, err)
		}
		stateSet[coverageKey(serialized)] = struct{}{}
	}
	return PrototypeCoverageProjection{
		Schema:    SemanticSchemaV2Prototype,
		StateKeys: sortedCoverageKeys(stateSet),
	}, nil
}

// SerializeV2PrototypeState returns the stable, explicitly versioned JSON
// representation that is hashed into a v2 key.
func SerializeV2PrototypeState(state model.State) (string, error) {
	parsed, err := parsePrototypeState(state)
	if err != nil {
		return "", err
	}
	key, err := parsed.semanticKey()
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("serialize %s state: %w", SemanticSchemaV2Prototype, err)
	}
	return string(encoded), nil
}

// PrototypeRecoveryComplete reports whether an active restarted node has
// caught up with the unique maximal-term active leader. It is used only by
// offline lifecycle-context reconstruction and does not change v2 keys.
func PrototypeRecoveryComplete(state model.State, node core.NodeID) (bool, error) {
	parsed, err := parsePrototypeState(state)
	if err != nil {
		return false, err
	}
	index := int(node) - 1
	if index < 0 || index >= len(parsed.roles) || !parsed.active[int(node)] {
		return false, nil
	}
	leaders := make([]int, 0, 1)
	var maximum uint64
	for candidate, role := range parsed.roles {
		if role != "leader" || !parsed.active[candidate+1] {
			continue
		}
		if len(leaders) == 0 || parsed.terms[candidate] > maximum {
			maximum = parsed.terms[candidate]
			leaders = leaders[:0]
		}
		if parsed.terms[candidate] == maximum {
			leaders = append(leaders, candidate)
		}
	}
	if len(leaders) != 1 {
		return false, nil
	}
	leader := leaders[0]
	if prototypeLogRelation(parsed.logs[index], parsed.logs[leader]) != "equal" ||
		parsed.commit[index] != parsed.commit[leader] {
		return false, nil
	}
	if parsed.storage != nil {
		if parsed.storage.applied[index] != parsed.storage.applied[leader] ||
			parsed.storage.pending[leader][index] > 0 ||
			parsed.storage.next[leader][index] < parsed.storage.first[leader] {
			return false, nil
		}
	}
	return true, nil
}

type prototypeEntry struct {
	term  uint64
	value uint64
}

type prototypeStorage struct {
	applied  []uint64
	snapshot []uint64
	first    []uint64
	next     [][]uint64
	pending  [][]uint64
}

type prototypeState struct {
	roles    []string
	terms    []uint64
	logs     [][]prototypeEntry
	commit   []uint64
	match    [][]uint64
	votes    []map[int]bool
	votedFor []uint64
	active   map[int]bool
	storage  *prototypeStorage
}

type prototypeClassCount struct {
	Class string `json:"class"`
	Count int    `json:"count"`
}

type prototypeStateKey struct {
	Schema              string                `json:"schema"`
	ClusterSize         int                   `json:"cluster_size"`
	QuorumAvailable     bool                  `json:"quorum_available"`
	RoleTopology        []prototypeClassCount `json:"role_topology"`
	TermTopology        string                `json:"term_topology"`
	LeaderTermPosition  string                `json:"leader_term_position"`
	CandidateTerm       string                `json:"candidate_term_position"`
	LogTopology         string                `json:"log_topology"`
	CommittedPrefixes   string                `json:"committed_prefixes"`
	CatchUpTopology     []prototypeClassCount `json:"catch_up_topology"`
	SnapshotTopology    []prototypeClassCount `json:"snapshot_topology"`
	VotingTopology      []prototypeClassCount `json:"voting_topology"`
	CanonicalNodeShapes []prototypeClassCount `json:"canonical_node_shapes"`
}

type prototypeNodeShape struct {
	Lifecycle       string                `json:"lifecycle"`
	Role            string                `json:"role"`
	TermPosition    string                `json:"term_position"`
	LogRelation     string                `json:"log_relation"`
	CommitLag       string                `json:"commit_lag"`
	AppliedLag      string                `json:"applied_lag"`
	SnapshotPhase   string                `json:"snapshot_phase"`
	VotedFor        string                `json:"voted_for"`
	CandidateVotes  string                `json:"candidate_votes"`
	LeaderPeerLags  []prototypeClassCount `json:"leader_peer_lags"`
	InboundCatchUps []prototypeClassCount `json:"inbound_catch_ups"`
}

func parsePrototypeState(state model.State) (prototypeState, error) {
	if strings.TrimSpace(state.Text) == "" {
		return prototypeState{}, fmt.Errorf("state text is empty (raw key %d)", state.Key)
	}
	assignments := stateAssignments(state.Text)
	for _, name := range []string{"currentActive", "state", "currentTerm", "log", "commitIndex", "matchIndex", "votesGranted", "votedFor"} {
		if assignments[name] == "" {
			return prototypeState{}, fmt.Errorf("missing %s assignment", name)
		}
	}
	roles, ok := parseStringTuple(assignments["state"])
	if !ok || len(roles) == 0 {
		return prototypeState{}, fmt.Errorf("invalid state assignment")
	}
	for index, role := range roles {
		if role != "follower" && role != "candidate" && role != "leader" {
			return prototypeState{}, fmt.Errorf("invalid role for node %d", index+1)
		}
	}
	terms := parseUintTuple(assignments["currentTerm"])
	commit := parseUintTuple(assignments["commitIndex"])
	votedFor := parseUintTuple(assignments["votedFor"])
	if len(terms) != len(roles) || len(commit) != len(roles) || len(votedFor) != len(roles) {
		return prototypeState{}, fmt.Errorf("invalid node tuple dimensions")
	}
	active, ok := parseNodeSet(assignments["currentActive"], len(roles))
	if !ok {
		return prototypeState{}, fmt.Errorf("invalid currentActive assignment")
	}
	logs, err := parsePrototypeLogs(assignments["log"], len(roles))
	if err != nil {
		return prototypeState{}, err
	}
	for index := range roles {
		if commit[index] > uint64(len(logs[index])) {
			return prototypeState{}, fmt.Errorf("commitIndex exceeds log length for node %d", index+1)
		}
		if votedFor[index] > uint64(len(roles)) {
			return prototypeState{}, fmt.Errorf("votedFor target out of range for node %d", index+1)
		}
	}
	match, err := parsePrototypeMatrix(assignments["matchIndex"], len(roles), "matchIndex")
	if err != nil {
		return prototypeState{}, err
	}
	voteRows, ok := splitTLATuple(assignments["votesGranted"])
	if !ok || len(voteRows) != len(roles) {
		return prototypeState{}, fmt.Errorf("invalid votesGranted assignment")
	}
	votes := make([]map[int]bool, len(roles))
	for index, row := range voteRows {
		votes[index], ok = parseNodeSet(row, len(roles))
		if !ok {
			return prototypeState{}, fmt.Errorf("invalid votesGranted set for node %d", index+1)
		}
	}

	result := prototypeState{
		roles: roles, terms: terms, logs: logs, commit: commit, match: match,
		votes: votes, votedFor: votedFor, active: active,
	}
	storagePresent := false
	for _, name := range []string{"appliedIndex", "snapshotIndex", "firstIndex", "nextIndex", "pendingSnapshot"} {
		if assignments[name] != "" {
			storagePresent = true
			break
		}
	}
	if !storagePresent {
		return result, nil
	}
	for _, name := range []string{"appliedIndex", "snapshotIndex", "snapshotTerm", "firstIndex", "nextIndex", "pendingSnapshot"} {
		if assignments[name] == "" {
			return prototypeState{}, fmt.Errorf("storage state is missing %s assignment", name)
		}
	}
	applied := parseUintTuple(assignments["appliedIndex"])
	snapshot := parseUintTuple(assignments["snapshotIndex"])
	snapshotTerm := parseUintTuple(assignments["snapshotTerm"])
	first := parseUintTuple(assignments["firstIndex"])
	if len(applied) != len(roles) || len(snapshot) != len(roles) ||
		len(snapshotTerm) != len(roles) || len(first) != len(roles) {
		return prototypeState{}, fmt.Errorf("invalid storage tuple dimensions")
	}
	next, err := parsePrototypeMatrix(assignments["nextIndex"], len(roles), "nextIndex")
	if err != nil {
		return prototypeState{}, err
	}
	pending, err := parsePrototypeMatrix(assignments["pendingSnapshot"], len(roles), "pendingSnapshot")
	if err != nil {
		return prototypeState{}, err
	}
	for index := range roles {
		if applied[index] > commit[index] || snapshot[index] > applied[index] || first[index] == 0 ||
			first[index] > snapshot[index]+1 {
			return prototypeState{}, fmt.Errorf("invalid storage boundary for node %d", index+1)
		}
	}
	result.storage = &prototypeStorage{
		applied: applied, snapshot: snapshot, first: first, next: next, pending: pending,
	}
	return result, nil
}

func parsePrototypeLogs(value string, nodeCount int) ([][]prototypeEntry, error) {
	nodes, ok := splitTLATuple(value)
	if !ok || len(nodes) != nodeCount {
		return nil, fmt.Errorf("invalid log assignment")
	}
	result := make([][]prototypeEntry, len(nodes))
	for index, node := range nodes {
		entries, entryOK := splitTLATuple(node)
		if !entryOK {
			return nil, fmt.Errorf("invalid log tuple for node %d", index+1)
		}
		result[index] = make([]prototypeEntry, len(entries))
		for entryIndex, entry := range entries {
			termMatch := entryTermPattern.FindStringSubmatch(entry)
			valueMatch := entryValuePattern.FindStringSubmatch(entry)
			if len(termMatch) != 2 || len(valueMatch) != 2 {
				return nil, fmt.Errorf("invalid log entry %d for node %d", entryIndex+1, index+1)
			}
			term, termErr := strconv.ParseUint(termMatch[1], 10, 64)
			value, valueErr := strconv.ParseUint(valueMatch[1], 10, 64)
			if termErr != nil || valueErr != nil {
				return nil, fmt.Errorf("invalid log entry %d for node %d", entryIndex+1, index+1)
			}
			result[index][entryIndex] = prototypeEntry{term: term, value: value}
		}
	}
	return result, nil
}

func parsePrototypeMatrix(value string, nodeCount int, name string) ([][]uint64, error) {
	rows, ok := splitTLATuple(value)
	if !ok || len(rows) != nodeCount {
		return nil, fmt.Errorf("invalid %s assignment", name)
	}
	result := make([][]uint64, len(rows))
	for index, row := range rows {
		result[index] = parseUintTuple(row)
		if len(result[index]) != nodeCount {
			return nil, fmt.Errorf("invalid %s row for node %d", name, index+1)
		}
	}
	return result, nil
}

func (s prototypeState) semanticKey() (prototypeStateKey, error) {
	n := len(s.roles)
	activeCount := len(s.active)
	quorum := n/2 + 1
	roleClasses := make([]string, 0, n)
	votingClasses := make([]string, 0, n*2)
	snapshotClasses := make([]string, 0, n)
	catchUpByNode := make([][]string, n)
	leaderLagByNode := make([][]string, n)
	catchUpGlobal := make([]string, 0)

	for index := range s.roles {
		lifecycle := "crashed"
		if s.active[index+1] {
			lifecycle = "active"
		}
		roleClasses = append(roleClasses, lifecycle+":"+s.roles[index])
		votingClasses = append(votingClasses, "voted-for:"+s.votedForClass(index))
		if s.roles[index] == "candidate" && s.active[index+1] {
			votingClasses = append(votingClasses, "candidate-votes:"+s.candidateVoteClass(index))
		}
		snapshotClasses = append(snapshotClasses, s.snapshotPhase(index))
	}

	for leader := range s.roles {
		if s.roles[leader] != "leader" || !s.active[leader+1] {
			continue
		}
		for peer := range s.roles {
			if peer == leader {
				continue
			}
			if s.match[leader][peer] > uint64(len(s.logs[leader])) {
				return prototypeStateKey{}, fmt.Errorf("leader matchIndex exceeds log length for node %d", leader+1)
			}
			lag := uint64(len(s.logs[leader])) - s.match[leader][peer]
			leaderLagByNode[leader] = append(leaderLagByNode[leader], lagBucket(lag))
			catchUp := s.catchUpClass(leader, peer, lag)
			catchUpByNode[peer] = append(catchUpByNode[peer], catchUp)
			catchUpGlobal = append(catchUpGlobal, catchUp)
		}
	}
	if len(catchUpGlobal) == 0 {
		catchUpGlobal = append(catchUpGlobal, "none")
	}

	anchor := s.referenceLog()
	maxTerm := maxUint64(s.terms)
	nodeShapes := make([]string, 0, n)
	for index := range s.roles {
		appliedLag := "not-modeled"
		if s.storage != nil {
			appliedLag = lagBucket(s.commit[index] - s.storage.applied[index])
		}
		shape := prototypeNodeShape{
			Lifecycle:       lifecycleClass(s.active[index+1]),
			Role:            s.roles[index],
			TermPosition:    relativeTermClass(s.terms[index], maxTerm),
			LogRelation:     prototypeLogRelation(s.logs[index], anchor),
			CommitLag:       lagBucket(uint64(len(s.logs[index])) - s.commit[index]),
			AppliedLag:      appliedLag,
			SnapshotPhase:   s.snapshotPhase(index),
			VotedFor:        s.votedForClass(index),
			CandidateVotes:  s.candidateVoteClass(index),
			LeaderPeerLags:  countedClassesOrNone(leaderLagByNode[index]),
			InboundCatchUps: countedClassesOrNone(catchUpByNode[index]),
		}
		encoded, err := json.Marshal(shape)
		if err != nil {
			return prototypeStateKey{}, fmt.Errorf("serialize canonical node shape: %w", err)
		}
		nodeShapes = append(nodeShapes, string(encoded))
	}

	logTopology, committedPrefixes := s.logTopology()
	return prototypeStateKey{
		Schema:              SemanticSchemaV2Prototype,
		ClusterSize:         n,
		QuorumAvailable:     activeCount >= quorum,
		RoleTopology:        countedClasses(roleClasses),
		TermTopology:        activeTermTopology(s.terms, s.active),
		LeaderTermPosition:  roleTermPosition("leader", s.roles, s.terms, s.active),
		CandidateTerm:       roleTermPosition("candidate", s.roles, s.terms, s.active),
		LogTopology:         logTopology,
		CommittedPrefixes:   committedPrefixes,
		CatchUpTopology:     countedClasses(catchUpGlobal),
		SnapshotTopology:    countedClasses(snapshotClasses),
		VotingTopology:      countedClasses(votingClasses),
		CanonicalNodeShapes: countedClasses(nodeShapes),
	}, nil
}

func lifecycleClass(active bool) string {
	if active {
		return "active"
	}
	return "crashed"
}

func lagBucket(lag uint64) string {
	switch {
	case lag == 0:
		return "zero"
	case lag == 1:
		return "one"
	case lag <= PrototypeLagSmallMax:
		return "small"
	default:
		return "large"
	}
}

func relativeTermClass(term, maximum uint64) string {
	switch {
	case term == maximum:
		return "max"
	case maximum-term == 1:
		return "behind-one"
	default:
		return "behind-multiple"
	}
}

func activeTermTopology(terms []uint64, active map[int]bool) string {
	values := make([]uint64, 0, len(active))
	for index, term := range terms {
		if active[index+1] {
			values = append(values, term)
		}
	}
	if len(values) == 0 {
		return "no-active-nodes"
	}
	maximum := maxUint64(values)
	minimum := maximum
	maxCount := 0
	allOthersEqual := true
	var other uint64
	otherSet := false
	for _, term := range values {
		if term == maximum {
			maxCount++
			continue
		}
		if !otherSet {
			other, otherSet = term, true
		} else if term != other {
			allOthersEqual = false
		}
		if term < minimum {
			minimum = term
		}
	}
	if maxCount == len(values) {
		return "all-same"
	}
	if maxCount == 1 && allOthersEqual {
		if maximum-minimum == 1 {
			return "one-node-one-term-ahead"
		}
		return "one-node-multiple-terms-ahead"
	}
	return "split-terms"
}

func roleTermPosition(role string, roles []string, terms []uint64, active map[int]bool) string {
	activeTerms := make([]uint64, 0, len(active))
	for index, term := range terms {
		if active[index+1] {
			activeTerms = append(activeTerms, term)
		}
	}
	if len(activeTerms) == 0 {
		return "none"
	}
	maximum := maxUint64(activeTerms)
	count, atMax := 0, 0
	for index, nodeRole := range roles {
		if nodeRole != role || !active[index+1] {
			continue
		}
		count++
		if terms[index] == maximum {
			atMax++
		}
	}
	switch {
	case count == 0:
		return "none"
	case atMax == count:
		return "all-at-max"
	case atMax == 0:
		return "none-at-max"
	default:
		return "some-at-max"
	}
}

func (s prototypeState) referenceLog() []prototypeEntry {
	candidates := make([]int, 0, len(s.logs))
	activeLeaderMax := uint64(0)
	haveActiveLeader := false
	for index, role := range s.roles {
		if role != "leader" || !s.active[index+1] {
			continue
		}
		if !haveActiveLeader || s.terms[index] > activeLeaderMax {
			activeLeaderMax = s.terms[index]
			candidates = candidates[:0]
			haveActiveLeader = true
		}
		if s.terms[index] == activeLeaderMax {
			candidates = append(candidates, index)
		}
	}
	if len(candidates) == 0 {
		for index := range s.logs {
			candidates = append(candidates, index)
		}
	}
	termRanks, valueRanks := prototypeEntryRanks(s.logs)
	sort.Slice(candidates, func(i, j int) bool {
		left, right := s.logs[candidates[i]], s.logs[candidates[j]]
		if len(left) != len(right) {
			return len(left) > len(right)
		}
		return prototypeLogSignature(left, termRanks, valueRanks) <
			prototypeLogSignature(right, termRanks, valueRanks)
	})
	return s.logs[candidates[0]]
}

func prototypeEntryRanks(logs [][]prototypeEntry) (map[uint64]int, map[uint64]int) {
	terms := make(map[uint64]struct{})
	values := make(map[uint64]struct{})
	for _, log := range logs {
		for _, entry := range log {
			terms[entry.term] = struct{}{}
			values[entry.value] = struct{}{}
		}
	}
	return orderedRanks(terms), orderedRanks(values)
}

func orderedRanks(values map[uint64]struct{}) map[uint64]int {
	ordered := make([]uint64, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	result := make(map[uint64]int, len(ordered))
	for index, value := range ordered {
		result[value] = index
	}
	return result
}

func prototypeLogSignature(log []prototypeEntry, termRanks, valueRanks map[uint64]int) string {
	parts := make([]string, len(log))
	for index, entry := range log {
		parts[index] = strconv.Itoa(termRanks[entry.term]) + ":" + strconv.Itoa(valueRanks[entry.value])
	}
	return strings.Join(parts, ",")
}

func prototypeLogRelation(log, reference []prototypeEntry) string {
	common := min(len(log), len(reference))
	for index := 0; index < common; index++ {
		if log[index] != reference[index] {
			return "conflict"
		}
	}
	switch {
	case len(log) == len(reference):
		return "equal"
	case len(log) < len(reference):
		return "prefix"
	default:
		return "extends"
	}
}

func (s prototypeState) logTopology() (string, string) {
	allEqual := true
	allPrefixComparable := true
	committedAgree := true
	for left := 0; left < len(s.logs); left++ {
		for right := left + 1; right < len(s.logs); right++ {
			relation := prototypeLogRelation(s.logs[left], s.logs[right])
			if relation != "equal" {
				allEqual = false
			}
			if relation == "conflict" {
				allPrefixComparable = false
			}
			commonCommit := min(s.commit[left], s.commit[right])
			for index := uint64(0); index < commonCommit; index++ {
				if s.logs[left][index] != s.logs[right][index] {
					committedAgree = false
					break
				}
			}
		}
	}
	committed := "agree"
	if !committedAgree {
		committed = "conflict"
	}
	switch {
	case allEqual:
		return "all-equal", committed
	case !committedAgree:
		return "committed-conflict", committed
	case allPrefixComparable:
		return "prefix-divergence", committed
	default:
		return "uncommitted-suffix-divergence", committed
	}
}

func (s prototypeState) snapshotPhase(index int) string {
	if s.storage == nil {
		return "not-modeled"
	}
	snapshot, first := s.storage.snapshot[index], s.storage.first[index]
	switch {
	case snapshot == 0:
		return "none"
	case first == 1:
		return "created-uncompacted"
	case first == snapshot+1:
		return "compacted-retain-zero"
	default:
		return "compacted-retaining"
	}
}

func (s prototypeState) catchUpClass(leader, peer int, lag uint64) string {
	if s.storage != nil {
		if s.storage.pending[leader][peer] > 0 {
			return "snapshot-pending"
		}
		if s.storage.next[leader][peer] < s.storage.first[leader] {
			return "snapshot-required"
		}
	}
	if lag == 0 {
		return "caught-up"
	}
	return "append-" + lagBucket(lag)
}

func (s prototypeState) candidateVoteClass(index int) string {
	if s.roles[index] != "candidate" || !s.active[index+1] {
		return "not-active-candidate"
	}
	count := len(s.votes[index])
	quorum := len(s.roles)/2 + 1
	switch {
	case count >= quorum:
		return "quorum-reached"
	case count == 1 && s.votes[index][index+1]:
		return "self-only"
	case count == quorum-1:
		return "one-short-of-quorum"
	default:
		return "minority"
	}
}

func (s prototypeState) votedForClass(index int) string {
	target := s.votedFor[index]
	switch {
	case target == 0:
		return "none"
	case target == uint64(index+1):
		return "self"
	}
	targetIndex := int(target - 1)
	switch {
	case s.roles[targetIndex] == "candidate" && s.terms[targetIndex] == s.terms[index]:
		return "same-term-candidate"
	case s.roles[targetIndex] == "leader" && s.terms[targetIndex] == s.terms[index]:
		return "same-term-leader"
	case s.terms[targetIndex] == s.terms[index]:
		return "same-term-peer"
	default:
		return "other-term-peer"
	}
}

func countedClassesOrNone(classes []string) []prototypeClassCount {
	if len(classes) == 0 {
		return []prototypeClassCount{{Class: "none", Count: 1}}
	}
	return countedClasses(classes)
}

func countedClasses(classes []string) []prototypeClassCount {
	counts := make(map[string]int, len(classes))
	for _, class := range classes {
		counts[class]++
	}
	names := make([]string, 0, len(counts))
	for class := range counts {
		names = append(names, class)
	}
	sort.Strings(names)
	result := make([]prototypeClassCount, len(names))
	for index, class := range names {
		result[index] = prototypeClassCount{Class: class, Count: counts[class]}
	}
	return result
}

func maxUint64(values []uint64) uint64 {
	var result uint64
	for _, value := range values {
		if value > result {
			result = value
		}
	}
	return result
}
