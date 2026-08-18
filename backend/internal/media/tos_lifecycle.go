package media

import (
	"strings"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

// TOSLifecycleVerification is deliberately read-only evidence about the
// bucket rule. It does not grant publication permission by itself.
type TOSLifecycleVerification struct {
	Status         string `json:"status"`
	ExpectedPrefix string `json:"expected_prefix"`
	ExpectedDays   int    `json:"expected_days"`
	MatchedRuleID  string `json:"matched_rule_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// VerifyTOSLifecycleRules checks the standard all-task-artifacts retention
// contract without mutating the bucket. A rule must be enabled, target the
// configured object prefix, and expire current objects after exactly the
// expected number of days.
func VerifyTOSLifecycleRules(rules []tos.LifecycleRule, prefix string, expectedDays int) TOSLifecycleVerification {
	prefix = normalizeLifecyclePrefix(prefix)
	result := TOSLifecycleVerification{Status: "mismatch", ExpectedPrefix: prefix, ExpectedDays: expectedDays}
	if expectedDays <= 0 {
		result.Reason = "expected_days_invalid"
		return result
	}
	for _, rule := range rules {
		if normalizeLifecyclePrefix(rule.Prefix) != prefix {
			continue
		}
		if rule.Status != enum.LifecycleStatusEnabled {
			result.Reason = "matching_rule_disabled"
			result.MatchedRuleID = rule.ID
			continue
		}
		if rule.Expiration == nil {
			result.Reason = "matching_rule_has_no_expiration"
			result.MatchedRuleID = rule.ID
			continue
		}
		if rule.Expiration.Days != expectedDays || !rule.Expiration.Date.IsZero() {
			result.Reason = "expiration_does_not_match_expected_days"
			result.MatchedRuleID = rule.ID
			continue
		}
		result.Status = "verified"
		result.MatchedRuleID = rule.ID
		result.Reason = "enabled_prefix_expiration_verified"
		return result
	}
	if result.Reason == "" {
		result.Reason = "no_matching_prefix_rule"
	}
	return result
}

func normalizeLifecyclePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}
