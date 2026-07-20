package api

import (
	"net/http"

	"go.uber.org/zap"
)

// ragSettingsResponse is the wire shape for GET/PUT /api/settings/rag.
// Keep the JSON keys aligned with what the FE form expects (snake_case).
// Field order mirrors RAGSnapshot so staticcheck's S1016 conversion
// suggestion holds — we round-trip via direct conversion instead of
// re-typing field names.
type ragSettingsResponse struct {
	MaxToolRounds        int  `json:"max_tool_rounds"`
	MaxImagesPerTurn     int  `json:"max_images_per_turn"`
	EnableMultimodal     bool `json:"enable_multimodal"`
	EnableOpenAttachment bool `json:"enable_open_attachment"`
}

type ragSettingsRequest struct {
	MaxToolRounds        int  `json:"max_tool_rounds"`
	MaxImagesPerTurn     int  `json:"max_images_per_turn"`
	EnableMultimodal     bool `json:"enable_multimodal"`
	EnableOpenAttachment bool `json:"enable_open_attachment"`
}

// GetRAGSettings godoc
//
//	@Summary	Get RAG runtime settings
//	@Description	Returns the runtime knobs the RAG orchestrator reads per turn (currently the agentic-tool round cap).
//	@Tags		settings
//	@Produce	json
//	@Success	200	{object}	ragSettingsResponse
//	@Security	BearerAuth
//	@Router		/settings/rag [get]
func (h *handler) GetRAGSettings(w http.ResponseWriter, _ *http.Request) {
	if h.ragMgr == nil {
		writeError(w, http.StatusInternalServerError, "rag manager not configured")
		return
	}
	snap := h.ragMgr.Snapshot()
	writeJSON(w, http.StatusOK, ragSettingsResponse(snap))
}

// UpdateRAGSettings godoc
//
//	@Summary	Update RAG runtime settings
//	@Description	Updates the runtime knobs the RAG orchestrator reads per turn. Hot-reloads on success — no restart required. max_tool_rounds must be between 0 and 5; 0 disables agentic tool calls entirely.
//	@Tags		settings
//	@Accept		json
//	@Produce	json
//	@Param		request	body	ragSettingsRequest	true	"RAG settings"
//	@Success	200	{object}	ragSettingsResponse
//	@Failure	400	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/settings/rag [put]
func (h *handler) UpdateRAGSettings(w http.ResponseWriter, r *http.Request) {
	if h.ragMgr == nil {
		writeError(w, http.StatusInternalServerError, "rag manager not configured")
		return
	}
	var req ragSettingsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	snap := RAGSnapshot(req)
	if err := h.ragMgr.UpdateFromSettings(r.Context(), snap); err != nil {
		h.log.Warn("update rag settings failed", zap.Error(err))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ragSettingsResponse(snap))
}
