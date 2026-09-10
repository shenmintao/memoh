package approval

import "testing"

func TestLocalCapabilityToolsHonorWorkspaceApprovalPolicy(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.Enabled = true
	cfg.Exec.Mode = PolicyModeAsk
	cfg.Read.Mode = PolicyModeDeny
	if got := policyDecision(cfg, "computer_mcp_123", map[string]any{"command": "MCP ha/state"}); got != DecisionNeedsApproval {
		t.Fatalf("MCP ignored ask policy: %s", got)
	}
	if got := policyDecision(cfg, "computer_skill_123", map[string]any{"path": "/skills/ha/SKILL.md"}); got != DecisionDeny {
		t.Fatalf("Skill ignored deny policy: %s", got)
	}
	cfg.Exec.Mode = PolicyModeDeny
	if got := policyDecision(cfg, "computer_mcp_123", nil); got != DecisionDeny {
		t.Fatalf("MCP ignored deny policy: %s", got)
	}
}
