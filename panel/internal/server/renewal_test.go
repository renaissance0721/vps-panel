package server

import (
	"errors"
	"testing"
	"time"
)

func renewalExpiration(t *testing.T, date string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02", date, renewalLocation)
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, renewalLocation).UTC()
}

func TestNextRenewalExpirationPreservesAnchorAcrossMonthEnds(t *testing.T) {
	tests := []struct {
		name     string
		start    string
		period   int
		anchor   int
		expected []string
	}{
		{"common year monthly", "2026-01-31", 1, 31, []string{"2026-02-28", "2026-03-31"}},
		{"leap year monthly", "2028-01-31", 1, 31, []string{"2028-02-29", "2028-03-31"}},
		{"quarterly", "2026-11-30", 3, 30, []string{"2027-02-28"}},
		{"half yearly", "2026-08-31", 6, 31, []string{"2027-02-28"}},
		{"yearly leap anchor", "2028-02-29", 12, 29, []string{"2029-02-28", "2030-02-28", "2031-02-28", "2032-02-29"}},
		{"two yearly", "2028-02-29", 24, 29, []string{"2030-02-28"}},
		{"three yearly", "2028-02-29", 36, 29, []string{"2031-02-28"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := renewalExpiration(t, test.start)
			for _, expected := range test.expected {
				var err error
				current, err = nextRenewalExpiration(current, test.period, test.anchor)
				if err != nil {
					t.Fatal(err)
				}
				if want := renewalExpiration(t, expected); !current.Equal(want) {
					t.Fatalf("next expiration = %v, want %v", current, want)
				}
			}
		})
	}
}

func TestUpdateRenewalSettingsValidationAndNormalization(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(t.Context(), "Renewal settings")
	if err != nil {
		t.Fatal(err)
	}

	for _, period := range []int{1, 3, 6, 12, 24, 36} {
		value := period
		if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
			RenewalPeriodSet: true, RenewalPeriodMonths: &value,
		}); err != nil {
			t.Fatalf("valid period %d error = %v", period, err)
		}
	}
	if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{RenewalPeriodSet: true}); err != nil {
		t.Fatalf("null renewal period error = %v", err)
	}
	for _, period := range []int{0, 2, 4, 7, 18, 25, 35, 37, -1} {
		value := period
		if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
			RenewalPeriodSet: true, RenewalPeriodMonths: &value,
		}); !errors.Is(err, ErrInvalidRenewalPeriod) {
			t.Fatalf("invalid period %d error = %v", period, err)
		}
	}
	if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
		AutoRenewSet: true, AutoRenew: true,
	}); !errors.Is(err, ErrAutoRenewRequirements) {
		t.Fatalf("auto renewal without expiration error = %v", err)
	}

	expiresAt := renewalExpiration(t, "2026-10-31")
	if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
		ExpiresAtSet: true, ExpiresAt: &expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
		AutoRenewSet: true, AutoRenew: true,
	}); !errors.Is(err, ErrAutoRenewRequirements) {
		t.Fatalf("auto renewal without period error = %v", err)
	}
	period := 1
	updated, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
		RenewalPeriodSet: true, RenewalPeriodMonths: &period, AutoRenewSet: true, AutoRenew: true,
	})
	if err != nil || updated.RenewalPeriodMonths == nil || *updated.RenewalPeriodMonths != 1 || !updated.AutoRenew {
		t.Fatalf("valid renewal settings = (%+v, %v)", updated, err)
	}
	var anchorDay int
	if err := db.QueryRow(`SELECT renewal_anchor_day FROM servers WHERE id = ?`, created.ID).Scan(&anchorDay); err != nil {
		t.Fatalf("read renewal anchor: %v", err)
	}
	if anchorDay != 31 {
		t.Fatalf("renewal anchor = %d, want 31", anchorDay)
	}
	if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
		AutoRenewSet: true, AutoRenew: false,
	}); err != nil {
		t.Fatal(err)
	}
	var anchorAfterToggle int
	if err := db.QueryRow(`SELECT renewal_anchor_day FROM servers WHERE id = ?`, created.ID).Scan(&anchorAfterToggle); err != nil {
		t.Fatal(err)
	}
	if anchorAfterToggle != anchorDay {
		t.Fatalf("anchor after automatic renewal toggle = %d, want %d", anchorAfterToggle, anchorDay)
	}

	cleared, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{ExpiresAtSet: true})
	if err != nil || cleared.ExpiresAt != nil || cleared.RenewalPeriodMonths != nil || cleared.AutoRenew {
		t.Fatalf("cleared renewal settings = (%+v, %v)", cleared, err)
	}
	var expiration, renewalPeriod, anchor any
	var autoRenew bool
	if err := db.QueryRow(
		`SELECT expires_at, renewal_period_months, auto_renew, renewal_anchor_day FROM servers WHERE id = ?`, created.ID,
	).Scan(&expiration, &renewalPeriod, &autoRenew, &anchor); err != nil {
		t.Fatal(err)
	}
	if expiration != nil || renewalPeriod != nil || autoRenew || anchor != nil {
		t.Fatalf("stored cleared settings = (%v, %v, %t, %v)", expiration, renewalPeriod, autoRenew, anchor)
	}
}

func TestApplyAutomaticRenewalsCatchesUpAndIsIdempotent(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(t.Context(), "Catch up")
	if err != nil {
		t.Fatal(err)
	}
	period := 1
	expiresAt := renewalExpiration(t, "2026-05-31")
	if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
		ExpiresAtSet: true, ExpiresAt: &expiresAt, RenewalPeriodSet: true,
		RenewalPeriodMonths: &period, AutoRenewSet: true, AutoRenew: true,
	}); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, renewalLocation) }
	if err := service.ApplyAutomaticRenewals(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := service.Get(t.Context(), created.ID)
	if err != nil || first.ExpiresAt == nil || !first.ExpiresAt.Equal(renewalExpiration(t, "2026-09-30")) {
		t.Fatalf("caught-up expiration = (%v, %v)", first.ExpiresAt, err)
	}
	if err := service.ApplyAutomaticRenewals(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := service.Get(t.Context(), created.ID)
	if err != nil || second.ExpiresAt == nil || !second.ExpiresAt.Equal(*first.ExpiresAt) || !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("second reconciliation changed server = (%+v, %v)", second, err)
	}
}

func TestApplyAutomaticRenewalsSkipsDisabledAndArchivedServers(t *testing.T) {
	service, _ := newTestService(t)
	period := 1
	oldExpiration := renewalExpiration(t, "2026-05-31")
	for _, test := range []struct {
		name     string
		auto     bool
		archived bool
	}{
		{"disabled", false, false},
		{"archived", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			created, err := service.Create(t.Context(), test.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
				ExpiresAtSet: true, ExpiresAt: &oldExpiration, RenewalPeriodSet: true,
				RenewalPeriodMonths: &period, AutoRenewSet: true, AutoRenew: test.auto,
			}); err != nil {
				t.Fatal(err)
			}
			if test.archived {
				if err := service.Archive(t.Context(), created.ID); err != nil {
					t.Fatal(err)
				}
			}
			service.now = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, renewalLocation) }
			if err := service.ApplyAutomaticRenewals(t.Context()); err != nil {
				t.Fatal(err)
			}
			var stored int64
			if err := service.db.QueryRow(`SELECT expires_at FROM servers WHERE id = ?`, created.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != oldExpiration.Unix() {
				t.Fatalf("stored expiration = %d, want %d", stored, oldExpiration.Unix())
			}
		})
	}
}

func TestManualExpirationUpdateResetsRenewalAnchor(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(t.Context(), "Manual anchor")
	if err != nil {
		t.Fatal(err)
	}
	period := 1
	first := renewalExpiration(t, "2026-02-28")
	if _, err := db.Exec(
		`UPDATE servers SET expires_at = ?, renewal_period_months = 1, auto_renew = 1, renewal_anchor_day = 31 WHERE id = ?`,
		first.Unix(), created.ID,
	); err != nil {
		t.Fatal(err)
	}
	manual := renewalExpiration(t, "2026-03-15")
	if _, err := service.UpdateRenewalSettings(t.Context(), created.ID, RenewalSettingsUpdate{
		ExpiresAtSet: true, ExpiresAt: &manual,
	}); err != nil {
		t.Fatal(err)
	}
	var anchor int
	if err := db.QueryRow(`SELECT renewal_anchor_day FROM servers WHERE id = ?`, created.ID).Scan(&anchor); err != nil {
		t.Fatal(err)
	}
	if anchor != 15 {
		t.Fatalf("renewal anchor = %d, want 15", anchor)
	}
	next, err := nextRenewalExpiration(manual, period, anchor)
	if err != nil || !next.Equal(renewalExpiration(t, "2026-04-15")) {
		t.Fatalf("next expiration = (%v, %v)", next, err)
	}
}
