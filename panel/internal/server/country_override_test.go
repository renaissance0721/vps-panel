package server

import "testing"

func TestCountryOverrideIndependentOfSystemInfo(t *testing.T) {
	s, db := newTestService(t)
	ctx := t.Context()
	created, err := s.Create(ctx, "country")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.RegisterAgent(ctx, created.EnrollmentToken, "v1.0.0", false)
	if err != nil {
		t.Fatal(err)
	}
	report := SystemInfoReport{Hostname: "host", PublicIPv4: "198.51.100.1"}
	if _, err := s.ReportSystemInfo(ctx, agent.ID, created.ID, report); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET country_code = 'JP' WHERE server_id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	code := "DE"
	updated, err := s.UpdateCountryOverride(ctx, created.ID, &code)
	if err != nil || updated.SystemInfo == nil || updated.SystemInfo.CountryCode != "JP" || updated.CountryOverrideCode == nil || *updated.CountryOverrideCode != "DE" {
		t.Fatalf("override lost geoip: %+v %v", updated, err)
	}
	report.PublicIPv4 = "198.51.100.2"
	if _, err := s.ReportSystemInfo(ctx, agent.ID, created.ID, report); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET country_code = 'SG' WHERE server_id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	updated, err = s.Get(ctx, created.ID)
	if err != nil || updated.SystemInfo.CountryCode != "SG" || updated.CountryOverrideCode == nil || *updated.CountryOverrideCode != "DE" {
		t.Fatalf("geoip lost override: %+v %v", updated, err)
	}
	updated, err = s.UpdateCountryOverride(ctx, created.ID, nil)
	if err != nil || updated.CountryOverrideCode != nil || updated.SystemInfo.CountryCode != "SG" {
		t.Fatalf("clear: %+v %v", updated, err)
	}
}
