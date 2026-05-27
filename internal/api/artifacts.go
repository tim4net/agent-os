package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ArtifactAPI holds dependencies for artifact CRUD handlers.
type ArtifactAPI struct {
	queries       *db.Queries
	artifactsPath string
}

// NewArtifactAPI creates a new ArtifactAPI.
func NewArtifactAPI(queries *db.Queries, artifactsPath string) *ArtifactAPI {
	return &ArtifactAPI{
		queries:       queries,
		artifactsPath: artifactsPath,
	}
}

// ListArtifacts handles GET /api/artifacts?type=image&agent_id=uuid&limit=20&offset=0
func (aa *ArtifactAPI) ListArtifacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	artifactType := r.URL.Query().Get("type")
	if artifactType == "" {
		artifactType = "" // will be treated as NULL in query
	}

	var agentID pgtype.UUID
	if aidStr := r.URL.Query().Get("agent_id"); aidStr != "" {
		if err := agentID.Scan(aidStr); err != nil {
			http.Error(w, "invalid agent_id parameter", http.StatusBadRequest)
			return
		}
	}

	limit := int32(20)
	if l := r.URL.Query().Get("limit"); l != "" {
		var n int
		if _, err := fmt.Sscanf(l, "%d", &n); err == nil && n > 0 {
			limit = int32(n)
		}
	}

	offset := int32(0)
	if o := r.URL.Query().Get("offset"); o != "" {
		var n int
		if _, err := fmt.Sscanf(o, "%d", &n); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	artifacts, err := aa.queries.ListArtifacts(ctx, db.ListArtifactsParams{
		Column1: artifactType,
		Column2: agentID,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		http.Error(w, "failed to list artifacts", http.StatusInternalServerError)
		return
	}

	if artifacts == nil {
		artifacts = []db.Artifact{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(artifacts)
}

// GetArtifact handles GET /api/artifacts/:id
func (aa *ArtifactAPI) GetArtifact(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid artifact ID", http.StatusBadRequest)
		return
	}

	artifact, err := aa.queries.GetArtifact(r.Context(), id)
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(artifact)
}

// GetArtifactFile handles GET /api/artifacts/:id/file — serves the actual file from disk.
func (aa *ArtifactAPI) GetArtifactFile(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid artifact ID", http.StatusBadRequest)
		return
	}

	artifact, err := aa.queries.GetArtifact(r.Context(), id)
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	if !artifact.FilePath.Valid || artifact.FilePath.String == "" {
		http.Error(w, "artifact has no file", http.StatusNotFound)
		return
	}

	filePath := artifact.FilePath.String

	// Security: ensure the path is within artifactsPath
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		http.Error(w, "invalid file path", http.StatusInternalServerError)
		return
	}
	absBase, _ := filepath.Abs(aa.artifactsPath)
	if !strings.HasPrefix(absPath, absBase) {
		http.Error(w, "file access denied", http.StatusForbidden)
		return
	}

	f, err := os.Open(absPath)
	if err != nil {
		http.Error(w, "file not found on disk", http.StatusNotFound)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "file stat error", http.StatusInternalServerError)
		return
	}

	// Set Content-Type from mime_type stored in DB
	if artifact.MimeType.Valid && artifact.MimeType.String != "" {
		w.Header().Set("Content-Type", artifact.MimeType.String)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))

	// Set Content-Disposition for downloads
	filename := filepath.Base(absPath)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	http.ServeContent(w, r, filename, stat.ModTime(), f)
}

// UploadArtifact handles POST /api/artifacts — multipart form upload.
func (aa *ArtifactAPI) UploadArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse multipart form (max 100MB)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "failed to parse multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file in form", http.StatusBadRequest)
		return
	}
	defer file.Close()

	artifactType := r.FormValue("type")
	if artifactType == "" {
		artifactType = detectTypeFromExt(header.Filename)
	}

	title := r.FormValue("title")
	description := r.FormValue("description")
	agentIDStr := r.FormValue("agent_id")

	var agentID pgtype.UUID
	if agentIDStr != "" {
		if err := agentID.Scan(agentIDStr); err != nil {
			http.Error(w, "invalid agent_id", http.StatusBadRequest)
			return
		}
	}

	// Generate unique filename
	ext := filepath.Ext(header.Filename)
	fileUUID := uuid.New().String()
	relativePath := filepath.Join(artifactType, fileUUID+ext)
	fullPath := filepath.Join(aa.artifactsPath, relativePath)

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, "failed to create directory", http.StatusInternalServerError)
		return
	}

	// Save file to disk
	dst, err := os.Create(fullPath)
	if err != nil {
		http.Error(w, "failed to save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	// Detect mime type
	mimeType := detectMimeFromExt(ext)

	// Create DB record
	artifact, err := aa.queries.CreateArtifact(ctx, db.CreateArtifactParams{
		AgentID:     agentID,
		Type:        artifactType,
		Title:       pgtype.Text{String: title, Valid: title != ""},
		Description: pgtype.Text{String: description, Valid: description != ""},
		FilePath:    pgtype.Text{String: relativePath, Valid: true},
		MimeType:    pgtype.Text{String: mimeType, Valid: mimeType != ""},
		Metadata:    []byte("{}"),
	})
	if err != nil {
		// Clean up file on DB error
		os.Remove(fullPath)
		http.Error(w, "failed to create artifact record: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(artifact)
}

// DeleteArtifact handles DELETE /api/artifacts/:id
func (aa *ArtifactAPI) DeleteArtifact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid artifact ID", http.StatusBadRequest)
		return
	}

	// Get the artifact first so we can delete the file
	artifact, err := aa.queries.GetArtifact(ctx, id)
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	// Delete DB record
	if err := aa.queries.DeleteArtifact(ctx, id); err != nil {
		http.Error(w, "failed to delete artifact", http.StatusInternalServerError)
		return
	}

	// Delete file from disk
	if artifact.FilePath.Valid && artifact.FilePath.String != "" {
		fullPath := filepath.Join(aa.artifactsPath, artifact.FilePath.String)
		os.Remove(fullPath) // best effort
	}

	w.WriteHeader(http.StatusNoContent)
}

// ArtifactRoutes returns a Chi router with artifact routes.
func (aa *ArtifactAPI) ArtifactRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", aa.ListArtifacts)
	r.Post("/", aa.UploadArtifact)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", aa.GetArtifact)
		r.Get("/file", aa.GetArtifactFile)
		r.Delete("/", aa.DeleteArtifact)
	})

	return r
}

// ExportArtifact handles POST /api/artifacts/:id/export — exports artifact as a markdown note to the Obsidian vault.
func (a *API) ExportArtifact(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")

	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid artifact ID", http.StatusBadRequest)
		return
	}

	artifact, err := a.artifacts.queries.GetArtifact(r.Context(), id)
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}

	// Derive filename from file_path
	filename := "unknown"
	var fileSize int64
	var absFilePath string
	if artifact.FilePath.Valid && artifact.FilePath.String != "" {
		filename = filepath.Base(artifact.FilePath.String)
		absFilePath = filepath.Join(a.artifacts.artifactsPath, artifact.FilePath.String)
		if stat, err := os.Stat(absFilePath); err == nil {
			fileSize = stat.Size()
		}
	}

	// Build title
	title := "Artifact Export"
	if artifact.Title.Valid && artifact.Title.String != "" {
		title = artifact.Title.String
	} else {
		title = filename
	}

	// Read file content for code/text artifacts
	var fileContent string
	if (artifact.Type == "code" || artifact.Type == "text") && absFilePath != "" {
		absPath, err := filepath.Abs(absFilePath)
		if err == nil {
			absBase, _ := filepath.Abs(a.artifacts.artifactsPath)
			if strings.HasPrefix(absPath, absBase) {
				data, err := os.ReadFile(absPath)
				if err == nil {
					fileContent = string(data)
				}
			}
		}
	}

	// Build markdown note
	var body strings.Builder

	// YAML frontmatter
	artifactType := artifact.Type
	created := ""
	if artifact.CreatedAt.Valid {
		created = artifact.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	mimeType := ""
	if artifact.MimeType.Valid {
		mimeType = artifact.MimeType.String
	}
	body.WriteString(fmt.Sprintf("---\nartifact_id: %s\ntype: %s\nmime_type: %q\nfilename: %q\nsize: %d\ncreated: %s\nsource: agent-os\n---\n\n",
		id.String(), artifactType, mimeType, filename, fileSize, created))

	body.WriteString(fmt.Sprintf("# %s\n\n", title))

	// Metadata section
	body.WriteString("**Type:** " + artifactType + "  \n")
	if mimeType != "" {
		body.WriteString(fmt.Sprintf("**MIME:** %s  \n", mimeType))
	}
	if fileSize > 0 {
		body.WriteString(fmt.Sprintf("**Size:** %s  \n", formatFileSize(fileSize)))
	}
	body.WriteString(fmt.Sprintf("**Created:** %s  \n", created))
	body.WriteString("\n> Exported from Agent OS Workspace.\n\n")

	// Content
	if fileContent != "" {
		// Detect language for code block
		lang := ""
		ext := strings.ToLower(filepath.Ext(filename))
		ext = strings.TrimPrefix(ext, ".")
		langMap := map[string]string{
			"py": "python", "js": "javascript", "ts": "typescript",
			"go": "go", "rs": "rust", "java": "java",
			"html": "html", "css": "css", "json": "json",
			"yaml": "yaml", "yml": "yaml", "toml": "toml",
			"md": "markdown", "txt": "",
		}
		if l, ok := langMap[ext]; ok {
			lang = l
		} else if artifactType == "code" {
			lang = ext
		}

		if artifactType == "code" {
			body.WriteString(fmt.Sprintf("```%s\n%s\n```\n", lang, fileContent))
		} else {
			body.WriteString(fileContent + "\n")
		}
	} else if artifactType == "image" {
		body.WriteString("*Image file. View in Agent OS Studio or Workspace.*\n")
	} else if artifactType == "video" {
		body.WriteString("*Video file. View in Agent OS Workspace.*\n")
	} else if artifactType == "audio" {
		body.WriteString("*Audio file. View in Agent OS Workspace.*\n")
	}

	content := body.String()

	// Write to vault
	vaultDir := filepath.Join(a.obsidianPath, "projects", "agent-os", "artifacts")
	if err := os.MkdirAll(vaultDir, 0755); err != nil {
		http.Error(w, "failed to create vault directory", http.StatusInternalServerError)
		return
	}

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	slug = regexp.MustCompile(`-+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	date := ""
	if artifact.CreatedAt.Valid {
		date = artifact.CreatedAt.Time.Format("2006-01-02")
	} else {
		date = "unknown-date"
	}
	exportFilename := fmt.Sprintf("%s-%s.md", date, slug)
	fullPath := filepath.Join(vaultDir, exportFilename)

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		http.Error(w, "failed to write export file", http.StatusInternalServerError)
		return
	}

	relPath := filepath.Join("projects", "agent-os", "artifacts", exportFilename)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "exported",
		"path":     relPath,
		"filename": exportFilename,
	})
}

// formatFileSize returns a human-readable file size string.
func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

// detectTypeFromExt returns artifact type based on file extension.
func detectTypeFromExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
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

// detectMimeFromExt returns MIME type based on file extension.
func detectMimeFromExt(ext string) string {
	ext = strings.ToLower(ext)
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
