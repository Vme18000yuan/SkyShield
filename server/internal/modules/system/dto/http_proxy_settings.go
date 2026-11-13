package dto

import (
	"time"

	systemapp "github.com/yyhuni/lunafox/server/internal/modules/system/application"
)

type HTTPProxySettingsResponse struct {
	Name         string     `json:"name"`
	Enabled      bool       `json:"enabled"`
	Configured   bool       `json:"configured"`
	ProxyAddress string     `json:"proxyAddress"`
	UpdatedAt    *time.Time `json:"updatedAt"`
}

func HTTPProxySettingsOutput(view systemapp.HTTPProxySettingsView) HTTPProxySettingsResponse {
	return HTTPProxySettingsResponse{
		Name:         "admin/system/httpProxy/current",
		Enabled:      view.Enabled,
		Configured:   view.Configured,
		ProxyAddress: view.ProxyAddress,
		UpdatedAt:    view.UpdatedAt,
	}
}
