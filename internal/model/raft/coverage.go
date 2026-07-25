package raft

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

const SemanticSchemaVersion = "raft-coverage-v1"

var (
	currentTermPattern = regexp.MustCompile(`currentTerm\s*=\s*<<([^>]*)>>`)
	entryTermPattern   = regexp.MustCompile(`term\s*\|->\s*([0-9]+)`)
	entryValuePattern  = regexp.MustCompile(`value\s*\|->\s*([0-9]+)`)
	numberPattern      = regexp.MustCompile(`[0-9]+`)
)

// CoverageProjection 是独立于 TLC 原始 fingerprint 的语义覆盖。StateKeys
// 归一化绝对 term 后保留节点身份、角色、活动集合、日志、提交与复制关系；
// TransitionKeys 进一步区分模型动作类别和前后语义状态。
type CoverageProjection struct {
	StateKeys      []int64
	TransitionKeys []int64
}

// ProjectCoverage 将 controlled TLC 的初始状态和逐事件后继状态投影为稳定键。
// TLC 返回 states[0] 为 Init，states[i+1] 为 events[i] 的后继。
func ProjectCoverage(states []model.State, events []model.Event) (CoverageProjection, error) {
	projected := make([]string, len(states))
	stateSet := make(map[int64]struct{}, len(states))
	for index, state := range states {
		var err error
		projected[index], err = projectState(state)
		if err != nil {
			return CoverageProjection{}, fmt.Errorf("state %d: %w", index, err)
		}
		stateSet[coverageKey(projected[index])] = struct{}{}
	}
	transitionSet := make(map[int64]struct{}, min(len(events), max(0, len(states)-1)))
	for index := 0; index < len(events) && index+1 < len(projected); index++ {
		transitionSet[coverageKey(projected[index]+"\x00"+events[index].Name+"\x00"+projected[index+1])] = struct{}{}
	}
	return CoverageProjection{
		StateKeys:      sortedCoverageKeys(stateSet),
		TransitionKeys: sortedCoverageKeys(transitionSet),
	}, nil
}

func projectState(state model.State) (string, error) {
	if strings.TrimSpace(state.Text) == "" {
		return "", fmt.Errorf("state text is empty (raw key %d)", state.Key)
	}
	assignments := stateAssignments(state.Text)
	for _, name := range []string{"currentActive", "state", "currentTerm", "log", "commitIndex", "matchIndex", "votesGranted", "votedFor"} {
		if assignments[name] == "" {
			return "", fmt.Errorf("missing %s assignment", name)
		}
	}
	currentTerms := parseUintTuple(assignments["currentTerm"])
	if len(currentTerms) == 0 {
		return "", fmt.Errorf("invalid currentTerm assignment")
	}
	terms := termRanks("currentTerm = " + assignments["currentTerm"] + " " + assignments["log"])
	logShape, logLengths, logTerms, ok := semanticLog(assignments["log"], terms)
	if !ok {
		return "", fmt.Errorf("invalid log assignment")
	}
	roles, ok := parseStringTuple(assignments["state"])
	if !ok || len(roles) != len(logLengths) || len(currentTerms) != len(logLengths) {
		return "", fmt.Errorf("invalid state assignment")
	}
	for index, role := range roles {
		if role != "follower" && role != "candidate" && role != "leader" {
			return "", fmt.Errorf("invalid role for node %d", index+1)
		}
	}
	active, ok := parseNodeSet(assignments["currentActive"], len(roles))
	if !ok {
		return "", fmt.Errorf("invalid currentActive assignment")
	}
	commit, err := commitShape(assignments["commitIndex"], logLengths)
	if err != nil {
		return "", err
	}
	replication, err := replicationShape(assignments["matchIndex"], roles, active, logLengths)
	if err != nil {
		return "", err
	}
	votes, err := voteShape(assignments["votesGranted"], roles, active)
	if err != nil {
		return "", err
	}
	votedFor, err := votedForShape(assignments["votedFor"], roles, currentTerms)
	if err != nil {
		return "", err
	}
	features := []string{
		"schema=" + SemanticSchemaVersion,
		"active=" + compact(assignments["currentActive"]),
		"roles=" + compact(assignments["state"]),
		"terms=" + replaceTerms(assignments["currentTerm"], terms),
		"log=" + logShape,
		"commit=" + commit,
		"replication=" + replication,
		"votes=" + votes,
		"votedFor=" + votedFor,
	}
	storagePresent := false
	for _, name := range []string{"appliedIndex", "snapshotIndex", "snapshotTerm", "firstIndex", "pendingSnapshot"} {
		if assignments[name] != "" {
			storagePresent = true
			break
		}
	}
	if storagePresent {
		for _, name := range []string{"appliedIndex", "snapshotIndex", "snapshotTerm", "firstIndex", "pendingSnapshot", "nextIndex"} {
			if assignments[name] == "" {
				return "", fmt.Errorf("storage state is missing %s assignment", name)
			}
		}
		storage, err := storageShape(assignments, roles, active, logLengths, logTerms)
		if err != nil {
			return "", err
		}
		features = append(features, "storage="+storage)
	}
	return strings.Join(features, "|"), nil
}

func termRanks(text string) map[uint64]uint64 {
	terms := make(map[uint64]struct{})
	for _, match := range currentTermPattern.FindAllStringSubmatch(text, -1) {
		for _, value := range numberPattern.FindAllString(match[1], -1) {
			term, _ := strconv.ParseUint(value, 10, 64)
			terms[term] = struct{}{}
		}
	}
	for _, match := range entryTermPattern.FindAllStringSubmatch(text, -1) {
		term, _ := strconv.ParseUint(match[1], 10, 64)
		terms[term] = struct{}{}
	}
	ordered := make([]uint64, 0, len(terms))
	for term := range terms {
		ordered = append(ordered, term)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	ranks := make(map[uint64]uint64, len(ordered))
	next := uint64(1)
	for _, term := range ordered {
		if term == 0 {
			ranks[term] = 0
			continue
		}
		ranks[term] = next
		next++
	}
	return ranks
}

func replaceTerms(value string, ranks map[uint64]uint64) string {
	return numberPattern.ReplaceAllStringFunc(compact(value), func(number string) string {
		term, _ := strconv.ParseUint(number, 10, 64)
		return strconv.FormatUint(ranks[term], 10)
	})
}

func stateAssignments(text string) map[string]string {
	result := make(map[string]string)
	current := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `/\ `) {
			parts := strings.SplitN(strings.TrimPrefix(line, `/\ `), " = ", 2)
			if len(parts) != 2 {
				current = ""
				continue
			}
			current = parts[0]
			result[current] = parts[1]
		} else if current != "" && line != "" {
			result[current] += " " + line
		}
	}
	return result
}

func semanticLog(value string, ranks map[uint64]uint64) (string, []int, [][]uint64, bool) {
	nodes, ok := splitTLATuple(value)
	if !ok {
		return "", nil, nil, false
	}
	shapes := make([]string, len(nodes))
	lengths := make([]int, len(nodes))
	terms := make([][]uint64, len(nodes))
	valueRanks := make(map[uint64]uint64)
	nextValueRank := uint64(1)
	for index, node := range nodes {
		entries, entryOK := splitTLATuple(node)
		if !entryOK {
			return "", nil, nil, false
		}
		lengths[index] = len(entries)
		terms[index] = make([]uint64, 0, len(entries))
		parts := make([]string, 0, len(entries))
		for _, entry := range entries {
			termMatch := entryTermPattern.FindStringSubmatch(entry)
			valueMatch := entryValuePattern.FindStringSubmatch(entry)
			if len(termMatch) != 2 || len(valueMatch) != 2 {
				return "", nil, nil, false
			}
			term, termErr := strconv.ParseUint(termMatch[1], 10, 64)
			entryValue, valueErr := strconv.ParseUint(valueMatch[1], 10, 64)
			if termErr != nil || valueErr != nil {
				return "", nil, nil, false
			}
			terms[index] = append(terms[index], term)
			valueClass := "nil"
			if entryValue != 0 {
				rank, seen := valueRanks[entryValue]
				if !seen {
					rank = nextValueRank
					valueRanks[entryValue] = rank
					nextValueRank++
				}
				valueClass = "v" + strconv.FormatUint(rank, 10)
			}
			parts = append(parts, strconv.FormatUint(ranks[term], 10)+":"+valueClass)
		}
		shapes[index] = "[" + strings.Join(parts, ",") + "]"
	}
	return strings.Join(shapes, ";"), lengths, terms, true
}

func storageShape(assignments map[string]string, roles []string, active map[int]bool, logLengths []int, logTerms [][]uint64) (string, error) {
	applied := parseUintTuple(assignments["appliedIndex"])
	snapshots := parseUintTuple(assignments["snapshotIndex"])
	snapshotTerms := parseUintTuple(assignments["snapshotTerm"])
	first := parseUintTuple(assignments["firstIndex"])
	commit := parseUintTuple(assignments["commitIndex"])
	nextRows, nextOK := splitTLATuple(assignments["nextIndex"])
	matchRows, matchOK := splitTLATuple(assignments["matchIndex"])
	pendingRows, pendingOK := splitTLATuple(assignments["pendingSnapshot"])
	n := len(logLengths)
	if len(applied) != n || len(snapshots) != n || len(snapshotTerms) != n || len(first) != n || len(commit) != n ||
		!nextOK || !matchOK || !pendingOK || len(nextRows) != n || len(matchRows) != n || len(pendingRows) != n {
		return "", fmt.Errorf("invalid storage tuple dimensions")
	}

	nodes := make([]string, n)
	progress := make([]string, 0)
	for index := 0; index < n; index++ {
		if applied[index] > uint64(logLengths[index]) || snapshots[index] > applied[index] || first[index] == 0 {
			return "", fmt.Errorf("invalid storage boundary for node %d", index+1)
		}
		appliedClass := "behind-commit"
		if applied[index] > commit[index] {
			return "", fmt.Errorf("invalid commit/applied relationship for node %d", index+1)
		}
		switch {
		case applied[index] == 0:
			appliedClass = "zero"
		case applied[index] == snapshots[index] && applied[index] == commit[index]:
			appliedClass = "at-snapshot-commit"
		case applied[index] == snapshots[index]:
			appliedClass = "at-snapshot"
		case applied[index] == commit[index]:
			appliedClass = "at-commit"
		}
		snapshotClass := "behind-applied"
		termClass := "matches-log"
		switch snapshots[index] {
		case 0:
			snapshotClass = "none"
			if snapshotTerms[index] != 0 {
				termClass = "mismatch"
			}
		case applied[index]:
			snapshotClass = "at-applied"
		}
		if snapshots[index] > 0 {
			snapshotPosition := int(snapshots[index] - 1)
			if snapshotPosition >= len(logTerms[index]) || logTerms[index][snapshotPosition] != snapshotTerms[index] {
				termClass = "mismatch"
			}
		}
		firstClass := "retained2+"
		switch {
		case first[index] == 1:
			firstClass = "uncompacted"
		case first[index] == snapshots[index]+1:
			firstClass = "retain0"
		case first[index] == snapshots[index]:
			firstClass = "retain1"
		case first[index] > snapshots[index]+1:
			firstClass = "beyond-snapshot"
		}
		nodes[index] = strings.Join([]string{appliedClass, snapshotClass, termClass, firstClass}, "/")

		if roles[index] != "leader" || !active[index+1] {
			continue
		}
		next := parseUintTuple(nextRows[index])
		matches := parseUintTuple(matchRows[index])
		pending := parseUintTuple(pendingRows[index])
		if len(next) != n || len(matches) != n || len(pending) != n {
			return "", fmt.Errorf("invalid leader progress row for node %d", index+1)
		}
		for peer := 0; peer < n; peer++ {
			if peer == index {
				continue
			}
			nextClass := "available"
			switch {
			case next[peer] < first[index]:
				nextClass = "below-first"
			case next[peer] == uint64(logLengths[index]+1):
				nextClass = "at-tip"
			case next[peer] > uint64(logLengths[index]+1):
				nextClass = "beyond-tip"
			}
			pendingClass := "none"
			if pending[peer] > 0 {
				pendingClass = "ahead-match"
				if pending[peer] <= matches[peer] {
					pendingClass = "matched"
				}
				if next[peer] <= pending[peer] {
					pendingClass += ":next-not-after"
				} else {
					pendingClass += ":next-after"
				}
			}
			progress = append(progress, fmt.Sprintf("%d>%d:%s:%s", index+1, peer+1, nextClass, pendingClass))
		}
	}
	if len(progress) == 0 {
		progress = append(progress, "none")
	}
	return "nodes=" + strings.Join(nodes, ",") + ";progress=" + strings.Join(progress, ","), nil
}

func parseStringTuple(value string) ([]string, bool) {
	parts, ok := splitTLATuple(value)
	if !ok {
		return nil, false
	}
	for index := range parts {
		parts[index] = strings.Trim(strings.TrimSpace(parts[index]), `"`)
	}
	return parts, true
}

func parseNodeSet(value string, nodeCount int) (map[int]bool, bool) {
	result := make(map[int]bool)
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return nil, false
	}
	inside := strings.TrimSpace(value[1 : len(value)-1])
	if inside == "" {
		return result, true
	}
	for _, number := range strings.Split(inside, ",") {
		parsed, err := strconv.Atoi(strings.TrimSpace(number))
		if err != nil || parsed < 1 || parsed > nodeCount || result[parsed] {
			return nil, false
		}
		result[parsed] = true
	}
	return result, true
}

func commitShape(value string, logLengths []int) (string, error) {
	commits := parseUintTuple(value)
	if len(commits) != len(logLengths) {
		return "", fmt.Errorf("invalid commitIndex assignment")
	}
	result := make([]string, len(commits))
	for index, commit := range commits {
		if commit > uint64(logLengths[index]) {
			return "", fmt.Errorf("commitIndex exceeds log length for node %d", index+1)
		}
		lag := logLengths[index] - int(commit)
		switch {
		case commit == 0:
			result[index] = "zero"
		case lag <= 0:
			result[index] = "full"
		case lag == 1:
			result[index] = "lag1"
		default:
			result[index] = "lag2+"
		}
	}
	return strings.Join(result, ","), nil
}

func replicationShape(value string, roles []string, active map[int]bool, logLengths []int) (string, error) {
	rows, ok := splitTLATuple(value)
	if !ok || len(rows) != len(logLengths) {
		return "", fmt.Errorf("invalid matchIndex assignment")
	}
	result := make([]string, 0)
	for rowIndex, row := range rows {
		matches := parseUintTuple(row)
		if len(matches) != len(logLengths) {
			return "", fmt.Errorf("invalid matchIndex row for node %d", rowIndex+1)
		}
		if roles[rowIndex] != "leader" || !active[rowIndex+1] {
			continue
		}
		buckets := make([]string, 0, len(matches)-1)
		for index, match := range matches {
			if index == rowIndex {
				continue
			}
			if match > uint64(logLengths[rowIndex]) {
				return "", fmt.Errorf("leader matchIndex exceeds log length for node %d", rowIndex+1)
			}
			lag := logLengths[rowIndex] - int(match)
			switch {
			case match == 0:
				buckets = append(buckets, fmt.Sprintf("%d:zero", index+1))
			case lag <= 0:
				buckets = append(buckets, fmt.Sprintf("%d:full", index+1))
			case lag == 1:
				buckets = append(buckets, fmt.Sprintf("%d:lag1", index+1))
			default:
				buckets = append(buckets, fmt.Sprintf("%d:lag2+", index+1))
			}
		}
		result = append(result, fmt.Sprintf("%d>%s", rowIndex+1, strings.Join(buckets, ",")))
	}
	if len(result) == 0 {
		return "none", nil
	}
	return strings.Join(result, ";"), nil
}

func voteShape(value string, roles []string, active map[int]bool) (string, error) {
	sets, ok := splitTLATuple(value)
	if !ok || len(sets) != len(roles) {
		return "", fmt.Errorf("invalid votesGranted assignment")
	}
	result := make([]string, 0)
	quorum := len(roles)/2 + 1
	for index, set := range sets {
		votes, setOK := parseNodeSet(set, len(roles))
		if !setOK {
			return "", fmt.Errorf("invalid votesGranted set for node %d", index+1)
		}
		if roles[index] != "candidate" || !active[index+1] {
			continue
		}
		class := "minority"
		switch {
		case len(votes) >= quorum:
			class = "quorum-reached"
		case len(votes) == 1 && votes[index+1]:
			class = "self-only"
		case len(votes) == quorum-1:
			class = "one-short-of-quorum"
		}
		result = append(result, fmt.Sprintf("%d:%s", index+1, class))
	}
	if len(result) == 0 {
		return "none", nil
	}
	return strings.Join(result, ","), nil
}

func votedForShape(value string, roles []string, currentTerms []uint64) (string, error) {
	votes := parseUintTuple(value)
	if len(votes) != len(roles) || len(currentTerms) != len(roles) {
		return "", fmt.Errorf("invalid votedFor assignment")
	}
	result := make([]string, len(votes))
	for index, target := range votes {
		switch {
		case target == 0:
			result[index] = "none"
		case target > uint64(len(roles)):
			return "", fmt.Errorf("votedFor target out of range for node %d", index+1)
		case target == uint64(index+1):
			result[index] = "self"
		case roles[target-1] == "candidate" && currentTerms[target-1] == currentTerms[index]:
			result[index] = "current-candidate"
		default:
			result[index] = "other"
		}
	}
	return strings.Join(result, ","), nil
}

func parseUintTuple(value string) []uint64 {
	parts, ok := splitTLATuple(value)
	if !ok {
		return nil
	}
	result := make([]uint64, len(parts))
	for index, part := range parts {
		parsed, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return nil
		}
		result[index] = parsed
	}
	return result
}

func splitTLATuple(value string) ([]string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "<<") || !strings.HasSuffix(value, ">>") {
		return nil, false
	}
	inside := strings.TrimSpace(value[2 : len(value)-2])
	if inside == "" {
		return []string{}, true
	}
	parts := make([]string, 0)
	start := 0
	angle, square, brace := 0, 0, 0
	for index := 0; index < len(inside); index++ {
		switch {
		case index+1 < len(inside) && inside[index:index+2] == "<<":
			angle++
			index++
		case index+1 < len(inside) && inside[index:index+2] == ">>":
			angle--
			index++
		case inside[index] == '[':
			square++
		case inside[index] == ']':
			square--
		case inside[index] == '{':
			brace++
		case inside[index] == '}':
			brace--
		case inside[index] == ',' && angle == 0 && square == 0 && brace == 0:
			parts = append(parts, strings.TrimSpace(inside[start:index]))
			start = index + 1
		}
	}
	if angle != 0 || square != 0 || brace != 0 {
		return nil, false
	}
	parts = append(parts, strings.TrimSpace(inside[start:]))
	return parts, true
}

func compact(value string) string {
	return strings.Join(strings.Fields(value), "")
}

func coverageKey(value string) int64 {
	digest := sha256.Sum256([]byte(value))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func sortedCoverageKeys(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
