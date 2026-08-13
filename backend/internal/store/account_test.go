package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestAccountStoresPersistAndCloneState(t *testing.T) {
	ctx := context.Background()
	state := &model.AccountState{Authenticated: true, Profile: model.AccountProfile{ID: "user_1", DisplayName: "Jonathan Zhang", Initials: "JZ", UpdatedAt: time.Now().UTC()}, Plan: model.AccountPlan{ID: "pro", Name: "Pro", CreditsIncluded: 100, CreditsUsed: 25, CreditsRemaining: 75}}
	for name, accountStore := range map[string]AccountStore{
		"memory": NewMemoryAccountStore(),
		"file":   NewFileAccountStore(filepath.Join(t.TempDir(), "account", "account.json")),
	} {
		t.Run(name, func(t *testing.T) {
			if err := accountStore.Save(ctx, state); err != nil {
				t.Fatal(err)
			}
			loaded, err := accountStore.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Profile.DisplayName != "Jonathan Zhang" || loaded.Plan.CreditsRemaining != 75 {
				t.Fatalf("unexpected account state: %#v", loaded)
			}
			loaded.Profile.DisplayName = "Changed"
			reloaded, err := accountStore.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Profile.DisplayName != "Jonathan Zhang" {
				t.Fatal("account store returned shared mutable state")
			}
		})
	}
}
