package api

import (
	"encoding/json"
	"net/http"

	"gotth/app/lib"
)

func SharedDataHandler(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var content, drawingID string
	var allowPublicEdits int
	err := lib.DB.QueryRowContext(r.Context(),
		"SELECT id, content, allow_public_edits FROM drawings WHERE share_slug = ?", slug,
	).Scan(&drawingID, &content, &allowPublicEdits)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	scene := sanitizeSceneJSON(content)

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(scene, &parsed); err != nil {
		http.NotFound(w, r)
		return
	}

	// Embed allow_public_edits.
	parsed["allowPublicEdits"] = json.RawMessage(func() string {
		if allowPublicEdits == 1 {
			return "true"
		}
		return "false"
	}())

	// Embed drawingId so the client can fetch files.
	parsed["drawingId"] = json.RawMessage(`"` + drawingID + `"`)

	files := listFiles(drawingID)
	fileJSON, _ := json.Marshal(files)
	parsed["files"] = fileJSON

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parsed)
}
