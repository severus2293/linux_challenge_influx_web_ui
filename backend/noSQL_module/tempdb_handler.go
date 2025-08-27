package noSQL_module

import (
	"encoding/json"
	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/kit/platform/errors"
	"net/http"
	"strconv"
)

type TempDBHandler struct {
	tempDBService *TempDBService
	errorHandler  errors.HTTPErrorHandler
}

func NewTempDBHandler(orgService influxdb.OrganizationService, userService influxdb.UserService, authService influxdb.AuthorizationService, passwordsService influxdb.PasswordsService, userResourceMappingService influxdb.UserResourceMappingService, errorHandler errors.HTTPErrorHandler) *TempDBHandler {
	return &TempDBHandler{
		tempDBService: &TempDBService{
			OrgService:                 orgService,
			UserService:                userService,
			AuthService:                authService,
			PasswordsService:           passwordsService,
			UserResourceMappingService: userResourceMappingService,
		},
		errorHandler: errorHandler,
	}
}

func (h *TempDBHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/create_temp_db":
		h.handleCreate(w, r)
	case r.Method == "POST" && r.URL.Path == "/delete_temp_db":
		h.handleDelete(w, r)
	default:
		h.errorHandler.HandleHTTPError(r.Context(), &errors.Error{
			Msg:  "not found",
			Code: errors.ENotFound,
		}, w)
	}
}

func (h *TempDBHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// читаем ttl из query
	ttlStr := r.URL.Query().Get("ttl")
	ttl := 10
	if ttlStr != "" {
		if v, err := strconv.Atoi(ttlStr); err == nil && v > 0 {
			ttl = v
		}
	}

	result, err := h.tempDBService.CreateTempDB(ctx, ttl)
	if err != nil {
		h.errorHandler.HandleHTTPError(ctx, err, w)
		return
	}
	encodeResponse(ctx, w, http.StatusOK, result)
}

func (h *TempDBHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		OrgName string `json:"org_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OrgName == "" {
		h.errorHandler.HandleHTTPError(ctx, &errors.Error{
			Msg:  "invalid request body",
			Code: errors.EInvalid,
		}, w)
		return
	}

	if err := h.tempDBService.DeleteTempDB(ctx, body.OrgName); err != nil {
		h.errorHandler.HandleHTTPError(ctx, err, w)
		return
	}

	encodeResponse(ctx, w, http.StatusOK, map[string]string{
		"message": "organization deleted",
	})
}
