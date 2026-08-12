package app

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/model"
)

// browserAgentCredentialResolver is deliberately narrower than the Direct
// credential envelope. The Browser Agent can resolve only an already approved
// opaque secret_ref; it never receives job, lease, installation, or scope
// metadata and cannot enumerate available secrets.
type browserAgentCredentialResolver interface {
	Resolve(secretRef string) (string, error)
	Destroy()
}

type oneTimeBrowserAgentCredentialBroker struct {
	mu        sync.Mutex
	secretRef string
	secret    []byte
	destroyed bool
}

func newOneTimeBrowserAgentCredentialBroker(secretRef, secret string) (*oneTimeBrowserAgentCredentialBroker, error) {
	secretRef = strings.TrimSpace(secretRef)
	if secretRef == "" || secret == "" {
		return nil, errors.New("browser-agent credential broker requires secret_ref and secret")
	}
	return &oneTimeBrowserAgentCredentialBroker{secretRef: secretRef, secret: []byte(secret)}, nil
}

func (b *oneTimeBrowserAgentCredentialBroker) Resolve(secretRef string) (string, error) {
	if b == nil {
		return "", errors.New("browser-agent credential broker is unavailable")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.destroyed || len(b.secret) == 0 {
		return "", errors.New("browser-agent credential broker has been destroyed")
	}
	if strings.TrimSpace(secretRef) != b.secretRef {
		return "", errors.New("browser-agent secret_ref is outside the consumed credential envelope")
	}
	return string(b.secret), nil
}

func (b *oneTimeBrowserAgentCredentialBroker) Destroy() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for index := range b.secret {
		b.secret[index] = 0
	}
	b.secret = nil
	b.secretRef = ""
	b.destroyed = true
}

func directPackageSecretRefs(pkg *model.ClientExecutionPackage) []string {
	if pkg == nil || pkg.ExecutableScriptBundle == nil {
		return nil
	}
	refs := map[string]bool{}
	add := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			refs[value] = true
		}
	}
	bundle := pkg.ExecutableScriptBundle
	if bundle.PlanJSON != nil {
		for _, step := range bundle.PlanJSON.Steps {
			add(step.Action.SecretRef)
		}
	}
	if bundle.StageApprovalPlan != nil {
		for _, stage := range bundle.StageApprovalPlan.Stages {
			add(stage.Interaction.SecretRef)
			for _, input := range stage.InputContent {
				add(input.SecretRef)
			}
		}
	}
	if bundle.ScriptOutline != nil {
		for _, stage := range bundle.ScriptOutline.Stages {
			for _, interaction := range stage.Interactions {
				add(interaction.SecretRef)
			}
		}
	}
	values := make([]string, 0, len(refs))
	for value := range refs {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func validateDirectPackageCredentialRefs(pkg *model.ClientExecutionPackage) error {
	refs := directPackageSecretRefs(pkg)
	if len(refs) > 1 {
		return errors.New("direct execution currently supports one distinct approved secret_ref per job")
	}
	for _, ref := range refs {
		approved := false
		for _, grant := range pkg.CredentialGrants {
			if ref == strings.TrimSpace(grant.CloudSecretRef) || ref == strings.TrimSpace(grant.EncryptedSecretAttachmentID) {
				approved = true
				break
			}
		}
		if !approved {
			return errors.New("direct execution secret_ref is not backed by an approved credential grant")
		}
	}
	return nil
}

func browserAgentStageSecretValues(stage driver.BrowserAgentWorkerStage, resolver browserAgentCredentialResolver) (map[string]string, error) {
	refs := map[string]bool{}
	for _, interaction := range stage.Interactions {
		if ref := strings.TrimSpace(interaction.SecretRef); ref != "" {
			refs[ref] = true
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	if resolver == nil {
		return nil, errors.New("browser-agent stage requires a credential broker")
	}
	values := make(map[string]string, len(refs))
	for ref := range refs {
		value, err := resolver.Resolve(ref)
		if err != nil {
			clearBrowserAgentStageSecrets(values)
			return nil, err
		}
		values[ref] = value
	}
	return values, nil
}

func clearBrowserAgentStageSecrets(values map[string]string) {
	for ref := range values {
		values[ref] = ""
		delete(values, ref)
	}
}
