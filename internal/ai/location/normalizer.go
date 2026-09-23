package location

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

// RuleBasedNormalizer performs only transformations supported by the extracted
// wording. It is deterministic and intentionally does not consult a map or
// infer coordinates.
type RuleBasedNormalizer struct{}

// Normalize implements Provider.
func (RuleBasedNormalizer) Normalize(ctx context.Context, field extraction.Field) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateLocationReference(field); err != nil {
		return nil, fmt.Errorf("validate input location reference: %w", err)
	}
	normalization := Normalization{
		ContractVersion:         ContractVersion,
		InputContractVersion:    InputContractVersion,
		LocationState:           field.Status,
		SourceLocationReference: cloneField(field),
		Candidates:              []Candidate{},
	}
	if field.Status == "unknown" {
		return marshalNormalization(normalization)
	}

	values := make([]string, 0, 1)
	if field.Status == "identified" {
		values = append(values, *field.Value)
	} else {
		values = append(values, field.Candidates...)
	}
	for _, value := range values {
		reportedText := strings.TrimSpace(value)
		normalizedText, qualifier := normalizeText(reportedText)
		normalization.Candidates = append(normalization.Candidates, Candidate{
			ReportedText:   reportedText,
			NormalizedText: normalizedText,
			ReferenceKind:  classifyReference(normalizedText),
			Qualifier:      qualifier,
			EvidenceQuotes: append([]string(nil), field.EvidenceQuotes...),
		})
	}
	return marshalNormalization(normalization)
}

var locationQualifiers = []string{
	"in front of",
	"opposite",
	"outside",
	"inside",
	"towards",
	"around",
	"behind",
	"between",
	"along",
	"under",
	"near",
	"beside",
	"at",
	"by",
	"in",
	"on",
}

func normalizeText(reported string) (string, *string) {
	parts := strings.Fields(reported)
	if len(parts) == 0 {
		return reported, nil
	}
	joined := strings.Join(parts, " ")
	var qualifier *string
	for _, candidate := range locationQualifiers {
		candidateParts := strings.Fields(candidate)
		if len(parts) < len(candidateParts) || !equalFold(parts[:len(candidateParts)], candidateParts) {
			continue
		}
		value := candidate
		qualifier = &value
		parts = parts[len(candidateParts):]
		break
	}
	if len(parts) > 0 && (strings.EqualFold(parts[0], "the") || strings.EqualFold(parts[0], "a") || strings.EqualFold(parts[0], "an")) {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return joined, qualifier
	}
	return strings.Join(parts, " "), qualifier
}

func equalFold(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !strings.EqualFold(left[index], right[index]) {
			return false
		}
	}
	return true
}

func classifyReference(normalized string) string {
	words := strings.Fields(strings.ToLower(normalized))
	if containsWord(words, "market") {
		return "market"
	}
	if containsWord(words, "junction") {
		return "junction"
	}
	if containsAnyWord(words, "bridge", "mall", "stadium", "airport", "station", "church", "mosque", "school") {
		return "landmark"
	}
	if containsAnyWord(words, "road", "street", "avenue", "expressway", "highway", "lane") {
		return "road"
	}
	if containsAnyWord(words, "estate", "island", "district", "neighborhood", "neighbourhood") {
		return "area"
	}
	if startsWithUppercase(normalized) {
		return "named_place"
	}
	return "unknown"
}

func containsWord(words []string, want string) bool {
	for _, word := range words {
		if word == want {
			return true
		}
	}
	return false
}

func containsAnyWord(words []string, wants ...string) bool {
	for _, want := range wants {
		if containsWord(words, want) {
			return true
		}
	}
	return false
}

func startsWithUppercase(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) {
			continue
		}
		return unicode.IsUpper(r)
	}
	return false
}
