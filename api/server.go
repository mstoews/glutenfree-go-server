package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
)

// Server serves the GlutenFree HTTP API.
type Server struct {
	config     util.Config
	store      db.Store
	tokenMaker token.Maker
	router     *gin.Engine
}

// NewServer wires dependencies and routes.
func NewServer(config util.Config, store db.Store) (*Server, error) {
	maker, err := token.NewJWTMaker(config.TokenSymmetricKey)
	if err != nil {
		return nil, fmt.Errorf("cannot create token maker: %w", err)
	}

	server := &Server{
		config:     config,
		store:      store,
		tokenMaker: maker,
	}
	server.setupRouter()
	return server, nil
}

func (server *Server) setupRouter() {
	router := gin.Default()

	router.GET("/healthz", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Public auth routes.
	router.POST("/auth/register", server.registerUser)
	router.POST("/auth/login", server.loginUser)
	router.POST("/auth/refresh", server.renewAccessToken)

	// Authenticated routes.
	authed := router.Group("/").Use(authMiddleware(server.tokenMaker))
	authed.GET("/subscription/status", server.getSubscriptionStatus)

	server.router = router
}

// Start runs the HTTP server on the given address. It blocks.
func (server *Server) Start(address string) error {
	return server.router.Run(address)
}

func errorResponse(err error) gin.H {
	return gin.H{"error": err.Error()}
}
