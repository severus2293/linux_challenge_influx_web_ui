package noSQL_module

import (
	"net/http"

	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/kit/platform/errors"
	"github.com/influxdata/influxdb/v2/kit/tracing"
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
	if r.Method != "GET" {
		h.errorHandler.HandleHTTPError(r.Context(), &errors.Error{
			Msg:  "method not allowed",
			Code: errors.EInvalid,
		}, w)
		return
	}

	ctx := r.Context()
	span, ctx := tracing.StartSpanFromContext(ctx)
	defer span.Finish()

	result, err := h.tempDBService.CreateTempDB(ctx)
	if err != nil {
		h.errorHandler.HandleHTTPError(ctx, err, w)
		return
	}

	if err := encodeResponse(ctx, w, http.StatusOK, result); err != nil {
		h.errorHandler.HandleHTTPError(ctx, err, w)
	}
}
