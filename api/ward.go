package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type wardResponse struct {
	ID     int32  `json:"id"`
	NameJa string `json:"name_ja"`
	NameEn string `json:"name_en"`
}

// listWards returns all wards. Public (no auth): the app ships a bundled
// wards.json and treats this endpoint as the source of truth / fallback.
func (server *Server) listWards(ctx *gin.Context) {
	wards, err := server.store.ListWards(ctx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, errorResponse(err))
		return
	}

	resp := make([]wardResponse, len(wards))
	for i, w := range wards {
		resp[i] = wardResponse{ID: w.ID, NameJa: w.NameJa, NameEn: w.NameEn}
	}
	ctx.JSON(http.StatusOK, resp)
}
