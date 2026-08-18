package model

import "testing"

func TestDefaultMediaDeliveryPreferencesUsesApprovedDefaults(t *testing.T) {
	pref := DefaultMediaDeliveryPreferences()
	if pref.Narration.Mode != MediaNarrationModeAutoByProductStyle || pref.TOSRetention.RetentionDays != 30 || pref.DefaultCandidateProvider != MediaCandidateProviderSeedance {
		t.Fatalf("unexpected defaults: %+v", pref)
	}
	if err := ValidateMediaDeliveryPreferences(&pref); err != nil {
		t.Fatal(err)
	}
}

func TestMediaDeliveryPreferencesRejectsUnapprovedProviderAndRetention(t *testing.T) {
	pref := DefaultMediaDeliveryPreferences()
	pref.DefaultCandidateProvider = "other"
	if err := ValidateMediaDeliveryPreferences(&pref); err == nil {
		t.Fatal("expected provider rejection")
	}
	pref = DefaultMediaDeliveryPreferences()
	pref.TOSRetention.RetentionDays = 7
	if err := ValidateMediaDeliveryPreferences(&pref); err == nil {
		t.Fatal("expected standard retention rejection")
	}
}
