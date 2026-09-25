package cli

import (
	"context"
	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
	"github.com/gentleman-programming/gentle-ai/v3/internal/opencode"
	"io"
	"strings"
	"testing"
)

// TestOpenCodeV2TransportCapabilityAdmitted pins the admission contract this
// build certifies locally: a detected OpenCode 2.x runtime is admitted for
// immutable receipt review, and its relay reaches input decoding instead of
// being refused as an unsupported transport at the capability gate. A runtime
// whose version cannot be proven still fails closed, and the unreadable
// sentinel proves that refusal still happens before a single frame is read.
func TestOpenCodeV2TransportCapabilityAdmitted(t *testing.T) {
	old := opencode.VersionRunnerOverride
	t.Cleanup(func() { opencode.VersionRunnerOverride = old })
	for _, version := range []string{"2.0.4", "unknown"} {
		t.Run(version, func(t *testing.T) {
			opencode.VersionRunnerOverride = func(context.Context, opencode.Command) (opencode.CommandOutput, error) {
				return opencode.CommandOutput{Stdout: []byte(version)}, nil
			}
			supported := reviewImmutableRuntimeCapability(model.AgentOpenCode).supportsImmutableReceiptReview()
			if version == "2.0.4" {
				if !supported {
					t.Fatal("detected V2 runtime must be admitted for immutable receipt review")
				}
				// Past the capability gate the relay decodes frames, so an
				// empty stream must surface as an invalid envelope and never
				// as an unsupported transport.
				err := runReviewOpenCodeTransport(nil, strings.NewReader(""), io.Discard)
				if err == nil || !strings.Contains(err.Error(), "opencode_review_transport_envelope_invalid") {
					t.Fatalf("V2 relay did not reach input decoding: %v", err)
				}
				return
			}
			if supported {
				t.Fatal("undetectable runtime advertised")
			}
			if err := runReviewOpenCodeTransport(nil, v2UnreadableInput{}, io.Discard); err == nil || !strings.Contains(err.Error(), reviewImmutableTransportUnsupportedCode) {
				t.Fatalf("direct relay did not refuse before input: %v", err)
			}
		})
	}
}

type v2UnreadableInput struct{}

func (v2UnreadableInput) Read([]byte) (int, error) {
	panic("undetectable runtime read transport input before capability refusal")
}

// TestOpenCodeRelayContractEnvironmentDoesNotGateCapability pins that the
// managed relay-contract declaration no longer gates admission: it can only
// ever narrow a capability, never enable or withhold one, so the env value
// must not short-circuit version detection. Both V1 and V2 are admitted, and
// only an unprovable version stays refused.
func TestOpenCodeRelayContractEnvironmentDoesNotGateCapability(t *testing.T) {
	t.Setenv("GENTLE_AI_OPENCODE_RELAY_CONTRACT", "gentle-ai.opencode-relay/v2-staged")
	old := opencode.VersionRunnerOverride
	t.Cleanup(func() { opencode.VersionRunnerOverride = old })
	for _, test := range []struct {
		name      string
		version   string
		supported bool
	}{
		{name: "declared V1 host", version: "1.18.30", supported: true},
		{name: "declared V2 host", version: "2.0.4", supported: true},
		{name: "undetectable declared host", version: "unknown", supported: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			probed := false
			opencode.VersionRunnerOverride = func(context.Context, opencode.Command) (opencode.CommandOutput, error) {
				probed = true
				return opencode.CommandOutput{Stdout: []byte(test.version)}, nil
			}
			supported := reviewImmutableRuntimeCapability(model.AgentOpenCode).supportsImmutableReceiptReview()
			if !probed {
				t.Fatal("relay contract environment must not short-circuit runtime detection")
			}
			if supported != test.supported {
				t.Fatalf("declared host at %q: supported = %t, want %t", test.version, supported, test.supported)
			}
		})
	}
}
