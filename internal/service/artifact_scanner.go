package service

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ArtifactScanner watches ARTIFACTS_PATH and auto-creates DB records for new files.
type ArtifactScanner struct {
	queries       *db.Queries
	bus           *EventBus
	artifactsPath string
	stopCh        chan struct{}
}

// NewArtifactScanner creates a new scanner.
func NewArtifactScanner(queries *db.Queries, bus *EventBus, artifactsPath string) *ArtifactScanner {
	return &ArtifactScanner{
		queries:       queries,
		bus:           bus,
		artifactsPath: artifactsPath,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the periodic scan loop.
func (s *ArtifactScanner) Start(ctx context.Context) {
	// Ensure base directory exists
	if err := os.MkdirAll(s.artifactsPath, 0755); err != nil {
		slog.Error("artifact-scanner: failed to create artifacts dir", "path", s.artifactsPath, "error", err)
	}

	go func() {
		// Initial scan
		s.scan(ctx)

		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.scan(ctx)
			}
		}
	}()

	slog.Info("artifact-scanner: started", "path", s.artifactsPath, "interval", "60s")
}

// Stop signals the scanner to stop.
func (s *ArtifactScanner) Stop() {
	close(s.stopCh)
}

func (s *ArtifactScanner) scan(ctx context.Context) {
	count := 0
	err := filepath.Walk(s.artifactsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return nil
		}

		// Get relative path from artifacts root
		relPath, err := filepath.Rel(s.artifactsPath, path)
		if err != nil {
			return nil
		}

		// Check if already in DB
		_, err = s.queries.GetArtifactByPath(ctx, pgtype.Text{String: relPath, Valid: true})
		if err == nil {
			// Already exists, skip
			return nil
		}

		// Doesn't exist — auto-create
		artifactType := s.detectTypeFromExt(path)
		mimeType := detectMimeFromExtExt(path)

		filename := filepath.Base(path)
		title := strings.TrimSuffix(filename, filepath.Ext(filename))

		artifact, err := s.queries.CreateArtifact(ctx, db.CreateArtifactParams{
			AgentID:     pgtype.UUID{Valid: false},
			Type:        artifactType,
			Title:       pgtype.Text{String: title, Valid: true},
			Description: pgtype.Text{String: "Auto-imported by artifact scanner", Valid: true},
			FilePath:    pgtype.Text{String: relPath, Valid: true},
			MimeType:    pgtype.Text{String: mimeType, Valid: mimeType != ""},
			Metadata:    []byte("{}"),
		})
		if err != nil {
			slog.Error("artifact-scanner: failed to create record", "path", relPath, "error", err)
			return nil
		}

		slog.Info("artifact-scanner: auto-imported", "path", relPath, "type", artifactType, "id", uuid.UUID(artifact.ID.Bytes).String())
		count++

		// Publish event
		if s.bus != nil {
			s.bus.PublishTyped(EventNewArtifact, map[string]any{
				"artifact_id": uuid.UUID(artifact.ID.Bytes).String(),
				"type":        artifactType,
				"file_path":   relPath,
				"title":       title,
			})
		}

		return nil
	})
	if err != nil {
		slog.Error("artifact-scanner: walk error", "error", err)
	}

	if count > 0 {
		slog.Info("artifact-scanner: scan complete", "imported", count)
	}
}

// detectTypeFromExt returns artifact type based on file extension.
func (s *ArtifactScanner) detectTypeFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	ext = strings.TrimPrefix(ext, ".")

	switch ext {
	case "jpg", "jpeg", "png", "gif", "webp", "svg":
		return "image"
	case "mp4", "webm", "mov":
		return "video"
	case "mp3", "wav", "ogg", "flac":
		return "audio"
	case "py", "js", "ts", "go", "rs", "java", "html", "css":
		return "code"
	case "md", "txt", "json", "yaml", "yml", "toml":
		return "text"
	default:
		return "other"
	}
}

// detectMimeFromExtExt returns MIME type based on file extension.
func detectMimeFromExtExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	ext = strings.TrimPrefix(ext, ".")

	mimeMap := map[string]string{
		"jpg":  "image/jpeg",
		"jpeg": "image/jpeg",
		"png":  "image/png",
		"gif":  "image/gif",
		"webp": "image/webp",
		"svg":  "image/svg+xml",
		"mp4":  "video/mp4",
		"webm": "video/webm",
		"mov":  "video/quicktime",
		"mp3":  "audio/mpeg",
		"wav":  "audio/wav",
		"ogg":  "audio/ogg",
		"flac": "audio/flac",
		"py":   "text/x-python",
		"js":   "text/javascript",
		"ts":   "text/typescript",
		"go":   "text/x-go",
		"rs":   "text/x-rust",
		"java": "text/x-java",
		"html": "text/html",
		"css":  "text/css",
		"md":   "text/markdown",
		"txt":  "text/plain",
		"json": "application/json",
		"yaml": "text/yaml",
		"yml":  "text/yaml",
		"toml": "text/toml",
	}

	if m, ok := mimeMap[ext]; ok {
		return m
	}
	return "application/octet-stream"
}
