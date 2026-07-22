// Package metrics 从一次 Engine 执行中提取协议无关的统计信息。
package metrics

import (
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
)

// RunMetrics 是一条执行轨迹的明细统计。这里仅使用 core、plan、model 和
// oracle 已经暴露的通用字段，不解释 Raft 消息的具体语义。
type RunMetrics struct {
	ActionCounts             map[string]int `json:"action_counts"`
	EffectCounts             map[string]int `json:"effect_counts"`
	MessageTypeCounts        map[string]int `json:"outbound_message_type_counts"`
	ResolutionCounts         map[string]int `json:"resolution_counts"`
	DecisionCounts           map[string]int `json:"decision_counts"`
	ModelEventCounts         map[string]int `json:"model_event_counts"`
	OracleCounts             map[string]int `json:"oracle_counts"`
	TimerFireCounts          map[string]int `json:"timer_fire_counts"`
	FailureCounts            map[string]int `json:"failure_counts"`
	Termination              string         `json:"termination,omitempty"`
	SnapshotsCreated         int            `json:"snapshots_created"`
	SnapshotsSent            int            `json:"snapshots_sent"`
	SnapshotsDelivered       int            `json:"snapshots_delivered"`
	SnapshotsApplied         int            `json:"snapshots_applied"`
	SnapshotsRejectedOrStale int            `json:"snapshots_rejected_or_stale"`
	LogsCompacted            int            `json:"logs_compacted"`
	CompactedEntries         uint64         `json:"compacted_entries"`
	SnapshotBytes            uint64         `json:"snapshot_bytes"`

	InitialQueuedMessages       int    `json:"initial_queued_messages"`
	FinalQueuedMessages         int    `json:"final_queued_messages"`
	EstimatedPeakQueuedMessages int    `json:"estimated_peak_queued_messages"`
	MaxFinalMessageAge          uint64 `json:"max_final_message_age"`
}

// Collect 收集一次执行的统计。队列峰值根据具体消息动作和 SendMessage Effect
// 重建；FinalQueuedMessages 则直接来自最终 Observation。
func Collect(result engine.Result) RunMetrics {
	metrics := RunMetrics{
		ActionCounts: make(map[string]int), EffectCounts: make(map[string]int),
		MessageTypeCounts: make(map[string]int),
		ResolutionCounts:  make(map[string]int), DecisionCounts: make(map[string]int), ModelEventCounts: make(map[string]int),
		OracleCounts: make(map[string]int), TimerFireCounts: make(map[string]int),
		FailureCounts: make(map[string]int), Termination: string(result.Termination),
		InitialQueuedMessages: len(result.Initial.Messages), FinalQueuedMessages: len(result.Final.Messages),
	}
	queued := metrics.InitialQueuedMessages
	metrics.EstimatedPeakQueuedMessages = queued
	for _, action := range result.Actions.Actions {
		metrics.ActionCounts[string(action.Kind)]++
	}
	for _, resolution := range result.Resolutions {
		metrics.ResolutionCounts[string(resolution.Status)]++
		if resolution.ReasonCode != "" {
			metrics.DecisionCounts[string(resolution.ReasonCode)]++
		}
	}
	for _, step := range result.Trace.Steps {
		switch step.Action.Kind {
		case core.ActionDeliver, core.ActionDrop:
			if queued > 0 {
				queued--
			}
		case core.ActionDuplicate:
			queued++
		}
		for _, effect := range step.Effects {
			metrics.EffectCounts[string(effect.Kind)]++
			if effect.Kind == core.EffectModelEvent && effect.ModelEvent != nil {
				metrics.ModelEventCounts[effect.ModelEvent.Name]++
				switch effect.ModelEvent.Name {
				case "raft.snapshot_created":
					metrics.SnapshotsCreated++
					metrics.SnapshotBytes += unsignedMetric(effect.ModelEvent.Params["snapshot_bytes"])
				case "raft.snapshot_sent":
					metrics.SnapshotsSent++
				case "raft.snapshot_delivered":
					metrics.SnapshotsDelivered++
				case "raft.snapshot_applied":
					metrics.SnapshotsApplied++
				case "raft.snapshot_rejected_or_stale":
					metrics.SnapshotsRejectedOrStale++
				case "raft.log_compacted":
					metrics.LogsCompacted++
					metrics.CompactedEntries += unsignedMetric(effect.ModelEvent.Params["compacted_entries"])
				}
			}
			if effect.Kind == core.EffectSendMessage {
				queued++
				if effect.Message != nil {
					typeHint := effect.Message.TypeHint
					if typeHint == "" {
						typeHint = "unknown"
					}
					metrics.MessageTypeCounts[typeHint]++
				}
			}
			if effect.Kind == core.EffectTimerFired && effect.TimerFired != nil {
				key := string(effect.TimerFired.Source)
				if effect.TimerFired.TypeHint != "" {
					key += ":" + effect.TimerFired.TypeHint
				}
				metrics.TimerFireCounts[key]++
			}
		}
		if queued > metrics.EstimatedPeakQueuedMessages {
			metrics.EstimatedPeakQueuedMessages = queued
		}
	}
	if metrics.FinalQueuedMessages > metrics.EstimatedPeakQueuedMessages {
		metrics.EstimatedPeakQueuedMessages = metrics.FinalQueuedMessages
	}
	for _, event := range result.ModelEvents {
		name := event.Name
		if event.Reset {
			name = "reset"
		}
		metrics.ModelEventCounts[name]++
	}
	for _, finding := range result.OracleFindings {
		metrics.OracleCounts[finding.Oracle+":"+finding.Code]++
	}
	if result.Failure != nil {
		metrics.FailureCounts[string(result.Failure.Kind)]++
	}
	for _, message := range result.Final.Messages {
		if result.Final.Time >= message.EnqueuedAt {
			age := uint64(result.Final.Time - message.EnqueuedAt)
			if age > metrics.MaxFinalMessageAge {
				metrics.MaxFinalMessageAge = age
			}
		}
	}
	return metrics
}

func unsignedMetric(value any) uint64 {
	switch value := value.(type) {
	case uint64:
		return value
	case uint32:
		return uint64(value)
	case int:
		if value >= 0 {
			return uint64(value)
		}
	case int64:
		if value >= 0 {
			return uint64(value)
		}
	case float64:
		if value >= 0 {
			return uint64(value)
		}
	}
	return 0
}

// DurationSummary 汇总已完成执行的耗时，单位为微秒。
type DurationSummary struct {
	MinMicros  int64   `json:"min_micros"`
	MaxMicros  int64   `json:"max_micros"`
	MeanMicros float64 `json:"mean_micros"`
	P50Micros  int64   `json:"p50_micros"`
	P95Micros  int64   `json:"p95_micros"`
	P99Micros  int64   `json:"p99_micros"`
}

// Durations 计算 nearest-rank 分位数。空输入返回零值。
func Durations(values []int64) DurationSummary {
	if len(values) == 0 {
		return DurationSummary{}
	}
	copy := append([]int64(nil), values...)
	sort.Slice(copy, func(i, j int) bool { return copy[i] < copy[j] })
	var total int64
	for _, value := range copy {
		total += value
	}
	return DurationSummary{
		MinMicros: copy[0], MaxMicros: copy[len(copy)-1], MeanMicros: float64(total) / float64(len(copy)),
		P50Micros: percentile(copy, 50), P95Micros: percentile(copy, 95), P99Micros: percentile(copy, 99),
	}
}

func percentile(sorted []int64, percent int) int64 {
	index := (percent*len(sorted) + 99) / 100
	if index < 1 {
		index = 1
	}
	return sorted[index-1]
}
