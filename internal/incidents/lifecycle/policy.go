// Package lifecycle contains the deterministic incident freshness policy.
package lifecycle

import (
	"fmt"
	"time"
)

const PolicyVersion = "signa.incident-lifecycle.v1"

const (
	Open      = "OPEN"
	Resolving = "RESOLVING"
	Resolved  = "RESOLVED"
	Expired   = "EXPIRED"
)

type Policy struct {
	ResolvingAfter time.Duration
	ResolvedAfter  time.Duration
	ExpiredAfter   time.Duration
}

type Incident struct {
	Status     string
	ActivityAt time.Time
	ResolvedAt *time.Time
	ExpiresAt  *time.Time
}

type Decision struct {
	Status     string
	ActivityAt time.Time
	Age        time.Duration
	ExpiresAt  time.Time
	ResolvedAt *time.Time
}

func (p Policy) Validate() error {
	if p.ResolvingAfter <= 0 || p.ResolvedAfter <= 0 || p.ExpiredAfter <= 0 {
		return fmt.Errorf("lifecycle thresholds must be positive")
	}
	if p.ResolvingAfter >= p.ResolvedAfter || p.ResolvedAfter >= p.ExpiredAfter {
		return fmt.Errorf("lifecycle thresholds must be strictly increasing")
	}
	return nil
}

func Evaluate(now time.Time, incident Incident, policy Policy) (Decision, error) {
	if err := policy.Validate(); err != nil {
		return Decision{}, err
	}
	if incident.ActivityAt.IsZero() {
		return Decision{}, fmt.Errorf("incident activity time is required")
	}
	if incident.Status != Open && incident.Status != Resolving && incident.Status != Resolved && incident.Status != Expired {
		return Decision{}, fmt.Errorf("unsupported incident lifecycle status %q", incident.Status)
	}
	now = now.UTC()
	activityAt := incident.ActivityAt.UTC()
	age := now.Sub(activityAt)
	if age < 0 {
		age = 0
	}
	expiresAt := activityAt.Add(policy.ExpiredAfter)
	var status string
	switch incident.Status {
	case Expired:
		status = Expired
	case Resolved:
		if now.Sub(activityAt) >= policy.ExpiredAfter {
			status = Expired
		} else {
			status = Resolved
		}
	default:
		switch {
		case now.Sub(activityAt) >= policy.ExpiredAfter:
			status = Expired
		case now.Sub(activityAt) >= policy.ResolvedAfter:
			status = Resolved
		case now.Sub(activityAt) >= policy.ResolvingAfter:
			status = Resolving
		default:
			status = Open
		}
	}

	var resolvedAt *time.Time
	switch status {
	case Resolved:
		if incident.Status == Resolved && incident.ResolvedAt != nil {
			value := incident.ResolvedAt.UTC()
			resolvedAt = &value
		} else {
			value := activityAt.Add(policy.ResolvedAfter)
			resolvedAt = &value
		}
	case Expired:
		if incident.ResolvedAt != nil {
			value := incident.ResolvedAt.UTC()
			resolvedAt = &value
		}
	}
	return Decision{Status: status, ActivityAt: activityAt, Age: age, ExpiresAt: expiresAt, ResolvedAt: resolvedAt}, nil
}
