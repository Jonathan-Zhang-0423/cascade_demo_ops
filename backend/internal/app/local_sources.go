package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type LocalSourceRef struct {
	Ref   string `json:"ref"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Path  string `json:"path,omitempty"`
}

func (s *Service) RegisterLocalSource(kind, path string) (LocalSourceRef, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." || !filepath.IsAbs(path) {
		return LocalSourceRef{}, errors.New("an absolute local source path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return LocalSourceRef{}, err
	}
	if kind == "local_repository" && !info.IsDir() {
		return LocalSourceRef{}, errors.New("local repository source must be a directory")
	}
	if kind != "local_repository" && info.IsDir() {
		return LocalSourceRef{}, errors.New("document and brand sources must be files")
	}
	digest := sha256.Sum256([]byte(kind + "\x00" + path))
	ref := LocalSourceRef{Ref: "source_" + hex.EncodeToString(digest[:12]), Kind: kind, Label: filepath.Base(path), Path: path}
	s.sourceRefsMu.Lock()
	s.sourceRefs[ref.Ref] = ref
	err = s.persistLocalSourceRefsLocked()
	s.sourceRefsMu.Unlock()
	if err != nil {
		return LocalSourceRef{}, err
	}
	return LocalSourceRef{Ref: ref.Ref, Kind: ref.Kind, Label: ref.Label}, nil
}

func (s *Service) ResolveLocalSourceRef(ref string) (LocalSourceRef, bool) {
	s.sourceRefsMu.RLock()
	value, ok := s.sourceRefs[ref]
	s.sourceRefsMu.RUnlock()
	return value, ok
}

func (s *Service) loadLocalSourceRefs() {
	path := s.localSourceRefPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var refs map[string]LocalSourceRef
	if json.Unmarshal(data, &refs) == nil {
		s.sourceRefsMu.Lock()
		s.sourceRefs = refs
		s.sourceRefsMu.Unlock()
	}
}

func (s *Service) persistLocalSourceRefsLocked() error {
	path := s.localSourceRefPath()
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.sourceRefs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		return os.Rename(tmp, path)
	}
	return nil
}

func (s *Service) localSourceRefPath() string {
	if strings.TrimSpace(s.runtime.DataRoot) == "" {
		return ""
	}
	return filepath.Join(s.runtime.DataRoot, "local_source_refs", "refs.json")
}
