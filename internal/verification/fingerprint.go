package verification

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

const fingerprintVersion int16 = 1

func fingerprintResponse(input ResponseInput) (int16, [32]byte, error) {
	if input.Conclusion == nil && input.Observation == nil {
		return 0, [32]byte{}, ErrInvalidResponse
	}
	if input.Conclusion != nil {
		switch *input.Conclusion {
		case ConclusionConfirm, ConclusionCannotConfirm, ConclusionDispute:
		default:
			return 0, [32]byte{}, fmt.Errorf("%w: conclusion", ErrInvalidResponse)
		}
	}
	if input.Observation != nil {
		switch *input.Observation {
		case ObservationSaw, ObservationHeard:
		default:
			return 0, [32]byte{}, fmt.Errorf("%w: observation", ErrInvalidResponse)
		}
	}
	canonical := struct {
		Version     int16        `json:"version"`
		Conclusion  *Conclusion  `json:"conclusion"`
		Observation *Observation `json:"observation"`
	}{fingerprintVersion, input.Conclusion, input.Observation}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return 0, [32]byte{}, err
	}
	return fingerprintVersion, sha256.Sum256(encoded), nil
}
