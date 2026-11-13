package spacesearchwiring

import (
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/asset/application"
	registryapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	catalogapp "github.com/yyhuni/lunafox/server/internal/modules/catalog/application"
	spaceapp "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/application"
	spacerepo "github.com/yyhuni/lunafox/server/internal/modules/spacesearch/repository"
	"gorm.io/gorm"
)

func NewSpacesearchFofaStoreAdapter(db *gorm.DB) spaceapp.Store { return spacerepo.NewFofaStore(db) }
func NewSpacesearchAssetImporterAdapter(targets *catalogapp.TargetFacade, lookup catalogapp.TargetBatchCommandStore, hostPorts *assetapp.HostPortFacade, registry *registryapp.RegistrationFacade) spaceapp.AssetImporter {
	return &spacesearchAssetImporterAdapter{targets: targets, lookup: lookup, hostPorts: hostPorts, registry: registry}
}
