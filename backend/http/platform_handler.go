package http

import (
	"net/http"
	"strings"

	"github.com/NYTimes/gziphandler"
	"github.com/influxdata/influxdb/v2/http/legacy"
	"github.com/influxdata/influxdb/v2/kit/feature"
	kithttp "github.com/influxdata/influxdb/v2/kit/transport/http"
	"github.com/influxdata/influxdb/v2/noSQL_module"
	"github.com/influxdata/influxdb/v2/static"
)

// PlatformHandler is a collection of all the service handlers.
type PlatformHandler struct {
	AssetHandler  http.Handler
	DocsHandler   http.HandlerFunc
	APIHandler    http.Handler
	LegacyHandler http.Handler
	TempDBHandler http.Handler
}

// NewPlatformHandler returns a platform handler that serves the API and associated assets.
func NewPlatformHandler(b *APIBackend, opts ...APIHandlerOptFn) *PlatformHandler {
	h := NewAuthenticationHandler(b.Logger, b.HTTPErrorHandler)
	h.Handler = feature.NewHandler(b.Logger, b.Flagger, feature.Flags(), NewAPIHandler(b, opts...))
	h.AuthorizationService = b.AuthorizationService
	h.SessionService = b.SessionService
	h.SessionRenewDisabled = b.SessionRenewDisabled
	h.UserService = b.UserService

	h.RegisterNoAuthRoute("GET", "/api/v2")
	h.RegisterNoAuthRoute("POST", "/api/v2/signin")
	h.RegisterNoAuthRoute("POST", "/api/v2/signout")
	h.RegisterNoAuthRoute("POST", "/api/v2/setup")
	h.RegisterNoAuthRoute("GET", "/api/v2/setup")
	h.RegisterNoAuthRoute("GET", "/api/v2/swagger.json")

	assetHandler := static.NewAssetHandler(b.AssetsPath)
	if b.UIDisabled {
		b.Logger.Debug("http server running with UI disabled")
		assetHandler = http.NotFoundHandler()
	}

	wrappedHandler := kithttp.SetCORS(h)
	wrappedHandler = kithttp.SkipOptions(wrappedHandler)

	legacyBackend := newLegacyBackend(b)
	lh := newLegacyHandler(legacyBackend, *legacy.NewHandlerConfig())
	// legacy reponses can optionally be gzip encoded
	gh := gziphandler.GzipHandler(lh)

	// Create TempDBHandler and wrap it with AuthenticationHandler
	tempDBHandler := noSQL_module.NewTempDBHandler(
		b.OrganizationService,
		b.UserService,
		b.AuthorizationService,
		b.PasswordsService,
		b.UserResourceMappingService,
		b.HTTPErrorHandler,
	)

	// Wrap TempDBHandler with authentication
	authHandler := NewAuthenticationHandler(b.Logger, b.HTTPErrorHandler)
	authHandler.Handler = tempDBHandler
	authHandler.AuthorizationService = b.AuthorizationService
	authHandler.SessionService = b.SessionService
	authHandler.SessionRenewDisabled = b.SessionRenewDisabled
	authHandler.UserService = b.UserService

	return &PlatformHandler{
		AssetHandler:  assetHandler,
		DocsHandler:   Redoc("/api/v2/swagger.json"),
		APIHandler:    wrappedHandler,
		LegacyHandler: legacy.NewInflux1xAuthenticationHandler(gh, b.AuthorizerV1, b.HTTPErrorHandler),
		TempDBHandler: authHandler,
	}
}

// ServeHTTP delegates a request to the appropriate subhandler.
func (h *PlatformHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TODO(affo): change this to be mounted prefixes: https://github.com/influxdata/idpe/issues/6689.
	if r.URL.Path == "/write" ||
		r.URL.Path == "/query" ||
		r.URL.Path == "/ping" {
		h.LegacyHandler.ServeHTTP(w, r)
		return
	}

	if r.URL.Path == "/create_temp_db" {
		h.TempDBHandler.ServeHTTP(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/docs") {
		h.DocsHandler.ServeHTTP(w, r)
		return
	}

	// Serve the static UI assets for any basepath that does not start with
	// addressable parts of the platform API.
	if !strings.HasPrefix(r.URL.Path, "/v1") &&
		!strings.HasPrefix(r.URL.Path, "/api/v2") &&
		!strings.HasPrefix(r.URL.Path, "/private/") {
		h.AssetHandler.ServeHTTP(w, r)
		return
	}

	h.APIHandler.ServeHTTP(w, r)
}
