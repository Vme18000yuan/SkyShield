package assetregistrywiring

import (
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
)

var _ assetapp.Store = (*assetrepo.AssetRegistryStore)(nil)
var _ assetapp.RegistrationStore = (*assetrepo.AssetRegistrationStore)(nil)
