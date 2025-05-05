package noSQL_module

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/influxdata/influxdb/v2/kit/platform/errors"
)

// encodeResponse encodes resp as JSON to w with the given status.
func encodeResponse(ctx context.Context, w http.ResponseWriter, status int, resp interface{}) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	err := enc.Encode(resp)
	if err != nil {
		return &errors.Error{
			Msg:  "unable to encode response",
			Err:  err,
			Code: errors.EInternal,
		}
	}
	return nil
}
