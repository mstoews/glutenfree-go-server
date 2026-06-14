package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/mstoews/glutenfree-server/applesignin"
	"github.com/mstoews/glutenfree-server/appstore"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
	"github.com/rs/zerolog/log"
)

// Server serves the GlutenFree HTTP API.
type Server struct {
	config     util.Config
	store      db.Repository
	tokenMaker token.Maker
	appstore   *appstore.Verifier    // nil when StoreKit verification is not configured
	apple      *applesignin.Verifier // nil when Sign in with Apple is not configured
	router     *gin.Engine
}

// NewServer wires dependencies and routes.
func NewServer(config util.Config, store db.Repository) (*Server, error) {
	maker, err := token.NewJWTMaker(config.TokenSymmetricKey)
	if err != nil {
		return nil, fmt.Errorf("cannot create token maker: %w", err)
	}

	server := &Server{
		config:     config,
		store:      store,
		tokenMaker: maker,
	}

	// StoreKit verification is optional: without a configured Apple root CA the
	// subscription-verify + webhook routes return 501 rather than fail startup.
	if config.AppleRootCAPath != "" {
		verifier, err := appstore.NewVerifierFromFile(config.AppleRootCAPath, config.AppleBundleID)
		if err != nil {
			return nil, fmt.Errorf("cannot load apple root ca: %w", err)
		}
		server.appstore = verifier
	} else {
		log.Warn().Msg("APPLE_ROOT_CA_PATH not set; /subscription/verify and /webhooks/apple are disabled")
	}

	// Sign in with Apple is optional: the bundle id is the identity token's
	// audience. Without it, /auth/apple returns 501.
	if config.AppleBundleID != "" {
		server.apple = applesignin.NewAppleVerifier(config.AppleBundleID)
	} else {
		log.Warn().Msg("APPLE_BUNDLE_ID not set; /auth/apple is disabled")
	}

	server.setupRouter()
	return server, nil
}

func (server *Server) setupRouter() {
	router := gin.Default()

	router.GET("/healthz", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Public routes.
	router.POST("/auth/register", server.registerUser)
	router.POST("/auth/login", server.loginUser)
	router.POST("/auth/apple", server.appleSignIn) // Sign in with Apple
	router.POST("/auth/refresh", server.renewAccessToken)
	router.GET("/wards", server.listWards)
	router.POST("/webhooks/apple", server.appleWebhook) // App Store Server Notifications

	// Authenticated app-user routes.
	authed := router.Group("/").Use(authMiddleware(server.tokenMaker))
	authed.GET("/me", server.getCurrentUser)
	authed.GET("/subscription/status", server.getSubscriptionStatus)
	authed.POST("/subscription/verify", server.verifySubscription)
	authed.GET("/stores", server.listStores)
	authed.GET("/stores/:id", server.getStore)
	authed.GET("/stores/:id/menu", server.getStoreMenu)

	// Store-admin portal (/admin/*): one store per admin account.
	router.POST("/admin/auth/login", server.adminLogin)
	adminGrp := router.Group("/admin").Use(authMiddleware(server.tokenMaker), requireRole(token.RoleStoreAdmin))
	adminGrp.GET("/store", server.adminGetStore)
	adminGrp.PUT("/store", server.adminUpdateStore)
	adminGrp.POST("/store/submit", server.adminSubmitStore)
	adminGrp.GET("/store/menu", server.adminListMenu)
	adminGrp.POST("/store/menu", server.adminCreateMenu)
	adminGrp.PUT("/store/menu/:id", server.adminUpdateMenu)
	adminGrp.DELETE("/store/menu/:id", server.adminDeleteMenu)

	// Internal ops (/internal/*): review queue + onboarding.
	router.POST("/internal/auth/login", server.internalLogin)
	internalGrp := router.Group("/internal").Use(authMiddleware(server.tokenMaker), requireRole(token.RoleInternal))
	internalGrp.POST("/store-admins", server.provisionStoreAdmin)
	internalGrp.GET("/stores", server.internalListStores)
	internalGrp.POST("/stores/:id/approve", server.approveStore)
	internalGrp.POST("/stores/:id/reject", server.rejectStore)

	server.router = router
}

// Start runs the HTTP server on the given address. It blocks.
func (server *Server) Start(address string) error {
	return server.router.Run(address)
}

func errorResponse(err error) gin.H {
	return gin.H{"error": err.Error()}
}

// currentUser loads the authenticated user named in the token payload.
func (server *Server) currentUser(ctx *gin.Context) (db.User, error) {
	payload := ctx.MustGet(authorizationPayloadKey).(*token.Payload)
	return server.store.GetUserByID(ctx, payload.UserID)
}

// isPaidUser reports whether the user has an active subscription.
func isPaidUser(u db.User) bool {
	return u.SubscriptionStatus == db.SubscriptionStatusActive
}

// respondUserLookupError maps a failed currentUser() lookup to a response.
func respondUserLookupError(ctx *gin.Context, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		ctx.JSON(http.StatusNotFound, errorResponse(errors.New("user not found")))
		return
	}
	ctx.JSON(http.StatusInternalServerError, errorResponse(err))
}
