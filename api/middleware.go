package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mstoews/glutenfree-server/token"
)

const (
	authorizationHeaderKey  = "authorization"
	authorizationTypeBearer = "bearer"
	authorizationPayloadKey = "authorization_payload"
)

// authMiddleware validates the Bearer access token and stores the payload in
// the gin context under authorizationPayloadKey for downstream handlers.
func authMiddleware(tokenMaker token.Maker) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		authHeader := ctx.GetHeader(authorizationHeaderKey)
		if len(authHeader) == 0 {
			err := errors.New("authorization header is not provided")
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse(err))
			return
		}

		fields := strings.Fields(authHeader)
		if len(fields) < 2 {
			err := errors.New("invalid authorization header format")
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse(err))
			return
		}

		if strings.ToLower(fields[0]) != authorizationTypeBearer {
			err := fmt.Errorf("unsupported authorization type %q", fields[0])
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse(err))
			return
		}

		payload, err := tokenMaker.VerifyToken(fields[1])
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse(err))
			return
		}

		ctx.Set(authorizationPayloadKey, payload)
		ctx.Next()
	}
}

// corsMiddleware applies CORS for the browser-based admin/web portals. Allowed
// origins come from config.AllowedOrigins; a single "*" allows any origin.
// Auth is via Bearer token (not cookies), so wildcard origins are safe here.
func corsMiddleware(allowedOrigins []string) gin.HandlerFunc {
	allowAny := false
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAny = true
		}
		allowed[o] = true
	}

	return func(ctx *gin.Context) {
		origin := ctx.GetHeader("Origin")
		if origin != "" && (allowAny || allowed[origin]) {
			if allowAny {
				ctx.Header("Access-Control-Allow-Origin", "*")
			} else {
				ctx.Header("Access-Control-Allow-Origin", origin)
				ctx.Header("Vary", "Origin")
			}
			ctx.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			ctx.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			ctx.Header("Access-Control-Max-Age", "3600")
		}

		// Preflight: answer and stop before routing (the route may only define
		// GET/POST/etc., not OPTIONS).
		if ctx.Request.Method == http.MethodOptions {
			ctx.AbortWithStatus(http.StatusNoContent)
			return
		}
		ctx.Next()
	}
}

// requireRole aborts with 403 unless the authenticated token carries the given
// role. Must run after authMiddleware.
func requireRole(role string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		payload, ok := ctx.MustGet(authorizationPayloadKey).(*token.Payload)
		if !ok || payload.Role != role {
			ctx.AbortWithStatusJSON(http.StatusForbidden, errorResponse(errors.New("forbidden: insufficient role")))
			return
		}
		ctx.Next()
	}
}
