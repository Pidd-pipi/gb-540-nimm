package handler

import (
	"net/http"

	"cadastral-boundary-topology-resolution/backend/internal/dto"
	"github.com/gin-gonic/gin"
)

func (h *CadastralHandler) ListConflicts(c *gin.Context) {
	items, meta, err := h.service.ListConflicts(dto.ConflictQuery{ProposalID: queryUint(c, "proposal_id"), State: c.Query("state"), Type: c.Query("conflict_type"), Page: queryInt(c, "page", 1), PageSize: queryInt(c, "page_size", 50)})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, items, meta)
}

func (h *CadastralHandler) GetConflict(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	item, err := h.service.GetConflict(id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, item, nil)
}

func (h *CadastralHandler) DetectConflicts(c *gin.Context) {
	var req dto.DetectConflictRequest
	if !bind(c, h.validate, &req) {
		return
	}
	items, err := h.service.DetectConflicts(req, c.GetHeader("Idempotency-Key"), actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, items, nil)
}

func (h *CadastralHandler) TransitionConflict(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var req dto.ConflictTransitionRequest
	if !bind(c, h.validate, &req) {
		return
	}
	item, err := h.service.TransitionConflict(id, req, actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, item, nil)
}

func (h *CadastralHandler) ApplyConflictSuggestion(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var req dto.ApplySuggestionRequest
	if !bind(c, h.validate, &req) {
		return
	}
	item, err := h.service.ApplyConflictSuggestion(id, req, actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusCreated, item, nil)
}

func (h *CadastralHandler) PreviewConflictBatch(c *gin.Context) {
	var req dto.ConflictBatchPreviewRequest
	if !bind(c, h.validate, &req) {
		return
	}
	view, err := h.service.PreviewConflictBatch(req, c.GetHeader("Idempotency-Key"), actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusCreated, view, nil)
}

func (h *CadastralHandler) SubmitConflictBatch(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	view, err := h.service.SubmitConflictBatch(id, actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, view, nil)
}

func (h *CadastralHandler) GetConflictBatch(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	view, err := h.service.GetConflictBatch(id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, view, nil)
}

func (h *CadastralHandler) ListConflictBatches(c *gin.Context) {
	items, meta, err := h.service.ListConflictBatches(dto.ConflictBatchQuery{ParcelID: queryUint(c, "parcel_id"), State: c.Query("state"), Page: queryInt(c, "page", 1), PageSize: queryInt(c, "page_size", 20)})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, items, meta)
}
