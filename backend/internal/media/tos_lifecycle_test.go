package media

import (
	"testing"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

func TestVerifyTOSLifecycleRulesAcceptsEnabledThirtyDayPrefixRule(t *testing.T) {
	result := VerifyTOSLifecycleRules([]tos.LifecycleRule{{
		ID: "delete-ark-media-30d", Prefix: "ark-media/", Status: enum.LifecycleStatusEnabled,
		Expiration: &tos.Expiration{Days: 30},
	}}, "ark-media/", 30)
	if result.Status != "verified" || result.MatchedRuleID != "delete-ark-media-30d" {
		t.Fatalf("unexpected verification result: %+v", result)
	}
}

func TestVerifyTOSLifecycleRulesRejectsWrongDaysAndDisabledRules(t *testing.T) {
	for name, rule := range map[string]tos.LifecycleRule{
		"wrong days": {ID: "wrong", Prefix: "ark-media", Status: enum.LifecycleStatusEnabled, Expiration: &tos.Expiration{Days: 7}},
		"disabled":   {ID: "disabled", Prefix: "ark-media", Status: enum.LifecycleStatusDisabled, Expiration: &tos.Expiration{Days: 30}},
	} {
		t.Run(name, func(t *testing.T) {
			result := VerifyTOSLifecycleRules([]tos.LifecycleRule{rule}, "ark-media/", 30)
			if result.Status != "mismatch" || result.MatchedRuleID == "" {
				t.Fatalf("expected mismatch with matched rule evidence: %+v", result)
			}
		})
	}
}

func TestVerifyTOSLifecycleRulesReportsMissingPrefix(t *testing.T) {
	result := VerifyTOSLifecycleRules(nil, "ark-media/", 30)
	if result.Status != "mismatch" || result.Reason != "no_matching_prefix_rule" {
		t.Fatalf("unexpected missing-rule result: %+v", result)
	}
}
