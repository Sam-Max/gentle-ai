package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviewConsentAnswerChoices is the ordinary two-choice shape every success
// test resolves against: distinct labels, distinct provider-owned tokens, and
// one exact invocation per choice.
func reviewConsentAnswerChoices() []ReviewIntegrationConsentChoice {
	return []ReviewIntegrationConsentChoice{
		{Answer: "granted", Label: "Review this change", Effect: "reviews once", Invocation: "gentle-ai review start --target abc --consent granted"},
		{Answer: "declined", Label: "Skip this time", Effect: "skips once", Invocation: "gentle-ai review start --target abc --consent declined"},
	}
}

func reviewConsentAnswerEnvelopePayload(t *testing.T, result ReviewIntegrationConsentResult) []byte {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func reviewConsentAnswerWriteEnvelope(t *testing.T, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "consent.json")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestResolveReviewConsentAnswer is the alias/label single source of truth's
// table: every accepted shape resolves to exactly one option, and every
// non-option or ambiguous answer is refused.
func TestResolveReviewConsentAnswer(t *testing.T) {
	labels := []string{"Revisar este cambio", "Omitir esta vez"}
	tests := []struct {
		name        string
		labels      []string
		answer      string
		wantIndex   int
		wantRefused bool
	}{
		{"exact label", labels, "Revisar este cambio", 0, false},
		{"case and whitespace", labels, "  revisar ESTE cambio  ", 0, false},
		{"second label", labels, "Omitir esta vez", 1, false},
		{"bare ordinal", labels, "2", 1, false},
		{"la ordinal", labels, "la 2", 1, false},
		{"opción ordinal", labels, "opción 2", 1, false},
		{"first alias", labels, "first", 0, false},
		{"zero padded ordinal", labels, "01", 0, false},
		{"signed ordinal", labels, "+2", -1, true},
		{"out of range ordinal", labels, "3", -1, true},
		{"free text yes", labels, "yes", -1, true},
		{"free text dale", labels, "dale", -1, true},
		{"free text ok", labels, "ok", -1, true},
		{"raw token granted", labels, "granted", -1, true},
		{"raw token declined", labels, "declined", -1, true},
		{"empty answer", labels, "", -1, true},
		{"duplicate labels", []string{"Same", "same"}, "same", -1, true},
		{"label and alias collide", []string{"2", "granted"}, "2", -1, true},
		{"token as literal label", []string{"granted", "declined"}, "granted", 0, false},
		{"first as literal label", []string{"first", "second"}, "first", 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index, err := ResolveReviewConsentAnswer(test.labels, test.answer)
			if !test.wantRefused {
				if err != nil {
					t.Fatalf("ResolveReviewConsentAnswer(%q) error = %v, want resolution", test.answer, err)
				}
				if index != test.wantIndex {
					t.Fatalf("ResolveReviewConsentAnswer(%q) index = %d, want %d", test.answer, index, test.wantIndex)
				}
				return
			}
			var refused *ReviewConsentAnswerRefusalError
			if !errors.As(err, &refused) {
				t.Fatalf("ResolveReviewConsentAnswer(%q) error = %v, want *ReviewConsentAnswerRefusalError", test.answer, err)
			}
			if refused.ExitCode() != reviewConsentAnswerExitCodeRefused {
				t.Fatalf("refusal ExitCode() = %d, want %d", refused.ExitCode(), reviewConsentAnswerExitCodeRefused)
			}
			if !strings.Contains(refused.Error(), "re-present the COMPLETE consent envelope unchanged") {
				t.Fatalf("refusal %q does not instruct a re-present", refused.Error())
			}
		})
	}
}

// TestReviewConsentAnswerVerbOutputsExactInvocation pins the success invariant:
// stdout is exactly the chosen provider-owned invocation line, plus nothing.
func TestReviewConsentAnswerVerbOutputsExactInvocation(t *testing.T) {
	choices := reviewConsentAnswerChoices()
	envelope := ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchemaV3, Choices: choices}
	path := reviewConsentAnswerWriteEnvelope(t, reviewConsentAnswerEnvelopePayload(t, envelope))

	var stdout bytes.Buffer
	if err := runReviewConsentAnswer([]string{"--envelope", path, "--answer", "2"}, strings.NewReader(""), &stdout); err != nil {
		t.Fatalf("runReviewConsentAnswer: %v", err)
	}
	want := choices[1].Invocation + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want exactly %q", stdout.String(), want)
	}
}

// TestReviewConsentAnswerPrintsEnvelopeBytesVerbatim pins that the printed line
// is the choice's own invocation bytes and nothing else: the verb neither
// rebuilds nor normalizes them, so an orchestrator that runs the line runs
// exactly the provider-owned request. Surrounding whitespace survives, which is
// why the success invariant is stated as byte equality rather than equality of
// trimmed words.
func TestReviewConsentAnswerPrintsEnvelopeBytesVerbatim(t *testing.T) {
	invocation := "  gentle-ai review start --target abc --consent granted  "
	envelope := ReviewIntegrationConsentResult{
		Schema:  ReviewIntegrationConsentSchemaV3,
		Choices: []ReviewIntegrationConsentChoice{{Answer: "granted", Label: "Review", Invocation: invocation}},
	}
	path := reviewConsentAnswerWriteEnvelope(t, reviewConsentAnswerEnvelopePayload(t, envelope))

	var stdout bytes.Buffer
	if err := runReviewConsentAnswer([]string{"--envelope", path, "--answer", "Review"}, strings.NewReader(""), &stdout); err != nil {
		t.Fatalf("runReviewConsentAnswer: %v", err)
	}
	if stdout.String() != invocation+"\n" {
		t.Fatalf("stdout = %q, want the envelope bytes verbatim %q", stdout.String(), invocation+"\n")
	}
}

// TestReviewConsentAnswerReadsEnvelopeFromStdin proves `--envelope -` consumes
// the envelope from stdin rather than the filesystem.
func TestReviewConsentAnswerReadsEnvelopeFromStdin(t *testing.T) {
	choices := reviewConsentAnswerChoices()
	envelope := ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchemaV3, Choices: choices}
	payload := reviewConsentAnswerEnvelopePayload(t, envelope)

	var stdout bytes.Buffer
	if err := runReviewConsentAnswer([]string{"--envelope", "-", "--answer", "first"}, bytes.NewReader(payload), &stdout); err != nil {
		t.Fatalf("runReviewConsentAnswer --envelope -: %v", err)
	}
	if stdout.String() != choices[0].Invocation+"\n" {
		t.Fatalf("stdin envelope stdout = %q, want %q", stdout.String(), choices[0].Invocation+"\n")
	}
}

// TestReviewConsentAnswerRefusesUnresolvedAnswer proves zero-match and
// ambiguous inputs are refused with the distinct exit code and empty stdout.
func TestReviewConsentAnswerRefusesUnresolvedAnswer(t *testing.T) {
	envelope := ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchemaV3, Choices: reviewConsentAnswerChoices()}
	path := reviewConsentAnswerWriteEnvelope(t, reviewConsentAnswerEnvelopePayload(t, envelope))

	for _, answer := range []string{"yes", "3"} {
		var stdout bytes.Buffer
		err := runReviewConsentAnswer([]string{"--envelope", path, "--answer", answer}, strings.NewReader(""), &stdout)
		var refused *ReviewConsentAnswerRefusalError
		if !errors.As(err, &refused) {
			t.Fatalf("answer %q: error = %v, want refusal", answer, err)
		}
		if refused.ExitCode() != reviewConsentAnswerExitCodeRefused {
			t.Fatalf("answer %q: ExitCode() = %d, want %d", answer, refused.ExitCode(), reviewConsentAnswerExitCodeRefused)
		}
		if stdout.Len() != 0 {
			t.Fatalf("answer %q: stdout = %q, want empty", answer, stdout.String())
		}
	}
}

// TestReviewConsentAnswerUsageSchemaAndIOFailures fixes the exit-1 class:
// missing flags, wrong schema, absent choices, malformed JSON, and unreadable
// paths are ordinary errors, never refusals.
func TestReviewConsentAnswerUsageSchemaAndIOFailures(t *testing.T) {
	withEnvelope := func(t *testing.T, result ReviewIntegrationConsentResult, answer string) []string {
		t.Helper()
		return []string{"--envelope", reviewConsentAnswerWriteEnvelope(t, reviewConsentAnswerEnvelopePayload(t, result)), "--answer", answer}
	}
	tests := []struct {
		name    string
		args    func(t *testing.T) []string
		wantErr string
	}{
		{name: "missing envelope flag", args: func(*testing.T) []string { return []string{"--answer", "1"} }, wantErr: "requires --envelope"},
		{name: "missing answer flag", args: func(*testing.T) []string { return []string{"--envelope", "unused.json"} }, wantErr: "requires --answer"},
		{name: "wrong schema", args: func(t *testing.T) []string {
			return withEnvelope(t, ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchema, Choices: reviewConsentAnswerChoices()}, "1")
		}, wantErr: "unsupported consent envelope schema"},
		{name: "no choices", args: func(t *testing.T) []string {
			return withEnvelope(t, ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchemaV3}, "1")
		}, wantErr: "presents no choices"},
		{name: "malformed json", args: func(t *testing.T) []string {
			return []string{"--envelope", reviewConsentAnswerWriteEnvelope(t, []byte("{")), "--answer", "1"}
		}, wantErr: "decode consent envelope"},
		{name: "missing file", args: func(t *testing.T) []string {
			return []string{"--envelope", filepath.Join(t.TempDir(), "absent.json"), "--answer", "1"}
		}, wantErr: "read consent envelope"},
		{name: "empty invocation", args: func(t *testing.T) []string {
			return withEnvelope(t, ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchemaV3, Choices: []ReviewIntegrationConsentChoice{{Label: "Review"}}}, "Review")
		}, wantErr: "carries no provider-owned invocation"},
		{name: "whitespace-only invocation", args: func(t *testing.T) []string {
			return withEnvelope(t, ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchemaV3, Choices: []ReviewIntegrationConsentChoice{{Label: "Review", Invocation: "   "}}}, "Review")
		}, wantErr: "carries no provider-owned invocation"},
		{name: "unexpected positional argument", args: func(*testing.T) []string {
			return []string{"--envelope", "unused.json", "--answer", "1", "extra"}
		}, wantErr: "unexpected review consent-answer argument"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := runReviewConsentAnswer(test.args(t), strings.NewReader(""), &stdout)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
			var refused *ReviewConsentAnswerRefusalError
			if errors.As(err, &refused) {
				t.Fatalf("error %v classified as a refusal; usage/schema/IO failures must stay exit 1", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty on failure", stdout.String())
			}
		})
	}
}

// TestReviewConsentAnswerDispatchesThroughReviewFacade proves the verb is wired
// into the `review` subcommand help and dispatch, not only callable directly.
func TestReviewConsentAnswerDispatchesThroughReviewFacade(t *testing.T) {
	envelope := ReviewIntegrationConsentResult{Schema: ReviewIntegrationConsentSchemaV3, Choices: reviewConsentAnswerChoices()}
	path := reviewConsentAnswerWriteEnvelope(t, reviewConsentAnswerEnvelopePayload(t, envelope))

	var stdout bytes.Buffer
	if err := RunReview([]string{"consent-answer", "--envelope", path, "--answer", "Skip this time"}, &stdout); err != nil {
		t.Fatalf("RunReview consent-answer: %v", err)
	}
	if stdout.String() != reviewConsentAnswerChoices()[1].Invocation+"\n" {
		t.Fatalf("facade stdout = %q", stdout.String())
	}

	var help bytes.Buffer
	if err := RunReview([]string{"help"}, &help); err != nil {
		t.Fatalf("RunReview help: %v", err)
	}
	if !strings.Contains(help.String(), "consent-answer") {
		t.Fatalf("review help does not list consent-answer: %q", help.String())
	}
}
