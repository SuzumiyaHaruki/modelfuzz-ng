package raft

import (
	"crypto/sha256"
	"encoding/binary"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

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
func ProjectCoverage(states []model.State, events []model.Event) CoverageProjection {
	projected := make([]string, len(states))
	stateSet := make(map[int64]struct{}, len(states))
	for index, state := range states {
		projected[index] = projectState(state)
		stateSet[coverageKey(projected[index])] = struct{}{}
	}
	transitionSet := make(map[int64]struct{}, min(len(events), max(0, len(states)-1)))
	for index := 0; index < len(events) && index+1 < len(projected); index++ {
		transitionSet[coverageKey(projected[index]+"\x00"+events[index].Name+"\x00"+projected[index+1])] = struct{}{}
	}
	return CoverageProjection{
		StateKeys:      sortedCoverageKeys(stateSet),
		TransitionKeys: sortedCoverageKeys(transitionSet),
	}
}

func projectState(state model.State) string {
	if strings.TrimSpace(state.Text) == "" {
		return "raw:" + strconv.FormatInt(state.Key, 10)
	}
	assignments := stateAssignments(state.Text)
	if assignments["currentActive"] == "" || assignments["state"] == "" || assignments["currentTerm"] == "" || assignments["log"] == "" {
		return normalizeRelativeTerms(strings.Join(strings.Fields(state.Text), " "))
	}
	terms := termRanks("currentTerm = " + assignments["currentTerm"] + " " + assignments["log"])
	logShape, logLengths, ok := semanticLog(assignments["log"], terms)
	if !ok {
		return normalizeRelativeTerms(strings.Join(strings.Fields(state.Text), " "))
	}
	features := []string{
		"active=" + compact(assignments["currentActive"]),
		"roles=" + compact(assignments["state"]),
		"terms=" + replaceTerms(assignments["currentTerm"], terms),
		"log=" + logShape,
		"commit=" + commitShape(assignments["commitIndex"], logLengths),
		"replication=" + replicationShape(assignments["matchIndex"], logLengths),
		"votes=" + voteShape(assignments["votesGranted"]),
		"votedFor=" + compact(assignments["votedFor"]),
	}
	return strings.Join(features, "|")
}

func normalizeRelativeTerms(text string) string {
	terms := termRanks(text)
	text = currentTermPattern.ReplaceAllStringFunc(text, func(value string) string {
		return replaceTerms(value, terms)
	})
	return entryTermPattern.ReplaceAllStringFunc(text, func(value string) string {
		match := entryTermPattern.FindStringSubmatch(value)
		term, _ := strconv.ParseUint(match[1], 10, 64)
		return "term |-> " + strconv.FormatUint(terms[term], 10)
	})
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

func semanticLog(value string, ranks map[uint64]uint64) (string, []int, bool) {
	nodes, ok := splitTLATuple(value)
	if !ok {
		return "", nil, false
	}
	shapes := make([]string, len(nodes))
	lengths := make([]int, len(nodes))
	for index, node := range nodes {
		entries, entryOK := splitTLATuple(node)
		if !entryOK {
			return "", nil, false
		}
		lengths[index] = len(entries)
		parts := make([]string, 0, len(entries))
		for _, entry := range entries {
			termMatch := entryTermPattern.FindStringSubmatch(entry)
			valueMatch := entryValuePattern.FindStringSubmatch(entry)
			if len(termMatch) != 2 || len(valueMatch) != 2 {
				return "", nil, false
			}
			term, _ := strconv.ParseUint(termMatch[1], 10, 64)
			parts = append(parts, strconv.FormatUint(ranks[term], 10)+":"+valueMatch[1])
		}
		shapes[index] = "[" + strings.Join(parts, ",") + "]"
	}
	return strings.Join(shapes, ";"), lengths, true
}

func commitShape(value string, logLengths []int) string {
	commits := parseUintTuple(value)
	if len(commits) != len(logLengths) {
		return compact(value)
	}
	result := make([]string, len(commits))
	for index, commit := range commits {
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
	return strings.Join(result, ",")
}

func replicationShape(value string, logLengths []int) string {
	rows, ok := splitTLATuple(value)
	if !ok || len(rows) != len(logLengths) {
		return compact(value)
	}
	result := make([]string, len(rows))
	for rowIndex, row := range rows {
		matches := parseUintTuple(row)
		buckets := make([]string, len(matches))
		for index, match := range matches {
			lag := logLengths[rowIndex] - int(match)
			switch {
			case match == 0:
				buckets[index] = "zero"
			case lag <= 0:
				buckets[index] = "full"
			case lag == 1:
				buckets[index] = "lag1"
			default:
				buckets[index] = "lag2+"
			}
		}
		result[rowIndex] = strings.Join(buckets, ",")
	}
	return strings.Join(result, ";")
}

func voteShape(value string) string {
	sets, ok := splitTLATuple(value)
	if !ok {
		return compact(value)
	}
	result := make([]string, len(sets))
	for index, set := range sets {
		inside := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(set), "{"), "}"))
		switch {
		case inside == "":
			result[index] = "0"
		case !strings.Contains(inside, ","):
			result[index] = "1"
		default:
			result[index] = "2+"
		}
	}
	return strings.Join(result, ",")
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
