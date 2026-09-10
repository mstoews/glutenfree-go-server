package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/mstoews/glutenfree-server/applesignin"
	"github.com/mstoews/glutenfree-server/appstore"
	"github.com/mstoews/glutenfree-server/blobstore"
	db "github.com/mstoews/glutenfree-server/db/sqlc"
	"github.com/mstoews/glutenfree-server/discovery"
	"github.com/mstoews/glutenfree-server/geocode"
	"github.com/mstoews/glutenfree-server/mailer"
	"github.com/mstoews/glutenfree-server/token"
	"github.com/mstoews/glutenfree-server/util"
	"github.com/rs/zerolog/log"
)

// Server serves the GlutenFree HTTP API.
type Server struct {
	discovery  discovery.Provider
	config     util.Config
	store      db.Repository
	tokenMaker token.Maker
	appstore   *appstore.Verifier    // nil when StoreKit verification is not configured
	apple      *applesignin.Verifier // nil when Sign in with Apple is not configured
	geocoder   geocode.Geocoder
	uploader   blobstore.Uploader // nil when IMAGE_BUCKET is unset
	mailer     mailer.Sender      // noop sender when RESEND_API_KEY is unset
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
		discovery:  discovery.New(config.BraveSearchAPIKey),
		store:      store,
		tokenMaker: maker,
		// GSI needs no API key or billing; swap for a paid provider by
		// assigning a different geocode.Geocoder here.
		geocoder: geocode.NewGSI(),
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

	// Image uploads are optional: without a bucket, /internal/uploads/image
	// returns 501 rather than failing startup.
	if config.ImageBucket != "" {
		uploader, err := blobstore.NewGCS(context.Background(), config.ImageBucket)
		if err != nil {
			return nil, fmt.Errorf("cannot create image uploader: %w", err)
		}
		server.uploader = uploader
	} else {
		log.Warn().Msg("IMAGE_BUCKET not set; /internal/uploads/image is disabled")
	}

	// Transactional mail is optional: without an API key the operator
	// forgot-password route returns 501 and reset links are logged instead, so
	// the admin-to-admin reset path still works.
	if config.ResendAPIKey != "" && config.MailFrom != "" {
		server.mailer = mailer.NewResend(config.ResendAPIKey, config.MailFrom, config.MailFromName)
	} else {
		server.mailer = mailer.NewNoop()
		log.Warn().Msg("RESEND_API_KEY/MAIL_FROM not set; /internal/auth/forgot-password is disabled")
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

	// CORS for the browser admin portal (gurufuri-admin) and web clients.
	router.Use(corsMiddleware(server.config.AllowedOrigins))

	// /health, not /healthz: Google's Cloud Run edge reserves /healthz and
	// returns its own 404 without forwarding to the container.
	router.GET("/health", func(ctx *gin.Context) {
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
	router.POST("/internal/auth/refresh", server.renewInternalAccessToken)
	router.POST("/internal/auth/logout", server.internalLogout)
	// Password reset by emailed link: public by necessity — the operator
	// requesting it cannot sign in.
	router.POST("/internal/auth/forgot-password", server.forgotInternalPassword)
	router.POST("/internal/auth/reset-password", server.resetInternalPassword)
	internalGrp := router.Group("/internal").Use(authMiddleware(server.tokenMaker), requireRole(token.RoleInternal))
	internalGrp.POST("/auth/change-password", server.changeInternalPassword)
	internalGrp.GET("/admins", server.listInternalAdmins)
	internalGrp.POST("/admins", server.createInternalAdmin)
	internalGrp.PUT("/admins/:id/password", server.setInternalAdminPassword)
	internalGrp.POST("/store-admins", server.provisionStoreAdmin)
	internalGrp.GET("/stores", server.internalListStores)
	internalGrp.POST("/stores", server.internalCreateStore)
	internalGrp.POST("/stores/import", server.internalImportStores)
	internalGrp.POST("/discovery", server.internalDiscover)
	internalGrp.POST("/stores/geocode", server.internalGeocodeStores)
	internalGrp.POST("/geocode", server.internalGeocodeAddress)
	internalGrp.POST("/uploads/image", server.internalUploadImage)
	internalGrp.GET("/stores/:id", server.internalGetStore)
	internalGrp.PUT("/stores/:id", server.internalUpdateStore)
	internalGrp.DELETE("/stores/:id", server.internalDeleteStore)
	internalGrp.GET("/stores/:id/menu", server.internalListMenu)
	internalGrp.POST("/stores/:id/menu", server.internalCreateMenu)
	internalGrp.PUT("/stores/:id/menu/:item_id", server.internalUpdateMenu)
	internalGrp.DELETE("/stores/:id/menu/:item_id", server.internalDeleteMenu)
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
