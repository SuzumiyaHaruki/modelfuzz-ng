package coverageanalysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
)

type RunArtifact struct {
	Name        string
	Source      string
	ModelConfig raftmodel.Config
	Initial     core.Observation
	Trace       core.Trace
	ModelEvents []model.Event
	ModelStates []model.State
}

type CoverageFrame struct {
	RunID           string
	Source          string
	StepIndex       int
	ModelEventIndex int
	ModelStateIndex int
	ModelState      model.State
	Action          *core.Action
	Effects         []core.Effect
	ModelEvent      *model.Event
	Context         raftmodel.FacetContext
}

func BuildCoverageFrames(run RunArtifact) ([]CoverageFrame, error) {
	if run.Name == "" {
		return nil, fmt.Errorf("coverage frame run name is empty")
	}
	if err := run.Trace.Validate(); err != nil {
		return nil, fmt.Errorf("run %q trace: %w", run.Name, err)
	}
	if len(run.ModelStates) != len(run.ModelEvents)+1 {
		return nil, fmt.Errorf(
			"run %q model alignment: %d states, %d events; want states=events+1",
			run.Name, len(run.ModelStates), len(run.ModelEvents))
	}
	mapper, err := raftmodel.NewMapperWithConfig(run.ModelConfig)
	if err != nil {
		return nil, fmt.Errorf("run %q mapper config: %w", run.Name, err)
	}
	tracker, err := newFrameContextTracker(run.Initial)
	if err != nil {
		return nil, fmt.Errorf("run %q initial context: %w", run.Name, err)
	}
	frames := []CoverageFrame{{
		RunID: run.Name, Source: run.Source, StepIndex: -1, ModelEventIndex: -1,
		ModelStateIndex: 0, ModelState: run.ModelStates[0],
		Context: tracker.context(false, "", "", false).FacetContext,
	}}
	eventCursor := 0
	stateCursor := 0
	for stepIndex, step := range run.Trace.Steps {
		transition, err := model.TransitionFromRecord(step)
		if err != nil {
			return nil, fmt.Errorf("run %q step %d transition: %w", run.Name, stepIndex, err)
		}
		mapped, err := mapper.Map(transition)
		if err != nil {
			return nil, fmt.Errorf("run %q step %d remap: %w", run.Name, stepIndex, err)
		}
		if eventCursor+len(mapped) > len(run.ModelEvents) {
			return nil, fmt.Errorf("run %q step %d emits beyond persisted model events", run.Name, stepIndex)
		}
		for offset := range mapped {
			if !sameModelEvent(mapped[offset], run.ModelEvents[eventCursor+offset]) {
				return nil, fmt.Errorf(
					"run %q step %d event %d differs from persisted model event",
					run.Name, stepIndex, eventCursor+offset)
			}
		}

		delivered, justHealed, err := tracker.applyStep(step)
		if err != nil {
			return nil, fmt.Errorf("run %q step %d context: %w", run.Name, stepIndex, err)
		}
		action := step.Action.Copy()
		if len(mapped) == 0 {
			outcome, retryPending := tracker.snapshotEffectOutcome(step.Effects)
			recoveryMode, termRelation := tracker.recoveryMessageContext(delivered, step.NodesBefore)
			recovered, recoveryErr := tracker.finishRecoveries(run.ModelStates[stateCursor])
			if recoveryErr != nil {
				return nil, fmt.Errorf("run %q step %d recovery: %w", run.Name, stepIndex, recoveryErr)
			}
			frames = append(frames, CoverageFrame{
				RunID: run.Name, Source: run.Source, StepIndex: stepIndex,
				ModelEventIndex: -1, ModelStateIndex: stateCursor,
				ModelState: run.ModelStates[stateCursor], Action: &action,
				Effects: copyEffects(step.Effects),
				Context: tracker.context(justHealed, outcome, recoveryMode, retryPending).
					withRecovery(recovered, termRelation),
			})
			continue
		}
		for offset := range mapped {
			event := mapped[offset].Copy()
			eventCursor++
			stateCursor++
			outcome, retryPending := tracker.snapshotEventOutcome(event)
			recoveryMode, termRelation := tracker.recoveryEventContext(event, delivered, step.NodesBefore)
			recovered, recoveryErr := tracker.finishRecoveries(run.ModelStates[stateCursor])
			if recoveryErr != nil {
				return nil, fmt.Errorf(
					"run %q step %d event %d recovery: %w", run.Name, stepIndex, eventCursor-1, recoveryErr)
			}
			context := tracker.context(justHealed, outcome, recoveryMode, retryPending).
				withRecovery(recovered, termRelation)
			frames = append(frames, CoverageFrame{
				RunID: run.Name, Source: run.Source, StepIndex: stepIndex,
				ModelEventIndex: eventCursor - 1, ModelStateIndex: stateCursor,
				ModelState: run.ModelStates[stateCursor], Action: &action,
				Effects: copyEffects(step.Effects), ModelEvent: &event, Context: context,
			})
			_ = offset
		}
	}
	if eventCursor != len(run.ModelEvents) || stateCursor != len(run.ModelStates)-1 {
		return nil, fmt.Errorf(
			"run %q alignment ended at event=%d/%d state=%d/%d",
			run.Name, eventCursor, len(run.ModelEvents), stateCursor, len(run.ModelStates)-1)
	}
	return frames, nil
}

type facetContextValue struct {
	raftmodel.FacetContext
}

func (c facetContextValue) withRecovery(recovered int, termRelation string) raftmodel.FacetContext {
	c.RecoveredThisFrame = recovered
	c.MessageTermRelation = termRelation
	return c.FacetContext
}

func sameModelEvent(left, right model.Event) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

type queuedFrameMessage struct {
	message core.Message
	delayed bool
}

type snapshotPair struct {
	leader   core.NodeID
	follower core.NodeID
}

type frameContextTracker struct {
	partition     *core.NetworkPartition
	queues        map[core.LinkID][]queuedFrameMessage
	maxMessageID  core.MessageID
	restarted     map[core.NodeID]bool
	recovering    map[core.NodeID]bool
	snapshotRetry map[snapshotPair]bool
}

func newFrameContextTracker(initial core.Observation) (*frameContextTracker, error) {
	tracker := &frameContextTracker{
		queues:    make(map[core.LinkID][]queuedFrameMessage),
		restarted: make(map[core.NodeID]bool), recovering: make(map[core.NodeID]bool),
		snapshotRetry: make(map[snapshotPair]bool),
	}
	if initial.NetworkPartition != nil {
		partition := initial.NetworkPartition.Copy()
		tracker.partition = &partition
	}
	for _, observed := range initial.Messages {
		message := core.Message{
			ID: observed.ID, From: observed.From, To: observed.To, SenderEpoch: observed.SenderEpoch,
			Sequence: observed.LinkSequence, ParentID: observed.ParentID,
			TypeHint: observed.TypeHint, PayloadDigest: observed.PayloadDigest,
			Metadata: cloneStrings(observed.Metadata),
		}
		if err := message.Validate(); err != nil {
			return nil, err
		}
		tracker.enqueue(message, false)
	}
	return tracker, nil
}

func (t *frameContextTracker) applyStep(step core.StepRecord) (*core.Message, bool, error) {
	var delivered *core.Message
	justHealed := false
	action := step.Action
	switch action.Kind {
	case core.ActionDeliver, core.ActionDrop:
		message, err := t.remove(action)
		if err != nil {
			return nil, false, err
		}
		delivered = &message
	case core.ActionDuplicate:
		message, err := t.resolve(action)
		if err != nil {
			return nil, false, err
		}
		copy := message
		t.maxMessageID++
		copy.ID = t.maxMessageID
		copy.ParentID = message.ID
		copy.Sequence = uint64(len(t.queues[copy.Link()]) + 1)
		t.enqueue(copy, t.tokenDelayed(action))
	case core.ActionPartition:
		partition := action.Partition.Copy()
		t.partition = &partition
	case core.ActionHeal:
		if t.partition == nil {
			return nil, false, fmt.Errorf("heal has no active partition")
		}
		for link, queue := range t.queues {
			if !t.partition.Blocks(link) {
				continue
			}
			for index := range queue {
				queue[index].delayed = true
			}
			t.queues[link] = queue
		}
		t.partition = nil
		justHealed = true
	case core.ActionCrash:
		delete(t.recovering, action.Node)
	case core.ActionRestart:
		t.restarted[action.Node] = true
		t.recovering[action.Node] = true
	}
	for _, effect := range step.Effects {
		if effect.Kind != core.EffectSendMessage || effect.Message == nil {
			continue
		}
		t.enqueue(effect.Message.Copy(), false)
	}
	return delivered, justHealed, nil
}

func (t *frameContextTracker) resolve(action core.Action) (core.Message, error) {
	if action.Selector == nil {
		return core.Message{}, fmt.Errorf("message action has no selector")
	}
	queue := t.queues[action.Selector.Link]
	if action.Selector.Position < 0 || action.Selector.Position >= len(queue) {
		return core.Message{}, fmt.Errorf(
			"message %s position %d is unavailable on %s",
			action.Message, action.Selector.Position, action.Selector.Link)
	}
	message := queue[action.Selector.Position].message
	if message.ID != action.Message {
		return core.Message{}, fmt.Errorf(
			"message selector resolved %s, persisted action wants %s", message.ID, action.Message)
	}
	return message.Copy(), nil
}

func (t *frameContextTracker) tokenDelayed(action core.Action) bool {
	if action.Selector == nil {
		return false
	}
	queue := t.queues[action.Selector.Link]
	if action.Selector.Position < 0 || action.Selector.Position >= len(queue) {
		return false
	}
	return queue[action.Selector.Position].delayed
}

func (t *frameContextTracker) remove(action core.Action) (core.Message, error) {
	message, err := t.resolve(action)
	if err != nil {
		return core.Message{}, err
	}
	link := action.Selector.Link
	queue := t.queues[link]
	queue = append(queue[:action.Selector.Position], queue[action.Selector.Position+1:]...)
	t.queues[link] = queue
	return message, nil
}

func (t *frameContextTracker) enqueue(message core.Message, delayed bool) {
	if message.ID > t.maxMessageID {
		t.maxMessageID = message.ID
	}
	link := message.Link()
	t.queues[link] = append(t.queues[link], queuedFrameMessage{message: message.Copy(), delayed: delayed})
}

func (t *frameContextTracker) context(
	justHealed bool, snapshotOutcome, recoveryMode string, retryPending bool,
) facetContextValue {
	var partition *core.NetworkPartition
	if t.partition != nil {
		copy := t.partition.Copy()
		partition = &copy
	}
	return facetContextValue{raftmodel.FacetContext{
		NetworkPartition: partition, JustHealed: justHealed,
		DelayedMessages: t.delayedMessages(),
		RestartedNodes:  sortedNodeSet(t.restarted), RecoveringNodes: sortedNodeSet(t.recovering),
		RecoveryMode: recoveryMode, SnapshotOutcome: snapshotOutcome,
		SnapshotRetryPending: retryPending || len(t.snapshotRetry) > 0,
	}}
}

func (t *frameContextTracker) delayedMessages() bool {
	for _, queue := range t.queues {
		for _, message := range queue {
			if message.delayed {
				return true
			}
		}
	}
	return false
}

func (t *frameContextTracker) finishRecoveries(state model.State) (int, error) {
	recovered := 0
	for node := range t.recovering {
		complete, err := raftmodel.PrototypeRecoveryComplete(state, node)
		if err != nil {
			return 0, err
		}
		if complete {
			delete(t.recovering, node)
			recovered++
		}
	}
	return recovered, nil
}

func (t *frameContextTracker) snapshotEventOutcome(event model.Event) (string, bool) {
	switch event.Name {
	case "CreateSnapshot":
		return "created", len(t.snapshotRetry) > 0
	case "SendSnapshot":
		pair, ok := eventPair(event)
		if ok && t.snapshotRetry[pair] {
			return "retry-pending", true
		}
		return "pending", len(t.snapshotRetry) > 0
	case "InstallSnapshot":
		return "installed", len(t.snapshotRetry) > 0
	case "FastForwardSnapshot":
		return "fast-forward", len(t.snapshotRetry) > 0
	case "RejectSnapshot":
		return "rejected-or-stale", len(t.snapshotRetry) > 0
	case "HandleSnapshotStatus":
		pair, ok := eventPair(event)
		success, _ := event.Params["success"].(bool)
		if !success {
			if ok {
				t.snapshotRetry[pair] = true
			}
			return "failed", true
		}
		if ok && t.snapshotRetry[pair] {
			delete(t.snapshotRetry, pair)
			return "retry-succeeded", len(t.snapshotRetry) > 0
		}
		return "delivered", len(t.snapshotRetry) > 0
	default:
		return "none", len(t.snapshotRetry) > 0
	}
}

func (t *frameContextTracker) snapshotEffectOutcome(effects []core.Effect) (string, bool) {
	for _, effect := range effects {
		if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil {
			continue
		}
		switch effect.ModelEvent.Name {
		case "raft.snapshot_fast_forwarded":
			return "fast-forward", len(t.snapshotRetry) > 0
		case "raft.snapshot_applied":
			return "installed", len(t.snapshotRetry) > 0
		case "raft.snapshot_rejected_or_stale":
			return "rejected-or-stale", len(t.snapshotRetry) > 0
		}
	}
	return "none", len(t.snapshotRetry) > 0
}

func eventPair(event model.Event) (snapshotPair, bool) {
	left, leftOK := eventNode(event.Params["i"])
	right, rightOK := eventNode(event.Params["j"])
	if !leftOK || !rightOK {
		return snapshotPair{}, false
	}
	return snapshotPair{leader: left, follower: right}, true
}

func eventNode(value any) (core.NodeID, bool) {
	switch value := value.(type) {
	case uint64:
		return core.NodeID(value), value > 0
	case int:
		return core.NodeID(value), value > 0
	case float64:
		return core.NodeID(value), value > 0 && value == float64(uint64(value))
	default:
		return 0, false
	}
}

func (t *frameContextTracker) recoveryEventContext(
	event model.Event, delivered *core.Message, before []core.NodeObservation,
) (string, string) {
	mode, relation := t.recoveryMessageContext(delivered, before)
	switch event.Name {
	case "InstallSnapshot", "FastForwardSnapshot":
		mode = "snapshot"
	case "Add":
		mode = "restart"
	}
	return mode, relation
}

func (t *frameContextTracker) recoveryMessageContext(
	delivered *core.Message, before []core.NodeObservation,
) (string, string) {
	if delivered == nil || !t.restarted[delivered.To] {
		return "", ""
	}
	mode := "other-message"
	switch delivered.TypeHint {
	case "MsgApp":
		mode = "append-entries"
	case "MsgSnap":
		mode = "snapshot"
	}
	messageTerm, termErr := strconv.ParseUint(delivered.Metadata["term"], 10, 64)
	nodeTerm, termOK := observedNodeTerm(before, delivered.To)
	if termErr != nil || !termOK {
		return mode, "unknown"
	}
	switch {
	case messageTerm < nodeTerm:
		return mode, "stale"
	case messageTerm > nodeTerm:
		return mode, "higher"
	default:
		return mode, "same"
	}
}

func observedNodeTerm(nodes []core.NodeObservation, target core.NodeID) (uint64, bool) {
	for _, node := range nodes {
		if node.ID != target {
			continue
		}
		switch value := node.Semantic["term"].(type) {
		case uint64:
			return value, true
		case float64:
			return uint64(value), value == float64(uint64(value))
		case json.Number:
			parsed, err := strconv.ParseUint(value.String(), 10, 64)
			return parsed, err == nil
		}
	}
	return 0, false
}

func sortedNodeSet(values map[core.NodeID]bool) []core.NodeID {
	result := make([]core.NodeID, 0, len(values))
	for value, present := range values {
		if present {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func copyEffects(values []core.Effect) []core.Effect {
	result := make([]core.Effect, len(values))
	for index, value := range values {
		result[index] = value.Copy()
	}
	return result
}

func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
