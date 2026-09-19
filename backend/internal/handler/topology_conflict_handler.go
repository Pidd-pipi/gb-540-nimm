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

func (h *CadastralHandler) CreateResolutionBatch(c *gin.Context) {
	var req dto.CreateResolutionBatchRequest
	if !bind(c, h.validate, &req) {
		return
	}
	view, err := h.service.CreateResolutionBatch(req, actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusCreated, view, nil)
}

func (h *CadastralHandler) SubmitResolutionBatch(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	var req dto.SubmitResolutionBatchRequest
	if !bind(c, h.validate, &req) {
		return
	}
	view, err := h.service.SubmitResolutionBatch(id, req, c.GetHeader("Idempotency-Key"), actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, view, nil)
}

func (h *CadastralHandler) GetResolutionBatch(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	view, err := h.service.GetResolutionBatch(id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, view, nil)
}

func (h *CadastralHandler) ListResolutionBatches(c *gin.Context) {
	views, err := h.service.ListResolutionBatches(queryUint(c, "parcel_id"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, views, nil)
}

func (h *CadastralHandler) CancelResolutionBatch(c *gin.Context) {
	id, valid := idParam(c)
	if !valid {
		return
	}
	view, err := h.service.CancelResolutionBatch(id, actor(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, view, nil)
}
