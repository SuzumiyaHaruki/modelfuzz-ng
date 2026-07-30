// Frozen experimental surface: stage-budgeted Branch allocation remains only
// for accepted round-six experiments and compatible artifact recomputation.
package goalsearch

import (
	"fmt"
	"sort"
)

type BranchBudgetMode string

const (
	BranchBudgetRoundRobin    BranchBudgetMode = "round-robin"
	BranchBudgetStageBudgeted BranchBudgetMode = "stage-budgeted"
)

func (mode BranchBudgetMode) Validate() error {
	switch mode {
	case BranchBudgetRoundRobin, BranchBudgetStageBudgeted:
		return nil
	default:
		return fmt.Errorf("unknown branch budget mode %q", mode)
	}
}

type StageBudgetConfig struct {
	InitialQuota      int `json:"initial_quota"`
	SupportedQuota    int `json:"supported_evidence_quota"`
	CommitmentQuota   int `json:"commitment_quota"`
	NextWaypointQuota int `json:"next_waypoint_quota"`
	PerBranchTotalCap int `json:"per_branch_total_cap"`
}

func (config StageBudgetConfig) Validate() error {
	if config.InitialQuota <= 0 || config.SupportedQuota < 0 ||
		config.CommitmentQuota < 0 || config.NextWaypointQuota < 0 ||
		config.PerBranchTotalCap < config.InitialQuota {
		return fmt.Errorf("invalid stage budget config %+v", config)
	}
	return nil
}

type BranchBudgetLedgerRecord struct {
	SchemaVersion      string           `json:"schema_version"`
	CandidateIndex     int              `json:"candidate_index"`
	BranchTemplateID   BranchTemplateID `json:"branch_template_id"`
	Event              string           `json:"event"`
	Granted            int              `json:"granted"`
	Used               int              `json:"used"`
	ActionUsed         int              `json:"action_used"`
	Available          int              `json:"available"`
	TotalCap           int              `json:"total_cap"`
	EvidenceLevel      EvidenceLevel    `json:"evidence_level,omitempty"`
	CompletedWaypoints int              `json:"completed_waypoint_count"`
	Reason             string           `json:"reason"`
	StableKey          string           `json:"stable_key"`
}

type BranchBudgetState struct {
	BranchTemplateID  BranchTemplateID `json:"branch_template_id"`
	Granted           int              `json:"granted"`
	Used              int              `json:"used"`
	ActionUsed        int              `json:"action_used"`
	Unused            int              `json:"unused"`
	SupportedGranted  bool             `json:"supported_quota_granted"`
	CommitmentGranted bool             `json:"commitment_quota_granted"`
	HighestWaypoint   int              `json:"highest_waypoint"`
	Stopped           bool             `json:"stopped"`
	StopReason        string           `json:"stop_reason,omitempty"`
}

type BranchBudgetSummary struct {
	SchemaVersion string                                 `json:"schema_version"`
	Mode          BranchBudgetMode                       `json:"mode"`
	Config        StageBudgetConfig                      `json:"config"`
	States        map[BranchTemplateID]BranchBudgetState `json:"states"`
	TotalGranted  int                                    `json:"total_granted"`
	TotalUsed     int                                    `json:"total_used"`
	TotalActions  int                                    `json:"total_actions"`
	TotalUnused   int                                    `json:"total_unused"`
	Reallocations int                                    `json:"budget_reallocations"`
}

type StageBudgetAllocator struct {
	config   StageBudgetConfig
	branches []BranchTemplateID
	states   map[BranchTemplateID]BranchBudgetState
	cursor   int
	ledger   []BranchBudgetLedgerRecord
}

func NewStageBudgetAllocator(
	branches []BranchTemplateID,
	config StageBudgetConfig,
) (*StageBudgetAllocator, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	ids := append([]BranchTemplateID(nil), branches...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return nil, fmt.Errorf("stage budget allocator needs at least one branch")
	}
	states := make(map[BranchTemplateID]BranchBudgetState, len(ids))
	seen := make(map[BranchTemplateID]struct{}, len(ids))
	allocator := &StageBudgetAllocator{config: config, branches: ids, states: states}
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate stage-budget branch %q", id)
		}
		seen[id] = struct{}{}
		state := BranchBudgetState{
			BranchTemplateID: id,
			Granted:          config.InitialQuota,
			Unused:           config.InitialQuota,
			HighestWaypoint:  -1,
		}
		states[id] = state
		allocator.appendLedger(-1, id, "initial-quota", config.InitialQuota,
			EvidenceLevelPlanned, 0, "fixed equal initial exploration quota")
	}
	return allocator, nil
}

func (allocator *StageBudgetAllocator) Next(candidate int) (BranchTemplateID, bool) {
	if allocator == nil || len(allocator.branches) == 0 {
		return "", false
	}
	for checked := 0; checked < len(allocator.branches); checked++ {
		index := (allocator.cursor + checked) % len(allocator.branches)
		id := allocator.branches[index]
		state := allocator.states[id]
		if state.Stopped || state.Used >= state.Granted ||
			state.Used >= allocator.config.PerBranchTotalCap {
			continue
		}
		state.Used++
		state.Unused = max(0, state.Granted-state.Used)
		allocator.states[id] = state
		allocator.cursor = (index + 1) % len(allocator.branches)
		allocator.appendLedger(candidate, id, "consume", 0,
			EvidenceLevelPlanned, state.HighestWaypoint, "deterministic eligible round-robin")
		return id, true
	}
	return "", false
}

func (allocator *StageBudgetAllocator) Observe(
	candidate int,
	id BranchTemplateID,
	vector BranchEvidenceVector,
	completedWaypoints int,
	actionCounts ...int,
) {
	if allocator == nil {
		return
	}
	state, ok := allocator.states[id]
	if !ok || state.Stopped {
		return
	}
	for _, count := range actionCounts {
		state.ActionUsed += max(0, count)
	}
	allocator.states[id] = state
	if vector.Contradicted {
		state.Stopped = true
		state.StopReason = "branch-contradicted"
		state.Unused = max(0, state.Granted-state.Used)
		allocator.states[id] = state
		allocator.appendLedger(candidate, id, "stop", 0, vector.HighestLevel,
			completedWaypoints, "causal evidence contradicts planned branch")
		return
	}
	if vector.SupportedCount > 0 && !state.SupportedGranted {
		granted := allocator.grant(&state, allocator.config.SupportedQuota)
		state.SupportedGranted = true
		allocator.states[id] = state
		allocator.appendLedger(candidate, id, "supported-evidence-quota", granted,
			vector.HighestLevel, completedWaypoints, "first necessary partial evidence")
	}
	if vector.Commitment.Reached && !state.CommitmentGranted {
		granted := allocator.grant(&state, allocator.config.CommitmentQuota)
		state.CommitmentGranted = true
		allocator.states[id] = state
		allocator.appendLedger(candidate, id, "commitment-quota", granted,
			vector.HighestLevel, completedWaypoints, "branch commitment reached")
	}
	if completedWaypoints > state.HighestWaypoint {
		previous := state.HighestWaypoint
		state.HighestWaypoint = completedWaypoints
		if previous >= 0 {
			granted := allocator.grant(&state, allocator.config.NextWaypointQuota)
			allocator.states[id] = state
			allocator.appendLedger(candidate, id, "next-waypoint-quota", granted,
				vector.HighestLevel, completedWaypoints, "new Goal waypoint reached")
		}
	}
	state.Unused = max(0, state.Granted-state.Used)
	allocator.states[id] = state
}

func (allocator *StageBudgetAllocator) Stop(
	candidate int, id BranchTemplateID, reason string,
) {
	if allocator == nil {
		return
	}
	state, ok := allocator.states[id]
	if !ok || state.Stopped {
		return
	}
	state.Stopped = true
	state.StopReason = reason
	state.Unused = max(0, state.Granted-state.Used)
	allocator.states[id] = state
	allocator.appendLedger(candidate, id, "stop", 0,
		EvidenceLevelPlanned, state.HighestWaypoint, reason)
}

func (allocator *StageBudgetAllocator) Ledger() []BranchBudgetLedgerRecord {
	if allocator == nil {
		return nil
	}
	return append([]BranchBudgetLedgerRecord(nil), allocator.ledger...)
}

func (allocator *StageBudgetAllocator) Summary() BranchBudgetSummary {
	summary := BranchBudgetSummary{
		SchemaVersion: BranchEvidenceSchemaVersion,
		Mode:          BranchBudgetStageBudgeted,
		Config:        allocator.config,
		States:        make(map[BranchTemplateID]BranchBudgetState, len(allocator.states)),
	}
	for id, state := range allocator.states {
		state.Unused = max(0, state.Granted-state.Used)
		summary.States[id] = state
		summary.TotalGranted += state.Granted
		summary.TotalUsed += state.Used
		summary.TotalActions += state.ActionUsed
		summary.TotalUnused += state.Unused
		if state.SupportedGranted {
			summary.Reallocations++
		}
		if state.CommitmentGranted {
			summary.Reallocations++
		}
	}
	return summary
}

func (allocator *StageBudgetAllocator) grant(
	state *BranchBudgetState, amount int,
) int {
	if amount <= 0 {
		return 0
	}
	available := allocator.config.PerBranchTotalCap - state.Granted
	granted := min(amount, max(0, available))
	state.Granted += granted
	state.Unused = max(0, state.Granted-state.Used)
	return granted
}

func (allocator *StageBudgetAllocator) appendLedger(
	candidate int,
	id BranchTemplateID,
	event string,
	granted int,
	level EvidenceLevel,
	waypoints int,
	reason string,
) {
	state := allocator.states[id]
	record := BranchBudgetLedgerRecord{
		SchemaVersion:      BranchEvidenceSchemaVersion,
		CandidateIndex:     candidate,
		BranchTemplateID:   id,
		Event:              event,
		Granted:            granted,
		Used:               state.Used,
		ActionUsed:         state.ActionUsed,
		Available:          max(0, state.Granted-state.Used),
		TotalCap:           allocator.config.PerBranchTotalCap,
		EvidenceLevel:      level,
		CompletedWaypoints: waypoints,
		Reason:             reason,
	}
	copyForKey := record
	copyForKey.StableKey = ""
	record.StableKey = stableHash(copyForKey)
	allocator.ledger = append(allocator.ledger, record)
}
