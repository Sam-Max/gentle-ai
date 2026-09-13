package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// reviewConsentAnswerExitCodeRefused is the distinct exit code the
// consent-answer verb assigns to a REFUSED answer (zero or more than one
// match), separate from the usage/IO/schema failure class (1). The refinement
// travels on the typed error and cmd/gentle-ai honours an ExitCode() int
// method before falling back to 1, so the two classes stay distinguishable to
// the orchestrator that has to branch on them.
const reviewConsentAnswerExitCodeRefused = 2

// ReviewConsentAnswerRefusalError reports that a raw human answer did not
// resolve to exactly one presented consent choice, so no provider-owned
// invocation may be selected. Matches holds the zero-based indices the answer
// resolved to: empty for zero matches, several for an ambiguous answer.
type ReviewConsentAnswerRefusalError struct {
	Answer  string
	Matches []int
}

// Error states why the answer was refused and what the orchestrator must do:
// re-present the COMPLETE envelope unchanged and keep waiting, never choose,
// default, or infer.
func (err *ReviewConsentAnswerRefusalError) Error() string {
	detail := "matches no presented choice"
	if len(err.Matches) > 1 {
		detail = "matches more than one presented choice"
	}
	return fmt.Sprintf("review consent-answer: the answer %q %s; the orchestrator must re-present the COMPLETE consent envelope unchanged and keep waiting -- never choose, default, or infer", err.Answer, detail)
}

// ExitCode reports the distinct refusal exit-code class (2), so a caller can
// tell a refused answer apart from a malformed request (exit 1).
func (err *ReviewConsentAnswerRefusalError) ExitCode() int { return reviewConsentAnswerExitCodeRefused }

// ResolveReviewConsentAnswer resolves one human's raw answer against the
// ordered labels of a consent envelope's presented choices and returns the
// zero-based index of the single choice the answer names.
//
// The domain is deliberately closed. An answer resolves only to a presented
// label (case-insensitive, surrounding whitespace ignored) or to an ordinal
// alias of a presented option, 1-based in the envelope's original order: the
// bare numeral "N" (digits only, so a signed "+2" is not an alias), "la N",
// "opción N", or "first" for option 1. An alias counts only when it names
// exactly one presented option. Provider-owned answer tokens (for example a
// raw "granted" or "declined" value) never resolve on their own; one resolves
// only when it is also a presented label. Zero matches or more than one match
// returns a *ReviewConsentAnswerRefusalError.
//
// This function is the single source of truth for the alias rules. The
// orchestrator prompt clause in internal/components/sdd/orchestrator.go must
// be kept in sync with it.
func ResolveReviewConsentAnswer(labels []string, answer string) (int, error) {
	trimmed := strings.TrimSpace(answer)
	matches := map[int]bool{}
	if trimmed != "" {
		for index, label := range labels {
			if strings.EqualFold(trimmed, strings.TrimSpace(label)) {
				matches[index] = true
			}
		}
		if ordinal, ok := reviewConsentOrdinalAlias(trimmed); ok && ordinal >= 1 && ordinal <= len(labels) {
			matches[ordinal-1] = true
		}
	}
	if len(matches) == 1 {
		for index := range matches {
			return index, nil
		}
	}
	ordered := make([]int, 0, len(matches))
	for index := range matches {
		ordered = append(ordered, index)
	}
	sort.Ints(ordered)
	return -1, &ReviewConsentAnswerRefusalError{Answer: answer, Matches: ordered}
}

// reviewConsentOrdinalAlias parses the accepted ordinal aliases ("N", "la N",
// "opción N", "first") into a 1-based option number. It reports false when the
// answer is not an ordinal alias at all; range is checked by the caller. The
// numeral must be digits only, so "+2" is refused rather than treated as a
// synonym for "2".
func reviewConsentOrdinalAlias(answer string) (int, bool) {
	lowered := strings.ToLower(strings.TrimSpace(answer))
	if lowered == "first" {
		return 1, true
	}
	numeral := lowered
	for _, prefix := range []string{"la ", "opción "} {
		if strings.HasPrefix(numeral, prefix) {
			numeral = strings.TrimSpace(strings.TrimPrefix(numeral, prefix))
			break
		}
	}
	if !reviewConsentIsBareNumeral(numeral) {
		return 0, false
	}
	value, err := strconv.Atoi(numeral)
	if err != nil {
		return 0, false
	}
	return value, true
}

// reviewConsentIsBareNumeral reports whether value is one or more ASCII digits
// and nothing else -- the only shape the "bare numeral N" alias admits. A
// zero-padded numeral such as "01" names the same option as "1"; a signed form
// such as "+2" does not, so strconv.Atoi's sign handling can never widen the
// closed domain on its own.
func reviewConsentIsBareNumeral(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// RunReviewConsentAnswer is the `gentle-ai review consent-answer` entry point.
// It reads one gentle-ai.review-integration.consent/v3 envelope (from
// --envelope, or from stdin when --envelope is -), resolves the human's raw
// --answer against the presented choices, and prints the exact provider-owned
// invocation of the single choice it names. A refused answer returns a
// *ReviewConsentAnswerRefusalError; usage, IO, schema, and parse failures
// return an ordinary error. stdout carries the invocation line and the flag
// set's own usage; every diagnostic is an error for the caller to surface.
func RunReviewConsentAnswer(args []string, stdout io.Writer) error {
	return runReviewConsentAnswer(args, os.Stdin, stdout)
}

// runReviewConsentAnswer is RunReviewConsentAnswer's testable form: stdin and
// stdout are explicit so a test can supply an envelope and assert on the
// invocation line without touching process streams. The flag set writes to
// stdout because newReviewFlagSet's own parameter contract is stdout and every
// sibling review verb passes it: TestEveryNamedReviewContinuationIsStructurallyReal
// reads a verb's flags from that route, so routing usage to stderr would hide
// this verb's flags from the shared guard.
func runReviewConsentAnswer(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := newReviewFlagSet("review consent-answer", stdout,
		"Resolve one human answer against the presented choices of a gentle-ai.review-integration.consent/v3 envelope and print the exact provider-owned invocation for the single choice it names.")
	envelopePath := flags.String("envelope", "", "path to the JSON consent envelope, or - to read the envelope from stdin")
	answer := flags.String("answer", "", "the human's raw answer text")
	if err := parseReviewFlags(flags, args); err != nil {
		return err
	}
	if reviewHelpRequested(args) {
		return nil
	}
	if flags.NArg() != 0 {
		return reviewPreflightError(fmt.Errorf("unexpected review consent-answer argument %q; run 'gentle-ai review consent-answer --help' for the invocation", flags.Arg(0)))
	}
	if !reviewFlagWasProvided(flags, "envelope") {
		return reviewPreflightError(errors.New("review consent-answer requires --envelope <path|->; run 'gentle-ai review consent-answer --help' for the invocation"))
	}
	if !reviewFlagWasProvided(flags, "answer") {
		return reviewPreflightError(errors.New("review consent-answer requires --answer <text>; run 'gentle-ai review consent-answer --help' for the invocation"))
	}
	raw, err := readReviewConsentAnswerEnvelope(*envelopePath, stdin)
	if err != nil {
		return err
	}
	var envelope ReviewIntegrationConsentResult
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("review consent-answer: decode consent envelope: %w", err)
	}
	if envelope.Schema != ReviewIntegrationConsentSchemaV3 {
		return fmt.Errorf("review consent-answer: unsupported consent envelope schema %q (want %s); re-derive it with gentle-ai review status --next-transition", envelope.Schema, ReviewIntegrationConsentSchemaV3)
	}
	if len(envelope.Choices) == 0 {
		return errors.New("review consent-answer: consent envelope presents no choices; re-derive it with gentle-ai review status --next-transition")
	}
	labels := make([]string, len(envelope.Choices))
	for index, choice := range envelope.Choices {
		labels[index] = choice.Label
	}
	choiceIndex, err := ResolveReviewConsentAnswer(labels, *answer)
	if err != nil {
		return err
	}
	// The invocation is taken verbatim from the choice the envelope already
	// carries: the verb neither rebuilds nor normalizes the provider-owned
	// bytes. reviewConsentChoiceInvocation is the builder's single construction
	// site, and validateReviewConsentInvocations proves at build time that the
	// bytes reaching this verb are the provider-owned request, so printing them
	// unchanged is what keeps the printed line and the validated line identical.
	invocation := envelope.Choices[choiceIndex].Invocation
	if strings.TrimSpace(invocation) == "" {
		return fmt.Errorf("review consent-answer: presented choice %q carries no provider-owned invocation", labels[choiceIndex]) // refusal:by-design world-action: the envelope builder always renders a choice invocation and validateReviewConsentInvocations enforces it, so a bare choice cannot come from the provider; the exit is a code fix, not a command
	}
	_, err = fmt.Fprintln(stdout, invocation)
	return err
}

// readReviewConsentAnswerEnvelope reads the consent envelope from path, or from
// stdin when path is "-".
func readReviewConsentAnswerEnvelope(path string, stdin io.Reader) ([]byte, error) {
	if path != "-" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("review consent-answer: read consent envelope %q: %w", path, err)
		}
		return raw, nil
	}
	if stdin == nil {
		return nil, errors.New("review consent-answer: standard input is unavailable for --envelope -") // refusal:by-design world-action: the process entry point always supplies os.Stdin, so only a caller that deliberately passed no reader reaches this branch; the exit is a code fix, not a command
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("review consent-answer: read consent envelope from stdin: %w", err)
	}
	return raw, nil
}
