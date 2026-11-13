package assetregistrywiring

import (
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/gorm"
)

func NewAssetRegistryStoreAdapter(db *gorm.DB) assetapp.Store {
	return assetrepo.NewAssetRegistryStore(db)
}

func NewAssetRegistryRootDomainStoreAdapter(db *gorm.DB) assetapp.RootDomainStore {
	return assetrepo.NewAssetRegistryStore(db)
}

func NewAssetRegistryApplicationProjectionStoreAdapter(db *gorm.DB) assetapp.ApplicationProjectionStore {
	return assetrepo.NewAssetRegistryStore(db)
}

func NewAssetRegistryRegistrationStoreAdapter(db *gorm.DB, creators assetapp.LegacyAssetCreators) assetapp.RegistrationStore {
	return assetrepo.NewAssetRegistrationStore(db, creators)
}
