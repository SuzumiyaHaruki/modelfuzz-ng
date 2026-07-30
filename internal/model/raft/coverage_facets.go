package raft

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

const CoverageFacetsSchemaVersion = "raft-coverage-facets-v1-prototype"

type FacetContext struct {
	NetworkPartition     *core.NetworkPartition
	JustHealed           bool
	DelayedMessages      bool
	RestartedNodes       []core.NodeID
	RecoveringNodes      []core.NodeID
	RecoveredThisFrame   int
	RecoveryMode         string
	MessageTermRelation  string
	SnapshotOutcome      string
	SnapshotRetryPending bool
}

type ElectionFacet struct {
	ActiveRoleTopology    []prototypeClassCount `json:"active_role_topology"`
	ActiveNodes           string                `json:"active_nodes"`
	CrashedNodes          string                `json:"crashed_nodes"`
	LeaderMode            string                `json:"leader_mode"`
	CandidateCount        string                `json:"candidate_count"`
	TermTopology          string                `json:"term_topology"`
	LeaderTermPosition    string                `json:"leader_term_position"`
	CandidateTermPosition string                `json:"candidate_term_position"`
	QuorumAvailable       bool                  `json:"quorum_available"`
	CandidateVotes        []prototypeClassCount `json:"candidate_votes"`
	VotedFor              []prototypeClassCount `json:"voted_for"`
}

type ReplicationFacet struct {
	LeaderMode          string                `json:"leader_mode"`
	LogTopology         string                `json:"log_topology"`
	CommittedPrefixes   string                `json:"committed_prefixes"`
	LaggingFollowers    string                `json:"lagging_followers"`
	ReplicationLags     []prototypeClassCount `json:"replication_lags"`
	CommitLags          []prototypeClassCount `json:"commit_lags"`
	CatchUpTopology     []prototypeClassCount `json:"catch_up_topology"`
	AppendCatchUp       bool                  `json:"append_catch_up"`
	SnapshotRequired    bool                  `json:"snapshot_required"`
	UncommittedConflict bool                  `json:"uncommitted_suffix_conflict"`
	CommittedConflict   bool                  `json:"committed_prefix_conflict"`
}

type SnapshotFacet struct {
	Mode         string                `json:"mode"`
	NodePhases   []prototypeClassCount `json:"node_phases"`
	Outcome      string                `json:"outcome"`
	RetryPending bool                  `json:"retry_pending"`
}

type RecoveryFacet struct {
	Phase               string `json:"phase"`
	RecoveringNodes     string `json:"recovering_nodes"`
	RecoveryMode        string `json:"recovery_mode"`
	MessageTermRelation string `json:"message_term_relation"`
}

type NetworkFacet struct {
	Mode                   string                `json:"mode"`
	GroupShapes            []prototypeClassCount `json:"group_shapes"`
	ConnectedQuorum        bool                  `json:"connected_quorum"`
	LeaderPlacement        string                `json:"leader_placement"`
	JustHealed             bool                  `json:"just_healed"`
	DelayedMessagesPending bool                  `json:"delayed_messages_pending"`
}

type FacetInteraction struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Key   int64  `json:"key"`
}

type CoverageFacetProjection struct {
	Schema         string             `json:"schema"`
	Election       ElectionFacet      `json:"election"`
	ElectionKey    int64              `json:"election_key"`
	Replication    ReplicationFacet   `json:"replication"`
	ReplicationKey int64              `json:"replication_key"`
	Snapshot       SnapshotFacet      `json:"snapshot"`
	SnapshotKey    int64              `json:"snapshot_key"`
	Recovery       RecoveryFacet      `json:"recovery"`
	RecoveryKey    int64              `json:"recovery_key"`
	Network        NetworkFacet       `json:"network"`
	NetworkKey     int64              `json:"network_key"`
	Interactions   []FacetInteraction `json:"interactions"`
}

func ProjectCoverageFacets(state model.State, context FacetContext) (CoverageFacetProjection, error) {
	parsed, err := parsePrototypeState(state)
	if err != nil {
		return CoverageFacetProjection{}, err
	}
	full, err := parsed.semanticKey()
	if err != nil {
		return CoverageFacetProjection{}, err
	}
	election := parsed.electionFacet(full)
	replication := parsed.replicationFacet(full)
	snapshot := parsed.snapshotFacet(full, context)
	recovery := parsed.recoveryFacet(context)
	network, err := parsed.networkFacet(context)
	if err != nil {
		return CoverageFacetProjection{}, err
	}
	projection := CoverageFacetProjection{
		Schema:      CoverageFacetsSchemaVersion,
		Election:    election,
		Replication: replication,
		Snapshot:    snapshot,
		Recovery:    recovery,
		Network:     network,
	}
	if projection.ElectionKey, err = facetKey("election", election); err != nil {
		return CoverageFacetProjection{}, err
	}
	if projection.ReplicationKey, err = facetKey("replication", replication); err != nil {
		return CoverageFacetProjection{}, err
	}
	if projection.SnapshotKey, err = facetKey("snapshot", snapshot); err != nil {
		return CoverageFacetProjection{}, err
	}
	if projection.RecoveryKey, err = facetKey("recovery", recovery); err != nil {
		return CoverageFacetProjection{}, err
	}
	if projection.NetworkKey, err = facetKey("network", network); err != nil {
		return CoverageFacetProjection{}, err
	}
	interactions := []struct {
		name  string
		value any
	}{
		{"election_network", struct {
			LeaderMode      string `json:"leader_mode"`
			CandidateVotes  string `json:"candidate_votes"`
			NetworkMode     string `json:"network_mode"`
			ConnectedQuorum bool   `json:"connected_quorum"`
		}{election.LeaderMode, dominantClass(election.CandidateVotes), network.Mode, network.ConnectedQuorum}},
		{"replication_network", struct {
			LogTopology      string `json:"log_topology"`
			CatchUp          string `json:"catch_up"`
			NetworkMode      string `json:"network_mode"`
			DelayedAfterHeal bool   `json:"delayed_after_heal"`
		}{replication.LogTopology, dominantClass(replication.CatchUpTopology), network.Mode, network.DelayedMessagesPending}},
		{"snapshot_recovery", struct {
			SnapshotMode    string `json:"snapshot_mode"`
			SnapshotOutcome string `json:"snapshot_outcome"`
			RecoveryPhase   string `json:"recovery_phase"`
		}{snapshot.Mode, snapshot.Outcome, recovery.Phase}},
		{"recovery_term_relation", struct {
			RecoveryPhase       string `json:"recovery_phase"`
			MessageTermRelation string `json:"message_term_relation"`
			TermTopology        string `json:"term_topology"`
		}{recovery.Phase, recovery.MessageTermRelation, election.TermTopology}},
	}
	projection.Interactions = make([]FacetInteraction, 0, len(interactions))
	for _, interaction := range interactions {
		serialized, serializeErr := facetSerialization(interaction.name, interaction.value)
		if serializeErr != nil {
			return CoverageFacetProjection{}, serializeErr
		}
		projection.Interactions = append(projection.Interactions, FacetInteraction{
			Name: interaction.name, Value: serialized, Key: coverageKey(serialized),
		})
	}
	return projection, nil
}

func SerializeCoverageFacet(name string, value any) (string, error) {
	return facetSerialization(name, value)
}

// SerializeCoverageFacetProjection returns the readable values corresponding
// to the five independent keys in a projection. It is shared by offline
// factorization and online coverage observations.
func SerializeCoverageFacetProjection(projection CoverageFacetProjection) (map[string]string, error) {
	values := []struct {
		name  string
		value any
	}{
		{"election", projection.Election},
		{"replication", projection.Replication},
		{"snapshot", projection.Snapshot},
		{"recovery", projection.Recovery},
		{"network", projection.Network},
	}
	result := make(map[string]string, len(values))
	for _, value := range values {
		serialized, err := SerializeCoverageFacet(value.name, value.value)
		if err != nil {
			return nil, err
		}
		result[value.name] = serialized
	}
	return result, nil
}

func facetKey(name string, value any) (int64, error) {
	serialized, err := facetSerialization(name, value)
	if err != nil {
		return 0, err
	}
	return coverageKey(serialized), nil
}

func facetSerialization(name string, value any) (string, error) {
	encoded, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Facet  string `json:"facet"`
		Value  any    `json:"value"`
	}{CoverageFacetsSchemaVersion, name, value})
	if err != nil {
		return "", fmt.Errorf("serialize %s facet %s: %w", CoverageFacetsSchemaVersion, name, err)
	}
	return string(encoded), nil
}

func (s prototypeState) electionFacet(full prototypeStateKey) ElectionFacet {
	activeRoles := make([]string, 0, len(s.roles))
	leaders, candidates := 0, 0
	for index, role := range s.roles {
		if !s.active[index+1] {
			continue
		}
		activeRoles = append(activeRoles, role)
		switch role {
		case "leader":
			leaders++
		case "candidate":
			candidates++
		}
	}
	candidateVotes := filterClasses(full.VotingTopology, "candidate-votes:")
	votedFor := filterClasses(full.VotingTopology, "voted-for:")
	return ElectionFacet{
		ActiveRoleTopology:    countedClasses(activeRoles),
		ActiveNodes:           countClass(len(s.active)),
		CrashedNodes:          countClass(len(s.roles) - len(s.active)),
		LeaderMode:            countClass(leaders),
		CandidateCount:        countClass(candidates),
		TermTopology:          full.TermTopology,
		LeaderTermPosition:    full.LeaderTermPosition,
		CandidateTermPosition: full.CandidateTerm,
		QuorumAvailable:       full.QuorumAvailable,
		CandidateVotes:        candidateVotes,
		VotedFor:              votedFor,
	}
}

func (s prototypeState) replicationFacet(full prototypeStateKey) ReplicationFacet {
	leaders := 0
	for index, role := range s.roles {
		if role == "leader" && s.active[index+1] {
			leaders++
		}
	}
	lagClasses := make([]string, 0)
	commitClasses := make([]string, 0)
	lagging := 0
	for _, class := range full.CanonicalNodeShapes {
		var shape prototypeNodeShape
		if err := json.Unmarshal([]byte(class.Class), &shape); err != nil {
			continue
		}
		for count := 0; count < class.Count; count++ {
			commitClasses = append(commitClasses, shape.CommitLag)
		}
		for _, lag := range shape.LeaderPeerLags {
			for count := 0; count < lag.Count*class.Count; count++ {
				lagClasses = append(lagClasses, lag.Class)
				if lag.Class != "zero" && lag.Class != "none" {
					lagging++
				}
			}
		}
	}
	appendCatchUp := containsClassPrefix(full.CatchUpTopology, "append-")
	snapshotRequired := containsClass(full.CatchUpTopology, "snapshot-required") ||
		containsClass(full.CatchUpTopology, "snapshot-pending")
	return ReplicationFacet{
		LeaderMode:          countClass(leaders),
		LogTopology:         full.LogTopology,
		CommittedPrefixes:   full.CommittedPrefixes,
		LaggingFollowers:    countClass(lagging),
		ReplicationLags:     countedClassesOrNone(lagClasses),
		CommitLags:          countedClassesOrNone(commitClasses),
		CatchUpTopology:     full.CatchUpTopology,
		AppendCatchUp:       appendCatchUp,
		SnapshotRequired:    snapshotRequired,
		UncommittedConflict: full.LogTopology == "uncommitted-suffix-divergence",
		CommittedConflict:   full.CommittedPrefixes == "conflict",
	}
}

func (s prototypeState) snapshotFacet(full prototypeStateKey, context FacetContext) SnapshotFacet {
	mode := "no-snapshot"
	switch {
	case containsClass(full.CatchUpTopology, "snapshot-pending"):
		mode = "snapshot-pending"
	case containsClass(full.CatchUpTopology, "snapshot-required"):
		mode = "snapshot-required"
	case hasSnapshot(full.SnapshotTopology):
		mode = "snapshot-available"
	case containsClass(full.SnapshotTopology, "not-modeled"):
		mode = "not-modeled"
	}
	outcome := context.SnapshotOutcome
	if outcome == "" {
		outcome = "none"
	}
	return SnapshotFacet{
		Mode: mode, NodePhases: full.SnapshotTopology,
		Outcome: outcome, RetryPending: context.SnapshotRetryPending,
	}
}

func (s prototypeState) recoveryFacet(context FacetContext) RecoveryFacet {
	crashed := 0
	for index := range s.roles {
		if !s.active[index+1] {
			crashed++
		}
	}
	phase := "normal"
	switch {
	case context.RecoveredThisFrame > 0:
		phase = "recovery-completed"
	case len(context.RecoveringNodes) > 0:
		phase = "restarted-waiting-catch-up"
	case crashed > 0:
		phase = "node-crashed"
	case len(context.RestartedNodes) > 0:
		phase = "restarted-recovered"
	}
	mode := context.RecoveryMode
	if mode == "" {
		mode = "none"
	}
	termRelation := context.MessageTermRelation
	if termRelation == "" {
		termRelation = "none"
	}
	return RecoveryFacet{
		Phase: phase, RecoveringNodes: countClass(len(context.RecoveringNodes)),
		RecoveryMode: mode, MessageTermRelation: termRelation,
	}
}

func (s prototypeState) networkFacet(context FacetContext) (NetworkFacet, error) {
	facet := NetworkFacet{
		Mode: "no-partition", GroupShapes: []prototypeClassCount{{Class: "all-connected", Count: 1}},
		ConnectedQuorum: len(s.active) >= len(s.roles)/2+1,
		LeaderPlacement: "not-partitioned", JustHealed: context.JustHealed,
		DelayedMessagesPending: context.DelayedMessages,
	}
	if context.NetworkPartition == nil {
		if context.JustHealed {
			facet.Mode = "healed"
		}
		return facet, nil
	}
	partition := context.NetworkPartition.Normalized()
	nodes := make([]core.NodeID, len(s.roles))
	for index := range nodes {
		nodes[index] = core.NodeID(index + 1)
	}
	if !partition.Covers(nodes) {
		return NetworkFacet{}, fmt.Errorf("facet network partition does not cover model nodes")
	}
	quorum := len(s.roles)/2 + 1
	groupClasses := make([]string, 0, len(partition.Groups))
	connectedQuorum := false
	leaderGroups := make([]int, 0)
	activePerGroup := make([]int, len(partition.Groups))
	for groupIndex, group := range partition.Groups {
		active, leaders, candidates := 0, 0, 0
		for _, id := range group {
			index := int(id - 1)
			if !s.active[int(id)] {
				continue
			}
			active++
			switch s.roles[index] {
			case "leader":
				leaders++
				leaderGroups = append(leaderGroups, groupIndex)
			case "candidate":
				candidates++
			}
		}
		activePerGroup[groupIndex] = active
		if active >= quorum {
			connectedQuorum = true
		}
		groupClasses = append(groupClasses,
			fmt.Sprintf("size=%d,active=%d,leaders=%d,candidates=%d", len(group), active, leaders, candidates))
	}
	facet.GroupShapes = countedClasses(groupClasses)
	facet.ConnectedQuorum = connectedQuorum
	facet.Mode = networkMode(partition, s, connectedQuorum)
	switch {
	case len(leaderGroups) == 0:
		facet.LeaderPlacement = "no-leader"
	case len(leaderGroups) > 1:
		facet.LeaderPlacement = "multiple-leaders"
	default:
		group := leaderGroups[0]
		switch {
		case len(partition.Groups[group]) == 1:
			facet.LeaderPlacement = "leader-isolated"
		case activePerGroup[group] >= quorum:
			facet.LeaderPlacement = "leader-in-majority"
		default:
			facet.LeaderPlacement = "leader-in-minority"
		}
	}
	return facet, nil
}

func networkMode(partition core.NetworkPartition, state prototypeState, connectedQuorum bool) string {
	if len(partition.Groups) == 2 {
		for _, group := range partition.Groups {
			if len(group) != 1 {
				continue
			}
			id := group[0]
			if state.active[int(id)] && state.roles[int(id)-1] == "leader" {
				return "leader-isolated"
			}
			if state.active[int(id)] && state.roles[int(id)-1] == "follower" {
				return "single-follower-isolated"
			}
		}
		if connectedQuorum {
			return "majority-minority-split"
		}
		return "no-connected-quorum"
	}
	if !connectedQuorum {
		return "no-connected-quorum"
	}
	return "multi-group-partition"
}

func countClass(count int) string {
	switch count {
	case 0:
		return "zero"
	case 1:
		return "one"
	default:
		return "multiple"
	}
}

func filterClasses(classes []prototypeClassCount, prefix string) []prototypeClassCount {
	result := make([]prototypeClassCount, 0)
	for _, class := range classes {
		if strings.HasPrefix(class.Class, prefix) {
			result = append(result, prototypeClassCount{
				Class: strings.TrimPrefix(class.Class, prefix), Count: class.Count,
			})
		}
	}
	if len(result) == 0 {
		return []prototypeClassCount{{Class: "none", Count: 1}}
	}
	return result
}

func containsClass(classes []prototypeClassCount, wanted string) bool {
	for _, class := range classes {
		if class.Class == wanted && class.Count > 0 {
			return true
		}
	}
	return false
}

func containsClassPrefix(classes []prototypeClassCount, prefix string) bool {
	for _, class := range classes {
		if strings.HasPrefix(class.Class, prefix) && class.Count > 0 {
			return true
		}
	}
	return false
}

func hasSnapshot(classes []prototypeClassCount) bool {
	for _, class := range classes {
		if class.Class != "none" && class.Class != "not-modeled" && class.Count > 0 {
			return true
		}
	}
	return false
}

func dominantClass(classes []prototypeClassCount) string {
	if len(classes) == 0 {
		return "none"
	}
	copy := append([]prototypeClassCount(nil), classes...)
	sort.Slice(copy, func(i, j int) bool {
		if copy[i].Count != copy[j].Count {
			return copy[i].Count > copy[j].Count
		}
		return copy[i].Class < copy[j].Class
	})
	return copy[0].Class
}
