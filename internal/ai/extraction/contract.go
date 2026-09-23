package extraction

// Extraction is the versioned, evidence-only interpretation of one report.
// It is not incident truth, confidence, final severity, priority, or alert policy.
type Extraction struct {
	ContractVersion   string `json:"contract_version"`
	TaxonomyVersion   string `json:"taxonomy_version"`
	EventType         Field  `json:"event_type"`
	LocationReference Field  `json:"location_reference"`
	TimeReference     Field  `json:"time_reference"`
	SourceClaim       Field  `json:"source_claim"`
	Language          Field  `json:"language"`
	SeverityCandidate Field  `json:"severity_candidate"`
}

type Field struct {
	Status         string   `json:"status"`
	Value          *string  `json:"value"`
	Candidates     []string `json:"candidates"`
	EvidenceQuotes []string `json:"evidence_quotes"`
}
