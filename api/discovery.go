package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/discovery"
)

type discoveryRequest struct {
	WardID int32    `json:"ward_id" binding:"required,min=1"`
	Query  string   `json:"query" binding:"max=150"`
	URLs   []string `json:"urls" binding:"max=5,dive,max=2048"`
	Limit  int      `json:"limit" binding:"min=0,max=5"`
}
type discoveryOutcome struct {
	URL      string   `json:"url"`
	Name     string   `json:"name,omitempty"`
	Status   string   `json:"status"`
	StoreID  string   `json:"store_id,omitempty"`
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Server) internalDiscover(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 16<<10)
	var req discoveryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(400, gin.H{"error": "Choose a ward, up to five URLs, and a limit between 1 and 5"})
		return
	}
	if req.Limit == 0 {
		req.Limit = 5
	}
	for _, raw := range req.URLs {
		if _, err := discovery.URL(raw); err != nil {
			ctx.JSON(400, gin.H{"error": err.Error()})
			return
		}
	}
	work, cancel := context.WithTimeout(ctx.Request.Context(), 90*time.Second)
	defer cancel()
	wards, err := s.store.ListWards(work)
	if err != nil {
		ctx.JSON(500, gin.H{"error": "Could not load wards"})
		return
	}
	var ward db.Ward
	for _, w := range wards {
		if w.ID == req.WardID {
			ward = w
			break
		}
	}
	if ward.ID == 0 {
		ctx.JSON(400, gin.H{"error": "Unknown ward"})
		return
	}
	urls := req.URLs
	if len(urls) == 0 {
		if s.config.BraveSearchAPIKey == "" {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "Automatic search requires BRAVE_SEARCH_API_KEY on the Go server. You can paste restaurant URLs instead."})
			return
		}
		urls, err = s.discovery.Search(work, fmt.Sprintf("%s %s Tokyo %s gluten free グルテンフリー restaurant", ward.NameEn, ward.NameJa, strings.TrimSpace(req.Query)), req.Limit)
		if err != nil {
			ctx.JSON(502, gin.H{"error": err.Error()})
			return
		}
	}
	outcomes := []discoveryOutcome{}
	seen := map[string]bool{}
	for _, raw := range urls {
		if len(outcomes) >= req.Limit {
			break
		}
		u, err := discovery.URL(raw)
		if err != nil {
			outcomes = append(outcomes, discoveryOutcome{URL: raw, Status: "failed", Error: err.Error()})
			continue
		}
		if seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		out := discoveryOutcome{URL: u.String(), Status: "failed"}
		c, err := s.discovery.Capture(work, u.String())
		if err != nil {
			out.Error = err.Error()
			outcomes = append(outcomes, out)
			continue
		}
		out.Name = c.Name
		out.Warnings = c.Warnings
		// Ward comes from captured address, not merely the search query.
		address := strings.ToLower(c.Address)
		if !strings.Contains(address, strings.ToLower(ward.NameJa)) && !regexp.MustCompile(`(?i)(^|[^a-z])`+regexp.QuoteMeta(ward.NameEn)+`([^a-z]|$)`).MatchString(address) {
			out.Error = "Captured address does not match the selected ward; review manually"
			outcomes = append(outcomes, out)
			continue
		}
		provenance, _ := json.MarshalIndent(c, "", "  ")
		saved, created, err := s.store.CreateDiscoveryDraft(work, db.CreateStoreFullParams{
			WardID: ward.ID, Name: c.Name, Address: c.Address, Phone: c.Phone, SourceUrl: c.SourceURL, Cuisine: c.Cuisine,
			OpeningHours: []byte("[]"), Status: db.StoreStatusDraft, GfStatus: db.GfStatusOnRequest, PriceLevel: 2,
			Notes: "Automatic discovery captured " + time.Now().UTC().Format(time.RFC3339) + "\nReview source claims, image rights, location and menus before approval.\n" + string(provenance),
		})
		if err != nil {
			out.Error = "Could not save draft; retry is safe"
		} else {
			out.StoreID = saved.ID.String()
			out.Status = "duplicate"
			if created {
				out.Status = "created"
			}
		}
		outcomes = append(outcomes, out)
	}
	ctx.JSON(200, gin.H{"results": outcomes})
}
