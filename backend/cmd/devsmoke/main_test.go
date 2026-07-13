package main

import (
	"strings"
	"testing"
)

func TestCascadeLoginDashboardNodesAreHumanPaced(t *testing.T) {
	nodes := cascadeLoginDashboardNodes("https://cascadeai.cn", "demo@example.com", "secret")
	if len(nodes) != 14 {
		t.Fatalf("expected 14 nodes, got %d", len(nodes))
	}

	wantDurations := map[string]int{
		"node_hold_home":          humanStageMinDurationMS,
		"node_hold_login":         humanStageMinDurationMS,
		"node_hold_choose_email":  humanStageMinDurationMS,
		"node_hold_fill_email":    humanStageMinDurationMS,
		"node_hold_fill_password": humanStageMinDurationMS,
		"node_hold_submit_login":  humanTransitionStageDurationMS,
		"node_hold_workspace":     humanFinalStageDurationMS,
	}

	gotWaits := 0
	for _, node := range nodes {
		if node.Action == "wait" {
			gotWaits++
			want, ok := wantDurations[node.ID]
			if !ok {
				t.Fatalf("unexpected wait node id %q", node.ID)
			}
			if node.DurationMS != want {
				t.Fatalf("node %s duration = %d, want %d", node.ID, node.DurationMS, want)
			}
			if captureScreenshotForSpec(node) {
				t.Fatalf("wait node %s should not create an extra screenshot", node.ID)
			}
			continue
		}
		if !captureScreenshotForSpec(node) {
			t.Fatalf("action node %s should capture a screenshot", node.ID)
		}
	}
	if gotWaits != len(wantDurations) {
		t.Fatalf("wait node count = %d, want %d", gotWaits, len(wantDurations))
	}
	if total := totalDurationMS(nodes); total < 87000 {
		t.Fatalf("total duration too small: %d", total)
	}
	if waits := waitDurationMS(nodes); waits != 77000 {
		t.Fatalf("explicit wait duration = %d, want 77000", waits)
	}
}

func TestScriptSourceFromNodesUsesHumanStageWaits(t *testing.T) {
	nodes := cascadeLoginDashboardNodes("https://cascadeai.cn", "demo@example.com", "secret")
	source := scriptSourceFromNodes(nodes)
	for _, token := range []string{
		`ctx.log("node_hold_home")`,
		"await ctx.page.waitForTimeout(10000)",
		"await ctx.page.waitForTimeout(12000)",
		"await ctx.page.waitForTimeout(15000)",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("generated script missing %q:\n%s", token, source)
		}
	}
	if strings.Contains(source, "waitForStage") {
		t.Fatalf("generated script should use explicit wait nodes, not timing hints:\n%s", source)
	}
}
