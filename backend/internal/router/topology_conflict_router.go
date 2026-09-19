package router

import (
	"cadastral-boundary-topology-resolution/backend/internal/constants"
	appmw "cadastral-boundary-topology-resolution/backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

func registerTopologyConflictRoutes(api *gin.RouterGroup, deps Dependencies) {
	h := deps.CadastralHandler
	conflicts := api.Group("/conflicts")
	conflicts.GET("", h.ListConflicts)
	conflicts.GET("/:id", h.GetConflict)
	conflicts.POST("/detect", appmw.RateLimitMiddleware(deps.AnalyzeLimiter, "conflict_detection"), appmw.RBACMiddleware(constants.RoleGISAnalyst, constants.RoleAdmin), h.DetectConflicts)
	conflicts.POST("/:id/transition", appmw.RBACMiddleware(constants.RoleReviewer, constants.RoleAdmin), h.TransitionConflict)
	conflicts.POST("/:id/apply-suggestion", appmw.RBACMiddleware(constants.RoleReviewer, constants.RoleAdmin), h.ApplyConflictSuggestion)

	batches := api.Group("/resolution-batches")
	batches.GET("", appmw.RBACMiddleware(constants.RoleReviewer, constants.RoleAdmin), h.ListResolutionBatches)
	batches.POST("", appmw.RBACMiddleware(constants.RoleReviewer, constants.RoleAdmin), h.CreateResolutionBatch)
	batches.GET("/:id", appmw.RBACMiddleware(constants.RoleReviewer, constants.RoleAdmin), h.GetResolutionBatch)
	batches.POST("/:id/submit", appmw.RBACMiddleware(constants.RoleReviewer, constants.RoleAdmin), h.SubmitResolutionBatch)
	batches.POST("/:id/cancel", appmw.RBACMiddleware(constants.RoleReviewer, constants.RoleAdmin), h.CancelResolutionBatch)
}
