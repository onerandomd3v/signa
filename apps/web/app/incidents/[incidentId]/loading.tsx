import { AppShell } from "../../_components/app-shell";
import {
  IncidentDetail,
  IncidentPageFrame,
} from "../_components/incident-views";

export default function LoadingIncidentDetailPage() {
  return (
    <AppShell>
      <IncidentPageFrame>
        <IncidentDetail state={{ status: "loading" }} />
      </IncidentPageFrame>
    </AppShell>
  );
}
