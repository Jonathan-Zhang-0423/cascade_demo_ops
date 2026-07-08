package storage

import (
	"net/url"
	"path/filepath"
	"strings"
)

type LocalLayout struct {
	DataRoot     string
	ArtifactRoot string
	CacheRoot    string
	LogRoot      string
}

func NewLocalLayout(dataRoot string, artifactRoot string, cacheRoot string, logRoot string) LocalLayout {
	return LocalLayout{
		DataRoot:     filepath.Clean(dataRoot),
		ArtifactRoot: filepath.Clean(artifactRoot),
		CacheRoot:    filepath.Clean(cacheRoot),
		LogRoot:      filepath.Clean(logRoot),
	}
}

func (l LocalLayout) ProjectArtifactDir(projectID string) string {
	return filepath.Join(l.ArtifactRoot, sanitizePathPart(projectID))
}

func (l LocalLayout) ArtifactPath(projectID string, fileName string) string {
	return filepath.Join(l.ProjectArtifactDir(projectID), sanitizePathPart(fileName))
}

func (l LocalLayout) ArtifactURI(projectID string, fileName string) string {
	path := filepath.ToSlash(l.ArtifactPath(projectID, fileName))
	if strings.HasPrefix(path, "/") {
		return "file://" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func (l LocalLayout) ProjectCacheDir(projectID string) string {
	return filepath.Join(l.CacheRoot, sanitizePathPart(projectID))
}

func (l LocalLayout) AppLogDir() string {
	return l.LogRoot
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "..", "_")
	return value
}
