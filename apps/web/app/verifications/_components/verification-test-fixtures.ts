import type { VerificationRequestView } from "../../../lib/api/generated";

export const requestId = "11111111-1111-4111-8111-111111111111";
export const verificationRequest: VerificationRequestView = {
  id: requestId,
  created_at: "2026-09-29T09:00:00Z",
  expires_at: "2026-09-29T11:00:00Z",
  safety_prompt:
    "Respond only from what you already safely know or observed. Never approach an incident or unsafe area to verify it.",
  incident: {
    id: "22222222-2222-4222-8222-222222222222",
    event_type: "ROAD_CLOSURE",
    status: "OPEN",
    confidence_state: "EMERGING",
    severity: "MODERATE",
    public_geometry: {
      type: "Polygon",
      coordinates: [
        [
          [3.37, 6.52],
          [3.38, 6.52],
          [3.38, 6.53],
          [3.37, 6.52],
        ],
      ],
    },
    started_at: null,
    last_signal_at: null,
    updated_at: "2026-09-29T09:00:00Z",
  },
};
