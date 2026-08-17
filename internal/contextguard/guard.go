package contextguard

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"omnibridge/internal/config"
)

const Version = "1.0"

type Report struct {
	Version         string `json:"version"`
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"`
	ContextWindow   int    `json:"context_window,omitempty"`
	TargetTokens    int64  `json:"target_tokens,omitempty"`
	BeforeTokens    int64  `json:"before_tokens"`
	AfterTokens     int64  `json:"after_tokens"`
	SavedTokens     int64  `json:"saved_tokens"`
	RemovedMessages int    `json:"removed_messages,omitempty"`
	Action          string `json:"action"`
	Reason          string `json:"reason,omitempty"`
}

func estimateString(s string) int64 {
	if s == "" {
		return 0
	}
	// Deliberately conservative and tokenizer-independent. CJK/Cyrillic/emoji
	// use rune count while long ASCII words still get a 4-char/token floor.
	runes := utf8.RuneCountInString(s)
	bytes := len(s)
	byRunes := (runes + 2) / 3
	byBytes := (bytes + 3) / 4
	if byRunes > byBytes {
		return int64(byRunes)
	}
	return int64(byBytes)
}

func estimateAny(v any) int64 {
	switch x := v.(type) {
	case string:
		return estimateString(x)
	case []any:
		var n int64
		for _, v := range x {
			n += estimateAny(v) + 1
		}
		return n
	case map[string]any:
		var n int64 = 3
		for k, v := range x {
			n += estimateString(k)/2 + estimateAny(v)
		}
		return n
	case nil:
		return 0
	default:
		return 1
	}
}

func EstimateJSON(raw []byte) int64 {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return int64((len(raw) + 3) / 4)
	}
	return estimateAny(v)
}

func messageRole(m map[string]any) string {
	v, _ := m["role"].(string)
	return strings.ToLower(strings.TrimSpace(v))
}
func protectedRole(role string) bool { return role == "system" || role == "developer" }

func compactMessageArray(root map[string]any, key string, target int64, minRecent int) (removed int) {
	arr, ok := root[key].([]any)
	if !ok || len(arr) <= minRecent {
		return 0
	}
	// Keep all system/developer messages and the latest minRecent conversation turns.
	keepFrom := len(arr) - minRecent
	if keepFrom < 0 {
		keepFrom = 0
	}
	kept := make([]any, 0, len(arr))
	removedCount := 0
	for i, item := range arr {
		m, _ := item.(map[string]any)
		role := messageRole(m)
		if i >= keepFrom || protectedRole(role) {
			kept = append(kept, item)
			continue
		}
		removedCount++
	}
	if removedCount == 0 {
		return 0
	}
	// Insert a non-secret marker. We intentionally do not summarize omitted
	// content here because local summarization would itself require another model call.
	marker := map[string]any{"role": "system", "content": fmt.Sprintf("[OmniBridge Context Guard omitted %d older conversation messages to fit the verified context window.]", removedCount)}
	// Put marker after existing leading system/developer instructions.
	pos := 0
	for pos < len(kept) {
		m, _ := kept[pos].(map[string]any)
		if !protectedRole(messageRole(m)) {
			break
		}
		pos++
	}
	kept = append(kept, nil)
	copy(kept[pos+1:], kept[pos:])
	kept[pos] = marker
	root[key] = kept
	// If still too large, trim additional oldest non-system entries while always preserving last 2.
	for estimateAny(root) > target {
		arr, _ = root[key].([]any)
		if len(arr) <= 3 {
			break
		}
		idx := -1
		for i := 0; i < len(arr)-2; i++ {
			m, _ := arr[i].(map[string]any)
			if !protectedRole(messageRole(m)) {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		root[key] = append(arr[:idx], arr[idx+1:]...)
		removedCount++
	}
	return removedCount
}

func Apply(raw []byte, apiType string, contextWindow int, cfg config.ContextGuardConfig) ([]byte, Report, error) {
	rep := Report{Version: Version, Enabled: cfg.Enabled, Mode: cfg.Mode, ContextWindow: contextWindow, BeforeTokens: EstimateJSON(raw), Action: "observe"}
	rep.AfterTokens = rep.BeforeTokens
	if !cfg.Enabled || strings.EqualFold(cfg.Mode, "off") {
		rep.Action = "disabled"
		return raw, rep, nil
	}
	if contextWindow <= 0 {
		rep.Reason = "context_window_unknown"
		return raw, rep, nil
	}
	pct := cfg.MaxContextPercent
	if pct <= 0 {
		pct = 90
	}
	target := int64(float64(contextWindow)*pct/100) - int64(cfg.ReserveOutputTokens)
	if target < 1024 {
		target = 1024
	}
	rep.TargetTokens = target
	if rep.BeforeTokens <= target {
		rep.Reason = "within_limit"
		return raw, rep, nil
	}
	if strings.EqualFold(cfg.Mode, "observe") {
		rep.Action = "would_compact"
		rep.Reason = "over_limit"
		return raw, rep, nil
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw, rep, err
	}
	key := "messages"
	if strings.EqualFold(apiType, "responses") {
		if _, ok := root["input"].([]any); ok {
			key = "input"
		}
	}
	minRecent := cfg.MinRecentMessages
	if minRecent < 2 {
		minRecent = 8
	}
	removed := compactMessageArray(root, key, target, minRecent)
	if removed == 0 {
		rep.Action = "unable_to_compact"
		rep.Reason = "no_removable_messages"
		return raw, rep, nil
	}
	out, err := json.Marshal(root)
	if err != nil {
		return raw, rep, err
	}
	rep.RemovedMessages = removed
	rep.AfterTokens = EstimateJSON(out)
	rep.SavedTokens = rep.BeforeTokens - rep.AfterTokens
	if rep.SavedTokens < 0 {
		rep.SavedTokens = 0
	}
	rep.Action = "compacted"
	if rep.AfterTokens > target {
		rep.Reason = "best_effort_still_over_target"
	} else {
		rep.Reason = "fit_target"
	}
	return out, rep, nil
}
