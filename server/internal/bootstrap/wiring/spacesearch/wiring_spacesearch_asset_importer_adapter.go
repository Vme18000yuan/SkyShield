package spacesearchwiring

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	assetapp "github.com/yyhuni/lunafox/server/internal/modules/asset/application"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	catalogapp "github.com/yyhuni/lunafox/server/internal/modules/catalog/application"
	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
)

type spacesearchAssetImporterAdapter struct {
	targets   *catalogapp.TargetFacade
	lookup    catalogapp.TargetBatchCommandStore
	hostPorts *assetapp.HostPortFacade
	registry  *registryapp.RegistrationFacade
}

func (a *spacesearchAssetImporterAdapter) Import(ctx context.Context, command spaceapp.ImportCommand) (spaceapp.ImportRowResult, error) {
	r := command.Record
	result := spaceapp.ImportRowResult{RowID: command.Selection.RowID, AssetIDs: []string{}, Warnings: []string{}}
	// Stored search snapshots can predate the root-domain grouping rule.
	targetName := spaceapp.AutomaticTarget(r.Hostname)
	canonicalOnly := false
	if ip, err := netip.ParseAddr(targetName); err == nil && ip.Is6() {
		canonicalOnly = true
	}
	if command.Selection.TargetID > 0 {
		target, err := a.targets.GetByIDContext(ctx, command.Selection.TargetID)
		if err != nil {
			return result, spaceapp.ErrDestination
		}
		targetName = target.Name
		canonicalOnly = false
	}
	if !canonicalOnly {
		org := command.Selection.OrganizationID
		created, err := a.targets.BatchCreateTargetsContext(ctx, []string{targetName}, &org)
		if err != nil || created == nil || created.NoValidTargets || created.FailedCount > 0 {
			return result, spaceapp.ErrDestination
		}
		targets, err := a.lookup.FindByNamesContext(ctx, []string{targetName})
		if err != nil {
			return result, err
		}
		if len(targets) != 1 {
			return result, spaceapp.ErrDestination
		}
		result.TargetID = targets[0].ID
	}
	ip, err := netip.ParseAddr(r.IP)
	if err != nil {
		return result, spaceapp.ErrInvalidInput
	}
	if ip.Is4() && result.TargetID > 0 {
		if _, err = a.hostPorts.BatchUpsertContext(ctx, result.TargetID, []assetapp.HostPortItem{{Host: r.Hostname, IP: r.IP, Port: r.Port}}); err != nil {
			return result, err
		}
	} else if ip.Is6() {
		result.Warnings = append(result.Warnings, "IPv6 已登记到统一资产库；原 IP/端口列表暂不支持 IPv6。")
	}
	registered, err := a.registry.Register(ctx, registryapp.BatchRegisterInput{UserID: command.Selection.UserID, Origin: "import", RequestID: spaceapp.ImportRequestID(command.Selection), Assets: spaceapp.RecordAssets(command, result.TargetID)})
	if err != nil {
		return result, err
	}
	if len(registered.Errors) > 0 {
		messages := make([]string, 0, len(registered.Errors))
		for _, item := range registered.Errors {
			messages = append(messages, item.Error)
		}
		return result, &spaceapp.ImportError{Message: strings.Join(messages, "；")}
	}
	seen := map[string]bool{}
	for _, item := range registered.Results {
		if !seen[item.ID] {
			result.AssetIDs = append(result.AssetIDs, item.ID)
			seen[item.ID] = true
		}
	}
	if len(result.AssetIDs) == 0 {
		return result, errors.New("FOFA result produced no assets")
	}
	return result, nil
}
