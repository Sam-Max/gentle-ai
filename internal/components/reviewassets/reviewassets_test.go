package reviewassets

import (
	"context"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v3/internal/assets"
	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
	"github.com/gentleman-programming/gentle-ai/v3/internal/opencode"
)

func TestRuntimeContracts(t *testing.T) {
	for _, tc := range []struct {
		agent              model.AgentID
		required, excluded string
	}{
		{model.AgentPi, "`gentle_review_capture_group`", "gentle-ai review status"},
		{model.AgentClaudeCode, "gentle-ai review status --cwd <repo> --contract gentle-ai.review-integration/v2 --agent claude-code --next-transition", "gentle_review_capture_group"},
		{model.AgentOpenCode, "### OpenCode Concurrent Reviewer Group", "gentle_review_capture_group"},
	} {
		t.Run(string(tc.agent), func(t *testing.T) {
			got, err := ReviewExecutionContractFor(tc.agent)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got, tc.required) || strings.Contains(got, tc.excluded) || strings.Contains(got, identityPlaceholder) {
				t.Fatalf("incorrect runtime contract for %s", tc.agent)
			}
		})
	}
	if _, err := ReviewExecutionContractFor(model.AgentKilocode); err == nil {
		t.Fatal("unsupported runtime accepted")
	}
}

func TestRenderedLensAssets(t *testing.T) {
	for _, path := range []string{
		"claude/agents/review-risk.md", "cursor/agents/review-reliability.md",
		"kimi/agents/review-readability.md", "kiro/agents/review-resilience.md",
	} {
		t.Run(path, func(t *testing.T) {
			source := assets.MustRead(path)
			got, ok := RenderReviewerAsset(path, source)
			if !ok || !strings.HasPrefix(got, source[:strings.Index(source, "\n---\n")+5]) {
				t.Fatal("review frontmatter changed")
			}
			for _, want := range []string{"## Candidate-Causal Admission", "## Scope", "## Output", "subject_hash", "GENTLE_AI_REVIEW_BINDING"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q", want)
				}
			}
		})
	}
	source := "---\nname: jd-judge-a\n---\noriginal"
	if got, ok := RenderReviewerAsset("kiro/agents/jd-judge-a.md", source); ok || got != source {
		t.Fatal("Judgment Day unexpectedly rendered as review lens")
	}
	if got, ok := RenderReviewerAsset("claude/agents/review-refuter.md", source); ok || got != source {
		t.Fatal("refuter unexpectedly rendered as lens")
	}
}

func TestInspectionCommandsIndependent(t *testing.T) {
	first, second := InspectionCommands(), InspectionCommands()
	first[0] = "changed"
	if second[0] == "changed" {
		t.Fatal("inspection command slices share storage")
	}
}

// TestOpenCodeV2ContractIsAuthorizedNotStaged pins both halves of the V2
// contract render: a detected V2 runtime receives the real execution contract
// as capability authorization and never the staged "unavailable" warning, and
// the OpenCode V2 tool-name renames still map `task` to `subagent` and
// `subagent_type` to `agent`.
func TestOpenCodeV2ContractIsAuthorizedNotStaged(t *testing.T) {
	old := opencode.VersionRunnerOverride
	t.Cleanup(func() { opencode.VersionRunnerOverride = old })
	opencode.VersionRunnerOverride = func(context.Context, opencode.Command) (opencode.CommandOutput, error) {
		return opencode.CommandOutput{Stdout: []byte("2.0.16")}, nil
	}
	got := ContractFor(model.AgentOpenCode)
	for _, staged := range []string{
		"OpenCode V2 review transport is unavailable",
		"pending organic runtime conformance",
		"staged reference, not capability authorization",
	} {
		if strings.Contains(got, staged) {
			t.Fatalf("V2 contract still carries the staged warning %q", staged)
		}
	}
	if !strings.Contains(got, "### OpenCode Concurrent Reviewer Group") {
		t.Fatal("V2 contract lost its execution contract body")
	}
	if strings.Contains(got, "`task`") {
		t.Fatal("`task` was not renamed to `subagent` for V2")
	}
	if !strings.Contains(got, "`subagent`") {
		t.Fatal("V2 contract is missing the `subagent` tool name")
	}
	if strings.Contains(got, "`subagent_type`") {
		t.Fatal("`subagent_type` was not renamed to `agent` for V2")
	}
	if !strings.Contains(got, "`agent`") {
		t.Fatal("V2 contract is missing the `agent` tool name")
	}
}
